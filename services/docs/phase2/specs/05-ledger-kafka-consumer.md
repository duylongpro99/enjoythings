# Phase 2.5: Ledger Kafka Consumer

**Priority:** P5 - asynchronous ledger writes  
**Session size:** One implementation session  
**Depends on:** `03-ledger-grpc-service.md`, `04-wallet-outbox-and-kafka-publisher.md`

## Goal

Make the ledger service consume `tx.initiated` events from Kafka and append idempotent debit and credit ledger entries.

## Problem

Kafka provides at-least-once delivery, so the ledger service may see the same transfer event more than once. The ledger must never duplicate accounting entries for a transfer.

## Structure

Add a ledger consumer to `services/cmd/ledger`. It joins consumer group `ledger-service`, reads JSON events from topic `tx.initiated`, validates the event, checks whether `transfer_id` has already been processed, and appends debit and credit entries in one database transaction. Kafka offsets are committed only after the database transaction succeeds.

## Scope

- Add Kafka consumer using `franz-go`.
- Add config for brokers, topic, group ID, and consumer enablement.
- Add ledger repo idempotency query by `transfer_id`.
- Add ledger repo append method that writes debit and credit entries transactionally.
- Start consumer alongside ledger gRPC server.
- Ensure malformed events are handled deliberately.

## Out of Scope

- Saga orchestration.
- Fraud checks.
- Payment processor events.
- Rewriting historical Phase 1 transfers.
- Exactly-once Kafka transactions.

## Files

- Create `services/internal/ledgerconsumer/consumer.go`.
- Create `services/internal/ledgerconsumer/consumer_test.go`.
- Modify ledger repo code for `TransferProcessed` and `AppendTransferEntries`.
- Modify `services/cmd/ledger/main.go` to run consumer lifecycle.
- Add migrations or unique indexes only if needed for transfer idempotency.

## Data Flow

```
Kafka tx.initiated
  -> Ledger consumer
  -> decode TransactionInitiated
  -> BEGIN
  -> check transfer_id not processed
  -> append debit entry for from_wallet_id
  -> append credit entry for to_wallet_id
  -> COMMIT
  -> commit Kafka offset
```

## Error Handling

- Duplicate `transfer_id` is treated as success and the offset may be committed.
- Invalid JSON or schema-invalid events are logged with safe metadata and skipped only after a clear poison-message policy is implemented for Phase 2. The default policy is to skip malformed JSON because retrying cannot fix it.
- Database errors do not commit offsets, allowing Kafka redelivery.
- Context cancellation shuts down the consumer without starting new message handling.

## Testing

- Unit-test duplicate transfer handling: second delivery appends no extra entries.
- Unit-test debit and credit entry creation for a valid event.
- Unit-test malformed JSON handling.
- Integration-test repo transaction behavior if Postgres test helpers already exist.
- Run `go test ./...` from `services`.

## Acceptance Criteria

- Ledger service consumes `tx.initiated` as consumer group `ledger-service`.
- A valid event creates exactly two ledger entries: debit from source wallet and credit to destination wallet.
- Duplicate delivery of the same `transfer_id` creates no duplicate entries.
- Offsets are committed only after successful handling.
- Ledger gRPC reads can return entries created by the consumer.

## Tradeoffs

Deduplicating by `transfer_id` matches the Phase 2 PRD and keeps idempotency local to ledger storage. A full dead-letter topic is deferred because Phase 2 asks for basic Kafka flow, not production-grade event operations.

