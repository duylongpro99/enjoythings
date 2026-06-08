package notification

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestDispatcherRendersAndDispatchesAllPhase3Notifications(t *testing.T) {
	tests := []struct {
		name         string
		topic        string
		payload      []byte
		messageID    string
		aggregateID  string
		subject      string
		bodyContains string
		traceID      string
	}{
		{
			name:         "payment success",
			topic:        TopicTxCompleted,
			payload:      []byte(`{"payment_id":"payment-1","trace_id":"trace-1","from_wallet_id":"wallet-from","to_wallet_id":"wallet-to","amount_cents":1250,"currency":"USD","transfer_id":"transfer-1","completed_at":"2026-06-04T00:00:00Z","occurred_at":"2026-06-04T00:00:00Z"}`),
			messageID:    "tx.completed:payment-1",
			aggregateID:  "payment-1",
			subject:      "Payment completed",
			bodyContains: "12.50 USD",
			traceID:      "trace-1",
		},
		{
			name:         "payment failure",
			topic:        TopicTxFailed,
			payload:      []byte(`{"payment_id":"payment-2","trace_id":"trace-2","from_wallet_id":"wallet-from","to_wallet_id":"wallet-to","amount_cents":2199,"currency":"USD","failure_code":"rail_declined","failure_message":"card declined","failed_at":"2026-06-04T00:00:00Z","occurred_at":"2026-06-04T00:00:00Z"}`),
			messageID:    "tx.failed:payment-2",
			aggregateID:  "payment-2",
			subject:      "Payment failed",
			bodyContains: "card declined",
			traceID:      "trace-2",
		},
		{
			name:         "payment paused",
			topic:        TopicTxPaused,
			payload:      []byte(`{"schema_version":1,"event_id":"tx.paused:payment-3","payment_id":"payment-3","session_id":"session-1","action":"block","risk_score":0.95,"reason":"high velocity","trace_id":"trace-5"}`),
			messageID:    "tx.paused:payment-3",
			aggregateID:  "payment-3",
			subject:      "Payment paused for review",
			bodyContains: "high velocity",
			traceID:      "trace-5",
		},
		{
			name:         "verification success",
			topic:        TopicUserVerified,
			payload:      []byte(`{"event_id":"evt-1","user_id":"user-1","verification_id":"ver-1","trace_id":"trace-3","verified_at":"2026-06-04T00:00:00Z","occurred_at":"2026-06-04T00:00:00Z"}`),
			messageID:    "user.verified:user-1",
			aggregateID:  "user-1",
			subject:      "Verification approved",
			bodyContains: "approved",
			traceID:      "trace-3",
		},
		{
			name:         "verification rejected",
			topic:        TopicUserRejected,
			payload:      []byte(`{"event_id":"evt-2","user_id":"user-2","verification_id":"ver-2","trace_id":"trace-4","reason":"document expired","rejected_at":"2026-06-04T00:00:00Z","occurred_at":"2026-06-04T00:00:00Z"}`),
			messageID:    "user.rejected:user-2",
			aggregateID:  "user-2",
			subject:      "Verification rejected",
			bodyContains: "document expired",
			traceID:      "trace-4",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			email := &recordingAdapter{}
			sms := &recordingAdapter{}
			var logs bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logs, nil))
			dispatcher := NewDispatcher(email, sms, logger)

			err := dispatcher.Dispatch(context.Background(), Event{Topic: tt.topic, Payload: tt.payload})
			if err != nil {
				t.Fatalf("Dispatch: %v", err)
			}
			assertDelivered(t, email.messages, "email", tt.messageID, tt.aggregateID, tt.subject, tt.bodyContains, tt.traceID)
			assertDelivered(t, sms.messages, "sms", tt.messageID, tt.aggregateID, tt.subject, tt.bodyContains, tt.traceID)
			if !strings.Contains(logs.String(), "trace_id="+tt.traceID) || !strings.Contains(logs.String(), "message_id="+tt.messageID) {
				t.Fatalf("logs %q do not include trace_id and message_id", logs.String())
			}
		})
	}
}

func TestDispatcherReturnsAdapterErrorForKafkaRedelivery(t *testing.T) {
	dispatcher := NewDispatcher(&recordingAdapter{err: errors.New("email unavailable")}, &recordingAdapter{}, slog.New(slog.DiscardHandler))

	err := dispatcher.Dispatch(context.Background(), Event{
		Topic:   TopicTxCompleted,
		Payload: []byte(`{"payment_id":"payment-1","trace_id":"trace-1","amount_cents":1250,"currency":"USD"}`),
	})
	if err == nil {
		t.Fatal("expected adapter error")
	}
}

func TestDispatcherSanitizesPausedReason(t *testing.T) {
	email := &recordingAdapter{}
	dispatcher := NewDispatcher(email, nil, slog.New(slog.DiscardHandler))

	err := dispatcher.Dispatch(context.Background(), Event{
		Topic:   TopicTxPaused,
		Payload: []byte(`{"schema_version":1,"event_id":"tx.paused:payment-9","payment_id":"payment-9","session_id":"session-1","action":"block","risk_score":0.95,"reason":"<script>alert(1)</script> high velocity","trace_id":"trace-9"}`),
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(email.messages) != 1 {
		t.Fatalf("email messages = %d, want 1", len(email.messages))
	}
	body := email.messages[0].Body
	if strings.Contains(body, "<script>") {
		t.Fatalf("body = %q, want sanitized reason", body)
	}
	if !strings.Contains(body, "&lt;script&gt;alert(1)&lt;/script&gt; high velocity") {
		t.Fatalf("body = %q, want escaped reason", body)
	}
}

func assertDelivered(t *testing.T, messages []Message, channel, messageID, aggregateID, subject, bodyContains, traceID string) {
	t.Helper()
	if len(messages) != 1 {
		t.Fatalf("%s messages = %d, want 1", channel, len(messages))
	}
	message := messages[0]
	if message.ID != messageID || message.AggregateID != aggregateID || message.TraceID != traceID {
		t.Fatalf("%s message identity = %+v", channel, message)
	}
	if message.Subject != subject {
		t.Fatalf("%s subject = %q, want %q", channel, message.Subject, subject)
	}
	if !strings.Contains(message.Body, bodyContains) {
		t.Fatalf("%s body = %q, want it to contain %q", channel, message.Body, bodyContains)
	}
}

type recordingAdapter struct {
	messages []Message
	err      error
}

func (adapter *recordingAdapter) Send(_ context.Context, message Message) error {
	if adapter.err != nil {
		return adapter.err
	}
	adapter.messages = append(adapter.messages, message)
	return nil
}
