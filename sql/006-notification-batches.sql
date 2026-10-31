-- Run as the application schema owner so the writer, reader, and reset process
-- retain their normal access to this table. One row per window keeps the exact
-- per-user IDs while avoiding thousands of notification tuples and indexes.
BEGIN;
CREATE TABLE IF NOT EXISTS notification_batches (
  posting_window_id varchar(36) PRIMARY KEY REFERENCES posting_windows(id) ON DELETE RESTRICT,
  created_at timestamptz NOT NULL,
  recipients jsonb NOT NULL
);
COMMIT;
