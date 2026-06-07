# Phase 4 Fraud Contracts and Events

Problem: The asynchronous fraud path already has partial Go events, Ledger RPCs, and a
Python integration boundary, but the contracts do not yet enforce stable event IDs,
all required fields, UTC timestamps, or a reproducible Python protobuf generation
workflow.

Structure: Keep shared Kafka payload validation and topic metadata in
`services/internal/event`. Keep sanitized enrichment messages in the existing Ledger
and Verification protobuf packages. Generate Python protobuf and gRPC modules into
`app.fraud.integrations.gen`, and allow only `app.fraud.integrations.grpc_client` to
import and map those generated transport types into fraud domain DTOs.

Tradeoffs: A schema registry and a cross-language event package would provide stronger
runtime governance, but they add infrastructure outside this Phase 4 contract scope.
Focused validators, stable ID helpers, committed generated clients, and boundary tests
provide the required guarantees with the existing Go and Python tooling.
