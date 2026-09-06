# Internal-gRPC Mutual TLS

Problem: Every internal gRPC hop — the gateway and saga orchestrator dialling
wallet, ledger, verification and each other, and the Python fraud worker
enriching over ledger and verification — ran on `insecure` credentials. The
`docs/phase4/specs/01-fraud-contracts-and-events.md` trusted-network assumption
was the only thing standing between an attacker on the pod network and every
balance, ledger entry, and KYC decision in the system. RS256 JWT verification
does not touch this: it authenticates the end user carried in a request, not the
service making the call. Anything that could reach a service port could speak to
it as if it were a peer.

Structure: Transport security lives behind one small package, `internal/mtls`,
shaped like `internal/auth` — a constructor per role and a single feature flag.
`ServerCredentials` presents the service's leaf certificate and sets
`RequireAndVerifyClientCert`, so a caller with no certificate signed by the
shared CA is refused during the handshake, before any RPC is dispatched.
`ClientCredentials` presents the same leaf as the caller's identity and verifies
the server against the CA; the name it checks comes from the dial target's host,
which is why the certificates carry the service's Compose/Kubernetes DNS name in
their SANs. Both fall back to `insecure` credentials when the flag is off.

The flag is `GRPC_TLS_ENABLED`, resolved by `loadMTLS` in `internal/config`
alongside the existing `loadJWT`. Off is the default, so local development, the
in-process test harnesses, and the current Compose stack are unchanged. On, all
three file paths — `GRPC_TLS_CERT_FILE`, `GRPC_TLS_KEY_FILE`, `GRPC_TLS_CA_FILE`
— are required together: a partial configuration is a misconfiguration that
fails at startup, not a silent downgrade to plaintext. The Python worker mirrors
this exactly under a `FRAUD_GRPC_TLS_` prefix, building
`grpc.ssl_channel_credentials` from the same three files.

Certificates are one local CA and a per-service leaf issued by
`services/devtools/gen-certs.sh` (`make certs`). Each leaf carries both
`serverAuth` and `clientAuth` usages because the saga orchestrator is a server
to the gateway and a client to wallet, ledger, and verification at once. The
material is git-ignored and never committed. Compose turns the feature on with
the `docker-compose.mtls.yml` overlay; the Helm chart turns it on with
`mtls.enabled=true`, mounting a `Secret` the operator creates from the same
generated files.

Tradeoffs: mTLS is off by default, which keeps the blast radius small and
matches the HS256-default choice made for JWTs, but it means the guarantee only
exists where an operator has explicitly turned it on — the safe default is the
insecure one. Certificate distribution is deliberately left to the operator: the
chart mounts an existing `Secret` rather than templating PEM through values,
because cert material does not belong in a values file and real deployments
issue it through cert-manager or a CA the chart should not assume. Rotation and
in-cluster issuance were left out of this step and landed later in Phase 5 —
see `phase5-cert-rotation.md`. The credentials pin TLS 1.3, which is safe for an
all-first-party mesh and avoids negotiating anything weaker.
