# Progressive Delivery Runbook

This runbook changes how a new gateway version reaches users. A rolling update
swaps every old pod for a new one as fast as readiness allows, and nothing
checks whether the new version is any good. Progressive delivery gives a small
share of pods the new version, watches metrics, grows the share only when a
human or a metric says yes, and reverses on its own when a metric says no.

The tool is Argo Rollouts, from the same project as Argo CD. It is applied to
the `gateway` service because that is the one thing users reach directly. The
Helm chart is not modified. Same format as the other runbooks: goal, commands,
why, check. Read [`k8s-deployment-runbook.md`](./k8s-deployment-runbook.md)
first; this one assumes a running cluster and a deployed chart.

## Before you start

You need the kind cluster `enjoythings`, the nine images loaded, and the chart
installed into the `enjoythings` namespace, from section 7 of the Kubernetes
runbook or from the Argo CD runbook. Sections 2 to 11 are written for the Helm
path; if Argo CD manages the chart, read section 12 before section 5, because
self-heal fights two of the steps unless told not to. Section 6 onward also
needs the Prometheus that the observability runbook installs as
`kube-prometheus-stack` in the `monitoring` namespace; only the metric-driven
analysis step depends on it.

## What you will end up with

| Thing | Where | Role |
| --- | --- | --- |
| Argo Rollouts controller v1.10.0 | namespace `argo-rollouts` | Watches `Rollout` resources and drives their ReplicaSets. |
| `kubectl argo rollouts` plugin v1.10.0 | your laptop | Watch, promote, abort, and serve the dashboard on port 3100. |
| `Rollout/gateway` | `services/k8s/rollouts/rollout-gateway.yaml` | Canary strategy that references the chart's `gateway` Deployment. |
| `Service/gateway-stable`, `Service/gateway-canary` | `services/k8s/rollouts/services.yaml` | Reach one side of a canary on purpose. |
| `AnalysisTemplate/gateway-success-rate` | `services/k8s/rollouts/analysistemplate-gateway-success-rate.yaml` | Asks Prometheus for the gateway's non-5xx ratio. |
| `traffic.sh` | `services/k8s/rollouts/traffic.sh` | Steady requests to `/readyz` so the analysis has data. |
| Blue-green variant | `services/k8s/rollouts/bluegreen/` | Section 10. Only one of the two Rollouts exists at a time. |
| Terraform stage `25-rollouts` | `infra/terraform/25-rollouts` | Optional. Installs the controller with the Helm provider, same pattern as stage 20. |

---

## 1. Progressive delivery vocabulary you need

| Term | What it is |
| --- | --- |
| **Rollout** | Argo Rollouts' replacement for a Deployment. Same pod template and ReplicaSets underneath, but a strategy with steps instead of a plain rolling update. |
| **workloadRef** | A Rollout field that says "take the pod template from that Deployment" instead of carrying its own. The Deployment stays. The Rollout runs the pods. |
| **Stable / canary ReplicaSet** | The version users trust, and the version under test. The Rollout owns both. |
| **Canary** | Run the new version on a fraction of pods, grow the fraction in steps, watch. |
| **Blue-green** | Run the new version at full size next to the old one, test it on a side door, then switch all traffic at once. |
| **setWeight** | A step naming the percentage of traffic the canary should get. Without a service mesh or ingress controller it is approximated by pod counts. |
| **Pause** | A step that stops. With a duration it resumes itself. Without one it waits for a promote. |
| **AnalysisTemplate / AnalysisRun** | A reusable metric check, and one execution of it during one rollout. |
| **Promote** | Move to the next step by hand. `--full` skips every remaining step. |
| **Abort** | Stop the update and send all traffic back to the stable version. Automatic when analysis fails or the progress deadline passes. |
| **Degraded** | The Rollout's status after an abort. Stable is serving; the desired version was rejected. |
| **Traffic router** | An Istio, NGINX, ALB or similar integration that sets exact weights independent of pod counts. Not installed here. |

---

## 2. Install the kubectl plugin

### Goal

`kubectl argo rollouts` works on your laptop.

### Steps

```sh
brew install argoproj/tap/kubectl-argo-rollouts
kubectl argo rollouts version
```

### Why

- The controller speaks only through Kubernetes resources. The plugin renders
  them as a coloured tree, turns promote and abort into one command instead of
  a JSON patch, and serves the dashboard. Its version should match the
  controller from section 3; a mismatch works but prints a warning.

### Check

`kubectl argo rollouts version` prints `kubectl-argo-rollouts: v1.10.0`.

---

## 3. Install the controller

### Goal

The controller running in its own namespace with its CRDs installed.

### Steps

Pick one of three ways.

```sh
# A. Pinned upstream manifests
kubectl create namespace argo-rollouts
kubectl apply -n argo-rollouts -f https://github.com/argoproj/argo-rollouts/releases/download/v1.10.0/install.yaml

# B. Helm chart 2.43.0, which ships v1.10.0
helm repo add argo https://argoproj.github.io/argo-helm
helm upgrade --install argo-rollouts argo/argo-rollouts \
  --version 2.43.0 --namespace argo-rollouts --create-namespace \
  --set controller.replicas=1 --wait

# C. Terraform, after stage 10 of the Terraform runbook has been applied
cd infra/terraform/25-rollouts && terraform init && terraform apply
```

### Why

- The controller is a Deployment plus five CRDs. Until they exist, applying a
  Rollout fails with `no matches for kind "Rollout"`. That ordering is why the
  Terraform stage is a separate root module, as stage 30 is from stage 20.
- The chart defaults to two controller replicas. One is enough on a one-node
  kind cluster; stage 25 sets the same value and reads stage 10's state in its
  `providers.tf` exactly as stage 20 does.

### Check

`kubectl get pods -n argo-rollouts` shows `argo-rollouts-...` as `1/1 Running`,
and `kubectl get crd | grep argoproj.io` lists `rollouts.argoproj.io`.

---

## 4. Read the manifests before applying them

### Goal

Understand why the Rollout references the Deployment instead of replacing it,
and what that costs.

### Steps

```sh
cat services/k8s/rollouts/rollout-gateway.yaml services/k8s/rollouts/services.yaml
kubectl argo rollouts lint -f services/k8s/rollouts/rollout-gateway.yaml
```

### Why

The usual migration edits the chart: `kind: Deployment` becomes `kind:
Rollout`, the `apiVersion` changes, and the rolling update becomes a canary
block. That is a change to a template that renders nine services, inherited by
every consumer whether they run the controller or not.

`workloadRef` avoids it. The Rollout has no pod template. It names the
Deployment, and the controller copies that Deployment's template into
ReplicaSets it owns. Each consequence is a field in the manifest:

| Field | Value | Why |
| --- | --- | --- |
| `spec.workloadRef` | `apps/v1` `Deployment` `gateway` | Where the pod template comes from. Changing the image on the Deployment starts a rollout. |
| `spec.workloadRef.scaleDown` | `onsuccess` | Once the first revision is healthy, the Deployment is scaled to zero. `never` leaves both running. `progressively` trades pods one for one. |
| `spec.selector` | the chart's two selector labels | Must match the pods the copied template produces. |
| `spec.replicas` | `4` | Total pods across both versions. Four gives clean 25 percent steps. The Deployment's `replicas: 1` stops mattering. |
| `progressDeadlineAbort` | `true` | A canary that never becomes Ready is aborted after `progressDeadlineSeconds`. Paused steps do not count. |
| `strategy.canary.steps` | 25, pause 60s, analysis, 50, pause, 75, pause 30s | The plan. Section 7 walks through it. |
| `stableService`, `canaryService` | the two extra Services | Optional. The controller narrows each selector to one ReplicaSet so you can curl one side. |

The chart's `gateway` Service keeps working because the Rollout's pods carry
the same two labels it selects. `http://localhost:18080` sees Deployment pods,
then a mix, then only Rollout pods. And because there is no service mesh or
ingress controller, that Service is the traffic model: it spreads connections
roughly evenly, so 1 canary pod out of 4 gets about a quarter of requests. Argo
Rollouts calls this the basic canary.

| | `workloadRef` here | Convert the Deployment in the chart |
| --- | --- | --- |
| Chart changes | None | Template plus values for the strategy |
| Where you change the image | The Deployment, as before | The Rollout |
| Objects per service | Deployment at zero replicas plus a Rollout | One Rollout |
| Argo CD | Must ignore the Deployment's `replicas`, section 12 | Understands Rollout health natively |
| Back to plain Kubernetes | Delete the Rollout, scale the Deployment up | Revert the chart |
| Fits when | Trying progressive delivery on one service without a chart release | The whole platform adopts it |

### Check

You can say which object you edit to ship a new gateway image, and what
happens to the Deployment's pod after the Rollout becomes healthy. The lint
prints no errors.

---

## 5. Apply the Rollout and watch it take over

### Goal

Four gateway pods owned by the Rollout, the Deployment at zero, and
`http://localhost:18080/readyz` answering throughout.

### Steps

```sh
# terminal A
kubectl argo rollouts get rollout gateway -n enjoythings --watch

# terminal B
kubectl apply -f services/k8s/rollouts/
kubectl get pods -n enjoythings -l app.kubernetes.io/component=gateway -w
```

### Why

- `apply -f` on a directory is not recursive, so it applies the three YAML
  files, ignores the script, and leaves `bluegreen/` alone.
- The first revision has no stable version to compare against, so the
  controller skips the steps and brings all four pods up at once. The steps run
  from the second revision onward.
- While the four pods come up, the Deployment's pod keeps serving. When the
  Rollout reports healthy, `scaleDown: onsuccess` sets the Deployment to zero.
  The watch shows only the Rollout's own ReplicaSet, marked `stable`, because
  the Rollout reads the Deployment's template and never manages its pods.

### Check

`kubectl get deployment gateway -n enjoythings` shows `READY 0/0`. The watch
shows `Status: ✔ Healthy`, `Available: 4`, one ReplicaSet with the `stable`
role. `curl -i http://localhost:18080/readyz` returns `200`.

---

## 6. Confirm Prometheus sees the gateway

### Goal

The query the AnalysisTemplate will run returns a number.

### Steps

```sh
# terminal A
kubectl port-forward svc/prometheus-operated -n monitoring 9090:9090

# terminal B
services/k8s/rollouts/traffic.sh &
sleep 60
curl -s http://localhost:9090/api/v1/query \
  --data-urlencode 'query=sum(rate(service_http_requests_total{service="gateway",route!="/metrics",status!~"5.."}[1m])) / sum(rate(service_http_requests_total{service="gateway",route!="/metrics"}[1m]))' | jq '.data.result'
kill %1
```

### Why

- The gateway registers `service_http_requests_total` in
  `services/internal/telemetry/metrics.go` with labels `service`, `method`,
  `route` and `status`. The `service` label is set by the process itself, so
  the query works whatever labels the scrape adds. Probes and `traffic.sh`
  land on `route="/readyz"`. `/metrics` is excluded so Prometheus's own scrapes
  do not inflate the count.
- The address `http://prometheus-operated.monitoring.svc:9090` is a **value
  to check**. `prometheus-operated` is the Service the Prometheus Operator
  creates; `monitoring` is the observability runbook's namespace. If yours
  differ, edit the `prometheus-address` default in the AnalysisTemplate.
- `kube-prometheus-stack` does not scrape `prometheus.io/scrape` annotations
  by itself; the observability runbook adds that.

### Check

`.data.result` is a one-element list whose `value` is `"1"` or close to it. An
empty list means Prometheus has no gateway samples yet.

---

## 7. Run a canary

### Goal

A second gateway revision travels through every step, and you see each one.

### Steps

Create a new tag. Same binary, different tag, so the Rollout sees a change.
Then watch, generate traffic, and start the rollout in three terminals:

```sh
docker tag enjoythings/gateway:local enjoythings/gateway:v2
kind load docker-image enjoythings/gateway:v2 --name enjoythings

kubectl argo rollouts get rollout gateway -n enjoythings --watch      # terminal A
services/k8s/rollouts/traffic.sh                                      # terminal B
kubectl argo rollouts set image gateway gateway=enjoythings/gateway:v2 -n enjoythings   # terminal C
```

When terminal A shows `Paused` at step 5, look around, then promote:

```sh
kubectl get analysisrun -n enjoythings
kubectl describe analysisrun -n enjoythings | grep -A12 'Metric Results'
kubectl port-forward svc/gateway-canary -n enjoythings 18081:8080 &
curl -s http://localhost:18081/readyz; echo
kill %1
kubectl argo rollouts promote gateway -n enjoythings
```

### Why

1. **`set image`.** With `workloadRef` the plugin patches the **Deployment**,
   not the Rollout; `kubectl set image deployment/gateway ...` does the same.
   The controller sees the template change and creates a new ReplicaSet.
2. **`setWeight: 25`.** The new ReplicaSet gets 1 pod, stable keeps 3.
   Terminal B keeps printing `2xx` because both versions answer `/readyz`.
3. **`pause: 60s`.** A soak that resumes by itself: time, not permission.
4. **`analysis`.** An `AnalysisRun` appears. After the 30 second
   `initialDelay` it queries Prometheus every 30 seconds, four times, and
   compares `result[0]` with the `success-threshold` argument, `0.95`. Four
   passes and the step is done. One failure and the Rollout aborts.
5. **`setWeight: 50`.** Two and two.
6. **`pause: {}`.** Indefinite. Nothing moves until `promote`. Paused Rollouts
   are exempt from `progressDeadlineSeconds`, so it can wait all day.
7. **`promote`**, `setWeight: 75`, a 30 second pause, then the canary
   ReplicaSet becomes stable, the old one scales to zero, and the Rollout is
   `Healthy` at revision 2. `gateway-canary` answered on `18081` because the
   controller added `rollouts-pod-template-hash` to that Service's selector.

### Check

`Images: enjoythings/gateway:v2 (stable)`, one ReplicaSet at 4 pods, the
AnalysisRun `Successful` with 4 measurements, and terminal B never printed a
`non-2xx` count above zero.

---

## 8. The dashboard

### Goal

The same rollout, as a web page.

### Steps

```sh
kubectl argo rollouts dashboard -n enjoythings
```

Open <http://localhost:3100>.

### Why

- The plugin serves the dashboard from your laptop using your kubeconfig;
  nothing is installed in the cluster. Stage 25 has a `dashboard_enabled`
  variable for the in-cluster version, off by default.
- The Rollout page draws the same data as `get rollout --watch`, and its
  **Promote**, **Promote Full**, **Abort**, **Retry** and **Restart** buttons
  call the same API as the plugin subcommands.

### Check

The list page shows `gateway`, strategy `Canary`, status `Healthy`. The detail
page shows seven steps.

---

## 9. Abort, rollback and retry

### Goal

Know the ways a rollout stops, what state that leaves, and how to move on.

### Steps and why

**9a. Manual abort.** During any step:

```sh
kubectl argo rollouts abort gateway -n enjoythings
```

The canary ReplicaSet scales to zero, stable back to 4, and the Rollout is
`✖ Degraded` with message `RolloutAborted`. Users are on stable, but the
Deployment still says `v2`, so the desired state is the rejected version. That
is why it is Degraded and not Healthy.

**9b. Automatic abort.** Two triggers do the same without you: a failed
AnalysisRun, and a canary that is not Ready within `progressDeadlineSeconds`
while `progressDeadlineAbort` is true. Exercises 3 and 4 provoke each.

**9c. Roll back the desired state.**

```sh
kubectl argo rollouts set image gateway gateway=enjoythings/gateway:local -n enjoythings
```

The desired template now equals the stable ReplicaSet's template. The Rollout
returns to `Healthy` with no pod restarts.

**9d. Retry, or skip the plan.**

```sh
kubectl argo rollouts retry rollout gateway -n enjoythings     # start over from setWeight: 25
kubectl argo rollouts promote gateway -n enjoythings --full    # skip every remaining pause and analysis
```

`retry` is for failures that were environmental. `--full` is for an incident
where the new version must be out now. Use it knowingly.

### Check

After 9a and 9c: `Status: ✔ Healthy`, `Images: enjoythings/gateway:local
(stable)`, and `curl localhost:18080/readyz` was `200` throughout.

---

## 10. Blue-green, briefly

### Goal

Replace the canary Rollout with a blue-green one and observe preview versus
active.

### Steps

Swap Rollouts without dropping the gateway:

```sh
kubectl patch rollout gateway -n enjoythings --type merge \
  -p '{"spec":{"workloadRef":{"scaleDown":"never"}}}'
kubectl scale deployment/gateway --replicas=1 -n enjoythings
kubectl rollout status deployment/gateway -n enjoythings
kubectl delete rollout gateway -n enjoythings
kubectl apply -f services/k8s/rollouts/bluegreen/
kubectl argo rollouts get rollout gateway -n enjoythings --watch
```

Once `Healthy` with the Deployment back at zero, ship a version and compare
the two doors while paused:

```sh
docker tag enjoythings/gateway:local enjoythings/gateway:green
kind load docker-image enjoythings/gateway:green --name enjoythings
kubectl argo rollouts set image gateway gateway=enjoythings/gateway:green -n enjoythings

kubectl get svc gateway-active gateway-preview -n enjoythings \
  -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.spec.selector.rollouts-pod-template-hash}{"\n"}{end}'
kubectl argo rollouts promote gateway -n enjoythings
kubectl get svc gateway-active -n enjoythings -o jsonpath='{.spec.selector.rollouts-pod-template-hash}'; echo
```

### Why

- Deleting a Rollout deletes its ReplicaSets and pods while the Deployment is
  at zero, so without the first three commands the gateway would be gone for a
  few seconds. `scaleDown: never` comes first because with `onsuccess` the
  controller would put the Deployment back to zero as soon as you scaled it up.
- Blue-green has no weights. Green starts with `previewReplicaCount: 1`, gets
  `gateway-preview` pointed at it, and waits because `autoPromotionEnabled` is
  false. `promote` scales green to `replicas`, waits for Ready, then rewrites
  `gateway-active`'s selector hash. That one write is the switch. Blue stays
  for `scaleDownDelaySeconds`, so a rollback inside that window is another
  selector write rather than a restart.
- The chart's `gateway` Service is not the active Service, so
  `localhost:18080` reaches both colours during the preview. In production the
  chart's Service would be `activeService`, which needs the chart converted,
  because the controller must own that selector. Blue-green costs two full
  copies and gives an all-or-nothing switch; stateless HTTP like the gateway
  usually suits canary better.

### Check

Before `promote`, the two Services show different hashes and the preview hash
matches the `:green` ReplicaSet. After, `gateway-active` shows the green hash
and the blue ReplicaSet drops to `0` about a minute later.

---

## 11. Hands-on practice

Return to the canary Rollout first if you did section 10: patch `scaleDown` to
`never`, scale the Deployment to 1, wait, delete the Rollout, then
`kubectl apply -f services/k8s/rollouts/`.

**Exercise 1: watch a canary step by step in the dashboard.** Start the
dashboard and `traffic.sh`, tag and load `enjoythings/gateway:v3`, `set image`
to it. Watch the step marker move from 1 to 3, the AnalysisRun count grow to
4, the pod squares shift from 3 and 1 to 2 and 2, and a Promote button appear
at the indefinite pause. Why: every state you see is a field on the Rollout
resource; the dashboard is a viewer, not a controller.

**Exercise 2: promote manually, twice.** At step 5, click Promote or run
`kubectl argo rollouts promote gateway -n enjoythings`. Observe that the
Rollout moves through `setWeight: 75`, waits 30 seconds, and finishes without
another promote. Start a `v4` rollout and run `promote --full` during the first
pause. Observe that no AnalysisRun is created and the Rollout is `Healthy`
within seconds. Why: the difference between the two runs is the whole value of
the plan.

**Exercise 3: make the analysis fail and watch the automatic rollback.**

```sh
kubectl patch rollout gateway -n enjoythings --type json \
  -p '[{"op":"replace","path":"/spec/strategy/canary/steps/2/analysis/args/0/value","value":"1.01"}]'
docker tag enjoythings/gateway:local enjoythings/gateway:v5
kind load docker-image enjoythings/gateway:v5 --name enjoythings
kubectl argo rollouts set image gateway gateway=enjoythings/gateway:v5 -n enjoythings
```

Keep `traffic.sh` running. Observe: step 1, the pause, then an AnalysisRun
whose first measurement is `Failed` because `1 >= 1.01` is false, and
`failureLimit: 1` makes one failure enough. The Rollout flips to `Degraded`,
the canary goes to 0, and `traffic.sh` never prints a non-2xx count. Restore:

```sh
kubectl patch rollout gateway -n enjoythings --type json \
  -p '[{"op":"replace","path":"/spec/strategy/canary/steps/2/analysis/args/0/value","value":"0.95"}]'
kubectl argo rollouts set image gateway gateway=enjoythings/gateway:local -n enjoythings
```

Why: this is the machinery that catches a real regression; only the threshold
moved instead of the metric. Rerun it without `traffic.sh` to see the other
failure mode: no data, two consecutive errors, abort.

**Exercise 4: ship a broken image and watch the progress deadline abort.**

```sh
kubectl argo rollouts set image gateway gateway=enjoythings/gateway:does-not-exist -n enjoythings
kubectl get pods -n enjoythings -l app.kubernetes.io/component=gateway -w
```

Observe: one new pod in `ErrImageNeverPull` or `ImagePullBackOff`, three
stable pods untouched, `ActualWeight: 0`, and after five minutes an automatic
abort with a progress deadline message. `localhost:18080` answered throughout.
Roll back with `set image` to `:local`. Why: `maxUnavailable: 0` protected the
stable pods, and `progressDeadlineAbort` turned a stuck rollout into a decided
one.

**Exercise 5: switch to blue-green and back.** Section 10 forwards, then the
reverse at the top of this section. Observe that `/readyz` answers throughout,
and name the command in each direction that prevents the gap.

---

## 12. Using this with Argo CD

If the chart is deployed by the Argo CD runbook, two things fight the Rollout.

**The Deployment's replicas.** Git says `replicas: 1`. The Rollout sets it to
0. Self-heal sets it back, and the Deployment's pod flaps. Tell Argo CD to
ignore that field and to respect the ignore during sync:

```yaml
# services/k8s/argocd/application.yaml, under spec:
  ignoreDifferences:
    - group: apps
      kind: Deployment
      name: gateway
      jsonPointers:
        - /spec/replicas
  syncPolicy:
    syncOptions:
      - CreateNamespace=true
      - RespectIgnoreDifferences=true
```

Commit and push, or for a quick test apply the same two fields with
`kubectl patch application enjoythings -n argocd --type merge` and remember
that the file is then stale.

**The image.** `set image` patches the Deployment and self-heal reverts it.
With Argo CD, change `applications.gateway.image` in `values-local.yaml`,
commit, push, refresh, as section 8c of the Argo CD runbook does for wallet.
The Rollout does not care who changed the Deployment's template. Git declares
the version, Argo CD applies it, Argo Rollouts decides how the pods get there.

**What Argo CD sees.** The Rollout and the extra Services are not part of the
Application, so Argo CD neither manages nor prunes them. They carry no
`app.kubernetes.io/instance` label on purpose, because that label is how Argo
CD tracks ownership; the Rollout's pods inherit it but are assigned to their
owner, the Rollout, so they are not orphans. To manage the Rollout with GitOps,
add `services/k8s/rollouts` as a second Application.

---

## 13. Flagger and Flux, for comparison

Flagger is the Flux project's progressive delivery controller. Not installed
here; this is so the names mean something when you meet them.

| | Argo Rollouts v1.10.0 | Flagger v1.45.0 with Flux v2.9.5 |
| --- | --- | --- |
| Unit of work | A `Rollout` that replaces or references a Deployment | A `Canary` resource that points at an untouched Deployment |
| Your Deployment | Converted, or scaled to zero via `workloadRef` | Cloned into `<name>-primary`; the original is scaled to zero |
| Traffic without a mesh | Replica-weight canary, as here | Requires a mesh or ingress controller |
| Metrics | `AnalysisTemplate`, Prometheus and other providers | `MetricTemplate`, Prometheus, Datadog and others |
| Manual gates | `pause: {}` and `promote` | Webhooks such as `confirm-rollout` and `confirm-promotion` |
| GitOps pairing | Argo CD, health checks and actions built in | Flux `Kustomization` and `HelmRelease` |
| Fits when | You run Argo CD, or want progressive delivery on a plain cluster | You run Flux and a mesh or ingress controller |

The shape is the same: a controller, a plan, a metric provider, and a rollback
when the numbers disagree. Choosing is mostly choosing between Argo CD and Flux.

---

## 14. Troubleshooting

Start with `kubectl argo rollouts get rollout gateway -n enjoythings`.

| Symptom | Meaning | What to do |
| --- | --- | --- |
| `no matches for kind "Rollout"` | CRDs not installed. | Section 3. |
| Rollout stays `Progressing` with `Available: 0`, Deployment pod still `1/1` | `spec.selector` does not match the pods the copied template creates. | Compare with `kubectl get deployment gateway -o yaml`. The chart's labels are `app.kubernetes.io/instance: enjoythings` and `app.kubernetes.io/component: gateway`. |
| Deployment flaps between 0 and 1 replicas | Argo CD self-heal versus `scaleDown: onsuccess`. | Section 12. |
| `set image` changes the Deployment but it reverts | Argo CD self-heal. | Section 12: change the tag in git. |
| `set image` accepted but nothing happens | Same tag as before, or tag not loaded into kind. | New tag, `docker tag`, `kind load`. Kubernetes compares references, not contents. |
| Canary pod `ErrImageNeverPull` / `ImagePullBackOff` | Tag not on the node. | Load it, or wait for the deadline abort and `set image` back. |
| AnalysisRun `Error`, message about no data or index out of range | Prometheus returned an empty vector: no traffic, or not scraping the gateway. | Run `traffic.sh`. Section 6 verifies the scrape. |
| AnalysisRun `Error`, message contains `dial tcp` or `no such host` | Wrong `prometheus-address`. | `kubectl get svc -n monitoring`; fix the arg default. |
| AnalysisRun `Failed` on every measurement with traffic flowing | Threshold too high, or the canary really fails. | `kubectl describe analysisrun` shows each value. Exercise 3 leaves `1.01` if you skipped its last step. |
| Rollout `Degraded`, `RolloutAborted` | An abort, manual or automatic. Stable is serving. | `set image` back to the stable tag, or `retry rollout`. |
| Rollout `Degraded`, `ProgressDeadlineExceeded` | Canary never became Ready in time. | `describe` and `logs` on the canary pod, fix, `retry`. |
| Gateway unreachable for seconds during a swap | Rollout deleted while the Deployment was at zero. | Patch `scaleDown` to `never`, scale up, wait, then delete. |

---

## 15. Cleanup

Hand the pods back to the Deployment before removing the Rollout:

```sh
kubectl patch rollout gateway -n enjoythings --type merge \
  -p '{"spec":{"workloadRef":{"scaleDown":"never"}}}'
kubectl scale deployment/gateway --replicas=1 -n enjoythings
kubectl rollout status deployment/gateway -n enjoythings
kubectl delete -f services/k8s/rollouts/            # or -f services/k8s/rollouts/bluegreen/
kubectl delete analysisrun --all -n enjoythings

# then the controller, matching how you installed it
kubectl delete -n argo-rollouts -f https://github.com/argoproj/argo-rollouts/releases/download/v1.10.0/install.yaml
kubectl delete namespace argo-rollouts
# or: helm uninstall argo-rollouts -n argo-rollouts
# or: cd infra/terraform/25-rollouts && terraform destroy
```

Set the Deployment's image back to `:local` if an exercise left it elsewhere.
The chart and everything from the Kubernetes runbook are untouched, which was
the point of `workloadRef`.

---

## 16. Toward production

1. **Add a traffic router.** Replica-weight canary needs many pods for fine
   steps and cannot do 1 percent. With an ingress controller or a mesh,
   `strategy.canary.trafficRouting` sets exact weights, header-based routing
   for internal testers, and mirroring. `docs/aws-deployment.md` puts the
   gateway behind an ALB, which has an integration.

2. **Make the analysis per canary.** The query here measures all gateway pods
   together. With a scrape that attaches the `pod` label, pass
   `valueFrom: {podTemplateHashValue: Latest}` from the Rollout and filter with
   `pod=~"gateway-{{args.canary-hash}}-.*"`, so stable cannot hide the canary.

3. **Measure latency too**, from `service_http_request_duration_seconds`, and
   run the analysis in the background with `strategy.canary.analysis` so every
   step is covered.

4. **Convert the chart once the pattern is proven.** Replace the gateway
   Deployment with a Rollout behind a values switch, make the chart's `gateway`
   Service the `stableService` or `activeService`, and drop `workloadRef`.

5. **Put the Rollout manifests in git for Argo CD**, turn on the controller's
   notifications for `on-rollout-aborted`, size `progressDeadlineSeconds` to
   the slowest healthy start you have measured, and raise `controller_replicas`
   in stage 25 to the chart's default of two.
