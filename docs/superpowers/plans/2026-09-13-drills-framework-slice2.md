# Drills Framework — Slice 2 Implementation Plan

> **Status: implemented.** All tasks below are done and unit-proven — `sh drills/bin/drill_test.sh` is green (51 checks, incl. sealed/unsealed/refusal/debrief) and `drill scenario validate payment-success-misreported` passes. The one remaining proof is the full `drill start`/`drill end` cycle for the new scenario against a live Docker stack (Task 5), left to a machine that can boot the platform — mirroring slice 1, whose live-stack proof was likewise deferred.
> Design: `docs/superpowers/specs/2026-08-25-drills-framework-design.md`. Prior slice: `docs/superpowers/plans/2026-09-06-drills-framework-slice1.md`.

> **For agentic workers:** Steps use checkbox (`- [ ]`) syntax for tracking. Each task ends with the command that proves it.

**Goal:** Add Tier B (`code.patch`) faults and the sealed history that keeps them from being solved by reading a diff. Ship the `code.patch` primitive in the enjoythings adapter, sealed-history creation/teardown in the `drill` CLI with `--sealed`/`--unsealed`, a debrief that shows the fault patch and compares the engineer's fix to the reference, one Tier B scenario proving the path end to end, state-machine test coverage, and the doc updates.

**Decisions carried from spec §6 / §13 (slice 1):** target-specific scenarios; Author drafts rubric, human merges; one active drill (lock); black-box probes only.

**Deferred to later slices (unchanged from slice 1):** Toxiproxy (`net.*`), the chaos LLM container (`dep.replace`), `drill sync-commands` emitters for agents other than Claude Code.

**Key clarification of spec §6 forced by implementation (see Task 2):** a shared `git worktree` cannot be sealed — it shares `.git` refs and objects with the main checkout, so `git show master:<file>` / `git diff master` in the worktree hand the engineer the pristine tree and thus the fault. Sealed history is therefore implemented as an **isolated single-commit git repository** populated from `git archive HEAD` (no remote, no other refs, no reflog to the pristine tree). Unsealed mode uses a real worktree, where history is meant to be visible. Both are built from an isolated tree, not the main checkout, so the running stack still boots from the main checkout for Tier A scenarios exactly as in slice 1. **§6 should be amended to say "isolated repo / archive" rather than "orphan branch in a worktree" (Task 7).**

**Layout note:** framework state (`drills/runs/`, `drills/.active`, `drills/.revert`, per-run seal storage) stays in the *main* checkout. Only the *service source that Docker builds* comes from the sealed tree, selected by a new `DRILL_BUILD_ROOT` override the adapter honours. This keeps the run record and lock exactly where slice 1 put them.

---

### Task 1: `code.patch` injection primitive in the enjoythings adapter

**Files:**
- Modify: `drills/targets/enjoythings/target.yaml` (add `code.patch` to `primitives`)
- Modify: `drills/targets/enjoythings/lib.sh` (honour a `DRILL_BUILD_ROOT` build-root override)
- Modify: `drills/targets/enjoythings/inject` (add a `code.patch <ref>` case for the unsealed path)
- Modify: `drills/targets/enjoythings/reset` (rebuild images on reset so a faulted image is replaced by pristine)

**Approach:**
- `target.yaml`: change the `primitives:` line to include `code.patch`. This is what makes a Tier B scenario pass `scenario_validate` (`drills/bin/drill` line 108–112 greps the manifest's primitives list). Leave `net.*` and `dep.replace` absent.
- `lib.sh` line 6–8: introduce `BUILD_ROOT="${DRILL_BUILD_ROOT:-$REPO_ROOT}"`, then `SERVICES_DIR="$BUILD_ROOT/services"` and read `.env` from `$BUILD_ROOT/.env` (falling back to `$REPO_ROOT/.env`). `ADAPTER_DIR`/`DRILLS_DIR` stay computed from the script location, so framework state is unaffected. `compose` and `compose_with_loadgen` already `cd "$SERVICES_DIR"`, so they pick up the override for free. Compose's default project name is `basename` of the services dir = `services` in both the main and the sealed tree, so the stack stays one project (D2 preserved).
- `inject` `code.patch)` case (unsealed only; sealed applies the patch during `start`, Task 2): `git -C "$BUILD_ROOT" apply --index "$ref"`, then `compose up -d --build --no-deps <affected service>`, and `record_revert "code.unpatch <ref>"`. Add a `code.unpatch)` internal case that runs `git -C "$BUILD_ROOT" apply -R "$ref"` and rebuilds. `known_component` is not applicable; validate that `$ref` resolves to a readable file. (In slice 2 the shipped scenario is sealed, so this path is exercised only by `--unsealed`; keep it minimal.)
- `reset`: for a code fault the running images are faulted, so a plain `compose up -d` after `down -v` would reuse the cached faulted image. Add a rebuild when reset is asked to: read `DRILL_RESET_REBUILD` and pass `--build` to the final `compose up -d` when set. `teardown` (Task 2) sets it for code faults.

- [x] Add `code.patch` to `target.yaml` primitives.
- [x] `lib.sh` honours `DRILL_BUILD_ROOT`; default behaviour unchanged when unset.
- [x] `inject code.patch <ref>` / `code.unpatch <ref>` apply and reverse a patch against `DRILL_BUILD_ROOT` and rebuild the affected service.
- [x] `reset` rebuilds images when `DRILL_RESET_REBUILD=1`.
- [x] **Prove:** `drills/bin/drill target validate enjoythings` (now lists `code.patch`), and `DRILL_BUILD_ROOT=/nonexistent drills/targets/enjoythings/env` still prints host URLs (override is inert for probes).

---

### Task 2: Sealed history in `drill start`

**Files:**
- Modify: `drills/bin/drill` (`cmd_start`, new helpers `resolve_seal_mode`, `seal_setup`, `worktree_setup`; run-record fields; `teardown`)
- Modify: `.gitignore` (ignore the sealed-tree scratch dir)

**Approach — where the tree lives:** per run, create the build tree under `drills/.worktrees/<ts>-<slug>/` (git-ignored) and per-run seal storage under `drills/runs/<ts>-<slug>/seal/`. Point the adapter at the tree with `DRILL_BUILD_ROOT`.

**Approach — sealed (default for `code.patch`):**
1. `WT="$DRILLS_DIR/.worktrees/$_ts-$scenario"; mkdir -p "$WT"`.
2. Populate an isolated tree from the current HEAD: `git -C "$REPO_ROOT" archive HEAD | tar -x -C "$WT"`. Copy the untracked-but-required `.env`: `[ -f "$REPO_ROOT/.env" ] && cp "$REPO_ROOT/.env" "$WT/.env"` (confirmed untracked; the fraud build context is the repo root `..`, so the whole tree, incl. `app/fraud`, must be present — `git archive HEAD` provides it).
3. Make it a sealed one-commit repo: `git -C "$WT" init -q`; apply the fault patch named by the scenario's `code.patch <ref>` line (`git -C "$WT" apply "$_d/$ref"`); `git -C "$WT" add -A`; commit with a framework identity, not the user's: `git -C "$WT" -c user.name=drill -c user.email=drill@enjoythings.local -c commit.gpgsign=false commit -q -m "drill: $scenario (sealed)"`. Result: `git -C "$WT" log` shows exactly one commit, no parent, faulted tree included; no ref reaches the pristine tree.
4. Seal storage (outside the tree, in the run dir): copy the fault patch to `seal/fault.patch`; record the sealed root SHA to `seal/root.sha` (used by the debrief and teardown); record `base_commit` (main `git rev-parse HEAD`).
5. Set `sealed: true`, `worktree: <WT>`, `seal_root: <sha>`, `base_commit: <sha>` in `run.yaml`.
6. Export `DRILL_BUILD_ROOT="$WT"` for every adapter call in this run (`up`, `load`, `revert`, and probes). The fault is already baked into the tree, so **do not** call `adapter/inject` for the `code.patch` line; still call `adapter/inject` for any non-`code.patch` primitives (a scenario may mix Tier A + Tier B).

**Approach — unsealed (`--unsealed`, or scenario forces it):** same as sealed but keep history: `git -C "$REPO_ROOT" worktree add -b "drills/run/$_ts-$scenario" "$WT" HEAD`, apply the patch, commit it as a visible "fault" commit. `git log` shows the base history plus the fault commit — the diff is readable, which is the documented trade-off (spec §6: history-dependent scenarios run unsealed). `DRILL_BUILD_ROOT="$WT"` as before. Record `sealed: false`.

**Approach — resolve_seal_mode (precedence):** CLI `--sealed`/`--unsealed` > scenario.yaml `seal: sealed|unsealed` > default. Default: sealed iff `fault.yaml` declares a `code.patch` primitive, else unsealed with no tree (Tier A path — unchanged from slice 1). Reject `--sealed` on a scenario with no `code.patch` (`die` with a clear message): sealing a config-only fault seals nothing.

**Approach — teardown (`drills/bin/drill` `teardown`):** for a run with a build tree, after `revert`/`load stop`, reset the target with the **main** build root (unset `DRILL_BUILD_ROOT`) and `DRILL_RESET_REBUILD=1` so pristine images replace the faulted ones; then `git -C "$REPO_ROOT" worktree remove --force "$WT"` (unsealed) or `rm -rf "$WT"` (sealed isolated repo), and `git -C "$REPO_ROOT" branch -D drills/run/... 2>/dev/null || true` (unsealed). Preserve the slice-1 ordering lesson: **close the run record and drop the lock before reset** (current lines 322–333); worktree cleanup happens with reset, after the lock is gone, and a failed cleanup is reported, not fatal.

- [x] `cmd_start` parses `--sealed`/`--unsealed` in any position and resolves the mode.
- [x] Sealed runs build an isolated one-commit repo from `git archive`; `git log` in it shows one commit and no ref reaches pristine.
- [x] `run.yaml` records `sealed`, `worktree`, `seal_root`, `base_commit`; `seal/fault.patch` is stored outside the tree.
- [x] Adapter calls for the run use `DRILL_BUILD_ROOT`; the stack boots faulted from the sealed tree.
- [x] `teardown` resets from the main tree with a rebuild, removes the tree/branch, after unlock.
- [x] **Prove:** covered by Task 6 (`sh drills/bin/drill_test.sh`), plus manual: after `drill start <tier-b-scenario>`, `git -C drills/.worktrees/<run> log --oneline | wc -l` prints `1` and `git -C drills/.worktrees/<run> show master:services/...` fails (no such ref).

---

### Task 3: `--sealed` / `--unsealed` flags and default selection

**Files:** Modify: `drills/bin/drill` (dispatch + `cmd_start` usage/help text, lines 130–196, 12–23 header, 392–394 help)

**Approach:** implemented by `resolve_seal_mode` in Task 2; this task is the surface: extend `cmd_start`'s arg parsing (currently `scenario=${1:-}`) to a small loop accepting `--sealed|--unsealed` and one positional scenario; update the header comment block and the `help` output to document them and the default rule; keep `scenario.yaml` `seal:` as the scenario-level force.

- [x] `drill start --sealed <s>` and `drill start --unsealed <s>` both parse regardless of flag position.
- [x] Default is sealed when `fault.yaml` has a `code.patch`, unsealed otherwise; `scenario.yaml seal:` overrides the default; the CLI flag overrides both.
- [x] `--sealed` on a `code.patch`-less scenario is refused with a clear message.
- [x] **Prove:** `drills/bin/drill help | grep -- --sealed` and the flag/default assertions in Task 6.

---

### Task 4: Debrief shows the fault patch and compares the fix

**Files:** Modify: `drills/bin/drill` (`write_debrief`, lines 275–306; `teardown` computes the engineer diff before tree removal)

**Approach:**
- Before removing the tree in `teardown`, compute the engineer's fix and stash it in seal storage: `git -C "$WT" -c core.pager=cat diff "$(cat "$_run/seal/root.sha")" > "$_run/seal/engineer-fix.diff"` (diffs the sealed root against the current tree, capturing committed + uncommitted edits regardless of whether the engineer committed). For unsealed, diff the fault commit tip.
- `write_debrief`: when `seal/fault.patch` exists, add two sections:
  - **"What the fault changed"** — render `seal/fault.patch` in a fenced ```diff block (this is the pristine→faulted delta; for Tier A this section stays as today's `fault_primitives` output).
  - **"Your fix vs the reference"** — render `seal/engineer-fix.diff` in a ```diff block, then `solution.md` as the reference reasoning, and a one-line note whether the fix restored the faulted lines. The Executor writes the prose comparison into the rubric section, as today.
- Keep the existing sections (proposals, timeline, rubric) unchanged.

- [x] `teardown` writes `seal/engineer-fix.diff` before tearing the tree down.
- [x] `debrief.md` for a code fault contains the fault patch and the engineer diff, both as `diff` blocks, plus `solution.md`.
- [x] Tier A debriefs are byte-for-byte unchanged (no seal storage ⇒ old path).
- [x] **Prove:** in Task 6, after `end`, `grep -q 'What the fault changed' runs/*/debrief.md` and the engineer diff block is present.

---

### Task 5: Tier B scenario — `payment-success-misreported`

**Files:** Create `drills/scenarios/payment-success-misreported/{brief.md,scenario.yaml,fault.yaml,hints.md,rubric.md,solution.md,faults/misreport.patch,probes/break,probes/fix}`

**The bug (chosen from the payment-processor seam, §5/§6):** the payment-processor charges the rail successfully but reports the outcome on the failure path — a mis-wired terminal outcome in `services/internal/paymentprocessor/processor.go`. `faults/misreport.patch` is a one-hunk patch to the `err == nil` success branch of `processPending` (lines ~117–126) so a completed charge publishes a `payment.failed` result instead of `payment.completed` (e.g. route the success through `publishFailed` / the `saga.TopicPaymentFailed` topic). Downstream, `HandlePaymentFailed` in the orchestrator marks the saga `FAILED` and compensates the wallet — so the sender is refunded in the wallet while the rail was actually charged: a real money-divergence incident, invisible in the diff once sealed.

**Why this bug and not the three §6 examples verbatim:** an *off-by-one retry bound* and a *wrong idempotency key* are only observable when the rail misbehaves or under a specific replay — a pure Tier B scenario has no runtime perturbation, so under a healthy rail they either never fire (retry bound) or produce a *silent* divergence the existing saga-state probe cannot see (idempotency collision returns the cached first result). An *outbox-outside-the-transaction* fault needs a crash to surface. The misreported-outcome bug is a genuine logic fault that is deterministic and black-box on the happy path, which is what "prove the path end to end" needs. (Note this openly in `solution.md`; a future balance-inspecting probe would unlock the silent-divergence variants.)

**Scenario config:** `scenario.yaml`: `target: enjoythings`, `level: L2`, `tier: B`, `load: steady`, `components: [payment-processor]`, `break_probe_attempts: 3`. `fault.yaml`: `inject:` with `- code.patch faults/misreport.patch`. No `seal:` key ⇒ defaults to sealed.

**Probes (black-box via `services/devtools/drillprobe`, mirroring `payment-processor-down`):**
- `probes/break`: `go -C services run ./devtools/drillprobe -want FAILED -within 30s -count 1` — a fresh transfer settles to `FAILED` though nothing is down.
- `probes/fix`: `go -C services run ./devtools/drillprobe -want COMPLETED -within 30s -count 10` — under load, ten transfers reach `COMPLETED`.

**Docs:** `brief.md` — SEV-2, transfers failing with no dependency alerting, nothing deployed; `hints.md` — three tiers (T1: gateway healthy, failures are downstream; T2: the rail returns success in traces yet the saga fails — read a failing payment's trace end to end; T3: the processor reports a successful charge on the failure path, so money leaves at the rail while the wallet is compensated). `rubric.md` — the seven §11 dimensions, weighting *localisation via trace* (rail span success vs saga FAILED) and *blast radius* (rail charged but wallet refunded ⇒ reconciliation debt). `solution.md` — fault statement, first useful signal (`loadgen_requests_total{outcome="failed"}` climbing with the rail's own success metric flat), reference fix (restore the success branch to publish `payment.completed`), and the trade-off (whether to also reconcile/refund the already-charged rail transactions from the incident window).

**Author verification (required before merge, per §7 and the slice-1 Task 5 precedent):** run break and fix probes against the real stack; if `HandlePaymentFailed` rejects the completed-shaped payload rather than compensating, adjust the patch to synthesize a proper `PaymentFailed` in the success branch (fallback noted in `solution.md`). Record any "found while proving" notes in this plan, as slice 1 did.

- [x] Scenario directory created, mirroring `payment-processor-down`, with `faults/misreport.patch`.
- [x] `probes/break` and `probes/fix` are executable and black-box.
- [x] `drills/bin/drill scenario validate payment-success-misreported` passes (needs `code.patch` in the manifest from Task 1).
- [x] `drills/bin/drill scenario validate payment-success-misreported` passes; the fault patch applies cleanly (`git apply --check`).
- [ ] **Prove (pending, needs a live stack):** a full sealed `drill start payment-success-misreported` / `… end` cycle against the running stack with both probes flipping.

---

### Task 6: Sealed-history state-machine coverage in `drill_test.sh`

**Files:** Modify: `drills/bin/drill_test.sh`

**Approach:** extend the existing fake-target harness (no Docker) to exercise the real sealing code path against a throwaway git repo built in `$WORK`.
- Build a tiny source tree under `$WORK/src` with a tracked file `services/app.txt` containing `OK`, `git init` it, and commit — set `GIT_CONFIG_GLOBAL=/dev/null`, `GIT_CONFIG_SYSTEM=/dev/null`, and `GIT_AUTHOR_*`/`GIT_COMMITTER_*` so the test does not depend on (or trip over) user git config, which is sandbox-blocked. Point `cmd_start`'s archive source at it via a test hook: add a `DRILL_REPO_ROOT` override read by `cmd_start` so the harness can point archiving at `$WORK/src` — small, test-only override to add in Task 2.
- Fake Tier B scenario `codebug`: `fault.yaml` → `- code.patch faults/bug.patch`; `faults/bug.patch` flips `OK`→`BROKEN` in `services/app.txt`. Fake `env` exports `DRILL_BUILD_ROOT`; `probes/break` asserts `grep -q BROKEN "$DRILL_BUILD_ROOT/services/app.txt"`, `probes/fix` asserts it is gone. The fake adapter `up`/`reset` stay no-ops.
- Assertions:
  - `start codebug` (no flag) ⇒ `run.yaml` has `sealed: true`; `.worktrees/<run>` exists; `git -C <wt> log --oneline | wc -l` = 1 (sealed).
  - `git -C <wt> rev-list --all | wc -l` = 1 (no other refs reach pristine).
  - `seal/fault.patch` exists outside the tree.
  - break probe passed at start (BROKEN present).
  - Simulate the engineer's fix: rewrite `app.txt` to `OK` in the worktree and commit; `evaluate` passes; `resolve`; `end`.
  - `debrief.md` contains "What the fault changed" and the engineer diff (`seal/engineer-fix.diff` non-empty).
  - after `end`: `.worktrees/<run>` removed, lock dropped, `result: resolved`.
  - `start --unsealed codebug` ⇒ `sealed: false` and `git -C <wt> log --oneline | wc -l` > 1 (base history visible); abort cleans up.
  - `start --sealed demo` (the Tier A fake scenario, no `code.patch`) is refused.
- Keep every existing slice-1 assertion (the Tier A `demo` path stays green: no tree created, run record and lock in `$WORK` as before).

- [x] New fake git repo + `codebug` scenario added to the harness.
- [x] Sealed/unsealed/default/refusal and debrief assertions added; slice-1 assertions untouched and passing.
- [x] **Prove:** `sh drills/bin/drill_test.sh` (all pass, `0 failed`).

---

### Task 7: Docs and role/command wiring

**Files:**
- Modify: `docs/superpowers/specs/2026-08-25-drills-framework-design.md` (§6 wording, §5 `code.patch` row, §13 status)
- Modify: `drills/roles/instructor.md`, `drills/roles/executor.md`, `drills/commands/drill-start.md`, `drills/commands/drill-end.md`, and regenerate `.claude/commands/drill-*.md`
- Modify: `drills/README.md`
- Modify: `.gitignore` (`drills/.worktrees/`)
- Create: `docs/superpowers/plans/2026-09-13-drills-framework-slice2.md` (this file)

**Approach:**
- Spec §6: replace "creates the worktree from an orphan branch" with the isolated-repo/`git archive` mechanism and the reason (shared object store leaks the pristine tree); keep the sealed/unsealed trade-off text. §5: mark `code.patch` as implemented (Tier B). §13: add a "Settled 2026-09-13" row pointing at this plan.
- `instructor.md`: for a code fault, tell the engineer where the source lives (the run's worktree path from `run.yaml`) and that `git log`/`git blame` are unavailable in a sealed run by design; never read `seal/fault.patch` aloud.
- `executor.md`: implement the proposal by editing source **in the run's worktree** and rebuilding the affected service (`DRILL_BUILD_ROOT=<worktree> docker compose up -d --build --no-deps <service>`), then `drill evaluate`; do not edit the main checkout.
- `drill-start.md`: mention `--sealed`/`--unsealed` and that code faults default to sealed; surface the worktree path after start. `drill-end.md`: note the debrief now includes the fault patch and the fix comparison.
- `README.md`: a short "Tier B and sealed history" subsection; note `drills/.worktrees/` is scratch.
- `.gitignore`: add `drills/.worktrees/`.
- Regenerate shims and keep them checked in.

- [x] Spec §5/§6/§13 updated; plan file added.
- [x] Roles/commands describe the worktree, rebuild step, and seal flags; README + `.gitignore` updated.
- [x] **Prove:** `drills/bin/drill sync-commands && git diff --exit-code .claude/commands`.

---

## Riskiest / most uncertain decisions

1. **Shared worktree leaks the answer.** The spec's literal "orphan branch in a worktree" does not seal — a git worktree shares refs/objects with the main repo, so `git show master:<file>` reveals pristine. The plan switches sealed mode to an isolated `git archive` repo. This is a real deviation from §6 and needs the spec amended; if a reviewer insists on a true worktree, sealing is not achievable and the decision must be revisited.
2. **The scenario bug's exact black-box symptom is unverified from source.** `payment-success-misreported` is chosen because it is deterministic on the happy path, but whether the orchestrator's `HandlePaymentFailed` compensates cleanly on a completed-shaped payload (vs. rejecting it) must be confirmed against the running stack; the patch may need to synthesize a proper `PaymentFailed`. The three §6 example bugs (idempotency key, outbox-outside-txn, off-by-one retry) were evaluated and rejected for a pure Tier B scenario because they are silent or non-firing without a runtime perturbation.
3. **Build-root override vs. one Compose project.** Building from `$WT/services` relies on Compose's project name staying `services` (basename), so the sealed build recreates the same stack rather than a parallel one. `.env` must be copied in because it is untracked.
4. **Reset must rebuild.** A faulted image is cached; teardown/reset must force `--build` from the main tree or the next drill inherits the fault. The added `DRILL_RESET_REBUILD` path is load-bearing and slows teardown.
5. **Dual location for the engineer.** Framework state stays in the main checkout while code lives in the worktree. The role docs must make this split unmissable, or an Executor will "fix" the main checkout and the probe will never flip.
6. **Test harness needs real git under the sandbox.** `drill_test.sh` must create commits with `GIT_CONFIG_GLOBAL=/dev/null` and explicit identity env; the user's `~/.gitconfig` is not readable in the sandbox (observed).
