-- New images are stored in image_objects_20261028 (reset truncates it each initialize).
-- UNLOGGED removes their bytes from WAL and replication: during the grand prix ~330 posts
-- (~120MB) no longer have to stream to every standby before COMMIT returns.
-- Standbys cannot read an UNLOGGED partition, so every image byte read must use the
-- primary (ISUREAL_IMAGE_DB_HOST). Data survives clean restarts; a crash truncates it.
ALTER TABLE image_objects_20261028 SET UNLOGGED;
