# Provisioning Tooling Runbook

This runbook does not build anything new. It takes the three Terraform stages
from [`terraform-runbook.md`](./terraform-runbook.md) and wraps them with
tooling that real infrastructure teams put on top of plain Terraform once more
than one person touches the code: Terragrunt for orchestration and DRY
configuration, a CI workflow that plans on every pull request, and Crossplane
as a different model entirely — a Kubernetes control plane that reconciles
infrastructure instead of a CLI that applies a plan.

Read the Terraform runbook first. Everything here assumes you can already
explain what a stage, a provider, a plan and a state file are. This runbook
answers a different question: once the stages exist, what do you put around
them so that ten engineers can change them safely, and what does an
alternative to Terraform's plan/apply model look like.

## What you will end up with

```
infra/terragrunt/
├── root.hcl              shared remote_state + terraform{} block, included by every stage
├── 10-cluster/           wraps infra/terraform/10-cluster, depends on nothing
├── 20-platform/          wraps infra/terraform/20-platform, dependency "cluster"
└── 30-apps/              wraps infra/terraform/30-apps, dependency "cluster" + "platform"

.github/workflows/terraform-plan.yml   fmt + validate for all 3 stages, plan for stage 10, on every PR

infra/crossplane/
├── 10-provider-kubernetes.yaml   DeploymentRuntimeConfig, RBAC, the Provider, a Function
├── 20-providerconfig.yaml       how the provider authenticates
├── 30-xrd.yaml                  the API: XAppNamespace (composite) / AppNamespace (claim)
├── 40-composition.yaml          how a claim becomes a Namespace + ResourceQuota + NetworkPolicy
└── 50-claim.yaml                what a team writes
```

Terragrunt does not replace the Terraform stages; it drives them. Crossplane
replaces the idea of "run a CLI to apply a plan," for one narrow job — a
self-service application namespace — with "declare the desired state as a
Kubernetes object and let a controller loop keep reality matching it."

**This runbook does not apply anything.** Terragrunt commands here are
`hclfmt`, `validate` and reading the dependency graph — the same
no-cluster-required boundary the Terraform runbook's CI section draws.
Crossplane sections are a walkthrough of the manifests already on disk: what
each means and how it flows to the next, not a live install. Actually
running Crossplane against the kind cluster is a follow-on exercise outside
this runbook, after you have read every manifest below.

---

## 1. Vocabulary you need

| Term | What it is | Where it shows up here |
| --- | --- | --- |
| **Unit** | Terragrunt's word for one `terragrunt.hcl` directory — the analogue of a Terraform root module. | `10-cluster`, `20-platform`, `30-apps`. |
| **`include`** | Pulls a parent config's blocks into a unit, merged in. | Every stage's `include "root" { path = find_in_parent_folders("root.hcl") }`. |
| **`dependency`** | Declares that one unit must run after another, and exposes that unit's outputs. | `dependency "cluster"` in 20-platform and 30-apps. |
| **`mock_outputs`** | Placeholder values for a dependency's outputs, used when the real ones do not exist yet. | Lets `validate` and `plan` run on a fresh checkout, never used by `apply`. |
| **`generate`** | Tells Terragrunt to write a file into the copied source before running Terraform. | `root.hcl` generates `backend.tf`; the stages generate `terragrunt_override.tf`. |
| **`run --all`** | Runs a command across every unit in dependency order (was `run-all` before Terragrunt's 1.0 CLI redesign; that name still appears in a lot of blog posts and older docs). | `terragrunt run --all validate`. |
| **`.terragrunt-cache/`** | Where Terragrunt copies a unit's source and generates files before running Terraform there. A build artifact, never hand-edited, git-ignored. | One under `infra/terragrunt/` and one inside each stage directory. |
| **XRD** (CompositeResourceDefinition) | Crossplane's schema object: defines a new Kubernetes API (a composite resource, and optionally a claim). | `30-xrd.yaml`, defines `XAppNamespace` / `AppNamespace`. |
| **Composite resource (XR)** | The cluster-scoped object created from an XRD; what the platform team operates. | `XAppNamespace`, created from a claim. |
| **Claim (XRC)** | The namespaced object a team creates; the self-service surface. | `AppNamespace`, in `50-claim.yaml`. |
| **Composition** | The recipe: how an XR's fields become one or more managed resources. | `40-composition.yaml`. |
| **Managed resource (MR)** | One real thing a Crossplane provider manages, one-to-one with something outside Crossplane. | The three `Object` resources the Composition creates. |
| **Provider (Crossplane)** | A controller plus CRDs that let Crossplane manage one API's resources. Unrelated to a Terraform provider except in name. | `provider-kubernetes`. |
| **Function** | A pipeline step a Composition calls to build resources. Crossplane v2 Compositions are pipelines of function calls, not templates. | `function-patch-and-transform`. |
| **ProviderConfig** | Where a Crossplane provider gets its credentials. | `20-providerconfig.yaml`, `InjectedIdentity`. |
| **Reconciliation** | A control loop: read desired state, read actual state, act on the difference, repeat forever — vs. Terraform's `plan`/`apply` run once, on demand. | Every Crossplane controller. |
| **Atlantis** | A self-hosted server running `terraform plan`/`apply` from PR comments. | Section 6, not installed. |

---

## 2. Install Terragrunt

### Goal

The `terragrunt` command, a version that matches what this repo was
validated against.

### Steps

```sh
brew install terragrunt
terragrunt --version
```

### Why

- Terragrunt is a single binary maintained by Gruntwork, distributed through
  the default Homebrew formula (no separate tap, unlike Terraform).
- Terragrunt reached 1.0 in 2025 and now follows the same "no breaking
  changes within a major version" promise Terraform does. The CLI itself was
  redesigned around that release: `terragrunt run-all plan` became
  `terragrunt run --all plan`, `graph-dependencies` became `dag graph`, and
  unknown legacy commands now fail with a message pointing at the migration
  guide instead of silently doing the wrong thing. This runbook uses the new
  command names throughout.
- This runbook was validated with **Terragrunt v1.1.4** (released August 27,
  2026, the newest release at the time of writing) against **Terraform
  v1.16.1**, which is what `.github/workflows/terraform-plan.yml` also pins.

### Check

`terragrunt --version` prints `terragrunt version 1.1.4` or newer. If your
version is older than 1.0 and a command in this runbook errors with
`unknown command`, that is the CLI redesign — use the `run --all` / `dag`
spelling shown here, or upgrade.

---

## 3. Read the wrapper before running it

### Goal

Understand `infra/terragrunt/root.hcl` and the three `terragrunt.hcl` files
before running anything against them.

### Steps

```sh
cat infra/terragrunt/root.hcl
cat infra/terragrunt/10-cluster/terragrunt.hcl
cat infra/terragrunt/20-platform/terragrunt.hcl
cat infra/terragrunt/30-apps/terragrunt.hcl
```

### Why

`root.hcl` is included by every stage and holds the two things that would
otherwise be copy-pasted three times: a `remote_state` block that generates
`backend.tf` inside the copied source, pointing Terraform's local backend at
`infra/terraform/<stage>/terraform.tfstate` — the exact path a hand-run
`terraform apply` would use — and a `terraform { extra_arguments }` block
that adds `-input=false` everywhere, so no unit ever blocks waiting for a
prompt in CI. Changing the backend once, in one file, instead of in three
`providers.tf` files, is the one piece of real DRY-ness Terragrunt buys here;
on a real cloud this block becomes an S3 backend with a bucket, a key
derived from `path_relative_to_include()`, and locking — again written once.

It is named `root.hcl`, not `terragrunt.hcl`, on purpose. `terragrunt run
--all` treats every directory holding a `terragrunt.hcl` as a runnable unit;
if the root config were also named `terragrunt.hcl`, Terragrunt would try to
run it as a fourth, parent-less unit. Current Terragrunt docs recommend the
`root.hcl` name and warn against the old convention.

Each stage's `terragrunt.hcl` then does three small things: include the root
config, point `terraform { source = ... }` at the existing
`infra/terraform/<stage>` directory unchanged, and — for stages 20 and 30 —
declare `dependency` blocks, covered next.

### Check

You can point at the one line in `root.hcl` that decides where Terraform's
state file lands (`config = { path = "${local.stage_dir}/terraform.tfstate"
}`), and explain why it uses `get_repo_root()` rather than a path relative to
the unit itself: `path_relative_to_include()` and relative paths are
evaluated inside `.terragrunt-cache/<hash>/<hash>`, a directory that moves
every run, so anything that must point at a fixed location on disk —a state
file, a script, a data file—has to be built from an absolute root instead.

---

## 4. `dependency` blocks: what they do here, and what full adoption would look like

### Goal

Understand the real effect of `dependency "cluster"` and `dependency
"platform"` in this repo today, and what changes in the Terraform stages
themselves to make those blocks do more.

### Steps

Compare what `20-platform/terragrunt.hcl` declares against what
`infra/terraform/20-platform/providers.tf` actually reads:

```sh
grep -n "dependency\|mock_outputs" infra/terragrunt/20-platform/terragrunt.hcl
grep -n "terraform_remote_state" infra/terraform/20-platform/providers.tf
```

### Why

**What the dependency blocks do now.** Two things, both real. **Ordering:**
`terragrunt run --all plan` (or `apply`, or `destroy`) computes a DAG from
every `dependency` block in the tree and runs units in that order — 10
before 20 before 30 on the way up, reversed on the way down. Without a
single `dependency` block, `run --all` would have no way to know that Argo
CD's CRDs (created in 20) must exist before 30 can even validate, and would
run all three in parallel. **Fresh-checkout validation:** `mock_outputs`
gives every dependency a placeholder value used only for `init`, `validate`
and `plan` on units that have never been applied, never for `apply` itself
— what let `terragrunt run --all validate` succeed against this repo with
zero state files anywhere: 20-platform's `dependency "cluster"` block handed
it a fake `cluster_name`, `endpoint` and `gateway_url` because 10-cluster has
never run, and validation only cares that the types match, not that the
values are real.

**What the dependency blocks do *not* do, as written.** The stages'
`providers.tf` files were not touched. They still read `data
"terraform_remote_state" "cluster"` — Terraform's own way of reading another
root module's state file — the same code path a hand-run `terraform apply`
uses without Terragrunt at all. `dependency.cluster.outputs.*` in the `.hcl`
files is never referenced by the actual Terraform code. The `generate
"absolute_paths"` block in each stage's `terragrunt.hcl` exists purely to
patch around a side effect of `source = "../../terraform/<stage>"`:
Terragrunt copies that directory into `.terragrunt-cache/<hash>/<hash>` and
runs Terraform there, so a relative path like
`${path.module}/../10-cluster/terraform.tfstate` no longer points at the real
state file — it points at an empty sibling directory inside the cache. The
override file replaces just the `config` block of that data source with an
absolute path built from `get_repo_root()`, and for 30-apps it does the same
for a second data source plus the `local.application_file` that reads
`services/k8s/argocd/application.yaml` by relative path. Nothing in
`infra/terraform` is edited; the patch lands only in the throwaway copy.

So today: `dependency` blocks buy ordering and safe validation on a fresh
checkout; the actual data flow between stages is still 100% Terraform's own
`terraform_remote_state`, exactly as it is without Terragrunt.

**What would have to change to fully adopt dependency blocks.** Delete the
`terraform_remote_state` data sources from `infra/terraform/20-platform/providers.tf`
and `infra/terraform/30-apps/providers.tf`, and: add variables to each
stage's `variables.tf` for every value it currently reads off
`local.cluster` / `local.platform` (stage 20: `cluster_endpoint`,
`cluster_ca_certificate`, `client_certificate`, `client_key`; stage 30: the
same four plus `argocd_namespace` and `app_namespace`); replace every
`local.cluster.endpoint`-style reference in `main.tf`/`providers.tf` with the
matching `var.*`; and add an `inputs = { cluster_endpoint =
dependency.cluster.outputs.endpoint, ... }` block to each stage's
`terragrunt.hcl`, which Terragrunt turns into `TF_VAR_cluster_endpoint`
environment variables before invoking Terraform. The `generate
"absolute_paths"` blocks then disappear entirely — there is no longer a
`terraform_remote_state` data source to patch — though 30-apps'
`application_file = yamldecode(file(".../services/k8s/argocd/application.yaml"))`
still needs its own fix for the same cache-copy path problem, independent of
dependency blocks.

That gets `argocd_namespace`, `app_namespace` and the credential values
flowing through Terragrunt's dependency graph instead of through Terraform's
own state-reading. It does **not** remove the need for a live cluster to plan
stages 20/30 (section 6 explains that separately). The trade-off: fully
adopting dependency blocks removes Terragrunt's file-patching, but the
stages can no longer be applied by hand with plain `terraform apply` without
also hand-supplying those variables — today's design keeps the stages 100%
usable without Terragrunt, which is why `dependency` is used only for
ordering and mock-output validation, not data flow.

### Check

You can name the exact line Terragrunt patches in 20-platform
(`data.terraform_remote_state.cluster.config.path`) and explain why it is
never applied to the real `infra/terraform/20-platform/providers.tf` file —
because `terraform { source }` copies into `.terragrunt-cache` before
`generate` writes anything, so the override lands in the copy, never the
original.

---

## 5. Run Terragrunt against this repo, without touching a cluster

### Goal

Confirm the wrapper is syntactically correct and internally consistent,
the same way `terraform validate` does for a single stage, but for all three
at once, in dependency order.

### Steps

```sh
cd infra/terragrunt
terragrunt hcl format --check --diff   # was `hclfmt --check` pre-1.0; both spellings exist in docs
terragrunt run --all validate --non-interactive
```

### Why

- `hcl format` (alias `hclfmt` still works as a top-level shortcut in some
  builds) reformats every `.hcl` file it finds under the working directory
  the way `terraform fmt` reformats `.tf` files. `--check` exits non-zero
  instead of rewriting, `--diff` shows what would change. This is what a CI
  job would run before anything else.
- `run --all validate` walks the DAG built from every `dependency` block —
  10-cluster first, then 20-platform, then 30-apps — running `terraform
  validate` in each unit's `.terragrunt-cache` copy. Because `validate` never
  evaluates a data source or contacts a provider's API, it succeeds using
  only `mock_outputs`, with no state file and no cluster anywhere. That is
  the same boundary the Terraform runbook's CI section draws for `terraform
  validate -backend=false`, just run for three units in one command instead
  of a matrix.
- This is real validation of the wrapper, not a no-op: it exercises
  `include`, both `generate` blocks, the dependency DAG, and `mock_outputs`
  end to end. If `root.hcl` had a typo, or a stage's `dependency` block named
  a directory that does not exist, this command fails before Terraform ever
  runs.

### Check

Output ends with:

```
❯❯ Run Summary  3 units  ...
   ────────────────────────────
   Succeeded    3
```

If a unit fails instead with `Unsupported attribute ... does not have an
attribute named "..."`, the mock output's key does not match what the real
`outputs.tf` produces — cross-check against
`infra/terraform/<stage>/outputs.tf`.

---

## 6. Plan-on-pull-request with GitHub Actions

### Goal

Understand `.github/workflows/terraform-plan.yml`: what runs on every pull
request, and why only one of the three stages gets a real `plan`.

### Steps

```sh
cat .github/workflows/terraform-plan.yml
```

Trigger it for real by opening a pull request that touches anything under
`infra/terraform/`, and watch the Actions tab.

### Why

The workflow has two jobs. `check` runs `terraform fmt -check -diff`,
`terraform init -backend=false -input=false` and `terraform validate` for
**all three stages**, in a matrix — the same guarantee section 5's
`terragrunt run --all validate` gives locally, run instead in CI without
Terragrunt, because `fmt`, `init -backend=false` and `validate` never touch
state or a provider's live API.

`plan` runs for **10-cluster only**, and that is a hard boundary, not a
choice of convenience. Stage 20's Kubernetes and Helm providers, and stage
30's Kubernetes provider, are configured from
`data.terraform_remote_state.cluster.outputs` — a real state file that must
already contain a real cluster's endpoint and certificates. A GitHub-hosted
runner has neither that state file (git-ignored by design, because it holds
secrets — see the Terraform runbook section 6) nor network access to a kind
cluster running in Docker on your laptop, and even granting it a copy of the
state file would not help: kind binds to `127.0.0.1`, so nothing outside the
machine that created the cluster can reach it. `plan` for stages 20/30 needs
a live, reachable cluster — a real deployment with a managed control plane
(EKS, GKE) reachable over the network, or a self-hosted runner with a route
to the cluster. Stage 10 has neither problem: the `kind` provider's plan is
pure configuration-against-schema with no external state to read, so it
produces a real, meaningful plan (`+ kind_cluster.this` on a fresh runner,
every time) using nothing but the provider's own schema.

The plan step posts its output as a pull request comment (one comment per
PR, updated on each push, found by an HTML marker), which is the pattern
every "plan-on-PR" CI workflow uses: reviewers read the diff to real
infrastructure in the same place they read the diff to code, before anyone
clicks approve.

### Check

Open a pull request touching `infra/terraform/10-cluster/variables.tf` (a
comment-only change is enough to trigger the path filter). The workflow
posts a comment starting with `#### Terraform plan for
infra/terraform/10-cluster:` and ending `success`. Confirm no `plan` job runs
for `20-platform` or `30-apps` — only `check` does, in the matrix.

### Atlantis: the self-hosted equivalent

Atlantis is a single Go binary you run yourself (as a Deployment, commonly
behind the same ingress your other services use) that listens for GitHub /
GitLab / Bitbucket webhooks instead of running inside GitHub's own runners.
A pull request touching a Terraform directory triggers a webhook to your
Atlantis server, which runs `terraform plan` in that directory (or every
directory matching its `repos.yaml` project config) and posts the plan as a
PR comment — the same job `terraform-plan.yml` does here, except the compute
running `plan` is a server you control, not a GitHub-hosted runner. `apply`
happens by commenting `atlantis apply` on the PR, gated by Atlantis's own
access-control policy, independent of GitHub's branch protection.

Because Atlantis runs on infrastructure you control, it can sit inside the
same network as a live cluster or a VPN-only cloud account — solving exactly
the reachability problem that keeps stages 20/30 out of this repo's GitHub
Actions plan job. That is the single biggest reason teams reach for Atlantis
over a hosted-runner workflow: not features, but network position. The
trade-off is operational: Atlantis is one more service you run, patch, and
grant cloud credentials to, versus a workflow file that runs on
infrastructure GitHub maintains. Most teams start with a plan-on-PR workflow
like this repo's, and move to Atlantis specifically when they hit the
reachability wall above — private VPCs, self-hosted clusters, or a policy
that `apply` must never run outside a network boundary. This runbook does
not install Atlantis; the point is to recognize the shape of the problem it
solves when you meet it.

---

## 7. Install Crossplane

### Goal

Crossplane running in the kind cluster from the Terraform runbook, ready for
the manifests in `infra/crossplane/`.

### Steps

```sh
helm repo add crossplane-stable https://charts.crossplane.io/stable
helm repo update
helm install crossplane crossplane-stable/crossplane \
  --namespace crossplane-system \
  --create-namespace \
  --version 2.4.0
kubectl get pods -n crossplane-system
```

### Why

- Crossplane installs as a set of controllers plus a package manager: once
  the core chart is running, `Provider` and `Function` objects (section 8)
  tell it to pull and run more controllers, the same way a Helm chart's
  values pull in sub-charts. **v2.4.0** is the current stable chart release
  at the time of writing (a regular quarterly Crossplane release); pin it the
  same way `argocd_chart_version` is pinned in stage 20 — an unpinned
  `helm install` silently picks up whatever `stable` currently resolves to.
- `crossplane-system` is fixed by convention, the same role `argocd`,
  `kube-system` and `enjoythings` play for their own controllers.
- This is the point where this runbook's model diverges from every Terraform
  stage: nothing here is a Terraform resource. Crossplane's controllers keep
  running after this command finishes, continuously reconciling — there is
  no `apply` that finishes and hands control to Argo CD the way stage 30
  does. Section 12 spells out that trade-off directly.

### Check

`kubectl get pods -n crossplane-system` shows `crossplane` and
`crossplane-rbac-manager` pods `Running`, and `kubectl get crds | grep -c
crossplane.io` prints a few dozen — the CRDs the core chart installs before
any `Provider` is added.

---

## 8. provider-kubernetes and the ProviderConfig

### Goal

Understand `infra/crossplane/10-provider-kubernetes.yaml` and
`20-providerconfig.yaml`: how Crossplane gets a controller that can create
plain Kubernetes objects, and how that controller authenticates.

### Steps

```sh
cat infra/crossplane/10-provider-kubernetes.yaml
cat infra/crossplane/20-providerconfig.yaml
```

To actually install it (optional, on your own kind cluster, outside this
runbook's validation):

```sh
kubectl apply -f infra/crossplane/10-provider-kubernetes.yaml
kubectl apply -f infra/crossplane/20-providerconfig.yaml
kubectl get providers
kubectl get functions
```

### Why

`10-provider-kubernetes.yaml` is four objects, applied together because the
later ones depend on the earlier. **`DeploymentRuntimeConfig`** pins the
provider pod's `ServiceAccount` name to `provider-kubernetes` — without it
Crossplane generates a random suffix, making it impossible to write a
`ClusterRoleBinding` for that account before the pod exists. **`ClusterRole`
+ `ClusterRoleBinding`** grant exactly `namespaces`, `resourcequotas` and
`networkpolicies` — the three kinds the Composition in section 10 creates;
the provider's own README suggests `cluster-admin` instead, far broader than
needed, and least privilege here is the same argument the Terraform runbook
makes about state files holding secrets: grant only what a controller
demonstrably uses. **`Provider`** (`pkg.crossplane.io/v1`, package
`crossplane-contrib/provider-kubernetes:v1.3.1`, current release) tells
Crossplane's package manager to pull that image, install the CRDs it ships
(`Object`, `ProviderConfig`, `ClusterProviderConfig`), and run its controller
Deployment in `crossplane-system`, using the runtime config above.
**`Function`** (package `crossplane-contrib/function-patch-and-transform:v0.10.10`,
current release) is not a provider at all — it is a pipeline step a
Composition calls to build resources from patches, used in section 10.

`20-providerconfig.yaml` is one `ProviderConfig` named `in-cluster`, with
`credentials.source: InjectedIdentity` — "use the ServiceAccount token
already mounted into the provider's own pod." That token is the
`provider-kubernetes` ServiceAccount from step 1, so the RBAC granted there
*is* the provider's entire permission model: it manages the cluster it runs
in, nothing else. Managing a second cluster would mean a different
`ProviderConfig` with `source: Secret` pointing at a kubeconfig, and a
Composition would select it per-resource via `providerConfigRef`.

### Check

If installed: `kubectl get providers` and `kubectl get functions` show
`INSTALLED=True`/`HEALTHY=True` a minute or two after apply, and `kubectl
describe clusterrolebinding provider-kubernetes-app-namespaces` shows the
`provider-kubernetes` ServiceAccount as its subject.

---

## 9. The XRD: defining a new Kubernetes API

### Goal

Read `infra/crossplane/30-xrd.yaml` and understand the two CRDs it creates
and the self-service boundary between them.

### Steps

```sh
cat infra/crossplane/30-xrd.yaml
```

### Why

A `CompositeResourceDefinition` (XRD) is Crossplane's schema layer: applying
one creates **two** CRDs from one spec. **`XAppNamespace`** is the composite
resource (XR), cluster-scoped (`scope: LegacyCluster`) because what it
ultimately creates (a `Namespace`) is itself cluster-scoped — the object
platform engineers query to see every application namespace Crossplane
manages, across every team. **`AppNamespace`** is the claim (XRC) — the
self-service surface: a developer creates one of these, writing only the
fields under `spec.parameters` (`cpuLimit`, `memoryLimit`, `maxPods`, each
with a default), never touching an `Object`, a `Namespace`, a
`ResourceQuota` or a `NetworkPolicy` directly, nor needing permission to.

`scope: LegacyCluster` is worth understanding precisely: Crossplane v2
introduced `Namespaced` and `Cluster` scopes for XRs that have **no** claim
at all — the namespaced XR itself becomes the thing teams create directly.
`LegacyCluster` is the v1-compatible mode that keeps the claim/XR split this
runbook's exercises use; since this Composition's job is to create a
cluster-scoped `Namespace`, cluster scope is the correct shape for the XR
either way, and `LegacyCluster` is chosen here specifically to keep the claim
around as a teaching example of the indirection.

`defaultCompositionRef` names `appnamespace-quota-netpol` — the one
Composition that exists today (section 10) — so a claim that does not pick
one explicitly still resolves to it. The `openAPIV3Schema` under
`spec.parameters` is what makes the claim self-documenting:
`kubectl explain appnamespace.spec.parameters.cpuLimit` prints the
description and default straight from this file, and the API server rejects
a claim that sets `maxPods` to `0` (below `minimum: 1`) before Crossplane's
own controller ever sees it.

### Check

If installed: `kubectl get xrd xappnamespaces.platform.enjoythings.io` shows
`ESTABLISHED=True`, and `kubectl explain appnamespace.spec.parameters` prints
the three fields, generated entirely from this YAML.

---

## 10. The Composition: how a claim becomes real objects

### Goal

Read `infra/crossplane/40-composition.yaml` and trace exactly how one claim's
fields end up in three different Kubernetes objects.

### Steps

```sh
cat infra/crossplane/40-composition.yaml
```

### Why

The Composition is a `Pipeline` with one step, `patch-and-transform`, which
calls the `function-patch-and-transform` Function installed in section 8
with a list of three resources to build — `namespace`, `quota`,
`network-policy` — each a `provider-kubernetes` `Object` wrapping one plain
Kubernetes manifest (a `Namespace`, `ResourceQuota`, `NetworkPolicy`
respectively).

The interesting part is the patches, because they are how a claim's fields
become three unrelated manifests without the Composition author repeating
the claim's schema three times. Every one of the three resources patches
`metadata.labels[crossplane.io/claim-name]` — a label Crossplane itself
writes onto the composite, copied from the claim's `metadata.name` — into
either the `Namespace`'s own name or the other two objects'
`metadata.namespace`; that single label is why a claim named `team-demo`
produces a namespace called `team-demo`, not something the Composition
hardcodes. The `quota` resource additionally patches
`spec.parameters.cpuLimit`, `memoryLimit` and `maxPods` from the composite
straight into the `ResourceQuota`'s `spec.hard` fields, with a `convert`
transform turning `maxPods` (an integer in the XRD's schema) into the string
`ResourceQuota.spec.hard.pods` expects. One patch runs the other direction:
the `namespace` resource's `patches` list includes a `ToCompositeFieldPath`
that copies the created namespace's name back onto the composite's
`status.namespace` — visible on the claim too, since Crossplane propagates a
claim's connected composite's status upward.

Because each of the three is a `provider-kubernetes` `Object`, not a native
Crossplane type, the actual manifest inside `spec.forProvider.manifest` is
byte-for-byte the same YAML you would `kubectl apply` by hand — the
Composition's whole job is deciding which three manifests to build and
patching values across them, not reinventing Kubernetes API shapes.

### Check

You can answer without looking again: which single label makes the
`Namespace`, the `ResourceQuota`'s `metadata.namespace` and the
`NetworkPolicy`'s `metadata.namespace` all agree on the same name?
(`crossplane.io/claim-name`, read off the composite via
`FromCompositeFieldPath`.)

---

## 11. The Claim, and drift correction

### Goal

Read `infra/crossplane/50-claim.yaml`, trace what applying it would create,
and understand how Crossplane responds when someone edits or deletes what it
manages — the Crossplane analogue of the Terraform runbook's drift exercise.

### Steps

```sh
cat infra/crossplane/50-claim.yaml
```

Trace it by hand, top to bottom, cross-referencing the files already read:
`AppNamespace/team-demo` in namespace `default` resolves via the XRD's
`defaultCompositionRef` to `appnamespace-quota-netpol`; Crossplane creates a
matching `XAppNamespace` (the claim's `spec.parameters` copied across, plus
the `crossplane.io/claim-name: team-demo` label); the Composition's pipeline
runs, producing three `Object` resources with `metadata.namespace` (or name,
for the `Namespace` itself) set to `team-demo` and `spec.hard` set from the
claim's `cpuLimit: "1"`, `memoryLimit: 2Gi`, `maxPods: 10`; `provider-kubernetes`
reconciles each `Object` by applying the manifest inside it — a real
`Namespace`, `ResourceQuota` and `NetworkPolicy` appear, named/namespaced
`team-demo`; and the `Namespace`'s created name flows back up
(`Object.status` → `XAppNamespace.status.namespace` →
`AppNamespace.status.namespace`), because Crossplane propagates a claim's
XR's status onto the claim.

### Why: drift correction

This is where Crossplane's model and Terraform's diverge in a way you can
observe directly, not just read about. Terraform's `plan` is a
**point-in-time diff**, run when a human (or CI) invokes it — nothing
watches between runs whether reality still matches state; the Terraform
runbook's own drift exercise (section 8c there) shows this by deleting a
Secret by hand and watching it stay gone until the next `terraform plan`.
`provider-kubernetes`'s `Object` controller **reconciles continuously**
instead: a control loop with a poll interval compares the live object
against `spec.forProvider.manifest` and re-applies on every pass, no human
or CI step involved. Delete the `ResourceQuota` created above by hand
(`kubectl delete resourcequota default-quota -n team-demo`) and it reappears
within that poll interval, unprompted — the same self-healing Argo CD gives
application workloads, just one layer lower, applied to the namespace's own
infrastructure instead of Deployments.

This is not free: continuous reconciliation means Crossplane is *always*
running controllers that can act on the cluster, versus Terraform's model
where nothing runs between invocations. It also means there is no single
"plan" to read before a change lands — a Composition edit takes effect on
every matching XR the next time each one reconciles, not on a schedule a
human chose. Section 12 weighs this trade-off directly.

### Check

You can state, without re-reading the manifests, what would happen to the
`team-demo` `NetworkPolicy` if someone ran
`kubectl edit networkpolicy default-allow-same-namespace-only -n team-demo`
and changed the `podSelector`: the `Object` controller's next reconcile pass
would see the live object no longer matches
`spec.forProvider.manifest.spec.podSelector` and patch it back, the same
answer as the `ResourceQuota` case above, on whatever interval the provider
polls at (a few minutes by default) rather than "never, until someone
notices" (Terraform) or "immediately, and only if a human ran `apply`."

---

## 12. Terraform vs. a control plane: the trade-off

### Goal

Weigh the two models directly, having now driven both against the same kind
of resource.

### Why

| | Terraform (+ Terragrunt) | Crossplane |
| --- | --- | --- |
| **Model** | Plan/apply, on demand. | Continuous reconciliation, no human step. |
| **Drift** | Detected only on the next `plan`/`refresh`. | Corrected automatically, every poll interval. |
| **Where state lives** | A state file outside the cluster, holding real secrets in plain text. | The Kubernetes API server itself, secured by the cluster's own RBAC and audit log. |
| **Self-service surface** | A PR that changes `.tf`/`.hcl`, applied by CI or a human with cloud credentials. | A namespaced object (the claim) a team creates directly — no cloud credentials needed. |
| **Schema enforcement** | HCL variable types, checked at `plan` time. | The XRD's `openAPIV3Schema`, enforced by the API server before any controller runs. |
| **Failure mode** | A human reads the plan before confirming. | A bad Composition change rolls out on next reconcile — no approval gate unless you build one. |
| **Good at** | Clear-finish-line jobs: a VPC, a cluster, an IAM role. | Things that must stay true forever: a namespace's quota, a backup policy. |
| **Operational cost** | A CI runner, only while a command executes. | Controllers running in the cluster at all times. |

Neither replaces the other in this repo: stages 10–30 create the cluster and
Argo CD itself, a "provision once, evolve occasionally" job Terraform is
good at. The `AppNamespace` claim is a "many teams, repeated self-service,
must never silently drift" job Crossplane's reconciliation is built for. A
team adopting Crossplane typically keeps Terraform (or Terragrunt) for the
cluster and cloud account underneath it, and layers Crossplane on top for
exactly the self-service surfaces this section describes.

---

## 13. Pulumi and eksctl: a short comparison

Two more tools you will hear about in the same conversations as Terragrunt
and Crossplane. Neither is used in this repo; this table exists so you can
place them relative to what you already know.

| | Terraform (+ Terragrunt) | Pulumi | eksctl |
| --- | --- | --- | --- |
| **What it is** | HCL, a declarative plan/apply CLI. | IaC in a general-purpose language (TypeScript, Python, Go, C#, Java, YAML). | A single-purpose CLI that creates/deletes an EKS cluster and node groups. |
| **Config language** | HCL, Terraform-specific. | Real code: loops, functions, package managers, tests. | A short YAML cluster spec, or flags. |
| **State** | A state file (or a wrapper's convention, as Terragrunt does here). | Pulumi Cloud by default, or a self-managed backend — same idea as a state file. | None to manage — it drives CloudFormation stacks; AWS holds that state. |
| **Scope** | Hundreds of providers, including Kubernetes, Helm, `kind`. | Similarly broad — most Pulumi providers are generated from Terraform provider schemas. | AWS EKS only — no S3, no IAM beyond the cluster, no in-cluster objects. |
| **Reach for it when** | A mature ecosystem and HCL's constraints are acceptable; want Terragrunt/Atlantis-style tooling. | Already fluent in a supported language and want real abstractions over HCL's limited expressions, trading a smaller ecosystem for that. | Want an EKS cluster today, fast, with no IaC — a bootstrap tool teams often replace with Terraform/Crossplane once the cluster exists. |

---

## 14. Hands-on practice

Do these against this repo's actual files. None of them touch a live
cluster or apply anything.

### 14a. `terragrunt run --all validate` and reading the summary

```sh
cd infra/terragrunt
terragrunt run --all validate --non-interactive
```

Read the summary Terragrunt prints at the end (`Run Summary  3 units ...
Succeeded  3`) and the per-unit `WARN` lines above it. Answer: which two
units printed a warning about "no outputs, but mock outputs provided," and
why didn't `10-cluster` print one? (It has no `dependency` block — the
warning is emitted by the unit that *declares* a dependency, not the one
being depended on.) Seeing a real `run --all plan` across the tree, rather
than `validate`, would need `10-cluster`'s Terraform state to already exist
locally — that means running `terraform apply` at least once, covered in
the Terraform runbook, not here.

### 14b. Read the dependency graph

```sh
cd infra/terragrunt
terragrunt dag graph
```

(On Terragrunt versions before the 1.0 CLI redesign this was
`terragrunt graph-dependencies`; both produce Graphviz DOT.) Expect:

```
digraph {
	"10-cluster" ;
	"20-platform" ;
	"20-platform" -> "10-cluster";
	"30-apps" ;
	"30-apps" -> "10-cluster";
	"30-apps" -> "20-platform";
}
```

Read the arrows as "depends on." Paste the output into
[Graphviz Online](https://dreampuf.github.io/GraphvizOnline/) (or `dot -Tpng`
if Graphviz is installed) to see it rendered. Confirm it matches exactly the
apply order the Terraform runbook's section 3 derives from first principles
(a provider needs its cluster before it can be configured; a custom resource
needs its CRD before it can be validated) — the graph makes the ordering
that was always implied by the stages' own data sources into something
`run --all` enforces automatically, instead of you remembering to `cd` into
three directories in the right order.

### 14c. Introduce a `fmt` error and watch CI fail

Make a deliberately misformatted change, for example in
`infra/terraform/10-cluster/variables.tf`, add two extra spaces before a
`default` value so it misaligns from the rest of the block. Open a draft
pull request and watch `.github/workflows/terraform-plan.yml`'s `check`
job: the `terraform fmt -check -diff` step for the `10-cluster` matrix entry
fails and prints the exact diff `terraform fmt` would apply, and because
`plan` declares `needs: check`, `plan` never runs at all. Fix it locally with
`terraform fmt -recursive infra/terraform`, push again, and watch both jobs
go green. This is the same gate the Terraform runbook's section 8f runs by
hand; the difference is CI enforces it on every PR instead of trusting
everyone to run it before pushing. Revert your misformatting before
finishing (do not commit it).

### 14d. Trace a claim to its Composition, without applying it

Using only the five files in `infra/crossplane/`, answer without running
anything:

- If `50-claim.yaml`'s `spec.parameters.maxPods` were changed to `0`, at what
  point would that be rejected, and by what? (The Kubernetes API server, at
  claim-creation time, because the XRD's schema declares `minimum: 1` — never
  reaches the Composition or the provider at all.)
- If a second Composition existed with a different
  `platform.enjoythings.io/flavor` label, and a claim did not set
  `spec.compositionRef`, which one would be used? (Whichever the XRD's
  `defaultCompositionRef` names — today, always
  `appnamespace-quota-netpol`, since it is the only one that exists.)
- Which field, present on the claim, the composite, and the `Namespace`
  object, is the single thread connecting all three? (The name
  `team-demo` — as the claim's own `metadata.name`, copied by Crossplane onto
  the composite as the `crossplane.io/claim-name` label, then patched by the
  Composition into the `Namespace`'s `metadata.name`.)

This is the exercise for section 10 and 11's walkthrough: proving you can
follow one value across five files without a cluster to check your work
against.

### 14e. Explain drift correction in one sentence per tool

Without looking back at section 12's table, write one sentence each for:
"Terraform detects drift when ___", and "Crossplane corrects drift when
___." (Terraform: only when a human or CI next runs `plan`/`refresh` against
that state. Crossplane: continuously, every reconcile-loop pass, with no
human step at all.) If your two sentences do not name a different trigger
for each tool, re-read section 11's drift walkthrough — that difference is
the entire point of the exercise.

---

## 15. What to read next

- [`terraform-runbook.md`](./terraform-runbook.md) — the prerequisite for
  everything above it, if you have not already read it.
- [`argocd-runbook.md`](./argocd-runbook.md) and
  [`k8s-deployment-runbook.md`](./k8s-deployment-runbook.md) — what Argo CD
  and plain `kubectl` do with the same cluster, underneath both Terraform
  and Crossplane.
- [Terragrunt's docs](https://terragrunt.gruntwork.io/), particularly the
  CLI redesign migration guide — a lot of existing blog content predates it
  and uses the old `run-all`/`graph-dependencies` names.
- [Crossplane's docs](https://docs.crossplane.io/), starting at "Get
  started" (installs the same chart section 7 does) then "Composite
  Resources" for the XRD/XR/claim relationship in full, including the
  `Namespaced`/`Cluster` scopes this runbook's `LegacyCluster` choice
  sidesteps.
