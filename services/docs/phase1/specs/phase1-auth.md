# Phase 1 Spec: Authentication

**Phase:** 1 - Monolith  
**Priority:** P1  
**Status:** Draft  
**Last updated:** 2026-06-02

## Purpose

Protect all Phase 1 business endpoints with JWT authentication. Phase 1 uses a static HMAC signing secret to keep local development simple. RS256 key management is intentionally deferred to Phase 2.

## Scope

Authentication includes:

- Bearer token extraction from the `Authorization` header.
- JWT signature and expiry validation.
- Required `user_id` and `role` claims.
- Request context injection for authenticated user data.
- Consistent `401 Unauthorized` responses.
- Test-token generation guidance for local development and automated tests.

Authentication excludes user registration, password login, refresh tokens, RBAC enforcement beyond capturing `role`, RS256/JWKS, sessions, and external identity providers.

## Claims

Required claims:

| Claim | Type | Required | Notes |
|---|---|---:|---|
| `user_id` | UUID string | Yes | Used for wallet ownership checks. |
| `role` | string | Yes | Stored in context for future authorization. |
| `exp` | Unix timestamp | Yes | Expired tokens return `401`. |
| `iat` | Unix timestamp | No | Accepted if present. |

Invalid UUIDs in `user_id` are authentication failures, not validation failures.

## Protected Routes

All `/v1/*` endpoints require authentication:

- `POST /v1/wallets`
- `GET /v1/wallets/:id`
- `GET /v1/wallets/:id/balance`
- `POST /v1/transfers`
- `GET /v1/ledger/:wallet_id`

Health endpoints are public:

- `GET /healthz`
- `GET /readyz`

## Middleware Behavior

Request handling order:

1. Read `Authorization` header.
2. Require `Bearer <token>` format.
3. Validate token with `JWT_SECRET` using HS256.
4. Verify token expiry.
5. Parse `user_id` as UUID.
6. Store authenticated principal in request context.
7. Call next handler.

Context value:

```go
type Principal struct {
    UserID uuid.UUID
    Role   string
}
```

Use a typed context key. Do not store claims in untyped maps outside the auth package.

## Error Responses

All authentication failures return `401 Unauthorized` with the same public message. Do not reveal whether the token was missing, malformed, expired, or signed incorrectly.

```json
{
  "error": {
    "code": "unauthorized",
    "message": "authentication required"
  }
}
```

The server may log the failure category for debugging, but logs must not include raw tokens.

## Local Test Tokens

The project should provide one safe local path for generating development tokens, either:

- A small documented Go command under `cmd/devtoken`, or
- A README snippet using the same JWT library as the app.

Generated tokens must be clearly marked as local-development only. Do not commit real user tokens.

## Testing Requirements

- Missing `Authorization` header returns `401`.
- Non-Bearer auth scheme returns `401`.
- Malformed token returns `401`.
- Wrong signature returns `401`.
- Expired token returns `401`.
- Missing `user_id`, missing `role`, or invalid `user_id` returns `401`.
- Valid token injects `Principal` and calls the next handler.
- `/healthz` and `/readyz` remain public.

## Acceptance Criteria

- Every `/v1/*` route is behind JWT middleware.
- Valid tokens allow requests to reach business handlers.
- Invalid or expired tokens consistently return `401`.
- Business handlers can retrieve `Principal.UserID` from context.
- No logs include raw JWTs or secrets.
