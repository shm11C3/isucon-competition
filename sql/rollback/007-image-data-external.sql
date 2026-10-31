-- Restore the original policy for subsequent writes; existing bytes remain
-- readable throughout and do not require a rewrite.
BEGIN;
SET LOCAL lock_timeout = '2s';
SET LOCAL statement_timeout = '10s';
ALTER TABLE image_objects ALTER COLUMN data SET STORAGE EXTENDED;
COMMIT;
