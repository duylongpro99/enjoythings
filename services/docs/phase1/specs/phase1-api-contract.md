# Phase 1 Spec: API Contract

**Phase:** 1 - Monolith  
**Status:** Draft  
**Last updated:** 2026-06-02

## Purpose

Define the external HTTP/JSON contract for Phase 1 in one place so handlers, tests, README examples, and future clients use the same routes, status codes, and error format.

## Scope

The API contract includes:

- Route list.
- Request and response JSON shapes.
- Standard error envelope.
- Status code conventions.
- Pagination convention for ledger reads.
- Authentication header convention.

The API contract excludes OpenAPI generation, version negotiation beyond `/v1`, admin APIs, and frontend-specific behavior.

## Common Rules

Base path for business endpoints: `/v1`.

All `/v1/*` endpoints require:

```http
Authorization: Bearer <jwt>
Content-Type: application/json
```

`Content-Type` is required for requests with a body. Health endpoints do not require authentication.

Use snake_case JSON fields. Timestamps are RFC 3339 UTC strings. Money amounts are integer cents.

## Route Summary

| Method | Path | Auth | Success |
|---|---|---:|---:|
| `GET` | `/healthz` | No | `200` |
| `GET` | `/readyz` | No | `200` or `503` |
| `POST` | `/v1/wallets` | Yes | `201` |
| `GET` | `/v1/wallets/:id` | Yes | `200` |
| `GET` | `/v1/wallets/:id/balance` | Yes | `200` |
| `POST` | `/v1/transfers` | Yes | `201` |
| `GET` | `/v1/ledger/:wallet_id` | Yes | `200` |

## Error Envelope

All non-2xx responses use this shape:

```json
{
  "error": {
    "code": "invalid_request",
    "message": "request body is invalid"
  }
}
```

Optional field-level details may be included when safe and useful:

```json
{
  "error": {
    "code": "invalid_request",
    "message": "request body is invalid",
    "fields": {
      "amount": "must be greater than zero"
    }
  }
}
```

Do not include stack traces, SQL errors, secrets, raw JWTs, or internal package names in API responses.

## Status Code Conventions

| Status | Use |
|---:|---|
| `200` | Successful read or health check. |
| `201` | Successful resource creation or completed transfer creation. |
| `400` | Malformed JSON, invalid UUID, invalid cursor, invalid query param. |
| `401` | Missing, malformed, invalid, or expired JWT. |
| `404` | Resource not found or not visible to authenticated user. |
| `422` | Well-formed request violates business rule. |
| `500` | Unexpected server error. |
| `503` | Readiness dependency unavailable. |

## Wallets

### Create Wallet

`POST /v1/wallets`

Request:

```json
{"currency":"USD"}
```

Response `201`:

```json
{
  "id": "6ed87f1f-7c9d-48d6-b23a-4d6255028c5c",
  "user_id": "449b3f19-8b5b-4f4b-b5d0-8e77d97d5c84",
  "balance": 0,
  "currency": "USD",
  "created_at": "2026-06-02T00:00:00Z",
  "updated_at": "2026-06-02T00:00:00Z"
}
```

### Get Wallet

`GET /v1/wallets/:id`

Response `200`: same shape as create wallet response.

### Get Balance

`GET /v1/wallets/:id/balance`

Response `200`:

```json
{
  "wallet_id": "6ed87f1f-7c9d-48d6-b23a-4d6255028c5c",
  "balance": 1500,
  "currency": "USD"
}
```

## Transfers

### Create Transfer

`POST /v1/transfers`

Request:

```json
{
  "from_wallet_id": "77787174-e221-49de-bf16-5834e0d250a1",
  "to_wallet_id": "a572d276-3292-4c0e-b4f8-e5256d2d814c",
  "amount": 1250
}
```

Response `201`:

```json
{
  "id": "68e09292-b720-4cfd-a99d-c9f265dcb59b",
  "from_wallet_id": "77787174-e221-49de-bf16-5834e0d250a1",
  "to_wallet_id": "a572d276-3292-4c0e-b4f8-e5256d2d814c",
  "amount": 1250,
  "status": "completed",
  "created_at": "2026-06-02T00:00:00Z",
  "balances": {
    "from": 3750,
    "to": 6250
  }
}
```

## Ledger

### List Ledger Entries

`GET /v1/ledger/:wallet_id?limit=50&cursor=<cursor>`

Response `200`:

```json
{
  "wallet_id": "77787174-e221-49de-bf16-5834e0d250a1",
  "entries": [
    {
      "id": "bc1f6d24-ff58-4fc7-8493-c5d380035b79",
      "transfer_id": "68e09292-b720-4cfd-a99d-c9f265dcb59b",
      "direction": "debit",
      "amount": 1250,
      "balance_after": 3750,
      "created_at": "2026-06-02T00:00:00Z"
    }
  ],
  "next_cursor": null
}
```

`next_cursor` is `null` when there are no more results. When present, clients pass it unchanged as the next request's `cursor` query parameter.

## Acceptance Criteria

- Every handler response matches the shapes in this contract.
- Every error response uses the standard envelope.
- Status codes match the conventions in this contract and the capability specs.
- README examples use these routes and JSON fields.
- Tests assert response status codes and representative JSON fields for each endpoint.
