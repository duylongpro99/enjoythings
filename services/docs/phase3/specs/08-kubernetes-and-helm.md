# Phase 3.8: Kubernetes and Helm

**Priority:** P8 - deployment after services exist  
**Session size:** One to two implementation sessions  
**Depends on:** P2, P3, P4, P5, P6, P7

## Goal

Deploy the Phase 3 services to a local Kubernetes cluster using Helm charts with health checks, readiness gates, config, and secrets.

## Problem

Phase 2 uses Docker Compose. Phase 3 needs Kubernetes deployment patterns, but charts should be written after service ports, env vars, topics, and dependencies are known.

## Scope

- Add one Helm chart per service.
- Add deployments and services for gateway, saga-orchestrator, wallet, ledger, payment-processor, verification, and notification.
- Add ConfigMaps for non-secret config.
- Add Secrets for database URLs, JWT secret, and stub adapter credentials if needed.
- Add liveness and readiness probes.
- Add HPA for Wallet and Ledger.
- Document local `kind` setup.

## Out of Scope

- Production-grade Kubernetes hardening.
- Multi-region deployment.
- Prometheus, Grafana, and Jaeger.
- Cloud-managed databases or Kafka.

## Chart Layout

```text
services/charts/
├── gateway/
├── saga-orchestrator/
├── wallet/
├── ledger/
├── payment-processor/
├── verification/
└── notification/
```

Each chart includes:

- `Chart.yaml`
- `values.yaml`
- `templates/deployment.yaml`
- `templates/service.yaml`
- `templates/configmap.yaml`
- `templates/secret.yaml` where needed
- `templates/hpa.yaml` where enabled

## Acceptance Criteria

- All Phase 3 services can run in local `kind`.
- Readiness probes prevent traffic before dependencies are ready.
- `kubectl rollout restart` of Wallet causes no accepted request downtime.
- Config is supplied through ConfigMaps and Secrets, not hard-coded values.
- Docker Compose remains available for earlier local workflows unless explicitly retired.
