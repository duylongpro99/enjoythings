package verification

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
)

const (
	StatusUnverified = "unverified"
	StatusPending    = "pending"
	StatusVerified   = "verified"
	StatusRejected   = "rejected"

	ModeAuto   = "auto"
	ModeManual = "manual"
	ModeRules  = "rules"

	DecisionApprove = "approve"
	DecisionReject  = "reject"
	DecisionPending = "pending"

	UserVerifiedTopic = "user.verified"
	UserRejectedTopic = "user.rejected"
)

var (
	ErrInvalidArgument        = errors.New("invalid verification argument")
	ErrNotFound               = errors.New("verification not found")
	ErrFailedPrecondition     = errors.New("verification state transition is not allowed")
	ErrIdempotencyKeyConflict = errors.New("idempotency key conflicts with another verification request")
)

type Config struct {
	Mode string
}

type Clock interface {
	Now() time.Time
}

type Outbox interface {
	Enqueue(context.Context, string, string, []byte) error
}

type Store interface {
	Submit(context.Context, SubmitCommand, Decision, time.Time) (SubmitResult, error)
	Decide(context.Context, DecisionCommand, Decision, time.Time) (SubmitResult, error)
	GetStatus(context.Context, string) (Record, error)
}

type Service struct {
	store  Store
	outbox Outbox
	cfg    Config
	clock  Clock
}

type SubmitCommand struct {
	PaymentID      string
	IdempotencyKey string
	TraceID        string
	UserID         string
	VerificationID string
	Decision       string
	Reason         string
}

type Decision struct {
	Status string
	Reason string
}

type DecisionCommand struct {
	UserID   string
	TraceID  string
	Decision string
	Reason   string
}

type SubmitResult struct {
	Record           Record
	Transitioned     bool
	TransitionedFrom string
}

type Record struct {
	VerificationID string
	UserID         string
	Status         string
	Reason         string
	TraceID        string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	DecidedAt      time.Time
}

func NewService(store Store, outbox Outbox, cfg Config, clock Clock) *Service {
	if cfg.Mode == "" {
		cfg.Mode = ModeAuto
	}
	if clock == nil {
		clock = systemClock{}
	}
	return &Service{store: store, outbox: outbox, cfg: cfg, clock: clock}
}

func (service *Service) Submit(ctx context.Context, cmd SubmitCommand) (Record, error) {
	cmd.UserID = strings.TrimSpace(cmd.UserID)
	cmd.IdempotencyKey = strings.TrimSpace(cmd.IdempotencyKey)
	cmd.Decision = strings.TrimSpace(strings.ToLower(cmd.Decision))
	if cmd.UserID == "" || cmd.IdempotencyKey == "" {
		return Record{}, ErrInvalidArgument
	}
	decision, err := service.decide(cmd)
	if err != nil {
		return Record{}, err
	}
	result, err := service.store.Submit(ctx, cmd, decision, service.clock.Now())
	if err != nil {
		return Record{}, err
	}
	if result.Transitioned && isTerminalEventStatus(result.Record.Status) && service.outbox != nil {
		if err := service.publish(ctx, result.Record); err != nil {
			return Record{}, err
		}
	}
	return result.Record, nil
}

func (service *Service) GetStatus(ctx context.Context, userID string) (Record, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return Record{}, ErrInvalidArgument
	}
	return service.store.GetStatus(ctx, userID)
}

func (service *Service) Decide(ctx context.Context, cmd DecisionCommand) (Record, error) {
	cmd.UserID = strings.TrimSpace(cmd.UserID)
	cmd.Decision = strings.TrimSpace(strings.ToLower(cmd.Decision))
	if cmd.UserID == "" {
		return Record{}, ErrInvalidArgument
	}
	decision, err := decisionFromAdminCommand(cmd)
	if err != nil {
		return Record{}, err
	}
	result, err := service.store.Decide(ctx, cmd, decision, service.clock.Now())
	if err != nil {
		return Record{}, err
	}
	if result.Transitioned && isTerminalEventStatus(result.Record.Status) && service.outbox != nil {
		if err := service.publish(ctx, result.Record); err != nil {
			return Record{}, err
		}
	}
	return result.Record, nil
}

func (service *Service) decide(cmd SubmitCommand) (Decision, error) {
	switch strings.ToLower(service.cfg.Mode) {
	case "", ModeAuto:
		return Decision{Status: StatusVerified}, nil
	case ModeManual:
		return Decision{Status: StatusPending, Reason: cmd.Reason}, nil
	case ModeRules:
		switch cmd.Decision {
		case "", DecisionPending:
			return Decision{Status: StatusPending, Reason: cmd.Reason}, nil
		case DecisionApprove, StatusVerified:
			return Decision{Status: StatusVerified, Reason: cmd.Reason}, nil
		case DecisionReject, StatusRejected:
			return Decision{Status: StatusRejected, Reason: cmd.Reason}, nil
		default:
			return Decision{}, ErrInvalidArgument
		}
	default:
		return Decision{}, ErrInvalidArgument
	}
}

func decisionFromAdminCommand(cmd DecisionCommand) (Decision, error) {
	switch cmd.Decision {
	case DecisionApprove, StatusVerified:
		return Decision{Status: StatusVerified, Reason: cmd.Reason}, nil
	case DecisionReject, StatusRejected:
		return Decision{Status: StatusRejected, Reason: cmd.Reason}, nil
	default:
		return Decision{}, ErrInvalidArgument
	}
}

func (service *Service) publish(ctx context.Context, record Record) error {
	topic := UserVerifiedTopic
	timestampField := "verified_at"
	if record.Status == StatusRejected {
		topic = UserRejectedTopic
		timestampField = "rejected_at"
	}
	occurredAt := record.DecidedAt
	if occurredAt.IsZero() {
		occurredAt = record.UpdatedAt
	}
	payload := map[string]string{
		"event_id":        newEventID(),
		"user_id":         record.UserID,
		"verification_id": record.VerificationID,
		"trace_id":        record.TraceID,
		timestampField:    occurredAt.Format(time.RFC3339Nano),
		"occurred_at":     occurredAt.Format(time.RFC3339Nano),
	}
	if record.Status == StatusRejected {
		payload["reason"] = record.Reason
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return service.outbox.Enqueue(ctx, topic, record.UserID, encoded)
}

func isTerminalEventStatus(status string) bool {
	return status == StatusVerified || status == StatusRejected
}

func newVerificationID() string {
	return "ver_" + randomHex(16)
}

func newEventID() string {
	return "evt_" + randomHex(16)
}

func randomHex(size int) string {
	bytes := make([]byte, size)
	if _, err := rand.Read(bytes); err != nil {
		return hex.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(bytes)
}

type systemClock struct{}

func (systemClock) Now() time.Time {
	return time.Now().UTC()
}

type memoryStore struct {
	mu      sync.Mutex
	records map[string]Record
	keys    map[string]string
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		records: map[string]Record{},
		keys:    map[string]string{},
	}
}

func (store *memoryStore) Submit(_ context.Context, cmd SubmitCommand, decision Decision, now time.Time) (SubmitResult, error) {
	store.mu.Lock()
	defer store.mu.Unlock()

	keyOwner, keyExists := store.keys[cmd.IdempotencyKey]
	if keyExists && keyOwner != cmd.UserID {
		return SubmitResult{}, ErrIdempotencyKeyConflict
	}
	existing, exists := store.records[cmd.UserID]
	if exists {
		if keyExists {
			return SubmitResult{Record: existing}, nil
		}
		if existing.Status == StatusVerified || existing.Status == StatusRejected {
			return SubmitResult{Record: existing}, nil
		}
	}

	record := existing
	if !exists {
		record = Record{
			VerificationID: strings.TrimSpace(cmd.VerificationID),
			UserID:         cmd.UserID,
			CreatedAt:      now,
		}
		if record.VerificationID == "" {
			record.VerificationID = newVerificationID()
		}
	}
	from := record.Status
	if from == "" {
		from = StatusUnverified
	}
	record.Status = decision.Status
	record.Reason = decision.Reason
	record.TraceID = cmd.TraceID
	record.UpdatedAt = now
	if record.Status == StatusVerified || record.Status == StatusRejected {
		record.DecidedAt = now
	}
	store.records[cmd.UserID] = record
	store.keys[cmd.IdempotencyKey] = cmd.UserID
	return SubmitResult{
		Record:           record,
		Transitioned:     from != record.Status,
		TransitionedFrom: from,
	}, nil
}

func (store *memoryStore) GetStatus(_ context.Context, userID string) (Record, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	record, ok := store.records[userID]
	if !ok {
		return Record{}, ErrNotFound
	}
	return record, nil
}

func (store *memoryStore) Decide(_ context.Context, cmd DecisionCommand, decision Decision, now time.Time) (SubmitResult, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	record, ok := store.records[cmd.UserID]
	if !ok {
		return SubmitResult{}, ErrNotFound
	}
	if record.Status == StatusVerified || record.Status == StatusRejected {
		return SubmitResult{Record: record}, nil
	}
	from := record.Status
	record.Status = decision.Status
	record.Reason = decision.Reason
	record.TraceID = cmd.TraceID
	record.UpdatedAt = now
	record.DecidedAt = now
	store.records[cmd.UserID] = record
	return SubmitResult{Record: record, Transitioned: from != record.Status, TransitionedFrom: from}, nil
}
