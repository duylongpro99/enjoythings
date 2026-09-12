# Kubernetes Deployment Runbook

This runbook takes EnjoyThings from source code to a running Kubernetes
deployment. It is written for someone who has never operated Kubernetes: every
section states a goal, the exact commands, why each step exists, and how to
check that it worked.

**Part 1** (sections 1 to 12) deploys to a local cluster with `kind`. This is
the path the repository ships and tests, so start here even if your target is
a cloud cluster. **Part 2** (section 13) lists exactly what changes when you
move to a shared or production cluster. Once Part 1 works,
[`argocd-runbook.md`](./argocd-runbook.md) replaces section 7 with GitOps.

## What you will end up with

The Helm chart at `services/charts/enjoythings` deploys these workloads into
one namespace:

| Workload | Kind of thing | Role |
| --- | --- | --- |
| `gateway` | Go service | REST entry point, JWT auth, rate limiting. The only thing exposed outside the cluster. |
| `wallet`, `ledger`, `verification`, `saga-orchestrator` | Go services | Business logic, talk to each other over gRPC |
| `payment-processor`, `notification` | Go services | Kafka consumers, no inbound traffic |
| `stub-payment-rail` | Go service | Fake external payment provider |
| `fraud-worker` | Python (LangGraph) | Kafka consumer that scores transactions with an LLM |
| `postgres` | Data store | Shared database for the Go services |
| `fraud-timescaledb` | Data store | Fraud audit database for the Python worker |
| `kafka` | Message broker | Event bus between services |
| `kafka-topic-init` | One-off Job | Creates the Kafka topics after install |

Not in the chart, and therefore out of scope here: the FastAPI chat API
(`app/main.py`), the Next.js web UI (`web/`), and the Prometheus, Grafana and
Jaeger stack. Those exist only in Docker Compose today. Section 13 explains what
it takes to add them.

---

## 1. Kubernetes vocabulary you need

You do not need to master Kubernetes to follow this runbook, but these ten
terms appear in every command and error message.

| Term | What it is | Where it shows up here |
| --- | --- | --- |
| **Cluster** | A set of machines running Kubernetes. | `kind create cluster` creates one on your laptop. |
| **Node** | One machine in the cluster. | kind runs one node as a Docker container. |
| **Pod** | The smallest unit Kubernetes runs: one or more containers sharing a network. Pods are disposable. | Each service runs in its own pod. |
| **Deployment** | Keeps N copies of a pod running and replaces them on crash or upgrade. | One per service, plus Postgres, Kafka, TimescaleDB. |
| **Service** | A stable DNS name and IP in front of a set of pods. `ClusterIP` is reachable only inside the cluster; `NodePort` also opens a port on the node. | `wallet:9090`, `kafka:9092`. The gateway is a `NodePort` so you can reach it from your laptop. |
| **ConfigMap** | Non-secret settings, injected as environment variables. | `enjoythings-config`, all the `*_ADDR`, Kafka and tuning values. |
| **Secret** | Same as ConfigMap but for sensitive values. Only base64 encoded, not encrypted, by default. | `enjoythings-secret`: database URLs, JWT secret, LLM API key. |
| **Job** | A pod that runs to completion instead of forever. | `kafka-topic-init` creates topics then exits. |
| **Namespace** | A folder that groups resources and isolates names. | Everything goes into `enjoythings`. |
| **Probe** | A health check Kubernetes runs against a container. Readiness gates traffic; liveness restarts a stuck container. | `/readyz` and `/healthz` on every Go service. |
| **Helm chart / release / values** | A chart is a template bundle. A release is one installed copy of a chart. Values are the variables that fill the templates. | `charts/enjoythings`, release `enjoythings`, `values.yaml` plus your overrides. |
| **kubectl context** | Which cluster and credentials `kubectl` currently talks to. | `kind-enjoythings` after cluster creation. |

---

## 2. Install and verify the tools

### Goal

Have every CLI the later sections use, and confirm each one responds.

### Steps

On macOS with Homebrew:

```sh
brew install --cask docker      # Docker Desktop, then start it from Applications
brew install kind kubectl helm jq go
```

Verify:

```sh
docker version          # Client and Server sections must both print
kind version
kubectl version --client
helm version
jq --version
go version              # 1.26 or newer, matching services/go.mod
```

### Why

- **Docker** builds the images and is also what kind uses to run the cluster
  node. If Docker is not running, nothing else in this runbook works.
- **kind** ("Kubernetes in Docker") runs a real Kubernetes control plane inside
  a container. It is free, disposable, and identical enough to a cloud cluster
  that the Helm chart behaves the same.
- **kubectl** is the Kubernetes command line. You use it to inspect pods, read
  logs and forward ports.
- **helm** installs the chart. Without it you would apply a dozen YAML files by
  hand and manage their differences yourself.
- **jq** extracts fields from JSON responses in the verification steps.
- **go** mints a local JWT and runs the repository smoke test.

### Check

Every command above prints a version. `docker version` must show a `Server:`
block, otherwise Docker Desktop is not running yet.

---

## 3. Create the local cluster

### Goal

A one-node Kubernetes cluster on your laptop that `kubectl` is pointed at.

### Steps

```sh
cd services
kind create cluster --name enjoythings --config k8s/kind/cluster.yaml
kubectl cluster-info --context kind-enjoythings
kubectl get nodes
```

### Why

`k8s/kind/cluster.yaml` is tiny but does one important thing:

```yaml
extraPortMappings:
  - containerPort: 30080
    hostPort: 18080
```

The gateway Service is a `NodePort` on port `30080` (see
`applications.gateway.nodePort` in `values.yaml`). Kubernetes opens that port on
the node, and the node is a Docker container, so this mapping publishes it to
your laptop as `localhost:18080`. That is how you reach the API in section 8
without any extra tooling.

`kind create cluster` also writes a new entry into `~/.kube/config` and switches
`kubectl` to it. If you work with other clusters, always confirm the context
before running `kubectl` or `helm`:

```sh
kubectl config current-context        # expect: kind-enjoythings
kubectl config use-context kind-enjoythings
```

### Check

`kubectl get nodes` shows one node named `enjoythings-control-plane` with
`STATUS Ready`. It can take about a minute after creation.

---

## 4. Build the container images and load them into the cluster

### Goal

Nine images, tagged exactly as `values-local.yaml` expects, present inside the
kind node.

### Steps

Build the eight Go services from `services/`. One Dockerfile builds them all;
the `SERVICE` build argument picks the `cmd/<name>` entry point.

```sh
cd services
for svc in gateway wallet ledger verification saga-orchestrator \
           payment-processor notification stub-payment-rail; do
  docker build --build-arg SERVICE=$svc -t enjoythings/$svc:local .
  kind load docker-image enjoythings/$svc:local --name enjoythings
done
```

Build the Python fraud worker from the **repository root**. Its Dockerfile
copies `pyproject.toml`, `uv.lock` and the `app/` package, which live above
`services/`.

```sh
cd ..    # repository root
docker build -f app/fraud/Dockerfile -t enjoythings/fraud-worker:local .
kind load docker-image enjoythings/fraud-worker:local --name enjoythings
```

### Why

- A kind node has its own image store, separate from your Docker daemon.
  Building an image on your laptop does not make it visible to the cluster.
  `kind load docker-image` copies it across. On a cloud cluster this step
  becomes "push to a registry" (section 13).
- The chart sets `imagePullPolicy: IfNotPresent`. Kubernetes uses the image
  already on the node and never tries to download `enjoythings/wallet:local`
  from Docker Hub, where it does not exist. If a tag is missing on the node you
  get `ImagePullBackOff` or `ErrImageNeverPull` (section 11).
- Tags must match `applications.<name>.image` in `values-local.yaml`. All of
  them are `enjoythings/<name>:local`.
- The Go image is a two-stage build: compile in `golang:1.26-alpine`, then copy
  only the binary and `db/migrations` into a small Alpine image that runs as a
  non-root user. Services run their own migrations at startup, which is why the
  SQL files travel with the binary.

### Check

```sh
docker exec enjoythings-control-plane crictl images | grep enjoythings
```

You should see nine `docker.io/enjoythings/*` rows tagged `local`.

---

## 5. Understand the chart before installing it

### Goal

Know what Helm is about to create, so the output of later commands makes sense.

### Steps

```sh
cd services
helm lint charts/enjoythings
helm template enjoythings charts/enjoythings -f charts/enjoythings/values-local.yaml | less
```

Skim the rendered YAML. Search for `kind:` to see each resource.

### Why

`helm template` renders the chart without touching the cluster. Reading it
once removes most of the mystery from Kubernetes. What you will find:

| Template file | Renders | Purpose |
| --- | --- | --- |
| `applications.yaml` | 9 Deployments | One per application in `values.yaml: applications`. Each gets its environment from the ConfigMap and Secret via `envFrom`, plus per-app `env` overrides such as `GRPC_ADDR`. Readiness probe `/readyz`, liveness probe `/healthz`, a rolling update strategy with `maxUnavailable: 0` so a deploy never drops below the desired replica count. |
| `services.yaml` | 9 Services | Stable DNS names. `gateway` is `NodePort 30080`; everything else is `ClusterIP`. |
| `configmap.yaml` | `enjoythings-config` | Every value under `config:` in `values.yaml`, as environment variables. |
| `secret.yaml` | `enjoythings-secret` | `DATABASE_URL`, `POSTGRES_PASSWORD`, `FRAUD_POSTGRES_PASSWORD`, `JWT_SECRET`, `FRAUD_DATABASE_URL`, `LOCAL_LLM_API_KEY`. Skipped when `secrets.create=false`, for clusters where the Secret is created outside the chart. |
| `postgres.yaml`, `kafka.yaml`, `fraud-timescaledb.yaml` | Deployment + Service each | Single-replica data stores. **No persistent volume**: data lives in the container and is lost when the pod restarts. Fine for learning, not for anything real (section 13). |
| `kafka-topic-job.yaml` | Job `kafka-topic-init` | Annotated as a Helm `post-install,post-upgrade` hook. Waits for `kafka:9092`, creates every topic in `kafka.topics`, and sets 30-day retention on the `*.dlq` topics. Runs on every `helm upgrade`, idempotently. |
| `hpa.yaml` | HorizontalPodAutoscalers | Only when `hpa.enabled=true`. Off by default because it needs metrics-server. |
| `certificates.yaml` | cert-manager Certificates | Only when mTLS with cert-manager is on. Off by default. |

Two behaviors are not visible in the YAML but matter:

- **Database migrations run inside each Go service at startup**, guarded by a
  Postgres advisory lock. Several services can start at once and they
  serialize safely. The fraud worker does the same for its own database: its
  container command runs `fraud-migrate` and then `exec`s the worker.
- **Services tolerate dependencies being late.** Readiness fails until Postgres
  and Kafka answer, and `failureThreshold: 24` at 5-second intervals gives them
  two minutes. That is why `--wait` in the next section usually succeeds without
  ordering anything by hand.

### Check

`helm lint` reports `0 chart(s) failed`. The template output contains
`kind: Deployment` twelve times, `kind: Service` twelve times, one ConfigMap,
one Secret and one Job.

---

## 6. Set your own secrets and LLM endpoint

### Goal

A values file, outside version control, that overrides every credential in the
chart and points the fraud worker at an LLM you actually run.

### Steps

Create `services/charts/enjoythings/values-secrets.yaml`. The name is listed in
`.gitignore`, so it cannot be committed by accident.

```yaml
# services/charts/enjoythings/values-secrets.yaml  (never commit)
postgres:
  password: <pg-password>
fraudTimescaledb:
  password: <fraud-db-password>
secrets:
  jwtSecret: <long-random-string>
  databaseUrl: postgres://enjoythings:<pg-password>@postgres:5432/enjoythings?sslmode=disable
  fraudDatabaseUrl: postgres://fraud_worker:<fraud-db-password>@fraud-timescaledb:5432/fraud_audit?sslmode=disable
  localLLMApiKey: <api-key-or-anything-for-ollama>
config:
  llmProvidersJson: '{"providers":[{"id":"local","driver_type":"openai_compatible","base_url":"http://host.docker.internal:11434/v1","api_key_env":"LOCAL_LLM_API_KEY","model":"llama3.1","timeout_seconds":30}]}'
  llmDefaultProvider: local
```

Generate the random values:

```sh
openssl rand -hex 32     # run once per secret
```

### Why

- `values.yaml` ships development passwords such as `enjoythings_dev_password`
  and `local-dev-jwt-secret-change-me`. They exist so the chart installs with
  zero configuration. Anyone who reads the repository knows them.
- **The database passwords appear twice** and must agree: once where the
  database container is told its password (`postgres.password`,
  `fraudTimescaledb.password`), and once inside the connection URL the
  applications use (`secrets.databaseUrl`, `secrets.fraudDatabaseUrl`). If they
  differ, every database-backed service crash-loops with an authentication
  error.
- The JWT secret signs and verifies every API token. Whoever has it can mint
  tokens for any user, including `admin`.
- `llmProvidersJson` tells the fraud worker where the LLM is.
  `host.docker.internal` is a name Docker Desktop resolves to your laptop, so
  the default reaches an Ollama server running locally on port `11434`. If you
  use a hosted OpenAI-compatible API instead, change `base_url` and `model`,
  and put the real key in `localLLMApiKey`.
- The fraud worker is **fail-open**: when the LLM is unreachable, it publishes
  `fraud.error` and the payment completes normally. So a wrong LLM address does
  not break the platform, it only disables fraud scoring. Section 8 shows how to
  see that in the logs.

### Check

```sh
helm template enjoythings charts/enjoythings \
  -f charts/enjoythings/values-local.yaml \
  -f charts/enjoythings/values-secrets.yaml | grep -A6 'kind: Secret'
```

Your values, not the defaults, appear under `stringData`. Later `-f` files win
over earlier ones, which is why the secrets file comes last on every command.

---

## 7. Install the release

### Goal

Everything from the table in "What you will end up with" running in the
`enjoythings` namespace.

### Steps

```sh
cd services
helm upgrade --install enjoythings charts/enjoythings \
  --namespace enjoythings \
  --create-namespace \
  -f charts/enjoythings/values-local.yaml \
  -f charts/enjoythings/values-secrets.yaml \
  --wait \
  --timeout 10m
```

In a second terminal, watch it happen:

```sh
kubectl get pods -n enjoythings -w
```

### Why

- `upgrade --install` installs on the first run and upgrades on every later run.
  Using one command for both means you never have to remember which state you
  are in.
- `--namespace enjoythings --create-namespace` keeps the platform separate from
  anything else in the cluster and lets you delete it all with one command.
- `--wait` makes Helm block until every Deployment reports its pods ready, then
  run the post-install hook. Without it Helm returns immediately and you have
  to poll yourself.
- `--timeout 10m` is generous because Kafka and the first Postgres migrations
  take a while on a laptop. If you hit the timeout, the release is marked
  `failed` but the pods keep running; section 11 tells you how to diagnose.

What you will see in the watch window, roughly in order:

1. All pods appear at once as `ContainerCreating` then `Running` with
   `READY 0/1`. Kubernetes does not know about dependencies; it starts
   everything and lets readiness probes sort it out.
2. `postgres`, `fraud-timescaledb` and `kafka` turn `1/1` first.
3. The Go services connect, run migrations, and turn `1/1`. Log lines such as
   `applying migration 000004_sagas` are normal.
4. `fraud-worker` turns `1/1` after `fraud-migrate` finishes.
5. `kafka-topic-init-xxxxx` appears, runs for a few seconds, and goes to
   `Completed`. It is the Helm hook creating topics.

### Check

```sh
helm list -n enjoythings                 # STATUS deployed
kubectl get pods -n enjoythings          # all 1/1 Running, job Completed
kubectl get jobs -n enjoythings          # kafka-topic-init COMPLETIONS 1/1
kubectl logs job/kafka-topic-init -n enjoythings | tail -5
```

---

## 8. Verify the platform end to end

### Goal

Prove that traffic flows from your laptop through the gateway, into the gRPC
services, across Kafka and back, and that the fraud worker is alive.

### Steps

**8a. Health endpoints through the NodePort.** No port-forward is needed
because section 3 mapped `30080` to `localhost:18080`.

```sh
curl -i http://localhost:18080/healthz
curl -i http://localhost:18080/readyz
```

**8b. An authenticated request.** Mint a token with the same secret you put in
`values-secrets.yaml`, then create a wallet.

```sh
cd services
export JWT_SECRET='<your jwtSecret from values-secrets.yaml>'
JWT=$(go run ./cmd/devtoken -user-id 11111111-1111-1111-1111-111111111111 -role user -ttl 1h)

curl -s -X POST http://localhost:18080/v1/wallets \
  -H "Authorization: Bearer $JWT" \
  -H "Content-Type: application/json" \
  -d '{"currency":"USD"}' | jq
```

**8c. The repository smoke test.** It creates two wallets, seeds a balance
directly in Postgres, runs a transfer, and waits for the ledger consumer to
record it. It needs a database connection, so open a port-forward first.

```sh
# terminal A: expose the in-cluster Postgres on your laptop as port 15432
kubectl port-forward service/postgres 15432:5432 -n enjoythings

# terminal B
cd services
GATEWAY_URL=http://localhost:18080 \
DATABASE_URL="postgres://enjoythings:<pg-password>@localhost:15432/enjoythings?sslmode=disable" \
JWT_SECRET='<your jwtSecret>' \
  make phase3-smoke
```

**8d. The fraud worker.**

```sh
kubectl logs deployment/fraud-worker -n enjoythings --tail=50
kubectl port-forward service/fraud-worker 9101:9101 -n enjoythings &
curl -s http://localhost:9101/metrics | grep -i fraud | head
```

**8e. Can the worker reach your LLM?** Run a throwaway pod inside the cluster
and call the same URL the worker uses.

```sh
kubectl run llm-check --rm -it --restart=Never -n enjoythings \
  --image=curlimages/curl:8.10.1 -- \
  curl -s -m 5 http://host.docker.internal:11434/v1/models
```

### Why

- `/healthz` says the process is up. `/readyz` says its dependencies answer.
  Kubernetes uses the same two endpoints for its probes, so if `curl` gets a
  `200` you are seeing exactly what the cluster sees.
- The smoke test exercises the real saga path: gateway to saga orchestrator
  over gRPC, wallet outbox to Kafka, ledger consumer from Kafka. Passing it
  means networking, DNS, topics and migrations are all correct.
- `kubectl port-forward` opens a tunnel from a local port to a Service inside
  the cluster. It is the standard way to reach a `ClusterIP` Service from your
  laptop without exposing it. The tunnel lives only as long as the command.
- If the LLM check fails, the transfer in 8c still completes because scoring is
  fail-open. The worker logs will show `fraud.error` events. If Ollama runs on
  your laptop but the check fails, Ollama is probably bound to `127.0.0.1`
  only; restart it with `OLLAMA_HOST=0.0.0.0`.

### Check

- 8a: both return `200`.
- 8b: a JSON wallet with an `id`.
- 8c: the smoke test exits `0` and prints the completed transfer.
- 8d: metrics output contains fraud counters, and the log shows the consumer
  joined group `fraud-agent`.

---

## 9. Everyday operations

### Goal

Be able to look inside the cluster, change it, and undo a change.

### Steps and why

**Look at things.**

```sh
kubectl get all -n enjoythings                         # every resource at a glance
kubectl describe pod <pod> -n enjoythings              # image, probes, events for one pod
kubectl logs deployment/wallet -n enjoythings -f       # follow logs of the current pod
kubectl logs <pod> -n enjoythings --previous           # logs of the container that just crashed
kubectl get events -n enjoythings --sort-by=.metadata.creationTimestamp
kubectl exec -it deployment/postgres -n enjoythings -- psql -U enjoythings enjoythings
```

`describe` is where scheduling and probe failures are explained. `logs` is the
application's own output. `events` is the cluster's timeline. Ninety percent of
debugging is these three commands.

**Scale and restart.**

```sh
kubectl scale deployment/wallet --replicas=2 -n enjoythings
kubectl rollout status deployment/wallet -n enjoythings
kubectl rollout restart deployment/wallet -n enjoythings
```

`scale` changes the desired replica count. `rollout restart` replaces pods one
at a time; because the chart sets `maxUnavailable: 0`, a new pod must be ready
before an old one is removed, so the API stays up. The repository ships a
validator for exactly this, see `make wallet-rollout-test` in
`services/docs/phase3/kubernetes-local-guide.md`.

**Ship a code change.** Rebuild and reload the image, then tell Kubernetes to
pick it up.

```sh
cd services
docker build --build-arg SERVICE=wallet -t enjoythings/wallet:local .
kind load docker-image enjoythings/wallet:local --name enjoythings
kubectl rollout restart deployment/wallet -n enjoythings
```

The restart is required because the tag did not change. Kubernetes compares
image *references*, not contents, and `IfNotPresent` plus an unchanged tag means
"nothing to do". In a shared cluster you avoid this trap by tagging every build
uniquely, for example with the git SHA, and passing the new tag to Helm:

```sh
helm upgrade enjoythings charts/enjoythings -n enjoythings \
  -f charts/enjoythings/values-local.yaml \
  -f charts/enjoythings/values-secrets.yaml \
  --set applications.wallet.image=enjoythings/wallet:$(git rev-parse --short HEAD)
```

**Change configuration.** Edit your values file, then re-run the `helm upgrade`
from section 7. Helm computes the difference and updates only the affected
resources. A ConfigMap change alone does **not** restart pods, so follow with
`kubectl rollout restart` for the services that read the changed key.

**Inspect and roll back a release.**

```sh
helm history enjoythings -n enjoythings
helm get values enjoythings -n enjoythings
helm rollback enjoythings <revision> -n enjoythings
```

Every `helm upgrade` creates a numbered revision. `rollback` re-applies an
older one, which is the fastest way out of a bad configuration change.

---

## 10. Optional add-ons

Each of these is documented in the repository; this section says what they are
for and where to look.

**Mutual TLS between services.** By default gRPC inside the cluster is plain
text. Setting `mtls.enabled=true` makes every gRPC server require a client
certificate signed by a shared CA. You either build the certificates with
`make certs` and load them into a Secret named `enjoythings-mtls`, or install
cert-manager and set `mtls.certManager.enabled=true` so certificates are issued
and renewed automatically. Steps in
`services/docs/phase3/kubernetes-local-guide.md` section 5a.

**Autoscaling.** `hpa.enabled=true` adds HorizontalPodAutoscalers for wallet and
ledger that add pods when CPU passes 70 percent. It needs metrics-server
installed first, otherwise the HPA shows `<unknown>` targets forever. Steps in
the same guide, section 9.

**Metrics and traces.** Every pod carries `prometheus.io/scrape` annotations, so
a Prometheus that honors them, such as the `kube-prometheus-stack` Helm chart
with annotation-based discovery, collects the same metrics the Compose stack
shows in Grafana. Traces are off because `OTEL_EXPORTER_OTLP_ENDPOINT` is not
set in the chart; to send them to a Jaeger you deploy, add the variable under
`applications.<name>.env` for each service.

---

## 11. Troubleshooting

Start with `kubectl get pods -n enjoythings`, find the pod that is not
`1/1 Running`, then match its `STATUS` below.

| Symptom | Meaning | What to do |
| --- | --- | --- |
| `ImagePullBackOff` / `ErrImageNeverPull` | The node does not have the image tag. | Section 4: rebuild and `kind load` it. Confirm the tag with `crictl images`. |
| `CrashLoopBackOff` | The container starts and exits repeatedly. | `kubectl logs <pod> --previous`. Usual causes: database password mismatch (section 6), wrong `DATABASE_URL`, migration error. |
| `Running` but `READY 0/1` for minutes | Readiness probe fails; a dependency is unreachable. | `kubectl describe pod` shows the probe result. Check that `postgres` and `kafka` pods are ready and that `kubectl get svc` lists the names the ConfigMap references (`postgres:5432`, `kafka:9092`, `wallet:9090`, `ledger:9091`, `saga-orchestrator:9093`, `verification:9094`). |
| `Pending` | No node has room. | `kubectl describe pod` says `Insufficient cpu` or `memory`. Give Docker Desktop more resources or lower the `resources` in `values.yaml`. |
| `kafka-topic-init` stays `Running` or fails | The Job cannot reach `kafka:9092`. | `kubectl logs job/kafka-topic-init`. If Kafka is unhealthy, fix that first; the Job reruns on the next `helm upgrade`. |
| `helm upgrade` times out | Some pod never became ready within `--timeout`. | The release is `failed` but pods still run. Diagnose with the rows above, then re-run the same `helm upgrade`. |
| `curl localhost:18080` refused | Gateway not ready, or the cluster was created without `k8s/kind/cluster.yaml`. | Check the gateway pod. If the mapping is missing, `kubectl port-forward service/gateway 18080:8080 -n enjoythings` instead. |
| `kubectl` says `connection refused` or shows unexpected resources | Wrong context. | `kubectl config use-context kind-enjoythings`. |
| Smoke test `401` on every call | JWT secret in your shell differs from the one in the cluster. | `kubectl get secret enjoythings-secret -n enjoythings -o jsonpath='{.data.JWT_SECRET}' \| base64 -d` and compare. |
| Fraud worker logs show `fraud.error` for every transaction | LLM unreachable. Payments still complete. | Section 8e. Fix `llmProvidersJson`, re-run `helm upgrade`, then `kubectl rollout restart deployment/fraud-worker`. |
| Docker is not running | Nothing works, `kind` errors mention the Docker socket. | Start Docker Desktop, wait for it to be ready, retry. |

---

## 12. Cleanup

```sh
helm uninstall enjoythings -n enjoythings      # remove the release, keep the namespace
kubectl delete namespace enjoythings           # remove everything in the namespace
kind delete cluster --name enjoythings         # remove the cluster entirely
```

Use the first command when you want to reinstall from scratch but keep the
cluster. Use the last when you are done. The data stores have no persistent
volumes, so all three commands delete the databases and Kafka state.

---

## 13. Moving to a real cluster

The chart is honest about what it is: `Chart.yaml` describes it as a local
stack. Every item below is a gap between "runs on my laptop" and "runs for
other people". Do them in this order; each one is independent of the ones after
it, so you can stop wherever your needs end. `docs/aws-deployment.md` maps the
same list onto AWS services.

1. **Push images to a registry.** kind loading does not exist elsewhere. Tag
   images with the git SHA, push to a registry the cluster can pull from (ECR,
   GHCR, Artifact Registry), and override `applications.<name>.image` with the
   full registry path. If the registry is private, create an image pull Secret
   in the namespace and add `imagePullSecrets` to the pod template; the chart
   does not yet render that field. CI (`.github/workflows/ci.yml`) currently
   tests but does not build or push images, so this is the first CI change.

2. **Stop running databases and Kafka inside the chart.** The in-chart Postgres,
   TimescaleDB and Kafka are single-replica Deployments with no
   PersistentVolumeClaim. A pod restart loses every wallet, ledger entry and
   unconsumed event. For anything shared, set `postgres.enabled=false`,
   `fraudTimescaledb.enabled=false` and `kafka.enabled=false`, and point
   `secrets.databaseUrl`, `secrets.fraudDatabaseUrl` and `config.kafkaBrokers`
   at managed services or at operators built for stateful workloads (a
   Postgres operator, Strimzi for Kafka). Note that disabling `kafka` also
   disables the topic Job, so create the topics and the DLQ retention on the
   external broker yourself; the list is `kafka.topics` in `values.yaml`.

3. **Manage secrets outside the values file.** A values file on a laptop is a
   single point of leakage. Use External Secrets Operator or Sealed Secrets so
   the cluster fetches credentials from a vault at deploy time, and the git
   history never contains them. The chart writes one Secret named
   `enjoythings-secret`; set `secrets.create=false` and create that Secret from
   your vault with the same six keys. `docs/argocd-runbook.md` section 5 shows
   the manual version of this.

4. **Replace the NodePort with an Ingress or load balancer.** `NodePort 30080`
   depends on the kind port mapping. In a real cluster install an ingress
   controller (or the cloud load balancer controller), terminate TLS there, and
   route your hostname to `service/gateway:8080`. Remove `nodePort` from the
   gateway values so the Service becomes `ClusterIP`.

5. **Switch token verification to RS256.** With HS256 every service holds the
   signing secret. Set `JWT_ALG=RS256` and provide the issuer's public key
   through `JWT_PUBLIC_KEY_PEM` or a mounted `JWT_PUBLIC_KEY_FILE`, so services
   can verify tokens but never mint them. See "Token Verification" in the root
   `README.md`.

6. **Set `global.appEnv` to something other than `local`.** Outside `local` and
   `dev` the services sample traces at `OTEL_TRACES_SAMPLER_ARG` instead of
   recording everything.

7. **Size and protect the workloads.** Raise `replicas` to at least 2 for the
   request-serving services, tune `applicationResources` per service (the chart
   currently uses one block for all nine), enable the HPA, and add
   PodDisruptionBudgets so a node drain cannot take every replica at once.
   `docs/aws-deployment.md` section 6 has starting numbers.

8. **Turn on mTLS with cert-manager.** Section 10. Point
   `mtls.certManager.issuerRef` at a CA you control rather than the bootstrap
   self-signed one.

9. **Add observability.** Deploy Prometheus, Grafana and an OTLP collector into
   the cluster, or use managed equivalents, and set
   `OTEL_EXPORTER_OTLP_ENDPOINT` per service. The Grafana dashboards under
   `services/observability/grafana/dashboards` work unchanged.

10. **Bring the web UI and chat API into the chart.** Neither has a Dockerfile
    today. Add one for `app/main.py` (uv-based, like the fraud worker) and one
    for `web/` (Next.js standalone output), then add both to
    `applications` in `values.yaml` with their own ports and probes.

11. **One namespace per environment.** Install the same chart into `dev`,
    `staging` and `prod` namespaces or clusters with a values file each. The
    only differences between them should be image tags, replica counts,
    resource sizes and the external endpoints from steps 2 and 3.
