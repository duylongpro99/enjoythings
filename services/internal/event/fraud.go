package event

import (
	"errors"
	"fmt"
	"time"
)

const (
	FraudScoreRequestedTopic = "fraud.score.requested"
	FraudFlaggedTopic        = "fraud.flagged"
	FraudErrorTopic          = "fraud.error"
	TxPausedTopic            = "tx.paused"

	FraudActionFlag  = "flag"
	FraudActionBlock = "block"

	FraudReasonEnrichmentFailed = "enrichment_failed"
	FraudReasonPromptRejected   = "prompt_rejected"
	FraudReasonModelFailed      = "model_failed"
	FraudReasonValidationFailed = "validation_failed"
	FraudReasonAuditFailed      = "audit_failed"
	FraudReasonPublishFailed    = "publish_failed"
)

var ErrInvalidFraudEvent = errors.New("invalid fraud event")

type FraudScoreRequested struct {
	SchemaVersion int       `json:"schema_version"`
	EventID       string    `json:"event_id"`
	PaymentID     string    `json:"payment_id"`
	UserID        string    `json:"user_id"`
	FromWalletID  string    `json:"from_wallet_id"`
	ToWalletID    string    `json:"to_wallet_id"`
	AmountCents   int64     `json:"amount_cents"`
	Currency      string    `json:"currency"`
	OccurredAt    time.Time `json:"occurred_at"`
	TraceID       string    `json:"trace_id"`
}

func (event FraudScoreRequested) Validate() error {
	if event.SchemaVersion != 1 || event.EventID == "" || event.PaymentID == "" ||
		event.UserID == "" || event.FromWalletID == "" || event.ToWalletID == "" ||
		event.AmountCents <= 0 || event.Currency == "" {
		return ErrInvalidFraudEvent
	}
	return nil
}

type FraudFlagged struct {
	SchemaVersion int       `json:"schema_version"`
	EventID       string    `json:"event_id"`
	SourceEventID string    `json:"source_event_id"`
	PaymentID     string    `json:"payment_id"`
	SessionID     string    `json:"session_id"`
	Action        string    `json:"action"`
	RiskScore     float64   `json:"risk_score"`
	Reason        string    `json:"reason"`
	ProviderID    string    `json:"provider_id"`
	ModelID       string    `json:"model_id"`
	OccurredAt    time.Time `json:"occurred_at"`
	TraceID       string    `json:"trace_id"`
}

func (event FraudFlagged) Validate() error {
	if event.SchemaVersion != 1 || event.EventID == "" || event.SourceEventID == "" ||
		event.PaymentID == "" || event.SessionID == "" ||
		(event.Action != FraudActionFlag && event.Action != FraudActionBlock) ||
		event.RiskScore < 0 || event.RiskScore > 1 {
		return ErrInvalidFraudEvent
	}
	return nil
}

type FraudError struct {
	SchemaVersion int       `json:"schema_version"`
	EventID       string    `json:"event_id"`
	SourceEventID string    `json:"source_event_id"`
	PaymentID     string    `json:"payment_id"`
	SessionID     string    `json:"session_id,omitempty"`
	ReasonCode    string    `json:"reason_code"`
	OccurredAt    time.Time `json:"occurred_at"`
	TraceID       string    `json:"trace_id"`
}

func (event FraudError) Validate() error {
	if event.SchemaVersion != 1 || event.EventID == "" || event.SourceEventID == "" ||
		event.PaymentID == "" || !validFraudReason(event.ReasonCode) {
		return ErrInvalidFraudEvent
	}
	return nil
}

type TxPaused struct {
	SchemaVersion int       `json:"schema_version"`
	EventID       string    `json:"event_id"`
	PaymentID     string    `json:"payment_id"`
	SessionID     string    `json:"session_id"`
	Action        string    `json:"action"`
	RiskScore     float64   `json:"risk_score"`
	Reason        string    `json:"reason"`
	PausedAt      time.Time `json:"paused_at"`
	OccurredAt    time.Time `json:"occurred_at"`
	TraceID       string    `json:"trace_id"`
}

func FraudScoreRequestedEventID(paymentID string) string {
	return FraudScoreRequestedTopic + ":" + paymentID
}

func FraudFlaggedEventID(sourceEventID string) string {
	return FraudFlaggedTopic + ":" + sourceEventID
}

func FraudErrorEventID(sourceEventID, reasonCode string) string {
	return fmt.Sprintf("%s:%s:%s", FraudErrorTopic, sourceEventID, reasonCode)
}

func TxPausedEventID(paymentID string) string {
	return TxPausedTopic + ":" + paymentID
}

func validFraudReason(reason string) bool {
	switch reason {
	case FraudReasonEnrichmentFailed, FraudReasonPromptRejected, FraudReasonModelFailed,
		FraudReasonValidationFailed, FraudReasonAuditFailed, FraudReasonPublishFailed:
		return true
	default:
		return false
	}
}
