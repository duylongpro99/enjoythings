# Drills Framework — Slice 3 Implementation Plan

> **Status: not started.** Design: `docs/superpowers/specs/2026-08-25-drills-framework-design.md`.
> Prior slices: `docs/superpowers/plans/2026-09-06-drills-framework-slice1.md`,
> `docs/superpowers/plans/2026-09-13-drills-framework-slice2.md`.

> **For agentic workers:** Steps use checkbox (`- [ ]`) syntax for tracking. Each task ends with the command that proves it.

**Goal:** Close the last two target-adapter gaps from spec §9 so the drills can inject
*network* and *dependency* faults. Ship: Toxiproxy in the stack and the `net.latency` /
`net.partition` primitives; a containerised chaos LLM endpoint and the `dep.replace
llm-endpoint <profile>` primitive; the one piece of probe tooling the LLM scenario needs
(`drillmetric`, a black-box metrics assertion); and one proving scenario per primitive.
After this slice all nine §5 primitives are honoured by the enjoythings adapter.

**Decisions carried from spec §13 / slices 1–2:** target-specific scenarios; Author drafts
the rubric, human merges; one active drill (lock); black-box probes only; constant `steady`
load; teardown closes the run record and drops the lock **before** reset.

**Deferred to later work (unchanged):** `drill sync-commands` emitters for agents other than
Claude Code; the L4 "no clean fix" and multi-engineer richness from spec §13. The remaining
silent-fault library scenarios (`docs/superpowers/plans/2026-09-12-drills-library-scenarios.md`
"Deferred") become buildable once `drillmetric` lands here, but growing the library is a
separate plan.

---

## Design decisions forced by the stack (read before Task 1)

**N1 — Toxiproxy intercepts an edge only by re-pointing the client.** The Compose stack uses
no named networks; every service reaches its peers by service name on the default project
network (`services/docker-compose.yml`). Toxiproxy therefore cannot transparently sit on an
edge — the client `a` must be reconfigured to dial `toxiproxy:<port>`, which forwards to the
real upstream `b`. So `net.*` is only supportable on edges whose client URL is an environment
variable. The adapter ships a small **edge table** of exactly those edges; an unsupported
pair fails at inject time (and, because a scenario's fault line is validated at `start`, the
drill refuses to boot rather than half-injecting). Edges available today (all env-configurable
in `docker-compose.yml`):

| Edge `a → b` | Client env var | Real upstream | Proxy name / listen port |
| --- | --- | --- | --- |
| `payment-processor → stub-payment-rail` | `PAYMENT_RAIL_URL` (`http://stub-payment-rail:18090`) | `stub-payment-rail:18090` | `pp-rail` / `18190` |
| `fraud-worker → ledger` | `LEDGER_GRPC_ADDR` (`ledger:9091`) | `ledger:9091` | `fw-ledger` / `19091` |
| `fraud-worker → verification` | `VERIFICATION_GRPC_ADDR` (`verification:9094`) | `verification:9094` | `fw-verif` / `19094` |

The proving scenario uses the first edge (clean HTTP, deterministic against
`PAYMENT_RAIL_TIMEOUT=2s`). The other two are shipped in the table (Toxiproxy proxies raw TCP,
so gRPC works) but need no scenario yet.

**N2 — `net.partition` severs by disabling the proxy, not by a toxic.** Spec §5 defines
partition as "sever one edge while both stay healthy" (vs. `proc.pause`, which yields
timeouts). Disabling the Toxiproxy proxy drops the connection while both containers stay
`healthy` — exactly the intended semantics. `net.latency` adds a `latency` toxic to a proxy
that stays enabled.

**N3 — the chaos LLM only affects `fraud-worker`.** `LLM_PROVIDERS_JSON` is consumed by the
fraud worker alone (`services/docker-compose.yml:347`), so `dep.replace llm-endpoint` is
inherently fraud-scoped: bring up the chaos container, then `env.set` the worker's
`LLM_PROVIDERS_JSON` / `LLM_DEFAULT_PROVIDER` to point at it and recreate the worker.

**N4 — the chaos LLM reuses the existing image.** The root project (`pyproject.toml`,
`fastapi-chat-stream`) already depends on `uvicorn` and `fastapi`, and `tests/fraud/fake_provider.py`
already implements a scripted OpenAI-compatible SSE server (`FakeProviderServer`). The chaos
endpoint is that server, made standalone and env-driven (latency / error-rate / truncation),
built from the same `app/fraud/Dockerfile` base so no new toolchain enters the repo.

**N5 — drill-only containers are torn down by the existing `--remove-orphans`.** `reset` and
`down` already run `compose_with_loadgen down -v --remove-orphans`, which removes any container
in the `services` project that is not in the passed compose files — this already collects
`toxiproxy` and `chaos-llm`. `revert` additionally stops them explicitly via recorded revert
lines, so a drill that ends with `revert` (not full teardown) also leaves no drill container
running.

**N6 — `net.*` and `dep.replace` are Tier A.** No `code.patch`, so no sealed tree:
`DRILL_BUILD_ROOT` is the main checkout and slice-2 sealing is untouched. These primitives flow
through the existing `start → inject <each fault line>` path with no `drill` CLI state-machine
change; the CLI work in this slice is limited to the `net.partition` "both stay healthy"
assertion in `health`/probes and doc/help text.

---

### Task 1: Toxiproxy in the stack + `net.latency` / `net.partition` primitives

**Files:**
- Create: `drills/toxics/docker-compose.toxiproxy.yml` (the `toxiproxy` service overlay), `drills/toxics/README.md`
- Modify: `drills/targets/enjoythings/lib.sh` (a `compose_with_toxiproxy` helper + an `edge_lookup` table)
- Modify: `drills/targets/enjoythings/inject` (`net.latency`, `net.partition`, and internal `net.clear` cases)
- Modify: `drills/targets/enjoythings/target.yaml` (add `net.latency`, `net.partition` to `primitives`; add `toxiproxy` component)

**Approach:**
- **Overlay:** `toxiproxy` service from `ghcr.io/shopify/toxiproxy` (pinned tag), command default,
  admin API on `8474`, joined to the `services` project network by being brought up in that
  project. Expose `${TOXIPROXY_ADMIN_PORT:-8474}:8474` for the engineer's own inspection.
  No proxies are pre-declared; the adapter creates them per injection via the admin API.
- **`lib.sh`:** add `compose_with_toxiproxy()` (mirrors `compose_with_loadgen`, layering the
  toxiproxy overlay) and an `edge_lookup <a> <b>` function returning `CLIENT_ENV PROXY_NAME
  LISTEN_PORT UPSTREAM` from the N1 table, or `die` on an unknown edge.
- **`inject net.latency <a> <b> <ms>`:**
  1. `edge_lookup "$a" "$b"` → resolve client env var, proxy name, listen port, upstream.
  2. `compose_with_toxiproxy up -d toxiproxy` (idempotent; leaves it running for the drill).
  3. Create the proxy: `compose exec -T toxiproxy /toxiproxy-cli create -l 0.0.0.0:<port> -u <upstream> <name>` (ignore "already exists").
  4. Add the toxic: `/toxiproxy-cli toxic add -t latency -a latency=<ms> <name>`.
  5. Re-point the client: write an env override (reuse the `env.set` mechanism) setting the
     client env var to `<name-listen-host>:<port>` = `toxiproxy:<port>` (HTTP edges get the
     scheme, e.g. `http://toxiproxy:18190`; gRPC edges get `toxiproxy:19091`), then
     `compose up -d --no-deps --force-recreate <a>`.
  6. `record_revert "env.unset <a>"` and `record_revert "net.clear <name>"`.
- **`inject net.partition <a> <b>`:** same as steps 1–3, 5–6, but instead of a toxic run
  `/toxiproxy-cli toggle <name>` (disable) so the edge is severed while both endpoints stay
  healthy (N2). Record the same reverts.
- **`inject net.clear <name>` (internal):** `/toxiproxy-cli delete <name>` (tolerate absent).
  Reverting the paired `env.unset <a>` restores the real upstream URL.
- **`known_component` guard:** `net.*` validates both `a` and `b` are known components before
  `edge_lookup`, so a typo dies with the standard message.

- [ ] Toxiproxy overlay added; `toxiproxy` listed as a component in `target.yaml`; `net.latency`, `net.partition` added to `primitives`.
- [ ] `net.latency` creates a proxy, adds a `latency` toxic, and re-points the client; both revert lines recorded.
- [ ] `net.partition` creates a disabled proxy and re-points the client; both stay `healthy`.
- [ ] `revert` deletes the proxy and restores the client env, in order.
- [ ] **Prove:** `drills/bin/drill target validate enjoythings` lists `net.latency`/`net.partition`; the round-trip is covered by the fake-target assertions in Task 5.

---

### Task 2: Chaos LLM endpoint + `dep.replace llm-endpoint <profile>`

**Files:**
- Create: `app/fraud/chaosllm/__init__.py`, `app/fraud/chaosllm/server.py` (env-driven standalone server reusing `FakeProviderServer` scaffolding), `app/fraud/chaosllm/server_test.py`
- Modify: `pyproject.toml` (add a `chaos-llm` console script → `app/fraud/chaosllm/server.py:main`)
- Create: `drills/chaosllm/docker-compose.chaosllm.yml` (the `chaos-llm` service, built from `app/fraud/Dockerfile`, entrypoint `uv run chaos-llm`), `drills/chaosllm/README.md`
- Modify: `drills/targets/enjoythings/lib.sh` (`compose_with_chaosllm` helper)
- Modify: `drills/targets/enjoythings/inject` (`dep.replace`, internal `dep.restore`)
- Modify: `drills/targets/enjoythings/target.yaml` (add `dep.replace` to `primitives`; add `llm-endpoint` component)

**Approach:**
- **Server:** lift `ScriptedProvider` / SSE encoding from `tests/fraud/fake_provider.py` into a
  standalone `server.py` that serves `POST /{provider}/v1/chat/completions` and reads profile
  knobs from the environment:
  - `CHAOS_LLM_PROFILE` ∈ `healthy | slow | errors | truncate | flaky` (sets the defaults below).
  - `CHAOS_LLM_LATENCY_MS` — `asyncio.sleep` before responding (default per profile; `slow` = 8000, above the worker's provider `timeout_seconds`).
  - `CHAOS_LLM_ERROR_RATE` — fraction of calls answered `500` (`errors` = 1.0, `flaky` = 0.5).
  - `CHAOS_LLM_TRUNCATE` — emit a partial SSE stream with no `[DONE]` (`truncate` = on).
  - `CHAOS_LLM_PORT` (default `18091`), served by `uvicorn` on `0.0.0.0`.
  A benign scripted verdict is returned on the healthy path so `healthy` is a working control.
  Keep `tests/fraud/fake_provider.py` as-is (its in-process ephemeral-port form still serves the
  fraud e2e tests); share only the pure helpers to avoid a second SSE implementation drifting.
- **Overlay:** `chaos-llm` built from `app/fraud/Dockerfile` (context `..`), env from the
  profile, healthcheck hitting its own port, exposed on `${CHAOS_LLM_PORT:-18091}` for the
  engineer.
- **`inject dep.replace llm-endpoint <profile>`:**
  1. Validate `<profile>` against the known set; `die` otherwise.
  2. `CHAOS_LLM_PROFILE=<profile> compose_with_chaosllm up -d --build chaos-llm`.
  3. `env.set fraud-worker LLM_PROVIDERS_JSON='{"providers":[{"id":"chaos","driver_type":"openai_compatible","base_url":"http://chaos-llm:18091/chaos/v1","api_key_env":"LOCAL_LLM_API_KEY","model":"chaos-model","timeout_seconds":5}]}'` and `env.set fraud-worker LLM_DEFAULT_PROVIDER=chaos` (reusing the existing `env.set` override + recreate path, which records `env.unset fraud-worker`).
  4. `record_revert "dep.restore llm-endpoint"`.
  - Only `llm-endpoint` is a replaceable dependency in this slice; any other name dies (spec
    §5 lists `stub-payment-rail` too, but the `slow` rail variant is out of scope here — note
    it in the README as the next `dep.replace` target).
- **`inject dep.restore llm-endpoint` (internal):** `compose_with_chaosllm rm -sf chaos-llm`
  (tolerate absent). The paired `env.unset fraud-worker` restores the real provider registry.

- [ ] `chaos-llm` server serves scripted SSE and honours `slow`/`errors`/`truncate`/`flaky`/`healthy` from the environment; `server_test.py` covers each profile.
- [ ] `chaos-llm` builds from the fraud image; `dep.replace` in `target.yaml`, `llm-endpoint` a component.
- [ ] `dep.replace llm-endpoint <profile>` brings the container up and repoints the worker; revert stops it and restores the registry.
- [ ] **Prove:** `uv run pytest app/fraud/chaosllm/server_test.py`; `drills/bin/drill target validate enjoythings` lists `dep.replace`.

---

### Task 3: `drillmetric` — black-box metrics assertion

**Files:**
- Create: `services/devtools/drillmetric/main.go`, `services/devtools/drillmetric/main_test.go`

**Approach:** the LLM scenario's symptom (fraud scoring silently falls open) is invisible in a
saga state — it shows only in `fraud_transactions_scored_total{action}` on the worker's
`/metrics` (host `9101`). `drillmetric` reads a Prometheus-text metrics endpoint, sums a named
counter (optionally filtered by a label matcher), and asserts a **delta over a window**:

```
drillmetric -url http://localhost:9101/metrics \
  -metric fraud_transactions_scored_total -match action=fail_open \
  -min-delta 1 -within 60s      # symptom present: fail-open scoring is climbing
drillmetric ... -match action=allow -min-delta 5 -within 60s   # symptom gone: real scoring resumed
```

- Parse only the counter lines it needs (no full Prometheus client dependency); the parser and
  the delta logic are the unit-tested core, with `-url` fetched via `net/http`.
- `-within` polls until the delta is met or the window elapses (settle mode), mirroring
  `drillprobe`'s window semantics for consistency.
- Reads the target's *own* production observability (the `/metrics` every service exposes), so
  it stays within the black-box contract (Author contract #3) — documented as such.

- [ ] Parser sums a counter with an optional single-label matcher; unit-tested against sample Prometheus text.
- [ ] `-within` delta assertion polls to a deadline; `-min-delta` and `-match` behave as documented.
- [ ] **Prove:** `go -C services test ./devtools/drillmetric/` and `go -C services vet ./...`.

---

### Task 4: Proving scenarios — `payment-rail-latency` (net) and `fraud-scoring-degraded` (dep)

**Files:**
- Create `drills/scenarios/payment-rail-latency/{brief.md,scenario.yaml,fault.yaml,hints.md,rubric.md,solution.md,probes/break,probes/fix}`
- Create `drills/scenarios/fraud-scoring-degraded/{brief.md,scenario.yaml,fault.yaml,hints.md,rubric.md,solution.md,probes/break,probes/fix}`

**`payment-rail-latency` (L2, Tier A, `net.latency`):**
- `fault.yaml`: `- net.latency payment-processor stub-payment-rail 6000`. Charges exceed
  `PAYMENT_RAIL_TIMEOUT=2s`, so payments fail the rail call and the saga does not settle —
  deterministic, black-box via `drillprobe` on saga state.
- `scenario.yaml`: `target: enjoythings`, `level: L2`, `tier: A`, `load: steady`,
  `components: [payment-processor, stub-payment-rail]`, `break_probe_attempts: 3`.
- `probes/break`: `drillprobe -want COMPLETED -after 25s -count 1` **negated** — i.e. a fresh
  transfer does *not* reach `COMPLETED` in the window (assert the observed non-settling terminal
  state directly, decided against the live stack per the library-plan pattern: rail timeout may
  land the saga in `FAILED` or leave it processing — the break probe asserts whichever the stack
  actually produces).
- `probes/fix`: `drillprobe -want COMPLETED -within 30s -count 10` — under load, ten transfers
  settle once the edge latency is removed.
- `hints.md`: T1 gateway healthy, every service `up`; T2 a payment trace shows the rail span
  timing out though the rail container is healthy; T3 the network edge to the rail is slow, not
  the rail itself. `solution.md`: reference fix removes the latency (drop the toxic); the
  trade-off is whether to raise `PAYMENT_RAIL_TIMEOUT` (tolerate a slow rail, longer holds) vs.
  fail fast and compensate; blast radius = held/failed transfers during the window.

**`fraud-scoring-degraded` (L3, Tier A, `dep.replace`):**
- `fault.yaml`: `- dep.replace llm-endpoint slow`. The worker's model calls exceed
  `timeout_seconds` and fall open (`graph.py` `fail_open`), so scoring silently stops while
  payments keep settling.
- `scenario.yaml`: `target: enjoythings`, `level: L3`, `tier: A`, `load: steady`,
  `components: [fraud-worker, llm-endpoint]`, `break_probe_attempts: 3`.
- `probes/break`: `drillmetric -url http://localhost:9101/metrics -metric fraud_transactions_scored_total -match action=fail_open -min-delta 1 -within 60s` — fail-open scoring is climbing.
- `probes/fix`: `drillmetric ... -match action=allow -min-delta 3 -within 90s` (or the
  real allow/review action observed) — genuine scoring resumes after restore.
- `brief.md`: "payments look fine; risk is quiet" — the point is *noticing* scoring stopped
  (spec §5). `hints.md`: T1 saga health is green, look past the money path; T2 the fraud
  dashboard's scored-rate dropped while volume held; T3 the model provider is timing out and the
  worker is failing open. `solution.md`: fault statement, first signal
  (`rate(fraud_transactions_scored_total{action="fail_open"}[1m])` climbing while
  `fraud_model_latency_seconds` p95 pins at the timeout); reference fix restores a healthy
  provider; trade-off = fail-open is *correct* (don't block payments on a model outage) so the
  real fix is detection + alerting, not blocking. Note openly that this scenario is the first
  to grade "did the engineer notice a silent degradation", enabled by `drillmetric`.

**Author verification (required before merge, per §7):** run break + fix probes against the live
stack for both; adjust the exact observed states/actions as the library plan prescribes. Record
"found while proving" notes in this plan.

- [ ] Both scenario directories created; `fault.yaml` lines use only now-supported primitives.
- [ ] Probes executable and black-box (`drillprobe` / `drillmetric` only).
- [ ] `drills/bin/drill scenario validate payment-rail-latency` and `… fraud-scoring-degraded` pass.
- [ ] **Prove (pending, needs a live stack):** a full `drill start … / drill end` cycle for each with both probes flipping.

---

### Task 5: State-machine + adapter coverage in `drill_test.sh`

**Files:** Modify: `drills/bin/drill_test.sh`

**Approach:** extend the fake-target harness (no Docker) so the new inject/revert round-trips
are proven without the real stack. The fake adapter's `inject` records each call to a log and
its `revert` replays in reverse (as slice 1/2 do); add:
- A fake `net.latency`/`net.partition`/`net.clear` path that appends to the call log and a fake
  `dep.replace`/`dep.restore` path likewise, so the harness asserts: (a) a scenario whose
  `fault.yaml` names `net.latency` boots (fault line injected, break probe passes), (b) `revert`
  emits the matching `net.clear` + `env.unset` in reverse order, (c) same for `dep.replace` →
  `dep.restore` + `env.unset`.
- Reuse the existing fake-scenario scaffolding; keep every slice-1/2 assertion green (Tier A
  `demo`, sealed `codebug`, unsealed, refusal, debrief).

- [ ] Fake net/dep primitives added; inject-at-start and reverse-order-revert asserted for both.
- [ ] All prior assertions untouched and passing.
- [ ] **Prove:** `sh drills/bin/drill_test.sh` (all pass, `0 failed`).

---

### Task 6: Docs, roles/commands, wiring

**Files:**
- Modify: `docs/superpowers/specs/2026-08-25-drills-framework-design.md` (§5 mark `net.*` / `dep.replace` implemented; §9 mark Toxiproxy + chaos LLM landed; §13 add a "Settled 2026-09-13 (slice 3)" row)
- Modify: `drills/roles/instructor.md`, `drills/roles/executor.md` (network/dependency scenarios: where the engineer looks, that the fix is removing the toxic / restoring the provider, that `dep.replace` is fraud-scoped)
- Modify: `drills/commands/drill-observe.md` if present, else `drill-start.md` (mention the Toxiproxy admin port and the chaos-LLM port among what the engineer may inspect); regenerate `.claude/commands/drill-*.md`
- Modify: `drills/targets/enjoythings/observe` (list Toxiproxy admin `:8474` and the chaos-LLM port under "what the engineer may look at")
- Modify: `drills/README.md` (a "Network and dependency faults" subsection; list the two new scenarios with level + one-line symptom), root `README.md` if it enumerates scenarios
- Modify: `.gitignore` if the chaos/toxics overlays produce scratch (they should not — no new ignored paths expected)

**Approach:** keep the spec the single source of truth; the slice's mechanical changes are the
overlays + inject cases; the judgement changes are the two role notes. Regenerated shims stay
byte-identical to the canonical command bodies.

- [ ] Spec §5/§9/§13 updated; this plan referenced.
- [ ] Roles/commands/observe describe the network + dependency faults and the inspection ports.
- [ ] README lists all six scenarios; `.gitignore` reviewed.
- [ ] **Prove:** `drills/bin/drill sync-commands && git diff --exit-code .claude/commands`.

---

## Riskiest / most uncertain decisions

1. **Toxiproxy needs the client re-pointed, so `net.*` is edge-table-bound.** The stack has no
   named networks (N1), so there is no transparent interception — an edge is only faultable if
   its client URL is an env var. The edge table is the honest surface of that constraint; a
   scenario naming an unsupported edge must fail validation, not silently no-op. If a future
   scenario needs an edge with a hardcoded address, that address must first become configurable
   in `docker-compose.yml`.
2. **The `net.partition` semantics (`toggle`/disable) vs. a `timeout` toxic.** Disabling the
   proxy severs cleanly while both containers stay healthy (the spec's definition), but produces
   connection *refusals*, not *timeouts*; if a scenario needs "hangs, both healthy" that is a
   `latency` toxic with a huge delay or a `timeout` toxic, not `net.partition`. Documented so an
   Author picks the right primitive.
3. **The chaos-LLM symptom is only visible in metrics.** `dep.replace llm-endpoint slow` yields a
   *silent* degradation (fail-open is the correct behaviour), so the whole scenario rests on
   `drillmetric` reading `fraud_transactions_scored_total`. If the worker does not emit a
   distinct `fail_open` action label under a provider timeout (to be confirmed against the live
   stack), the probe must key off `fraud_model_latency_seconds` pinning at the timeout instead —
   the fallback is noted in `solution.md`.
4. **`fail_open` requires real fraud sessions.** The scenario only breaks if `steady` load
   actually drives fraud scoring; if loadgen traffic does not trigger fraud sessions, the
   scenario needs a load tweak or a targeted probe-driven trigger (confirm during Author
   verification, as slice 1 did for the rate-limit storm).
5. **Drill-only containers must be cleaned.** `toxiproxy`/`chaos-llm` are brought up in the
   `services` project but live in separate overlays, so they are orphans to a plain `compose
   down`; teardown relies on the existing `--remove-orphans` (N5) and on `revert` stopping them.
   A reset that ever drops `--remove-orphans` would leak a chaos container into the next drill.
6. **Reusing `fake_provider.py` helpers without forking the SSE code.** The chaos server and the
   fraud e2e test server must share one SSE encoder or they drift; the plan shares the pure
   helpers and keeps the in-process test server's lifecycle separate.
