package event

import (
	"errors"
	"fmt"
	"math"
	"time"
)

const (
	FraudScoreRequestedTopic = "fraud.score.requested"
	FraudFlaggedTopic        = "fraud.flagged"
	FraudErrorTopic          = "fraud.error"
	TxPausedTopic            = "tx.paused"

	TraceparentHeader = "traceparent"
	TracestateHeader  = "tracestate"

	SagaOrchestratorProducer      = "saga-orchestrator"
	FraudWorkerProducer           = "fraud-worker"
	FraudAgentConsumerGroup       = "fraud-agent"
	SagaOrchestratorConsumerGroup = "saga-orchestrator"
	NotificationConsumerGroup     = "notification-service"
	ObservabilityConsumer         = "observability-admin"
	PaymentIDPartitionKey         = "payment_id"

	DuplicateDeduplicateByEventID = "deduplicate_by_event_id"
	DuplicateStableRepublish      = "stable_event_id_republishable"

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

type FraudTopicContract struct {
	Producer          string
	Consumers         []string
	PartitionKey      string
	DuplicateBehavior string
	RequiredHeaders   []string
	OptionalHeaders   []string
}

var FraudTopicContracts = map[string]FraudTopicContract{
	FraudScoreRequestedTopic: {
		Producer:          SagaOrchestratorProducer,
		Consumers:         []string{FraudAgentConsumerGroup},
		PartitionKey:      PaymentIDPartitionKey,
		DuplicateBehavior: DuplicateDeduplicateByEventID,
		RequiredHeaders:   []string{TraceparentHeader},
		OptionalHeaders:   []string{TracestateHeader},
	},
	FraudFlaggedTopic: {
		Producer:          FraudWorkerProducer,
		Consumers:         []string{SagaOrchestratorConsumerGroup, NotificationConsumerGroup},
		PartitionKey:      PaymentIDPartitionKey,
		DuplicateBehavior: DuplicateStableRepublish,
		RequiredHeaders:   []string{TraceparentHeader},
		OptionalHeaders:   []string{TracestateHeader},
	},
	FraudErrorTopic: {
		Producer:          FraudWorkerProducer,
		Consumers:         []string{ObservabilityConsumer},
		PartitionKey:      PaymentIDPartitionKey,
		DuplicateBehavior: DuplicateStableRepublish,
		RequiredHeaders:   []string{TraceparentHeader},
		OptionalHeaders:   []string{TracestateHeader},
	},
	TxPausedTopic: {
		Producer:          SagaOrchestratorProducer,
		Consumers:         []string{NotificationConsumerGroup},
		PartitionKey:      PaymentIDPartitionKey,
		DuplicateBehavior: DuplicateStableRepublish,
		RequiredHeaders:   []string{TraceparentHeader},
		OptionalHeaders:   []string{TracestateHeader},
	},
}

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
	if event.SchemaVersion != 1 || event.EventID != FraudScoreRequestedEventID(event.PaymentID) || event.PaymentID == "" ||
		event.UserID == "" || event.FromWalletID == "" || event.ToWalletID == "" ||
		event.AmountCents <= 0 || event.Currency == "" || !validUTCTime(event.OccurredAt) ||
		event.TraceID == "" {
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
	if event.SchemaVersion != 1 || event.EventID != FraudFlaggedEventID(event.SourceEventID) || event.SourceEventID == "" ||
		event.PaymentID == "" || event.SessionID == "" ||
		(event.Action != FraudActionFlag && event.Action != FraudActionBlock) ||
		math.IsNaN(event.RiskScore) || event.RiskScore < 0 || event.RiskScore > 1 ||
		event.Reason == "" || event.ProviderID == "" || event.ModelID == "" ||
		!validUTCTime(event.OccurredAt) || event.TraceID == "" {
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
	if event.SchemaVersion != 1 ||
		event.EventID != FraudErrorEventID(event.SourceEventID, event.ReasonCode) ||
		event.SourceEventID == "" || event.PaymentID == "" ||
		!validFraudReason(event.ReasonCode) || !validUTCTime(event.OccurredAt) ||
		event.TraceID == "" {
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

func (event TxPaused) Validate() error {
	if event.SchemaVersion != 1 || event.EventID != TxPausedEventID(event.PaymentID) ||
		event.PaymentID == "" || event.SessionID == "" ||
		(event.Action != FraudActionFlag && event.Action != FraudActionBlock) ||
		math.IsNaN(event.RiskScore) || event.RiskScore < 0 || event.RiskScore > 1 ||
		event.Reason == "" || !validUTCTime(event.PausedAt) ||
		!validUTCTime(event.OccurredAt) || event.TraceID == "" {
		return ErrInvalidFraudEvent
	}
	return nil
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

func validUTCTime(value time.Time) bool {
	if value.IsZero() {
		return false
	}
	_, offset := value.Zone()
	return offset == 0
}
