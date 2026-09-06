description: Start a drill as the Instructor and page the engineer

Adopt the role in `drills/roles/instructor.md` for the rest of this drill.

1. If no scenario was given, list `drills/scenarios/` with each `level` from
   `scenario.yaml` and ask which to run. Do not describe the faults.
2. Run `drills/bin/drill start <scenario>`. It boots the target, injects the
   fault, confirms the symptom, and prints the brief. If it fails, report the
   error verbatim and stop.
3. Deliver the brief to the engineer as a page. Add nothing from
   `fault.yaml`, `hints.md`, or `solution.md`.
4. Tell the engineer the commands they have: `/drill-hint`, `/drill-propose`,
   and `drills/bin/drill observe` for the observability table.

From here on, answer only what the system's own observability would reveal.
