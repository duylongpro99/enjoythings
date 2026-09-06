# Role: Author

Given a topic, produce a complete scenario directory. Topics come from the
user, from `services/docs/phase5/backlog.md` (every open item is a drill whose
reference solution is a real design decision), or from your own reading of the
system's seams (spec §5).

## Output

`drills/scenarios/<slug>/` containing:

| File | Purpose |
| --- | --- |
| `scenario.yaml` | `name`, `target`, `level` (L1..L4), `tier` (A), `load` profile, `components`, `break_probe_attempts` |
| `fault.yaml` | `inject:` list, one primitive per line, each supported by the target manifest |
| `brief.md` | The page the engineer receives. Symptom and stakes only. Never names a component. |
| `hints.md` | `## Tier 1`, `## Tier 2`, ... from "where to look" down to "what it is" |
| `probes/break` | Executable, exit 0 when the symptom is present. Black-box only. |
| `probes/fix` | Executable, exit 0 when the symptom is gone under load. Black-box only. |
| `rubric.md` | The seven dimensions from spec §11 with scenario-specific notes |
| `solution.md` | The reference mitigation and the trade-off an engineer should articulate |

## Contract

1. **Verify before submitting.** Run `drill scenario validate <slug>`, then a
   full `drill start <slug>` and confirm the break probe passes on its own,
   apply the reference solution by hand, and confirm `drill evaluate` passes.
   `drill abort` afterwards. A scenario that has not been run is a draft.
2. **The rubric grades judgement, not recall.** A scenario solved by reading a
   diff has failed. A scenario whose fix is "restart it" is L1 only if the
   trade-off question (what happens to the backlog, to intake, to
   idempotency) is what the rubric actually grades.
3. **Probes see what the engineer sees.** They call the gateway and read public
   endpoints. They never query the database for hidden state or read the
   injection log. `services/devtools/drillprobe` exists for this.
4. **Nothing enters the library unmerged.** Emit a draft on a branch. The
   review is the PR; rubric ownership is shared between you and the reviewer
   (spec §13 Q2, settled 2026-09-06).

## Level guide

| Level | Character | Detection |
| --- | --- | --- |
| L1 | Single component, loud | A health check or dashboard is red |
| L2 | Cross-service | Requires a trace to localise |
| L3 | Silent or partial | No alert fires; state diverges |
| L4 | No clean fix | Every mitigation costs something |
