package event

import (
	"encoding/json"
	"testing"
)

func TestFraudScoreRequestedValidateAndStableEventID(t *testing.T) {
	event := FraudScoreRequested{
		SchemaVersion: 1,
		EventID:       FraudScoreRequestedEventID("payment-1"),
		PaymentID:     "payment-1",
		UserID:        "user-1",
		FromWalletID:  "wallet-1",
		ToWalletID:    "wallet-2",
		AmountCents:   100,
		Currency:      "USD",
	}
	if err := event.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if event.EventID != "fraud.score.requested:payment-1" {
		t.Fatalf("event id = %q", event.EventID)
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
	}
	if err := fraudError.Validate(); err == nil {
		t.Fatal("expected invalid reason code")
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
