import { afterEach, describe, expect, it, vi } from "vitest";

import { GET, POST } from "./route";

function context(path?: string[]) {
  return { params: Promise.resolve({ path }) };
}

function request(method: string, path: string, init: RequestInit = {}): Request {
  return new Request(`http://localhost/api/admin/fraud-reviews${path}`, { method, ...init });
}

function gatewayResponse(body: string, status = 200): Response {
  return new Response(body, { status, headers: { "content-type": "application/json" } });
}

const authorized = { authorization: "Bearer admin-token" };

afterEach(() => {
  vi.unstubAllGlobals();
  vi.unstubAllEnvs();
});

describe("fraud review proxy", () => {
  it("requires a bearer token", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    const response = await GET(request("GET", ""), context());

    expect(response.status).toBe(401);
    await expect(response.json()).resolves.toEqual({ error: "Missing bearer token" });
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("rejects paths that are not operator routes", async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);

    const unknown = [
      POST(request("POST", "/p1", { headers: authorized }), context(["p1"])),
      GET(request("GET", "/p1/resume", { headers: authorized }), context(["p1", "resume"])),
      POST(request("POST", "/p1/approve", { headers: authorized }), context(["p1", "approve"])),
      GET(request("GET", "/p1/audit/x", { headers: authorized }), context(["p1", "audit", "x"])),
    ];
    for (const response of await Promise.all(unknown)) {
      expect(response.status).toBe(404);
    }
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("forwards the queue read with the operator token", async () => {
    vi.stubEnv("GATEWAY_URL", "http://gateway.test");
    const fetchMock = vi.fn().mockResolvedValue(gatewayResponse('{"reviews":[]}'));
    vi.stubGlobal("fetch", fetchMock);

    const response = await GET(request("GET", "", { headers: authorized }), context());

    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("http://gateway.test/v1/fraud-reviews");
    expect(init.method).toBe("GET");
    expect(init.headers.authorization).toBe("Bearer admin-token");
    expect(init.body).toBeUndefined();
    expect(response.status).toBe(200);
    await expect(response.json()).resolves.toEqual({ reviews: [] });
  });

  it("forwards a detail read to the payment's review", async () => {
    vi.stubEnv("GATEWAY_URL", "http://gateway.test");
    const fetchMock = vi.fn().mockResolvedValue(gatewayResponse('{"review":{},"audit":[]}'));
    vi.stubGlobal("fetch", fetchMock);

    await GET(request("GET", "/p1", { headers: authorized }), context(["p1"]));

    expect(fetchMock.mock.calls[0][0]).toBe("http://gateway.test/v1/fraud-reviews/p1");
  });

  it("forwards a decision with its JSON body", async () => {
    vi.stubEnv("GATEWAY_URL", "http://gateway.test");
    const fetchMock = vi.fn().mockResolvedValue(gatewayResponse('{"status":"FAILED"}'));
    vi.stubGlobal("fetch", fetchMock);

    await POST(
      request("POST", "/p1/reject", {
        headers: { ...authorized, "content-type": "application/json" },
        body: '{"reason":"confirmed fraud"}',
      }),
      context(["p1", "reject"]),
    );

    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("http://gateway.test/v1/payments/p1/fraud-review/reject");
    expect(init.method).toBe("POST");
    expect(init.headers["content-type"]).toBe("application/json");
    expect(init.body).toBe('{"reason":"confirmed fraud"}');
  });

  it("defaults to the local gateway", async () => {
    vi.stubEnv("GATEWAY_URL", undefined);
    const fetchMock = vi.fn().mockResolvedValue(gatewayResponse('{"reviews":[]}'));
    vi.stubGlobal("fetch", fetchMock);

    await GET(request("GET", "", { headers: authorized }), context());

    expect(fetchMock.mock.calls[0][0]).toBe("http://127.0.0.1:8080/v1/fraud-reviews");
  });

  it("passes the gateway's error envelope through", async () => {
    const envelope = { error: { code: "forbidden", message: "the fraud review queue requires an administrator" } };
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(gatewayResponse(JSON.stringify(envelope), 403)));

    const response = await GET(request("GET", "", { headers: authorized }), context());

    expect(response.status).toBe(403);
    await expect(response.json()).resolves.toEqual(envelope);
  });

  it("reports an unreachable gateway as a bad gateway", async () => {
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("connection refused")));

    const response = await GET(request("GET", "", { headers: authorized }), context());

    expect(response.status).toBe(502);
    await expect(response.json()).resolves.toEqual({ error: "Failed to reach gateway" });
  });
});
