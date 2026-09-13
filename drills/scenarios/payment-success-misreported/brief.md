# PAGE — SEV-2: transfers are failing, and finance says the money moved anyway

**Time:** now  •  **Severity:** SEV-2  •  **Paged by:** payments on-call

Two signals arrived within a few minutes of each other:

1. The payment failure rate has climbed sharply. Customers who start a transfer
   see it come back **FAILED**, and their wallet balance is unchanged (the
   platform refunded them). No dependency alert is firing — the rail, the
   databases, and every service report healthy.

2. Finance escalated: the payment rail's settlement report shows **successful
   charges** for a batch of payments that the platform recorded as failed. The
   two ledgers disagree, and the gap is growing with every failed payment.

There was no recent deploy or config change to point at. Intake still accepts
payments (the gateway returns 202), and money is leaving the rail — but the saga
keeps landing in FAILED and compensating the wallet.

Find where the payment outcome diverges from what the rail actually did, decide
how to stop the bleeding, and say what you would do about the charges already
made during the incident window.

You have the full observability stack: traces (Jaeger), metrics (Prometheus),
Grafana dashboards, and per-service logs. Run `drill observe` for the URLs.
