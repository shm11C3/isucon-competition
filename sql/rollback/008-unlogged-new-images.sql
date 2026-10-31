-- Rewrites the partition into WAL (size-proportional); run only when writes are idle.
ALTER TABLE image_objects_20261028 SET LOGGED;
