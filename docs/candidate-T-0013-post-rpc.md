# T-0013 create-post RPC candidate

Install the versioned function after the base schema exists and before running
this application revision:

```sh
psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f sql/004-create-post-v1.sql
```

The function is not removed by the current `sql/reset.sql` because that script
resets data tables without rebuilding the schema. After a full schema rebuild,
install this migration again before switching application code.

Rollback the application by restoring the previous `createPostHandler` from
parent `8dad6118f0323d9df3a9162c46d78f683372958a`; the SQL function may remain
installed while no callers use it. Drop it only at a safe boundary after all
callers have rolled back:

```sql
DROP FUNCTION public.create_post_v1(text, text, text, bytea, text, text, text, numeric, numeric, text);
```
