package phase3e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	sagav1 "enjoythings/services/gen/saga/v1"
	"enjoythings/services/internal/auth"
	"enjoythings/services/internal/event"
	gatewayhandler "enjoythings/services/internal/gateway/handler"
	"enjoythings/services/internal/notification"
	"enjoythings/services/internal/paymentprocessor"
	"enjoythings/services/internal/saga"
	"enjoythings/services/internal/sagagrpc"
	"enjoythings/services/internal/verification"

	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kgo"
)

type railMode int

const (
	railSuccess railMode = iota
	railTerminalFailure
	railRetryOnce
)

var acceptanceTime = time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)

type harness struct {
	ctx               context.Context
	userID            string
	fromWalletID      string
	toWalletID        string
	sagaStore         *sagaMemoryStore
	verificationStore *verificationMemoryStore
	paymentStore      *paymentMemoryStore
	bus               *eventBus
	wallet            *walletBoundary
	ledger            *ledgerBoundary
	rail              *railBoundary
	orchestrator      *saga.Orchestrator
	processor         *paymentprocessor.Processor
	verification      *verification.Service
	notification      *notification.Consumer
	email             *notificationAdapter
}

func newHarness(t *testing.T, mode railMode) *harness {
	t.Helper()
	bus := &eventBus{}
	wallet := &walletBoundary{balance: 5000, debits: map[string]string{}, compensated: map[string]bool{}}
	ledger := &ledgerBoundary{status: map[string]string{}, reservations: map[string]string{}, transfers: map[string]string{}}
	sagaStore := &sagaMemoryStore{items: map[string]saga.Saga{}}
	paymentStore := &paymentMemoryStore{items: map[string]paymentprocessor.Attempt{}}
	verificationStore := &verificationMemoryStore{records: map[string]verification.Record{}, keys: map[string]string{}}
	rail := &railBoundary{mode: mode}
	email := &notificationAdapter{}
	dispatcher := notification.NewDispatcher(email, nil, slog.New(slog.DiscardHandler))
	h := &harness{
		ctx:               context.Background(),
		userID:            "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		fromWalletID:      "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
		toWalletID:        "cccccccc-cccc-cccc-cccc-cccccccccccc",
		sagaStore:         sagaStore,
		verificationStore: verificationStore,
		paymentStore:      paymentStore,
		bus:               bus,
		wallet:            wallet,
		ledger:            ledger,
		rail:              rail,
		verification:      verification.NewService(verificationStore, bus, verification.Config{Mode: verification.ModeAuto}, fixedClock{}),
		processor: paymentprocessor.NewProcessor(paymentStore, rail, bus, paymentprocessor.ProcessorConfig{
			Backoffs: []time.Duration{time.Millisecond, 2 * time.Millisecond},
			Jitter:   func(duration time.Duration) time.Duration { return duration },
			Sleeper:  noSleep{},
			Clock:    fixedClock{},
		}),
		notification: notification.NewConsumer(dispatcher, nil, slog.New(slog.DiscardHandler)),
		email:        email,
	}
	h.newOrchestrator()
	return h
}

func (h *harness) newOrchestrator() {
	h.orchestrator = saga.NewOrchestrator(h.sagaStore, verificationBoundary{service: h.verification}, h.wallet, h.ledger, fixedClock{})
	h.orchestrator.SetOutbox(h.bus)
}

func (h *harness) verifyUser(t *testing.T) verification.Record {
	t.Helper()
	record, err := h.verification.Submit(h.ctx, verification.SubmitCommand{
		UserID:         h.userID,
		IdempotencyKey: "verify-" + h.userID,
		TraceID:        "trace-verification",
	})
	if err != nil {
		t.Fatalf("verification boundary: Submit: %v", err)
	}
	return record
}

func (h *harness) startPayment(t *testing.T, paymentID string) saga.Saga {
	t.Helper()
	started, err := h.orchestrator.StartPaymentSaga(h.ctx, h.startRequest(paymentID))
	if err != nil {
		t.Fatalf("saga boundary: StartPaymentSaga: %v", err)
	}
	return started
}

func (h *harness) startPaymentError(paymentID string) error {
	_, err := h.orchestrator.StartPaymentSaga(h.ctx, h.startRequest(paymentID))
	return err
}

func (h *harness) startRequest(paymentID string) saga.StartRequest {
	return saga.StartRequest{
		PaymentID:      paymentID,
		IdempotencyKey: "start-" + paymentID,
		TraceID:        "trace-" + paymentID,
		UserID:         h.userID,
		FromWalletID:   h.fromWalletID,
		ToWalletID:     h.toWalletID,
		AmountCents:    1250,
		Currency:       "USD",
	}
}

func (h *harness) drainEvents(t *testing.T) {
	t.Helper()
	for {
		event, ok := h.bus.pop("")
		if !ok {
			return
		}
		h.deliver(t, event)
	}
}

func (h *harness) deliverNextTopic(t *testing.T, topic string) {
	t.Helper()
	event, ok := h.bus.pop(topic)
	if !ok {
		t.Fatalf("%s boundary: no queued event", topic)
	}
	h.deliver(t, event)
}

func (h *harness) deliverNextTopicTwice(t *testing.T, topic string) {
	t.Helper()
	event, ok := h.bus.pop(topic)
	if !ok {
		t.Fatalf("%s boundary: no queued event", topic)
	}
	h.deliver(t, event)
	h.deliver(t, event)
}

func (h *harness) deliver(t *testing.T, recorded recordedEvent) {
	t.Helper()
	var err error
	switch recorded.topic {
	case saga.TopicPaymentExecute:
		var command saga.PaymentExecute
		if unmarshalErr := json.Unmarshal(recorded.payload, &command); unmarshalErr != nil {
			t.Fatalf("payment.execute boundary: unmarshal: %v", unmarshalErr)
		}
		err = h.processor.HandleExecute(h.ctx, command)
	case saga.TopicPaymentCompleted:
		var completed saga.PaymentCompleted
		if unmarshalErr := json.Unmarshal(recorded.payload, &completed); unmarshalErr != nil {
			t.Fatalf("payment.completed boundary: unmarshal: %v", unmarshalErr)
		}
		err = h.orchestrator.HandlePaymentCompleted(h.ctx, completed)
	case saga.TopicPaymentFailed:
		var failed saga.PaymentFailed
		if unmarshalErr := json.Unmarshal(recorded.payload, &failed); unmarshalErr != nil {
			t.Fatalf("payment.failed boundary: unmarshal: %v", unmarshalErr)
		}
		err = h.orchestrator.HandlePaymentFailed(h.ctx, failed)
	case event.FraudScoreRequestedTopic:
		var requested event.FraudScoreRequested
		if unmarshalErr := json.Unmarshal(recorded.payload, &requested); unmarshalErr != nil {
			t.Fatalf("fraud.score.requested boundary: unmarshal: %v", unmarshalErr)
		}
		if validateErr := requested.Validate(); validateErr != nil {
			t.Fatalf("fraud.score.requested boundary: validate: %v", validateErr)
		}
		return
	case notification.TopicTxCompleted, notification.TopicTxFailed, notification.TopicUserVerified, notification.TopicUserRejected:
		err = h.notification.HandleRecord(h.ctx, &kgo.Record{Topic: recorded.topic, Value: recorded.payload})
	default:
		t.Fatalf("event bus boundary: unsupported topic %q", recorded.topic)
	}
	if err != nil {
		t.Fatalf("%s boundary: deliver: %v", recorded.topic, err)
	}
}

func (h *harness) failNextLedgerReserve() {
	h.ledger.failReserve = true
}

func (h *harness) restartOrchestrator(t *testing.T) {
	t.Helper()
	h.newOrchestrator()
	if err := h.orchestrator.ResumeNonTerminal(h.ctx); err != nil {
		t.Fatalf("saga restart boundary: ResumeNonTerminal: %v", err)
	}
}

func (h *harness) requireSagaState(t *testing.T, paymentID, want string) {
	t.Helper()
	current, err := h.sagaStore.GetByPaymentID(h.ctx, paymentID)
	if err != nil {
		t.Fatalf("saga store boundary: GetByPaymentID: %v", err)
	}
	if current.State != want {
		t.Fatalf("saga state = %s, want %s", current.State, want)
	}
}

func (h *harness) requireWalletBalance(t *testing.T, want int64) {
	t.Helper()
	if h.wallet.balance != want {
		t.Fatalf("wallet boundary: balance = %d, want %d", h.wallet.balance, want)
	}
}

func (h *harness) requireWalletDebitCalls(t *testing.T, want int) {
	t.Helper()
	if h.wallet.debitCalls != want {
		t.Fatalf("wallet boundary: debit calls = %d, want %d", h.wallet.debitCalls, want)
	}
}

func (h *harness) requireLedgerStatus(t *testing.T, want string) {
	t.Helper()
	if h.ledger.status[h.lastPaymentID()] != want {
		t.Fatalf("ledger boundary: status = %s, want %s", h.ledger.status[h.lastPaymentID()], want)
	}
}

func (h *harness) requireLedgerConfirmCalls(t *testing.T, want int) {
	t.Helper()
	if h.ledger.confirmCalls != want {
		t.Fatalf("ledger boundary: confirm calls = %d, want %d", h.ledger.confirmCalls, want)
	}
}

func (h *harness) requireRailCalls(t *testing.T, want int) {
	t.Helper()
	if h.rail.calls != want {
		t.Fatalf("payment rail boundary: calls = %d, want %d", h.rail.calls, want)
	}
}

func (h *harness) requirePaymentAttempt(t *testing.T, status string, attempts int) {
	t.Helper()
	attempt, err := h.paymentStore.GetByPaymentID(h.ctx, h.lastPaymentID())
	if err != nil {
		t.Fatalf("payment store boundary: GetByPaymentID: %v", err)
	}
	if attempt.Status != status || attempt.AttemptCount != attempts {
		t.Fatalf("payment attempt = %+v, want status %s attempts %d", attempt, status, attempts)
	}
}

func (h *harness) requirePublished(t *testing.T, topic string) {
	t.Helper()
	for _, event := range h.bus.published {
		if event.topic == topic {
			return
		}
	}
	t.Fatalf("event bus boundary: topic %s was not published", topic)
}

func (h *harness) requireNotification(t *testing.T, subject string) {
	t.Helper()
	for _, message := range h.email.messages {
		if message.Subject == subject {
			return
		}
	}
	t.Fatalf("notification boundary: subject %q was not dispatched; messages=%+v", subject, h.email.messages)
}

func (h *harness) requireGatewayUnverifiedStatus(t *testing.T, want int) {
	t.Helper()
	server := sagagrpc.NewServer(h.orchestrator)
	client := directSagaClient{server: server}
	handler := gatewayhandler.NewPayments(client)
	body := `{"payment_id":"99999999-9999-9999-9999-999999999999","from_wallet_id":"` + h.fromWalletID + `","to_wallet_id":"` + h.toWalletID + `","amount_cents":1250,"currency":"USD"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/transfers", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(auth.ContextWithPrincipal(req.Context(), auth.Principal{UserID: uuid.MustParse(h.userID), Role: "user"}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != want {
		t.Fatalf("gateway boundary: status = %d, want %d; body %s", rec.Code, want, rec.Body.String())
	}
}

func (h *harness) lastPaymentID() string {
	h.sagaStore.mu.Lock()
	defer h.sagaStore.mu.Unlock()
	var latest saga.Saga
	for _, current := range h.sagaStore.items {
		if latest.PaymentID == "" || current.CreatedAt.After(latest.CreatedAt) || current.PaymentID > latest.PaymentID {
			latest = current
		}
	}
	return latest.PaymentID
}

type recordedEvent struct {
	topic        string
	partitionKey string
	payload      []byte
}

type eventBus struct {
	mu        sync.Mutex
	queue     []recordedEvent
	published []recordedEvent
}

func (bus *eventBus) Enqueue(_ context.Context, topic, partitionKey string, payload []byte) error {
	bus.mu.Lock()
	defer bus.mu.Unlock()
	event := recordedEvent{topic: topic, partitionKey: partitionKey, payload: append([]byte(nil), payload...)}
	bus.queue = append(bus.queue, event)
	bus.published = append(bus.published, event)
	return nil
}

func (bus *eventBus) pop(topic string) (recordedEvent, bool) {
	bus.mu.Lock()
	defer bus.mu.Unlock()
	for i, event := range bus.queue {
		if topic == "" || event.topic == topic {
			bus.queue = append(bus.queue[:i], bus.queue[i+1:]...)
			return event, true
		}
	}
	return recordedEvent{}, false
}

type sagaMemoryStore struct {
	mu    sync.Mutex
	items map[string]saga.Saga
}

func (store *sagaMemoryStore) Create(_ context.Context, current saga.Saga) (saga.Saga, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if existing, ok := store.items[current.PaymentID]; ok {
		if existing.IdempotencyKey == current.IdempotencyKey && existing.UserID == current.UserID && existing.FromWalletID == current.FromWalletID && existing.ToWalletID == current.ToWalletID && existing.AmountCents == current.AmountCents && existing.Currency == current.Currency {
			return existing, nil
		}
		return saga.Saga{}, saga.ErrAlreadyExists
	}
	current.ID = "saga-" + current.PaymentID
	store.items[current.PaymentID] = current
	return current, nil
}

func (store *sagaMemoryStore) GetByPaymentID(_ context.Context, paymentID string) (saga.Saga, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	current, ok := store.items[paymentID]
	if !ok {
		return saga.Saga{}, saga.ErrNotFound
	}
	return current, nil
}

func (store *sagaMemoryStore) ListNonTerminal(context.Context) ([]saga.Saga, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	var result []saga.Saga
	for _, current := range store.items {
		if current.State != saga.StateCompleted && current.State != saga.StateFailed {
			result = append(result, current)
		}
	}
	return result, nil
}

func (store *sagaMemoryStore) Update(_ context.Context, current saga.Saga) (saga.Saga, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, ok := store.items[current.PaymentID]; !ok {
		return saga.Saga{}, saga.ErrNotFound
	}
	store.items[current.PaymentID] = current
	return current, nil
}

type verificationBoundary struct {
	service *verification.Service
}

func (boundary verificationBoundary) GetStatus(ctx context.Context, req saga.VerificationRequest) (saga.VerificationResult, error) {
	record, err := boundary.service.GetStatus(ctx, req.UserID)
	if errors.Is(err, verification.ErrNotFound) {
		return saga.VerificationResult{}, saga.ErrVerificationNotFound
	}
	if err != nil {
		return saga.VerificationResult{}, err
	}
	return saga.VerificationResult{Status: record.Status}, nil
}

type verificationMemoryStore struct {
	mu      sync.Mutex
	records map[string]verification.Record
	keys    map[string]string
}

func (store *verificationMemoryStore) Submit(_ context.Context, cmd verification.SubmitCommand, decision verification.Decision, now time.Time) (verification.SubmitResult, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if owner, ok := store.keys[cmd.IdempotencyKey]; ok {
		if owner != cmd.UserID {
			return verification.SubmitResult{}, verification.ErrIdempotencyKeyConflict
		}
		return verification.SubmitResult{Record: store.records[cmd.UserID]}, nil
	}
	record := verification.Record{
		VerificationID: "ver-" + cmd.UserID,
		UserID:         cmd.UserID,
		Status:         decision.Status,
		Reason:         decision.Reason,
		TraceID:        cmd.TraceID,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if decision.Status == verification.StatusVerified || decision.Status == verification.StatusRejected {
		record.DecidedAt = now
	}
	store.keys[cmd.IdempotencyKey] = cmd.UserID
	store.records[cmd.UserID] = record
	return verification.SubmitResult{Record: record, Transitioned: true, TransitionedFrom: verification.StatusUnverified}, nil
}

func (store *verificationMemoryStore) Decide(_ context.Context, cmd verification.DecisionCommand, decision verification.Decision, now time.Time) (verification.SubmitResult, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	record, ok := store.records[cmd.UserID]
	if !ok {
		return verification.SubmitResult{}, verification.ErrNotFound
	}
	from := record.Status
	record.Status = decision.Status
	record.Reason = decision.Reason
	record.TraceID = cmd.TraceID
	record.UpdatedAt = now
	record.DecidedAt = now
	store.records[cmd.UserID] = record
	return verification.SubmitResult{Record: record, Transitioned: true, TransitionedFrom: from}, nil
}

func (store *verificationMemoryStore) GetStatus(_ context.Context, userID string) (verification.Record, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	record, ok := store.records[userID]
	if !ok {
		return verification.Record{}, verification.ErrNotFound
	}
	return record, nil
}

type walletBoundary struct {
	balance     int64
	debitCalls  int
	debits      map[string]string
	compensated map[string]bool
}

func (wallet *walletBoundary) DebitForSaga(_ context.Context, req saga.WalletDebitRequest) (saga.WalletDebitResult, error) {
	wallet.debitCalls++
	if debitID, ok := wallet.debits[req.PaymentID]; ok {
		return saga.WalletDebitResult{WalletDebitID: debitID}, nil
	}
	if wallet.balance < req.AmountCents {
		return saga.WalletDebitResult{}, errors.New("insufficient funds")
	}
	wallet.balance -= req.AmountCents
	debitID := "debit-" + req.PaymentID
	wallet.debits[req.PaymentID] = debitID
	return saga.WalletDebitResult{WalletDebitID: debitID}, nil
}

func (wallet *walletBoundary) CompensateDebit(_ context.Context, req saga.WalletCompensateRequest) error {
	if wallet.compensated[req.PaymentID] {
		return nil
	}
	wallet.balance += req.AmountCents
	wallet.compensated[req.PaymentID] = true
	return nil
}

type ledgerBoundary struct {
	failReserve  bool
	reserveCalls int
	confirmCalls int
	status       map[string]string
	reservations map[string]string
	transfers    map[string]string
}

func (ledger *ledgerBoundary) ReserveTransfer(_ context.Context, req saga.LedgerReserveRequest) (saga.LedgerReserveResult, error) {
	ledger.reserveCalls++
	if ledger.failReserve {
		ledger.failReserve = false
		return saga.LedgerReserveResult{}, errors.New("ledger unavailable")
	}
	if reservationID, ok := ledger.reservations[req.PaymentID]; ok {
		return saga.LedgerReserveResult{LedgerReservationID: reservationID}, nil
	}
	reservationID := "reservation-" + req.PaymentID
	ledger.reservations[req.PaymentID] = reservationID
	ledger.status[req.PaymentID] = "RESERVED"
	return saga.LedgerReserveResult{LedgerReservationID: reservationID}, nil
}

func (ledger *ledgerBoundary) ConfirmTransfer(_ context.Context, req saga.LedgerConfirmRequest) (saga.LedgerConfirmResult, error) {
	ledger.confirmCalls++
	if transferID, ok := ledger.transfers[req.PaymentID]; ok {
		return saga.LedgerConfirmResult{TransferID: transferID, CompletedAt: acceptanceTime}, nil
	}
	transferID := "transfer-" + req.PaymentID
	ledger.transfers[req.PaymentID] = transferID
	ledger.status[req.PaymentID] = "CONFIRMED"
	return saga.LedgerConfirmResult{TransferID: transferID, CompletedAt: acceptanceTime}, nil
}

func (ledger *ledgerBoundary) CancelReservation(_ context.Context, req saga.LedgerCancelRequest) error {
	ledger.status[req.PaymentID] = "CANCELLED"
	return nil
}

type railBoundary struct {
	mode  railMode
	calls int
}

func (rail *railBoundary) Charge(_ context.Context, req paymentprocessor.RailChargeRequest) (paymentprocessor.RailResult, error) {
	rail.calls++
	switch rail.mode {
	case railTerminalFailure:
		return paymentprocessor.RailResult{}, paymentprocessor.TerminalRailError("terminal_failure", "terminal rail failure")
	case railRetryOnce:
		if rail.calls == 1 {
			return paymentprocessor.RailResult{}, paymentprocessor.RetryableRailError("rail_unavailable", "retryable rail failure")
		}
	}
	return paymentprocessor.RailResult{ProcessorPaymentID: "processor-" + req.PaymentID, CompletedAt: acceptanceTime}, nil
}

type paymentMemoryStore struct {
	mu    sync.Mutex
	items map[string]paymentprocessor.Attempt
}

func (store *paymentMemoryStore) GetByPaymentID(_ context.Context, paymentID string) (paymentprocessor.Attempt, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	attempt, ok := store.items[paymentID]
	if !ok {
		return paymentprocessor.Attempt{}, paymentprocessor.ErrNotFound
	}
	return attempt, nil
}

func (store *paymentMemoryStore) CreatePending(_ context.Context, command saga.PaymentExecute, now time.Time) (paymentprocessor.Attempt, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if attempt, ok := store.items[command.PaymentID]; ok {
		return attempt, nil
	}
	attempt := paymentprocessor.Attempt{
		PaymentID:           command.PaymentID,
		IdempotencyKey:      command.IdempotencyKey,
		TraceID:             command.TraceID,
		AmountCents:         command.AmountCents,
		Currency:            command.Currency,
		LedgerReservationID: command.LedgerReservationID,
		WalletDebitID:       command.WalletDebitID,
		Status:              paymentprocessor.StatusPending,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	store.items[command.PaymentID] = attempt
	return attempt, nil
}

func (store *paymentMemoryStore) MarkAttemptStarted(_ context.Context, paymentID string, count int, now time.Time) (paymentprocessor.Attempt, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	attempt, ok := store.items[paymentID]
	if !ok {
		return paymentprocessor.Attempt{}, paymentprocessor.ErrNotFound
	}
	attempt.AttemptCount = count
	attempt.UpdatedAt = now
	store.items[paymentID] = attempt
	return attempt, nil
}

func (store *paymentMemoryStore) MarkCompleted(_ context.Context, paymentID string, result paymentprocessor.RailResult, now time.Time) (paymentprocessor.Attempt, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	attempt, ok := store.items[paymentID]
	if !ok {
		return paymentprocessor.Attempt{}, paymentprocessor.ErrNotFound
	}
	attempt.Status = paymentprocessor.StatusCompleted
	attempt.ProcessorPaymentID = result.ProcessorPaymentID
	attempt.CompletedAt = result.CompletedAt
	attempt.UpdatedAt = now
	store.items[paymentID] = attempt
	return attempt, nil
}

func (store *paymentMemoryStore) MarkFailed(_ context.Context, paymentID string, failure paymentprocessor.RailFailure, now time.Time) (paymentprocessor.Attempt, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	attempt, ok := store.items[paymentID]
	if !ok {
		return paymentprocessor.Attempt{}, paymentprocessor.ErrNotFound
	}
	attempt.Status = paymentprocessor.StatusFailed
	attempt.FailureCode = failure.Code
	attempt.FailureMessage = failure.Message
	attempt.FailedAt = now
	attempt.UpdatedAt = now
	store.items[paymentID] = attempt
	return attempt, nil
}

type notificationAdapter struct {
	messages []notification.Message
}

func (adapter *notificationAdapter) Send(_ context.Context, message notification.Message) error {
	adapter.messages = append(adapter.messages, message)
	return nil
}

type fixedClock struct{}

func (fixedClock) Now() time.Time {
	return acceptanceTime
}

type noSleep struct{}

func (noSleep) Sleep(context.Context, time.Duration) error {
	return nil
}

type directSagaClient struct {
	server *sagagrpc.Server
}

func (client directSagaClient) StartPayment(ctx context.Context, req saga.StartRequest) (saga.Saga, error) {
	resp, err := client.server.StartPaymentSaga(ctx, &sagav1.StartPaymentSagaRequest{
		PaymentId:      req.PaymentID,
		IdempotencyKey: req.IdempotencyKey,
		TraceId:        req.TraceID,
		UserId:         req.UserID,
		FromWalletId:   req.FromWalletID,
		ToWalletId:     req.ToWalletID,
		AmountCents:    req.AmountCents,
		Currency:       req.Currency,
	})
	if err != nil {
		return saga.Saga{}, err
	}
	return saga.Saga{PaymentID: resp.GetPaymentId(), State: resp.GetStatus()}, nil
}

func (client directSagaClient) GetPayment(ctx context.Context, paymentID, traceID string) (saga.Saga, error) {
	resp, err := client.server.GetPaymentSaga(ctx, &sagav1.GetPaymentSagaRequest{PaymentId: paymentID, TraceId: traceID})
	if err != nil {
		return saga.Saga{}, err
	}
	message := resp.GetSaga()
	return saga.Saga{PaymentID: message.GetPaymentId(), State: message.GetStatus()}, nil
}
