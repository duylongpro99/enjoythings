# Drills Framework — Slice 1 Implementation Plan

> **Status: in progress.** Design: `docs/superpowers/specs/2026-08-25-drills-framework-design.md`.

> **For agentic workers:** Steps use checkbox (`- [ ]`) syntax for tracking. Each task ends with the command that proves it.

**Goal:** Ship the smallest vertical slice that runs one real drill end to end against the enjoythings stack: the portable `drill` CLI, the target adapter, a constant-rate load generator with Prometheus export, the three role playbooks, and one L1 Tier A scenario.

**Decisions settled on 2026-09-06** (spec §13):

| Question | Decision |
| --- | --- |
| Scenario portability | Target-specific. `scenario.yaml` keeps a `components` field for a future role mapping. |
| Rubric ownership | Author drafts the rubric with the scenario; nothing enters `drills/scenarios/` unmerged. Human review is the PR. |
| Load profile | Constant rate with a request mix. Bursts and diurnal shapes are deferred until a scenario needs them. |
| Multi-engineer | Single engineer. The run record has no participants list. |

**Deferred to later slices:** sealed history and `code.patch` (Tier B), Toxiproxy (`net.*`), the chaos LLM container (`dep.replace`), `drill sync-commands` emitters for agents other than Claude Code.

**Layout deviation from spec §12:** the load generator's Go source lives at `services/cmd/loadgen` and `services/internal/loadgen` because it imports `services/devtools/smoke` from the `enjoythings/services` module and reuses the services Dockerfile (`SERVICE=loadgen`). `drills/loadgen/` holds the Compose overlay and the README that points there.

---

### Task 1: Load generator

**Files:**
- Create: `services/internal/loadgen/loadgen.go`, `services/internal/loadgen/loadgen_test.go`, `services/cmd/loadgen/main.go`
- Modify: `services/devtools/smoke/client.go` (expose `Do` timing or a `Transfer` helper if needed), `services/observability/prometheus/prometheus.yml`
- Create: `drills/loadgen/docker-compose.loadgen.yml`, `drills/loadgen/README.md`

- [x] Write tests for the rate ticker, the wallet pool refill, and the metrics registration.
- [x] Implement: seed N verified users with funded wallet pairs (via `smoke.SetBalance`), then issue transfers at `-rate` req/s, poll each payment to a terminal state with a bounded timeout, and export `loadgen_requests_total{outcome}`, `loadgen_request_duration_seconds` (histogram), and `loadgen_payment_settle_seconds` on `/metrics`.
- [x] Add `loadgen:8080` to the Prometheus scrape config.
- [x] Compose overlay runs `loadgen` on the stack network with `LOADGEN_RATE`, `LOADGEN_USERS`, `LOADGEN_AMOUNT_CENTS`.
- [x] Prove: `go test ./internal/loadgen/` and `go vet ./...`.

### Task 2: Target adapter

**Files:**
- Create: `drills/targets/enjoythings/target.yaml`, `up`, `down`, `reset`, `health`, `observe`, `load`, `inject`, `revert`

- [x] `up`/`down`/`reset` wrap `docker compose` in `services/` with the root `.env`.
- [x] `health` polls every component's health URL from `target.yaml` until all pass or a timeout.
- [x] `load start|stop <profile>` brings the loadgen overlay up or down; profiles are `steady` (the only one in this slice).
- [x] `inject` supports `proc.stop`, `proc.start`, `proc.pause`, `proc.kill`, `env.set`, `data.exec`. Every injection appends a revert line to `drills/.revert` so `revert` can undo in reverse order.
- [x] `target.yaml` lists only the primitives `inject` actually honours; `net.*`, `dep.replace`, `code.patch` are absent so scenarios that need them fail validation.
- [x] Prove: `drills/bin/drill target validate enjoythings`.

### Task 3: `drill` CLI

**Files:**
- Create: `drills/bin/drill`, `drills/bin/drill_test.sh`

- [x] POSIX `sh`. Subcommands: `start <scenario>`, `status`, `observe`, `hint`, `propose`, `execute`, `evaluate`, `resolve`, `end`, `abort`, `target validate <name>`, `scenario validate <slug>`.
- [x] `start`: refuse if `drills/.active` exists; validate scenario against target manifest; `up`, `health`, `load start`, `inject` each fault line, run break probe until it passes (bounded), write `.active` and `drills/runs/<ts>-<slug>/run.yaml`, print the brief.
- [x] State machine per spec §8; every transition appends `- {at, from, to, note}` to `run.yaml`. `evaluate` runs the fix probe under load and records the result; `resolve` requires a passing fix probe.
- [x] `hint` prints the next unrevealed tier from `hints.md` and records it.
- [x] `end`: `revert`, `load stop`, `reset`, write `debrief.md` skeleton from the reference solution, remove `.active`. `abort`: same but marks the run `aborted` and writes no debrief.
- [x] Shell tests run the state machine against a fake target adapter in `$TMPDIR`.
- [x] Prove: `sh drills/bin/drill_test.sh`.

### Task 4: Roles and commands

**Files:**
- Create: `drills/roles/author.md`, `drills/roles/instructor.md`, `drills/roles/executor.md`
- Create: `drills/commands/drill-start.md`, `drill-hint.md`, `drill-propose.md`, `drill-execute.md`, `drill-end.md`
- Create: `.claude/commands/drill-*.md` (generated shims)

- [x] Role playbooks carry the §7 contracts verbatim with no agent-specific syntax.
- [x] Canonical command bodies name the role to adopt and the `drill` subcommand to run.
- [x] `drill sync-commands` emits the Claude Code shims; shims are checked in.
- [x] Prove: `drills/bin/drill sync-commands && git diff --exit-code .claude/commands`.

### Task 5: First scenario — `payment-processor-down`

**Files:**
- Create: `drills/scenarios/payment-processor-down/{brief.md,scenario.yaml,fault.yaml,hints.md,rubric.md,solution.md,probes/break,probes/fix}`

- [x] L1, Tier A, unsealed. Fault: `proc.stop payment-processor`. Symptom: new payments stay in `PAYMENT_PROCESSING`; the Prometheus target goes down; loadgen settle latency climbs while the gateway keeps returning 202.
- [x] Break probe: start one transfer, assert it is still `PAYMENT_PROCESSING` after 15s. Fix probe: with load running, ten consecutive transfers reach `COMPLETED` within 30s.
- [x] Rubric grades the seven §11 dimensions; the reference solution is "restart the consumer, then confirm the backlog drains and no payment was double-charged", with the L1 trade-off being whether to pause intake while the backlog drains.
- [ ] Prove: `drills/bin/drill scenario validate payment-processor-down`, then a full `drill start` / `drill end` cycle against the running stack.

**Found while proving Task 5 against the real stack (2026-09-06):**

- The gateway rate-limits per user (burst 600, then 1 req/s). Four generator
  users polling a growing backlog produced a 429 storm the engineer would have
  read as a symptom. The generator now models many customers each inside a
  per-user budget (`LOADGEN_USER_BUDGET_RPS`, default 1), with status polls
  spending only what transfers leave over; `steady` runs 20 users.
- `drill end` originally reset the target before closing the run record and
  dropping the lock, so a failed reset left a finished run looking active. The
  record and unlock now come first; a failed reset is reported for the operator.
- The fraud worker image runs `uv run` without `--no-sync`, so every container
  start and every healthcheck re-synced the dev group from the network and the
  concurrent syncs blocked one another. `UV_NO_SYNC=1` in `app/fraud/Dockerfile`
  fixes it; the adapter's `up`/`reset` also tolerate Compose giving up on a slow
  dependency and let `health` decide.
- Docker Desktop's disk filled during the first boot (26 GB of build cache).
  Not a framework issue, but `drill end` failing on a full disk is what exposed
  the ordering bug above.

### Task 6: Docs and wiring

- [x] `drills/README.md`: what a drill is, the five commands an engineer touches, how to add a scenario.
- [x] Root `README.md`: one section pointing at `drills/`.
- [x] `.gitignore`: `drills/.active`, `drills/.revert`.
- [x] Update the spec's §13 with the settled decisions and mark this plan's status.
- [ ] Prove: `make test` in `services/`, `sh drills/bin/drill_test.sh`, CI green.
