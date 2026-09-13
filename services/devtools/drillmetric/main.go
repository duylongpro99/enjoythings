// Command drillmetric is the black-box assertion behind drill scenario probes
// whose symptom shows only in a service's own Prometheus metrics, not in a saga
// state — e.g. the fraud worker falling open when its model provider degrades.
//
// It reads a Prometheus-text metrics endpoint, sums a named counter (optionally
// filtered to one label), and asserts the sum grows by at least -min-delta
// within -within. Exit 0 means the assertion held.
//
//	drillmetric -url http://localhost:9101/metrics \
//	  -metric fraud_transactions_scored_total -match action=fail_open \
//	  -min-delta 1 -within 60s        # symptom present: fail-open scoring climbing
//	drillmetric ... -match action=allow -min-delta 5 -within 90s   # symptom gone
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"enjoythings/services/devtools/smoke"
)

const defaultMetricsURL = "http://localhost:9101/metrics"

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "drillmetric: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("drillmetric: ok")
}

func run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("drillmetric", flag.ContinueOnError)
	url := fs.String("url", smoke.GetenvDefault("DRILL_METRICS_URL", defaultMetricsURL), "Prometheus-text metrics endpoint")
	metric := fs.String("metric", "", "counter metric name to sum")
	match := fs.String("match", "", "optional single label filter, label=value")
	minDelta := fs.Float64("min-delta", 1, "required increase over the window")
	within := fs.Duration("within", 60*time.Second, "assert the delta is reached within this window")
	interval := fs.Duration("interval", 2*time.Second, "poll interval")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *metric == "" {
		return errors.New("-metric is required")
	}
	label, value, err := parseMatch(*match)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, *within+30*time.Second)
	defer cancel()

	base, err := scrapeSum(ctx, *url, *metric, label, value)
	if err != nil {
		return err
	}

	deadline := time.Now().Add(*within)
	for {
		cur, err := scrapeSum(ctx, *url, *metric, label, value)
		if err != nil {
			return err
		}
		if cur-base >= *minDelta {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s%s grew by %.3f over %s, want >= %.3f",
				*metric, matchSuffix(label, value), cur-base, *within, *minDelta)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(*interval):
		}
	}
}

// scrapeSum fetches the endpoint and returns the summed value of the metric.
func scrapeSum(ctx context.Context, url, metric, label, value string) (float64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("scrape %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("scrape %s: status %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}
	return sumMetric(string(body), metric, label, value)
}

// sumMetric sums every series of metric in Prometheus text form, optionally
// restricted to series carrying label="value".
func sumMetric(text, metric, label, value string) (float64, error) {
	var total float64
	found := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, labels, v, ok := parseSample(line)
		if !ok || name != metric {
			continue
		}
		if label != "" && labels[label] != value {
			continue
		}
		total += v
		found = true
	}
	if !found {
		return 0, nil
	}
	return total, nil
}

// parseSample splits one Prometheus text line into name, labels, and value.
func parseSample(line string) (name string, labels map[string]string, value float64, ok bool) {
	labels = map[string]string{}
	head := line
	if i := strings.IndexByte(line, '{'); i >= 0 {
		j := strings.LastIndexByte(line, '}')
		if j < i {
			return "", nil, 0, false
		}
		name = line[:i]
		labels = parseLabels(line[i+1 : j])
		head = line[j+1:]
	} else {
		if sp := strings.IndexAny(line, " \t"); sp >= 0 {
			name = line[:sp]
			head = line[sp:]
		} else {
			return "", nil, 0, false
		}
	}
	fields := strings.Fields(head)
	if len(fields) == 0 {
		return "", nil, 0, false
	}
	v, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return "", nil, 0, false
	}
	return name, labels, v, true
}

func parseLabels(block string) map[string]string {
	labels := map[string]string{}
	for _, pair := range splitLabels(block) {
		eq := strings.IndexByte(pair, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(pair[:eq])
		val := strings.TrimSpace(pair[eq+1:])
		val = strings.TrimSuffix(strings.TrimPrefix(val, `"`), `"`)
		labels[key] = val
	}
	return labels
}

// splitLabels splits a label block on commas that are not inside a quoted value.
func splitLabels(block string) []string {
	var out []string
	var cur strings.Builder
	inQuote := false
	for i := 0; i < len(block); i++ {
		c := block[i]
		switch {
		case c == '"':
			inQuote = !inQuote
			cur.WriteByte(c)
		case c == ',' && !inQuote:
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func parseMatch(match string) (label, value string, err error) {
	if match == "" {
		return "", "", nil
	}
	eq := strings.IndexByte(match, '=')
	if eq <= 0 {
		return "", "", fmt.Errorf("-match must be label=value, got %q", match)
	}
	return match[:eq], match[eq+1:], nil
}

func matchSuffix(label, value string) string {
	if label == "" {
		return ""
	}
	return fmt.Sprintf("{%s=%q}", label, value)
}
