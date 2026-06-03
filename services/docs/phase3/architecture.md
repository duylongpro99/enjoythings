# Architecture — Phase 3: Resilience and Distributed Patterns

**Phase:** 3 of 4  
**Last updated:** 2026-06-01

---

## 1. System diagram

```
Client
  │
  ▼
API Gateway
  │ gRPC
  ▼
Saga Orchestrator ──gRPC──► Wallet Service ──► Postgres
  │                │
  │                └──gRPC──► Ledger Service ──► Event Store
  │
  ├──gRPC──► Verification Service ──► Postgres
  │
  ├── Kafka: payment.execute ──► Payment Processor ──► Stub Rail
  │                                    │
  │                           payment.completed/failed
  │
  ├── Kafka: tx.completed / tx.failed
  │
  └── Kafka: tx.completed, tx.failed, user.verified, user.rejected ──► Notification Service
```

---

## 2. Saga state machine

```
States: STARTED → VERIFICATION_CHECKED → WALLET_DEBITED →
        LEDGER_RESERVED → PAYMENT_PROCESSING → LEDGER_CONFIRMED →
        COMPLETED

Compensation (reverse):
COMPENSATING_LEDGER → COMPENSATING_WALLET → FAILED
```

### Saga state table

```sql
CREATE TABLE sagas (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  payment_id      UUID NOT NULL UNIQUE,
  idempotency_key TEXT NOT NULL,
  user_id         UUID NOT NULL,
  state           TEXT NOT NULL DEFAULT 'STARTED',
  from_wallet_id  UUID NOT NULL,
  to_wallet_id    UUID NOT NULL,
  amount_cents    BIGINT NOT NULL,
  currency        TEXT NOT NULL,
  last_error      TEXT,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

The orchestrator updates `state` atomically at each step. On restart, it queries for sagas in non-terminal states and resumes them.

### Saga step implementation (Go pseudocode)

```go
func (o *Orchestrator) RunSaga(ctx context.Context, saga *Saga) error {
    switch saga.State {
    case "STARTED":
        if err := o.checkVerification(ctx, saga); err != nil {
            return o.fail(ctx, saga, err)
        }
        saga.State = "VERIFICATION_CHECKED"
        o.repo.Save(ctx, saga)
        fallthrough

    case "VERIFICATION_CHECKED":
        if err := o.debitWallet(ctx, saga); err != nil {
            return o.compensate(ctx, saga, err)
        }
        saga.State = "WALLET_DEBITED"
        o.repo.Save(ctx, saga)
        fallthrough

    case "WALLET_DEBITED":
        if err := o.reserveLedger(ctx, saga); err != nil {
            return o.compensate(ctx, saga, err)
        }
        saga.State = "LEDGER_RESERVED"
        o.repo.Save(ctx, saga)
        fallthrough

    case "LEDGER_RESERVED":
        o.publishPaymentCommand(ctx, saga) // fire to Kafka, wait for reply event
        saga.State = "PAYMENT_PROCESSING"
        o.repo.Save(ctx, saga)
        return nil // resume on payment.completed / payment.failed event

    case "PAYMENT_PROCESSING":
        // triggered by incoming Kafka event
        if saga.LastError != "" {
            return o.compensate(ctx, saga, errors.New(saga.LastError))
        }
        // confirm ledger, publish tx.completed
    }
    return nil
}
```

---

## 3. Payment processor

```
Kafka consumer: payment.execute
  │
  ├─ Deserialise PaymentCommand {payment_id, amount, to_account}
  │
  ├─ Idempotency check: SELECT FROM payments WHERE payment_id = ?
  │   └─ If exists and status = 'completed': publish payment.completed and ack
  │
  ├─ INSERT INTO payments (payment_id, status='pending')
  │
  ├─ HTTP POST to stub payment rail
  │   ├─ Success: UPDATE payments SET status='completed'
  │   │           publish payment.completed
  │   └─ Failure (after retries): UPDATE payments SET status='failed'
  │                               publish payment.failed
  │
  └─ Ack Kafka offset
```

### Retry policy

```go
backoff := []time.Duration{1*time.Second, 3*time.Second, 9*time.Second}
for attempt, wait := range backoff {
    resp, err := httpClient.Post(railURL, ...)
    if err == nil && resp.StatusCode < 500 {
        break
    }
    if attempt == len(backoff)-1 {
        return ErrPaymentFailed
    }
    time.Sleep(wait + jitter())
}
```

---

## 4. Verification service state machine

```
UNVERIFIED ──submit(auto)──► VERIFIED
UNVERIFIED ──submit(manual)──► PENDING ──approve──► VERIFIED
                                  └──reject──► REJECTED
```

```go
type VerificationState string

const (
    Unverified VerificationState = "unverified"
    Pending    VerificationState = "pending"
    Verified   VerificationState = "verified"
    Rejected   VerificationState = "rejected"
)

func (v *Verification) Submit(mode string) error {
    if v.State != Unverified {
        return ErrInvalidTransition
    }
    if mode == "auto" {
        v.State = Verified
        return nil
    }
    v.State = Pending
    return nil
}

func (v *Verification) Approve() error {
    if v.State != Pending {
        return ErrInvalidTransition
    }
    v.State = Verified
    return nil
}
```

`VERIFICATION_MODE=auto` is the default local mode. The Saga Orchestrator calls `VerificationService.GetStatus(user_id)` via gRPC before wallet debit. If status is not `verified`, `StartPaymentSaga` fails with `FAILED_PRECONDITION` and the gateway returns HTTP `422`.

---

## 5. Kubernetes manifests structure

```
charts/
├── gateway/
│   ├── Chart.yaml
│   ├── values.yaml
│   └── templates/
│       ├── deployment.yaml
│       ├── service.yaml
│       ├── hpa.yaml
│       └── configmap.yaml
├── wallet/
├── ledger/
├── verification/
├── saga-orchestrator/
├── payment-processor/
└── notification/
```

### Standard deployment template (values.yaml pattern)

```yaml
replicaCount: 2

image:
  repository: fintech/wallet
  tag: latest

resources:
  requests:
    cpu: 100m
    memory: 128Mi
  limits:
    cpu: 500m
    memory: 256Mi

probes:
  liveness:
    path: /healthz
    initialDelaySeconds: 5
    periodSeconds: 10
  readiness:
    path: /readyz
    initialDelaySeconds: 3
    periodSeconds: 5

autoscaling:
  enabled: true
  minReplicas: 2
  maxReplicas: 5
  targetCPUUtilizationPercentage: 70
```

---

## 6. New Kafka topics in Phase 3

| Topic | Producer | Consumer |
|---|---|---|
| `payment.execute` | Saga Orchestrator | Payment Processor |
| `payment.completed` | Payment Processor | Saga Orchestrator |
| `payment.failed` | Payment Processor | Saga Orchestrator |
| `tx.completed` | Saga Orchestrator | Notification, Ledger |
| `tx.failed` | Saga Orchestrator | Notification |
| `user.verified` | Verification Service | Notification |
| `user.rejected` | Verification Service | Notification |

---

## 7. Structured logging (pre-tracing)

Every service adds these fields to every log line:

```go
log.Info("payment saga started",
    "trace_id",      traceID,      // manually propagated via gRPC metadata for now
    "payment_id",    paymentID,
    "from_wallet_id", fromID,
    "amount_cents",  amount,
    "duration_ms",   duration.Milliseconds(),
)
```

Full OpenTelemetry tracing is added in Phase 4. For now, manually propagate a `trace_id` UUID in gRPC metadata and Kafka message headers so logs can be correlated across services.
