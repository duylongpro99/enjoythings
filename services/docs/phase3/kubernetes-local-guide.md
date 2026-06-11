# Local Kubernetes and Helm Guide

This guide deploys the complete Phase 3 service stack to a local `kind`
Kubernetes cluster with Helm. Docker Compose remains available for the faster
local workflow; this guide is for learning Kubernetes resources and release
operations.

The local chart starts Postgres, Kafka, the Kafka topic initialization Job, the
stub payment rail, and all Go services inside Kubernetes. It uses development
defaults only.

## 1. Verify Prerequisites

Run these commands from any directory:

```sh
docker version
kind version
kubectl version --client
helm version
```

Docker must be running before creating the cluster. `kind` runs Kubernetes nodes
as Docker containers, so cluster creation fails when Docker is stopped.

`kubectl` talks to the cluster selected by its current context. After creating
the cluster below, the context is named `kind-enjoythings`.

## 2. Create and Inspect the Cluster

```sh
cd services
kind create cluster --name enjoythings --config k8s/kind/cluster.yaml
kubectl cluster-info --context kind-enjoythings
kubectl get nodes
```

Successful output usually shows one Ready control-plane node. The kind config
also maps host port `18080` to the node port used by the gateway service, but
this guide uses `kubectl port-forward` first because it is explicit.

If `kubectl` points at another cluster, switch back with:

```sh
kubectl config use-context kind-enjoythings
```

## 3. Build and Load Images

The Dockerfile builds one service command at a time with the `SERVICE` build
argument. `kind` does not automatically see images from your host Docker daemon,
so each image must also be loaded into the cluster node.

```sh
docker build --build-arg SERVICE=gateway -t enjoythings/gateway:local .
kind load docker-image enjoythings/gateway:local --name enjoythings

docker build --build-arg SERVICE=saga-orchestrator -t enjoythings/saga-orchestrator:local .
kind load docker-image enjoythings/saga-orchestrator:local --name enjoythings

docker build --build-arg SERVICE=wallet -t enjoythings/wallet:local .
kind load docker-image enjoythings/wallet:local --name enjoythings

docker build --build-arg SERVICE=ledger -t enjoythings/ledger:local .
kind load docker-image enjoythings/ledger:local --name enjoythings

docker build --build-arg SERVICE=payment-processor -t enjoythings/payment-processor:local .
kind load docker-image enjoythings/payment-processor:local --name enjoythings

docker build --build-arg SERVICE=verification -t enjoythings/verification:local .
kind load docker-image enjoythings/verification:local --name enjoythings

docker build --build-arg SERVICE=notification -t enjoythings/notification:local .
kind load docker-image enjoythings/notification:local --name enjoythings

docker build --build-arg SERVICE=stub-payment-rail -t enjoythings/stub-payment-rail:local .
kind load docker-image enjoythings/stub-payment-rail:local --name enjoythings
```

The chart uses `imagePullPolicy: IfNotPresent`, so Kubernetes uses these loaded
local images instead of pulling from a registry.

## 4. Validate the Chart Before Installation

```sh
helm lint charts/enjoythings
helm template enjoythings charts/enjoythings -f charts/enjoythings/values-local.yaml
```

`helm lint` checks chart structure. `helm template` renders the Kubernetes YAML
without installing anything. The rendered output should include Deployments,
Services, a ConfigMap, a Secret, and the `kafka-topic-init` Job.

## 5. Install the Stack

```sh
kubectl create namespace enjoythings
helm upgrade --install enjoythings charts/enjoythings \
  --namespace enjoythings \
  -f charts/enjoythings/values-local.yaml \
  --wait \
  --timeout 10m
```

The namespace keeps this practice stack separate from other cluster resources.
The Helm release name is `enjoythings`. `--wait` asks Helm to wait for workloads
and hook Jobs to finish becoming ready. The timeout gives Kafka and application
startup enough room on slower machines.

Postgres migrations run during database-backed service startup. The repository
migrations are idempotent and protected by a Postgres advisory lock, so multiple
services can start together and safely serialize migration execution.

## 6. Inspect Workloads

```sh
kubectl get all -n enjoythings
kubectl get pods -n enjoythings -w
kubectl get jobs -n enjoythings
kubectl get configmaps,secrets -n enjoythings
kubectl describe pod <pod-name> -n enjoythings
kubectl logs <pod-name> -n enjoythings
kubectl get events -n enjoythings --sort-by=.metadata.creationTimestamp
```

A Pod is one or more containers. A Deployment keeps the requested number of Pods
running. A Service gives Pods a stable DNS name such as `wallet` or `kafka`.
A Job runs work that should finish, such as creating Kafka topics. A ConfigMap
stores non-secret settings. A Secret stores sensitive settings, but Kubernetes
Secrets are only base64-encoded by default and are not production secret
management.

Use `describe` for scheduling, image, probe, and event details. Use `logs` for
application output. Use events to see the timeline of cluster decisions.

## 7. Access and Verify the Gateway

In one terminal:

```sh
kubectl port-forward service/gateway 18080:8080 -n enjoythings
```

In another terminal:

```sh
curl http://localhost:18080/healthz
curl http://localhost:18080/readyz
```

Both commands should return an HTTP 200 response. Then run the available Phase 3
contract test against the forwarded gateway:

```sh
go test ./devtools -run TestPhase3Contract
```

## 8. Practice Core Kubernetes Operations

Scale Wallet to two replicas:

```sh
kubectl scale deployment/wallet --replicas=2 -n enjoythings
kubectl rollout status deployment/wallet -n enjoythings
kubectl get pods -n enjoythings -l app.kubernetes.io/component=wallet
```

Restart Wallet and wait for the rollout:

```sh
kubectl rollout restart deployment/wallet -n enjoythings
kubectl rollout status deployment/wallet -n enjoythings
```

With the gateway port-forward from section 7 still running, create an
authenticated Wallet probe:

```sh
cd services
USER_ID=11111111-1111-1111-1111-111111111111
GATEWAY_TOKEN="$(JWT_SECRET=local-dev-jwt-secret-change-me go run ./cmd/devtoken -user-id "$USER_ID")"
WALLET_ID="$(
  curl -fsS -X POST http://localhost:18080/v1/wallets \
    -H "Authorization: Bearer $GATEWAY_TOKEN" \
    -H "Content-Type: application/json" \
    -d '{"currency":"USD"}' |
  jq -r .id
)"
WALLET_PROBE_URL="http://localhost:18080/v1/wallets/$WALLET_ID" \
GATEWAY_TOKEN="$GATEWAY_TOKEN" \
  make wallet-rollout-test
```

The validator scales Wallet to two replicas before restarting it and
continuously requests Gateway `/readyz` and the Wallet-backed API endpoint. It
fails if readiness or authenticated Wallet requests are interrupted, or if
fewer than two Wallet replicas are ready after the rollout. The setup requires
`jq` to extract the created Wallet ID.

Delete one Wallet Pod and watch the Deployment replace it:

```sh
kubectl delete pod <wallet-pod> -n enjoythings
kubectl get pods -n enjoythings -l app.kubernetes.io/component=wallet -w
```

Inspect Helm release data:

```sh
helm get values enjoythings -n enjoythings
helm history enjoythings -n enjoythings
kubectl logs deployment/wallet -n enjoythings
```

## 9. Optional HPA Practice

Wallet and Ledger include HorizontalPodAutoscaler resources, disabled by
default. HPA needs Metrics Server, so do not enable it for the base install.

For local practice, install Metrics Server first, then upgrade the release:

```sh
kubectl apply -f https://github.com/kubernetes-sigs/metrics-server/releases/latest/download/components.yaml
helm upgrade --install enjoythings charts/enjoythings \
  --namespace enjoythings \
  -f charts/enjoythings/values-local.yaml \
  --set hpa.enabled=true \
  --wait \
  --timeout 10m
kubectl get hpa -n enjoythings
```

## 10. Troubleshooting

Docker is not running: start Docker, then rerun `kind create cluster`.

Cluster creation fails: check `docker ps` and delete any partial cluster with
`kind delete cluster --name enjoythings` before retrying.

Wrong context: run `kubectl config current-context` and switch with
`kubectl config use-context kind-enjoythings`.

`ImagePullBackOff`: the image tag in chart values was not loaded into kind.
Rebuild and rerun `kind load docker-image ... --name enjoythings`.

`CrashLoopBackOff`: inspect the container logs with `kubectl logs <pod-name>
-n enjoythings` and the events with `kubectl describe pod <pod-name>
-n enjoythings`.

Pod never becomes ready: check the readiness probe in `describe pod`, then check
the dependency it reports. Database-backed services need Postgres reachable and
migrations to complete.

Database migration fails: inspect the service logs. Because migrations use a
Postgres advisory lock, another service may be waiting; a true migration error
appears in the failing service logs.

Kafka topic Job fails: run `kubectl logs job/kafka-topic-init -n enjoythings`.
It should wait for `kafka:9092` and create topics with `--if-not-exists`.

Service name or port is incorrect: compare `kubectl get svc -n enjoythings`
with the expected DNS names: `postgres:5432`, `kafka:9092`, `wallet:9090`,
`ledger:9091`, `saga-orchestrator:9093`, `verification:9094`, and
`stub-payment-rail:18090`.

Helm install or upgrade times out: inspect Pods, Jobs, logs, and events. Slow
machines may need more time, but a stuck Pod usually points to an image,
dependency, or readiness problem.

Machine has too little CPU or memory: stop other local workloads or delete the
cluster. Kafka and Postgres are the heaviest containers in this stack.

## 11. Cleanup

```sh
helm uninstall enjoythings -n enjoythings
kubectl delete namespace enjoythings
kind delete cluster --name enjoythings
```

Deleting the kind cluster deletes all data stored inside it, including the local
Postgres database and Kafka state.
