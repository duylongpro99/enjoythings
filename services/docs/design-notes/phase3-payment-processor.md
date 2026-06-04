# Phase 3 Payment Processor

Problem: The saga needs an external payment step that can be retried safely without charging twice. The processor must persist local status by `payment_id`, publish result events only after local state is durable, and avoid committing Kafka offsets until the command outcome is safe.

Structure: `internal/paymentprocessor` owns command handling, retry policy, rail abstraction, persistence, and the Kafka consumer. `PostgresStore` persists `payment_attempts`; `Processor` transitions attempts from `PENDING` to `COMPLETED` or `FAILED`; `HTTPRail` calls the stub rail; and the DB outbox publishes `payment.completed` or `payment.failed`. `cmd/payment-processor` wires Kafka, Postgres, the outbox publisher, and the HTTP rail. `cmd/stub-payment-rail` provides deterministic local rail behavior for success, retry-once, terminal failure, and timeout scenarios.

Tradeoffs: The payment attempt store uses direct SQL rather than adding sqlc queries because existing saga persistence already follows this pattern and the API surface is small. Result events are republished for duplicate terminal attempts, which keeps Kafka redelivery idempotent without recharging. The stub rail uses deterministic `payment_id` substrings instead of a separate scenario database, keeping local testing simple while staying out of real provider scope.
