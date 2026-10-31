-- The open-window transaction inserts notifications from existing users for
-- the window it just created. Keep its users SHARE lock and notification
-- uniqueness; avoid two redundant FK trigger lookups per recipient.
BEGIN;
ALTER TABLE notifications
  DROP CONSTRAINT notifications_user_id_fkey,
  DROP CONSTRAINT notifications_posting_window_id_fkey;
COMMIT;
