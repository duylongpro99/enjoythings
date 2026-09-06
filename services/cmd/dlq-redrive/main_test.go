package main

import (
	"strings"
	"testing"
	"time"
)

func TestParseOptionsDerivesTheDeadLetterTopicFromTheSource(t *testing.T) {
	opts, err := parseOptions([]string{"list", "--topic", "payment.completed", "--brokers", "a:9092, b:9092", "--max", "3", "--wait", "2s"})
	if err != nil {
		t.Fatalf("parseOptions: %v", err)
	}
	if opts.action != actionList || opts.topic != "payment.completed" || opts.max != 3 || opts.wait != 2*time.Second {
		t.Fatalf("opts = %+v", opts)
	}
	if strings.Join(opts.brokers, ",") != "a:9092,b:9092" {
		t.Fatalf("brokers = %v, want the trimmed list", opts.brokers)
	}
	if opts.group != defaultGroup {
		t.Fatalf("group = %q, want %q", opts.group, defaultGroup)
	}
}

func TestParseOptionsRejectsMisuse(t *testing.T) {
	for name, args := range map[string][]string{
		"no action":              {},
		"unknown action":         {"peek", "--topic", "tx.failed"},
		"missing topic":          {"list"},
		"dlq topic given":        {"list", "--topic", "tx.failed.dlq"},
		"negative max":           {"discard", "--topic", "tx.failed", "--max", "-1"},
		"value-file on list":     {"list", "--topic", "tx.failed", "--value-file", "fix.json"},
		"value-file without max": {"redrive", "--topic", "tx.failed", "--value-file", "fix.json"},
		"value-file with max 2":  {"redrive", "--topic", "tx.failed", "--max", "2", "--value-file", "fix.json"},
		"empty brokers":          {"list", "--topic", "tx.failed", "--brokers", " , "},
	} {
		if _, err := parseOptions(args); err == nil {
			t.Fatalf("%s: parseOptions accepted %v", name, args)
		}
	}
	if _, err := parseOptions([]string{"redrive", "--topic", "tx.failed", "--max", "1", "--value-file", "fix.json"}); err != nil {
		t.Fatalf("redrive --max 1 --value-file rejected: %v", err)
	}
}

func TestPreviewShowsTextAndHexesBinary(t *testing.T) {
	if got := preview([]byte("{\"a\":1}\n")); got != `{"a":1}\n` {
		t.Fatalf("text preview = %q", got)
	}
	if got := preview([]byte{0xff, 0xfe}); got != "hex:fffe" {
		t.Fatalf("binary preview = %q", got)
	}
	long := preview([]byte(strings.Repeat("x", previewBytes+5)))
	if !strings.HasSuffix(long, "…") || len(long) != previewBytes+len("…") {
		t.Fatalf("long preview = %q, want %d chars plus an ellipsis", long, previewBytes)
	}
}
