# Observability Runbook

This runbook adds eyes and ears to the cluster. Section 7 of
[`k8s-deployment-runbook.md`](./k8s-deployment-runbook.md) gets EnjoyThings
running; nothing in these runbooks so far tells you *why* a request failed,
*which* pod is slow, or *where* a payment got stuck between the saga
orchestrator and the fraud worker. This one installs metrics, logs, traces and
alerting, in that order, and ends with a drill: break something on purpose and
use the four signals together to find it, fix it, and write it up.

Same format as the other runbooks: goal, steps, why, check. Read
[`k8s-deployment-runbook.md`](./k8s-deployment-runbook.md) first; this one
assumes the kind cluster `enjoythings`, the nine images loaded, and the chart
installed into the `enjoythings` namespace. Nothing here edits
`services/charts/enjoythings`; every change to the application pods is a
values overlay, the same pattern `values-local.yaml` already uses.

## Before you start

You need the running cluster from the Kubernetes runbook, `helm` on your
laptop, and about 2 vCPU and 3 GiB of memory free for the observability
namespace on top of what EnjoyThings and kind already use. If your laptop is
tight on resources, lower the `retention` values in `loki-values.yaml` and
`tempo-values.yaml` before installing rather than after.

## What you will end up with

| Thing | Where | Role |
| --- | --- | --- |
| kube-prometheus-stack 90.0.0 | Helm release `monitoring`, namespace `monitoring` | Prometheus Operator, Prometheus, Alertmanager, Grafana, kube-state-metrics, node-exporter. |
| PodMonitor `enjoythings-applications` | `services/k8s/observability/podmonitor-enjoythings.yaml` | Tells Prometheus to scrape every EnjoyThings pod's `/metrics`. |
| Annotation-based scrape config (alternative) | `services/k8s/observability/kube-prometheus-stack-values.annotations.yaml` | Same result, driven by the `prometheus.io/*` pod annotations the chart already sets. |
| PrometheusRule `enjoythings` | `services/k8s/observability/prometheusrule-enjoythings.yaml` | Five alerts on real metrics the services export. |
| Alertmanager route | `kube-prometheus-stack-values.yaml` | Routes every alert to the webhook receiver below. |
| `alert-webhook` Deployment + Service | `services/k8s/observability/alert-webhook.yaml` | Echoes every Alertmanager notification to its own logs. |
| Grafana dashboards | ConfigMaps built by `services/k8s/observability/build-dashboard-configmaps.sh` from `services/observability/grafana/dashboards` | The same three dashboards the Compose stack shows, loaded by the chart's sidecar. |
| Loki 7.3.0 | Helm release `loki`, namespace `monitoring` | Single-binary log store, local disk. |
| Grafana Alloy 1.12.1 | Helm release `alloy`, namespace `monitoring`, DaemonSet | Tails every pod's logs on its node and pushes them to Loki. |
| Tempo 1.24.4 | Helm release `tempo`, namespace `monitoring` | Single-binary trace store, local disk, OTLP on 4317/4318. |
| OpenTelemetry Collector 0.172.1 | Helm release `otel-collector`, namespace `monitoring` | Receives OTLP from the services, forwards to Tempo. |
| `otel-endpoint-values.example.yaml` | `services/k8s/observability` | The values overlay that turns on trace export per application. |
| Terraform stage `27-observability` | `infra/terraform/27-observability` | Optional. Installs the same five Helm releases and the dashboards, the same pattern as stage 20. |
| k9s | your laptop | A terminal UI over the whole cluster: pods, logs, describe, exec, without typing `kubectl` each time. |

---

## 1. Observability vocabulary you need

| Term | What it is |
| --- | --- |
| **Metric** | A number with labels, sampled over time: a request count, a latency bucket, a gauge. Prometheus's unit. |
| **Scrape** | Prometheus pulling `/metrics` from a target on an interval. Nothing is pushed unless something pulls it first. |
| **PodMonitor / ServiceMonitor** | Prometheus Operator custom resources. Each names a label selector; the Operator turns matches into scrape jobs, no `prometheus.yml` edit required. |
| **PrometheusRule** | A custom resource holding alerting and recording rules, hot-loaded by the Operator the same way. |
| **Alertmanager** | Receives firing alerts from Prometheus, groups them, and routes each group to a receiver (webhook, Slack, PagerDuty, ...) on its own schedule. |
| **Receiver / route** | A receiver is a destination. A route is a rule saying which alerts go to which receiver, and how often to repeat. |
| **Log** | An unstructured or structured line a process writes about one thing that happened. Loki's unit. |
| **Stream** | One unique combination of Loki labels (`namespace`, `pod`, `app`, ...). Keep the label set small: a new label value is a new stream, and Loki indexes streams, not log content. |
| **LogQL** | Loki's query language: a label selector in braces, piped through line filters and parsers, e.g. `` {app="gateway"} |= "error" ``. |
| **Trace** | The end-to-end record of one request as it crosses services. Tempo's unit. |
| **Span** | One unit of work inside a trace: one HTTP handler, one database call, one Kafka publish. Spans nest to form the trace tree. |
| **Trace ID / span ID** | The identifiers that tie spans to the same trace and to their parent. Propagated in request headers between services. |
| **OTLP** | OpenTelemetry's wire protocol for metrics, logs and traces. This stack only sends traces over it, on ports 4317 (gRPC) and 4318 (HTTP). |
| **OTel Collector** | A vendor-neutral receiver/processor/exporter pipeline. Here: receive OTLP, batch, forward to Tempo. Services never talk to Tempo directly. |
| **`up`** | A metric Prometheus writes for every scrape target itself: 1 if the last scrape succeeded, 0 if it did not. The cheapest signal that a target has gone quiet. |
| **Datasource UID** | The stable identifier a Grafana dashboard JSON uses to reference a datasource, instead of its human-readable name. The dashboards in this repo were written against uid `prometheus`. |
| **Sidecar (Grafana)** | A container next to Grafana that watches ConfigMaps with a chosen label and writes their contents into Grafana's provisioning directories, so `kubectl apply` is how dashboards and datasources get in. |

---

## 2. Install kube-prometheus-stack

### Goal

Prometheus, Alertmanager, Grafana and kube-state-metrics running in a
`monitoring` namespace, with a Prometheus datasource name that matches what
the existing dashboards expect.

### Steps

```sh
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
helm repo update

helm upgrade --install monitoring prometheus-community/kube-prometheus-stack \
  --version 90.0.0 -n monitoring --create-namespace \
  -f services/k8s/observability/kube-prometheus-stack-values.yaml

kubectl get pods -n monitoring
```

### Why

- `services/k8s/observability/kube-prometheus-stack-values.yaml` turns off
  `kubeControllerManager`, `kubeScheduler`, `kubeProxy` and `kubeEtcd`: kind
  runs the whole control plane inside one container and does not expose these
  on a scrapeable address, so their bundled alerts would fire forever
  otherwise.
- `podMonitorSelectorNilUsesHelmValues`, `serviceMonitorSelectorNilUsesHelmValues`
  and `ruleSelectorNilUsesHelmValues` are all set to `false`. By default the
  Operator only picks up PodMonitors, ServiceMonitors and PrometheusRules
  carrying the label `release: monitoring`; turning this off means "any such
  object in any namespace counts", so `podmonitor-enjoythings.yaml` and
  `prometheusrule-enjoythings.yaml` need no extra label.
- The values file sets `grafana.sidecar.datasources.name: Prometheus` and
  `uid: prometheus` as the chart defaults, spelled out on purpose. Check
  `services/observability/grafana/provisioning/datasources/prometheus.yml`,
  the file the Docker Compose stack uses: `name: Prometheus`, `uid:
  prometheus`. The three dashboard JSON files under
  `services/observability/grafana/dashboards` hardcode `"datasource":
  {"type": "prometheus", "uid": "prometheus"}` on every panel. If the uid
  here ever drifted from `prometheus`, every panel would show "Datasource not
  found" — matching it is not cosmetic, it is what makes the dashboards from
  Compose work unmodified in Kubernetes.
- `additionalDataSources` adds Loki and Tempo up front so sections 4 and 5 do
  not need a second `helm upgrade`.
- Alertmanager's route already points at the webhook receiver installed in
  section 6, so alerts have somewhere to go from the first release.

### Check

```sh
kubectl get pods -n monitoring
```

Shows `monitoring-kube-prometheus-operator-...`,
`prometheus-monitoring-kube-prometheus-prometheus-0`,
`alertmanager-monitoring-kube-prometheus-alertmanager-0`, `monitoring-grafana-...`,
`monitoring-kube-state-metrics-...`, and one `monitoring-prometheus-node-exporter-...`
per node, all `Running`.

```sh
kubectl port-forward svc/monitoring-grafana -n monitoring 3000:80
```

Open <http://localhost:3000>, log in `admin` / `enjoythings-local` (see the
`adminPassword` comment in the values file — a local-cluster-only password,
never reused). Under Connections → Data sources, "Prometheus" shows a green
"Data source is working".

---

## 3. Scrape the EnjoyThings pods

### Goal

Prometheus has `up{namespace="enjoythings"} == 1` for every application pod
except `stub-payment-rail`, using one of two interchangeable methods.

### Steps

Pick the PodMonitor (recommended: an explicit resource you can read) or the
annotation-based scrape config (closer to a "classic" Prometheus setup), not
both:

```sh
# A. PodMonitor
kubectl apply -f services/k8s/observability/podmonitor-enjoythings.yaml

# B. Annotations, re-running the install with a second values file
helm upgrade --install monitoring prometheus-community/kube-prometheus-stack \
  --version 90.0.0 -n monitoring \
  -f services/k8s/observability/kube-prometheus-stack-values.yaml \
  -f services/k8s/observability/kube-prometheus-stack-values.annotations.yaml
```

### Why

- Every EnjoyThings pod template carries three annotations, set once in
  `services/charts/enjoythings/templates/applications.yaml` and inherited by
  all nine applications:

  ```yaml
  prometheus.io/scrape: "true"
  prometheus.io/path: "/metrics"
  prometheus.io/port: {{ default $app.httpPort $app.metricsPort | quote }}
  ```

  For eight services `metricsPort` is unset, so the port defaults to
  `httpPort` — `8080` for most, `18090` for `stub-payment-rail`. `fraud-worker`
  sets `metricsPort: 9101` explicitly, matching its `httpPort`. Method B reads
  exactly these three annotations through `kubernetes_sd_configs` relabeling;
  it is the PodMonitor's job description written out by hand.
- Method A (the PodMonitor) does not read the annotations at all — the
  Prometheus Operator generates its own scrape config from
  `spec.podMetricsEndpoints`, matching pods by the `app.kubernetes.io/instance`
  label and a container port literally named `http`. Every application
  container exposes a port named `http`, so one PodMonitor covers all of them.
  `stub-payment-rail` is excluded by a `matchExpressions` clause because it is
  annotated for scraping by the chart but has no `/metrics` handler; its
  target would otherwise sit permanently `down`.
- Using both at once double-scrapes every target under two job names,
  doubling ingestion for no benefit — pick one.

### Check

```sh
kubectl port-forward svc/monitoring-kube-prometheus-prometheus -n monitoring 9090:9090
```

Open <http://localhost:9090/targets>. Every `enjoythings` pod except
`stub-payment-rail` shows state `UP` under job `enjoythings-applications`
(method A) or `annotated-pods` (method B). Query
`up{namespace="enjoythings"}` in the Prometheus UI; every returned series is
`1`.

---

## 4. Load the Grafana dashboards

### Goal

The three dashboards from the Compose stack — System Overview, Saga Health,
Fraud Agent — appear in Grafana under an "EnjoyThings" folder, unmodified.

### Steps

```sh
services/k8s/observability/build-dashboard-configmaps.sh | kubectl apply -f -
kubectl get configmap -n monitoring -l grafana_dashboard=1
```

### Why

- `kube-prometheus-stack-values.yaml` turns on
  `grafana.sidecar.dashboards.enabled` with `label: grafana_dashboard`,
  `labelValue: "1"` and `folderAnnotation: grafana_folder`. The sidecar
  container watches every namespace for ConfigMaps carrying that label, reads
  the annotation for the folder name, and writes the ConfigMap's data into
  Grafana's dashboard provisioning directory — the Kubernetes equivalent of
  `services/observability/grafana/provisioning/dashboards/dashboards.yml`,
  which names the same folder, `EnjoyThings`.
- `build-dashboard-configmaps.sh` does not rewrite the JSON. It wraps each
  file in `services/observability/grafana/dashboards/*.json` in a ConfigMap
  named `grafana-dashboard-<filename>`, so the panels' `datasource.uid:
  "prometheus"` references still resolve against the datasource from
  section 2 without edits.
### Check

Reload Grafana (<http://localhost:3000>, port-forward from section 2). The
left-hand Dashboards list shows a folder "EnjoyThings" with three dashboards:
System Overview, Saga Health, Fraud Agent. Open System Overview; "Request
rate" and "Error rate" show data once traffic hits the gateway (section 9
generates some).

---

## 5. Install Loki and Grafana Alloy for logs

### Goal

Every application pod's stdout is queryable in Grafana through Loki within a
few seconds of being written.

### Steps

```sh
helm repo add grafana https://grafana.github.io/helm-charts
helm repo update

helm upgrade --install loki grafana/loki --version 7.3.0 -n monitoring \
  -f services/k8s/observability/loki-values.yaml

helm upgrade --install alloy grafana/alloy --version 1.12.1 -n monitoring \
  -f services/k8s/observability/alloy-values.yaml

kubectl get pods -n monitoring -l app.kubernetes.io/name=loki
kubectl get pods -n monitoring -l app.kubernetes.io/name=alloy
```

### Why

- `loki-values.yaml` sets `deploymentMode: SingleBinary`: one process, local
  disk, no object store, no multi-tenant auth. Enough for a laptop; "Toward
  production" in section 15 says what a real deployment needs instead.
- Alloy runs as a DaemonSet — one pod per node — because
  `controller.type: daemonset` is set. Each pod discovers only the pods
  scheduled on its own node (`field = "spec.nodeName=" + sys.env("NODE_NAME")`
  in the River config), tails their container logs through the kubelet API
  with `loki.source.kubernetes`, and pushes to
  `http://loki.monitoring.svc:3100/loki/api/v1/push`. Without the node
  filter, every Alloy pod on a multi-node cluster would ship every pod's
  logs, storing each line once per node.
- The relabel stage keeps the label set intentionally small — `namespace`,
  `pod`, `container`, `app` (from the chart's
  `app.kubernetes.io/component` label), `node`. Every distinct combination of
  these becomes a separate Loki stream; a label with high cardinality (a
  request ID, a timestamp) would multiply streams and defeat Loki's index.
- Loki's Service was already added to Grafana as a datasource in section 2's
  values file, so nothing else needs configuring in Grafana.

### Check

```sh
kubectl port-forward svc/loki -n monitoring 3100:3100
curl -s http://localhost:3100/ready
```

Returns `ready`. In Grafana, Explore → Loki, run `{namespace="enjoythings"}`;
log lines from every application pod appear, most recent first.

### LogQL walkthrough

All of these run in Grafana Explore (datasource Loki) or
`logcli query '<expr>'` against the port-forward above.

```logql
# Every line from the gateway
{app="gateway"}

# Only lines that look like an error, case-sensitive substring match
{app="gateway"} |= "error"

# Same, but on saga-orchestrator, and excluding health check noise
{app="saga-orchestrator"} |= "error" != "readyz"

# Every application's logs, split by app, restricted to one container
{namespace="enjoythings", container="saga-orchestrator"}

# Parse Go's slog key=value output and filter on a field it added
{app="saga-orchestrator"} | logfmt | level="ERROR"

# Rate of error lines per app over 5 minutes, for a dashboard panel
sum by (app) (rate({namespace="enjoythings"} |= "error" [5m]))
```

The last two only work once a line's fields are parsed out of its text: `|
logfmt` parses Go's `log/slog` default text output (`time=... level=INFO
msg="..." key=value`) into labels usable after the pipe, the same way `|
json` would parse a JSON log line. Run the plain `{app="..."}` queries first
to see what the actual line shape is before reaching for a parser stage.

---

## 6. Install Tempo and the OTel Collector for traces

### Goal

A trace of one gateway request shows spans for `gateway`, `saga-orchestrator`
and `fraud-worker`, in that call order, in Tempo.

### Steps

```sh
helm upgrade --install tempo grafana/tempo --version 1.24.4 -n monitoring \
  -f services/k8s/observability/tempo-values.yaml

helm repo add open-telemetry https://open-telemetry.github.io/opentelemetry-helm-charts
helm repo update

helm upgrade --install otel-collector open-telemetry/opentelemetry-collector \
  --version 0.172.1 -n monitoring \
  -f services/k8s/observability/otel-collector-values.yaml

kubectl get pods -n monitoring -l app.kubernetes.io/name=tempo
kubectl get pods -n monitoring -l app.kubernetes.io/name=opentelemetry-collector
```

### Why

- `tempo-values.yaml` replaces the chart's default receivers (which also open
  Jaeger, Zipkin and OpenCensus ports nobody here uses) with OTLP only, gRPC
  on 4317 and HTTP on 4318, and stores trace blocks on local disk — the same
  emptyDir tradeoff the EnjoyThings chart's own data stores make: traces
  disappear on a pod restart unless `persistence.enabled` is turned on.
- `otel-collector-values.yaml` runs one Deployment replica, receives OTLP on
  both ports, and forwards traces to
  `http://tempo.monitoring.svc:4318` (Tempo's own Service, not a separate
  ingest path). A `debug` exporter alongside it prints a one-line summary per
  batch to the collector's own log, so `kubectl logs` proves spans are
  arriving before you go looking in Tempo's UI.
- The collector sits between the services and Tempo instead of the services
  exporting straight to Tempo so that batching, retries and the export
  destination live in one place — a values change to
  `otel-collector-values.yaml`, touching no application.
- **The services do not send traces yet.** `services/internal/telemetry/telemetry.go`
  and `app/fraud/tracing.py` both start an OTLP/HTTP exporter only when
  `OTEL_EXPORTER_OTLP_ENDPOINT` is set in the process's environment; the
  chart's `values.yaml` leaves it unset today, so every service still creates
  spans internally and throws them away unexported. Section 7 turns this on.

### Check

```sh
kubectl port-forward svc/tempo -n monitoring 3100:3100
curl -s http://localhost:3100/ready
```

Returns `ready`. `kubectl logs -n monitoring deploy/otel-collector` shows no
export errors (it will show nothing at all until section 7 sends the first
span).

---

## 7. Turn on trace export and follow one trace

### Goal

`OTEL_EXPORTER_OTLP_ENDPOINT` set for the services that participate in a
payment, and one saga-to-fraud-worker trace visible end to end in Tempo.

### Steps

```sh
cd services
helm upgrade --install enjoythings charts/enjoythings -n enjoythings \
  -f charts/enjoythings/values-local.yaml \
  -f charts/enjoythings/values-secrets.yaml \
  -f ../services/k8s/observability/otel-endpoint-values.example.yaml
cd -

curl -s -X POST http://localhost:18080/v1/transfers \
  -H 'Content-Type: application/json' \
  -d '{"from_wallet_id":"...", "to_wallet_id":"...", "amount_cents": 500}'
```

(Use whatever request the gateway's transfer endpoint actually expects on
your running cluster — the point of this step is any request that walks
through the saga, not this exact body.)

### Why

- `otel-endpoint-values.example.yaml` adds one variable,
  `OTEL_EXPORTER_OTLP_ENDPOINT: http://otel-collector.monitoring.svc.cluster.local:4318`,
  under `applications.<name>.env` for every application that initializes
  telemetry (all but `stub-payment-rail`). Helm deep-merges maps under
  `applications.<name>.env`, so this file adds the one variable per service
  without repeating the existing `HTTP_ADDR` / `GRPC_ADDR` entries already set
  in `values.yaml` and `values-local.yaml` — copy it, do not edit the chart's
  templates.
- The value is the OTel Collector's cluster-DNS Service address from
  section 6, base URL only; both exporters (`otlptracehttp` in Go,
  the OTLP HTTP exporter in the fraud worker) append `/v1/traces` themselves.
- With Argo CD instead of a bare `helm upgrade`, add the same block to the
  values file the Application already uses, or list this file under
  `spec.source.helm.valueFiles`, then commit and push — the Argo CD runbook's
  pattern for every other values change.
- A transfer walks gateway → saga-orchestrator (over HTTP) → wallet/ledger (gRPC)
  → fraud-worker (over Kafka, scored asynchronously) → back through the saga.
  Each hop that has a span propagates the trace ID onward, so one trace ties
  the whole path together even though part of it crosses a message queue
  instead of a direct call.

### Check

```sh
kubectl port-forward svc/monitoring-grafana -n monitoring 3000:80
```

Grafana → Explore → Tempo → Search: service name `saga-orchestrator`, run.
The most recent trace shows spans nested under `saga-orchestrator`, with a
child (or a linked span, if it crosses the Kafka publish) under
`fraud-worker`. Click a span, "Logs for this span" jumps to Loki filtered to
the same pod and a 10-minute window around the span's start — the
`tracesToLogsV2` wiring in `kube-prometheus-stack-values.yaml`'s Tempo
datasource. If no trace appears, restart the pods so they pick up the new
environment variable: `kubectl rollout restart deployment -n enjoythings`.

---

## 8. Alerting: PrometheusRule and Alertmanager

### Goal

Every alert in `prometheusrule-enjoythings.yaml` is loaded, and a real alert
produces a JSON payload you can read in the webhook's logs.

### Steps

```sh
kubectl apply -f services/k8s/observability/prometheusrule-enjoythings.yaml
kubectl apply -f services/k8s/observability/alert-webhook.yaml

promtool check rules services/k8s/observability/prometheusrule-enjoythings.yaml

kubectl logs -n monitoring deployment/alert-webhook -f
```

### Why

- Five alerts, three groups, every expression built from a metric the
  services genuinely export or that kube-state-metrics exports for every
  cluster — verified against `services/internal/telemetry/metrics.go` and
  `app/fraud/metrics.py`:

  | Alert | Metric | Exported by |
  | --- | --- | --- |
  | `EnjoyThingsDeploymentUnavailable` | `kube_deployment_status_replicas_available`, `kube_deployment_spec_replicas` | kube-state-metrics |
  | `EnjoyThingsScrapeTargetDown` | `up` | Prometheus itself |
  | `EnjoyThingsGatewayHigh5xxRatio` | `service_http_requests_total` | every Go service, `telemetry.go` |
  | `EnjoyThingsKafkaRecordFailures` | `service_kafka_records_total` | every Go service, `telemetry.go` |
  | `EnjoyThingsDatabaseErrors` | `service_database_operation_duration_seconds_count` | every Go service, `telemetry.go` |
  | `EnjoyThingsFraudWorkerFailOpen` | `fraud_transactions_scored_total{action="fail_open"}` | `app/fraud/metrics.py` |

  There is deliberately no Kafka consumer lag alert: the services count
  records by outcome, not lag, and the in-chart Kafka broker has no exporter.
  `EnjoyThingsKafkaRecordFailures` is the closest real signal, and the rule
  file says so in its own header comment rather than inventing a metric that
  does not exist.
- `promtool check rules` only understands a bare Prometheus rule file — a
  top-level `groups:` list — not the `PrometheusRule` custom resource's
  `apiVersion`/`kind`/`metadata`/`spec` wrapper, so running it straight
  against `prometheusrule-enjoythings.yaml` fails on those extra fields. Pull
  out just the `spec.groups` value into its own file first, then check that:
  `yq '.spec' prometheusrule-enjoythings.yaml > /tmp/rules.yaml && promtool
  check rules /tmp/rules.yaml`. `kubeconform` validates the CRD shape
  (`apiVersion`, `kind`, the rest of `spec`) separately; between the two, both
  the PromQL and the resource structure are checked before anything reaches
  the cluster.
- Alertmanager's route (`kube-prometheus-stack-values.yaml`) sends everything,
  including the built-in `Watchdog` heartbeat alert, to the `alert-webhook`
  receiver: `group_wait: 10s`, `group_interval: 1m`, `repeat_interval: 1h`, far
  shorter than a production 30s/5m/4h so the drill in section 10 shows results
  in minutes.
- `Watchdog` fires constantly by design (it exists so "alerting is broken"
  itself pages someone); seeing it arrive at the webhook within a minute or
  two of applying these manifests proves the whole path — rule evaluation,
  Alertmanager routing, webhook delivery — works before you ever break
  anything on purpose.

### Check

Within about two minutes, `kubectl logs -n monitoring deployment/alert-webhook -f`
prints a JSON body containing `"alertname":"Watchdog"`. In Alertmanager's UI
(`kubectl port-forward svc/monitoring-kube-prometheus-alertmanager -n
monitoring 9093:9093`, then <http://localhost:9093>), the Watchdog alert
shows state `firing`.

---

## 9. A short k9s tour

### Goal

Navigate pods, logs, and a shell across both the `enjoythings` and
`monitoring` namespaces from one terminal UI, without retyping `kubectl -n
... get ...` for every question.

### Steps

```sh
brew install derailed/k9s/k9s   # k9s v0.51.0
k9s
```

Inside k9s:

- `:ns` then select `enjoythings`, then `:pods` — the pod list for that
  namespace, live.
- `l` on a pod — stream its logs, `Ctrl+f`/`Ctrl+b` to page.
- `s` on a pod — shell into its first container (works for the Go services'
  distroless-adjacent debug images; if a container has no shell, k9s reports
  it rather than hanging).
- `d` on a pod — the same as `kubectl describe`, scrollable.
- `:pf` — active port-forwards started from within k9s.
- `0` (or `:ctx`) to switch context if you have more than one cluster, `:q` to
  quit a view, `Ctrl+c` to quit k9s.
- `/` inside any list — fuzzy-filter by name.
- `:pulses` — a live dashboard of cluster resource pressure, a good first
  screen when something feels slow.

### Why

k9s reads the same API server every `kubectl` command does; it changes
nothing about the cluster, it only makes the pod → log → describe → shell loop
one keypress apart instead of four separate commands, which matters most in
section 10 where speed of navigation is the whole point of the drill.

### Check

You can, without leaving k9s, go from the `enjoythings` namespace's pod list
to a live log stream of `saga-orchestrator` and back, in under five
keystrokes.

---

## 10. Guided incident drill

### Goal

Break something, follow alert → dashboard → logs → trace to the cause, fix
it, and write a postmortem.

### Steps

Pick one:

```sh
# A. Scale postgres to zero
kubectl scale deployment/postgres --replicas=0 -n enjoythings

# B. Point wallet at a database that does not exist
kubectl set env deployment/wallet -n enjoythings DATABASE_URL='postgres://wallet:wrongpass@postgres:5432/wallet?sslmode=disable'
```

Then, in order:

1. **Alert.** Watch `kubectl logs -n monitoring deployment/alert-webhook -f`.
   Within a few minutes (`EnjoyThingsDeploymentUnavailable` has a 2-minute
   `for`), a payload names the affected Deployment(s). Option A takes down
   every service that depends on postgres, since none of them retry a
   connection refused indefinitely; option B only affects `wallet`.
2. **Dashboard.** Open the System Overview dashboard in Grafana. "Request
   rate" for the affected service drops, "Error rate" rises, or both go to
   zero if the gateway itself can no longer route to it.
3. **Logs.** Grafana Explore → Loki: `` {app="wallet"} |= "error" `` (or
   whichever service the alert named). Read what the service itself says
   about the failure — a connection refused, an authentication failure, a
   timeout — the actual text depends on the driver, but the shape is "I tried
   to reach the database and could not."
4. **Trace.** Grafana Explore → Tempo: search for a recent trace on
   `saga-orchestrator` or the affected service (needs section 7's
   `OTEL_EXPORTER_OTLP_ENDPOINT` to already be set). The trace ends at a span
   for the failing database call instead of completing normally — the same
   moment the log line describes, from the request's point of view instead of
   the service's.
5. **Fix.**

   ```sh
   # undo A
   kubectl scale deployment/postgres --replicas=1 -n enjoythings

   # undo B
   kubectl set env deployment/wallet -n enjoythings DATABASE_URL-
   kubectl rollout restart deployment/wallet -n enjoythings
   ```

6. **Confirm recovery.** The alert resolves in Alertmanager and the webhook
   log, the dashboard's request rate returns, and a new trace completes
   without an error span.
7. **Write it up.**

   ```sh
   cp services/k8s/observability/postmortem-template.md docs/design-notes/postmortem-$(date +%Y%m%d)-postgres-scaled-to-zero.md
   ```

   Fill in every section: summary, impact, a timeline with real timestamps
   from your own terminal history and the webhook/Grafana/Loki evidence you
   just gathered, root cause (not just the trigger command — *why* one
   Deployment taking a nap took the rest down with it), contributing factors,
   what went well, what went badly, action items with owners and dates, and
   the lessons at the end. Do not skip the lessons section; if you cannot
   write it, the postmortem is not finished.

### Why

This is the loop every later incident uses: an alert names a symptom, not a
cause; the dashboard shows scope and trend; logs show what one service
thought was happening; a trace shows what one request actually experienced
end to end. The four signals are strongest together — an alert alone tells
you something is wrong, a trace alone tells you about one request, and
neither tells you whether it is getting better without the dashboard.

### Check

You went alert → dashboard → logs → trace → fix → resolved alert without
guessing, and a filled-in postmortem file exists with a non-empty "Lessons"
section.

---

## 11. Hands-on practice

**Exercise 1: read one dashboard cold.** Before doing anything else, open the
Fraud Agent dashboard and, for each of its six panels, say in one sentence
what it is measuring and what "bad" would look like on it, using only the
panel title and its PromQL (Grafana's panel editor shows both). Then check
your answer against the metric's definition in `app/fraud/metrics.py`.

**Exercise 2: cause a real scrape failure, not a simulated one.**

```sh
kubectl scale deployment/wallet -n enjoythings --replicas=0
```

Watch `up{namespace="enjoythings", pod=~"wallet.*"}` in Prometheus disappear
(not go to 0 — the target itself vanishes once the pod is gone) within one
scrape interval, then watch `EnjoyThingsScrapeTargetDown` never fire for it
because the target no longer exists to be scraped, while
`EnjoyThingsDeploymentUnavailable` does fire. Explain in one sentence why
those two alerts react differently to the same event. Scale back to 1.

**Exercise 3: find a specific log line by trace ID.** Generate a transfer
(section 7), copy its trace ID from Tempo, then in Loki run
`` {namespace="enjoythings"} |= "<trace id>" `` across every app. Note which
services' log lines actually contain the trace ID (only ones that log it
explicitly) versus which you can only find by time-window correlation.

**Exercise 4: break the fraud path without breaking payments.** Point the
fraud worker's LLM provider at an address that does not exist (edit its
`env` in a values overlay, or `kubectl set env` directly), and watch
`EnjoyThingsFraudWorkerFailOpen` fire while `EnjoyThingsGatewayHigh5xxRatio`
does not. Confirm from the Fraud Agent dashboard that scored transactions
keep flowing but skew toward `action="fail_open"`. This is the "quiet
degradation" case the alert exists for — nothing user-facing breaks, so
without this alert nobody would notice fraud scoring stopped happening.

**Exercise 5: run the whole drill again with the other break.** If you used
option A in section 10, redo it with option B (or vice versa), timing
yourself from "alert fires" to "root cause identified." Compare which signal
was most useful for each: option A (postgres gone) is fastest to diagnose
from the dashboard and the deployment-unavailable alert alone; option B
(wrong credentials) needs the logs, since the Deployment itself stays
healthy and only the database calls fail.

---

## 12. Troubleshooting

| Symptom | Meaning | What to do |
| --- | --- | --- |
| Grafana panel says "Datasource prometheus was not found" | Sidecar datasource uid does not match `prometheus`. | Check `grafana.sidecar.datasources.uid` in `kube-prometheus-stack-values.yaml`; it must stay `prometheus` to match the dashboard JSON and `services/observability/grafana/provisioning/datasources/prometheus.yml`. |
| Dashboards do not appear in Grafana | Sidecar not enabled, or ConfigMaps not labelled. | `kubectl get configmap -n monitoring -l grafana_dashboard=1`; if empty, rerun `build-dashboard-configmaps.sh \| kubectl apply -f -`. |
| Target `stub-payment-rail` shows `DOWN` in Prometheus | Expected. It carries the scrape annotations but has no `/metrics` handler. | Nothing to fix; the PodMonitor already excludes it, the annotations method does not, so it will show `down` under method B — that is documented, not a bug. |
| Both the PodMonitor and the annotations config are applied | Every target scraped twice under two job names, metrics double-counted in any query summed across jobs. | Remove one: `kubectl delete podmonitor enjoythings-applications -n monitoring`, or drop the `.annotations.yaml` values file and `helm upgrade` without it. |
| No alerts ever reach the webhook, not even `Watchdog` | Alertmanager route misconfigured, or the webhook Service/Deployment not applied. | `kubectl get pods -n monitoring -l app.kubernetes.io/name=alert-webhook`; `kubectl -n monitoring exec deploy/monitoring-kube-prometheus-alertmanager-0 -- wget -qO- http://alert-webhook.monitoring.svc:8080/healthz`. |
| `promtool check rules` fails on `field apiVersion not found` | Ran against the whole `PrometheusRule` file instead of just `spec.groups`. | Extract `spec.groups` into its own file first (see section 8), or apply the manifest and trust the Operator's admission webhook. |
| No spans in Tempo | `OTEL_EXPORTER_OTLP_ENDPOINT` not set, or pods not restarted after the values change. | `kubectl get deployment gateway -n enjoythings -o jsonpath='{.spec.template.spec.containers[0].env}'`; `kubectl rollout restart deployment -n enjoythings`. |
| Spans arrive at the collector's debug log but never reach Tempo | Collector's `otlphttp/tempo` exporter endpoint wrong, or Tempo not up yet. | `kubectl logs -n monitoring deploy/otel-collector \| grep -i error`; confirm `http://tempo.monitoring.svc:4318` resolves and Tempo's pod is `Running`. |
| Loki Explore query returns nothing | Alloy not running on the node the pod is scheduled on, or the label selector does not match. | `kubectl get pods -n monitoring -l app.kubernetes.io/name=alloy -o wide`; confirm one per node. Start with `{namespace="enjoythings"}` alone before adding filters. |
| Alloy pod `CrashLoopBackOff` | River config syntax error, usually after a hand-edit. | `kubectl logs -n monitoring ds/alloy`; the error names the line. Revert to the committed `alloy-values.yaml`. |
| Trace "Logs for this span" link goes nowhere | `tracesToLogsV2` in the Tempo datasource points at the wrong Loki uid, or the pod label used to filter (`service.name`) does not match Loki's `app` label value. | Confirm the Tempo datasource's `tracesToLogsV2.datasourceUid` is `loki` and the tag mapping uses a label Loki actually has. |
| `helm upgrade` for the EnjoyThings chart with the OTel overlay fails to merge | Values file path wrong, or run from the wrong directory. | Run from `services/`, as the comment at the top of `otel-endpoint-values.example.yaml` shows; the relative path to the observability directory is `../services/k8s/observability/...` from there only if your shell is already inside `services/`. |
| k9s shows no resources at all | Wrong context, or kubeconfig not pointing at the kind cluster. | `:ctx` inside k9s, or `kubectl config current-context` outside it; should be `kind-enjoythings`. |

---

## 13. Cleanup

```sh
# Undo anything left over from the drill or exercises
kubectl scale deployment/postgres --replicas=1 -n enjoythings
kubectl scale deployment/wallet --replicas=1 -n enjoythings
kubectl set env deployment/wallet -n enjoythings DATABASE_URL- 2>/dev/null || true

# Application-facing objects this runbook added directly
kubectl delete -f services/k8s/observability/podmonitor-enjoythings.yaml --ignore-not-found
kubectl delete -f services/k8s/observability/prometheusrule-enjoythings.yaml --ignore-not-found
kubectl delete -f services/k8s/observability/alert-webhook.yaml --ignore-not-found
kubectl delete configmap -n monitoring -l grafana_dashboard=1

# The five Helm releases, in reverse dependency order
helm uninstall otel-collector -n monitoring
helm uninstall tempo -n monitoring
helm uninstall alloy -n monitoring
helm uninstall loki -n monitoring
helm uninstall monitoring -n monitoring

kubectl delete namespace monitoring
```

Revert the EnjoyThings chart's trace export by reinstalling without the OTel
overlay:

```sh
cd services
helm upgrade --install enjoythings charts/enjoythings -n enjoythings \
  -f charts/enjoythings/values-local.yaml \
  -f charts/enjoythings/values-secrets.yaml
cd -
```

Deleting the `monitoring` namespace removes the Prometheus Operator's CRDs'
*instances* but not the CRDs themselves (`kube_prometheus_stack` installs
them cluster-scoped); `helm uninstall monitoring -n monitoring` does remove
the CRDs by default for this chart, so a reinstall starts clean. The
EnjoyThings chart, its namespace, and everything from the Kubernetes runbook
are untouched — nothing here modifies `services/charts/enjoythings`, only
adds values overlays and objects in a separate namespace.

---

## 14. Toward production

1. **Object storage for Loki and Tempo.** `deploymentMode: SingleBinary` and
   `storage.trace.backend: local` are laptop shortcuts. A real deployment
   uses `deploymentMode: Distributed` (or `SimpleScalable`) for Loki backed by
   S3/GCS, and Tempo's `storage.trace.backend: s3` with the same, so retention
   is not bounded by one node's disk and losing a pod does not lose the data.

2. **Long-term metrics storage.** Prometheus here keeps 2 days locally. Add
   Thanos or Mimir sidecar/remote-write to keep months of history without
   growing Prometheus's own disk, and to query across more than one
   Prometheus if the cluster grows past one.

3. **Real receivers, not an echo server.** `alert-webhook.yaml` exists so you
   can read the exact JSON Alertmanager sends. Replace the receiver with
   Slack, PagerDuty or Opsgenie `webhook_configs`/native integrations, and
   split the single `route` into per-severity and per-team routes so a
   `critical` alert pages someone while a `warning` posts to a channel.

4. **The chart's own PodMonitor and PrometheusRule**, not `kubectl apply`.
   Fold `podmonitor-enjoythings.yaml` and `prometheusrule-enjoythings.yaml`
   into `services/charts/enjoythings` behind a values flag (`observability.
   enabled`), the way progressive delivery's Rollout stays a values switch on
   the gateway Deployment, so the objects travel with every chart release
   instead of being applied by hand or by a separate Terraform stage.

5. **Sampling before Tempo, not after.** The OTel Collector's pipeline here
   exports every span. At real traffic volume, add a tail-sampling processor
   that keeps all error and slow traces but only a fraction of fast, healthy
   ones, so storage cost tracks incident-relevant traffic instead of raw
   volume.

6. **mTLS, real secrets, and platform-level alerts.** Prometheus, Loki and
   Tempo here trust anything inside the cluster network, and the Grafana admin
   password is a plaintext values-file default; put these behind the same
   network policies and secrets management the chart's own `mtls.enabled` flag
   uses for service-to-service traffic. Add cluster-level alerts too — node
   disk pressure, node not ready, PVC nearly full — from
   kube-prometheus-stack's own bundled rules, re-enabling the
   `kubeControllerManager`/`kubeScheduler`-style toggles once those components
   are reachable on a real cluster rather than kind.
