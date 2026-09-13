# Drills Library — First Scenario Batch Implementation Plan

> **Status: not started.** Design: `docs/superpowers/specs/2026-08-25-drills-framework-design.md`. Framework: `docs/superpowers/plans/2026-09-06-drills-framework-slice1.md`. Author contract: `drills/roles/author.md`.

> **For agentic workers:** Steps use checkbox (`- [ ]`) syntax for tracking. Each task ends with the command that proves it.

**Goal:** Grow the drills library from one scenario to four by adding an L1→L2 ladder that covers three distinct failure classes — a single-component config fault, a broker-scope fault, and a cross-service compensation fault — each validated end to end against the running stack. Along the way, close slice 1's two open "Prove" boxes and add the one piece of probe tooling the auth scenario needs.

**Decisions:**

| Question | Decision |
| --- | --- |
| Scenario selection | Only faults expressible with the primitives the target already honours (`proc.*`, `env.set`, `data.exec`) and observable through a **black-box probe** (gateway/public endpoint only — Author contract #3). |
| Probe mechanism | State symptoms reuse `services/devtools/drillprobe`. HTTP-status symptoms get a new `drillhttp` probe helper (Task 1). No probe reads the database or the injection log. |
| Level range | L1 and L2 only. L3/L4 richness needs deterministic silent-fault probing or unlanded primitives (see Deferred). |
| Merge unit | One worktree and **one PR per scenario** (Author contract #4). Rubric ownership shared with the PR reviewer. |
| Load profile | `steady` for all three, as in slice 1. |

**Deferred (and why):**

- **Silent / partial (L3) scenarios** — fraud-worker fail-open, poison-record DLQ, partial verification stalls. Each needs a *deterministic* black-box symptom, which the current probe surface can't express (fraud verdicts are non-deterministic; a single dropped record isn't visible through the gateway). Track separately once probe tooling grows.
- **L4 "no clean fix" and code faults** — need sealed history / `code.patch` (Tier B), `net.*` (Toxiproxy), or the chaos-LLM container (`dep.replace`), none of which have landed. These advance the *framework*, not the library.

**Prerequisite reality:** every new scenario must be run to merge (Author contract #1), so the Docker stack must boot. Slice 1 hit a full-disk failure from ~26 GB of build cache; check disk headroom before Task 0.

---

### Task 0: Recover the drill CLI, then prove the harness end to end

**Blocker found 2026-09-12:** the drill CLI engine (`drills/bin/drill`, `drills/bin/drill_test.sh`) was never committed in slice 1 — absent from the working tree, `origin/master`, every ref, and dangling git objects. The merged framework was non-functional; every documented command pointed at a missing script. Local recovery was exhausted, so the CLI is **reconstructed from spec** (slice-1 plan Task 3, spec §8 lifecycle / §10–11, and the existing adapter/roles/commands/scenario, which pin the exact interface). Committing it is what closes the gap for good. See `docs/lessons.md`.

**Files:** Create `drills/bin/drill`, `drills/bin/drill_test.sh`. Record any live-cycle outcome under `drills/runs/`.

- [x] Reconstruct `drills/bin/drill` (POSIX sh): the §8 state machine, `start/investigate/hint/propose/execute/evaluate/resolve/end/abort/status/observe`, `scenario|target validate`, `sync-commands`; run record and `debrief.md` matching the slice-1 formats; slice-1 teardown ordering (close record + drop lock before reset).
- [x] Reconstruct `drills/bin/drill_test.sh`: drives the full state machine and its guards against a fake target in `$TMPDIR`. 29/29 pass.
- [x] Static checks on the real assets: `drill target validate enjoythings`, `drill scenario validate payment-processor-down`, and `sync-commands` output byte-identical to the checked-in `.claude/commands/drill-*.md`.
- [ ] Full cycle on the **live stack** (needs Docker, run outside the sandbox): `drills/bin/drill start payment-processor-down`, confirm the break probe passed, apply the reference fix (`docker compose start payment-processor`), `drill evaluate` passes, `drill end`.
- [ ] `cd services && make test` passes; confirm CI is green.
- [ ] If any framework bug surfaces (as in the slice-1 "Found while proving" notes), fix it in its own commit before proceeding.
- [ ] Prove: a completed run record in `drills/runs/` and a clean `drill status` afterwards.

### Task 1: `gateway-auth-misconfig` (L1) + `drillhttp` probe helper

A config-drift incident: the gateway is recreated with an algorithm it has no key for, so every authenticated route rejects valid tokens. Loud, single-component, and the first scenario whose symptom is an HTTP status rather than a saga state — so it also introduces the black-box HTTP probe helper the library will reuse.

**Files:**
- Create: `services/devtools/drillhttp/main.go` — submit an authenticated request to a business route and assert the response status (`-want-status`, `-count`, `-within`/`-after`, reusing `devtools/smoke` for token minting).
- Create: `drills/scenarios/gateway-auth-misconfig/{brief.md,scenario.yaml,fault.yaml,hints.md,rubric.md,solution.md,probes/break,probes/fix}`

- [ ] `drillhttp` mints a valid HS256 token and asserts the status of a business route (e.g. `GET /v1/verification/status`); unit-testable status logic, no DB dependency.
- [ ] `fault.yaml`: `env.set gateway JWT_ALG=RS256` (no `JWT_PUBLIC_KEY_*` provided), so the recreated gateway rejects the HS256 token the rest of the stack still signs.
- [ ] `scenario.yaml`: `level: L1`, `tier: A`, `load: steady`, `components: [gateway]`.
- [ ] `brief.md`: symptom and stakes only — "authenticated users get 401 across the board; nothing was deployed" — never names the cause. `hints.md`: where to look (gateway logs / config) → `up` is green so it is not a crash → the accepted signing algorithm changed.
- [ ] Break probe: valid token → **401**. Fix probe under load: valid token → **200** for N consecutive calls.
- [ ] `rubric.md`: the seven §11 dimensions. `solution.md`: reference fix is to restore `JWT_ALG=HS256` (rollback); the trade-off is rollback vs. roll-forward by provisioning a real RS256 key, and the blast radius (a total auth outage with green health checks — config, not crash).
- [ ] Validate per Author contract #1: `drill scenario validate gateway-auth-misconfig`, full `drill start` → break passes → apply fix by hand → `drill evaluate` passes → `drill abort`.
- [ ] Prove: `go test ./devtools/drillhttp/`, `go vet ./...`, and the validated drill cycle above. One PR from a worktree.

### Task 2: `kafka-broker-down` (L2)

The event backbone stops: transfers are accepted but no saga can advance because every outbox publisher and consumer depends on Kafka. Cross-service by nature — the engineer must localise past the individual services to the shared broker.

**Files:**
- Create: `drills/scenarios/kafka-broker-down/{brief.md,scenario.yaml,fault.yaml,hints.md,rubric.md,solution.md,probes/break,probes/fix}`

- [ ] `fault.yaml`: `proc.stop kafka`.
- [ ] `scenario.yaml`: `level: L2`, `tier: A`, `load: steady`, `components: [kafka]`, `break_probe_attempts` tuned so the stall is observable.
- [ ] Break probe (drillprobe): a fresh transfer is accepted (202) but still in its pre-settlement saga state after the hold window. Fix probe (drillprobe): with load running, N consecutive transfers reach `COMPLETED` within the settle window after the broker returns.
- [ ] `brief.md`: "transfers accepted, nothing progresses, multiple services look unhealthy at once." `hints.md`: which saga state → `up` shows many consumers idle, not one → the broker they all share is down and the outbox is buffering.
- [ ] `rubric.md` / `solution.md`: reference fix is to restart Kafka and let outbox publishers and consumer groups resume from committed offsets. Trade-offs to articulate: unbounded outbox growth during the outage, in-order drain on recovery, and pause-intake vs. keep-accepting (as in `payment-processor-down`, but here the buffer is the outbox, not one consumer's backlog).
- [ ] Validate per Author contract #1 (`scenario validate` → `start` → break → manual fix → `evaluate` → `abort`).
- [ ] Prove: the validated drill cycle. One PR from a worktree.

### Task 3: `ledger-down` (L2)

A downstream dependency of the saga is gone: payments cannot record their accounting entry, so sagas fail and compensate. The engineer needs a trace to see the failing ledger gRPC call, and must reason about whether compensation re-credits the sender exactly once.

**Files:**
- Create: `drills/scenarios/ledger-down/{brief.md,scenario.yaml,fault.yaml,hints.md,rubric.md,solution.md,probes/break,probes/fix}`

- [ ] `fault.yaml`: `proc.stop ledger`.
- [ ] `scenario.yaml`: `level: L2`, `tier: A`, `load: steady`, `components: [ledger]`.
- [ ] Confirm the real behaviour first (a debit that cannot be ledgered): does the saga stall, retry, or compensate to `FAILED`? The break probe asserts whichever terminal-or-stuck state the stack actually produces — decided by observation, not assumption.
- [ ] Break probe (drillprobe): a fresh transfer does not reach `COMPLETED` and lands in the observed failure/stuck state. Fix probe (drillprobe): after ledger returns, N consecutive transfers reach `COMPLETED` under load.
- [ ] `brief.md`: "some transfers are failing / hanging; senders unsure if they were charged." `hints.md`: saga state distribution → a trace shows one gRPC hop erroring → ledger is down. `rubric.md` / `solution.md`: reference fix restarts ledger; trade-off centres on compensation correctness — is the sender re-credited exactly once, and are already-compensated payments left alone on recovery.
- [ ] Validate per Author contract #1.
- [ ] Prove: the validated drill cycle. One PR from a worktree.

### Task 4: Wire-up and docs

**Files:**
- Modify: `drills/README.md` (list the available scenarios), root `README.md` if it enumerates scenarios.
- Modify: this plan's status; note any framework bugs found and where they were fixed.

- [ ] `drills/README.md` names all four scenarios with their level and one-line symptom.
- [ ] Each scenario merged via its own PR; `master` CI green after each.
- [ ] Prove: `git diff --exit-code` clean on the docs after regeneration steps (if any), and `sh drills/bin/drill_test.sh` still green.
