package event

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestFraudTopicContracts(t *testing.T) {
	tests := []struct {
		topic        string
		producer     string
		consumers    []string
		partitionKey string
		duplicates   string
	}{
		{FraudScoreRequestedTopic, SagaOrchestratorProducer, []string{FraudAgentConsumerGroup}, PaymentIDPartitionKey, DuplicateDeduplicateByEventID},
		{FraudFlaggedTopic, FraudWorkerProducer, []string{SagaOrchestratorConsumerGroup, NotificationConsumerGroup}, PaymentIDPartitionKey, DuplicateStableRepublish},
		{FraudErrorTopic, FraudWorkerProducer, []string{ObservabilityConsumer}, PaymentIDPartitionKey, DuplicateStableRepublish},
		{TxPausedTopic, SagaOrchestratorProducer, []string{NotificationConsumerGroup}, PaymentIDPartitionKey, DuplicateStableRepublish},
	}

	for _, test := range tests {
		contract, ok := FraudTopicContracts[test.topic]
		if !ok {
			t.Fatalf("missing contract for %q", test.topic)
		}
		if contract.Producer != test.producer || !reflect.DeepEqual(contract.Consumers, test.consumers) ||
			contract.PartitionKey != test.partitionKey || contract.DuplicateBehavior != test.duplicates {
			t.Fatalf("contract for %q = %+v", test.topic, contract)
		}
		if !reflect.DeepEqual(contract.RequiredHeaders, []string{TraceparentHeader}) ||
			!reflect.DeepEqual(contract.OptionalHeaders, []string{TracestateHeader}) {
			t.Fatalf("headers for %q = required:%v optional:%v", test.topic, contract.RequiredHeaders, contract.OptionalHeaders)
		}
	}
	if TraceparentHeader != "traceparent" || TracestateHeader != "tracestate" {
		t.Fatalf("trace headers = %q, %q", TraceparentHeader, TracestateHeader)
	}
}

func TestFraudContractsValidateRequiredFieldsAndStableEventIDs(t *testing.T) {
	occurredAt := time.Date(2026, time.June, 6, 0, 0, 0, 0, time.UTC)
	requested := FraudScoreRequested{
		SchemaVersion: 1,
		EventID:       FraudScoreRequestedEventID("payment-1"),
		PaymentID:     "payment-1",
		UserID:        "user-1",
		FromWalletID:  "wallet-1",
		ToWalletID:    "wallet-2",
		AmountCents:   100,
		Currency:      "USD",
		OccurredAt:    occurredAt,
		TraceID:       "trace-1",
	}
	flagged := FraudFlagged{
		SchemaVersion: 1,
		EventID:       FraudFlaggedEventID(requested.EventID),
		SourceEventID: requested.EventID,
		PaymentID:     "payment-1",
		SessionID:     "session-1",
		Action:        FraudActionFlag,
		RiskScore:     0.8,
		Reason:        "velocity",
		ProviderID:    "provider-1",
		ModelID:       "model-1",
		OccurredAt:    occurredAt,
		TraceID:       "trace-1",
	}
	fraudError := FraudError{
		SchemaVersion: 1,
		EventID:       FraudErrorEventID(requested.EventID, FraudReasonModelFailed),
		SourceEventID: requested.EventID,
		PaymentID:     "payment-1",
		ReasonCode:    FraudReasonModelFailed,
		OccurredAt:    occurredAt,
		TraceID:       "trace-1",
	}
	paused := TxPaused{
		SchemaVersion: 1,
		EventID:       TxPausedEventID("payment-1"),
		PaymentID:     "payment-1",
		SessionID:     "session-1",
		Action:        FraudActionBlock,
		RiskScore:     0.95,
		Reason:        "velocity",
		PausedAt:      occurredAt,
		OccurredAt:    occurredAt,
		TraceID:       "trace-1",
	}

	for name, validate := range map[string]func() error{
		"requested": requested.Validate,
		"flagged":   flagged.Validate,
		"error":     fraudError.Validate,
		"paused":    paused.Validate,
	} {
		if err := validate(); err != nil {
			t.Fatalf("%s Validate: %v", name, err)
		}
	}

	requested.EventID = "wrong"
	flagged.EventID = "wrong"
	fraudError.EventID = "wrong"
	paused.EventID = "wrong"
	for name, validate := range map[string]func() error{
		"requested": requested.Validate,
		"flagged":   flagged.Validate,
		"error":     fraudError.Validate,
		"paused":    paused.Validate,
	} {
		if err := validate(); err == nil {
			t.Fatalf("%s accepted unstable event ID", name)
		}
	}
}

func TestFraudContractsRejectUnknownEnumsAndSchemaVersions(t *testing.T) {
	flagged := FraudFlagged{
		SchemaVersion: 2,
		EventID:       "fraud.flagged:source-1",
		SourceEventID: "source-1",
		PaymentID:     "payment-1",
		SessionID:     "session-1",
		Action:        "review",
		Reason:        "reason",
		ProviderID:    "provider-1",
		ModelID:       "model-1",
		OccurredAt:    time.Now().UTC(),
		TraceID:       "trace-1",
	}
	if err := flagged.Validate(); err == nil {
		t.Fatal("expected invalid schema/action")
	}
	fraudError := FraudError{
		SchemaVersion: 1,
		EventID:       "fraud.error:source-1:unknown",
		SourceEventID: "source-1",
		PaymentID:     "payment-1",
		ReasonCode:    "unknown",
		OccurredAt:    time.Now().UTC(),
		TraceID:       "trace-1",
	}
	if err := fraudError.Validate(); err == nil {
		t.Fatal("expected invalid reason code")
	}
}

func TestFraudContractsRejectMissingRequiredFieldsAndNonUTCTimestamps(t *testing.T) {
	base := FraudScoreRequested{
		SchemaVersion: 1,
		EventID:       FraudScoreRequestedEventID("payment-1"),
		PaymentID:     "payment-1",
		UserID:        "user-1",
		FromWalletID:  "wallet-1",
		ToWalletID:    "wallet-2",
		AmountCents:   100,
		Currency:      "USD",
		OccurredAt:    time.Now().In(time.FixedZone("offset", 3600)),
		TraceID:       "trace-1",
	}
	if err := base.Validate(); err == nil {
		t.Fatal("expected non-UTC occurred_at to be rejected")
	}
	base.OccurredAt = time.Now().UTC()
	base.TraceID = ""
	if err := base.Validate(); err == nil {
		t.Fatal("expected missing trace_id to be rejected")
	}
}

func TestFraudContractsRejectRequiredFieldOmissions(t *testing.T) {
	occurredAt := time.Date(2026, time.June, 6, 0, 0, 0, 0, time.UTC)
	requested := FraudScoreRequested{
		SchemaVersion: 1, EventID: FraudScoreRequestedEventID("payment-1"),
		PaymentID: "payment-1", UserID: "user-1", FromWalletID: "wallet-1",
		ToWalletID: "wallet-2", AmountCents: 100, Currency: "USD",
		OccurredAt: occurredAt, TraceID: "trace-1",
	}
	flagged := FraudFlagged{
		SchemaVersion: 1, EventID: FraudFlaggedEventID(requested.EventID),
		SourceEventID: requested.EventID, PaymentID: "payment-1", SessionID: "session-1",
		Action: FraudActionFlag, RiskScore: 0.8, Reason: "velocity",
		ProviderID: "provider-1", ModelID: "model-1", OccurredAt: occurredAt, TraceID: "trace-1",
	}
	fraudError := FraudError{
		SchemaVersion: 1, EventID: FraudErrorEventID(requested.EventID, FraudReasonModelFailed),
		SourceEventID: requested.EventID, PaymentID: "payment-1", ReasonCode: FraudReasonModelFailed,
		OccurredAt: occurredAt, TraceID: "trace-1",
	}
	paused := TxPaused{
		SchemaVersion: 1, EventID: TxPausedEventID("payment-1"), PaymentID: "payment-1",
		SessionID: "session-1", Action: FraudActionBlock, RiskScore: 0.9, Reason: "velocity",
		PausedAt: occurredAt, OccurredAt: occurredAt, TraceID: "trace-1",
	}

	tests := []struct {
		name     string
		validate func() error
	}{
		{"requested user_id", func() error { value := requested; value.UserID = ""; return value.Validate() }},
		{"requested from_wallet_id", func() error { value := requested; value.FromWalletID = ""; return value.Validate() }},
		{"requested to_wallet_id", func() error { value := requested; value.ToWalletID = ""; return value.Validate() }},
		{"requested amount_cents", func() error { value := requested; value.AmountCents = 0; return value.Validate() }},
		{"requested currency", func() error { value := requested; value.Currency = ""; return value.Validate() }},
		{"requested occurred_at", func() error { value := requested; value.OccurredAt = time.Time{}; return value.Validate() }},
		{"flagged session_id", func() error { value := flagged; value.SessionID = ""; return value.Validate() }},
		{"flagged reason", func() error { value := flagged; value.Reason = ""; return value.Validate() }},
		{"flagged provider_id", func() error { value := flagged; value.ProviderID = ""; return value.Validate() }},
		{"flagged model_id", func() error { value := flagged; value.ModelID = ""; return value.Validate() }},
		{"flagged occurred_at", func() error { value := flagged; value.OccurredAt = time.Time{}; return value.Validate() }},
		{"error payment_id", func() error { value := fraudError; value.PaymentID = ""; return value.Validate() }},
		{"error occurred_at", func() error { value := fraudError; value.OccurredAt = time.Time{}; return value.Validate() }},
		{"paused session_id", func() error { value := paused; value.SessionID = ""; return value.Validate() }},
		{"paused reason", func() error { value := paused; value.Reason = ""; return value.Validate() }},
		{"paused paused_at", func() error { value := paused; value.PausedAt = time.Time{}; return value.Validate() }},
		{"paused occurred_at", func() error { value := paused; value.OccurredAt = time.Time{}; return value.Validate() }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.validate(); err == nil {
				t.Fatal("expected required field omission to be rejected")
			}
		})
	}
}

func TestFraudErrorJSONDoesNotExposeUnboundedErrorDetails(t *testing.T) {
	payload, err := json.Marshal(FraudError{
		SchemaVersion: 1,
		EventID:       FraudErrorEventID("source-1", FraudReasonModelFailed),
		SourceEventID: "source-1",
		PaymentID:     "payment-1",
		ReasonCode:    FraudReasonModelFailed,
	})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, exists := decoded["error"]; exists {
		t.Fatal("fraud.error must not contain raw error")
	}
}
