"use client";

import { FormEvent, useState } from "react";

import {
  decideFraudReview,
  formatAmount,
  formatRiskScore,
  getFraudReview,
  listFraudReviews,
  type FraudReviewAction,
  type FraudReviewDetail,
  type FraudReviewSummary,
} from "@/lib/fraud-reviews";

const underReview = "FRAUD_REVIEW";

function shortId(id: string): string {
  return id.slice(0, 8);
}

function formatTime(iso: string | null | undefined): string {
  return iso ? new Date(iso).toLocaleString() : "—";
}

function errorText(error: unknown): string {
  return error instanceof Error ? error.message : "Request failed";
}

export function FraudReviewConsole() {
  const [token, setToken] = useState("");
  const [queue, setQueue] = useState<FraudReviewSummary[] | null>(null);
  const [detail, setDetail] = useState<FraudReviewDetail | null>(null);
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  async function run(work: () => Promise<void>) {
    setBusy(true);
    setError(null);
    try {
      await work();
    } catch (failure) {
      setError(errorText(failure));
    } finally {
      setBusy(false);
    }
  }

  const loadQueue = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    void run(async () => {
      setQueue(await listFraudReviews(token));
    });
  };

  const openReview = (paymentId: string) => {
    void run(async () => {
      setNotice(null);
      setDetail(await getFraudReview(token, paymentId));
      setReason("");
    });
  };

  const decide = (action: FraudReviewAction) => {
    if (!detail) {
      return;
    }
    const paymentId = detail.review.payment_id;
    void run(async () => {
      const outcome = await decideFraudReview(token, paymentId, action, reason);
      setNotice(`${action === "resume" ? "Resumed" : "Rejected"} ${shortId(paymentId)}: the saga is now ${outcome.status}.`);
      const [nextQueue, nextDetail] = await Promise.all([listFraudReviews(token), getFraudReview(token, paymentId)]);
      setQueue(nextQueue);
      setDetail(nextDetail);
      setReason("");
    });
  };

  const review = detail?.review;
  const canDecide = review?.status === underReview && !busy;

  return (
    <div className="review-console">
      <form className="review-token" onSubmit={loadQueue}>
        <label htmlFor="admin-token">Admin JWT</label>
        <input
          id="admin-token"
          type="password"
          value={token}
          onChange={(event) => setToken(event.target.value)}
          placeholder="Paste a token whose role claim is admin"
          autoComplete="off"
        />
        <button type="submit" disabled={busy || token.trim().length === 0}>
          Load queue
        </button>
      </form>

      {error ? (
        <p className="error" role="alert">
          {error}
        </p>
      ) : null}
      {notice ? (
        <p className="review-notice" role="status">
          {notice}
        </p>
      ) : null}

      <div className="review-columns">
        <section className="review-queue" aria-label="Review queue">
          <h2>Queue{queue ? ` (${queue.length})` : ""}</h2>
          {queue === null ? (
            <p className="hint">Load the queue to see payments held in FRAUD_REVIEW.</p>
          ) : queue.length === 0 ? (
            <p className="hint">Nothing is waiting for review.</p>
          ) : (
            <table>
              <thead>
                <tr>
                  <th>Payment</th>
                  <th>Amount</th>
                  <th>Risk</th>
                  <th>Verdict</th>
                  <th>Flagged</th>
                  <th>Rail result</th>
                </tr>
              </thead>
              <tbody>
                {queue.map((item) => (
                  <tr key={item.payment_id} className={review?.payment_id === item.payment_id ? "review-selected" : undefined}>
                    <td>
                      <button type="button" className="review-link" onClick={() => openReview(item.payment_id)} disabled={busy}>
                        {shortId(item.payment_id)}
                      </button>
                      <span className="review-sub">user {shortId(item.user_id)}</span>
                    </td>
                    <td>{formatAmount(item.amount_cents, item.currency)}</td>
                    <td>{formatRiskScore(item.fraud_risk_score)}</td>
                    <td>{item.fraud_action || "—"}</td>
                    <td>{formatTime(item.fraud_flagged_at)}</td>
                    <td>{item.deferred_result_pending ? "waiting" : "—"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </section>

        <section className="review-detail" aria-label="Review detail">
          <h2>Detail</h2>
          {detail && review ? (
            <>
              <dl className="review-facts">
                <dt>Payment</dt>
                <dd>
                  <code>{review.payment_id}</code>
                </dd>
                <dt>Status</dt>
                <dd>
                  {review.status}
                  {review.failure_code ? ` (${review.failure_code})` : ""}
                </dd>
                <dt>Amount</dt>
                <dd>{formatAmount(review.amount_cents, review.currency)}</dd>
                <dt>User</dt>
                <dd>
                  <code>{review.user_id}</code>
                </dd>
                <dt>Wallets</dt>
                <dd>
                  <code>{shortId(review.from_wallet_id)}</code> → <code>{shortId(review.to_wallet_id)}</code>
                </dd>
                <dt>Verdict</dt>
                <dd>
                  {review.fraud_action || "—"} · risk {formatRiskScore(review.fraud_risk_score)}
                </dd>
                <dt>Reason</dt>
                <dd>{review.fraud_reason || "—"}</dd>
                <dt>Fraud session</dt>
                <dd>
                  <code>{review.fraud_session_id || "—"}</code>
                </dd>
                <dt>Flagged</dt>
                <dd>{formatTime(review.fraud_flagged_at)}</dd>
                <dt>Rail result</dt>
                <dd>
                  {review.deferred_result_pending
                    ? "arrived during review; applied on resume, kept for audit on reject"
                    : "not received yet"}
                </dd>
              </dl>

              <div className="review-actions">
                <label htmlFor="review-reason">Reason</label>
                <textarea
                  id="review-reason"
                  rows={2}
                  value={reason}
                  onChange={(event) => setReason(event.target.value)}
                  placeholder="Optional for resume; written to the audit trail with your user id"
                  disabled={!canDecide}
                />
                <div className="review-buttons">
                  <button type="button" onClick={() => decide("resume")} disabled={!canDecide}>
                    Resume payment
                  </button>
                  <button type="button" className="review-reject" onClick={() => decide("reject")} disabled={!canDecide}>
                    Reject and refund
                  </button>
                </div>
                {review.status !== underReview ? (
                  <p className="hint">This review has been decided; the trail below explains it.</p>
                ) : null}
              </div>

              <h3>Audit trail</h3>
              {detail.audit.length === 0 ? (
                <p className="hint">No fraud audit records for this payment.</p>
              ) : (
                <ol className="review-audit">
                  {detail.audit.map((entry) => (
                    <li key={entry.event_id}>
                      <div className="review-audit-head">
                        <span className="review-kind">{entry.kind}</span>
                        <span className="review-sub">
                          {entry.saga_state || "—"} · {formatTime(entry.created_at)}
                        </span>
                      </div>
                      <pre>{JSON.stringify(entry.details, null, 2)}</pre>
                    </li>
                  ))}
                </ol>
              )}
            </>
          ) : (
            <p className="hint">Select a payment from the queue to see its verdict and audit trail.</p>
          )}
        </section>
      </div>
    </div>
  );
}
