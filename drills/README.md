# Drills

A practice range built on the running enjoythings stack. An engineer is paged
with a symptom, investigates through the observability the system really
exposes, proposes a fix in prose, watches an agent apply that proposal
faithfully, and reads an evaluation of what the fix actually bought.

Design: `docs/superpowers/specs/2026-08-25-drills-framework-design.md`.
Plans: `docs/superpowers/plans/2026-09-06-drills-framework-slice1.md` (framework),
`docs/superpowers/plans/2026-09-13-drills-framework-slice2.md` (Tier B),
`docs/superpowers/plans/2026-09-13-drills-framework-slice3.md` (network + dependency faults).

## Running a drill

Prerequisites: Docker, Go 1.26, `curl`. One drill at a time (`drills/.active`
is the lock).

```sh
drills/bin/drill start payment-processor-down   # boots, injects, confirms, pages you
drills/bin/drill observe                        # what you may look at
drills/bin/drill hint                           # next hint tier; recorded
drills/bin/drill propose proposal.md            # your mitigation, in prose
drills/bin/drill execute                        # the Executor begins
drills/bin/drill evaluate                       # fix probe under load
drills/bin/drill resolve                        # or: drill investigate
drills/bin/drill end                            # revert, reset, debrief
```

`drill abort` at any point reverts and resets without scoring. `drill status`
prints the timeline.

## Tier B and sealed history

Some scenarios inject a **code fault** (`code.patch`) rather than a runtime one —
a wrong outcome, a bad key, an off-by-one — because that is what most real
incidents are. To keep the fault from being solved by reading a diff, a code
scenario runs **sealed**: `drill start` builds an isolated, single-commit copy of
the source under `drills/.worktrees/<run>/` (faulted, `git log` shows one commit,
no ref reaches the pristine tree) and boots the stack from it. You investigate
and fix the source **in that build tree**, not the main checkout — its path is
the `worktree:` field in `run.yaml`. `drill end` reveals the fault patch in the
debrief, resets the stack to pristine, and removes the tree.

Sealing costs you `git blame`/`git log <file>`. A scenario that needs history as
part of the investigation runs `drill start --unsealed <scenario>` (a real
worktree with visible history and a visible fault commit). Code scenarios default
to sealed; `--sealed`/`--unsealed` and the scenario's `seal:` key override.

## Network and dependency faults

Runtime faults are not only "a component is down". Two more primitives inject the
harder cases (spec §5, §9):

- **`net.latency <a> <b> <ms>` / `net.partition <a> <b>`** — degrade or sever one
  edge via a Toxiproxy container (`toxics/`), leaving both endpoints healthy. The
  engineer localises from a trace, not a red dashboard. The stack has no named
  networks, so only edges whose client URL is an env var are supportable (see
  `toxics/README.md`).
- **`dep.replace llm-endpoint <profile>`** — swap the fraud worker's LLM for a
  scripted chaos endpoint (`chaosllm/`) that is slow, errors, or truncates. The
  worker fails open *silently* (the correct behaviour), so the symptom shows only
  in `fraud_transactions_scored_total{action="fail_open"}` — read with
  `services/devtools/drillmetric`, not a saga state.

## Scenarios

| Scenario | Level | Symptom |
| --- | --- | --- |
| `payment-processor-down` | L1 | transfers accepted but never settle (consumer stopped) |
| `payment-success-misreported` | L2 (Tier B) | a charged payment is reported as failed and refunded |
| `payment-rail-latency` | L2 | transfers fail: the edge to the payment rail exceeds its timeout |
| `fraud-scoring-degraded` | L3 | scoring silently falls open: the LLM provider is timing out |

With Claude Code, the same loop runs through `/drill-start`, `/drill-hint`,
`/drill-propose`, `/drill-execute`, and `/drill-end`. Those shims are generated
from `drills/commands/` by `drill sync-commands`; edit the canonical files, not
the shims.

## Layout

```
drills/
  bin/drill                 portable mechanics; POSIX sh, no agent needed
  bin/drill_test.sh         state-machine tests against a fake target
  roles/                    Author, Instructor, Executor contracts
  commands/                 canonical slash-command bodies
  targets/enjoythings/      adapter: target.yaml, up, down, reset, health,
                            observe, load, inject, revert
  scenarios/<slug>/         brief.md, scenario.yaml, fault.yaml, hints.md,
                            rubric.md, solution.md, probes/{break,fix},
                            faults/*.patch (Tier B)
  loadgen/                  Compose overlay + README for the traffic generator
  toxics/                   Toxiproxy overlay + README (net.*)
  chaosllm/                 chaos LLM overlay + README (dep.replace)
  runs/<ts>-<slug>/         run.yaml, proposals/, debrief.md (committed),
                            seal/ (fault patch + fix diff, Tier B)
  .worktrees/<run>/         per-run sealed/unsealed build tree (git-ignored)
```

## Adding a scenario

Follow `roles/author.md`. Validate with `drill scenario validate <slug>`, then
run it end to end before opening the PR. Probes must be black-box: use
`services/devtools/drillprobe` to create transfers and assert their saga state
through the gateway, or `services/devtools/drillmetric` to assert a counter's
growth on a service's own `/metrics` (both stay outside the target's internals).

Supported primitives are whatever `targets/enjoythings/target.yaml` lists. A
scenario naming anything else fails validation, which is correct until that
primitive is implemented.

## Tests

```sh
sh drills/bin/drill_test.sh                 # state machine, lock, revert order
go -C services test ./internal/loadgen/     # traffic generator
```

## What is not here yet

Command emitters for agents other than Claude Code (Codex, Hermes), and the
`stub-payment-rail` hanging variant of `dep.replace`. Each is described in the
design spec and deliberately deferred to a later slice.
