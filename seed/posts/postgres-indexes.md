Most slow Postgres queries are not a Postgres problem. They are a missing or misused index, and `EXPLAIN (ANALYZE, BUFFERS)` tells you which.

## Column order in composite indexes

An index on `(user_id, created_at)` serves equality on `user_id` plus a range or sort on `created_at`. It does not help a query filtering only on `created_at`.

```sql
CREATE INDEX CONCURRENTLY posts_user_recent_idx
    ON posts (user_id, created_at DESC);
```

Put equality columns first and the range or sort column last. Use `CONCURRENTLY` on any table that takes live traffic, because a plain `CREATE INDEX` blocks writes for the whole build.

## Partial indexes

If 95% of queries look at published rows, do not index the drafts:

```sql
CREATE INDEX posts_published_idx
    ON posts (published_at DESC)
    WHERE status = 'published';
```

The index stays small, and writes to drafts do not pay for it. The query must repeat the same predicate for the planner to use it.

## Keyset pagination

`OFFSET 100000` reads and discards 100,000 rows, so deep pages get slower and slower. Paginate from the last row you saw:

```sql
SELECT id, title, published_at
FROM posts
WHERE status = 'published'
  AND (published_at, id) < ($1, $2)
ORDER BY published_at DESC, id DESC
LIMIT 20;
```

## Indexes are not free

Every index slows writes and takes up cache. Check for ones nobody uses:

```sql
SELECT relname, indexrelname, pg_size_pretty(pg_relation_size(indexrelid))
FROM pg_stat_user_indexes
WHERE idx_scan = 0
ORDER BY pg_relation_size(indexrelid) DESC;
```

Check replicas too before dropping anything, as these counters are per server.
