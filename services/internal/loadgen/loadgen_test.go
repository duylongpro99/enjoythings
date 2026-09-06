package loadgen

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"enjoythings/services/devtools/smoke"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type fakeGateway struct {
	mu       sync.Mutex
	statuses []string // successive GetPayment answers; the last one repeats
	startErr error
}

func (gateway *fakeGateway) StartPayment(context.Context, uuid.UUID, uuid.UUID, int64, int) (smoke.Payment, error) {
	if gateway.startErr != nil {
		return smoke.Payment{}, gateway.startErr
	}
	return smoke.Payment{PaymentID: uuid.NewString(), Status: "STARTED"}, nil
}

func (gateway *fakeGateway) GetPayment(_ context.Context, id string) (smoke.Payment, error) {
	gateway.mu.Lock()
	defer gateway.mu.Unlock()
	status := gateway.statuses[0]
	if len(gateway.statuses) > 1 {
		gateway.statuses = gateway.statuses[1:]
	}
	return smoke.Payment{PaymentID: id, Status: status}, nil
}

func testConfig(rate float64) Config {
	// A generous budget so the poll allowance never gates the fast tests below.
	return Config{Rate: rate, AmountCents: 100, SettleTimeout: 200 * time.Millisecond, PollInterval: 5 * time.Millisecond, UserBudget: 10_000}
}

func TestPollAllowanceRequiresHeadroom(t *testing.T) {
	config := Config{Rate: 5, AmountCents: 1, SettleTimeout: time.Second, PollInterval: time.Millisecond, UserBudget: 1}
	if _, err := NewRunner(config, []Account{{}, {}, {}, {}}, NewMetrics()); err == nil {
		t.Fatal("4 accounts at 5/s exceed a 1 req/s per-user budget; expected an error")
	}
	allowance, err := config.pollAllowance(20)
	if err != nil {
		t.Fatal(err)
	}
	if allowance != 0.75 {
		t.Fatalf("allowance = %v, want 0.75 (1 - 5/20)", allowance)
	}
}

func TestPollsStayInsideBudget(t *testing.T) {
	// One account whose payments never settle, with almost no poll allowance
	// beyond the burst: the bucket must cap polling at its 10 burst tokens
	// instead of letting the backlog turn into a poll storm.
	gateway := &countingGateway{}
	metrics := NewMetrics()
	config := testConfig(200)
	config.UserBudget = 200.001 // allowance ≈ 0.001 polls/s beyond the burst
	config.SettleTimeout = 30 * time.Millisecond
	runner, err := NewRunner(config, []Account{{Gateway: gateway}}, metrics)
	if err != nil {
		t.Fatal(err)
	}
	runFor(runner, 60*time.Millisecond)
	if polls := gateway.polls.Load(); polls > 10 {
		t.Fatalf("polls = %d, want at most the burst of 10", polls)
	}
}

type countingGateway struct {
	polls atomic.Int64
}

func (gateway *countingGateway) StartPayment(context.Context, uuid.UUID, uuid.UUID, int64, int) (smoke.Payment, error) {
	return smoke.Payment{PaymentID: uuid.NewString(), Status: "STARTED"}, nil
}

func (gateway *countingGateway) GetPayment(_ context.Context, id string) (smoke.Payment, error) {
	gateway.polls.Add(1)
	return smoke.Payment{PaymentID: id, Status: "PAYMENT_PROCESSING"}, nil
}

func runFor(runner *Runner, duration time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()
	runner.Run(ctx)
}

func TestConfigValidateAndInterval(t *testing.T) {
	if err := testConfig(4).Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	if got := testConfig(4).Interval(); got != 250*time.Millisecond {
		t.Fatalf("interval = %s, want 250ms", got)
	}
	for name, config := range map[string]Config{
		"zero rate":    {Rate: 0, AmountCents: 1, SettleTimeout: time.Second, PollInterval: time.Millisecond},
		"zero amount":  {Rate: 1, AmountCents: 0, SettleTimeout: time.Second, PollInterval: time.Millisecond},
		"zero settle":  {Rate: 1, AmountCents: 1, SettleTimeout: 0, PollInterval: time.Millisecond},
		"zero polling": {Rate: 1, AmountCents: 1, SettleTimeout: time.Second, PollInterval: 0, UserBudget: 1},
		"zero budget":  {Rate: 1, AmountCents: 1, SettleTimeout: time.Second, PollInterval: time.Millisecond},
	} {
		if err := config.Validate(); err == nil {
			t.Errorf("%s: expected validation error", name)
		}
	}
}

func TestNewRunnerRequiresAccounts(t *testing.T) {
	if _, err := NewRunner(testConfig(1), nil, NewMetrics()); err == nil {
		t.Fatal("expected error for empty accounts")
	}
}

func TestRunRecordsAcceptedAndCompleted(t *testing.T) {
	gateway := &fakeGateway{statuses: []string{"PAYMENT_PROCESSING", "COMPLETED"}}
	metrics := NewMetrics()
	runner, err := NewRunner(testConfig(200), []Account{{Gateway: gateway, From: uuid.New(), To: uuid.New()}}, metrics)
	if err != nil {
		t.Fatal(err)
	}
	runFor(runner, 60*time.Millisecond)

	if accepted := testutil.ToFloat64(metrics.requests.WithLabelValues("accepted")); accepted < 3 {
		t.Fatalf("accepted = %v, want at least 3 transfers in 60ms at 200/s", accepted)
	}
	if got := testutil.CollectAndCount(metrics.settle); got != 1 {
		t.Fatalf("settle series = %d, want 1 (completed only)", got)
	}
	if got := testutil.ToFloat64(metrics.inflight); got != 0 {
		t.Fatalf("inflight after Run = %v, want 0", got)
	}
}

func TestRunClassifiesRejectedAndError(t *testing.T) {
	rejected := &fakeGateway{startErr: errors.New("POST /v1/transfers status=422 want=202 body={}")}
	failing := &fakeGateway{startErr: errors.New("dial tcp: connection refused")}
	metrics := NewMetrics()
	runner, err := NewRunner(testConfig(200), []Account{{Gateway: rejected}, {Gateway: failing}}, metrics)
	if err != nil {
		t.Fatal(err)
	}
	runFor(runner, 60*time.Millisecond)

	if got := testutil.ToFloat64(metrics.requests.WithLabelValues("rejected")); got < 1 {
		t.Fatalf("rejected = %v, want at least 1", got)
	}
	if got := testutil.ToFloat64(metrics.requests.WithLabelValues("error")); got < 1 {
		t.Fatalf("error = %v, want at least 1", got)
	}
	if got := testutil.ToFloat64(metrics.requests.WithLabelValues("accepted")); got != 0 {
		t.Fatalf("accepted = %v, want 0", got)
	}
}

func TestSettlementTimesOut(t *testing.T) {
	gateway := &fakeGateway{statuses: []string{"PAYMENT_PROCESSING"}}
	metrics := NewMetrics()
	config := testConfig(200)
	config.SettleTimeout = 20 * time.Millisecond
	runner, err := NewRunner(config, []Account{{Gateway: gateway}}, metrics)
	if err != nil {
		t.Fatal(err)
	}
	runFor(runner, 30*time.Millisecond)

	count, err := testutil.GatherAndCount(metrics.Gatherer(), "loadgen_payment_settle_seconds")
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("settle series = %d, want exactly the timeout series", count)
	}
}

func TestMetricsHandlerServes(t *testing.T) {
	metrics := NewMetrics()
	metrics.requests.WithLabelValues("accepted").Inc()
	recorder := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	if body := recorder.Body.String(); !strings.Contains(body, "loadgen_requests_total") {
		t.Fatalf("metrics body missing loadgen_requests_total:\n%s", body)
	}
}
