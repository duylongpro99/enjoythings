# Phase 1 Foundation Design Note

Problem: Phase 1 needs a runnable Go monolith foundation before business endpoints exist. The foundation must provide process startup, config validation, Postgres connectivity, health routing, migration/query conventions, Docker local development, and repeatable developer commands without coupling handlers directly to storage details.

Structure: Keep the API as a single binary under `cmd/api`. Put environment loading in `internal/config`, database pool creation and pinging in `internal/repo`, shared JSON response helpers and health handlers in `internal/handler`, and router setup in `internal/service`. Keep placeholder `domain` and `middleware` packages small so future specs can add behavior without changing the foundation layout.

Tradeoffs: Use Go's standard `net/http` router instead of adding `chi` now because the foundation only owns two unversioned health endpoints. Use `pgxpool` directly for readiness because it is the concrete dependency the runtime must prove. Keep sqlc query files minimal with one harmless query so `sqlc generate` can succeed before capability specs add real SQL.
