export const defaultGatewayUrl = "http://127.0.0.1:8080";

/**
 * Maps a request under /api/admin/fraud-reviews onto the gateway route it
 * proxies. Only the three operator routes exist; anything else is null.
 */
export function gatewayPath(method: string, segments: readonly string[]): string | null {
  const [paymentId, action, ...rest] = segments;
  if (rest.length > 0) {
    return null;
  }
  if (method === "GET" && paymentId === undefined) {
    return "/v1/fraud-reviews";
  }
  if (method === "GET" && action === undefined) {
    return `/v1/fraud-reviews/${encodeURIComponent(paymentId)}`;
  }
  if (method === "POST" && paymentId !== undefined && (action === "resume" || action === "reject")) {
    return `/v1/payments/${encodeURIComponent(paymentId)}/fraud-review/${action}`;
  }
  return null;
}
