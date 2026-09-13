# PAGE: transfers failing, but every dependency looks healthy

**Severity:** SEV-2  
**Reported by:** payments on-call dashboard, then support

A rising share of transfers are ending in failure. Customers who retry
sometimes succeed, sometimes fail again. The gateway answers normally — a
transfer submits and returns 202 — and every service, including the payment
rail, reports healthy. Nothing has been deployed in the last hour.

Steady-state traffic is running. Find out what is wrong, decide what to do, and
write down the mitigation you want applied. An operator will apply exactly what
you write.

Stakes: each failed transfer compensates and refunds the sender, so no money is
lost yet — but the failure rate is climbing and the on-call channel wants an ETA
and a root cause, not just "it's flaky".
