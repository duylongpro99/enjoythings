// Package phase4e2e drives the Phase 4 fraud loop across the saga orchestrator,
// the Kafka consumer boundary, a fraud worker double that speaks the published
// event contract, and the notification service.
package phase4e2e

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"enjoythings/services/internal/event"
	"enjoythings/services/internal/notification"
	"enjoythings/services/internal/saga"
	"enjoythings/services/internal/sagaconsumer"

	"github.com/twmb/franz-go/pkg/kgo"
)

var acceptanceTime = time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)

const (
	userID       = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	fromWalletID = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	toWalletID   = "cccccccc-cccc-cccc-cccc-cccccccccccc"
	amountCents  = 1250

	providerID = "local-fake"
	modelID    = "fake-model"
)

// verdict is the fraud worker outcome a scenario scripts for one payment.
type verdict struct {
	action     string
	riskScore  float64
	reason     string
	reasonCode string // non-empty publishes fraud.error instead of fraud.flagged
}

var (
	allowVerdict = verdict{action: "allow", riskScore: 0.10, reason: "amount matches history"}
	flagVerdict  = verdict{action: event.FraudActionFlag, riskScore: 0.80, reason: "velocity spike"}
	blockVerdict = verdict{action: event.FraudActionBlock, riskScore: 0.95, reason: "velocity spike and new recipient"}
)

func errorVerdict(reasonCode string) verdict {
	return verdict{reasonCode: reasonCode}
}

type harness struct {
	ctx           context.Context
	bus           *eventBus
	store         *sagaStore
	ledger        *ledgerBoundary
	orchestrator  *saga.Orchestrator
	sagaConsumer  *sagaconsumer.Consumer
	notifications *notification.Consumer
	email         *notificationAdapter
	worker        *fraudWorkerDouble
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	bus := &eventBus{}
	store := newSagaStore()
	ledger := newLedgerBoundary()
	orchestrator := saga.NewOrchestrator(store, verifiedUser{}, newWalletBoundary(), ledger, fixedClock{})
	orchestrator.SetOutbox(bus)
	email := &notificationAdapter{}
	return &harness{
		ctx:           context.Background(),
		bus:           bus,
		store:         store,
		ledger:        ledger,
		orchestrator:  orchestrator,
		sagaConsumer:  sagaconsumer.New(orchestrator, nil, nil, slog.New(slog.DiscardHandler)),
		notifications: notification.NewConsumer(notification.NewDispatcher(email, nil, slog.New(slog.DiscardHandler)), nil, nil, slog.New(slog.DiscardHandler)),
		email:         email,
		worker:        &fraudWorkerDouble{bus: bus},
	}
}

// startPayment drives a verified user's payment to PAYMENT_PROCESSING, which is
// where the saga publishes payment.execute and fraud.score.requested together.
func (h *harness) startPayment(t *testing.T, paymentID string) saga.Saga {
	t.Helper()
	started, err := h.orchestrator.StartPaymentSaga(h.ctx, saga.StartRequest{
		PaymentID:      paymentID,
		IdempotencyKey: "start-" + paymentID,
		TraceID:        "trace-" + paymentID,
		UserID:         userID,
		FromWalletID:   fromWalletID,
		ToWalletID:     toWalletID,
		AmountCents:    amountCents,
		Currency:       "USD",
	})
	if err != nil {
		t.Fatalf("saga boundary: StartPaymentSaga: %v", err)
	}
	return started
}

// scoreFraud consumes fraud.score.requested and publishes the scripted verdict.
func (h *harness) scoreFraud(t *testing.T, result verdict) {
	t.Helper()
	requested, ok := h.bus.pop(event.FraudScoreRequestedTopic)
	if !ok {
		t.Fatalf("fraud.score.requested boundary: no queued event")
	}
	h.worker.score(t, requested.payload, result)
}

// deliverFraudVerdict feeds fraud.flagged through the saga Kafka consumer.
func (h *harness) deliverFraudVerdict(t *testing.T) {
	t.Helper()
	flagged, ok := h.bus.pop(event.FraudFlaggedTopic)
	if !ok {
		t.Fatalf("fraud.flagged boundary: no queued event")
	}
	h.consumeSagaRecord(t, flagged)
}

// redeliverLastFraudVerdict replays the most recent fraud.flagged event.
func (h *harness) redeliverLastFraudVerdict(t *testing.T) {
	t.Helper()
	flagged, ok := h.bus.last(event.FraudFlaggedTopic)
	if !ok {
		t.Fatalf("fraud.flagged boundary: no published event to replay")
	}
	h.consumeSagaRecord(t, flagged)
}

// completePayment feeds a processor result through the saga Kafka consumer.
func (h *harness) completePayment(t *testing.T, paymentID string) {
	t.Helper()
	payload, err := json.Marshal(saga.PaymentCompleted{
		EventID:            "payment.completed:" + paymentID,
		PaymentID:          paymentID,
		IdempotencyKey:     "execute-payment:" + paymentID,
		TraceID:            "trace-" + paymentID,
		ProcessorPaymentID: "processor-" + paymentID,
		Status:             "completed",
		CompletedAt:        acceptanceTime,
		OccurredAt:         acceptanceTime,
	})
	if err != nil {
		t.Fatalf("payment.completed boundary: marshal: %v", err)
	}
	h.consumeSagaRecord(t, recordedEvent{topic: sagaconsumer.PaymentCompletedTopic, partitionKey: paymentID, payload: payload})
}

// deliverNotifications drains every queued notification topic.
func (h *harness) deliverNotifications(t *testing.T) {
	t.Helper()
	for _, topic := range []string{event.TxPausedTopic, saga.TopicTxCompleted, saga.TopicTxFailed} {
		for {
			queued, ok := h.bus.pop(topic)
			if !ok {
				break
			}
			if err := h.notifications.HandleRecord(h.ctx, &kgo.Record{Topic: queued.topic, Value: queued.payload}); err != nil {
				t.Fatalf("%s boundary: notification: %v", topic, err)
			}
		}
	}
}

func (h *harness) consumeSagaRecord(t *testing.T, queued recordedEvent) {
	t.Helper()
	if err := h.sagaConsumer.HandleRecord(h.ctx, &kgo.Record{Topic: queued.topic, Value: queued.payload}); err != nil {
		t.Fatalf("%s boundary: saga consumer: %v", queued.topic, err)
	}
}

func (h *harness) requireSagaState(t *testing.T, paymentID, want string) saga.Saga {
	t.Helper()
	current, err := h.store.GetByPaymentID(h.ctx, paymentID)
	if err != nil {
		t.Fatalf("saga store boundary: GetByPaymentID: %v", err)
	}
	if current.State != want {
		t.Fatalf("saga state = %s, want %s", current.State, want)
	}
	return current
}

func (h *harness) requirePublishedCount(t *testing.T, topic string, want int) {
	t.Helper()
	if got := h.bus.count(topic); got != want {
		t.Fatalf("event bus boundary: %s published %d times, want %d", topic, got, want)
	}
}

// requirePartitionKey proves a topic keeps the payment id as its partition key.
func (h *harness) requirePartitionKey(t *testing.T, topic, want string) {
	t.Helper()
	found := false
	for _, published := range h.bus.published {
		if published.topic != topic {
			continue
		}
		found = true
		if published.partitionKey != want {
			t.Fatalf("%s boundary: partition key = %q, want %q", topic, published.partitionKey, want)
		}
	}
	if !found {
		t.Fatalf("%s boundary: no published event", topic)
	}
}

func (h *harness) requireNotificationSubjects(t *testing.T, want ...string) {
	t.Helper()
	var subjects []string
	for _, message := range h.email.messages {
		subjects = append(subjects, message.Subject)
	}
	if strings.Join(subjects, "|") != strings.Join(want, "|") {
		t.Fatalf("notification boundary: subjects = %v, want %v", subjects, want)
	}
}

func (h *harness) requireAudit(t *testing.T, eventID, kind string) saga.FraudAuditRecord {
	t.Helper()
	audit, ok := h.store.audits[eventID]
	if !ok {
		t.Fatalf("saga audit boundary: no record for %s; have %v", eventID, h.store.auditIDs())
	}
	if audit.Kind != kind {
		t.Fatalf("saga audit boundary: kind = %s, want %s", audit.Kind, kind)
	}
	return audit
}

// eventBus records outbox publications and replays them to consumers.
type eventBus struct {
	queue     []recordedEvent
	published []recordedEvent
}

type recordedEvent struct {
	topic        string
	partitionKey string
	payload      []byte
}

func (bus *eventBus) Enqueue(_ context.Context, topic, partitionKey string, payload []byte) error {
	recorded := recordedEvent{topic: topic, partitionKey: partitionKey, payload: append([]byte(nil), payload...)}
	bus.queue = append(bus.queue, recorded)
	bus.published = append(bus.published, recorded)
	return nil
}

func (bus *eventBus) pop(topic string) (recordedEvent, bool) {
	for i, queued := range bus.queue {
		if queued.topic == topic {
			bus.queue = append(bus.queue[:i], bus.queue[i+1:]...)
			return queued, true
		}
	}
	return recordedEvent{}, false
}

func (bus *eventBus) last(topic string) (recordedEvent, bool) {
	for i := len(bus.published) - 1; i >= 0; i-- {
		if bus.published[i].topic == topic {
			return bus.published[i], true
		}
	}
	return recordedEvent{}, false
}

func (bus *eventBus) count(topic string) int {
	total := 0
	for _, published := range bus.published {
		if published.topic == topic {
			total++
		}
	}
	return total
}

func (bus *eventBus) topicOrder() []string {
	order := make([]string, 0, len(bus.published))
	for _, published := range bus.published {
		order = append(order, published.topic)
	}
	return order
}

func (bus *eventBus) payloads(topic string) []string {
	var payloads []string
	for _, published := range bus.published {
		if published.topic == topic {
			payloads = append(payloads, string(published.payload))
		}
	}
	return payloads
}

// fraudWorkerDouble mimics the Python worker's published event contract.
type fraudWorkerDouble struct {
	bus      *eventBus
	requests []event.FraudScoreRequested
}

func (worker *fraudWorkerDouble) score(t *testing.T, payload []byte, result verdict) {
	t.Helper()
	var requested event.FraudScoreRequested
	if err := json.Unmarshal(payload, &requested); err != nil {
		t.Fatalf("fraud worker boundary: unmarshal: %v", err)
	}
	if err := requested.Validate(); err != nil {
		t.Fatalf("fraud worker boundary: validate: %v", err)
	}
	worker.requests = append(worker.requests, requested)
	switch {
	case result.reasonCode != "":
		worker.publish(t, event.FraudErrorTopic, event.FraudError{
			SchemaVersion: 1,
			EventID:       event.FraudErrorEventID(requested.EventID, result.reasonCode),
			SourceEventID: requested.EventID,
			PaymentID:     requested.PaymentID,
			SessionID:     sessionID(requested.PaymentID),
			ReasonCode:    result.reasonCode,
			OccurredAt:    acceptanceTime,
			TraceID:       requested.TraceID,
		}, requested.PaymentID)
	case result.action == event.FraudActionFlag || result.action == event.FraudActionBlock:
		worker.publish(t, event.FraudFlaggedTopic, event.FraudFlagged{
			SchemaVersion: 1,
			EventID:       event.FraudFlaggedEventID(requested.EventID),
			SourceEventID: requested.EventID,
			PaymentID:     requested.PaymentID,
			SessionID:     sessionID(requested.PaymentID),
			Action:        result.action,
			RiskScore:     result.riskScore,
			Reason:        result.reason,
			ProviderID:    providerID,
			ModelID:       modelID,
			OccurredAt:    acceptanceTime,
			TraceID:       requested.TraceID,
		}, requested.PaymentID)
	}
}

func (worker *fraudWorkerDouble) publish(t *testing.T, topic string, payload any, partitionKey string) {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("fraud worker boundary: marshal %s: %v", topic, err)
	}
	if validator, ok := payload.(interface{ Validate() error }); ok {
		if err := validator.Validate(); err != nil {
			t.Fatalf("fraud worker boundary: %s contract: %v", topic, err)
		}
	}
	if err := worker.bus.Enqueue(context.Background(), topic, partitionKey, encoded); err != nil {
		t.Fatalf("fraud worker boundary: publish %s: %v", topic, err)
	}
}

func sessionID(paymentID string) string {
	return "fraud-session-" + paymentID
}

// scoreRequestFor builds a saga-shaped scoring request for a payment the saga
// store does not own, which is how an orphan verdict reaches the consumer.
func scoreRequestFor(t *testing.T, paymentID string) []byte {
	t.Helper()
	payload, err := json.Marshal(event.FraudScoreRequested{
		SchemaVersion: 1,
		EventID:       event.FraudScoreRequestedEventID(paymentID),
		PaymentID:     paymentID,
		UserID:        userID,
		FromWalletID:  fromWalletID,
		ToWalletID:    toWalletID,
		AmountCents:   amountCents,
		Currency:      "USD",
		OccurredAt:    acceptanceTime,
		TraceID:       "trace-" + paymentID,
	})
	if err != nil {
		t.Fatalf("fraud.score.requested boundary: marshal: %v", err)
	}
	return payload
}

type sagaStore struct {
	byPaymentID map[string]saga.Saga
	audits      map[string]saga.FraudAuditRecord
}

func newSagaStore() *sagaStore {
	return &sagaStore{byPaymentID: map[string]saga.Saga{}, audits: map[string]saga.FraudAuditRecord{}}
}

func (store *sagaStore) Create(_ context.Context, current saga.Saga) (saga.Saga, error) {
	if existing, ok := store.byPaymentID[current.PaymentID]; ok {
		return existing, nil
	}
	current.ID = "saga-" + current.PaymentID
	store.byPaymentID[current.PaymentID] = current
	return current, nil
}

func (store *sagaStore) GetByPaymentID(_ context.Context, paymentID string) (saga.Saga, error) {
	current, ok := store.byPaymentID[paymentID]
	if !ok {
		return saga.Saga{}, saga.ErrNotFound
	}
	return current, nil
}

func (store *sagaStore) ListNonTerminal(context.Context) ([]saga.Saga, error) {
	var result []saga.Saga
	for _, current := range store.byPaymentID {
		if current.State != saga.StateCompleted && current.State != saga.StateFailed {
			result = append(result, current)
		}
	}
	return result, nil
}

func (store *sagaStore) ListFraudReview(context.Context) ([]saga.Saga, error) {
	var result []saga.Saga
	for _, current := range store.byPaymentID {
		if current.State == saga.StateFraudReview {
			result = append(result, current)
		}
	}
	return result, nil
}

func (store *sagaStore) Update(_ context.Context, current saga.Saga) (saga.Saga, error) {
	if _, ok := store.byPaymentID[current.PaymentID]; !ok {
		return saga.Saga{}, saga.ErrNotFound
	}
	store.byPaymentID[current.PaymentID] = current
	return current, nil
}

func (store *sagaStore) RecordFraudAudit(_ context.Context, audit saga.FraudAuditRecord) error {
	if audit.EventID == "" {
		return nil
	}
	if _, ok := store.audits[audit.EventID]; ok {
		return nil
	}
	store.audits[audit.EventID] = audit
	return nil
}

func (store *sagaStore) auditIDs() []string {
	ids := make([]string, 0, len(store.audits))
	for id := range store.audits {
		ids = append(ids, id)
	}
	return ids
}

type verifiedUser struct{}

func (verifiedUser) GetStatus(context.Context, saga.VerificationRequest) (saga.VerificationResult, error) {
	return saga.VerificationResult{Status: saga.VerificationVerified}, nil
}

type walletBoundary struct {
	debits map[string]string
}

func newWalletBoundary() *walletBoundary {
	return &walletBoundary{debits: map[string]string{}}
}

func (wallet *walletBoundary) DebitForSaga(_ context.Context, req saga.WalletDebitRequest) (saga.WalletDebitResult, error) {
	if debitID, ok := wallet.debits[req.PaymentID]; ok {
		return saga.WalletDebitResult{WalletDebitID: debitID}, nil
	}
	debitID := "debit-" + req.PaymentID
	wallet.debits[req.PaymentID] = debitID
	return saga.WalletDebitResult{WalletDebitID: debitID}, nil
}

func (wallet *walletBoundary) CompensateDebit(context.Context, saga.WalletCompensateRequest) error {
	return nil
}

type ledgerBoundary struct {
	confirmCalls int
	cancelCalls  int
	reservations map[string]string
}

func newLedgerBoundary() *ledgerBoundary {
	return &ledgerBoundary{reservations: map[string]string{}}
}

func (ledger *ledgerBoundary) ReserveTransfer(_ context.Context, req saga.LedgerReserveRequest) (saga.LedgerReserveResult, error) {
	if reservationID, ok := ledger.reservations[req.PaymentID]; ok {
		return saga.LedgerReserveResult{LedgerReservationID: reservationID}, nil
	}
	reservationID := "reservation-" + req.PaymentID
	ledger.reservations[req.PaymentID] = reservationID
	return saga.LedgerReserveResult{LedgerReservationID: reservationID}, nil
}

func (ledger *ledgerBoundary) ConfirmTransfer(_ context.Context, req saga.LedgerConfirmRequest) (saga.LedgerConfirmResult, error) {
	ledger.confirmCalls++
	return saga.LedgerConfirmResult{TransferID: "transfer-" + req.PaymentID, CompletedAt: acceptanceTime}, nil
}

func (ledger *ledgerBoundary) CancelReservation(context.Context, saga.LedgerCancelRequest) error {
	ledger.cancelCalls++
	return nil
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
