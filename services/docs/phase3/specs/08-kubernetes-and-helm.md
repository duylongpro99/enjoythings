# Phase 3.8: Local Kubernetes and Helm

**Priority:** P8 - deployment after services exist

**Session size:** Two to three implementation sessions

**Depends on:** P2, P3, P4, P5, P6, P7

## Goal

Deploy the complete Phase 3 system to a local `kind` Kubernetes cluster using
Helm. The setup must be suitable for a Kubernetes beginner to run, inspect,
break, recover, and remove without requiring a cloud account.

The local Kubernetes workflow complements Docker Compose. Docker Compose remains
the fastest earlier-phase workflow, while `kind` is the environment for learning
and validating Kubernetes deployment behavior.

## Problem

Phase 2 uses Docker Compose, where service startup order, local image builds,
ports, and dependencies are managed in one file. Kubernetes replaces those
mechanisms with Deployments, Services, Jobs, ConfigMaps, Secrets, probes, and
cluster networking.

Deploying only the Go services is not enough to run the system. A complete local
cluster also needs Postgres, Kafka, Kafka topic creation, the stub payment rail,
application images loaded into `kind`, and a documented way to access the
gateway.

## Learning Outcomes

After completing this phase, a beginner should be able to:

- Explain the difference between a Pod, Deployment, Service, Job, ConfigMap,
  Secret, and Helm release.
- Build application images and load them into a local `kind` cluster.
- Install, inspect, upgrade, and uninstall a Helm release.
- Use Kubernetes DNS names for service-to-service communication.
- Inspect Pods, logs, events, Services, ConfigMaps, Secrets, and Jobs.
- Understand how liveness and readiness probes affect traffic and restarts.
- Scale a Deployment and perform a rolling restart.
- Diagnose common failures such as `ImagePullBackOff`, `CrashLoopBackOff`, and
  failed readiness probes.

## Chosen Approach

Use one local umbrella Helm chart named `enjoythings` for the first Kubernetes
implementation. The chart owns the complete local stack and keeps values grouped
by component.

This is intentionally simpler than maintaining one independently versioned chart
per service. It allows a beginner to install and remove the whole system with one
command while still exposing all important Kubernetes resources. Splitting the
umbrella chart into reusable per-service charts is a possible later exercise,
not a Phase 3 requirement.

Postgres and Kafka run inside Kubernetes. This creates a complete, isolated
practice environment and teaches Kubernetes networking and dependency
initialization. Their configuration is local-development quality, not production
guidance.

## Scope

- Add a reproducible single-node `kind` cluster configuration.
- Add one local umbrella Helm chart for the complete Phase 3 stack.
- Deploy Postgres and Kafka inside the cluster.
- Add Deployments and Services for:
  - gateway
  - saga-orchestrator
  - wallet
  - ledger
  - payment-processor
  - verification
  - notification
  - stub-payment-rail
- Add a Kafka topic initialization Job that creates all required topics.
- Add ConfigMaps for non-secret configuration.
- Add Secrets for the database URL, Postgres password, and JWT secret.
- Add liveness and readiness probes to long-running application containers.
- Add resource requests and limits to application Deployments.
- Add optional HPA resources for Wallet and Ledger.
- Document the complete beginner workflow from cluster creation through cleanup.
- Keep Docker Compose available for existing local workflows.

## Out of Scope

- Production-grade Kubernetes hardening.
- Production-grade Postgres or Kafka operation.
- Persistent recovery guarantees after deleting the `kind` cluster.
- Multi-region or multi-cluster deployment.
- TLS, cert-manager, and external secret managers.
- Ingress controllers and public DNS.
- Prometheus, Grafana, Jaeger, and production observability.
- Building or publishing images in CI.
- Independently versioned Helm charts for every service.

## Proposed Layout

```text
services/
├── charts/
│   └── enjoythings/
│       ├── Chart.yaml
│       ├── values.yaml
│       ├── values-local.yaml
│       └── templates/
│           ├── _helpers.tpl
│           ├── configmap.yaml
│           ├── secret.yaml
│           ├── postgres.yaml
│           ├── kafka.yaml
│           ├── kafka-topic-job.yaml
│           ├── applications.yaml
│           ├── services.yaml
│           └── hpa.yaml
├── k8s/
│   └── kind/
│       └── cluster.yaml
└── docs/
    └── phase3/
        └── kubernetes-local-guide.md
```

Templates may be split into additional focused files if a template becomes hard
to understand. Avoid creating a generic abstraction that hides the Kubernetes
resources from a beginner.

## Component Design

### Local Cluster

Use a single-control-plane `kind` cluster named `enjoythings`. The cluster
configuration maps host port `18080` to the node so the gateway can optionally
be exposed with a NodePort. The beginner guide uses `kubectl port-forward` first
because it is explicit and does not require an ingress controller.

### Images

The existing `services/Dockerfile` builds one service image at a time with the
`SERVICE` build argument. The local workflow builds one image for each command
and loads it into `kind`.

Use deterministic local tags such as:

```text
enjoythings/gateway:local
enjoythings/wallet:local
enjoythings/ledger:local
```

Application values use `imagePullPolicy: IfNotPresent` so Kubernetes uses images
loaded into the `kind` node instead of trying to pull them from a registry.

### Infrastructure

Postgres runs as one local replica with a ClusterIP Service named `postgres`.
Kafka runs as one KRaft broker with a ClusterIP Service named `kafka`. The local
setup may use ephemeral storage because deleting the cluster is expected to
delete local practice data.

The application connection names are:

```text
postgres:5432
kafka:9092
wallet:9090
ledger:9091
saga-orchestrator:9093
verification:9094
stub-payment-rail:18090
```

### Initialization and Jobs

Database-backed Go services retain the repository's existing startup migration
behavior. Migrations are idempotent and protected by a Postgres advisory lock,
so concurrently starting services safely serialize migration execution. The
guide explains this behavior and shows how to inspect service logs when a
migration fails.

The Kafka topic Job creates all Phase 2 and Phase 3 topics with
`--if-not-exists`. It waits until Kafka is reachable and then exits successfully.

Explicit readiness waiting must ensure Kafka is available before the topic Job
runs. Application readiness probes remain the final protection against receiving
traffic too early.

### Application Deployments

Each Go service runs in its own Deployment. The gateway, Wallet, Ledger,
Saga Orchestrator, Verification, and any other gRPC-serving component also get
ClusterIP Services for their required HTTP or gRPC ports.

Start with one replica per service to reduce local resource use. Wallet and
Ledger can use two replicas when practicing rolling restarts.

Every long-running application container defines:

- An HTTP liveness probe using `/healthz`.
- An HTTP readiness probe using `/readyz`.
- CPU and memory requests and limits.
- Environment variables sourced from a shared ConfigMap and Secret where
  appropriate.

### Configuration and Secrets

Non-secret settings belong in a ConfigMap, including service addresses, Kafka
topics, consumer groups, timeouts, and local feature modes.

Secret values belong in a Kubernetes Secret, including:

- `DATABASE_URL`
- `POSTGRES_PASSWORD`
- `JWT_SECRET`

The committed local values contain development-only defaults. The guide must
state clearly that base64-encoded Kubernetes Secrets are not encrypted and are
not production secret management.

### Autoscaling

Wallet and Ledger include optional HPA resources, disabled by default. The guide
documents that HPA requires Metrics Server and provides an optional local
installation step. HPA practice must not block the base stack from running.

## Beginner Local Runbook Requirements

The detailed guide at `services/docs/phase3/kubernetes-local-guide.md` must
explain both what each command does and what successful output generally looks
like.

### 1. Verify Prerequisites

Required tools:

```sh
docker version
kind version
kubectl version --client
helm version
```

The guide must explain that `kind` runs Kubernetes nodes as Docker containers,
so Docker must be running before cluster creation.

### 2. Create and Inspect the Cluster

```sh
cd services
kind create cluster --name enjoythings --config k8s/kind/cluster.yaml
kubectl cluster-info --context kind-enjoythings
kubectl get nodes
```

Explain Kubernetes contexts and how `kubectl` selects the target cluster.

### 3. Build and Load Images

Build every required service image from the existing Dockerfile, then load it:

```sh
docker build --build-arg SERVICE=wallet -t enjoythings/wallet:local .
kind load docker-image enjoythings/wallet:local --name enjoythings
```

The final guide provides commands for every application image and explains why
locally built images must be loaded into the `kind` node.

### 4. Validate the Chart Before Installation

```sh
helm lint charts/enjoythings
helm template enjoythings charts/enjoythings -f charts/enjoythings/values-local.yaml
```

Explain that `helm lint` checks chart structure and `helm template` renders the
Kubernetes YAML without installing it.

### 5. Install the Stack

```sh
kubectl create namespace enjoythings
helm upgrade --install enjoythings charts/enjoythings \
  --namespace enjoythings \
  -f charts/enjoythings/values-local.yaml \
  --wait \
  --timeout 10m
```

Explain namespace isolation, Helm release names, `--wait`, and timeouts.

### 6. Inspect Workloads

```sh
kubectl get all -n enjoythings
kubectl get pods -n enjoythings -w
kubectl get jobs -n enjoythings
kubectl get configmaps,secrets -n enjoythings
kubectl describe pod <pod-name> -n enjoythings
kubectl logs <pod-name> -n enjoythings
kubectl get events -n enjoythings --sort-by=.metadata.creationTimestamp
```

Explain common Pod states and the difference between `describe`, `logs`, and
events.

### 7. Access and Verify the Gateway

Use port-forwarding as the default beginner path:

```sh
kubectl port-forward service/gateway 18080:8080 -n enjoythings
curl http://localhost:18080/healthz
curl http://localhost:18080/readyz
```

The guide then runs an existing smoke or contract test against the forwarded
gateway when the required Phase 3 test exists.

### 8. Practice Core Kubernetes Operations

The guide includes safe exercises:

```sh
kubectl scale deployment/wallet --replicas=2 -n enjoythings
kubectl rollout restart deployment/wallet -n enjoythings
kubectl rollout status deployment/wallet -n enjoythings
kubectl delete pod <wallet-pod> -n enjoythings
kubectl logs deployment/wallet -n enjoythings
helm get values enjoythings -n enjoythings
helm history enjoythings -n enjoythings
```

Each exercise explains the expected controller behavior and how to verify
recovery.

### 9. Troubleshooting

The guide includes a symptom-based section for:

- Docker is not running.
- `kind` cluster creation fails.
- `kubectl` is using the wrong context.
- Pod is in `ImagePullBackOff`.
- Pod is in `CrashLoopBackOff`.
- Pod never becomes ready.
- A database migration fails during application startup.
- Kafka topic Job fails.
- A Service name or port is incorrect.
- Helm install or upgrade times out.
- Local machine does not have enough CPU or memory.

### 10. Cleanup

```sh
helm uninstall enjoythings -n enjoythings
kubectl delete namespace enjoythings
kind delete cluster --name enjoythings
```

Explain that deleting the `kind` cluster deletes all data stored inside it.

## Error Handling and Readiness

- Infrastructure containers must expose readiness checks suitable for waiting
  Jobs and application dependencies.
- Database migrations and the Kafka topic Job must fail visibly rather than
  silently ignoring errors.
- Application Deployments must not become ready when required runtime
  dependencies are unavailable.
- Liveness probes must only detect whether the process is stuck or unhealthy;
  they must not restart a healthy process solely because a dependency is
  temporarily unavailable.
- Helm installation failures must leave enough Job logs and Pod events for a
  beginner to diagnose the cause.

## Testing and Verification

Implementation verification must include:

```sh
helm lint services/charts/enjoythings
helm template enjoythings services/charts/enjoythings \
  -f services/charts/enjoythings/values-local.yaml
```

When Docker is available, verification must also include:

- Create a fresh `kind` cluster.
- Build and load every local application image.
- Install the Helm release into a new namespace.
- Confirm the Kafka topic initialization Job completes.
- Confirm database-backed services complete startup migrations.
- Confirm all application Pods become ready.
- Confirm gateway `/healthz` and `/readyz` succeed through port-forwarding.
- Run the available Phase 3 smoke or contract test.
- Scale Wallet to two replicas.
- Restart Wallet and confirm the rollout succeeds.
- Delete one Wallet Pod and confirm the Deployment replaces it.
- Uninstall the release and delete the cluster.

## Acceptance Criteria

- A beginner can follow `services/docs/phase3/kubernetes-local-guide.md` from a
  running Docker installation to a healthy local system without undocumented
  steps.
- The complete Phase 3 stack, including Postgres and Kafka, runs in local
  `kind`.
- One Helm command installs or upgrades the complete local stack.
- Existing startup database migrations succeed under their Postgres advisory
  lock.
- Kafka topic initialization runs as a visible, idempotent Kubernetes Job.
- All long-running application Pods expose liveness and readiness probes.
- Readiness probes prevent traffic before required dependencies are available.
- Config is supplied through ConfigMaps and Secrets rather than hard-coded in
  Deployment templates.
- Locally built images run without requiring a container registry.
- `kubectl rollout restart deployment/wallet` succeeds while Wallet has at
  least two ready replicas.
- HPA for Wallet and Ledger can be enabled after installing Metrics Server, but
  HPA is not required for the base local installation.
- Docker Compose remains available and unchanged for earlier local workflows.
