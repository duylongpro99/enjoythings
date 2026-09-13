| Dimension | What a strong run looks like here |
| --- | --- |
| Detection | Read the failure-rate signal (`loadgen_requests_total{outcome="failed"}` climbing, or the Saga Health FAILED share) before touching logs. Weak: started from `docker compose logs` on a guess. |
| Localisation | Used a trace to see the rail span time out while the rail is healthy — not "the rail is down". The distinction between a slow *edge* and a broken *service* is the whole exercise. Weak: restarted the rail and declared victory when the toxic was still in place. |
| Hypothesis quality | Tested the "is the rail actually down?" theory cheaply (its health endpoint, its own latency metric) and discarded it. Weak: assumed the rail based on the symptom. |
| Fix correctness | Fix probe passes under load after the proposal is applied — the latency is removed, not merely tolerated. |
| Blast radius | Named what the failed-and-compensated transfers during the window cost: senders were refunded, so no money lost, but a burst of failed transactions and customer retries. Did the engineer confirm compensation was exactly-once? |
| Trade-off articulation | Explicit choice: remove the latency (fix the edge) vs. raise `PAYMENT_RAIL_TIMEOUT` to tolerate a slow rail. Raising the timeout trades failures for longer holds and hides a real network problem; naming that trade-off is the point. |
| Hints consumed | L2: zero or one expected. Tier 3 consumed means the localisation dimension is capped. |
