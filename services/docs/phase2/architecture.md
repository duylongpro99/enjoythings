# Architecture — Phase 2: Split into Services

**Phase:** 2 of 4  
**Last updated:** 2026-06-01

---

## 1. System diagram

```
Client (HTTP/JSON)
        │
        ▼
  ┌─────────────┐
  │ API Gateway │  JWT auth · rate limit · REST→gRPC
  └──────┬──────┘
         │ gRPC
    ┌────┴────┐
    ▼         ▼
 Wallet     Ledger
 Service    Service ◄─── Kafka consumer
    │          │
    ▼          ▼
 Postgres   Postgres
 (wallets)  (ledger_events)
    │
    ▼ publish
  Kafka
  tx.initiated
```

---

## 2. New folder structure additions

```
fintech-platform/
├── services/
│   ├── gateway/             # NEW — REST gateway
│   │   ├── cmd/main.go
│   │   └── internal/
│   │       ├── handler/
│   │       ├── middleware/
│   │       └── client/      # gRPC client wrappers
│   ├── wallet/              # Extracted from monolith
│   │   ├── cmd/main.go
│   │   └── internal/
│   │       ├── grpc/        # gRPC server implementation
│   │       ├── service/
│   │       ├── repo/
│   │       └── kafka/       # Kafka producer
│   └── ledger/              # Extracted from monolith
│       ├── cmd/main.go
│       └── internal/
│           ├── grpc/        # gRPC server implementation
│           ├── consumer/    # Kafka consumer
│           ├── service/
│           └── repo/
├── proto/
│   ├── wallet/v1/
│   │   └── wallet.proto
│   └── ledger/v1/
│       └── ledger.proto
└── docker-compose.yml       # Updated: add Kafka + Zookeeper
```

---

## 3. Protobuf definitions

### wallet/v1/wallet.proto
```protobuf
syntax = "proto3";
package wallet.v1;

service WalletService {
  rpc CreateWallet(CreateWalletRequest) returns (CreateWalletResponse);
  rpc GetBalance(GetBalanceRequest) returns (GetBalanceResponse);
  rpc InitiateTransfer(InitiateTransferRequest) returns (InitiateTransferResponse);
}

message InitiateTransferRequest {
  string from_wallet_id = 1;
  string to_wallet_id   = 2;
  int64  amount_cents   = 3;
  string idempotency_key = 4;
}

message InitiateTransferResponse {
  string transfer_id      = 1;
  int64  from_balance     = 2;
  int64  to_balance       = 3;
}
```

### ledger/v1/ledger.proto
```protobuf
syntax = "proto3";
package ledger.v1;

service LedgerService {
  rpc GetEntries(GetEntriesRequest) returns (GetEntriesResponse);
}

message LedgerEntry {
  string id            = 1;
  string wallet_id     = 2;
  string transfer_id   = 3;
  string direction     = 4;  // "debit" | "credit"
  int64  amount_cents  = 5;
  int64  balance_after = 6;
  string created_at    = 7;
}
```

---

## 4. Kafka event schema

### Topic: `tx.initiated`
Partition key: `from_wallet_id` (string)  
Format: JSON (Protobuf in Phase 3)

```json
{
  "transfer_id":    "uuid",
  "from_wallet_id": "uuid",
  "to_wallet_id":   "uuid",
  "amount_cents":   5000,
  "currency":       "USD",
  "initiated_at":   "2026-06-01T10:00:00Z"
}
```

---

## 5. Wallet service — transfer flow

```
gRPC: InitiateTransfer(req)
  │
  ├─ Validate: amount > 0, wallets exist and belong to caller
  │
  ├─ BEGIN TRANSACTION
  │    ├─ SELECT wallets FOR UPDATE (ordered by ID to avoid deadlock)
  │    ├─ Check from.balance >= amount
  │    ├─ UPDATE wallets (debit from, credit to)
  │    ├─ INSERT transfers record
  │    └─ INSERT outbox_events (tx.initiated payload)  ← outbox record
  ├─ COMMIT
  │
  ├─ Background: outbox publisher polls outbox_events, publishes to Kafka, marks sent
  │
  └─ Return gRPC response with new balances
```

The outbox pattern ensures the Kafka publish is atomic with the DB write. If the process crashes after COMMIT but before publishing, the outbox publisher picks it up on restart.

---

## 6. Outbox table

```sql
CREATE TABLE outbox_events (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  topic        TEXT NOT NULL,
  partition_key TEXT NOT NULL,
  payload      JSONB NOT NULL,
  published    BOOLEAN NOT NULL DEFAULT false,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX ON outbox_events (published, created_at)
  WHERE published = false;
```

The outbox publisher runs as a goroutine: polls for `published = false`, publishes to Kafka, sets `published = true`. Poll interval: 100ms. Batch size: 100 events.

---

## 7. Ledger consumer — idempotency

```go
// On receiving tx.initiated event:
func (c *Consumer) Handle(ctx context.Context, msg TransactionInitiated) error {
    // Idempotency: skip if transfer_id already processed
    exists, err := c.repo.LedgerEntryExists(ctx, msg.TransferID)
    if err != nil {
        return err
    }
    if exists {
        return nil // already processed, safe to ack
    }
    // Append debit entry for from_wallet
    // Append credit entry for to_wallet
    return c.repo.AppendEntries(ctx, msg)
}
```

---

## 8. Updated Docker Compose

```yaml
services:
  zookeeper:
    image: confluentinc/cp-zookeeper:7.6.0
    environment:
      ZOOKEEPER_CLIENT_PORT: 2181

  kafka:
    image: confluentinc/cp-kafka:7.6.0
    depends_on: [zookeeper]
    ports:
      - "9092:9092"
    environment:
      KAFKA_BROKER_ID: 1
      KAFKA_ZOOKEEPER_CONNECT: zookeeper:2181
      KAFKA_ADVERTISED_LISTENERS: PLAINTEXT://kafka:9092
      KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR: 1

  postgres-wallet:
    image: postgres:16-alpine
    environment:
      POSTGRES_DB: wallet
      POSTGRES_USER: wallet
      POSTGRES_PASSWORD: secret

  postgres-ledger:
    image: postgres:16-alpine
    environment:
      POSTGRES_DB: ledger
      POSTGRES_USER: ledger
      POSTGRES_PASSWORD: secret

  gateway:
    build: ./services/gateway
    ports: ["8080:8080"]
    environment:
      WALLET_GRPC_ADDR: wallet:9090
      LEDGER_GRPC_ADDR: ledger:9091
      JWT_SECRET: dev-secret

  wallet:
    build: ./services/wallet
    ports: ["9090:9090"]
    environment:
      DATABASE_URL: postgres://wallet:secret@postgres-wallet:5432/wallet
      KAFKA_BROKERS: kafka:9092

  ledger:
    build: ./services/ledger
    ports: ["9091:9091"]
    environment:
      DATABASE_URL: postgres://ledger:secret@postgres-ledger:5432/ledger
      KAFKA_BROKERS: kafka:9092
      KAFKA_GROUP_ID: ledger-service
```

---

## 9. Error propagation

gRPC status codes map to HTTP in the gateway:

| gRPC code | HTTP status | Scenario |
|---|---|---|
| `OK` | 200 | Success |
| `INVALID_ARGUMENT` | 400 | Bad request body |
| `NOT_FOUND` | 404 | Wallet not found |
| `FAILED_PRECONDITION` | 422 | Insufficient funds |
| `INTERNAL` | 500 | Unexpected error |

All gRPC errors include a human-readable message in the `status.Status` details.
