# Go Services Dev Mode Design Note

Problem: Developers need to run Go services in watch mode without rebuilding API containers for every code change. The workflow must also scale beyond the current `cmd/api` service as more Go commands are added.

Structure: Keep stateful external dependencies in Docker Compose and run Go services locally through one parameterized dev entry point. `make dev SERVICE=api` starts Postgres if needed and invokes a file watcher that runs `go run ./cmd/<service>` with local environment defaults. The watcher config uses an environment-provided command so future `cmd/<name>` services can reuse the same configuration.

Tradeoffs: Use the `air` watcher because it is the established lightweight Go dev-reload tool and avoids building a container for the API. Do not add Docker-based dev service images because that preserves the slow build loop the change is meant to remove. Do not create a custom watcher because it would add maintenance burden for behavior a standard tool already provides.
