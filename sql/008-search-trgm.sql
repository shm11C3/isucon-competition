-- Accelerate normalized literal-substring user search. The C collation keeps
-- LIKE matching bytewise-equivalent to the existing strpos predicates.
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE INDEX IF NOT EXISTS users_search_username_trgm
  ON users USING GIN ((search_username COLLATE "C") gin_trgm_ops)
  WHERE status = 'active';

CREATE INDEX IF NOT EXISTS users_search_display_name_trgm
  ON users USING GIN ((search_display_name COLLATE "C") gin_trgm_ops)
  WHERE status = 'active';
