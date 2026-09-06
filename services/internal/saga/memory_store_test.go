package saga

import (
	"cmp"
	"context"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"
)

type memoryStore struct {
	byPaymentID map[string]Saga
	byUserKey   map[string]string
	audits      map[string]FraudAuditRecord
	createCalls int
}

type atomicMemoryStore struct {
	*memoryStore
	events []OutboxRecord
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		byPaymentID: make(map[string]Saga),
		byUserKey:   make(map[string]string),
		audits:      make(map[string]FraudAuditRecord),
	}
}

func (store *memoryStore) Create(_ context.Context, saga Saga) (Saga, error) {
	store.createCalls++
	userKey := saga.UserID + ":" + saga.IdempotencyKey
	if paymentID, ok := store.byUserKey[userKey]; ok {
		existing := store.byPaymentID[paymentID]
		if sameStartPayload(existing, saga) {
			return existing, nil
		}
		return Saga{}, ErrAlreadyExists
	}
	if existing, ok := store.byPaymentID[saga.PaymentID]; ok {
		if sameStartPayload(existing, saga) {
			return existing, nil
		}
		return Saga{}, ErrAlreadyExists
	}
	if saga.ID == "" {
		saga.ID = uuid.NewString()
	}
	store.byPaymentID[saga.PaymentID] = saga
	store.byUserKey[userKey] = saga.PaymentID
	return saga, nil
}

func (store *memoryStore) GetByPaymentID(_ context.Context, paymentID string) (Saga, error) {
	saga, ok := store.byPaymentID[paymentID]
	if !ok {
		return Saga{}, ErrNotFound
	}
	return saga, nil
}

func (store *memoryStore) ListNonTerminal(context.Context) ([]Saga, error) {
	sagas := make([]Saga, 0, len(store.byPaymentID))
	for _, saga := range store.byPaymentID {
		if saga.State != StateCompleted && saga.State != StateFailed {
			sagas = append(sagas, saga)
		}
	}
	return sagas, nil
}

func (store *memoryStore) ListFraudReview(context.Context) ([]Saga, error) {
	var sagas []Saga
	for _, saga := range store.byPaymentID {
		if saga.State == StateFraudReview {
			sagas = append(sagas, saga)
		}
	}
	slices.SortFunc(sagas, func(left, right Saga) int {
		return cmp.Or(left.FraudFlaggedAt.Compare(right.FraudFlaggedAt), cmp.Compare(left.ID, right.ID))
	})
	return sagas, nil
}

func (store *memoryStore) ListFraudAudit(_ context.Context, paymentID string) ([]FraudAuditRecord, error) {
	var audits []FraudAuditRecord
	for _, audit := range store.audits {
		if audit.PaymentID == paymentID {
			audits = append(audits, audit)
		}
	}
	slices.SortFunc(audits, func(left, right FraudAuditRecord) int {
		return cmp.Or(left.CreatedAt.Compare(right.CreatedAt), cmp.Compare(left.EventID, right.EventID))
	})
	return audits, nil
}

func (store *memoryStore) Update(_ context.Context, saga Saga) (Saga, error) {
	if _, ok := store.byPaymentID[saga.PaymentID]; !ok {
		return Saga{}, ErrNotFound
	}
	store.byPaymentID[saga.PaymentID] = saga
	return saga, nil
}

func (store *atomicMemoryStore) UpdateWithOutbox(ctx context.Context, saga Saga, events []OutboxRecord) (Saga, error) {
	return store.UpdateWithOutboxAndAudit(ctx, saga, events, FraudAuditRecord{})
}

func (store *memoryStore) UpdateWithAudit(ctx context.Context, saga Saga, audit FraudAuditRecord) (Saga, error) {
	updated, err := store.Update(ctx, saga)
	if err != nil {
		return Saga{}, err
	}
	if err := store.RecordFraudAudit(ctx, audit); err != nil {
		return Saga{}, err
	}
	return updated, nil
}

func (store *atomicMemoryStore) UpdateWithOutboxAndAudit(ctx context.Context, saga Saga, events []OutboxRecord, audit FraudAuditRecord) (Saga, error) {
	updated, err := store.Update(ctx, saga)
	if err != nil {
		return Saga{}, err
	}
	store.events = append(store.events, events...)
	if err := store.RecordFraudAudit(ctx, audit); err != nil {
		return Saga{}, err
	}
	return updated, nil
}

func (store *memoryStore) RecordFraudAudit(_ context.Context, audit FraudAuditRecord) error {
	if audit.EventID == "" {
		return nil
	}
	if _, ok := store.audits[audit.EventID]; ok {
		return nil
	}
	store.audits[audit.EventID] = audit
	return nil
}

func (store *memoryStore) DeleteFraudAuditBefore(_ context.Context, before time.Time, limit int) (int64, error) {
	expired := make([]FraudAuditRecord, 0, len(store.audits))
	for _, audit := range store.audits {
		if !audit.CreatedAt.Before(before) {
			continue
		}
		if saga, ok := store.byPaymentID[audit.PaymentID]; ok && saga.State != StateCompleted && saga.State != StateFailed {
			continue
		}
		expired = append(expired, audit)
	}
	sort.Slice(expired, func(i, j int) bool {
		if !expired[i].CreatedAt.Equal(expired[j].CreatedAt) {
			return expired[i].CreatedAt.Before(expired[j].CreatedAt)
		}
		return expired[i].EventID < expired[j].EventID
	})
	if len(expired) > limit {
		expired = expired[:limit]
	}
	for _, audit := range expired {
		delete(store.audits, audit.EventID)
	}
	return int64(len(expired)), nil
}
