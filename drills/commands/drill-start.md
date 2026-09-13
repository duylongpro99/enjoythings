description: Start a drill as the Instructor and page the engineer

Adopt the role in `drills/roles/instructor.md` for the rest of this drill.

1. If no scenario was given, list `drills/scenarios/` with each `level` from
   `scenario.yaml` and ask which to run. Do not describe the faults.
2. Run `drills/bin/drill start <scenario>`. It boots the target, injects the
   fault, confirms the symptom, and prints the brief. If it fails, report the
   error verbatim and stop. A Tier B (`code.patch`) scenario runs **sealed** by
   default: the fault is baked into an isolated one-commit build tree with no
   diff to read. Pass `--unsealed` only when the scenario needs git history as
   part of the investigation; never pass `--sealed`/`--unsealed` to defeat a
   scenario's own choice.
3. Deliver the brief to the engineer as a page. Add nothing from
   `fault.yaml`, `hints.md`, `solution.md`, or `seal/fault.patch`.
4. Tell the engineer the commands they have: `/drill-hint`, `/drill-propose`,
   and `drills/bin/drill observe` for the observability table. For a code fault,
   tell them the source they investigate and fix lives in the run's build tree
   (the `worktree:` path in `run.yaml`), **not** the main checkout, and that in
   a sealed run `git log`/`git blame` show only one commit by design.

From here on, answer only what the system's own observability would reveal.
