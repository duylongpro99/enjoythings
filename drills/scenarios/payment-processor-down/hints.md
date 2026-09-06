## Tier 1

The gateway is fine, so the problem is downstream of it. Which saga state are
the stuck payments sitting in? `GET /v1/payments/<id>` tells you, and the Saga
Health dashboard shows the distribution.

## Tier 2

Every saga state has an owner. Open Prometheus and look at the `up` series:
one scrape target that should be reporting is not.

## Tier 3

The payment-processor consumer is not running. Payments reach
`PAYMENT_PROCESSING` and wait for a `payment.completed` event that nobody is
producing. Kafka is holding the backlog.
