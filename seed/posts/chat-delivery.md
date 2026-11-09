A chat message feels simple until a user loses signal in a lift. The hard requirement is that a message is never lost, and never appears twice, on an unreliable network.

## Connections

Each client holds one long-lived connection to a gateway. A gateway handles tens of thousands of idle connections, and it keeps no durable state. A presence store maps `user_id` to the gateway that currently holds them.

## Delivery sequence

1. The client creates a `client_msg_id` (a UUID) and sends the message.
2. The server writes it to durable storage, with a unique constraint on the sender and that ID.
3. The server acknowledges with the assigned sequence number.
4. The message goes to the recipient's gateway, if they are online.
5. The recipient acknowledges, and their read position moves forward.

The client retries until it gets the acknowledgement in step 3. If the first attempt succeeded but the acknowledgement was lost, the unique constraint turns the retry into a no-op. That is effectively-once delivery, built from at-least-once delivery plus idempotency.

## Ordering

Global ordering is not needed; ordering **per conversation** is. Allocate a monotonic sequence per conversation:

```sql
UPDATE conversations
SET last_seq = last_seq + 1
WHERE id = $1
RETURNING last_seq;
```

The row lock serialises writers within a conversation only. Clients detect a gap in the sequence and fetch the missing range.

## Reconnecting

On reconnect the client sends the last sequence it saw for each conversation, and the server returns what is newer. This one path replaces most "sync" special cases, so make it fast.

## Large groups

Fanning out on write to a 100,000-member group is wasteful, since most members are offline. Store the message once, notify online members, and let everyone else pull when they open the conversation.
