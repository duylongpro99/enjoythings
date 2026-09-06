// Command dlq-redrive lets an operator work through a dead-letter topic.
//
// Consumers park records they cannot decode on <topic>.dlq with everything
// needed to replay them, and nothing else reads those topics. This tool reads
// them through a consumer group of its own, so "pending" means "not yet
// decided": list shows what is waiting without deciding anything, redrive puts
// a record back on its source topic, and discard drops it. A decision commits
// the dead-letter offset only after it has taken effect, so a failed replay is
// seen again on the next run instead of being lost twice.
//
// Records are decided in order. To correct one record's bytes before replaying
// it, redrive with --max 1 and --value-file.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"enjoythings/services/internal/deadletter"

	"github.com/twmb/franz-go/pkg/kgo"
)

const (
	actionList    = "list"
	actionRedrive = "redrive"
	actionDiscard = "discard"

	defaultBrokers = "127.0.0.1:9092"
	defaultGroup   = "dlq-redrive"
	defaultWait    = 10 * time.Second
	previewBytes   = 200
)

type options struct {
	action    string
	topic     string
	brokers   []string
	group     string
	max       int
	wait      time.Duration
	valueFile string
}

func main() {
	opts, err := parseOptions(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "dlq-redrive:", err)
		usage()
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, opts); err != nil {
		fmt.Fprintln(os.Stderr, "dlq-redrive:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `usage: dlq-redrive <%s|%s|%s> --topic <source topic> [flags]

  --topic       source topic whose dead letters to read (its .dlq topic is derived)
  --brokers     comma-separated Kafka brokers (default $KAFKA_BROKERS or %s)
  --group       consumer group that remembers which dead letters were decided (default %s)
  --max         stop after this many records; 0 means every pending record
  --value-file  replacement value for the one record being redriven; requires --max 1
  --wait        how long to wait for further records before stopping (default %s)
`, actionList, actionRedrive, actionDiscard, defaultBrokers, defaultGroup, defaultWait)
}

func parseOptions(args []string) (options, error) {
	if len(args) == 0 {
		return options{}, errors.New("an action is required")
	}
	opts := options{action: args[0]}
	switch opts.action {
	case actionList, actionRedrive, actionDiscard:
	default:
		return options{}, fmt.Errorf("unknown action %q", opts.action)
	}

	flags := flag.NewFlagSet("dlq-redrive", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	brokers := flags.String("brokers", envOr("KAFKA_BROKERS", defaultBrokers), "")
	flags.StringVar(&opts.topic, "topic", "", "")
	flags.StringVar(&opts.group, "group", defaultGroup, "")
	flags.IntVar(&opts.max, "max", 0, "")
	flags.DurationVar(&opts.wait, "wait", defaultWait, "")
	flags.StringVar(&opts.valueFile, "value-file", "", "")
	if err := flags.Parse(args[1:]); err != nil {
		return options{}, err
	}

	opts.topic = strings.TrimSpace(opts.topic)
	if opts.topic == "" {
		return options{}, errors.New("--topic is required")
	}
	if strings.HasSuffix(opts.topic, deadletter.Suffix) {
		return options{}, fmt.Errorf("--topic names the source topic, not %s", opts.topic)
	}
	if opts.max < 0 {
		return options{}, errors.New("--max must not be negative")
	}
	if opts.wait <= 0 {
		return options{}, errors.New("--wait must be positive")
	}
	if opts.valueFile != "" && (opts.action != actionRedrive || opts.max != 1) {
		return options{}, errors.New("--value-file applies to exactly one record: use it with redrive --max 1")
	}
	for _, broker := range strings.Split(*brokers, ",") {
		if broker = strings.TrimSpace(broker); broker != "" {
			opts.brokers = append(opts.brokers, broker)
		}
	}
	if len(opts.brokers) == 0 {
		return options{}, errors.New("--brokers must name at least one broker")
	}
	return opts, nil
}

func run(ctx context.Context, opts options) error {
	var override []byte
	if opts.valueFile != "" {
		value, err := os.ReadFile(opts.valueFile)
		if err != nil {
			return fmt.Errorf("read --value-file: %w", err)
		}
		override = value
	}

	dlqTopic := deadletter.TopicFor(opts.topic)
	client, err := kgo.NewClient(
		kgo.SeedBrokers(opts.brokers...),
		kgo.ConsumerGroup(opts.group),
		kgo.ConsumeTopics(dlqTopic),
		kgo.DisableAutoCommit(),
	)
	if err != nil {
		return err
	}
	defer client.Close()

	fmt.Printf("%s %s (group %s)\n", opts.action, dlqTopic, opts.group)
	seen := 0
	for opts.max == 0 || seen < opts.max {
		records, err := pollOnce(ctx, client, opts.wait)
		if err != nil {
			return err
		}
		if len(records) == 0 {
			break
		}
		for _, parked := range records {
			if opts.max != 0 && seen >= opts.max {
				break
			}
			if err := decide(ctx, client, opts.action, parked, override); err != nil {
				return err
			}
			seen++
		}
	}
	fmt.Printf("%d record(s) %s\n", seen, pastTense(opts.action))
	return nil
}

// pollOnce fetches the next batch, treating a quiet wait as the end of the
// pending records. Group membership can take a few seconds to settle, so the
// wait is also the time allowed for the first fetch.
func pollOnce(ctx context.Context, client *kgo.Client, wait time.Duration) ([]*kgo.Record, error) {
	pollCtx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	fetches := client.PollFetches(pollCtx)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	for _, err := range fetches.Errors() {
		if errors.Is(err.Err, context.DeadlineExceeded) || errors.Is(err.Err, context.Canceled) {
			continue
		}
		return nil, fmt.Errorf("fetch %s/%d: %w", err.Topic, err.Partition, err.Err)
	}
	return fetches.Records(), nil
}

func decide(ctx context.Context, client *kgo.Client, action string, parked *kgo.Record, override []byte) error {
	payload, decodeErr := deadletter.Decode(parked.Value)
	describe(parked, payload, decodeErr)

	switch action {
	case actionList:
		return nil
	case actionDiscard:
		return client.CommitRecords(ctx, parked)
	case actionRedrive:
		if decodeErr != nil {
			return fmt.Errorf("cannot redrive %s/%d/%d: %w (discard it, or fix the payload by hand)", parked.Topic, parked.Partition, parked.Offset, decodeErr)
		}
		if err := client.ProduceSync(ctx, deadletter.Replay(parked, payload, override)).FirstErr(); err != nil {
			return fmt.Errorf("produce to %s: %w", payload.Topic, err)
		}
		return client.CommitRecords(ctx, parked)
	default:
		return fmt.Errorf("unknown action %q", action)
	}
}

func describe(parked *kgo.Record, payload deadletter.Payload, decodeErr error) {
	fmt.Printf("\n%s partition=%d offset=%d\n", parked.Topic, parked.Partition, parked.Offset)
	if decodeErr != nil {
		fmt.Printf("  undecodable dead-letter payload: %v\n", decodeErr)
		return
	}
	fmt.Printf("  source     %s partition=%d offset=%d\n", payload.Topic, payload.Partition, payload.Offset)
	fmt.Printf("  failed_at  %s\n", payload.FailedAt.Format(time.RFC3339))
	fmt.Printf("  error      %s\n", payload.Error)
	if len(payload.Key) > 0 {
		fmt.Printf("  key        %s\n", preview(payload.Key))
	}
	fmt.Printf("  value      %s\n", preview(payload.Value))
}

// preview renders bytes for a terminal: text as text, anything else as hex,
// both truncated so one poison record cannot flood the screen.
func preview(value []byte) string {
	truncated := len(value) > previewBytes
	if truncated {
		value = value[:previewBytes]
	}
	text := ""
	if utf8.Valid(value) {
		text = strings.ReplaceAll(string(value), "\n", `\n`)
	} else {
		text = fmt.Sprintf("hex:%x", value)
	}
	if truncated {
		text += "…"
	}
	return text
}

func pastTense(action string) string {
	switch action {
	case actionRedrive:
		return "redriven"
	case actionDiscard:
		return "discarded"
	default:
		return "listed"
	}
}

func envOr(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok && value != "" {
		return value
	}
	return fallback
}
