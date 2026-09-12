# Terraform Runbook

This runbook builds the same environment as the Kubernetes and Argo CD runbooks,
but with Terraform doing the work instead of you typing commands. It exists to
teach infrastructure as code on a laptop, at no cost, in a way that transfers
to a real cloud.

Same format as the other runbooks: each section has a goal, the commands, why
each step exists, and how to check it worked. Read
[`k8s-deployment-runbook.md`](./k8s-deployment-runbook.md) and
[`argocd-runbook.md`](./argocd-runbook.md) first; this one assumes you know what
a Pod, a Secret and an Argo CD Application are.

## Why Docker is the cloud here

Terraform's job is to create infrastructure: networks, clusters, databases,
credentials. On AWS that means real resources with real bills. There is no free
simulator for a managed Kubernetes service: LocalStack, the usual AWS emulator,
puts EKS behind its paid tier. So this runbook treats your Docker daemon as the
cloud and a kind cluster as the managed cluster the cloud would hand you.

What transfers to a real cloud unchanged:

- The Terraform workflow: `init`, `plan`, `apply`, state, outputs, `destroy`.
- The layering into stages, and why a provider cannot be configured from a
  resource created in the same apply.
- Installing cluster add-ons with the Helm provider, generating credentials
  with the random provider, writing them into Kubernetes Secrets, registering
  Argo CD Applications with the Kubernetes provider.
- Reading one stage's outputs from another stage's state.

What does not: the single `kind_cluster` resource in stage 10. On AWS that one
block becomes a VPC module and an EKS module. Section 11 shows the swap.

## What the code creates

```
infra/terraform/
├── 10-cluster/     kind cluster with the gateway port mapping      ≈ VPC + EKS
├── 20-platform/    Argo CD, app namespace, generated Secret         ≈ add-ons + secret manager
└── 30-apps/        Argo CD Application for the EnjoyThings chart    ≈ same
```

Stages are applied in order and destroyed in reverse. Each is an independent
Terraform root module with its own state file.

---

## 1. Terraform vocabulary you need

| Term | What it is | Where it shows up here |
| --- | --- | --- |
| **Provider** | A plugin that knows how to talk to one API. | `tehcyx/kind`, `hashicorp/helm`, `hashicorp/kubernetes`, `hashicorp/random`. |
| **Resource** | One thing Terraform creates and owns. | `kind_cluster.this`, `helm_release.argocd`, `kubernetes_secret_v1.app`. |
| **Data source** | Something Terraform reads but does not own. | `terraform_remote_state.cluster` reads stage 10's outputs. |
| **Variable** | An input with a type and a default. | `cluster_name`, `argocd_chart_version`, `llm_api_key`. |
| **Output** | A value exported after apply, for humans or other stages. | `endpoint`, `jwt_secret`, `argocd_admin_password_command`. |
| **State** | Terraform's record of what it created and the real IDs. A local file `terraform.tfstate` here; a remote, locked, encrypted store in real use. | One per stage. **Contains the generated passwords in plain text.** |
| **Plan** | The diff between code plus state and reality. Nothing changes until you apply. | `+` create, `~` update in place, `-/+` destroy and recreate, `-` destroy. |
| **Apply / Destroy** | Execute the plan. Destroy is a plan that removes everything. | Sections 4 to 7, and 10. |
| **Root module** | A directory Terraform runs in. Each stage is one. | The three numbered directories. |
| **Lock file** | `.terraform.lock.hcl`, records exact provider versions and checksums. Committed on purpose. | One per stage. |
| **Sensitive** | A flag that hides a value from `plan` output and `terraform output`. It does not encrypt anything. | Every certificate and password output. |

---

## 2. Install Terraform

### Goal

The `terraform` command, version 1.6 or newer.

### Steps

```sh
brew install hashicorp/tap/terraform
terraform version
```

### Why

- Terraform is a single binary. HashiCorp distributes it through its own
  Homebrew tap because the license changed in 2023. OpenTofu is the
  community fork under the old license; it runs this code unchanged if you
  prefer it (`brew install opentofu`, then `tofu` instead of `terraform`).
- `required_version = ">= 1.6"` in each `versions.tf` refuses to run on an
  older binary, which is how a team avoids state files written by mismatched
  versions.

### Check

`terraform version` prints `Terraform v1.6` or later. This runbook was
validated with `v1.16.0`.

---

## 3. Read the code before running it

### Goal

Know what each file does, so `plan` output is not a surprise.

### Steps

```sh
cd infra/terraform
cat 10-cluster/*.tf
cat 20-platform/*.tf
cat 30-apps/*.tf
```

### Why

Every stage uses the same four files. The split is convention, not
requirement, but it is the convention you will meet everywhere.

| File | Holds | Read it for |
| --- | --- | --- |
| `versions.tf` | Which Terraform and which providers, with version constraints. | What has to be downloaded by `init`. |
| `providers.tf` | How to connect to the APIs. | Where the cluster credentials come from. |
| `main.tf` | The resources. | What gets created. |
| `variables.tf` | Inputs and their defaults. | What you can change without editing code. |
| `outputs.tf` | Exports. | What later stages and you get back. |

**Why three stages instead of one.** Two rules of Terraform force it:

1. A provider must be fully configured before `plan` runs. Stage 20's
   Kubernetes provider needs the cluster endpoint and certificates. If the
   cluster were created in the same root module, those values would be unknown
   at plan time and the provider would fail to connect. So the cluster lives in
   stage 10, and stage 20 reads its outputs from state.
2. `kubernetes_manifest` validates against the cluster's API at plan time. An
   Argo CD `Application` is a custom resource, so its definition must already
   be installed before stage 30 can even plan. Argo CD is installed in stage 20.

Real cloud repositories are layered for the same two reasons, typically
network, cluster, platform, applications. The numbers in the directory names
are the apply order.

**How stages talk.** `providers.tf` in stages 20 and 30 contains:

```hcl
data "terraform_remote_state" "cluster" {
  backend = "local"
  config  = { path = "${path.module}/../10-cluster/terraform.tfstate" }
}
```

That reads stage 10's outputs. With a remote backend the `config` block would
name an S3 bucket and key instead; nothing else changes.

### Check

You can answer: which stage creates the `enjoythings` namespace, and why is it
not left to Argo CD's `CreateNamespace=true`? (Stage 20, because the Secret has
to exist in that namespace before Argo CD's first sync.)

---

## 4. Stage 10: create the cluster

### Goal

A kind cluster identical to the one from the Kubernetes runbook, now recorded
in Terraform state.

### Steps

```sh
cd infra/terraform/10-cluster
terraform init
terraform plan
terraform apply
```

Type `yes` at the prompt. Then:

```sh
terraform output
kubectl config current-context
kubectl get nodes
```

### Why

- `init` downloads the providers named in `versions.tf` into `.terraform/`
  and writes `.terraform.lock.hcl`. Run it once per directory, and again
  whenever `versions.tf` changes.
- `plan` prints what would happen. The first time, one resource with a `+`.
  Read plans every time; they are the whole point of the tool.
- `apply` re-plans, asks for confirmation, then creates. The kind provider
  drives the same library as the `kind` CLI, so the result is exactly
  `kind create cluster --config services/k8s/kind/cluster.yaml`, and the
  provider also merges a `kind-enjoythings` context into your kubeconfig the
  way the CLI does. It additionally writes a standalone kubeconfig named
  `enjoythings-config` next to the state file. That file is a credential, so
  it is git-ignored; `KUBECONFIG=./enjoythings-config kubectl get nodes` is a
  handy way to target this cluster without switching contexts.
- `terraform output` prints the exports. The certificates show as
  `<sensitive>`; `terraform output -raw client_key` reveals one when you need
  it. Stage 20 reads them from state, not from your kubeconfig, which is
  the same shape as reading an EKS endpoint and CA from an EKS module.
- The variables have defaults matching the other runbooks: cluster name
  `enjoythings`, host port `18080`. Override with `-var gateway_host_port=28080`
  or a `terraform.tfvars` file.

### Check

`kubectl get nodes` shows `enjoythings-control-plane Ready`. `terraform output
gateway_url` prints `http://localhost:18080`. The apply takes about a minute.

---

## 5. Load the images

### Goal

The nine application images inside the kind node.

### Steps

Exactly section 4 of the Kubernetes runbook:

```sh
cd services
for svc in gateway wallet ledger verification saga-orchestrator \
           payment-processor notification stub-payment-rail; do
  docker build --build-arg SERVICE=$svc -t enjoythings/$svc:local .
  kind load docker-image enjoythings/$svc:local --name enjoythings
done
cd ..
docker build -f app/fraud/Dockerfile -t enjoythings/fraud-worker:local .
kind load docker-image enjoythings/fraud-worker:local --name enjoythings
```

### Why

This step is deliberately not in Terraform. Building and publishing images is
a CI job, not infrastructure: it runs on every commit, produces artifacts, and
has nothing to record in state. In a cloud setup CI pushes to a registry and
the cluster pulls; Terraform would at most create the registry. Keeping the
boundary clean here is how you keep it clean there.

If you skip this step nothing breaks in Terraform. The pods Argo CD creates in
section 7 sit in `ImagePullBackOff` until the images arrive, then start.

### Check

`docker exec enjoythings-control-plane crictl images | grep -c enjoythings`
prints `9`.

---

## 6. Stage 20: install Argo CD and create the Secret

### Goal

Argo CD running, the `enjoythings` namespace present, and `enjoythings-secret`
populated with generated passwords.

### Steps

```sh
cd infra/terraform/20-platform
cp terraform.tfvars.example terraform.tfvars   # optional: set llm_api_key for a hosted LLM
terraform init
terraform plan
terraform apply
```

Then:

```sh
kubectl get pods -n argocd
kubectl get secret enjoythings-secret -n enjoythings -o jsonpath='{.data}' | jq 'keys'
terraform output -raw jwt_secret; echo
eval "$(terraform output -raw argocd_admin_password_command)"; echo
```

### Why

Six resources, each replacing a manual step from the Argo CD runbook:

| Resource | Replaces | Note |
| --- | --- | --- |
| `helm_release.argocd` | `kubectl apply -f install.yaml` | Pinned to a chart version, so upgrades are a one-line diff. `wait = true` blocks until every Argo CD pod is ready, about two minutes. |
| `kubernetes_namespace_v1.app` | `kubectl create namespace enjoythings` | Created here so the Secret has somewhere to live before Argo CD syncs. |
| `random_password` ×3 | `openssl rand` | Generated once, stored in state, stable across applies. |
| `kubernetes_secret_v1.app` | `kubectl create secret generic ...` | Same name and six keys the chart expects with `secrets.create=false`. Each database password is used twice, from one variable, so the container and the connection URL cannot disagree. |

Two things to internalize:

- **State now holds secrets.** `terraform.tfstate` in this directory contains
  the JWT secret and both database passwords in plain text. `.gitignore`
  excludes it, but on a laptop that is the only protection. This is the
  concrete reason real teams use a remote backend with encryption at rest and
  access control, never a state file in a repo or a shared drive.
- **The provider configuration came from stage 10's state.** Nothing here read
  your kubeconfig. If you ran `kind delete cluster` by hand, this stage would
  fail with `connection refused`, because state says the cluster exists at an
  endpoint that no longer answers. Section 9 covers recovery.

The Argo CD admin password is generated by the chart, not by Terraform, so it
is read from the cluster with the command in the output. Change it and delete
the initial Secret as in section 3 of the Argo CD runbook.

### Check

`kubectl get pods -n argocd` shows seven pods `Running` and one init job
`Completed`. The Secret has six keys. The `jq` output lists exactly:
`DATABASE_URL`, `FRAUD_DATABASE_URL`, `FRAUD_POSTGRES_PASSWORD`, `JWT_SECRET`,
`LOCAL_LLM_API_KEY`, `POSTGRES_PASSWORD`.

---

## 7. Stage 30: register the Application

### Goal

Argo CD deploying the chart from git, registered by Terraform.

### Steps

Make sure the chart changes and `services/k8s/argocd/application.yaml` are
pushed to the branch Argo CD watches (`master` unless you override it). Argo
CD reads GitHub, not your working tree.

```sh
cd infra/terraform/30-apps
terraform init
terraform plan
terraform apply
```

Then, with a port-forward to Argo CD running as in the Argo CD runbook:

```sh
argocd app get enjoythings
argocd app wait enjoythings --health --timeout 600
curl -i http://localhost:18080/readyz
```

### Why

- The single resource is a `kubernetes_manifest` whose content is
  `yamldecode(file("../../../services/k8s/argocd/application.yaml"))`. The
  manifest the Argo CD runbook applies by hand is the same bytes Terraform
  applies here, so there is one source of truth for what "the application" is.
  Terraform only overlays the namespaces from stage 20's outputs and, if you
  set `-var target_revision=some-branch`, the branch.
- `computed_fields` lists the paths Argo CD's controller writes to after
  creation. Without it, every later `plan` would propose removing the labels
  and annotations Argo CD added, and `apply` would fight the controller.
- After this apply Terraform's involvement with the workloads ends. It does
  not know about the twelve Deployments; Argo CD owns them. Running
  `terraform plan` here after Argo CD has synced shows `No changes`, which is
  the correct division of labor: Terraform declares that the app exists and
  where it comes from, Argo CD keeps it running.

### Check

`terraform plan` prints `No changes`. `argocd app get enjoythings` shows
`Synced` and `Healthy` once images are loaded. `/readyz` returns `200`.

---

## 8. Practice the Terraform loop

### Goal

See what `plan` says for the four kinds of change you will meet, and learn to
read state.

### Steps and why

**8a. A change that updates in place.** Bump the Argo CD chart version.

```sh
cd infra/terraform/20-platform
terraform plan -var argocd_chart_version=10.8.2   # or whatever the next version is
```

The plan shows `~ helm_release.argocd` with `version: "10.8.1" -> "10.8.2"`.
A tilde means Terraform can change the existing thing. Apply it and Argo CD
upgrades with no downtime for anything it manages.

**8b. A change that forces replacement.** Change the cluster's port mapping.

```sh
cd infra/terraform/10-cluster
terraform plan -var gateway_host_port=28080
```

The plan shows `-/+ kind_cluster.this (forces replacement)`. Do **not**
apply. The kind provider cannot edit a running cluster's port mappings, so
Terraform would destroy the cluster, and with it every workload, then create a
new one. Stage 20 and 30 state would then point at a cluster that no longer
exists. Reading the plan for `forces replacement` before typing `yes` is the
single most important habit in Terraform. On a cloud, the same words on a
database resource mean data loss.

**8c. Drift.** Delete something Terraform owns, then plan.

```sh
kubectl delete secret enjoythings-secret -n enjoythings
cd infra/terraform/20-platform
terraform plan
```

The plan shows `+ kubernetes_secret_v1.app` because refresh noticed it is
gone. `terraform apply` recreates it with the **same** passwords, because
`random_password` results live in state and are not regenerated. Argo CD's
self-heal covers drift in the workloads; Terraform's refresh covers drift in
what Terraform owns. Together they cover the cluster.

**8d. Rotate a secret.** Force one resource to be recreated.

```sh
terraform apply -replace=random_password.jwt
kubectl rollout restart deployment -n enjoythings
```

`-replace` regenerates the JWT secret and, because the Secret depends on it,
rewrites the Secret. Pods keep the old value in their environment until
restarted, hence the second command. Existing tokens stop working, which is
what rotation means.

**8e. Read state.**

```sh
terraform state list                          # every resource this stage owns
terraform state show random_password.jwt      # its recorded attributes
terraform output -raw postgres_password       # one output, no quotes, for scripts
terraform show                                # the whole state, human readable
```

Notice that `state show` prints the password. Anyone who can read the state
file can read every secret in it.

**8f. Hygiene before committing.**

```sh
cd infra/terraform
terraform fmt -recursive
for d in 10-cluster 20-platform 30-apps; do (cd $d && terraform validate); done
```

`fmt` normalizes whitespace so diffs show only meaning. `validate` catches
type and reference errors without touching any API. Both belong in CI.

---

## 9. Troubleshooting

| Symptom | Meaning | What to do |
| --- | --- | --- |
| `init`: `Failed to query available provider packages` | No route to `registry.terraform.io`. | Check network and proxy. Providers are downloaded once and cached in `.terraform/`. |
| `apply` in 10: `node(s) already exist for a cluster with the name "enjoythings"` | A cluster with that name exists outside Terraform state, for example from the Kubernetes runbook. | `kind delete cluster --name enjoythings`, then apply again. Terraform cannot adopt a kind cluster it did not create. |
| `plan` in 20 or 30: `Unsupported attribute` on `local.cluster.endpoint` | Stage 10 has not been applied, or its state file moved. | Apply stage 10. Confirm `10-cluster/terraform.tfstate` exists. |
| `plan` in 20: `connection refused` or `no such host` for `127.0.0.1:<port>` | The cluster in stage 10's state no longer exists. Someone ran `kind delete cluster`. | In 10-cluster: `terraform state rm kind_cluster.this`, then `terraform apply` to recreate, then re-apply 20 and 30. Or destroy 20 and 30 state the same way. |
| `plan` in 30: `no matches for kind "Application" in group "argoproj.io"` | Argo CD's CRDs are not installed. | Apply stage 20 first. This is the reason 30 is a separate stage. |
| `helm_release.argocd` times out | Slow image pulls on first install. | `kubectl get pods -n argocd`; when they are ready, `terraform apply` again. Helm marks the release failed but the pods keep coming up. |
| `plan` keeps showing changes on `kubernetes_manifest.enjoythings` | Argo CD writes a field not listed in `computed_fields`. | `terraform plan` names the path. Add it to `computed_fields` in `30-apps/main.tf`. |
| Argo CD app `Unknown`: `Failed to load target state ... context deadline exceeded` | Pods in the cluster cannot reach GitHub. | Test with `kubectl run curl --rm -it --image=curlimages/curl:8.10.1 -- curl -sI https://github.com`. Fix Docker's network or proxy; this is not a Terraform problem. |
| `Error acquiring the state lock` | A previous run crashed and left `.terraform.tfstate.lock.info`. | Make sure no other `terraform` process is running, then `terraform force-unlock <id>` with the ID from the message. |
| `destroy` in 10 hangs or errors after you already deleted Docker containers | State and reality disagree. | `terraform state rm kind_cluster.this` then `terraform destroy` reports nothing to do. |

---

## 10. Cleanup

Destroy in reverse order. Each stage's providers need the cluster to still be
there.

```sh
cd infra/terraform
(cd 30-apps     && terraform destroy)
(cd 20-platform && terraform destroy)
(cd 10-cluster  && terraform destroy)
```

Destroying 30 deletes the Argo CD Application, and its finalizer deletes every
workload. Destroying 20 removes Argo CD, the namespace and the Secret.
Destroying 10 deletes the cluster and removes the `kind-enjoythings` context
from your kubeconfig. The state files remain, now recording zero resources;
the `.terraform/` directories keep the downloaded providers so the next `init`
is instant.

If you only want a fresh application install, destroy and re-apply 30 alone.

---

## 11. Toward a real cloud

The stages stay. Their contents change like this.

1. **Stage 10 becomes network plus cluster.** Replace `kind_cluster` with the
   community modules `terraform-aws-modules/vpc/aws` and
   `terraform-aws-modules/eks/aws`. Their outputs are the same shape: endpoint,
   CA certificate, and a way to authenticate. `docs/aws-deployment.md` sections
   4 and 6 give the subnet plan and node sizes to feed them.

2. **Authentication changes, provider blocks do not.** kind hands out a client
   certificate. EKS hands out short-lived tokens, so the Kubernetes and Helm
   providers use an `exec` block that runs `aws eks get-token`, or the
   `aws_eks_cluster_auth` data source. Everything below the provider block is
   untouched.

3. **State moves to a remote backend.** Add a `backend "s3"` block to each
   stage with a bucket, a key per stage, encryption on, and state locking. The
   `terraform_remote_state` data sources change their `backend` and `config`
   to match. This is the fix for the "state holds secrets" problem from section
   6, and the prerequisite for two people running Terraform.

4. **Secrets move out of state where possible.** Keep `random_password` for
   things only the cluster needs, but write them into AWS Secrets Manager with
   `aws_secretsmanager_secret_version`, and let External Secrets Operator
   (installed in stage 20) copy them into `enjoythings-secret`. Terraform then
   never holds the Kubernetes Secret at all.

5. **Stage 20 grows.** AWS Load Balancer Controller, cert-manager,
   metrics-server, External Secrets Operator, and the IAM roles for service
   accounts they need, each a `helm_release` or an IAM module.

6. **One directory or workspace per environment.** `envs/dev`, `envs/staging`,
   `envs/prod`, each with its own `terraform.tfvars` and backend key, all
   calling the same stage modules. Promotion is copying a variable value, not
   editing code.

7. **Terraform runs in CI.** `fmt -check`, `validate` and `plan` on every pull
   request with the plan posted as a comment; `apply` only on merge to the
   main branch, with the same OIDC role the AWS deployment document describes
   for image pushes.

8. **Practice the AWS provider syntax first, for free.** LocalStack's free
   tier emulates S3, IAM, Secrets Manager and SSM well enough to write and
   apply real `aws_*` resources against `http://localhost:4566`. It does not
   emulate EKS, which is why this runbook uses kind for the cluster.
