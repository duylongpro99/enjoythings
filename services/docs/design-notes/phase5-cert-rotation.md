# Certificate Rotation and Workload Identity

Problem: `phase5-mtls.md` closed the trusted-network hole but left the
certificates as a hand-run script and hand-mounted files. Each service read its
leaf and the CA once at startup into a static `tls.Config`, so a renewed
certificate on disk changed nothing until the process restarted: a leaf reaching
its `CERT_DAYS` horizon meant re-running the generator, rebuilding the Secret,
and restarting every pod before the handshakes started failing. Nothing issued
the certificates from inside the cluster either — the Kubernetes path was
`kubectl create secret` from a developer's laptop, which is neither a workload
identity nor something that renews on a schedule.

Structure: Rotation lives entirely inside `internal/mtls`; no caller changed.
`ServerCredentials` and `ClientCredentials` now return a `rotating` credential —
a `credentials.TransportCredentials` that, before every handshake, re-reads the
three files and compares their bytes to what it last loaded. Unchanged, it hands
the handshake to the credentials it already built. Changed, it parses the new
pair and CA, swaps them in, logs the new leaf's `not_after`, and serves the
handshake with them. A read or parse failure — the key rewritten before its
certificate, a half-written file, a path missing mid-swap — logs a warning and
serves the last good set; the next handshake tries again. Startup is unchanged:
the first load is eager and a bad file still fails fast.

The wrapper rebuilds `credentials.NewTLS` from a plain `tls.Config` rather than
using the `GetCertificate`/`GetClientCertificate` callbacks. The callbacks
rotate the leaf but not the trust pool: `ClientCAs` and `RootCAs` are fixed at
config time, and swapping them on the client means `InsecureSkipVerify` plus a
hand-rolled `VerifyConnection`, while on the server it means
`GetConfigForClient` reproducing gRPC's ALPN setup. Rebuilding the whole
credential keeps one mechanism for leaf and CA on both roles and leaves the
standard library's verification and gRPC's `h2`/authority handling exactly as
they were. Comparing content rather than modtime is deliberate too: Kubernetes
updates a mounted Secret by swapping a `..data` symlink, and byte comparison is
immune to that and to filesystem timestamp granularity.

Issuance moves into the chart behind `mtls.certManager.enabled`. For every gRPC
server (detected by `grpcPort`) and every name in `mtls.clients`,
`templates/certificates.yaml` renders a cert-manager `Certificate` whose SANs
are the service's DNS name in the four forms a peer might dial (`wallet`,
`wallet.<ns>`, `wallet.<ns>.svc`, `wallet.<ns>.svc.cluster.local`), with both
`server auth` and `client auth` usages because the orchestrator is both,
`duration`/`renewBefore` from values, and `rotationPolicy: Always` so a renewal
rotates the key as well. Each writes a `<service>-mtls` Secret in cert-manager's
`tls.crt`/`tls.key`/`ca.crt` layout; the Deployment mounts that Secret at the
same `mountPath` and points `*_CERT_FILE`/`*_KEY_FILE` at `tls.*` instead of
`<service>.*`. The hand-built shared Secret remains the path when
`certManager.enabled` is false. `bootstrapCA.enabled` adds a self-signed
`Issuer`, a CA `Certificate`, and a CA `Issuer` under `issuerRef.name`, so a
kind cluster needs nothing but cert-manager; a real deployment points
`issuerRef` at a CA it already operates. Compose needs nothing new: the bind
mount already exposes file changes, so re-running `make certs` against a
running stack rotates every leaf live.

The Python fraud worker is left as a restart-only path, deliberately.
grpc-python's `ssl_channel_credentials` takes PEM bytes and offers no reload
hook for client channels (only servers have `dynamic_ssl_server_credentials`),
so reload-on-handshake is not available; a SIGHUP handler would add
channel-swapping state to the worker without an automation story, since nothing
in Kubernetes sends it. The worker reads its files when the channel is created —
once per process — and the operational rule is a
`kubectl rollout restart deployment/fraud-worker` at any point in the
`renewBefore` window (30 of 90 days by default), during which the old leaf is
still valid. Any routine rollout satisfies it.

Tradeoffs: Reading three small files on every handshake is the cost of never
missing a change and never needing a watcher goroutine; gRPC connections are
long-lived, so handshakes are rare and the reads are a few kilobytes. It also
means an already-open connection keeps its old certificate until it reconnects —
the reload is per handshake, not per stream — which is correct: the old leaf is
valid until its expiry, and nothing needs to force a reconnect.

A persistent parse failure warns on every handshake rather than once. A broken
file on disk is an operator error to fix, and a single log line at rotation
time would be easy to miss.

The `Certificate` SANs are Service DNS names and the issuer is a per-cluster CA.
That is a workload identity in the sense that a pod's certificate now comes from
the cluster's PKI on a schedule, but it is not SPIFFE: there is no `spiffe://`
URI SAN, no trust-domain federation across clusters, and no mesh enforcing
policy on the identity. The chart also assumes a CA-type issuer that populates
`ca.crt` in the Secret; an issuer that does not (ACME, some external issuers)
needs the bundle delivered another way. Both remain in `docs/phase5/backlog.md`.
