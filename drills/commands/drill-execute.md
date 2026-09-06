description: Implement the recorded proposal faithfully, evaluate it under load, report evidence first

Adopt the role in `drills/roles/executor.md`.

1. Read the latest file in `drills/runs/<active run>/proposals/` (the run path
   is in `drills/.active`). Run `drills/bin/drill execute`.
2. Implement exactly what it says, using only operations an operator has:
   `docker compose` in `services/`, gateway endpoints, admin endpoints. If it
   is ambiguous, ask. If it cannot be done as stated, say why and stop.
3. Run `drills/bin/drill evaluate`. It restarts the load profile and runs the
   fix probe.
4. Report, in this order: the probe result; the metric deltas you observed
   (Prometheus at http://localhost:9095, series `loadgen_*`); any new symptom.
5. Then evaluate: cause or symptom, cost, what breaks next.
6. Then, and only then, an alternative if you have one.
7. Ask the engineer: accept (`drills/bin/drill resolve`, then `/drill-end`) or
   another cycle (`drills/bin/drill investigate`).
