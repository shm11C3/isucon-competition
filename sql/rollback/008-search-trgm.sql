DROP INDEX IF EXISTS users_search_username_trgm;
DROP INDEX IF EXISTS users_search_display_name_trgm;
-- Keep pg_trgm installed: it may have been present before this migration or
-- be used by other indexes.
