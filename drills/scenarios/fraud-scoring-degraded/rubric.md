| Dimension | What a strong run looks like here |
| --- | --- |
| Detection | Noticed the *silent* degradation: read `fraud_transactions_scored_total{action="fail_open"}` climbing (or the fail-open share on the Fraud Agent dashboard) rather than waiting for an alert that never fires. This is the whole scenario. Weak: concluded "fraud dropped to zero" and closed the page. |
| Localisation | Separated "scoring stopped" from "worker stopped": volume held, but verdicts turned to fail-open, and `fraud_model_latency_seconds` pinned at the timeout — so the model provider, not the worker or Kafka, is the fault. |
| Hypothesis quality | Tested the provider theory cheaply (latency metric, worker logs showing timeouts) before proposing. Weak: restarted the worker, which changes nothing. |
| Fix correctness | Fix probe passes under load: genuine verdicts resume once a healthy provider is restored. |
| Blast radius | Named the real cost: every transaction in the window went out unscored, and the exposure is invisible until a missed fraud clears. Recognised fail-open as correct, so the fix is not "block on model failure". |
| Trade-off articulation | The key insight: fail-open is the right behaviour; the failure is *observability*, not logic. A strong proposal restores a working provider *and* asks for an alert on the fail-open rate. Blocking payments when the model is down is the wrong answer and should be named as such. |
| Hints consumed | L3: detecting a silent fault unaided is the bar. Tier 2+ consumed caps the detection dimension. |
