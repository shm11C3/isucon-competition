-- Ordinary (logged) append table; install before enabling the frontend flag.
CREATE TABLE public.replica_commit_barriers (
  id uuid PRIMARY KEY,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
