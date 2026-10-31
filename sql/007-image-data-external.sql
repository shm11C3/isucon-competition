-- Apply on the primary at an idle deployment boundary. Existing image bytes
-- are not rewritten; the storage policy applies to subsequent writes.
BEGIN;
SET LOCAL lock_timeout = '2s';
SET LOCAL statement_timeout = '10s';
ALTER TABLE image_objects ALTER COLUMN data SET STORAGE EXTERNAL;
COMMIT;
