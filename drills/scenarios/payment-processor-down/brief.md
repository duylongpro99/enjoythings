# PAGE: transfers accepted but not completing

**Severity:** SEV-2  
**Reported by:** support, then the payments on-call dashboard

Customers report that transfers show as "in progress" for minutes and never
finish. Support has three tickets in the last ten minutes. The gateway is
answering normally: submitting a transfer returns 202 and the payment shows up
when queried. Nothing has been deployed in the last hour.

Steady-state traffic is running. Your job is to find out what is wrong, decide
what to do about it, and write down the mitigation you want applied. An
operator will apply exactly what you write.

Stakes: every transfer accepted during the incident has already debited the
sender. The longer this runs, the larger the set of customers whose money is
neither in their wallet nor in the recipient's.
