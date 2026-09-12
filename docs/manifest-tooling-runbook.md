# Manifest and Chart Tooling Runbook

This runbook is about the YAML itself. It teaches the tools that sit between
a chart and a cluster: Kustomize for per-environment variations, Helmfile and
the Argo CD "app of apps" pattern for managing many charts as one unit, and
kubeconform, kube-linter and conftest for catching mistakes in CI before any
cluster sees them.

Same format as the other runbooks. Every section states a goal, the commands,
why each step exists, and how to check it worked. Read
[`k8s-deployment-runbook.md`](./k8s-deployment-runbook.md) and
[`argocd-runbook.md`](./argocd-runbook.md) first. Sections 3 to 4 and 8 to 12
need no cluster at all. Sections 5 and 7 need the kind cluster `enjoythings`
with Argo CD installed and the CLI logged in, exactly as the Argo CD runbook
leaves it.

## Before you start

Two things must be true:

- **The files this runbook adds are pushed to `master`.** Argo CD reads git,
  not your disk, so sections 5 and 7 need `services/k8s/kustomize` and
  `services/k8s/argocd/apps` on GitHub.
- **You have memory for a second copy of the stack, or you remove the first.**
  Section 5 deploys the chart again into `enjoythings-dev`. Two copies need
  roughly 6 GB in Docker Desktop. If that is too much, delete the master
  Application first:

```sh
kubectl delete -f services/k8s/argocd/application.yaml
```

## What you will end up with

```
services/k8s/
├── kustomize/
│   ├── base/                      the Argo CD Application, environment-free
│   │   ├── kustomization.yaml
│   │   └── application.yaml
│   └── overlays/
│       ├── dev/                   HEAD, namespace enjoythings-dev, appEnv=dev
│       └── staging/               release-staging branch, enjoythings-staging, 2 wallets
├── argocd/apps/
│   ├── root.yaml                  the app of apps
│   └── children/
│       ├── enjoythings.yaml       the chart on master (mirror of ../application.yaml)
│       ├── enjoythings-dev.yaml   the dev Kustomize overlay, manual sync
│       └── monitoring.yaml        kube-prometheus-stack 90.0.0, manual sync
└── policy/
    ├── *.rego                     conftest rules: image tags, resources, probes, non-root, Applications
    ├── policy_test.rego           unit tests for the rules
    └── kube-linter.yaml           which kube-linter checks run, and why some do not
.github/workflows/manifests.yml    renders and validates all of the above on every push
```

---

## 1. Vocabulary you need

| Term | What it is |
| --- | --- |
| **Base** | A directory of plain manifests plus a `kustomization.yaml`. Contains nothing that only one environment wants. |
| **Overlay** | A directory whose `kustomization.yaml` points at a base and adds patches, a name suffix, labels. One per environment. |
| **Strategic merge patch** | A partial manifest. Kustomize merges its fields over the matching resource. Good for scalars such as a branch or a namespace. |
| **JSON 6902 patch** | A list of operations such as `add`, `replace`, `remove` at a path. Needed to append to a list without replacing it. |
| **Load restrictor** | Kustomize's rule that a kustomization may only read files in or below its own directory. |
| **Helm parameter** | Argo CD's name for a `--set` value. Stored in `spec.source.helm.parameters` on an Application. |
| **App of apps** | An Argo CD Application whose manifests are other Applications. Syncing the root registers every child. |
| **Directory source** | An Application source with no chart and no kustomization: Argo CD applies every YAML file in the path. |
| **Schema validation / lint** | Checking that every field exists and has the right type, versus checking for bad patterns that are still valid YAML. kubeconform does the first, kube-linter the second. |
| **Policy** | A rule your team writes. conftest evaluates policies written in Rego. |
| **Rego** | The query language of Open Policy Agent. A policy is a set of rules that produce messages. |
| **deny / warn** | Rego rule names conftest looks for. `deny` fails the run; `warn` prints and exits 0. |
| **Exception** | A named object a policy deliberately skips. Lives in git, so it is reviewed like code. |

---

## 2. Install and verify the tools

### Goal

Every CLI the later sections use responds with a version.

### Steps

```sh
brew install kustomize kubeconform kube-linter conftest helmfile actionlint
kustomize version
kubeconform -v
kube-linter version
conftest --version
helmfile version
actionlint -version
```

### Why

- **kustomize** builds overlays. `kubectl` has an older copy built in as
  `kubectl apply -k`; the standalone binary is what CI runs.
- **kubeconform** validates manifests against the Kubernetes API schemas
  without a cluster. **kube-linter** is a static analyzer with about fifty
  built-in checks. **conftest** runs Rego policies against YAML.
- **helmfile** declares a set of Helm releases in one file, section 6 only.
  **actionlint** checks GitHub Actions workflow files, section 11 only.

### Check

The versions print as `v5.8.1`, `v0.8.0`, `0.8.3`, OPA `1.19.0`, `v1.7.4`
and `1.7.12`. Newer is fine.

---

## 3. Read the Kustomize base

### Goal

Understand what a base is and why this one is a copy of a file that already
exists.

### Steps

```sh
cat services/k8s/kustomize/base/kustomization.yaml
diff services/k8s/argocd/application.yaml services/k8s/kustomize/base/application.yaml
```

### Why

- **What was chosen as the base, and why.** The base is the Argo CD
  Application manifest, not the chart's Deployments. The chart already
  varies per environment through values files, so wrapping it in Kustomize
  would duplicate Helm's job. What has no variation mechanism yet is the
  Application that points Argo CD at the chart. Each environment needs one
  with a different branch, namespace and Helm parameters, which is exactly a
  base plus overlays. It is also one resource, so every patch is easy to read.
- **Why it is a copy.** The `diff` shows only comment lines differ. Kustomize
  refuses `resources: ../../argocd/application.yaml`, because its load
  restrictor forbids reaching outside the kustomization's directory. The
  restriction exists so a base can be moved or vendored without breaking. The
  workflow in section 11 fails if the copy drifts from the original, which is
  how a team keeps two files honest.
- **Why Kustomize exists beside Helm.** Helm templates a chart you own.
  Kustomize patches YAML you do not own or do not want to template: an
  upstream operator's manifests, the output of `helm template`, or as here an
  Argo CD Application. Teams use both: Helm for packaging, Kustomize for the
  last-mile edits per environment.

### Check

`diff` prints only lines that start with `#`, plus the line numbers around
them.

---

## 4. Build the overlays and read the diff

### Goal

See how each overlay changes the base, and prove that the same base yields two
different Applications.

### Steps

```sh
cd services/k8s/kustomize
cat overlays/dev/kustomization.yaml overlays/dev/patch-source.yaml overlays/dev/patch-helm-parameters.yaml
kustomize build overlays/dev
kustomize build overlays/staging
diff <(kustomize build overlays/dev) <(kustomize build overlays/staging)
```

### Why

Reading `overlays/dev/kustomization.yaml` top to bottom:

| Field | Effect | Why |
| --- | --- | --- |
| `resources: [../../base]` | Start from the base. | An overlay never repeats the base's content. |
| `nameSuffix: -dev` | `enjoythings` becomes `enjoythings-dev`. | Both Applications live in the same `argocd` namespace, so their names must differ. |
| `labels` with `includeSelectors: false` | Adds `enjoythings.io/environment: dev` to the Application. | Lets `argocd app list -l enjoythings.io/environment=dev` find it. `includeSelectors: false` keeps the label off selectors, which matters when the resource is a Deployment. |
| `patches: - path: patch-source.yaml` | Strategic merge: sets `targetRevision` and `destination.namespace`. | Two scalar fields; a partial manifest is the shortest way to say it. |
| `patches: - path: patch-helm-parameters.yaml` with `target` | JSON 6902: appends entries to `spec.source.helm.parameters`. | Kustomize has no schema for a custom resource, so a merge patch would replace the whole list and drop `secrets.create=false`. `add` at path `/-` appends instead. |

Two details that surprise people. The merge patch names `namespace: argocd`
because Kustomize matches a patch by group, kind, name and namespace; leave it
out and the error is `no resource matches strategic merge patch`. And the
patches name `enjoythings`, not `enjoythings-dev`: patches match the original
name, and the suffix is applied afterwards.

### Check

Both builds print one Application. The `diff` shows exactly these
differences: the label value, the name, the destination namespace, the
`global.appEnv` value, the `nodePort` value, one extra parameter
`applications.wallet.replicas=2` in staging, and `targetRevision` `HEAD`
versus `release-staging`.

---

## 5. Give the dev overlay to Argo CD

### Goal

The dev environment running in namespace `enjoythings-dev`, first created
with `kubectl` to see the mechanism, then handed to Argo CD in section 7.

### Steps

Create the namespace and the Secret the chart expects. Run the
`kubectl create secret` block from section 5 of the Argo CD runbook with
`-n enjoythings-dev` instead of `-n enjoythings`:

```sh
kubectl create namespace enjoythings-dev
# ... the kubectl create secret generic enjoythings-secret command, with -n enjoythings-dev
```

Apply the overlay with kubectl's built-in Kustomize, then watch Argo CD pick
it up:

```sh
kubectl apply -k services/k8s/kustomize/overlays/dev
argocd app get enjoythings-dev --refresh
argocd app wait enjoythings-dev --health --timeout 600
kubectl get pods -n enjoythings-dev
kubectl get configmap enjoythings-config -n enjoythings-dev -o jsonpath='{.data.APP_ENV}'; echo
```

Reach the dev gateway. Its NodePort is `30081`, which kind does not map to
your laptop, so use a port-forward:

```sh
kubectl port-forward svc/gateway -n enjoythings-dev 18081:8080
curl -i http://localhost:18081/healthz
```

### Why

- `kubectl apply -k` runs Kustomize and applies the output in one step.
  The output is an Application, so what you created is a request to Argo CD,
  not workloads. Argo CD then renders the chart with `HEAD` resolved to
  `master`, the parameters from the overlay, and deploys into
  `enjoythings-dev`.
- `secrets.create=false` is inherited from the base, so the Secret has to
  exist in the new namespace before pods start. Without it pods show
  `CreateContainerConfigError`.
- `applications.gateway.nodePort=30081` avoids `provided port is already
  allocated`: the master gateway holds `30080` and a NodePort is cluster-wide.
- `global.appEnv=dev` ends up in the ConfigMap as `APP_ENV`, which the
  services read to pick trace sampling.

### Check

`argocd app get enjoythings-dev` reports `Synced` and `Healthy`. The
ConfigMap prints `dev`. `curl` returns `200`. The master environment at
`http://localhost:18080/healthz`, if you kept it, still answers.

---

## 6. Helmfile in brief

### Goal

Know what Helmfile is, run it once against the chart, and understand when to
prefer it over Argo CD.

### Steps

Helmfile is one YAML file listing releases. Create one in a scratch directory
so nothing in the repository changes. Run this from the repository root:

```sh
REPO=$(pwd)
mkdir -p /tmp/helmfile-demo
cat > /tmp/helmfile-demo/helmfile.yaml <<EOF
repositories:
  - name: prometheus-community
    url: https://prometheus-community.github.io/helm-charts

releases:
  - name: enjoythings
    namespace: enjoythings
    chart: $REPO/services/charts/enjoythings
    values:
      - $REPO/services/charts/enjoythings/values-local.yaml
    set:
      - name: secrets.create
        value: "false"
  - name: monitoring
    namespace: monitoring
    chart: prometheus-community/kube-prometheus-stack
    version: 90.0.0
    installed: false
EOF
cd /tmp/helmfile-demo
helmfile list
helmfile template --selector name=enjoythings | grep -c '^kind:'
```

Do not run `helmfile apply` here: Argo CD owns this cluster, and a release
installed from your laptop would fight with it.

### Why

- Helmfile answers "install these charts with these values, in this order"
  from a laptop or a CI job. `helmfile apply` diffs and upgrades every
  release; `installed: false` means "uninstall if present".
- It is the right tool when there is no GitOps controller: bootstrapping a
  cluster before Argo CD exists, ephemeral CI clusters, or teams that run Helm
  from a pipeline on purpose. Once Argo CD is in the cluster, the same list of
  charts is better expressed as Applications, which is section 7.
  `helmfile apply` needs the `helm-diff` plugin; `template` and `list` do not.

### Check

`helmfile list` shows two releases, `monitoring` with `INSTALLED false`.
The template count prints `26`, the same as `helm template` in the Kubernetes
runbook without the Secret.

---

## 7. App of apps

### Goal

One root Application that owns three child Applications, so the whole
environment is registered with one `kubectl apply` and torn down with one
`kubectl delete`.

### Steps

Remove the Application you created by hand in section 5, so the root can own
it instead:

```sh
kubectl delete application enjoythings-dev -n argocd
```

Read the root and the children, then apply the root only:

```sh
cat services/k8s/argocd/apps/root.yaml
ls services/k8s/argocd/apps/children
kubectl apply -f services/k8s/argocd/apps/root.yaml
argocd app get enjoythings-root --refresh
argocd app list
```

Sync the dev child by hand and watch the three levels appear:

```sh
argocd app sync enjoythings-dev-overlay
argocd app wait enjoythings-dev --health --timeout 600
argocd app list
```

Optionally `argocd app sync monitoring`, which pulls about 1 GB of images,
then `kubectl port-forward svc/monitoring-grafana -n monitoring 3000:80` and
open <http://localhost:3000> as `admin` / `admin`.

### Why

- The root's source is a **directory**: no `Chart.yaml`, no
  `kustomization.yaml`, so Argo CD applies each file in `children/` as is,
  into the `argocd` namespace where Applications must live.
- **The root decides which Applications exist. Each child decides when it
  deploys.** `enjoythings.yaml` keeps automated sync. `enjoythings-dev.yaml`
  and `monitoring.yaml` have no `syncPolicy`, so they wait for
  `argocd app sync`. One tree can therefore hold a production app that must
  always match git and an expensive optional add-on.
- `children/enjoythings.yaml` has the same name as the Application from the
  Argo CD runbook. Applying it through the root **adopts** the existing one:
  Argo CD adds its tracking label and from then on the root owns it. If
  Terraform stage 30 also manages it, two owners fight over one object; pick
  one per cluster.
- The dev child is a three-level tree: root creates `enjoythings-dev-overlay`,
  which runs `kustomize build` and creates `enjoythings-dev`, which renders the
  chart and creates the workloads. Argo CD detects Kustomize by the presence
  of `kustomization.yaml`, as it detects Helm by `Chart.yaml`.
- `monitoring.yaml` shows a chart from a Helm repository: `chart` replaces
  `path` and `targetRevision` is the chart version. `ServerSideApply=true` is
  needed because the Prometheus operator's CRDs exceed the annotation size
  limit client-side apply relies on.
- With `prune: true` on the root, deleting a file from `children/` and
  pushing deletes that child, and the child's finalizer deletes its
  workloads. The tree in git is the tree in the cluster.

### Check

`argocd app list` shows five rows: `enjoythings-root`, `enjoythings`,
`enjoythings-dev-overlay` and `enjoythings-dev` Synced and Healthy, and
`monitoring` OutOfSync until you sync it. In the UI, the root's resource tree
shows the children as `Application` nodes you can click through.

---

## 8. kubeconform: is this valid Kubernetes YAML

### Goal

Validate everything the repository renders against the API schemas of the
Kubernetes version the cluster runs, with no cluster involved.

### Steps

```sh
cd services
mkdir -p /tmp/rendered
helm template enjoythings charts/enjoythings -f charts/enjoythings/values-local.yaml \
  --set secrets.create=false > /tmp/rendered/chart.yaml
kubeconform -strict -summary -kubernetes-version 1.37.0 /tmp/rendered/chart.yaml

CRD='https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json'
kustomize build k8s/kustomize/overlays/dev | kubeconform -strict -summary \
  -schema-location default -schema-location "$CRD" -
```

Break it on purpose:

```sh
sed 's/^  replicas: 1$/  replicas: one/' /tmp/rendered/chart.yaml | kubeconform -strict -summary -kubernetes-version 1.37.0 -
```

### Why

- `helm template` renders without a cluster, so this whole section runs in
  CI. `--set secrets.create=false` renders what Argo CD renders and keeps
  credentials out of the output.
- `-strict` rejects fields the schema does not know. Without it a typo such
  as `replicaz` passes silently and the cluster ignores the field.
- `-kubernetes-version` matters because fields appear and disappear between
  releases. kind `v0.33.0` runs `1.37.0` by default; use what `kubectl
  version` prints for your cluster.
- kubeconform's built-in schemas cover core kinds only. An Argo CD
  Application is a custom resource, so without the second `-schema-location`
  it reports `could not find schema`. The CRDs catalog is a community
  collection of CRD schemas converted to JSON.

### Check

The chart summary reads `26 resources found in 1 file - Valid: 26, Invalid:
0`. The overlay reads `1 resource found parsing stdin - Valid: 1`. The broken
run lists twelve `Deployment ... is invalid` lines ending in `got string, want
null or integer` and exits `1`.

---

## 9. kube-linter: is this good Kubernetes YAML

### Goal

Run the built-in best-practice checks and understand every one the repository
turns off.

### Steps

```sh
kube-linter lint /tmp/rendered/chart.yaml | tail -1
kube-linter lint --config services/k8s/policy/kube-linter.yaml /tmp/rendered/chart.yaml
cat services/k8s/policy/kube-linter.yaml
```

### Why

- Without a config, kube-linter reports 33 findings on this chart, all of
  them real: no container runs as non-root, none has a read-only root
  filesystem, three declare no resources, and the hook Job sets no TTL. None
  of that is invalid YAML, which is why a linter is a separate step from
  kubeconform.
- The config excludes five checks and says why next to each. Two wait for
  the chart to set a `securityContext`. Two are handed to conftest in section
  10, where the three offending containers are named as exceptions instead of
  the whole check being silenced. One is redundant because Helm's hook
  deletion policy already removes the finished Job.
- An exclusion with a written reason is a to-do item. An exclusion without one
  is a silenced alarm.

### Check

The first command prints `Error: found 33 lint errors`. The second prints
`No lint errors found!`.

---

## 10. conftest and Rego: is this our Kubernetes YAML

### Goal

Read the five policies, run their unit tests, and run them against the chart
and the Applications.

### Steps

```sh
ls services/k8s/policy
cat services/k8s/policy/lib.rego services/k8s/policy/image_tags.rego
conftest verify -p services/k8s/policy
conftest test -p services/k8s/policy /tmp/rendered/chart.yaml
kustomize build services/k8s/kustomize/overlays/staging | conftest test -p services/k8s/policy -
conftest test -p services/k8s/policy services/k8s/argocd/apps/root.yaml services/k8s/argocd/apps/children
```

### Why

Rego in four sentences. A policy file declares `package main`, which is the
package conftest reads. `input` is the manifest being checked, one document
at a time. A rule `deny contains msg if { ... }` adds `msg` to the set of
failures whenever every line in the body is true. `warn` works the same but
does not fail the run.

| File | Rule | Level | Why this rule |
| --- | --- | --- | --- |
| `image_tags.rego` | Image has a tag or digest, and the tag is not `latest`. | deny | Argo CD cannot see a change behind an unchanged tag, and `latest` makes rollback meaningless. |
| `resources.rego` | Every container sets cpu and memory requests and limits. | deny, with three named exceptions | Requests drive scheduling, limits contain leaks. `kafka`, `fraud-timescaledb` and `kafka-topic-init` are listed because the chart does not set theirs yet. |
| `probes.rego` | Every Deployment container has a readiness probe. | deny | `maxUnavailable: 0` only means zero downtime when readiness is honest. |
| `run_as_non_root.rego` | Pod or container sets `runAsNonRoot: true`. | warn | The chart sets none, so a deny would fail all 13 containers. The warning keeps the gap visible on every run. |
| `argocd_application.rego` | Applications carry the resources finalizer, a project, and do not deploy into `default`. | deny | Without the finalizer, deleting an Application orphans its workloads. |

- `lib.rego` holds the shared helpers: which kinds have a pod template, and a
  `containers` set that includes init containers.
- `policy_test.rego` is why you can change a policy with confidence. Each test
  builds a tiny manifest, evaluates `deny` or `warn` with `with input as`, and
  counts the results. `conftest verify` runs them. A policy without tests
  drifts into allowing everything or blocking everything.
- The exception list in `resources.rego` is the pattern to copy whenever a
  rule is right but the code is not ready: enforce for everything else, name
  the gaps, review any addition.

### Check

`conftest verify` prints `14 tests, 14 passed`. The chart run prints
`208 tests, 195 passed, 13 warnings, 0 failures` and exits `0`; the 13
warnings are the `runAsNonRoot` gap. The staging overlay prints `8 tests, 8
passed`. The app-of-apps run prints `32 tests, 32 passed`.

---

## 11. Run everything in CI

### Goal

Understand `.github/workflows/manifests.yml` well enough to extend it, and
lint it locally.

### Steps

```sh
cat .github/workflows/manifests.yml
actionlint .github/workflows/manifests.yml
```

Push a branch and open a pull request to see it run, or look at the Actions
tab after the next push to `master`.

### Why

The workflow does, in order, what sections 8 to 10 did by hand:

| Step | What it does | Why it is there |
| --- | --- | --- |
| `env:` block | Pins every tool version and the Kubernetes version. | One place to bump. The `KUBERNETES_VERSION` must track the cluster. |
| Install tools | Downloads pinned release tarballs into `$RUNNER_TEMP/bin`. | Preinstalled runner tools change without notice; pinned downloads do not. |
| `helm lint`, `helm template` | Renders `rendered/chart.yaml` with `secrets.create=false`. | Same output Argo CD produces, no credentials in it. |
| `kustomize build` twice | Renders both overlays. | An overlay that fails to build fails the job before Argo CD ever sees it. |
| Copy check | `diff` of non-comment lines between `application.yaml` and its two copies. | The copies exist because of load restrictions; this step makes drift a failure instead of a surprise. |
| kubeconform | Chart, both overlays, the root and every child, with the CRD catalog. | Schema errors. |
| kube-linter | Chart only, with `kube-linter.yaml`. | Best practices. Applications are not workloads, so kube-linter has nothing to say about them. |
| `conftest verify` then `conftest test` | Tests first, then the same file set as kubeconform. | A broken policy should fail on its own tests, not by passing everything. |
| Upload artifact | Keeps `rendered/` for seven days, even on failure. | Lets a reviewer read the rendered YAML without rendering locally. |

### Check

`actionlint` prints nothing and exits `0`. On GitHub the job `Render and
validate` is green and the run has an artifact named `rendered-manifests`.

---

## 12. Practice

### Goal

Break each tool on purpose and read what it says. Every exercise states the
task, what you will see, and why.

### Steps and why

**12a. Ship a `latest` tag.** Render with one image changed and run conftest.

```sh
cd services
helm template enjoythings charts/enjoythings -f charts/enjoythings/values-local.yaml \
  --set secrets.create=false --set applications.wallet.image=enjoythings/wallet:latest \
  | conftest test -p k8s/policy -
echo "exit $?"
```

You see one `FAIL` line naming `Deployment/wallet`, then `1 failure`, and exit
`1`. The `image_tags.rego` rule split the reference on `/`, took the last
segment, found the tag `latest`, and added a message to `deny`. A `deny`
message is a non-zero exit, which is what fails the CI job. The same command
without the `--set` exits `0`.

**12b. Scale dev with a patch.** Add one operation to the dev overlay and
build.

```sh
cd services/k8s/kustomize
cat >> overlays/dev/patch-helm-parameters.yaml <<'EOF'
- op: add
  path: /spec/source/helm/parameters/-
  value:
    name: applications.wallet.replicas
    value: "2"
EOF
kustomize build overlays/dev | grep -A1 wallet.replicas
```

You see the new parameter at the end of the list. Commit, push, and
`argocd app sync enjoythings-dev-overlay`; within a minute
`kubectl get deployment wallet -n enjoythings-dev` shows `2/2`. The change
travelled overlay to Application to `helm template` to Deployment with no
edit to the chart. Undo with `git checkout` and push again.

**12c. Make a typo and let kubeconform catch it.** Misspell a field in the
overlay output and validate with `-strict`.

```sh
CRD='https://raw.githubusercontent.com/datreeio/CRDs-catalog/main/{{.Group}}/{{.ResourceKind}}_{{.ResourceAPIVersion}}.json'
kustomize build overlays/dev | sed 's/targetRevision/targetRevison/' \
  | kubeconform -strict -schema-location default -schema-location "$CRD" -
echo "exit $?"
```

You see `additional properties 'targetRevison' not allowed` and exit `1`.
Without `-strict`, or without the CRD schema, the same typo passes, and Argo
CD would deploy `master` because the unknown field is ignored and the base's
value stays.

**12d. Remove a policy exception.** Delete the `kafka` line from
`resource_exceptions` in `services/k8s/policy/resources.rego` and rerun.

```sh
conftest test -p services/k8s/policy /tmp/rendered/chart.yaml | grep -E 'FAIL|tests'
```

You see `Deployment/kafka: container "kafka" must set cpu and memory under
resources.limits and resources.requests` and `1 failure`. The exception was
the only thing standing between the rule and the chart. The right fix is in
the chart, not the policy: define `kafka.resources` in `values.yaml`. Restore
the line with `git checkout services/k8s/policy/resources.rego` for now.

**12e. Promote warnings to failures.** Do not edit the policy; ask conftest
to treat warnings as failures.

```sh
conftest test --fail-on-warn -p services/k8s/policy /tmp/rendered/chart.yaml
echo "exit $?"
```

You see the same 13 warnings and exit `1`. (Piping the first command through
`tail -1` looks tidier but hides the bug: `$?` after a pipeline reports the
*last* command's exit status, so `| tail -1` would print `exit 0` no matter
what conftest did. Capture the exit status before you trim the output, or
don't pipe at all.) This is the flag to add to the workflow on the day the
chart sets `runAsNonRoot`. Renaming the rule from `warn` to `deny` instead
would break `policy_test.rego`, which asserts on `warn`.

**12f. Prune a child.** Remove `monitoring.yaml` from `children/`, commit,
push, and refresh the root.

```sh
git rm services/k8s/argocd/apps/children/monitoring.yaml
git commit -m "chore(argocd): drop monitoring child"
git push
argocd app get enjoythings-root --refresh
argocd app list
```

The `monitoring` Application disappears, and if you had synced it, its
workloads go too. The root's `prune: true` deleted a resource that left git;
the child's finalizer did the rest. Revert the commit and push to bring it back.

---

## 13. Troubleshooting

| Symptom | Meaning | What to do |
| --- | --- | --- |
| `kustomize build`: `no resource matches strategic merge patch ... [noNs]` | The patch's `metadata` lacks the namespace the base resource has. | Add `namespace: argocd` under `metadata` in the patch. |
| `kustomize build`: `security; file ... is not in or below` | A `resources:` entry points outside the kustomization directory. | Copy the file into the base, or move the kustomization. Do not use `--load-restrictor LoadRestrictionsNone`; Argo CD runs with the default. |
| Argo CD: `enjoythings-staging` shows `ComparisonError` mentioning `release-staging` | The staging overlay tracks a branch that does not exist yet. | `git branch release-staging && git push origin release-staging`, then refresh. That is what a release branch is for. |
| Pods in `enjoythings-dev` show `CreateContainerConfigError` | `enjoythings-secret` is missing in that namespace. | Section 5 first command block, with `-n enjoythings-dev`. |
| Gateway Service in dev: `provided port is already allocated` | Two environments asked for the same NodePort. | Keep `applications.gateway.nodePort` different per overlay; `30081` and `30082` are used here. |
| `argocd app list` shows `enjoythings` flipping between two owners | Both the app-of-apps root and Terraform stage 30 manage the same Application. | Choose one. Either remove `children/enjoythings.yaml` or stop applying stage 30. |
| kubeconform: `could not find schema for Application` | Only the built-in Kubernetes schemas were given. | Add the second `-schema-location` with the CRDs catalog URL. |
| kubeconform: `no schema found for version 1.xx.x` | That Kubernetes version has no published schema yet. | Use the newest version that exists in `yannh/kubernetes-json-schema`, or drop `-kubernetes-version` to use `master`. |
| conftest: `rego_parse_error: ... expected 'if' keyword` | Rego v0 syntax in a v1 world. | Write `deny contains msg if {`. OPA 1.0 made `if` and `contains` mandatory. |
| conftest: `rego_unsafe_var_error: var warn is unsafe` | A test refers to `warn` but no `warn` rule exists any more. | You renamed the last `warn` rule to `deny`. Use `--fail-on-warn` instead, or update the tests with the policy. |
| conftest exits `0` although it printed `WARN` lines | Expected. Warnings never fail the run. | Add `--fail-on-warn` when you want them to. |
| Workflow step `Check that copies of application.yaml match` fails | `application.yaml` changed and a copy did not. | Apply the same non-comment change to `kustomize/base/application.yaml` and `argocd/apps/children/enjoythings.yaml`. |
| `helm template` prints `failed to load plugin ... helm-secrets` | A stale Helm plugin on your laptop. | Harmless. `helm plugin uninstall secrets` removes the noise. |

---

## 14. Cleanup

```sh
kubectl delete -f services/k8s/argocd/apps/root.yaml   # root, then children, then their workloads via finalizers
kubectl delete namespace enjoythings-dev               # removes the hand-made Secret too
kubectl delete namespace monitoring                    # only if you synced the monitoring child
rm -rf /tmp/helmfile-demo /tmp/rendered
```

If the root was never applied but section 5 was, delete
`application enjoythings-dev -n argocd` and the namespace instead.

Deleting the root first matters, for the same reason as in the Argo CD
runbook: delete the namespace while the Applications exist and Argo CD
recreates everything.

---

## 15. Toward production

1. **Generate the environment Applications instead of writing them.** Three
   overlays are fine; thirty are not. An Argo CD `ApplicationSet` with a
   list or git directory generator produces one Application per overlay
   directory, and the app of apps shrinks to one root plus one
   ApplicationSet.

2. **One `AppProject` per environment.** Restrict which repos, namespaces and
   kinds `enjoythings-staging` may touch, and give the staging team sync
   rights on it alone. The `default` project used here allows everything.

3. **Pin the schema sources.** `CRD_SCHEMAS` points at the `main` branch of the
   catalog. Pin a commit, or generate schemas from your own cluster's CRDs
   with `kubectl get crd -o yaml` and `openapi2jsonschema`, so a catalog
   change cannot break your build.

4. **Enforce the same policies at admission.** conftest checks git; nothing
   stops `kubectl apply` from a laptop. Gatekeeper runs Rego as an admission
   webhook and Kyverno does the same with YAML rules, so the cluster rejects
   what CI would have rejected.

5. **Retire the exceptions.** Set `resources` for `kafka`, `fraud-timescaledb`
   and `kafka-topic-init` in the chart, delete the three names from
   `resources.rego`, set `runAsNonRoot` on the pod templates, add
   `--fail-on-warn` to the workflow, and re-enable the two kube-linter
   checks. Each step is one commit with a red-then-green CI run.

6. **Bump versions on a schedule.** Every version in this runbook, the
   workflow `env:` block and `monitoring.yaml` will be stale in a few months.
   Renovate understands GitHub Actions, Helm chart and tool versions.
