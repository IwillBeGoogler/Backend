#!/usr/bin/env python3
"""Dev-only demo data: authors, stories, diagrams and activity, through the public API.

    docker compose --profile dev run --rm --build seed

Safe to re-run: stories are matched by title and diagrams use fixed ids, so nothing is duplicated.
Rate limits are honoured (429 + Retry-After), so a first run takes a few minutes.
All authors are fictional. Their password is SEED_PASSWORD (default iwillbegoogler-demo-2026).
"""
import base64
import json
import os
import pathlib
import struct
import subprocess
import sys
import time
import urllib.error
import urllib.request
import uuid
import zlib

API = os.environ.get("SEED_API", "http://localhost:8080").rstrip("/")
DB = os.environ.get("SEED_DATABASE_URL", "")
PASSWORD = os.environ.get("SEED_PASSWORD", "iwillbegoogler-demo-2026")
ADMIN = (os.environ.get("ADMIN_USERNAME", "admin"), os.environ.get("ADMIN_PASSWORD", "admin12345"))
HERE = pathlib.Path(__file__).parent

AUTHORS = {
    "dilnoza": ("Dilnoza Karimova", "Backend engineer in Tashkent. I write about databases and the systems around them."),
    "jasur": ("Jasur Toshmatov", "Site reliability engineer. Caches, queues, and the outages they cause."),
    "malika": ("Malika Rahimova", "Staff engineer working on payments. Ledgers are my idea of fun."),
    "sardor": ("Sardor Yusupov", "Distributed systems, mostly messaging. Occasionally writes in Uzbek."),
    "elena": ("Elena Petrova", "Platform engineer. Rate limits, gateways and developer tooling."),
    "tom": ("Tom Becker", "Software architect and mentor. Likes simple designs that survive production."),
    "aziza": ("Aziza Nazarova", "Computer science student in Samarkand, preparing for system design interviews."),
    "bobur": ("Bobur Ergashev", "Mobile developer learning backend, one outage at a time."),
}

# file, title, subtitle, author, category, tags, days ago, cover colours, status
POSTS = [
    ("url-shortener", "Designing a URL Shortener That Survives Real Traffic", "Key generation, the redirect path, and why analytics must never slow a click.", "tom", "System Design", ["system-design", "caching", "scalability"], 41, ((37, 99, 235), (14, 165, 233))),
    ("postgres-indexes", "Postgres Indexes I Wish I Had Understood Earlier", "Composite order, partial indexes and keyset pagination, with the queries to check them.", "dilnoza", "Databases", ["postgres", "databases", "performance"], 34, ((5, 150, 105), (16, 185, 129))),
    ("rate-limiting", "Rate Limiting: Algorithms, Redis, and Failing Safely", "The algorithm matters less than where the state lives and what happens when it is gone.", "elena", "System Design", ["system-design", "redis", "api-design"], 27, ((217, 70, 239), (124, 58, 237))),
    ("chat-delivery", "How Chat Apps Deliver Every Message Exactly Once", "Idempotent writes, per-conversation ordering, and one reconnect path for everything.", "sardor", "Distributed Systems", ["distributed-systems", "websockets", "messaging"], 21, ((234, 88, 12), (245, 158, 11))),
    ("outbox-pattern", "The Outbox Pattern: Stop Losing Events", "Writing to a database and a broker is a distributed transaction. Here is the fix.", "dilnoza", "Distributed Systems", ["distributed-systems", "kafka", "postgres"], 15, ((15, 118, 110), (6, 182, 212))),
    ("payment-idempotency", "Idempotency in Payment APIs: Never Charge Twice", "Keys, state machines, provider timeouts and reconciliation.", "malika", "Backend", ["payments", "api-design", "reliability"], 10, ((190, 18, 60), (244, 63, 94))),
    ("uz-cache", "Keshlash naqshlari: cache-aside, TTL va thundering herd", "Keshni to'g'ri ishlatish va eng ko'p uchraydigan xatolar.", "jasur", "System Design", ["uzbek", "caching", "redis"], 7, ((67, 56, 202), (99, 102, 241))),
    ("uz-kafka", "Kafka asoslari: topic, partition va consumer group", "Kafka nima uchun oddiy navbat emas va undan qanday ishonchli foydalanish kerak.", "sardor", "Distributed Systems", ["uzbek", "kafka", "messaging"], 4, ((71, 85, 105), (100, 116, 139))),
    ("uz-load-balancer", "Load balancer qanday ishlaydi: L4, L7 va algoritmlar", "Trafikni taqsimlash, health check va eng ko'p uchraydigan tuzoqlar.", "jasur", "Backend", ["uzbek", "scalability", "networking"], 2, ((161, 98, 7), (234, 179, 8))),
    ("draft-sharding", "When to Shard Your Database", "Draft notes.", "dilnoza", "Databases", ["databases"], 0, None, "draft"),
    ("scheduled-observability", "Alerts That Do Not Wake You Up for Nothing", "Page on symptoms, not on causes.", "elena", "Backend", ["observability", "reliability"], 0, None, "scheduled"),
]

COMMENTS = {
    "url-shortener": [("dilnoza", "Leasing ID blocks is underrated. We removed a whole coordination service by doing this.", None), ("bobur", "Why not simply use a UUID as the key?", None), ("tom", "Length. A short link with 36 characters defeats the purpose, and base62 of a counter stays at seven.", 1), ("aziza", "The traffic estimate section helped a lot. This was asked in my interview practice.", None)],
    "postgres-indexes": [("jasur", "The unused index query found 40 GB of waste on our primary. Thank you.", None), ("tom", "Remember to check replicas before dropping. Reporting queries often run only there.", 0), ("malika", "Keyset pagination should be the default in every ORM.", None)],
    "rate-limiting": [("sardor", "Failing closed on login is the detail most articles skip.", None), ("bobur", "Should the limit be per user or per IP?", None), ("elena", "Both. Per user when authenticated, per IP before login, with a higher limit for shared networks.", 1)],
    "chat-delivery": [("elena", "How do you handle one user on several devices?", None), ("sardor", "Read position is stored per device, and the presence store maps a user to many gateways.", 0), ("aziza", "Finally a clear explanation of exactly-once versus at-least-once.", None)],
    "outbox-pattern": [("malika", "We run exactly this for payment events, and it has been very boring. That is praise.", None), ("jasur", "Add an alert on the age of the oldest unpublished row. It catches a stuck relay early.", None)],
    "payment-idempotency": [("tom", "The note about timeouts meaning unknown should be printed and framed.", None), ("dilnoza", "Do you expire the keys?", None), ("malika", "Yes, after 24 hours. Long enough for any sane retry, and it keeps the table small.", 1), ("bobur", "Asked about this in a code review today. Sent the team this link.", None)],
    "uz-cache": [("sardor", "TTL'ga tasodifiy qiymat qo'shish bizni bir marta katta muammodan qutqargan.", None), ("aziza", "Juda tushunarli yozilibdi, rahmat!", None)],
    "uz-kafka": [("jasur", "Consumer lag bo'yicha alert albatta bo'lishi kerak.", None), ("bobur", "Partition sonini keyin oshirsa bo'ladimi?", None), ("sardor", "Bo'ladi, lekin kalitlar boshqa partition'ga tushadi va tartib buziladi.", 1)],
    "uz-load-balancer": [("tom", "Great point on health checks that depend on the database.", None), ("aziza", "L4 va L7 farqini endi tushundim.", None)],
}

FOLLOWS = {"aziza": ["tom", "dilnoza", "sardor", "jasur"], "bobur": ["tom", "elena", "malika"], "dilnoza": ["tom", "malika"], "jasur": ["sardor", "dilnoza"], "malika": ["dilnoza"], "sardor": ["jasur", "elena"], "elena": ["tom"], "tom": ["dilnoza"]}


def edge(i, a, b, label=""):
    e = {"id": f"e{i}", "type": "smoothstep", "source": a, "target": b, "sourceHandle": "right", "targetHandle": "left",
         "data": {"pinSource": "right", "pinTarget": "left"}, "style": {"stroke": "#2d3340", "strokeWidth": 1.6},
         "markerEnd": {"type": "arrowclosed", "color": "#2d3340", "width": 16, "height": 16}, "pathOptions": {"borderRadius": 0}}
    if label:
        e |= {"label": label, "labelShowBg": True, "labelBgPadding": [4, 2], "labelStyle": {"fill": "#2d3340", "fontSize": 11, "fontWeight": 500}, "labelBgStyle": {"fill": "#ffffff", "fillOpacity": 0.95}}
    return e


# nodes: id, label, icon, subtitle, column, row. edges: from, to, label.
DIAGRAMS = [
    ("url-shortener", "tom", "URL Shortener", "Read-heavy redirect path with CDN and Redis, and asynchronous click analytics through Kafka.", ["system-design", "caching"],
     [("u", "User", "user", "", 0, 1), ("c", "CDN", "cdn", "edge cache", 1, 1), ("lb", "Load balancer", "load-balancer", "", 2, 1), ("api", "Redirect service", "server", "stateless", 3, 1), ("r", "Redis", "redis", "hot keys", 4, 0), ("db", "PostgreSQL", "postgresql", "links", 4, 1), ("k", "Kafka", "kafka", "click events", 4, 2), ("w", "Analytics worker", "server", "", 5, 2), ("s3", "S3", "s3", "aggregates", 6, 2)],
     [("u", "c", "GET /abc123"), ("c", "lb", "miss"), ("lb", "api", ""), ("api", "r", "lookup"), ("api", "db", "on miss"), ("api", "k", "click"), ("k", "w", ""), ("w", "s3", "hourly")]),
    ("chat-system", "sardor", "Real-Time Chat", "Stateless WebSocket gateways, per-conversation ordering, and offline push.", ["distributed-systems", "messaging"],
     [("m", "Mobile client", "mobile", "", 0, 0), ("b", "Web client", "browser", "", 0, 2), ("lb", "Load balancer", "load-balancer", "L4", 1, 1), ("gw", "WebSocket gateway", "server", "stateless", 2, 1), ("p", "Presence", "redis", "user to gateway", 3, 0), ("ms", "Message service", "server", "", 3, 1), ("db", "Message store", "database", "by conversation", 4, 1), ("k", "Kafka", "kafka", "fan-out", 4, 2), ("n", "Push worker", "server", "APNs, FCM", 5, 2)],
     [("m", "lb", "wss"), ("b", "lb", "wss"), ("lb", "gw", ""), ("gw", "p", "heartbeat"), ("gw", "ms", "send"), ("ms", "db", "append"), ("ms", "k", "event"), ("k", "n", "offline")]),
    ("payment-platform", "malika", "Payment Platform", "Idempotent API, double-entry ledger, outbox, and provider webhooks.", ["payments", "reliability"],
     [("c", "Merchant", "browser", "", 0, 1), ("gw", "API gateway", "api-gateway", "auth, limits", 1, 1), ("p", "Payment service", "server", "state machine", 2, 1), ("i", "Idempotency keys", "redis", "24h", 3, 0), ("db", "PostgreSQL", "postgresql", "ledger, outbox", 3, 1), ("psp", "Payment provider", "server", "external", 3, 2), ("q", "Kafka", "kafka", "payment events", 4, 1), ("r", "Reconciliation", "server", "nightly", 5, 1)],
     [("c", "gw", "Idempotency-Key"), ("gw", "p", ""), ("p", "i", "claim"), ("p", "db", "transaction"), ("p", "psp", "charge"), ("db", "q", "outbox relay"), ("q", "r", "")]),
    ("news-feed", "dilnoza", "News Feed", "Hybrid fan-out: on write for most authors, on read for accounts with many followers.", ["system-design", "scalability"],
     [("u", "Client", "mobile", "", 0, 1), ("lb", "API gateway", "api-gateway", "", 1, 1), ("po", "Post service", "server", "", 2, 0), ("fe", "Feed service", "server", "", 2, 2), ("db", "PostgreSQL", "postgresql", "posts", 3, 0), ("q", "Kafka", "kafka", "post created", 3, 1), ("c", "Feed cache", "redis", "timeline per user", 3, 2), ("w", "Fan-out worker", "server", "", 4, 1)],
     [("u", "lb", ""), ("lb", "po", "write"), ("lb", "fe", "read"), ("po", "db", ""), ("po", "q", ""), ("q", "w", ""), ("fe", "c", "")]),
    ("video-streaming", "jasur", "Video Streaming", "Upload, transcoding queue and adaptive delivery from a CDN.", ["system-design", "storage"],
     [("cr", "Creator", "user", "", 0, 0), ("lb", "Load balancer", "load-balancer", "", 1, 0), ("up", "Upload service", "server", "resumable", 2, 0), ("raw", "Raw storage", "s3", "", 3, 0), ("q", "Job queue", "queue", "", 4, 0), ("tr", "Transcoders", "server", "HLS renditions", 5, 0), ("out", "Video storage", "s3", "", 6, 0), ("cdn", "CDN", "cdn", "", 5, 1), ("v", "Viewer", "browser", "", 4, 1)],
     [("cr", "lb", "chunks"), ("lb", "up", ""), ("up", "raw", ""), ("raw", "q", "uploaded"), ("q", "tr", ""), ("tr", "out", ""), ("out", "cdn", "origin"), ("cdn", "v", "HLS")]),
    ("rate-limiter", "elena", "Distributed Rate Limiter", "Token buckets in Redis, shared by every gateway instance.", ["api-design", "redis"],
     [("c", "Clients", "browser", "", 0, 1), ("lb", "Load balancer", "load-balancer", "", 1, 1), ("g1", "Gateway 1", "api-gateway", "", 2, 0), ("g2", "Gateway 2", "api-gateway", "", 2, 2), ("r", "Redis", "redis", "token buckets", 3, 1), ("api", "Upstream API", "server", "", 4, 1)],
     [("c", "lb", ""), ("lb", "g1", ""), ("lb", "g2", ""), ("g1", "r", "Lua script"), ("g2", "r", "Lua script"), ("r", "api", "allowed")]),
]

FEEDBACK = [
    ("idea", "Please add a dark theme option for the embedded diagram viewer in stories."),
    ("bug", "The reading time on Uzbek stories seems slightly lower than the real time it takes to read."),
    ("idea", "It would be great to export a diagram as a PDF with several pages."),
]


def call(method, path, body=None, token=None, raw=None, ok=(200, 201, 204), headers=None):
    """Sends a request; returns (status, data). Waits on 429; raises on any status outside ok."""
    h = dict(headers or {})
    data = raw
    if body is not None:
        data = json.dumps(body).encode()
        h["Content-Type"] = "application/json"
    if token:
        h["Authorization"] = "Bearer " + token
    while True:
        req = urllib.request.Request(API + path, data=data, method=method, headers=h)
        try:
            with urllib.request.urlopen(req, timeout=60) as r:
                code, text = r.status, r.read()
        except urllib.error.HTTPError as e:
            code, text = e.code, e.read()
            if code == 429:
                wait = int(e.headers.get("Retry-After") or 10)
                print(f"  rate limited on {path}, waiting {wait}s", flush=True)
                time.sleep(wait + 1)
                continue
        if code not in ok:
            raise SystemExit(f"{method} {path}: {code} {text[:400]!r}")
        parsed = json.loads(text) if text else {}
        return code, parsed.get("data", parsed) if isinstance(parsed, dict) else parsed


def sql(statement):
    subprocess.run(["psql", DB, "-v", "ON_ERROR_STOP=1", "-qc", statement], check=True, stdout=subprocess.DEVNULL)


def quote(s):
    return "'" + s.replace("'", "''") + "'"


def session(username, display, bio):
    code, s = call("POST", "/api/auth/login", {"login": username, "password": PASSWORD}, ok=(200, 400, 401))
    if code != 200:
        _, s = call("POST", "/api/auth/register", {"username": username, "email": f"{username}@demo.iwillbegoogler.dev", "password": PASSWORD})
        print(f"  registered {username}", flush=True)
    sql(f"UPDATE users SET email_verified_at = COALESCE(email_verified_at, now()) WHERE username = {quote(username)}")
    call("PATCH", "/api/me", {"display_name": display, "bio": bio}, s["token"])
    return s["token"]


def png(size, a, b):
    """A diagonal gradient PNG, used as a story cover."""
    w, h = size
    row = lambda y: b"\0" + bytes(c for x in range(0, w) for c in (tuple(a[i] + (b[i] - a[i]) * (x + y) // (w + h) for i in range(3))))
    def chunk(t, d):
        return struct.pack(">I", len(d)) + t + d + struct.pack(">I", zlib.crc32(t + d))
    return b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", w, h, 8, 2, 0, 0, 0)) + chunk(b"IDAT", zlib.compress(b"".join(row(y) for y in range(h)), 9)) + chunk(b"IEND", b"")


def upload(token, data):
    boundary = uuid.uuid4().hex
    body = f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="cover.png"\r\nContent-Type: image/png\r\n\r\n'.encode() + data + f"\r\n--{boundary}--\r\n".encode()
    _, r = call("POST", "/api/uploads", token=token, raw=body, headers={"Content-Type": f"multipart/form-data; boundary={boundary}"})
    return r["url"]


def main():
    if os.environ.get("APP_ENV", "dev") == "production":
        sys.exit("seed: refusing to run with APP_ENV=production")
    if not DB:
        sys.exit("seed: SEED_DATABASE_URL is required")

    print("authors", flush=True)
    tokens = {u: session(u, *AUTHORS[u]) for u in AUTHORS}
    _, s = call("POST", "/api/auth/login", {"login": ADMIN[0], "password": ADMIN[1]})
    admin = s["token"]

    print("categories", flush=True)
    wanted = sorted({p[4] for p in POSTS})
    for name in wanted:
        call("POST", "/api/categories", {"name": name}, admin, ok=(201, 409))
    _, cats = call("GET", "/api/categories")
    cats = cats if isinstance(cats, list) else cats.get("items", [])
    category = {c["name"]: c["id"] for c in cats}

    print("publication", flush=True)
    call("POST", "/api/publications", {"slug": "system-design-notes", "name": "System Design Notes", "description": "Practical write-ups on building systems that keep working."}, tokens["tom"], ok=(201, 409))
    for member in ("dilnoza", "elena"):
        call("PUT", f"/api/publications/system-design-notes/members/{member}", {"role": "writer"}, tokens["tom"], ok=(200, 204))
    _, pub = call("GET", "/api/publications/system-design-notes")

    print("stories", flush=True)
    ids, new = {}, set()
    for file, title, subtitle, author, cat, tags, days, cover, *rest in POSTS:
        status = rest[0] if rest else "published"
        _, mine = call("GET", f"/api/me/posts?limit=100&status={status}", token=tokens[author])
        items = mine if isinstance(mine, list) else mine.get("items") or []
        found = next((p for p in items if p["title"] == title), None)
        if found:
            ids[file] = found["id"]
            continue
        d = {"title": title, "subtitle": subtitle, "body": (HERE / "posts" / f"{file}.md").read_text(), "tags": tags, "category_id": category[cat], "status": status}
        if cover:
            d["cover_url"] = upload(tokens[author], png((600, 315), *cover))
        if status == "scheduled":
            d["publish_at"] = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(time.time() + 30 * 86400))
        if author == "tom" or file == "rate-limiting":
            d["publication_id"] = pub["id"]
        _, p = call("POST", "/api/posts", d, tokens[author])
        ids[file] = p["id"]
        new.add(file)
        print(f"  {title}", flush=True)

    print("comments, claps, bookmarks, views", flush=True)
    for file, thread in COMMENTS.items():
        if file not in new:
            continue
        made = []
        for who, text, parent in thread:
            body = {"text": text}
            if parent is not None:
                body["parent_id"] = made[parent]
            _, c = call("POST", f"/api/posts/{ids[file]}/comments", body, tokens[who])
            made.append(c["id"])
        call("POST", f"/api/comments/{made[0]}/like", token=tokens["aziza" if thread[0][0] != "aziza" else "tom"], ok=(200, 204, 409))
    readers = list(AUTHORS)
    published = [p for p in POSTS if len(p) == 8]
    for i, p in enumerate(published):
        if p[0] not in new:
            continue
        for j, who in enumerate(readers):
            if who == p[3] or (i + j) % 3 == 0:
                continue
            call("POST", f"/api/posts/{ids[p[0]]}/clap", {"count": 1 + (i * 3 + j * 5) % 12}, tokens[who])
            call("POST", f"/api/posts/{ids[p[0]]}/view", token=tokens[who], ok=(200, 202, 204))
            if (i + j) % 2 == 0:
                call("POST", f"/api/posts/{ids[p[0]]}/bookmark", token=tokens[who], ok=(200, 204, 409))

    print("follows, series, lists", flush=True)
    for who, targets in FOLLOWS.items():
        for t in targets:
            call("POST", f"/api/users/{t}/follow", token=tokens[who], ok=(200, 204, 409))
    call("POST", "/api/tags/system-design/follow", token=tokens["aziza"], ok=(200, 204, 409))
    call("POST", "/api/users/dilnoza/subscribe", token=tokens["aziza"], ok=(200, 204, 409))
    if "outbox-pattern" in new:
        _, series = call("POST", "/api/series", {"title": "Databases in Production", "description": "Indexes, transactions, and getting events out reliably."}, tokens["dilnoza"])
        call("PUT", f"/api/series/{series['slug']}/posts", {"post_ids": [ids["postgres-indexes"], ids["outbox-pattern"]]}, tokens["dilnoza"])
        _, lst = call("POST", "/api/lists", {"name": "Interview preparation", "description": "What to read before a system design interview."}, tokens["aziza"])
        for f in ("url-shortener", "rate-limiting", "chat-delivery", "uz-load-balancer"):
            call("PUT", f"/api/lists/{lst['slug']}/posts/{ids[f]}", token=tokens["aziza"])

    print("diagrams", flush=True)
    slugs = {}
    now = int(time.time() * 1000)
    for key, owner, name, description, tags, nodes, edges in DIAGRAMS:
        fid, pid = f"fil_seed_{key.replace('-', '_')}", f"prj_seed_{key.replace('-', '_')}"
        page = {"id": "pg_main", "name": "Architecture",
                "nodes": [{"id": n[0], "type": "icon", "position": {"x": 80 + n[4] * 200, "y": 80 + n[5] * 170}, "data": {"label": n[1], "iconId": n[2], **({"subtitle": n[3]} if n[3] else {})}} for n in nodes],
                "edges": [edge(i, *e) for i, e in enumerate(edges)]}
        data = {"id": fid, "name": name, "pages": [page], "version": 2, "createdAt": now, "updatedAt": now, "activePageId": "pg_main"}
        call("POST", "/api/diagrams/projects", {"id": pid, "name": name, "file": {"id": fid, "name": name, "data": data}}, tokens[owner], ok=(200, 201, 409))
        _, pub_info = call("POST", f"/api/diagrams/files/{fid}/publish", {"visibility": "public", "title": name, "description": description, "tags": tags}, tokens[owner])
        slugs[key] = (fid, pid, pub_info["slug"], owner)
        thumb = HERE / "thumbnails" / f"{key}.png"
        if thumb.exists():
            call("PUT", f"/api/diagrams/files/{fid}/thumbnail", token=tokens[owner], raw=thumb.read_bytes(), headers={"Content-Type": "image/png"})
        print(f"  {name}: /{pub_info['slug']}", flush=True)
    for i, (key, (fid, pid, slug, owner)) in enumerate(slugs.items()):
        for j, who in enumerate(readers):
            if who != owner and (i + j) % 2:
                call("POST", f"/api/diagrams/public/{slug}/clap", {"count": 1 + (i + j) % 5}, tokens[who], ok=(200, 204))
                call("POST", f"/api/diagrams/public/{slug}/bookmark", token=tokens[who], ok=(200, 201, 204, 409))
    # team work on one design: a member, a comment thread, and a share link
    fid, pid, _, owner = slugs["payment-platform"]
    call("POST", f"/api/diagrams/projects/{pid}/members", {"username": "dilnoza", "role": "editor"}, tokens[owner], ok=(200, 201, 409))
    _, existing = call("GET", f"/api/diagrams/files/{fid}/comments", token=tokens[owner])
    if "live payments?" not in json.dumps(existing):
        _, c = call("POST", f"/api/diagrams/files/{fid}/comments", {"body": "Should reconciliation read from a replica, so the nightly job cannot affect live payments?", "anchor": {"type": "node", "nodeId": "r", "dx": 0, "dy": 0}}, tokens["dilnoza"])
        c = c["comment"]
        call("POST", f"/api/diagrams/files/{fid}/comments", {"body": "Yes, a replica is fine since the job only compares settled entries.", "parentId": c["id"]}, tokens[owner])
    call("POST", f"/api/diagrams/projects/{pid}/shares", {"role": "viewer"}, tokens[owner], ok=(200, 201, 400))

    print("templates", flush=True)
    _, current = call("GET", "/api/admin/diagrams/templates", token=admin)
    current = current if isinstance(current, list) else current.get("items") or current.get("templates") or []
    have = {t["file_id"] for t in current}
    for pos, key in enumerate(("url-shortener", "chat-system", "payment-platform")):
        fid = slugs[key][0]
        if fid not in have:
            d = next(x for x in DIAGRAMS if x[0] == key)
            call("POST", "/api/admin/diagrams/templates", {"title": d[2], "kind": "system", "description": d[3], "file_id": fid, "published": True, "position": pos}, admin)

    print("moderation inbox", flush=True)
    _, inbox = call("GET", "/api/admin/feedback", token=admin)
    inbox = inbox if isinstance(inbox, list) else inbox.get("items") or []
    if not any(f["message"] == FEEDBACK[0][1] for f in inbox):
        call("POST", "/api/reports", {"target_type": "post", "target_id": ids["uz-kafka"], "reason": "other", "note": "The partition section may need a source. Not urgent, please review."}, tokens["bobur"])
        for kind, message in FEEDBACK:
            call("POST", "/api/feedback", {"kind": kind, "message": message, "page_url": "http://localhost:3000/"}, tokens["aziza"])

    print("backdating", flush=True)
    for p in published:
        when = f"now() - interval '{p[6]} days' - interval '{(p[6] * 37) % 600} minutes'"
        sql(f"UPDATE posts SET created_at = {when}, published_at = {when}, updated_at = {when} WHERE id = {ids[p[0]]} AND status = 'published'")
        sql(f"UPDATE comments SET created_at = (SELECT published_at FROM posts WHERE id = {ids[p[0]]}) + (comments.id % 40 + 1) * interval '2 hours' WHERE post_id = {ids[p[0]]}")
    print("done. Log in as any author with password " + PASSWORD)


main()
