# PAGE: risk is quiet — maybe too quiet

**Severity:** SEV-3 (escalating)  
**Reported by:** the fraud analytics team, not an alert

Payments are settling normally and no dashboard is red. But the fraud analytics
team noticed their review queue went nearly silent over the last half hour —
almost nothing is being flagged for review, where normally a steady trickle is.
They want to know whether fraud really dropped to zero, or whether something
stopped looking. Nothing has been deployed in the last hour.

Steady-state traffic is running. Find out what is happening, decide what to do,
and write down the mitigation you want applied. An operator will apply exactly
what you write.

Stakes: if scoring has silently stopped, every transaction in the window went
out unscored. There is no failed payment to point at — the cost is invisible
until a fraudulent transfer that would have been caught clears.
