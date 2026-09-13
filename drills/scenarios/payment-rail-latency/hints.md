## Tier 1

The gateway is fine and every service is green, so this is not a crash. Which
saga state are the failing transfers ending in? `GET /v1/payments/<id>` and the
Saga Health dashboard show the distribution — failures, not stalls.

## Tier 2

Open a failing payment's trace end to end in Jaeger. The saga reaches the
payment step and the rail call is there — but look at its duration and outcome.
The rail container is healthy, yet the call does not return in time.

## Tier 3

The edge between the payment-processor and the payment rail is slow: the charge
call exceeds `PAYMENT_RAIL_TIMEOUT` (2s) and is abandoned, so the payment fails
and the saga compensates. The rail is fine; the network path to it is not.
