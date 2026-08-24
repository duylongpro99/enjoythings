package phase4e2e

import (
	"strings"
	"testing"

	"enjoythings/services/internal/event"
	"enjoythings/services/internal/saga"
)

func TestFraudScoringNeverDelaysThePaymentPath(t *testing.T) {
	h := newHarness(t)

	started := h.startPayment(t, "11111111-1111-1111-1111-111111111111")

	h.requireSagaState(t, started.PaymentID, saga.StatePaymentProcessing)
	if order := h.bus.topicOrder(); len(order) != 2 || order[0] != saga.TopicPaymentExecute || order[1] != event.FraudScoreRequestedTopic {
		t.Fatalf("event bus boundary: publish order = %v, want payment.execute before fraud.score.requested", order)
	}
}

func TestFlaggedVerdictMovesActiveSagaToReviewAndPausesOnce(t *testing.T) {
	h := newHarness(t)
	started := h.startPayment(t, "22222222-2222-2222-2222-222222222222")

	h.scoreFraud(t, flagVerdict)
	h.deliverFraudVerdict(t)
	h.deliverNotifications(t)

	current := h.requireSagaState(t, started.PaymentID, saga.StateFraudReview)
	if current.FraudAction != event.FraudActionFlag || current.FraudSessionID != sessionID(started.PaymentID) {
		t.Fatalf("saga fraud fields = %+v, want flag verdict for the scored session", current)
	}
	h.requirePublishedCount(t, event.TxPausedTopic, 1)
	h.requirePartitionKey(t, event.TxPausedTopic, started.PaymentID)
	h.requirePartitionKey(t, event.FraudFlaggedTopic, started.PaymentID)
	h.requireNotificationSubjects(t, "Payment paused for review")
	h.requireAudit(t, event.FraudFlaggedEventID(event.FraudScoreRequestedEventID(started.PaymentID)), saga.FraudAuditKindTransition)
}

func TestBlockVerdictKeepsActionThroughReviewAndNotification(t *testing.T) {
	h := newHarness(t)
	started := h.startPayment(t, "33333333-3333-3333-3333-333333333333")

	h.scoreFraud(t, blockVerdict)
	h.deliverFraudVerdict(t)
	h.deliverNotifications(t)

	current := h.requireSagaState(t, started.PaymentID, saga.StateFraudReview)
	if current.FraudAction != event.FraudActionBlock {
		t.Fatalf("saga fraud action = %s, want %s", current.FraudAction, event.FraudActionBlock)
	}
	paused := h.bus.payloads(event.TxPausedTopic)
	if len(paused) != 1 || !strings.Contains(paused[0], `"action":"block"`) {
		t.Fatalf("tx.paused boundary: payloads = %v, want one block action", paused)
	}
	if body := h.email.messages[0].Body; !strings.Contains(body, "action block") {
		t.Fatalf("notification boundary: body = %q, want the block action", body)
	}
}

func TestAllowVerdictPublishesNothingAndPaymentCompletes(t *testing.T) {
	h := newHarness(t)
	started := h.startPayment(t, "44444444-4444-4444-4444-444444444444")

	h.scoreFraud(t, allowVerdict)
	h.completePayment(t, started.PaymentID)
	h.deliverNotifications(t)

	h.requireSagaState(t, started.PaymentID, saga.StateCompleted)
	h.requirePublishedCount(t, event.FraudFlaggedTopic, 0)
	h.requirePublishedCount(t, event.TxPausedTopic, 0)
	h.requireNotificationSubjects(t, "Payment completed")
}

func TestFraudErrorLeavesThePaymentPathFailOpen(t *testing.T) {
	h := newHarness(t)
	started := h.startPayment(t, "55555555-5555-5555-5555-555555555555")

	h.scoreFraud(t, errorVerdict(event.FraudReasonEnrichmentFailed))
	h.completePayment(t, started.PaymentID)
	h.deliverNotifications(t)

	h.requireSagaState(t, started.PaymentID, saga.StateCompleted)
	h.requirePublishedCount(t, event.FraudErrorTopic, 1)
	h.requirePublishedCount(t, event.FraudFlaggedTopic, 0)
	h.requirePublishedCount(t, event.TxPausedTopic, 0)
	h.requireNotificationSubjects(t, "Payment completed")
}

func TestDuplicateFlaggedEventCausesOneReviewTransitionAndOnePause(t *testing.T) {
	h := newHarness(t)
	started := h.startPayment(t, "66666666-6666-6666-6666-666666666666")
	h.scoreFraud(t, flagVerdict)

	h.deliverFraudVerdict(t)
	h.redeliverLastFraudVerdict(t)
	h.deliverNotifications(t)

	h.requireSagaState(t, started.PaymentID, saga.StateFraudReview)
	h.requirePublishedCount(t, event.TxPausedTopic, 1)
	h.requireNotificationSubjects(t, "Payment paused for review")
}

func TestPaymentResultRacingFraudReviewIsDeferred(t *testing.T) {
	h := newHarness(t)
	started := h.startPayment(t, "77777777-7777-7777-7777-777777777777")
	h.scoreFraud(t, flagVerdict)
	h.deliverFraudVerdict(t)

	h.completePayment(t, started.PaymentID)

	current := h.requireSagaState(t, started.PaymentID, saga.StateFraudReview)
	if !strings.Contains(current.DeferredPaymentJSON, `"payment.completed:`+started.PaymentID+`"`) {
		t.Fatalf("saga boundary: deferred payment = %q, want the stored payment.completed event", current.DeferredPaymentJSON)
	}
	if h.ledger.confirmCalls != 0 {
		t.Fatalf("ledger boundary: confirm calls = %d, want 0 while the saga is under review", h.ledger.confirmCalls)
	}
	h.requirePublishedCount(t, saga.TopicTxCompleted, 0)
}

func TestUnknownPaymentInFlaggedEventRecordsOrphanAuditAndCommits(t *testing.T) {
	h := newHarness(t)
	orphanPaymentID := "88888888-8888-8888-8888-888888888888"

	h.worker.score(t, scoreRequestFor(t, orphanPaymentID), flagVerdict)
	h.deliverFraudVerdict(t)

	audit := h.requireAudit(t, event.FraudFlaggedEventID(event.FraudScoreRequestedEventID(orphanPaymentID)), saga.FraudAuditKindOrphan)
	if audit.SagaState != "" {
		t.Fatalf("saga audit boundary: state = %q, want empty for an orphan verdict", audit.SagaState)
	}
	if len(h.store.byPaymentID) != 0 {
		t.Fatalf("saga store boundary: %d sagas mutated, want 0", len(h.store.byPaymentID))
	}
	h.requirePublishedCount(t, event.TxPausedTopic, 0)
}

func TestFraudVerdictEventsCarryNoRawIdentifiers(t *testing.T) {
	h := newHarness(t)
	started := h.startPayment(t, "99999999-9999-9999-9999-999999999999")

	h.scoreFraud(t, flagVerdict)
	h.deliverFraudVerdict(t)
	h.deliverNotifications(t)

	for _, topic := range []string{event.FraudFlaggedTopic, event.TxPausedTopic} {
		for _, payload := range h.bus.payloads(topic) {
			for _, identifier := range []string{userID, fromWalletID, toWalletID} {
				if strings.Contains(payload, identifier) {
					t.Fatalf("%s boundary: payload leaked a raw identifier: %s", topic, payload)
				}
			}
			if !strings.Contains(payload, started.PaymentID) {
				t.Fatalf("%s boundary: payload is missing the payment id: %s", topic, payload)
			}
		}
	}
	for _, message := range h.email.messages {
		for _, identifier := range []string{userID, fromWalletID, toWalletID} {
			if strings.Contains(message.Body, identifier) {
				t.Fatalf("notification boundary: message leaked a raw identifier: %s", message.Body)
			}
		}
	}
}
