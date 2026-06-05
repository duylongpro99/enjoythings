package verification

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestServiceAutoSubmitVerifiesUserAndPublishesOnce(t *testing.T) {
	store := newMemoryStore()
	events := &recordingOutbox{}
	service := NewService(store, events, Config{Mode: ModeAuto}, fixedClock{})

	first, err := service.Submit(context.Background(), SubmitCommand{
		UserID:         "user-1",
		IdempotencyKey: "verify-user-1",
		TraceID:        "trace-1",
	})
	if err != nil {
		t.Fatalf("submit verification: %v", err)
	}
	if first.Status != StatusVerified {
		t.Fatalf("status = %s, want %s", first.Status, StatusVerified)
	}
	if first.VerificationID == "" {
		t.Fatal("verification id is empty")
	}
	if len(events.events) != 1 {
		t.Fatalf("events = %d, want 1", len(events.events))
	}
	if events.events[0].topic != UserVerifiedTopic {
		t.Fatalf("event topic = %q, want %q", events.events[0].topic, UserVerifiedTopic)
	}
	if events.events[0].partitionKey != "user-1" {
		t.Fatalf("event partition key = %q, want user-1", events.events[0].partitionKey)
	}
	var payload map[string]string
	if err := json.Unmarshal(events.events[0].payload, &payload); err != nil {
		t.Fatalf("event payload json: %v", err)
	}
	if payload["user_id"] != "user-1" || payload["verification_id"] != first.VerificationID || payload["trace_id"] != "trace-1" {
		t.Fatalf("unexpected event payload: %#v", payload)
	}

	second, err := service.Submit(context.Background(), SubmitCommand{
		UserID:         "user-1",
		IdempotencyKey: "verify-user-1",
		TraceID:        "trace-1",
	})
	if err != nil {
		t.Fatalf("duplicate submit verification: %v", err)
	}
	if second.VerificationID != first.VerificationID || second.Status != StatusVerified {
		t.Fatalf("duplicate = %#v, want verification_id %q status %s", second, first.VerificationID, StatusVerified)
	}
	if len(events.events) != 1 {
		t.Fatalf("events after duplicate = %d, want 1", len(events.events))
	}
}

func TestServiceGetStatusReturnsNotFoundForUnknownUser(t *testing.T) {
	service := NewService(newMemoryStore(), &recordingOutbox{}, Config{Mode: ModeAuto}, fixedClock{})

	_, err := service.GetStatus(context.Background(), "missing-user")
	if err != ErrNotFound {
		t.Fatalf("GetStatus error = %v, want %v", err, ErrNotFound)
	}
}

func TestServiceRulesModeCanRejectDeterministically(t *testing.T) {
	events := &recordingOutbox{}
	service := NewService(newMemoryStore(), events, Config{Mode: ModeRules}, fixedClock{})

	record, err := service.Submit(context.Background(), SubmitCommand{
		UserID:         "user-2",
		IdempotencyKey: "verify-user-2",
		Decision:       DecisionReject,
		Reason:         "test rule rejection",
	})
	if err != nil {
		t.Fatalf("submit verification: %v", err)
	}
	if record.Status != StatusRejected {
		t.Fatalf("status = %s, want %s", record.Status, StatusRejected)
	}
	if record.Reason != "test rule rejection" {
		t.Fatalf("reason = %q, want test rule rejection", record.Reason)
	}
	if len(events.events) != 1 || events.events[0].topic != UserRejectedTopic {
		t.Fatalf("events = %#v, want one %s event", events.events, UserRejectedTopic)
	}
}

func TestServiceManualDecisionApprovesPendingVerification(t *testing.T) {
	events := &recordingOutbox{}
	service := NewService(newMemoryStore(), events, Config{Mode: ModeManual}, fixedClock{})

	pending, err := service.Submit(context.Background(), SubmitCommand{
		UserID:         "user-3",
		IdempotencyKey: "verify-user-3",
		TraceID:        "trace-submit",
	})
	if err != nil {
		t.Fatalf("submit verification: %v", err)
	}
	if pending.Status != StatusPending {
		t.Fatalf("status = %s, want %s", pending.Status, StatusPending)
	}
	if len(events.events) != 0 {
		t.Fatalf("events after pending submit = %d, want 0", len(events.events))
	}

	approved, err := service.Decide(context.Background(), DecisionCommand{
		UserID:   "user-3",
		Decision: DecisionApprove,
		TraceID:  "trace-approve",
	})
	if err != nil {
		t.Fatalf("approve verification: %v", err)
	}
	if approved.Status != StatusVerified || approved.VerificationID != pending.VerificationID {
		t.Fatalf("approved record = %+v, want status %s verification_id %s", approved, StatusVerified, pending.VerificationID)
	}
	if len(events.events) != 1 || events.events[0].topic != UserVerifiedTopic {
		t.Fatalf("events = %#v, want one %s event", events.events, UserVerifiedTopic)
	}
}

type recordingOutbox struct {
	events []recordedEvent
}

type recordedEvent struct {
	topic        string
	partitionKey string
	payload      []byte
}

func (outbox *recordingOutbox) Enqueue(_ context.Context, topic, partitionKey string, payload []byte) error {
	outbox.events = append(outbox.events, recordedEvent{
		topic:        topic,
		partitionKey: partitionKey,
		payload:      append([]byte(nil), payload...),
	})
	return nil
}

type fixedClock struct{}

func (fixedClock) Now() time.Time {
	return time.Date(2026, 6, 4, 10, 30, 0, 0, time.UTC)
}
