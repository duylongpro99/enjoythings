# Phase 4.8: Prometheus, Grafana, and Runtime

**Priority:** P8 - operational visibility and local wiring
**Session size:** One to two implementation sessions
**Depends on:** P5-P7

## Goal

Expose bounded operational metrics, provision useful dashboards, and run the complete Phase 4 stack locally.

## Scope

- Add metrics endpoints to Go services and the Python fraud worker.
- Add custom saga and fraud metrics.
- Add Prometheus scrape configuration.
- Provision Grafana data sources and dashboards.
- Add Jaeger, Prometheus, Grafana, TimescaleDB, and fraud worker to Compose.
- Extend Helm values/templates where Phase 4 services run in local Kubernetes.

## Fraud Metrics

| Metric | Type | Labels |
|---|---|---|
| `fraud_transactions_scored_total` | Counter | `action`, `provider` |
| `fraud_model_latency_seconds` | Histogram | `provider`, `model` |
| `fraud_risk_score` | Histogram | none |
| `fraud_enrichment_calls_total` | Counter | `method`, `outcome` |
| `fraud_callback_rejections_total` | Counter | `callback`, `reason` |
| `fraud_session_duration_seconds` | Histogram | `outcome` |
| `fraud_events_published_total` | Counter | `topic`, `outcome` |

Labels use bounded enums or configured provider/model IDs. IDs, reasons, exception strings, and Kafka offsets are forbidden labels.

`fraud_callback_rejections_total.reason` uses only the guard and validator rejection codes defined in P2. `fraud_events_published_total.topic` is restricted to `fraud.flagged` and `fraud.error`.

Histogram buckets are explicit:

- `fraud_risk_score`: `0.0` through `1.0` in `0.1` increments.
- `fraud_model_latency_seconds`: `0.1`, `0.25`, `0.5`, `1`, `2.5`, `5`, `10`, `30`.
- `fraud_session_duration_seconds`: `0.25`, `0.5`, `1`, `2.5`, `5`, `10`, `30`, `60`.

## Saga and Service Metrics

- Request rate, error rate, and latency per HTTP/gRPC service.
- Kafka records consumed, produced, failed, and retried by topic.
- Saga duration, step failures, compensation count, and fraud review count.
- Database operation latency by bounded operation name.

## Dashboards

- System overview: service traffic, errors, p99 latency, Kafka failures.
- Saga health: state counts, duration, failure/compensation rates, fraud reviews.
- Fraud agent: scored count, flag rate, score distribution, model latency, enrichment outcomes, guard rejections.

Dashboards are provisioned from version-controlled JSON. No manual setup is required.

## Runtime Rules

- Fraud worker exposes a worker-local metrics server; it does not require the FastAPI process.
- Fraud TimescaleDB uses environment-provided credentials and a dedicated volume.
- Compose health checks gate dependent services.
- `.env.example` documents required variables without real secrets.
- Pinned image versions are used instead of `latest`.
- Prometheus retains local metrics for 7 days; Grafana has no anonymous admin access and receives local credentials from environment variables.
- Dashboard queries use Prometheus only. Direct Grafana access to the fraud audit database is out of scope.

## Acceptance Criteria

- Prometheus discovers all configured targets and scrapes successfully.
- Grafana starts with provisioned data sources and three dashboards.
- Fraud dashboards populate after a local scoring scenario.
- Metric labels remain bounded and contain no identifiers.
- Compose and Helm contract tests cover Phase 4 configuration.
- No Compose service or dashboard configuration contains literal production credentials.
