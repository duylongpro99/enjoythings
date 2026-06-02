# Phase 2.4: Wallet Outbox and Kafka Publisher

**Priority:** P4 - durable event production  
**Session size:** One implementation session  
**Depends on:** `01-wallet-grpc-service.md`

## Goal

Make wallet transfers persist `tx.initiated` events atomically with balance updates and publish them to Kafka through an outbox publisher.

## Problem

Publishing directly to Kafka after a database commit can lose events if the process crashes between the commit and publish. Phase 2 requires transaction events to flow asynchronously, so wallet needs an outbox table before ledger can safely consume from Kafka.

## Structure

Add an `outbox_events` table to the wallet database. During `InitiateTransfer`, the wallet service updates balances, records the transfer, and inserts an outbox row in the same Postgres transaction. A background publisher polls unpublished rows, publishes JSON to Kafka topic `tx.initiated` with partition key `from_wallet_id`, and marks rows as published after successful Kafka acknowledgement.

## Scope

- Add wallet outbox migration.
- Add outbox repository methods for enqueue, claim/list unpublished, and mark published.
- Add Kafka producer using `franz-go`.
- Add background publisher lifecycle to `cmd/wallet`.
- Add config for Kafka brokers, topic, poll interval, and batch size.
- Ensure transfer response does not wait for Kafka publication beyond the database transaction.

## Out of Scope

- Ledger Kafka consumer.
- Exactly-once Kafka semantics.
- Protobuf event payloads.
- Outbox cleanup or retention jobs.
- Distributed tracing.

## Files

- Create wallet outbox migration under `services/db/migrations` or a wallet-specific migration path chosen during implementation.
- Create `services/internal/outbox/repo.go`.
- Create `services/internal/outbox/publisher.go`.
- Create `services/internal/wallet/events.go` if transfer-to-event mapping needs a focused package.
- Modify wallet transfer transaction code to insert outbox rows.
- Modify `services/cmd/wallet/main.go` to start and stop the publisher.

## Data Flow

```
WalletService.InitiateTransfer
  -> BEGIN
  -> lock wallets
  -> update balances
  -> insert transfer
  -> insert outbox_events(topic='tx.initiated', partition_key=from_wallet_id, payload=json)
  -> COMMIT
  -> return response

Outbox publisher
  -> poll unpublished rows
  -> publish to Kafka
  -> mark published
```

## Error Handling

- If enqueueing the outbox event fails, the transfer transaction rolls back.
- If Kafka publishing fails, the outbox row remains unpublished and is retried.
- If marking published fails after Kafka publish succeeds, the event may be published again; the ledger consumer must deduplicate by `transfer_id`.
- Publisher logs safe metadata only: outbox ID, topic, partition key, and error. Do not log credentials or raw secrets.

## Testing

- Transfer tests verify an outbox row is created in the same transaction as balance updates.
- Rollback tests verify failed transfers do not create outbox rows.
- Publisher tests use a fake Kafka producer and verify successful rows are marked published.
- Publisher failure tests verify rows remain unpublished.
- Run `go test ./...` from `services`.

## Acceptance Criteria

- Every successful transfer creates exactly one unpublished `tx.initiated` outbox event before publisher processing.
- Event payload matches `TransactionInitiated` schema.
- Kafka partition key is `from_wallet_id`.
- Publisher retries unpublished rows after transient publish failures.
- Wallet service can start with publisher enabled in local configuration.

## Tradeoffs

The outbox adds polling complexity, but it is the simplest reliable pattern for Phase 2 because it avoids a distributed transaction between Postgres and Kafka.

