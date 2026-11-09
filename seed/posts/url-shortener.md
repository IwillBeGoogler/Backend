A URL shortener looks like an interview warm-up, but the production version has three real problems: generating keys without coordination, serving reads at very low latency, and counting clicks without slowing the redirect.

## Traffic estimate

Assume 100 million new links a month and a 100:1 read ratio.

- Writes: 100M / 2.6M seconds is about **40 per second**
- Reads: about **4,000 per second**, with peaks of 3x
- Storage: 500 bytes per row, 6 billion rows over five years, about **3 TB**

This is a read-heavy system with a small working set, so a cache will do most of the work.

## Key generation

Seven base62 characters give 62^7, about 3.5 trillion keys. Hashing the long URL and truncating causes collisions you must retry. A simpler option is a counter encoded in base62, with each app server leasing a block of 1,000 IDs:

```sql
UPDATE id_blocks
SET next_id = next_id + 1000
WHERE name = 'links'
RETURNING next_id - 1000 AS block_start;
```

Blocks avoid a database round trip per link. A crash wastes at most 1,000 IDs, which does not matter at this scale. Sequential keys are guessable, so shuffle them with a keyed permutation if links can be private.

## Redirect path

Use `302` if you need analytics on every click, since `301` is cached by browsers and those clicks never reach you. Lookup order is CDN, then Redis, then the database. A small share of links gets most of the traffic, so a cache of 20% of daily keys usually reaches a hit rate above 90%.

## Counting clicks

Never update a counter inside the redirect. Publish an event to Kafka and aggregate it asynchronously. If the pipeline stalls, redirects keep working and the numbers catch up later.

> Redirects are the product. Analytics are allowed to be late; redirects are not.
