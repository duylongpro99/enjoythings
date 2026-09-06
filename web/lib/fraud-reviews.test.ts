import { afterEach, describe, expect, it, vi } from "vitest";

import {
  FraudReviewError,
  decideFraudReview,
  formatAmount,
  formatRiskScore,
  getFraudReview,
  listFraudReviews,
} from "./fraud-reviews";

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("fraud review data layer", () => {
  it("lists the queue with the bearer token", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ reviews: [{ payment_id: "p1" }], trace_id: "t" }));
    vi.stubGlobal("fetch", fetchMock);

    const reviews = await listFraudReviews("  admin-token  ");

    expect(reviews).toEqual([{ payment_id: "p1" }]);
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("/api/admin/fraud-reviews");
    expect((init.headers as Headers).get("authorization")).toBe("Bearer admin-token");
  });

  it("treats a queue without reviews as empty", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ trace_id: "t" })));

    await expect(listFraudReviews("admin-token")).resolves.toEqual([]);
  });

  it("reads one review by payment id", async () => {
    const detail = { review: { payment_id: "p 1" }, audit: [], trace_id: "t" };
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(detail));
    vi.stubGlobal("fetch", fetchMock);

    await expect(getFraudReview("admin-token", "p 1")).resolves.toEqual(detail);
    expect(fetchMock.mock.calls[0][0]).toBe("/api/admin/fraud-reviews/p%201");
  });

  it("posts a decision with its reason", async () => {
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ payment_id: "p1", status: "FAILED" }));
    vi.stubGlobal("fetch", fetchMock);

    const outcome = await decideFraudReview("admin-token", "p1", "reject", "  confirmed fraud ");

    expect(outcome.status).toBe("FAILED");
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("/api/admin/fraud-reviews/p1/reject");
    expect(init.method).toBe("POST");
    expect((init.headers as Headers).get("content-type")).toBe("application/json");
    expect((init.headers as Headers).get("authorization")).toBe("Bearer admin-token");
    expect(JSON.parse(init.body as string)).toEqual({ reason: "confirmed fraud" });
  });

  it("surfaces the gateway error envelope", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        jsonResponse({ error: { code: "forbidden", message: "the fraud review queue requires an administrator" } }, 403),
      ),
    );

    const failure = await listFraudReviews("user-token").catch((error: unknown) => error);

    expect(failure).toBeInstanceOf(FraudReviewError);
    expect(failure).toMatchObject({
      status: 403,
      code: "forbidden",
      message: "the fraud review queue requires an administrator",
    });
  });

  it("surfaces the proxy's own errors", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(jsonResponse({ error: "Missing bearer token" }, 401)));

    await expect(listFraudReviews("")).rejects.toMatchObject({
      status: 401,
      code: "request_failed",
      message: "Missing bearer token",
    });
  });

  it("falls back to the status when the body is not JSON", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("boom", { status: 502 })));

    await expect(getFraudReview("admin-token", "p1")).rejects.toMatchObject({
      status: 502,
      message: "Gateway responded with 502",
    });
  });

  it("formats amounts and risk scores for the queue", () => {
    expect(formatAmount(1250, "USD")).toBe("$12.50");
    expect(formatAmount(1250, "not-a-currency")).toBe("12.50 not-a-currency");
    expect(formatRiskScore(0.951)).toBe("95%");
  });
});
