// Package loadgen drives constant-rate transfer traffic through the gateway and
// exports what it observes to Prometheus, so a drill has a latency and error
// signal on an otherwise idle stack.
package loadgen

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"enjoythings/services/devtools/smoke"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Terminal saga states the generator waits for.
const (
	stateCompleted = "COMPLETED"
	stateFailed    = "FAILED"
)

// Gateway is the slice of the smoke client the generator needs, so tests can
// substitute a fake.
type Gateway interface {
	StartPayment(ctx context.Context, from, to uuid.UUID, amountCents int64, wantStatus int) (smoke.Payment, error)
	GetPayment(ctx context.Context, paymentID string) (smoke.Payment, error)
}

// Account is one funded wallet pair owned by one verified user.
type Account struct {
	Gateway Gateway
	From    uuid.UUID
	To      uuid.UUID

	polls *bucket // client-side allowance for status polls
}

// Config bounds one generator run.
type Config struct {
	Rate          float64       // transfers per second across all accounts
	AmountCents   int64         // amount per transfer
	SettleTimeout time.Duration // how long to wait for a terminal saga state
	PollInterval  time.Duration // how often to re-read payment status
	// UserBudget is the sustained requests per second the gateway grants one
	// user (its rate limiter refills one token per RATE_LIMIT_REFILL_EVERY).
	// The generator models many customers each inside that allowance: transfers
	// take what they need and status polls spend the remainder, so a growing
	// backlog slows polling instead of producing a 429 storm the engineer
	// would mistake for a symptom.
	UserBudget float64
}

// Validate rejects configurations the ticker cannot honour.
func (config Config) Validate() error {
	if config.Rate <= 0 {
		return errors.New("rate must be positive")
	}
	if config.AmountCents <= 0 {
		return errors.New("amount must be positive")
	}
	if config.SettleTimeout <= 0 {
		return errors.New("settle timeout must be positive")
	}
	if config.PollInterval <= 0 {
		return errors.New("poll interval must be positive")
	}
	if config.UserBudget <= 0 {
		return errors.New("user budget must be positive")
	}
	return nil
}

// pollAllowance is the per-account poll rate left after transfers.
func (config Config) pollAllowance(accounts int) (float64, error) {
	perAccount := config.Rate / float64(accounts)
	remaining := config.UserBudget - perAccount
	if remaining <= 0 {
		return 0, fmt.Errorf("%d accounts cannot sustain %.2f transfers/s inside a per-user budget of %.2f req/s; add accounts", accounts, config.Rate, config.UserBudget)
	}
	return remaining, nil
}

// bucket is a minimal token bucket: refill tokens per second up to burst.
type bucket struct {
	mu     sync.Mutex
	tokens float64
	burst  float64
	rate   float64
	last   time.Time
}

func newBucket(rate, burst float64) *bucket {
	return &bucket{tokens: burst, burst: burst, rate: rate, last: time.Now()}
}

func (b *bucket) take() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	b.tokens = min(b.burst, b.tokens+now.Sub(b.last).Seconds()*b.rate)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// Interval is the gap between transfers at the configured rate.
func (config Config) Interval() time.Duration {
	return time.Duration(float64(time.Second) / config.Rate)
}

// Metrics is what the generator exports. Outcome labels are bounded: requests
// are accepted, rejected, or error; settlements are completed, failed, or
// timeout.
type Metrics struct {
	registry *prometheus.Registry
	requests *prometheus.CounterVec
	latency  prometheus.Histogram
	settle   *prometheus.HistogramVec
	inflight prometheus.Gauge
}

// NewMetrics registers the generator's collectors on a private registry.
func NewMetrics() *Metrics {
	metrics := &Metrics{
		registry: prometheus.NewRegistry(),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "loadgen_requests_total", Help: "Transfer requests by gateway outcome."}, []string{"outcome"}),
		latency:  prometheus.NewHistogram(prometheus.HistogramOpts{Name: "loadgen_request_duration_seconds", Help: "Gateway round trip for POST /v1/transfers.", Buckets: prometheus.DefBuckets}),
		settle:   prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "loadgen_payment_settle_seconds", Help: "Time from accepted transfer to a terminal saga state.", Buckets: []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60}}, []string{"outcome"}),
		inflight: prometheus.NewGauge(prometheus.GaugeOpts{Name: "loadgen_inflight_payments", Help: "Accepted transfers not yet in a terminal state."}),
	}
	metrics.registry.MustRegister(metrics.requests, metrics.latency, metrics.settle, metrics.inflight)
	return metrics
}

// Handler serves the private registry.
func (metrics *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(metrics.registry, promhttp.HandlerOpts{})
}

// Gatherer exposes the registry for tests.
func (metrics *Metrics) Gatherer() prometheus.Gatherer {
	return metrics.registry
}

// Runner issues transfers round-robin over the accounts at a fixed rate.
type Runner struct {
	config   Config
	accounts []Account
	metrics  *Metrics
	next     int
	wg       sync.WaitGroup
}

// NewRunner validates the configuration and binds the accounts.
func NewRunner(config Config, accounts []Account, metrics *Metrics) (*Runner, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if len(accounts) == 0 {
		return nil, errors.New("at least one account is required")
	}
	allowance, err := config.pollAllowance(len(accounts))
	if err != nil {
		return nil, err
	}
	for i := range accounts {
		// A small burst lets a healthy payment be polled promptly without
		// leaning on the gateway's own burst allowance.
		accounts[i].polls = newBucket(allowance, 10)
	}
	return &Runner{config: config, accounts: accounts, metrics: metrics}, nil
}

// Run ticks until ctx is cancelled, then waits for in-flight settlements.
func (runner *Runner) Run(ctx context.Context) {
	ticker := time.NewTicker(runner.config.Interval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			runner.wg.Wait()
			return
		case <-ticker.C:
			account := runner.accounts[runner.next%len(runner.accounts)]
			runner.next++
			runner.wg.Add(1)
			go func() {
				defer runner.wg.Done()
				runner.transfer(ctx, account)
			}()
		}
	}
}

func (runner *Runner) transfer(ctx context.Context, account Account) {
	started := time.Now()
	payment, err := account.Gateway.StartPayment(ctx, account.From, account.To, runner.config.AmountCents, http.StatusAccepted)
	runner.metrics.latency.Observe(time.Since(started).Seconds())
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		runner.metrics.requests.WithLabelValues(classifyRequestError(err)).Inc()
		return
	}
	runner.metrics.requests.WithLabelValues("accepted").Inc()
	runner.metrics.inflight.Inc()
	defer runner.metrics.inflight.Dec()

	// Settlement outlives a cancelled run so the last transfers are still scored.
	settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), runner.config.SettleTimeout)
	defer cancel()
	outcome := runner.awaitSettlement(settleCtx, account, payment.PaymentID)
	runner.metrics.settle.WithLabelValues(outcome).Observe(time.Since(started).Seconds())
}

func (runner *Runner) awaitSettlement(ctx context.Context, account Account, paymentID string) string {
	ticker := time.NewTicker(runner.config.PollInterval)
	defer ticker.Stop()
	for {
		if account.polls.take() {
			payment, err := account.Gateway.GetPayment(ctx, paymentID)
			if err == nil {
				switch payment.Status {
				case stateCompleted:
					return "completed"
				case stateFailed:
					return "failed"
				}
			}
		}
		select {
		case <-ctx.Done():
			return "timeout"
		case <-ticker.C:
		}
	}
}

// classifyRequestError separates a gateway that answered with an unexpected
// status from one that did not answer at all. The smoke client formats the
// former as "... status=NNN want=202 ...".
func classifyRequestError(err error) string {
	if strings.Contains(err.Error(), " status=") {
		return "rejected"
	}
	return "error"
}
