-- name: CreateOutboxEvent :one
INSERT INTO outbox_events (topic, partition_key, payload)
VALUES ($1, $2, $3)
RETURNING id, topic, partition_key, payload, claimed_at, published_at, created_at;

-- name: ClaimUnpublishedOutboxEvents :many
WITH claimed AS (
  SELECT id
  FROM outbox_events
  WHERE published_at IS NULL
  ORDER BY created_at, id
  LIMIT $1
  FOR UPDATE SKIP LOCKED
)
UPDATE outbox_events AS outbox
SET claimed_at = now()
FROM claimed
WHERE outbox.id = claimed.id
RETURNING outbox.id, outbox.topic, outbox.partition_key, outbox.payload, outbox.claimed_at, outbox.published_at, outbox.created_at;

-- name: MarkOutboxEventPublished :exec
UPDATE outbox_events
SET published_at = now()
WHERE id = $1;
