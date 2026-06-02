CREATE UNIQUE INDEX IF NOT EXISTS ledger_entries_transfer_direction_unique_idx
  ON ledger_entries(transfer_id, direction);
