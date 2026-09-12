# Argo CD Runbook

This runbook replaces the `helm upgrade` step of
[`k8s-deployment-runbook.md`](./k8s-deployment-runbook.md) with GitOps: Argo CD
runs inside the cluster, watches the git repository, and keeps the cluster
equal to what the Helm chart renders from a branch. Deploying becomes
`git push`.

It follows the same format as the Kubernetes runbook. Every section states a
goal, the commands, why each step exists, and how to check it worked.

## Before you start

Complete sections 2, 3 and 4 of the Kubernetes runbook: tools installed, kind
cluster running, all nine images loaded into it. If the Helm release from
section 7 is still installed, remove it first so Argo CD starts from an empty
namespace:

```sh
helm uninstall enjoythings -n enjoythings
kubectl delete namespace enjoythings
```

Two things must be true about the repository:

- **The chart changes that this runbook relies on are pushed to the branch
  Argo CD watches.** Argo CD reads git, never your working tree. The `secrets.create`
  switch in `values.yaml` and the `services/k8s/argocd/application.yaml`
  manifest have to be on `master` on GitHub before section 6 works.
- The repository is public, so Argo CD needs no credentials to read it. Section
  4 covers the private case anyway.

## What changes compared with Helm from your laptop

| | Helm from your laptop (k8s runbook §7) | Argo CD |
| --- | --- | --- |
| Who runs Helm | You, with `helm upgrade` | Argo CD, continuously, inside the cluster |
| Source of truth | Whatever is on your disk when you run the command | A git branch |
| How to deploy | Run a command | Commit and push |
| Who notices drift | Nobody. A `kubectl scale` sticks until the next upgrade. | Argo CD, within seconds, and it reverts it if you ask |
| Where credentials live | A values file on your laptop | A Kubernetes Secret you create once; git never sees it |
| Rollback | `helm rollback` | `git revert` and push |

---

## 1. GitOps vocabulary you need

| Term | What it is |
| --- | --- |
| **Application** | Argo CD's unit of work: one source (repo, branch, path) deployed to one destination (cluster, namespace). Defined as a Kubernetes resource of kind `Application`. |
| **Sync** | Rendering the source and applying it to the cluster. Manual or automated. |
| **Synced / OutOfSync** | Whether the live cluster matches what git renders. |
| **Healthy / Progressing / Degraded / Missing** | Whether the deployed resources are actually working, judged by Argo CD's built-in health checks (for a Deployment: are the desired replicas available). |
| **Self-heal** | When the live cluster drifts from git, sync again automatically. |
| **Prune** | When a resource disappears from git, delete it from the cluster. Off by default because it is destructive. |
| **targetRevision** | The branch, tag or commit to deploy. |
| **AppProject** | A policy boundary: which repos and destinations a group of Applications may use. The `default` project allows everything. |
| **Refresh** | Re-read git and recompute the diff. Argo CD polls every 3 minutes by default; refresh forces it now. |
| **Sync hook / wave** | Ordering controls. Argo CD translates the chart's Helm hooks into its own, so `kafka-topic-init` still runs after everything else. |

---

## 2. Install Argo CD into the cluster

### Goal

Argo CD's components running in their own `argocd` namespace.

### Steps

```sh
kubectl create namespace argocd
kubectl apply -n argocd -f https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml
kubectl get pods -n argocd -w
```

Install the CLI too:

```sh
brew install argocd
```

### Why

- Argo CD is itself a set of Kubernetes Deployments, so it installs like any
  other application. The `stable` manifest is the maintainers' current release.
  For a real cluster you would pin a version and use the `ha/` variant; see
  section 11.
- The components you will see: `argocd-server` (API and web UI),
  `argocd-repo-server` (clones git and runs `helm template`),
  `argocd-application-controller` (compares git with the cluster and syncs),
  `argocd-redis` (cache), `argocd-dex-server` (SSO, unused here),
  `argocd-applicationset-controller` and `argocd-notifications-controller`
  (unused here).
- It goes into its own namespace so that uninstalling Argo CD never touches
  the applications it manages, and so its permissions are easy to reason about.
- The CLI is optional; everything it does the web UI also does. It is worth
  having because it is scriptable and its output pastes into a chat.

### Check

All pods in `argocd` are `Running` and `READY`, typically within two minutes.

---

## 3. Log in to the UI and CLI

### Goal

A browser tab and a terminal both authenticated to your Argo CD.

### Steps

```sh
# terminal A: tunnel the UI to your laptop
kubectl port-forward svc/argocd-server -n argocd 8443:443

# terminal B: read the generated admin password
argocd admin initial-password -n argocd

argocd login localhost:8443 --username admin --insecure
argocd account update-password
kubectl delete secret argocd-initial-admin-secret -n argocd
```

Open <https://localhost:8443>, accept the self-signed certificate warning, and
log in as `admin` with your new password.

### Why

- `argocd-server` is a `ClusterIP` Service, so like the gateway in the
  Kubernetes runbook it is reached through a port-forward. It speaks HTTPS with
  a self-signed certificate, hence `--insecure` on the CLI and the browser
  warning.
- Argo CD generates a random admin password at install time and stores it in a
  Secret. It is meant to be used once and deleted, which is what the last two
  commands do. Leaving it in place means anyone with read access to the
  `argocd` namespace can log in as admin.

### Check

`argocd app list` runs without an error and prints an empty table. The UI shows
an empty Applications page.

---

## 4. Connect the repository

### Goal

Argo CD can read `https://github.com/duylongpro99/enjoythings.git`.

### Steps

The repository is public, so nothing is required. Confirm Argo CD can clone it:

```sh
argocd repo add https://github.com/duylongpro99/enjoythings.git
argocd repo list
```

For a **private** repository you would instead register credentials once:

```sh
# HTTPS with a fine-grained GitHub token that has read access to Contents
argocd repo add https://github.com/<org>/<repo>.git --username <github-user> --password <token>

# or SSH with a deploy key
argocd repo add git@github.com:<org>/<repo>.git --ssh-private-key-path ~/.ssh/argocd_deploy_key
```

### Why

- The repo server clones the repository on every refresh. For a public repo an
  anonymous clone works; registering it anyway makes the connection status
  visible in the UI under Settings, Repositories, which is where you look when
  an Application says `ComparisonError`.
- Credentials for private repos are stored as Secrets in the `argocd`
  namespace, not in any Application manifest, so the manifest stays safe to
  commit.

### Check

`argocd repo list` shows the repository with `CONNECTION STATUS Successful`.

---

## 5. Create the application Secret before the first sync

### Goal

The `enjoythings-secret` Secret exists in the `enjoythings` namespace with your
credentials, created once by hand, so the chart does not have to render it from
values that would otherwise be committed to git.

### Steps

```sh
kubectl create namespace enjoythings

PG_PASSWORD=$(openssl rand -hex 16)
FRAUD_PASSWORD=$(openssl rand -hex 16)

kubectl create secret generic enjoythings-secret -n enjoythings \
  --from-literal=JWT_SECRET="$(openssl rand -hex 32)" \
  --from-literal=POSTGRES_PASSWORD="$PG_PASSWORD" \
  --from-literal=FRAUD_POSTGRES_PASSWORD="$FRAUD_PASSWORD" \
  --from-literal=DATABASE_URL="postgres://enjoythings:$PG_PASSWORD@postgres:5432/enjoythings?sslmode=disable" \
  --from-literal=FRAUD_DATABASE_URL="postgres://fraud_worker:$FRAUD_PASSWORD@fraud-timescaledb:5432/fraud_audit?sslmode=disable" \
  --from-literal=LOCAL_LLM_API_KEY="<api-key-or-anything-for-ollama>"
```

Keep the JWT secret somewhere you can find it; section 7 needs it to mint a
token:

```sh
kubectl get secret enjoythings-secret -n enjoythings -o jsonpath='{.data.JWT_SECRET}' | base64 -d
```

### Why

- With Helm from your laptop, `values-secrets.yaml` supplied the credentials
  and stayed off git because it never left your disk. With Argo CD every input
  comes from git, so that file cannot be used without committing it, which the
  repository rules forbid.
- The chart has a switch for exactly this: `secrets.create: false` skips
  rendering the Secret and expects one with the same name and keys to already
  exist. The Application manifest in section 6 sets it. The six keys are the
  ones every Deployment reads through `envFrom`, plus the two the database
  containers read for their own password.
- Argo CD only manages resources it created, so it neither prunes nor reports
  drift on this Secret. It is invisible to GitOps by design.
- The database passwords appear twice, once as the container's own password
  and once inside the connection URL. Building both from one shell variable
  is how the command above guarantees they match.
- This is the simplest safe pattern and is fine for one cluster. Section 11
  names the tools that automate it when there are several.

### Check

```sh
kubectl get secret enjoythings-secret -n enjoythings -o jsonpath='{.data}' | jq 'keys'
```

Prints the six key names.

---

## 6. Create the Application

### Goal

Argo CD knows about the chart and performs the first sync.

### Steps

```sh
kubectl apply -f services/k8s/argocd/application.yaml
argocd app get enjoythings
```

### Why

Every field of that manifest is a decision. Reading it top to bottom:

| Field | Value | Why |
| --- | --- | --- |
| `metadata.namespace` | `argocd` | Applications are read by the controller in the `argocd` namespace, so they live there, not next to the workloads. |
| `finalizers` | `resources-finalizer` | Deleting the Application then also deletes what it deployed. Without it, `kubectl delete application` orphans the workloads. |
| `spec.project` | `default` | The permissive built-in project. Section 11 covers locking this down. |
| `source.repoURL`, `targetRevision`, `path` | GitHub repo, `master`, `services/charts/enjoythings` | Where the chart is. Argo CD detects a Helm chart by the presence of `Chart.yaml`. |
| `source.helm.valueFiles` | `values-local.yaml` | Same file the Helm runbook used, resolved relative to `path`. |
| `source.helm.parameters` | `secrets.create=false` | The switch from section 5. Equivalent to `--set` on the command line. |
| `destination.server` | `https://kubernetes.default.svc` | The cluster Argo CD runs in. Argo CD can also deploy to other clusters it has credentials for. |
| `destination.namespace` | `enjoythings` | Where the resources go. |
| `syncPolicy.automated.prune` | `true` | Remove resources that leave git. Off by default because it deletes things. |
| `syncPolicy.automated.selfHeal` | `true` | Undo `kubectl` edits. Without it, drift is only reported. |
| `syncOptions` | `CreateNamespace=true` | Create the destination namespace if missing. Already done in section 5, harmless here. |

Two things about how Argo CD runs Helm deserve to be said explicitly:

- **Argo CD does not run `helm install`.** It runs `helm template` in the repo
  server and applies the resulting YAML with its own diffing engine. There is
  no Helm release object in the cluster; `helm list` shows nothing. Rollback,
  history and values inspection all move to Argo CD.
- **Helm hooks are translated, not lost.** The chart's `kafka-topic-init` Job is
  annotated `helm.sh/hook: post-install,post-upgrade`. Argo CD maps that to a
  `PostSync` hook and `before-hook-creation` to `BeforeHookCreation`, so the Job
  still runs after every sync, after the other resources are healthy, and is
  recreated each time.

### Check

`argocd app get enjoythings` shows `Sync Status: Synced` within a minute or
two and `Health Status: Progressing` turning to `Healthy` as pods come up. The
UI shows a tree of every resource under the Application.

---

## 7. Watch the first sync and verify the platform

### Goal

The same end-to-end proof as section 8 of the Kubernetes runbook, now for a
deployment nobody ran by hand.

### Steps

```sh
argocd app wait enjoythings --health --timeout 600
kubectl get pods -n enjoythings
kubectl get jobs -n enjoythings

curl -i http://localhost:18080/healthz
curl -i http://localhost:18080/readyz

cd services
export JWT_SECRET="$(kubectl get secret enjoythings-secret -n enjoythings -o jsonpath='{.data.JWT_SECRET}' | base64 -d)"
JWT=$(go run ./cmd/devtoken -user-id 11111111-1111-1111-1111-111111111111 -role user -ttl 1h)
curl -s -X POST http://localhost:18080/v1/wallets \
  -H "Authorization: Bearer $JWT" -H "Content-Type: application/json" \
  -d '{"currency":"USD"}' | jq
```

For the full smoke test, follow step 8c of the Kubernetes runbook with the
`DATABASE_URL` password taken from the Secret.

### Why

The workloads are identical to the Helm install, so the same checks apply. The
one new thing to look at is the Application view in the UI: click any resource
to see its live manifest, its desired manifest from git, the diff between them,
and its events and logs. That single screen replaces most of the `kubectl
describe` and `kubectl logs` commands from the Kubernetes runbook.

### Check

Health endpoints return `200`, the wallet is created, and
`argocd app get enjoythings` reports `Synced` and `Healthy`.

---

## 8. Everyday GitOps operations

### Goal

Change the platform the GitOps way, see what happens when you do not, and get
out of trouble.

### Steps and why

**8a. Change configuration.** Edit a value in git, commit, push.

```sh
# example: raise the gateway rate limit burst
cat >> services/charts/enjoythings/values-local.yaml <<'EOF'

config:
  rateLimitBurst: "1200"
EOF
git add services/charts/enjoythings/values-local.yaml
git commit -m "chore(k8s): raise gateway rate limit burst"
git push

argocd app get enjoythings --refresh     # skip the 3-minute poll
argocd app wait enjoythings --sync
```

Argo CD sees the commit, renders a new ConfigMap, and applies it. As with Helm,
**a ConfigMap change does not restart pods**. Argo CD does not add that
behavior. Restart the readers of the changed key:

```sh
kubectl rollout restart deployment/gateway -n enjoythings
```

The restart adds a timestamp annotation to the Deployment, so Argo CD briefly
shows `OutOfSync`, then self-heals by removing it. The pods are already
restarted by then. It is noisy but harmless.

**8b. See self-heal in action.** Make a change with `kubectl` and watch it get
undone.

```sh
kubectl scale deployment/wallet --replicas=3 -n enjoythings
kubectl get deployment wallet -n enjoythings -w
```

Within a few seconds the replica count returns to `1`, because git says `1`.
This is the central idea of GitOps: the cluster is a projection of git, and
anything else is drift. To really scale wallet, change `replicas` under
`applications.wallet` in a values file and push.

**8c. Ship a new image.** Build with a unique tag, load it, and change the tag
in git.

```sh
cd services
TAG=$(git rev-parse --short HEAD)
docker build --build-arg SERVICE=wallet -t enjoythings/wallet:$TAG .
kind load docker-image enjoythings/wallet:$TAG --name enjoythings

sed -i '' "s#enjoythings/wallet:.*#enjoythings/wallet:$TAG#" charts/enjoythings/values-local.yaml
git add charts/enjoythings/values-local.yaml
git commit -m "deploy(wallet): $TAG"
git push
argocd app get enjoythings --refresh
```

The unique tag matters more here than with Helm. A fixed `:local` tag looks
unchanged to Argo CD, so nothing syncs and nothing restarts. The tag in git
*is* the deployment record: `git log` on the values file tells you what ran
when. In a real cluster, CI does the build, push and tag bump.

**8d. Compare, pause, resume.**

```sh
argocd app diff enjoythings                       # what would change on the next sync
argocd app set enjoythings --sync-policy none     # stop automated sync (for an incident)
argocd app sync enjoythings                       # one manual sync
argocd app set enjoythings --sync-policy automated --self-heal --auto-prune
```

Pausing is how you make a temporary `kubectl` fix survive during an incident.
Remember to resume, or the cluster drifts silently.

**8e. Roll back.** Revert the commit and push.

```sh
git revert HEAD
git push
```

Argo CD also has `argocd app history` and `argocd app rollback`, but it refuses
to roll back while automated sync is on, and a rollback that is not in git is
undone by the next commit anyway. `git revert` keeps history honest and needs
no special permission.

**8f. Deploy a branch for testing.** Point the Application at another revision.

```sh
argocd app set enjoythings --revision feature/my-branch
```

This edits the Application in the cluster, so the copy in
`services/k8s/argocd/application.yaml` is now stale. For anything longer than
a quick test, change `targetRevision` in that file and apply it, or better,
create a second Application with its own namespace for the branch.

**8g. Look at things.**

```sh
argocd app get enjoythings
argocd app resources enjoythings
argocd app logs enjoythings --kind Deployment --name wallet --tail 50
argocd app manifests enjoythings | less          # what git renders right now
kubectl get application enjoythings -n argocd -o yaml | less
```

---

## 9. Troubleshooting

Start with `argocd app get enjoythings` and match the status.

| Symptom | Meaning | What to do |
| --- | --- | --- |
| `Sync Status: Unknown`, `ComparisonError` | Argo CD cannot render the source. | Read the message in `argocd app get`. Usual causes: typo in `path` or `targetRevision`, the branch does not contain `Chart.yaml` at that path, or the chart change is not pushed yet. `kubectl logs deploy/argocd-repo-server -n argocd` has the Helm error. |
| You pushed but nothing happens | Poll interval is 3 minutes, or you pushed to a different branch than `targetRevision`. | `argocd app get enjoythings --refresh`. Check `git branch --show-current`. |
| `Synced` but `Health: Degraded` or `Progressing` forever | Resources applied but pods not ready. | Same as the Kubernetes runbook section 11: `kubectl get pods -n enjoythings`, then `describe` and `logs`. |
| Pods in `CreateContainerConfigError` | A referenced Secret or ConfigMap key is missing. | The Secret from section 5 does not exist or lacks a key. `kubectl describe pod` names the key. Recreate it. |
| Pods in `ImagePullBackOff` | Image tag in git is not loaded into kind. | Kubernetes runbook section 4, with the tag that git currently names. |
| PostSync hook `kafka-topic-init` fails | Kafka unreachable. | `kubectl logs job/kafka-topic-init -n enjoythings`. Fix Kafka; retry with `argocd app sync enjoythings`. |
| `OutOfSync` that keeps coming back | Something in the cluster rewrites a field after every sync. | `argocd app diff enjoythings` shows the field. Either stop the thing rewriting it or add an `ignoreDifferences` entry to the Application. |
| `rollback cannot be initiated when auto-sync is enabled` | Expected. | Use `git revert` (8e) or disable automated sync first (8d). |
| `argocd` CLI: `Unauthenticated` | Session token expired. | `argocd login localhost:8443 --insecure` again. Make sure the port-forward is running. |
| Browser: connection refused on 8443 | Port-forward not running. | Restart `kubectl port-forward svc/argocd-server -n argocd 8443:443`. |
| Deleting the Application hangs | Finalizer waiting for resources to be deleted, and one is stuck. | `kubectl get all -n enjoythings` to find it. As a last resort remove the finalizer with `kubectl patch`. |

---

## 10. Cleanup

```sh
kubectl delete -f services/k8s/argocd/application.yaml   # removes the app and, via the finalizer, every workload
kubectl delete namespace enjoythings                      # removes the hand-made Secret too
kubectl delete namespace argocd                            # removes Argo CD itself
kind delete cluster --name enjoythings
```

Deleting the Application first matters. Deleting the `enjoythings` namespace
while the Application still exists makes Argo CD recreate everything, because
git still says it should be there.

---

## 11. Toward a real cluster

Everything in section 13 of the Kubernetes runbook still applies. These are the
Argo CD specific additions, in the order they usually become necessary.

1. **Pin the Argo CD version and use the HA manifests.** Replace `stable` with
   a tagged URL and `manifests/install.yaml` with `manifests/ha/install.yaml`,
   or install Argo CD with its own Helm chart, managed by itself.

2. **One Application per environment.** `dev`, `staging` and `prod` become
   three Applications with different `targetRevision` or `valueFiles` and
   different destination namespaces or clusters. When they multiply, an
   `ApplicationSet` generates them from a list, and an "app of apps" root
   Application deploys the Applications themselves from git.

3. **Replace the `default` project.** An `AppProject` restricts which repos an
   Application may pull from, which namespaces and clusters it may deploy to,
   and which resource kinds it may create. Combine with Argo CD RBAC so that
   developers can view and sync but not edit destinations.

4. **Single sign-on.** The bundled Dex connects Argo CD to GitHub, Google or an
   OIDC provider. Delete the local `admin` account once SSO works.

5. **Automate the Secret.** Section 5 is manual by design. External Secrets
   Operator pulls from a cloud secret manager into `enjoythings-secret` on a
   schedule; Sealed Secrets lets you commit an encrypted Secret that only the
   cluster can decrypt. Either way `secrets.create` stays `false`.

6. **Let CI bump image tags.** The build pipeline pushes `service:<sha>` to a
   registry and commits the new tag to the values file of the target
   environment. Argo CD Image Updater can do the commit for you.

7. **Notifications.** The bundled notifications controller posts sync results
   and health changes to Slack or GitHub commit statuses.

8. **Sync windows.** Block automated syncs to `prod` outside business hours,
   or during a freeze, with a `syncWindows` entry on the AppProject.

Sections 2, 5 and 6 of this runbook are what
[`terraform-runbook.md`](./terraform-runbook.md) automates.
