**Fault (Tier B, sealed):** in `processPending`
(`services/internal/paymentprocessor/processor.go`), the success branch charges
the rail, marks the attempt completed, and then publishes the outcome on the
**failure** path — `publishFailed` instead of `publishCompleted`:

```go
completed, err := processor.store.MarkCompleted(ctx, current.PaymentID, result, processor.clock.Now())
if err != nil {
    return err
}
return processor.publishFailed(ctx, completed)   // was: publishCompleted
```

The rail has taken the money, but the saga receives `payment.failed`,
compensates the ledger reservation, and refunds the wallet. Every successful
charge becomes a FAILED saga plus a real rail debit with no matching wallet
debit.

**First useful signal:** the failure metrics disagree with the rail. Payment
failures climb while the rail's own charge-success rate stays flat/high, and a
single FAILED payment's trace shows the rail charge span succeeding with the
saga recording an **empty** failure code and message — nothing errored. There is
no dependency `up == 0` and no error in any log, which is what makes this a
detection-and-trace exercise rather than a "what's red" exercise.

**Reference mitigation:** restore the success branch to publish
`payment.completed`. New payments then settle to COMPLETED and the rail and saga
outcomes agree again — the fix probe confirms this under load.

**The trade-off to articulate:** the code fix stops *new* divergence but does
nothing about the payments already charged-and-refunded during the incident.
Two defensible positions:

- *Reconcile against the rail.* Pull the rail's settlement report for the
  window, and for each charge with no matching completed saga, either reverse
  the rail charge or re-credit/re-drive the payment to COMPLETED. Correct, but
  slow and manual, and it must be idempotent so a re-run does not double-correct.
- *Re-drive the failed sagas.* Faster, but only sound for payments the rail
  actually charged; re-driving a genuinely failed payment would charge a
  customer who was correctly refunded. Requires the rail report to partition
  the two sets first — so it collapses back into reconciliation.

The rubric grades that the engineer treated the reconciliation gap as part of
the incident, not just the code line.

**Why this bug for the first Tier-B scenario:** it is a genuine logic fault that
is *deterministic on the happy path* — it fires on every successful payment with
no runtime perturbation, so the black-box probes flip reliably. The other
classic code faults considered (a wrong idempotency key, an outbox write outside
the transaction, an off-by-one retry bound) only surface when the rail
misbehaves, under replay, or after a crash, and several produce a *silent*
divergence the current saga-state probe cannot see. Those become good scenarios
once a balance-inspecting probe exists; this one proves the sealed Tier-B path
end to end today.
