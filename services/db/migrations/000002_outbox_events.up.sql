CREATE TABLE IF NOT EXISTS outbox_events (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  topic TEXT NOT NULL,
  partition_key TEXT NOT NULL,
  payload JSONB NOT NULL,
  traceparent TEXT NOT NULL DEFAULT '',
  tracestate TEXT NOT NULL DEFAULT '',
  claimed_at TIMESTAMPTZ,
  published_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE outbox_events
  ADD COLUMN IF NOT EXISTS traceparent TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS tracestate TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS outbox_events_unpublished_created_idx
  ON outbox_events (created_at, id)
  WHERE published_at IS NULL;
