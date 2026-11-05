# Diagram realtime over MQTT

The diagram workspace's realtime channels (presence, tree/file/comment events, live co-editing with Yjs, cursors, audio-room
signaling) run over an MQTT 5 broker, EMQX 5.x. They replace the old WebSockets `/ws/files/{id}`, `/ws/files/{id}/yjs` and
`/ws/projects/{id}`. This document is the protocol reference for clients. Code: `internal/interfaces/diagramhub` (server side
of the protocol), `internal/domain/diagram/topic` (topic space), `internal/application/diagram_mqtt.go` (auth and access rules).

Contents: [Overview](#overview) / [Connecting](#connecting) / [Topics](#topics) / [Access rules](#access-rules) /
[Payloads](#payloads) / [Yjs](#yjs-live-co-editing) / [Awareness](#awareness-cursors) / [Presence](#presence-and-roster) /
[Audio rooms](#audio-rooms) / [Control messages](#control-messages) / [Reconnecting](#reconnecting) / [QoS table](#qos-retain-expiry) /
[Deployment](#deployment) / [Server design notes](#server-design-notes) / [Old WebSocket mapping](#old-websocket-to-mqtt-mapping)

## Overview

```
browser --(MQTT 5 over WebSocket, same origin /mqtt)--> proxy --> EMQX :8083
API instances --(MQTT 5 over TCP :1883, superuser "api")--> EMQX
EMQX --(HTTP, bearer secret)--> API /api/internal/mqtt/auth and /acl     (who may connect, who may publish/subscribe what)
```

* Browsers publish only to topics that carry **their own client id**; the server republishes what it has checked. A browser
  can never publish to a topic other browsers read, except through the server. That is how identity (names on cursors, roster)
  is guaranteed.
* The API instances share the work with shared subscriptions (`$share/dg/...`): each message published by a browser is handled
  by exactly one instance.
* Everything a client needs at start (document, tree, role, version, tree revision) still comes from REST. MQTT carries what
  changes afterwards. There is no `hello` message any more.
* MQTT **version 5 is required** (user properties carry the origin of a message; reason codes carry refusals).

## Connecting

1. Ask the API where the broker is: `GET /api/diagrams/realtime/config` (session, or `X-Share-Token`) returns
   `{"wsPath":"/mqtt","collab":true,"presenceEveryMs":30000,"awarenessMaxPerSec":15}`. Connect to
   `ws(s)://<current host><wsPath>` (same origin; the site's proxy routes it to EMQX, see [Deployment](#deployment)).
2. Credentials, one of:

   | who | client id | username | password |
   |---|---|---|---|
   | signed-in user | `u<userId>.<nonce>` | `ticket` | a single-use ticket from `POST /api/me/stream-ticket` (`{ticket, expires_in: 30}`) |
   | share-link holder (no account, or an account that is not a member) | `g.<nonce>` | `share` | the share token |

   * `<nonce>` is `[A-Za-z0-9_-]{8,48}`, new for **every connection** (e.g. 16 random bytes in base64url).
   * The user id in the client id must be the ticket's owner. A client id is how the server knows who is speaking; one that does
     not match the login is refused (`CONNACK 0x87`).
   * A ticket is consumed by the CONNECT. **Every (re)connect needs a new ticket.** Do not rely on a client library's built-in
     reconnect with a fixed password (see [Reconnecting](#reconnecting)).
   * A signed-in user who opens a project they are not a member of through a share link connects as a guest (`g.` + `share`),
     exactly as the old WebSocket treated `?share=`.
3. CONNECT options: protocol version 5, `cleanStart: true`, `keepalive: 30`, session expiry 0 (the default). No message
   queue is kept for you: after a reconnect, resynchronise (see [Reconnecting](#reconnecting)).
4. If the client edits or shows presence in a file, set a **Last Will** (see [Presence](#presence-and-roster)): topic
   `dg/f/{fileId}/presence/{clientId}`, empty payload, QoS 1, retain false.
5. A refused login gives `CONNACK` reason `0x86` or `0x87` (bad ticket/token, or a client id that does not match). Fetch a
   new ticket once; if it fails again, treat the session as signed out.

**One connection per open file.** The Last Will is fixed at CONNECT, so a connection serves one file (plus its project's tree
topics). When the user switches file, publish the empty presence for the old file, disconnect, and connect again with a new
client id and a Will for the new file. A page that only shows the project tree connects without a Will and without presence.

Send the connection's client id as the `X-Client-Id` header on every REST call you make from that page. The events a request
causes carry it as their `origin`, so the page that made the change can drop them.

## Topics

`{c}` is a client id, `{f}` a file id, `{p}` a project id. "server" is the API's superuser client.

| topic | publisher | subscriber | payload | QoS |
|---|---|---|---|---|
| `dg/p/{p}/events` | server | clients of the project | JSON event | 1 |
| `dg/f/{f}/events` | server | clients of the file | JSON event | 1 |
| `dg/p/{p}/roster/{f}/{c}` | server | project clients (`dg/p/{p}/roster/+/+`) | JSON roster entry, **retained** | 0 |
| `dg/f/{f}/roster/{c}` | server | file clients (`dg/f/{f}/roster/+`) | JSON roster entry, **retained** | 0 |
| `dg/f/{f}/presence/{c}` | client `{c}` | server (shared) | any non-empty payload; **empty = left** (also the Last Will) | 0 (Will: 1) |
| `dg/f/{f}/yjs/req/{c}` | client `{c}`, any role | server (shared) | binary: sync step 1 | 1 |
| `dg/f/{f}/yjs/up/{c}` | client `{c}`, **editors only** | server (shared) | binary: sync update / step 2 / compaction snapshot | 1 |
| `dg/f/{f}/yjs/down` | server | file clients | binary: sync update (property `origin`) | 1 |
| `dg/f/{f}/yjs/sync/{c}` | server | client `{c}` | binary: join answer, compaction hint | 1 |
| `dg/f/{f}/awareness/up/{c}` | client `{c}`, any role | server (shared) | binary: awareness frame | 0 |
| `dg/f/{f}/awareness/out` | server | file clients | binary: awareness frame (property `origin`) | 0 |
| `dg/f/{f}/rtc/up/{c}` | client `{c}`, any role | server (shared) | JSON audio-room command | 1 |
| `dg/f/{f}/rtc/{c}` | server | client `{c}` | JSON audio-room message | 1 |
| `dg/c/{c}/ctl` | server | client `{c}` | JSON control message | 1 |

Differences from the first sketch of the protocol, and why:

* `yjs/up`, `awareness/up`, `rtc/up` and `yjs/req` carry the client id. The broker cannot tell subscribers who published, and
  the server must know whose update or cursor it is looking at (role, name, awareness-id ownership). Because the access rules
  only let a client publish to a topic with *its* id, the id is trustworthy.
* `yjs/req` exists because viewers must be able to ask for the document but must not be able to publish updates: the access
  rule for `yjs/up` is "editor", the one for `yjs/req` is "viewer".
* Presence is published to `presence/{c}` (not retained, nobody can subscribe to it) and the *server* publishes the retained
  `roster` entries. Retaining the client's own message would be invisible to everybody, since clients cannot subscribe to it
  and shared subscriptions never receive retained messages. The Last Will clears the roster (via the server).
* The 4404/1012 control codes travel on the file's `events` topic (`{"type":"ctl",...}`), not only on `dg/c/{c}/ctl`: the server
  cannot know cross-instance which clients are on a file. `dg/c/{c}/ctl` carries what is addressed to one client.

## Access rules

Decided by `POST /api/internal/mqtt/acl` with the REST routes' own rules (`fileAccessVia` / `projectAccessVia`), cached per client
and file for 30 s in the API, and by EMQX for 30 s per client/topic/action (so a removed member or revoked share link loses
access within about a minute; a deleted file at once for new connections).

| action | topic | allowed when |
|---|---|---|
| subscribe | `dg/p/{p}/events`, `dg/p/{p}/roster/+/+` | viewer of project `{p}` (a file-scoped share link does **not** open project topics) |
| subscribe | `dg/f/{f}/events`, `dg/f/{f}/roster/+`, `dg/f/{f}/yjs/down`, `dg/f/{f}/awareness/out` | viewer of file `{f}` |
| subscribe | `dg/f/{f}/yjs/sync/{c}`, `dg/f/{f}/rtc/{c}` | `{c}` is your client id and you are viewer of `{f}` |
| subscribe | `dg/c/{c}/ctl` | `{c}` is your client id |
| publish | `dg/f/{f}/yjs/up/{c}` | `{c}` is yours, you are **editor** or better, `COLLAB_ENABLED` |
| publish | `dg/f/{f}/yjs/req/{c}`, `dg/f/{f}/awareness/up/{c}` | `{c}` is yours, viewer, `COLLAB_ENABLED` |
| publish | `dg/f/{f}/presence/{c}`, `dg/f/{f}/rtc/up/{c}` | `{c}` is yours, viewer |
| anything else | | denied (`$share/...`, `$SYS/#`, `#`, `dg/#`, other wildcards, other clients' topics, server-only topics) |

A denied SUBSCRIBE comes back as SUBACK reason `0x87`. A denied PUBLISH at QoS 1 comes back as PUBACK reason `0x87` (and the
message is dropped). Role names: viewer < commenter < editor < owner. Viewers and commenters read and show cursors but never
write Yjs; when a write is refused for its role the server also sends `{"type":"denied","fileId":...,"reason":"read-only"}` on
`dg/c/{c}/ctl`.

A file that does not exist (deleted) is "not found": subscribing fails with `0x87`. Treat a refused subscription to a file's
topics right after opening it as "gone or no access" and confirm with `GET /api/diagrams/files/{id}` (404/403).

## Payloads

### Events (JSON, UTF-8)

Property `origin` (MQTT 5 user property, key `origin`) is the client id of the page that caused the event, when known (the
`X-Client-Id` of the REST request). **Ignore events whose origin is your own client id.** Events expire after 60 s for a
subscriber that is away (clean sessions drop them anyway).

On `dg/p/{p}/events` (the tree): `tree.updated`, `file.updated`, `library.updated`, comment events (these are also sent here),
`ctl`. On `dg/f/{f}/events`: `file.updated`, `comment.*`, `rtc.roster`, `ctl`.

```jsonc
{"type":"tree.updated","projectId":"..","rev":12,"by":{"userId":"3","name":"Ann"},"reason":"file.created","folderId":"..","fileId":".."}
{"type":"file.updated","projectId":"..","fileId":"..","version":7,"name":"x","kind":"sdd","updatedAt":1700000000000,"by":{"userId":"3","name":"Ann"},"restored":true}
{"type":"library.updated","projectId":"..","rev":3,"by":{"share":true}}
{"type":"comment.created","fileId":"..","comment":{ /* same shape as the REST comment */ }}   // also comment.updated
{"type":"comment.deleted","fileId":"..","commentId":".."}
{"type":"rtc.roster","fileId":"..","peers":[{"clientId":"..","userId":"3","name":"Ann"}]}
{"type":"ctl","code":4404,"reason":"deleted"}                                                    // see Control messages
```

`by` is `{userId,name}` (user ids are strings) or `{share:true}` for a share-link save. `reason`, `folderId`, `fileId`,
`restored` are present only when they apply. These are the shapes the old WebSocket sent.

### Binary (Yjs and awareness)

Payloads are y-websocket frames, so `y-protocols` encoders/decoders work unchanged. `varUint`/`varUint8Array` are lib0 encodings.

| frame | bytes |
|---|---|
| sync step 1 | `[0][0][varUint8Array stateVector]` (`syncProtocol.writeSyncStep1`) |
| sync step 2 | `[0][1][varUint8Array update]` |
| sync update | `[0][2][varUint8Array update]` |
| awareness | `[1][varUint8Array awarenessUpdate]` (`awarenessProtocol.encodeAwarenessUpdate`) |
| compaction snapshot (client to server) | `[3][varUint seq][varUint8Array update]` |
| compaction hint (server to client) | `[4][varUint seq]` |

The old message type 2 ("denied") is no longer used: refusals arrive as JSON on `dg/c/{c}/ctl`. Maximum message size is 16 MB
(EMQX `mqtt.max_packet_size`); the server also rejects Yjs messages above `DIAGRAM_MAX_PROJECT_MB` + 1 MiB.

## Yjs live co-editing

Needs `collab: true` in the realtime config. The server never merges: it stores updates (in order, numbered) and relays them.

**Join (every connection, and again after `ctl` 1012):**

1. Subscribe, and wait for the SUBACKs: `dg/f/{f}/yjs/down`, `dg/f/{f}/yjs/sync/{c}`, `dg/f/{f}/awareness/out`,
   `dg/f/{f}/events` (and the roster/rtc topics you use).
2. Publish sync step 1 with your state vector to `dg/f/{f}/yjs/req/{c}` (QoS 1). Any role may.
3. On `yjs/sync/{c}` you receive, in this order: the stored snapshot (a sync update, if any), every stored update (sync updates),
   an **empty sync step 2** (`yjs.EmptyUpdate`, bytes `[0,0]`; this is the "synced" signal for `y-websocket`-style clients), the
   server's own **sync step 1** (state vector of everything it holds), and an awareness frame with the cursors it knows.
   Apply updates; on the server's step 1, if you are an editor, reply with sync step 2 (`encodeStateAsUpdate(doc, sv)`) on
   `yjs/up/{c}` so the server learns what you made offline. Viewers just do not reply.
4. Live: updates from other editors arrive on `yjs/down`; apply them (they are idempotent). Messages with `origin` equal to your
   client id are your own, already applied; skip them.

**Writing (editors):** publish `[0][2][update]` on `dg/f/{f}/yjs/up/{c}` at QoS 1. Batch small edits (e.g. one publish per
animation frame or 50 ms). The server skips updates that carry nothing new, relays the rest on `yjs/down` immediately and writes
them to the database in batches every 150 ms. A PUBACK reason `0x87` means you are not an editor (stop writing, show read-only).
`dg/c/{c}/ctl` `{"type":"unavailable","code":1011}` means the server could not store your updates: resync (join again) and retry.

**Compaction:** after `COLLAB_COMPACT_AFTER` stored updates the server sends `[4][varUint seq]` on `yjs/sync/{c}` to the editor
whose update reached the mark. If you are synced (you have applied everything the server has sent), reply with
`[3][varUint seq][varUint8Array encodeStateAsUpdate(doc)]` on `yjs/up/{c}` (or `PUT /api/diagrams/files/{id}/ydoc?seq=N` with the
bytes as the body, which answers 409 `SNAPSHOT_INCOMPLETE` when the snapshot misses an update). The server checks the snapshot
covers every stored update up to `seq` before replacing them; an incomplete snapshot is silently dropped. Ignore hints while
you are not synced.

## Awareness (cursors)

Publish awareness frames to `dg/f/{f}/awareness/up/{c}` (QoS 0, any role, viewers included). Read `dg/f/{f}/awareness/out`
(skip `origin` equal to yours). **Throttle to at most 15 publishes per second** (the server drops anything faster than ~50/s per
client). Keep the usual y-protocols renewal (about every 15 s) because the server's picture is rebuilt only from what it hears.

The server rewrites the identity inside the state: `user.id` and `user.name` become the account's (a guest is `Guest`), whatever the
client wrote; other fields such as `user.color` and `cursor` are kept. An awareness client id is owned by the first connection that
used it (per API instance); another connection cannot take it over. When a connection's presence ends (Last Will, clean leave
or expiry) the server publishes a `null` state (clock + 1) for its ids.

Why not a shared topic with the identity checked by the broker? The broker can only allow or deny a publish; it cannot rewrite
a payload, and MQTT 5 does not tell subscribers who published. The `up`/`out` split is the smallest design that gives
subscribers an identity they can trust.

## Presence and roster

* On opening a file, publish a non-empty payload (for example `{"ts":1700000000000}`) to `dg/f/{f}/presence/{c}` (QoS 0), and
  again **every 30 s** (`presenceEveryMs`) while the file stays open. Publish immediately after every (re)connect.
* On leaving a file, publish an **empty** payload to the same topic (QoS 1) before disconnecting. The Last Will does the same if
  the connection dies without a DISCONNECT. (A normal DISCONNECT does not fire the Will, so the clean empty publish is needed.)
* The server turns this into **retained** roster entries, vouching for the identity, with a 90 s message expiry (so an entry
  nobody refreshes disappears):

  `dg/f/{f}/roster/{c}` and `dg/p/{p}/roster/{f}/{c}`:
  ```json
  {"clientId":"u3.abc..","userId":"3","name":"Ann","role":"editor","fileId":"..","projectId":".."}
  ```
  `userId` is `null` for guests. Subscribe to `dg/f/{f}/roster/+` (a file view) or `dg/p/{p}/roster/+/+` (the tree: who has which
  file open). On subscribing you receive the current entries (retained); afterwards one message per change. **An empty payload on
  a roster topic means that client left**: remove it. You see your own entry too. The list the old `presence` message carried is
  now the set of non-removed entries.
* The same client id never appears twice for a file; a user with two tabs has two client ids.

## Audio rooms

Signaling for a WebRTC mesh; media never touches the server. Room size is `GET /api/diagrams/rtc/config` `maxPeers`
(clamped 2–6, enforced atomically with Redis slots across API instances). The same config route gives the ICE servers.

Commands, published as JSON to `dg/f/{f}/rtc/up/{c}` (QoS 1):

```jsonc
{"type":"rtc.join"} {"type":"rtc.leave"}
{"type":"rtc.signal","to":"<clientId>","kind":"offer|answer|ice","data":{...}}   // data: a JSON object, at most 16000 bytes
{"type":"rtc.state","muted":true,"speaking":false}
```

Messages from the server to `dg/f/{f}/rtc/{c}` (QoS 1):

```jsonc
{"type":"rtc.joined","peers":[{"clientId":"..","userId":"3","name":"Ann"}]}   // you are in; the others already there
{"type":"rtc.peer-joined","peer":{"clientId":"..","userId":"3","name":"Ann"}}
{"type":"rtc.peer-left","clientId":".."}
{"type":"rtc.signal","from":"..","kind":"offer","data":{...}}
{"type":"rtc.state","from":"..","muted":true,"speaking":false}
{"type":"rtc.full","code":"room_full","max":6,"count":6}                       // code "unavailable" when the slot store is down
```

The whole file also gets `rtc.roster` on `dg/f/{f}/events` after every join/leave. The newcomer calls the others (offers);
the others answer, as before. A client leaves the room explicitly, or implicitly when its presence ends (Will, clean leave, expiry);
keep publishing presence while in a call, since the heartbeat also refreshes the room slot (it lapses after 90 s).
Signals are only forwarded between members of the same room; others are dropped.

## Control messages

`{"type":"ctl","code":N,"reason":"..."}` on `dg/f/{f}/events` (and `dg/p/{p}/events` for a project):

| code | meaning | client action |
|---|---|---|
| 4404 | file (or project) deleted | **final**: stop, disconnect, show "deleted", do not reconnect |
| 1012 | the document was replaced (a version was restored) | drop the Yjs doc state, rejoin (join steps 1–4), reload the file over REST |

On `dg/c/{c}/ctl`:

| message | meaning | client action |
|---|---|---|
| `{"type":"denied","fileId":"..","reason":"read-only"}` | your write was refused for your role | switch to read-only |
| `{"type":"unavailable","code":1011,"fileId":".."}` | the server could not load or store Yjs data | final for this attempt: back off, then resync; treat repeated 1011 as final |

(Codes are those the WebSocket close frames used: 4404 and 1011 final, 1012 "sync again".)

## Reconnecting

* Use a reconnect loop you control (e.g. `mqtt.js` with `reconnectPeriod: 0`, creating a new client each attempt): every attempt
  needs a fresh ticket and a fresh client id. Backoff with full jitter: `delay = random(0, min(30 s, 1 s * 2^attempt))`, reset after a
  connection that stayed up 30 s. If the ticket request answers 401, the session ended: stop.
* On every successful connect: subscribe (wait for SUBACK), publish presence, join Yjs (step 1 on `yjs/req`), and refresh what you
  may have missed from REST: events are not queued across connections, so compare `GET /files/{id}` `version` and the tree `rev`
  from `GET /projects/{id}/tree` with what you have.
* Pause the loop while the tab is hidden for a long time if you like; always publish the empty presence on `pagehide`.
* Do not reconnect after 4404, after a refused login that a fresh ticket does not fix, or when a SUBACK says `0x87` for a file you
  confirmed is gone.
* Keepalive 30 s. EMQX closes silent connections after 1.5x that.

## QoS, retain, expiry

| what | QoS | retain | expiry |
|---|---|---|---|
| events (`events`) | 1 | no | 60 s |
| Yjs up / down / sync / req | 1 | no | `sync`: 30 s |
| awareness up / out | 0 | no | |
| presence (client), Last Will | 0, Will 1 | no | |
| roster (server) | 0 | **yes** | 90 s |
| rtc up / inbox | 1 | no | 60 s |
| ctl | 1 | no | 60 s |

## Deployment

Compose (`docker-compose.yml`, service `emqx`, image `emqx/emqx:5.8.6`): listeners TCP 1883 (published on 127.0.0.1 only, for
`go run`) and WebSocket 8083 path `/mqtt` (127.0.0.1 only, for vite); the dashboard is off. In the compose network the API uses
`tcp://emqx:1883`. EMQX's HTTP authentication and authorization sources point at `${MQTT_AUTH_BASE:-http://api:8080}/api/internal/mqtt/{auth,acl}`
and send `Authorization: Bearer ${MQTT_INTERNAL_SECRET}`; the config lives inline in the compose `configs:` block (`$${...}` there
are EMQX's own placeholders). When the API runs on the host (`go run`), set `MQTT_AUTH_BASE=http://host.docker.internal:8080`.

Settings: `MQTT_URL`, `MQTT_USERNAME`, `MQTT_PASSWORD` (secret), `MQTT_INTERNAL_SECRET` (secret), `MQTT_PUBLIC_WS_PATH`. Secrets
must be at least 32 characters in production; in dev empty means the documented dev values (and the compose broker uses the same).
The `api` user is a broker superuser (no access rules apply to it): keep `MQTT_PASSWORD` secret and the 1883 listener off
the public network.

**Same-origin WebSocket proxy** (Web's `nginx.conf`; `MQTT_URL=http://emqx:8083` is passed to the web container by the root compose):

```nginx
location = /mqtt {
    proxy_pass ${MQTT_URL};                 # no URI part: the path /mqtt is passed through
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_read_timeout 1h;
    proxy_send_timeout 1h;
    proxy_buffering off;
}
# BEFORE `location /api/`: the broker's questions are not public (they also need the bearer secret)
location /api/internal/ { return 404; }
```

Vite dev server (`vite.config.ts`): `server.proxy = { "/mqtt": { target: "http://localhost:8083", ws: true }, "/api/internal": { target: "http://localhost:1", bypass: () => "/404" }, "/api": "http://localhost:8080" }`
(order matters; `/api/internal` must not reach the API). CSP `connect-src 'self'` covers same-origin WebSockets in current browsers;
add `ws: wss:` for older Safari.

Scaling: several API instances work out of the box (shared subscriptions, Redis for audio slots and guest tokens). For several EMQX
nodes use EMQX clustering; nothing in the protocol changes. Retained roster messages live in EMQX's data volume (`emqxdata`).

## Server design notes

* **Yjs ingest:** one shared-subscription consumer per instance on `dg/f/+/yjs/up/+` and `.../req/+`. Messages of one file are handled
  in order by one worker per instance. A tracker per file (loaded from the database on first use, dropped after 5 idle minutes or on
  `ctl` 1012/4404) decides whether an update is new; new updates are republished at once and written in batches (every 150 ms, one
  transaction per file). A batch that cannot be written is retried twice, then the room is dropped and the senders told `unavailable`.
  Because trackers are per instance, two instances can store the same update twice in a rare race; compaction removes the duplicate
  and clients ignore it (Yjs updates are idempotent).
* **Restore/delete:** the `ctl` event is published on the file's `events` topic; every instance subscribes to `dg/f/+/events` and drops
  its tracker and cached access when it sees `ctl` 1012/4404.
* **Authorization:** `MQTTAccess` (application layer). Access decisions are cached per `(client id, file|project)` for 30 s; EMQX caches
  its authorization answers for 30 s per client.
* **Failure of the broker:** the API keeps running (events are dropped and logged, REST works). `autopaho` reconnects with backoff
  and resubscribes. Clients see their own disconnects and follow [Reconnecting](#reconnecting).
* **Limits of this design:** awareness-id ownership and cursor state are per API instance (clients renew awareness periodically, so a
  restarted instance relearns it); events are not queued for offline clients (resync over REST on reconnect).

## Old WebSocket to MQTT mapping

| old | new |
|---|---|
| `GET /ws/files/{id}?ticket=` / `?share=` | MQTT connect (`ticket` / `share` login), subscribe `dg/f/{f}/events`, `dg/f/{f}/roster/+` |
| `GET /ws/projects/{id}` | subscribe `dg/p/{p}/events`, `dg/p/{p}/roster/+/+` |
| `GET /ws/files/{id}/yjs` | topics `dg/f/{f}/yjs/*`, `dg/f/{f}/awareness/*` |
| `hello` (`clientId`, `role`, `version`, `treeRev`) | the client picks its client id; `role`, `version` and `treeRev` come from REST (`GET /files/{id}` has `role`; `GET /projects/{id}/tree` has `rev`) |
| `presence {users}` | roster entries (retained, one per client) |
| `{"type":"ping"}` / `pong` | MQTT keepalive |
| `rtc.*` text frames | `rtc/up/{c}` and `rtc/{c}` JSON, same message types |
| close 4404 / 1012 / 1011 | `ctl` messages (see above) |
| message type 2 `denied` | `dg/c/{c}/ctl` `denied` |
| `PUT /files/{id}/ydoc?seq=` | unchanged |
| `GET /rtc/config` | unchanged; new: `GET /realtime/config` |
