**Fault:** the fraud worker's LLM provider was replaced with the chaos endpoint
on its `slow` profile (`dep.replace llm-endpoint slow`). Every model call
exceeds the provider's `timeout_seconds`, the worker retries up to its limit,
and then falls open — recording `action="fail_open"` and letting the transaction
through unscored. Payments keep settling; no saga fails; nothing turns red.

**First useful signal:** `rate(fraud_transactions_scored_total{action="fail_open"}[1m])`
climbing while total scored volume holds, and `fraud_model_latency_seconds` p95
pinned at the timeout. The tell is that the worker is *busy* (volume steady) but
producing no real verdicts (`action="allow"/"flag"/"block"` flat, `fail_open`
rising).

**Reference mitigation:** restore a healthy model provider (failover), so genuine
scoring resumes. In drill terms the operator restores the real registry (the
framework's `revert`); in production terms it is failover to a working provider
or model.

**The trade-off to articulate — the point of the scenario:**

- Fail-open is *correct*. You do not want to block or fail every payment because
  a model is slow; that would turn a scoring outage into a payments outage. An
  engineer who proposes "make fraud scoring block on model failure" has chosen
  the worse blast radius.
- The real failure is **observability**: nothing alerted when scoring silently
  degraded. A strong proposal restores the provider *and* adds an alert on the
  fail-open rate (and/or on model latency), so the next time this happens it
  pages instead of hiding.

The rubric rewards *detecting* the silent degradation and naming that the fix is
provider failover plus alerting — not blocking payments.

**Notes on the probes (confirm against the live stack):**

- The break/fix probes read the worker's own `/metrics` (host `:9101`) via
  `drillmetric` — production observability, within the black-box contract.
- The fix probe asserts `action="allow"` resumes, which assumes the restored
  provider returns real verdicts for benign load traffic and that the drill host
  has a working baseline LLM (without one, the baseline is *itself* fail-open and
  the scenario cannot distinguish fault from baseline). If the host has no real
  LLM, run the fix against the chaos `healthy` profile instead of the real
  provider, and note it in the run record.
