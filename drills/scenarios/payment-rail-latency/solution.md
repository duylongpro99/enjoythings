**Fault:** a 6s latency toxic on the payment-processor → stub-payment-rail edge
(Toxiproxy). `PAYMENT_RAIL_TIMEOUT` is 2s, so every charge call is abandoned
before it returns. The payment step fails, the saga compensates, and the sender
is refunded. The rail container stays healthy the whole time — the fault is on
the *network path*, not the service.

**First useful signal:** `loadgen_requests_total{outcome="failed"}` climbing
while `loadgen_requests_total{outcome="accepted"}` stays flat, and — the
decisive one — a payment trace in Jaeger showing the rail span timing out
(~2s, error) even though `up{instance="stub-payment-rail:..."}` is 1 and the
rail's own latency metric is low. A slow edge, not a down service.

**Reference mitigation:** remove the latency. In drill terms the operator drops
the toxic (the framework's `revert` does this); in production terms the fix is
to repair or route around the degraded network path between the two services,
*not* to touch the rail.

**The trade-off to articulate:** timeout policy.

- *Fix the edge (remove the latency).* Correct: the rail is fine, the path is
  broken. Failures stop immediately.
- *Raise `PAYMENT_RAIL_TIMEOUT`.* Tempting under pressure — it turns failures
  into successes — but it trades a visible failure for a longer hold on every
  payment, masks a real network problem, and increases the window where a saga
  is mid-charge. Defensible only as a deliberate, temporary tolerance while the
  path is repaired, never as the fix.

The rubric grades that the engineer distinguished "slow edge" from "down
service" via the trace, and named the timeout trade-off rather than just
raising it.

**Note on the break probe:** the probe asserts a fresh transfer reaches
`FAILED`, which is what a rail-call timeout drives through compensation. This is
confirmed against the live stack; if the stack instead leaves the payment in
`PAYMENT_PROCESSING` (a retry loop rather than a hard fail), the break probe
becomes `-want PAYMENT_PROCESSING -after 25s`.
