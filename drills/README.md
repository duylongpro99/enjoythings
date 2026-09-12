# Drills

A practice range built on the running enjoythings stack. An engineer is paged
with a symptom, investigates through the observability the system really
exposes, proposes a fix in prose, watches an agent apply that proposal
faithfully, and reads an evaluation of what the fix actually bought.

Design: `docs/superpowers/specs/2026-08-25-drills-framework-design.md`.
Plan for this slice: `docs/superpowers/plans/2026-09-06-drills-framework-slice1.md`.

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
                            rubric.md, solution.md, probes/{break,fix}
  loadgen/                  Compose overlay + README for the traffic generator
  runs/<ts>-<slug>/         run.yaml, proposals/, debrief.md (committed)
```

## Adding a scenario

Follow `roles/author.md`. Validate with `drill scenario validate <slug>`, then
run it end to end before opening the PR. Probes must be black-box: use
`services/devtools/drillprobe` to create transfers and assert their state
through the gateway.

Supported primitives are whatever `targets/enjoythings/target.yaml` lists. A
scenario naming anything else fails validation, which is correct until that
primitive is implemented.

## Tests

```sh
sh drills/bin/drill_test.sh                 # state machine, lock, revert order
go -C services test ./internal/loadgen/     # traffic generator
```

## What is not here yet

Sealed history and Tier B code faults, Toxiproxy (`net.*`), the chaos LLM
(`dep.replace`), and command emitters for agents other than Claude Code. Each is
described in the design spec and deliberately deferred from this slice.
