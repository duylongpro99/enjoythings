package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
)

const sample = `# HELP fraud_transactions_scored_total Fraud scoring outcomes.
# TYPE fraud_transactions_scored_total counter
fraud_transactions_scored_total{action="allow",provider="chaos"} 12
fraud_transactions_scored_total{action="fail_open",provider="chaos"} 3
fraud_transactions_scored_total{action="fail_open",provider="local"} 4
fraud_model_latency_seconds_count{provider="chaos",model="m"} 9
go_goroutines 42
`

func TestSumMetricAllSeries(t *testing.T) {
	got, err := sumMetric(sample, "fraud_transactions_scored_total", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got != 19 {
		t.Fatalf("sum all = %v, want 19", got)
	}
}

func TestSumMetricLabelFilter(t *testing.T) {
	got, err := sumMetric(sample, "fraud_transactions_scored_total", "action", "fail_open")
	if err != nil {
		t.Fatal(err)
	}
	if got != 7 {
		t.Fatalf("sum fail_open = %v, want 7", got)
	}
}

func TestSumMetricUnlabelled(t *testing.T) {
	got, err := sumMetric(sample, "go_goroutines", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got != 42 {
		t.Fatalf("go_goroutines = %v, want 42", got)
	}
}

func TestSumMetricAbsentIsZero(t *testing.T) {
	got, err := sumMetric(sample, "does_not_exist", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got != 0 {
		t.Fatalf("absent = %v, want 0", got)
	}
}

func TestParseMatch(t *testing.T) {
	l, v, err := parseMatch("action=fail_open")
	if err != nil || l != "action" || v != "fail_open" {
		t.Fatalf("parseMatch = %q,%q,%v", l, v, err)
	}
	if _, _, err := parseMatch("bogus"); err == nil {
		t.Fatal("expected error for match without =")
	}
	if l, _, err := parseMatch(""); err != nil || l != "" {
		t.Fatalf("empty match should be no-op, got %q,%v", l, err)
	}
}

// A metrics endpoint whose fail_open count rises after the first scrape; the
// delta assertion should pass once it grows by the required amount.
func TestRunPassesWhenDeltaReached(t *testing.T) {
	var scrapes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := scrapes.Add(1)
		count := 0
		if n >= 2 {
			count = 5
		}
		_, _ = w.Write([]byte(
			"fraud_transactions_scored_total{action=\"fail_open\"} " +
				strconv.Itoa(count) + "\n"))
	}))
	defer srv.Close()

	err := run(context.Background(), []string{
		"-url", srv.URL,
		"-metric", "fraud_transactions_scored_total",
		"-match", "action=fail_open",
		"-min-delta", "1",
		"-within", "5s",
		"-interval", "10ms",
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
}

func TestRunFailsWhenFlat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("fraud_transactions_scored_total{action=\"fail_open\"} 3\n"))
	}))
	defer srv.Close()

	err := run(context.Background(), []string{
		"-url", srv.URL,
		"-metric", "fraud_transactions_scored_total",
		"-match", "action=fail_open",
		"-min-delta", "1",
		"-within", "300ms",
		"-interval", "10ms",
	})
	if err == nil {
		t.Fatal("expected failure when the counter is flat")
	}
}
