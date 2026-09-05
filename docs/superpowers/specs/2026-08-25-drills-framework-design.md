# Drills Framework — Design

Status: design. No implementation plan is attached; see "Open questions" for what
must be settled before one is written.

## 1. Purpose

Turn a running microservice system into a practice range. An engineer is paged
with a symptom, investigates through the observability the system really
exposes, proposes a fix in prose, watches an agent execute that proposal
faithfully, and then reads an evaluation of what the fix actually bought.

The skill being trained is not "find the injected bug". It is the production
loop: read signals, localise under uncertainty, choose a mitigation, defend the
trade-off. The framework is judged by how honestly it reproduces that loop.

### Non-goals

- Not a chaos-engineering product. Fault injection is a means, not the feature.
- Not a CI gate. Drills are interactive and graded on judgement, not pass/fail.
- Not multi-tenant. One drill is active at a time (see §3).
- Not a code-quiz generator. A scenario that is solved by reading a diff has
  failed its own rubric.

## 2. Decisions

| # | Decision | Rationale |
| --- | --- | --- |
| D1 | Generic framework with a **target adapter**; `enjoythings` is target #1 | The drill loop, scenario format, run record, and role contracts are system-independent. Only boot/observe/inject bind to a system. |
| D2 | **One active drill**, shared Compose stack | The stack's ports are fixed across Compose, Prometheus, and Grafana provisioning. Templating them buys parallelism nobody needs yet. A lock file enforces the invariant. |
| D3 | **Two fault tiers** (runtime and code), scenarios run on a **sealed history** | Most production incidents are logic or configuration, not clean infrastructure failures. Sealing the history removes the cheat path that code faults would otherwise open — see §6. |
| D4 | Slash commands over a **portable core**, agent-agnostic | The loop must run under Claude Code, Codex, Hermes, or any agent that can read a file and run a command. Mechanics live in shell; judgement lives in role playbooks; per-agent files are generated shims. |

D3 was the framework's recommendation rather than a user preference, so §6 states
the reasoning in full and names what it costs.

## 3. Concepts

**Target** — a system that can be booted, observed, and broken. Implements the
adapter contract in §4.

**Scenario** — a versioned directory describing one incident: the brief the
engineer receives, the fault, the probes that decide whether the symptom is
present, and a sealed rubric.

**Fault** — a composition of injection primitives (§5) that produces the
symptom. Declarative; the framework performs the injection, the scenario only
names it.

**Probe** — an executable returning 0 or 1. Every scenario has a *break probe*
(the symptom is live) and a *fix probe* (the symptom is gone under load). Both
run against the target from the outside, never against internals the engineer
cannot reach.

**Run** — one engineer's pass at one scenario: a branch, a timeline, and a
debrief.

**Role** — Author, Instructor, Executor (agents) and Engineer (human). §7.

**Lock** — `drills/.active` holds the id of the running drill. `drill start`
refuses while it exists; `drill end` removes it. This is the whole of D2's
enforcement.

## 4. Target adapter contract

A target is a directory of executables plus a manifest. The framework never
reaches past this boundary.

```
drills/targets/<name>/
  target.yaml        # manifest: components, supported primitives, observability
  up                 # boot to healthy; idempotent; non-zero on failure
  down               # tear down including volumes
  reset              # return to pristine without a full rebuild
  health             # 0 when every component is ready
  observe            # print the URLs and commands the engineer may use
  load <profile>     # start or stop traffic; see §9
  inject <primitive> [args...]   # apply one primitive from §5
  revert                          # undo every injection this run applied
```

`target.yaml` declares components and the primitives the target can honour:

```yaml
name: enjoythings
components:
  - {name: gateway,          kind: service, health: http://localhost:8080/healthz}
  - {name: wallet,           kind: service, health: http://localhost:8081/healthz}
  - {name: ledger,           kind: service, health: http://localhost:8082/healthz}
  - {name: saga-orchestrator,kind: service, health: http://localhost:8084/healthz}
  - {name: payment-processor,kind: service, health: http://localhost:8085/healthz}
  - {name: verification,     kind: service, health: http://localhost:8086/healthz}
  - {name: notification,     kind: service, health: http://localhost:8087/healthz}
  - {name: fraud-worker,     kind: service, health: http://localhost:9101/metrics}
  - {name: kafka,            kind: broker}
  - {name: postgres,         kind: database}
  - {name: fraud-timescaledb,kind: database}
  - {name: stub-payment-rail,kind: dependency}
  - {name: llm-endpoint,     kind: dependency}
primitives: [env.set, proc.stop, proc.pause, proc.kill, net.latency, net.partition,
             dep.replace, data.exec, code.patch]
observability:
  traces:  http://localhost:16686
  metrics: http://localhost:9095
  dashboards: http://localhost:3001
  logs: "docker compose logs -f <component>"
```

The framework validates a scenario against the manifest before booting, so an
unsupported scenario fails at load time rather than mid-drill.

The enjoythings adapter is thin: `up`/`down`/`reset` wrap `docker compose`,
`health` reuses the readiness endpoints already exposed on every service,
`observe` prints the endpoint table from the root README, and `inject` maps
primitives onto `docker compose` and `kafka` admin calls.

## 5. Injection primitives

| Primitive | Meaning | enjoythings mapping |
| --- | --- | --- |
| `env.set <component> K=V` | Override configuration and restart the component | Compose env override file plus `up -d <component>` |
| `proc.stop\|start <component>` | Remove or restore a component | `docker compose stop/start` |
| `proc.pause <component>` | Freeze without closing sockets — produces timeouts, not refusals | `docker pause` |
| `proc.kill <component>` | Ungraceful termination mid-work | `docker kill -s KILL` |
| `net.latency <a> <b> <ms>` | Delay one edge | Toxiproxy between the pair (§9) |
| `net.partition <a> <b>` | Sever one edge while both stay healthy | Toxiproxy |
| `dep.replace <component> <stub>` | Swap an external dependency for a scripted one | `LLM_PROVIDERS_JSON` to the chaos LLM; `stub-payment-rail` to a hanging variant |
| `data.exec <component> <script>` | Mutate state or topics directly | `psql` / Kafka admin |
| `code.patch <ref>` | Tier-B logic fault | §6 |

Tier A is everything above `code.patch`: nothing about it is visible in the
source tree, so it is inherently cheat-proof and covers the operational half of
the scenario space.

### Scenario seams this target already offers

These come from the system as built, not from anything added for drills:

- `DB_MAX_CONNS=1` under load → pool exhaustion; latency without errors first.
- `proc.pause stub-payment-rail` → sagas held in flight; tests whether the
  engineer finds the orchestrator's timeout behaviour before the queue backs up.
- `dep.replace llm-endpoint slow` → the fraud worker's fail-open path under
  timeout. Fail-open is correct here; the drill is about *noticing* that
  scoring silently stopped.
- `proc.stop` the wallet outbox publisher → ledger drifts from wallet with **no
  alert firing**. The strongest scenario in the set, because detection is the
  whole exercise.
- Rotate the RS256 key without restarting consumers → 401 storm; tests reading
  an auth failure as a deploy-ordering problem.
- Replay Kafka records via `data.exec` → the real idempotency test.
- Flood a `.dlq` topic → nothing consumes dead-letter topics today
  (`services/docs/phase5/backlog.md`), so the records are the only evidence.

`services/docs/phase5/backlog.md` is effectively a scenario backlog: every open
item there is a drill whose reference solution is a real design decision.

### Difficulty ladder

| Level | Character | Detection |
| --- | --- | --- |
| L1 | Single component, loud | A health check or dashboard is red |
| L2 | Cross-service | Requires a trace to localise |
| L3 | Silent or partial | No alert fires; state diverges |
| L4 | No clean fix | Every mitigation costs something; the engineer must choose and defend |

L4 is where the Executor's evaluation matters most, and where a rubric grades
articulation rather than correctness.

## 6. Fault tiers and the sealed history

Tier B (`code.patch`) exists because excluding it would exclude most real
incidents. A wrong idempotency key, an outbox write that lands outside the
transaction, an off-by-one retry bound — these are what actually pages people,
and none of them are reachable through configuration.

The obvious problem is that a code fault leaves a diff, and a diff hands over
the answer. The mitigation is to remove the diff rather than hide it:

**Sealed history.** `drill start` creates the worktree from an orphan branch
whose entire tree — faulted source included — is a single initial commit. There
is no parent, no `refs/drills` ref in the worktree, and therefore no diff to
read. `git log` shows one commit. The engineer's own commits sit on top and stay
fully diffable, which is what the debrief needs.

The pristine tree and the fault patch live in the framework's own storage
outside the worktree, so the debrief can still show exactly what was changed and
compare the engineer's fix against the reference solution.

What this costs: the worktree has no upstream history, so `git blame` and
`git log <file>` are unavailable during the drill. That is a real loss — history
is a legitimate debugging tool. Scenarios that intend history to be part of the
investigation must therefore be Tier A and run on a normal branch. `drill start`
takes `--sealed` / `--unsealed`, defaulting to sealed when the scenario declares
a `code.patch`, and the scenario may force either.

## 7. Roles

### Author (agent)

Given a topic — researched independently, taken from the phase-5 backlog, or
supplied by the user — produces a complete scenario directory: brief, fault,
probes, rubric, reference solution. Emits a draft for human review; nothing
enters the scenario library unmerged. The Author must verify its own scenario by
running break probe and fix probe against the target before submitting.

### Instructor (agent)

Holds ground truth for the duration of the drill. Boots the target, injects,
confirms the break probe, and pages the engineer with the brief.

Contract:

1. Answer only what the target's own observability would reveal. If the engineer
   asks a question the dashboards cannot answer, say so and name where they
   would have to look.
2. Never name the fault, the component, or the primitive, in any phrasing,
   including by conspicuous omission.
3. Play the surrounding organisation: page escalations, a stakeholder asking for
   an ETA, a colleague offering a plausible wrong theory.
4. Hints are available on request, tiered, and recorded in the run. Never
   volunteered.

### Engineer (human)

Investigates. Forms a hypothesis. Proposes a mitigation in prose — not a patch.
Proposing in prose is deliberate: it is the artefact an incident channel
actually produces, and it is what the Executor is graded against.

### Executor (agent)

**Implements the engineer's proposal faithfully, including its flaws.** This is
the framework's load-bearing invariant. An Executor that quietly repairs a bad
proposal destroys the exercise, because the lesson lives in watching the idea
fail under load.

Contract:

1. Implement what was proposed, not what would have been better. If the proposal
   is ambiguous, ask; do not resolve the ambiguity toward correctness.
2. If the proposal cannot be implemented as stated, say precisely why and stop.
   Do not substitute.
3. Run the fix probe **under the scenario's load profile**. A fix that passes at
   idle and fails at load has failed.
4. Report what happened before offering an opinion: probe results, metric
   deltas, and any new symptom the fix introduced.
5. Then evaluate — does it address cause or symptom, what does it cost in
   latency, consistency, or operational burden, what breaks next.
6. Only then propose an alternative, with the reasoning that distinguishes it.

Steps 4–6 are ordered on purpose. Verdict-first evaluation trains the engineer
to argue with the agent instead of reading the evidence.

## 8. Lifecycle

```
  drill start <scenario>
        │  boot target, inject, confirm break probe, write .active
        ▼
   BRIEFED ──► INVESTIGATING ──► PROPOSED ──► EXECUTING ──► EVALUATED
                    ▲                                          │
                    └──────────── not resolved ────────────────┘
                                                               │ resolved
                                                               ▼
                                                          DEBRIEFED
        drill end: revert, reset target, write run record, drop .active
```

Every transition appends to the run timeline. `EVALUATED → INVESTIGATING` is the
normal path, not the exception; the number of cycles is a graded signal.

Commands, one per transition, plus `drill status`, `drill hint`, `drill observe`,
and `drill abort` (revert and reset without producing a scored run).

## 9. What the target does not yet have

Three gaps stand between this design and a first drill. All three are additions
to the target adapter, not to the framework.

**Load generator — hard prerequisite.** Nothing in the repository generates
sustained traffic, and most of the scenarios in §5 are invisible on an idle
stack. `drills/loadgen` should reuse `services/devtools/smoke/client.go`, which
already signs a local JWT, creates wallets, starts transfers, and polls saga
status. It needs: a requested rate, a request mix, run-until-stopped, and
p50/p95/p99 plus error rate exported to Prometheus so the existing Grafana
dashboards show the drill's traffic. Without the Prometheus export the engineer
has no latency signal, which removes the primary detection path for L2 and L3.

**Toxiproxy.** `net.latency` and `net.partition` need a proxy between service
pairs in Compose. Until it lands, those primitives are unsupported in
`target.yaml` and scenarios declaring them fail validation — which is the
correct behaviour, not a workaround.

**Chaos LLM endpoint.** `tests/fraud/fake_provider.py` already implements a
scripted OpenAI-compatible ASGI server (`FakeProviderServer`, served by uvicorn,
with per-provider scripted responses and request capture). Containerising it and
adding latency, error-rate, and truncation scripting yields `dep.replace
llm-endpoint <profile>` for free.

Observability needs nothing: Jaeger, Prometheus, Grafana dashboards, and
`/healthz` `/readyz` `/metrics` on every service are already provisioned, and
they are exactly the constraint that makes the drill realistic.

## 10. Agent portability

D4 requires the loop to run under any agent with file access and a shell, so the
split is:

- **Mechanics in shell.** `drills/bin/drill` — a POSIX script dispatching to the
  target adapter. Deterministic, testable, and runnable by a human with no agent
  at all. This is the minimum the "any agent" requirement forces: without it,
  every agent reimplements boot, inject, and probe.
- **Judgement in playbooks.** `drills/roles/{author,instructor,executor}.md`
  hold the §7 contracts as portable Markdown with no agent-specific syntax.
- **Commands as generated shims.** `drills/commands/*.md` is the single source.
  `drill sync-commands` generates one thin file per agent — Claude Code's
  `.claude/commands/`, Codex prompt files, Hermes commands — each a few lines
  that read the canonical command file and pass arguments through. Adding an
  agent means adding one emitter, never editing a contract.

Drift between agents is the failure mode this structure exists to prevent: one
source, generated leaves, and the shims are checked in so a fresh clone works
without a sync step.

## 11. Run record and scoring

`drills/runs/<timestamp>-<scenario>/` holds `run.yaml` (scenario, target, seal
mode, timeline, hints, probe results) and `debrief.md` (generated at
`DEBRIEFED`: what the fault was, what the signals showed, what the engineer did,
how their fix compares to the reference solution).

Graded dimensions, from the scenario's rubric:

| Dimension | What it measures |
| --- | --- |
| Detection | Was the right signal read first, or was it found by luck |
| Localisation | Time and path from brief to correct component |
| Hypothesis quality | Wrong turns, and whether each was cheap to test |
| Fix correctness | Fix probe under the load profile |
| Blast radius | What the fix costs elsewhere |
| Trade-off articulation | Whether the choice was defended or asserted |
| Hints consumed | Tier and count |

Runs are committed so progress is queryable across drills. That is the
difference between a practice framework and a set of puzzles.

## 12. Repository layout

```
drills/
  bin/drill                 # portable mechanics
  roles/                    # Author, Instructor, Executor contracts
  commands/                 # canonical slash-command bodies (generated into agents)
  targets/enjoythings/      # adapter: up, down, reset, health, observe, load, inject
  scenarios/<slug>/         # brief.md, scenario.yaml, fault.yaml, probes/, rubric.md
  loadgen/                  # traffic generator (§9)
  runs/<ts>-<slug>/         # run.yaml, debrief.md
  .active                   # lock (git-ignored)
```

`drills/` sits at the repository root because it is a peer of `app/`, `web/`,
and `services/`, not a member of any of them. When a second target appears, the
framework halves — `bin`, `roles`, `commands`, `scenarios` — extract cleanly;
`targets/enjoythings` stays behind.

## 13. Open questions

1. **Scenario portability across targets.** A scenario names components
   (`wallet`, `kafka`). Either scenarios are target-specific, or they name
   *roles* (`ledger-of-record`, `event-bus`) that each target maps. The second is
   more work and only pays off once a second target exists. Recommend
   target-specific now, with the component field reserved for a future mapping.
2. **Who owns the rubric.** An Author-generated rubric grading trade-off
   articulation is an agent grading judgement it also authored. A human-reviewed
   rubric is stronger but slows the library's growth.
3. **Load profile realism.** A constant rate is easy and unrealistic. Bursts and
   diurnal shapes make L3 detection meaningful but complicate probe determinism.
4. **Multi-engineer drills.** D2 forecloses this for now. Whether a drill should
   support two roles — an incident commander and an investigator — changes the
   run record, not the framework.
