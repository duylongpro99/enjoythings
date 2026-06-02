Problem: The ledger service must consume at-least-once Kafka events without duplicating ledger entries, while malformed events and database failures need different offset behavior.

Structure: Keep Kafka polling and offset commits in `internal/ledgerconsumer`, and keep ledger idempotency inside `repo.Database`. The consumer decodes and validates `tx.initiated`, calls one store method, and commits the record only after the store succeeds. The repo method owns the database transaction: check whether the transfer was already processed, lock both wallets, append debit and credit entries, and commit atomically.

Tradeoffs: A dead-letter topic is deferred per the Phase 2 spec, so malformed JSON is treated as a deliberate skip and committed. The existing schema keeps `balance_after`, but the event does not carry historical balances; the append path uses current wallet balances while preserving idempotency by `transfer_id`.
