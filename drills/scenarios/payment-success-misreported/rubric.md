| Dimension | What a strong run looks like here |
| --- | --- |
| Detection | Noticed the two signals disagree — payments FAILED while the rail settled them — and treated the rail/saga mismatch as the primary signal, not the raw failure count. Weak: chased the failure-rate spike as if a dependency were down. |
| Localisation | Followed one payment through a trace: rail charge span success, saga to FAILED, empty failure code. Reached "the success outcome is published on the failure path" via evidence, not guesswork. Weak: grepped logs for errors that were never there. |
| Hypothesis quality | Wrong turns are cheap — one trace, one PromQL query. Ruled out the rail and the databases quickly because they are healthy. Weak: restarted the processor or the orchestrator hoping the symptom would clear. |
| Fix correctness | Fix probe passes under load: the success branch publishes `payment.completed`, transfers reach COMPLETED, and the rail/saga outcomes agree again. |
| Blast radius | Named the real damage: this is not just failing payments, it is money charged at the rail with the sender refunded — a reconciliation gap for every payment in the incident window. A fix that only stops new divergence does not address the charges already made. |
| Trade-off articulation | Explicit plan for the already-charged payments: reconcile and re-credit the rail charges (or reverse them), versus re-driving them to COMPLETED. Either is defensible; shipping the code fix and calling the incident closed is not. |
| Hints consumed | L2: zero or one expected. Tier 3 consumed caps the localisation dimension — Tier 3 is the answer. |
