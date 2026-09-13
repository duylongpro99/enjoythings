# Hints

Tiered. Each reveals a little more; ask for the next only when stuck.

## Tier 1 — where to look

Intake is healthy: the gateway accepts payments and returns 202. The divergence
is downstream, where a payment turns terminal. Compare what the payment rail
reports for a single payment against the state the saga ends in for that same
payment — do not trust the aggregate dashboards, follow one payment through.

## Tier 2 — read one payment's trace end to end

Pick one FAILED payment and open its trace. The rail's charge span returns
**success** for it, and yet the saga transitions to compensation and lands in
FAILED. Notice the failure the saga records has an **empty failure code and
message** — nothing actually failed. The event that drove the saga to FAILED
was emitted on the happy path, not by any error.

## Tier 3 — the mechanism

The payment-processor charges the rail, marks the attempt completed, and then
reports the outcome on the **failure** path: it publishes `payment.failed` for a
charge that succeeded. The saga believes the payment failed, compensates the
ledger, and refunds the wallet — while the rail keeps the money. Every
"successful" payment becomes a FAILED saga plus a real charge with no
corresponding debit.
