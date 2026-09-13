# Toxiproxy overlay — network faults

This overlay adds a [Toxiproxy](https://github.com/Shopify/toxiproxy) container
to the platform stack so the drills adapter can inject the `net.latency` and
`net.partition` primitives (spec §5).

## How an edge is faulted

The Compose stack uses no named networks — every service dials its peers by
service name on the default project network. Toxiproxy therefore cannot sit on
an edge transparently: the **client** of the edge is re-pointed to dial
`toxiproxy:<listen-port>`, which forwards to the real upstream. So a `net.*`
fault is only possible on an edge whose client URL is an environment variable.

The adapter ships a small edge table (`edge_lookup` in
`drills/targets/enjoythings/lib.sh`); an unsupported pair fails at inject time,
which is why a scenario naming an unsupported edge fails validation rather than
half-injecting.

| Edge `a → b` | Client env var | Upstream | Proxy / listen |
| --- | --- | --- | --- |
| `payment-processor → stub-payment-rail` | `PAYMENT_RAIL_URL` | `stub-payment-rail:18090` | `pp-rail` / `18190` |
| `fraud-worker → ledger` | `LEDGER_GRPC_ADDR` | `ledger:9091` | `fw-ledger` / `19091` |
| `fraud-worker → verification` | `VERIFICATION_GRPC_ADDR` | `verification:9094` | `fw-verif` / `19094` |

## Semantics

- `net.latency <a> <b> <ms>` — creates the proxy (enabled) and adds a `latency`
  toxic of `<ms>` milliseconds, then re-points the client. The edge stays up but
  slow.
- `net.partition <a> <b>` — creates the proxy and **disables** it (`toggle`), so
  the connection is severed while both endpoints stay `healthy`. This produces
  connection failures, not hangs; a "hang, both healthy" fault is a large
  `latency` toxic instead.

`revert` deletes the proxy and restores the client's real upstream (`net.clear`
+ `env.unset`, in that order). The admin API is exposed on host `:8474` for the
engineer to inspect (`toxiproxy-cli list`).
