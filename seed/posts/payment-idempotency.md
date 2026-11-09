Charging a customer twice is one of the worst bugs a payment system can have, and a plain network timeout is enough to cause it. The client does not know whether the charge happened, so it retries.

## Idempotency keys

The client generates a key per payment attempt and sends it with every retry:

```http
POST /v1/payments
Idempotency-Key: 5f1c2a9e-7d4b-4c11-9a3e-2b6f0c8d1e77
```

```sql
CREATE TABLE idempotency_keys (
    key           text PRIMARY KEY,
    request_hash  text NOT NULL,
    status        text NOT NULL,
    response      jsonb,
    created_at    timestamptz NOT NULL DEFAULT now()
);
```

Three cases follow. A new key claims the row and proceeds. A finished key returns the stored response. A key still in progress returns `409`, so the client backs off. If the same key arrives with a different request hash, reject it: that is a client bug.

## State machine

A payment moves through explicit states: `created`, `authorized`, `captured`, `failed`, `refunded`. Enforce transitions in the update itself:

```sql
UPDATE payments SET status = 'captured'
WHERE id = $1 AND status = 'authorized';
```

Zero rows affected means someone else got there first.

## The provider timeout

When the provider times out, the state is **unknown**, not failed. Never mark it failed and never retry with a new key. Query the provider using your reference, or wait for its webhook.

## Ledger and reconciliation

Store money in a double-entry ledger as integer minor units, and never as floats. Each night, compare your ledger with the provider's settlement file. Every system with money in it eventually disagrees with the bank, and reconciliation is how you find out before your customers do.
