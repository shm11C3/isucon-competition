-- Drain and stop application processes before applying this rollback. Expand every batch
-- into the legacy representation before reverting application code.
BEGIN;
INSERT INTO notifications (id, user_id, posting_window_id, created_at)
SELECT recipient.value, recipient.key, batch.posting_window_id, batch.created_at
FROM notification_batches AS batch
CROSS JOIN LATERAL jsonb_each_text(batch.recipients) AS recipient(key, value);
DROP TABLE notification_batches;
COMMIT;
