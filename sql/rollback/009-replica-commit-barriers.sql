-- Disable ISUREAL_COMMIT_BARRIER and drain old callers before dropping.
DROP TABLE public.replica_commit_barriers;
