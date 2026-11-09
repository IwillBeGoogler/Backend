A service that writes to its database and then publishes to Kafka has a bug. If the process dies between those two steps, the row exists but the event does not. Reversing the order gives you the opposite failure.

## The outbox

Write the event to a table in the same transaction as the business change:

```sql
BEGIN;
INSERT INTO orders (id, user_id, total_cents) VALUES ($1, $2, $3);
INSERT INTO outbox (aggregate_id, type, payload)
VALUES ($1, 'order.created', $4);
COMMIT;
```

Either both rows exist or neither does. A separate relay reads the table and publishes.

## The relay

Polling is the simplest option, and it is enough for most systems:

```sql
SELECT id, type, payload FROM outbox
WHERE published_at IS NULL
ORDER BY id
LIMIT 500
FOR UPDATE SKIP LOCKED;
```

`SKIP LOCKED` lets several relays run without blocking each other, though it gives up strict ordering between them. If you need order per aggregate, partition relays by `aggregate_id`. Change data capture with Debezium reads the WAL instead, which gives lower latency in exchange for another system to operate.

## Consumers must be idempotent

The relay can publish and then crash before marking the row, so the event goes out twice. This is at-least-once delivery and it cannot be designed away. Consumers record the event IDs they have handled, in the same transaction as their own effects:

```sql
INSERT INTO processed_events (event_id) VALUES ($1)
ON CONFLICT DO NOTHING;
```

If no row was inserted, skip the event.

## Housekeeping

Delete published rows on a schedule, or partition the table by day and drop old partitions. An outbox nobody cleans becomes the largest table in the database.
