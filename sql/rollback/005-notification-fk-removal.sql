BEGIN;
ALTER TABLE notifications
  ADD CONSTRAINT notifications_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE RESTRICT,
  ADD CONSTRAINT notifications_posting_window_id_fkey
    FOREIGN KEY (posting_window_id) REFERENCES posting_windows(id) ON DELETE RESTRICT;
COMMIT;
