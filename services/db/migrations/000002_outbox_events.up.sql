CREATE TABLE IF NOT EXISTS outbox_events (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  topic TEXT NOT NULL,
  partition_key TEXT NOT NULL,
  payload JSONB NOT NULL,
  claimed_at TIMESTAMPTZ,
  published_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS outbox_events_unpublished_created_idx
  ON outbox_events (created_at, id)
  WHERE published_at IS NULL;
