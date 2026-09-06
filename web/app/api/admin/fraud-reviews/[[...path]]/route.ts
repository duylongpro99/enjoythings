import { defaultGatewayUrl, gatewayPath } from "@/lib/fraud-review-proxy";

export const runtime = "nodejs";

type RouteContext = { params: Promise<{ path?: string[] }> };

// The browser cannot call the gateway directly: it has no CORS handling and
// lives on another origin. This handler forwards the operator's bearer token
// and returns the gateway's answer verbatim, the way /api/chat fronts FastAPI.
async function proxy(req: Request, context: RouteContext): Promise<Response> {
  const { path = [] } = await context.params;
  const target = gatewayPath(req.method, path);
  if (!target) {
    return Response.json({ error: "Not found" }, { status: 404 });
  }
  const authorization = req.headers.get("authorization");
  if (!authorization) {
    return Response.json({ error: "Missing bearer token" }, { status: 401 });
  }

  const headers: Record<string, string> = { authorization };
  const init: RequestInit = { method: req.method, headers, signal: req.signal };
  if (req.method === "POST") {
    headers["content-type"] = "application/json";
    init.body = await req.text();
  }

  const gatewayUrl = process.env.GATEWAY_URL ?? defaultGatewayUrl;
  let upstream: Response;
  try {
    upstream = await fetch(`${gatewayUrl}${target}`, init);
  } catch {
    return Response.json({ error: "Failed to reach gateway" }, { status: 502 });
  }
  return new Response(upstream.body, {
    status: upstream.status,
    headers: { "content-type": upstream.headers.get("content-type") ?? "application/json" },
  });
}

export const GET = proxy;
export const POST = proxy;
