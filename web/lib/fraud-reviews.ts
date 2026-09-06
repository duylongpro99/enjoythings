// Browser-side data layer for the operator fraud review page. Every call goes
// through the Next proxy under /api/admin/fraud-reviews, which forwards the
// bearer token to the gateway.

export type FraudReviewSummary = {
  payment_id: string;
  status: string;
  user_id: string;
  from_wallet_id: string;
  to_wallet_id: string;
  amount_cents: number;
  currency: string;
  fraud_session_id: string;
  fraud_action: string;
  fraud_risk_score: number;
  fraud_reason: string;
  fraud_flagged_at: string | null;
  deferred_result_pending: boolean;
  failure_code: string;
  failure_message: string;
  created_at: string;
  updated_at: string;
};

export type FraudAuditEntry = {
  event_id: string;
  kind: string;
  saga_state: string;
  details: unknown;
  created_at: string;
};

export type FraudReviewDetail = {
  review: FraudReviewSummary;
  audit: FraudAuditEntry[];
  trace_id: string;
};

export type FraudReviewAction = "resume" | "reject";

export type FraudReviewOutcome = {
  payment_id: string;
  status: string;
  failure_code: string;
  failure_message: string;
  updated_at: string;
  trace_id: string;
};

/** A non-2xx answer from the gateway, or from the proxy in front of it. */
export class FraudReviewError extends Error {
  readonly status: number;
  readonly code: string;

  constructor(status: number, code: string, message: string) {
    super(message);
    this.name = "FraudReviewError";
    this.status = status;
    this.code = code;
  }
}

const basePath = "/api/admin/fraud-reviews";

export async function listFraudReviews(token: string): Promise<FraudReviewSummary[]> {
  const body = await callGateway<{ reviews?: FraudReviewSummary[] }>(token, "");
  return body.reviews ?? [];
}

export function getFraudReview(token: string, paymentId: string): Promise<FraudReviewDetail> {
  return callGateway<FraudReviewDetail>(token, `/${encodeURIComponent(paymentId)}`);
}

export function decideFraudReview(
  token: string,
  paymentId: string,
  action: FraudReviewAction,
  reason: string,
): Promise<FraudReviewOutcome> {
  return callGateway<FraudReviewOutcome>(token, `/${encodeURIComponent(paymentId)}/${action}`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ reason: reason.trim() }),
  });
}

async function callGateway<T>(token: string, path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  headers.set("authorization", `Bearer ${token.trim()}`);
  const response = await fetch(`${basePath}${path}`, { ...init, headers });
  const body: unknown = await response.json().catch(() => null);
  if (!response.ok) {
    throw new FraudReviewError(response.status, errorCode(body), errorMessage(body, response.status));
  }
  return body as T;
}

// The gateway answers {error:{code,message}}; the proxy's own failures are
// {error:"..."}.
function errorField(body: unknown): unknown {
  return typeof body === "object" && body !== null ? (body as { error?: unknown }).error : undefined;
}

function errorCode(body: unknown): string {
  const error = errorField(body);
  if (typeof error === "object" && error !== null) {
    const code = (error as { code?: unknown }).code;
    if (typeof code === "string" && code.length > 0) {
      return code;
    }
  }
  return "request_failed";
}

function errorMessage(body: unknown, status: number): string {
  const error = errorField(body);
  if (typeof error === "string" && error.length > 0) {
    return error;
  }
  if (typeof error === "object" && error !== null) {
    const message = (error as { message?: unknown }).message;
    if (typeof message === "string" && message.length > 0) {
      return message;
    }
  }
  return `Gateway responded with ${status}`;
}

export function formatAmount(cents: number, currency: string): string {
  try {
    return new Intl.NumberFormat("en-US", { style: "currency", currency }).format(cents / 100);
  } catch {
    return `${(cents / 100).toFixed(2)} ${currency}`;
  }
}

export function formatRiskScore(score: number): string {
  return `${Math.round(score * 100)}%`;
}
