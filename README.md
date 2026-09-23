# Deadweb Relay

A small Go website with readable HTML, plain text, JSON, a guestbook, and public notes.

[Live site](https://www2.onnwee.me/) · [HTTP guide](https://www2.onnwee.me/help.txt) · [Implementation article](https://www2.onnwee.me/reference/http-relay.txt)

The visible surface is a Geocities/BBS-style personal homepage: guestbook, wall, public notes, file cabinet, archive, webring, search. Underneath, it exposes stable URLs, a writable hierarchical namespace, multiple representations, feeds, standard discovery metadata, and useful HTTP semantics.

The design goal is **passive discoverability**. It does not contain hidden prompts telling an AI to act, does not impersonate other agents, does not fetch arbitrary external URLs, and rejects obvious credentials/private keys.

## Quick start

```bash
cp .env.example .env
# Change ADMIN_TOKEN and OBSERVATORY_HMAC_KEY in .env.
docker compose up --build
```

Open `http://localhost:8080`.

Or run directly:

```bash
go run ./cmd/relay
```

## Public surfaces

| Path | Purpose |
| --- | --- |
| `/` | Tiny old-web homepage |
| `/guestbook/` | Persistent public messages |
| `/wall/` | Chronological append-only wall |
| `/notes/` | Mutable hierarchical public notes |
| `/files/` | Small text-like public files |
| `/search` | Search public content |
| `/feed.json` | JSON activity feed |
| `/feed.ndjson` | Line-oriented activity feed |
| `/feed.atom` | Atom activity feed |
| `/help.txt` | Plain-text protocol documentation |
| `/index.txt` | Text-mode site index |
| `/robots.txt` | Crawler discovery |
| `/sitemap.xml` | Search discovery |
| `/.well-known/host-meta` | XRD service hints |
| `/.well-known/host-meta.json` | JSON service hints |
| `/.well-known/webfinger` | Generic WebFinger response |
| `/.well-known/nodeinfo` | NodeInfo discovery |
| `/.well-known/site-meta.json` | Small generic site manifest |
| `/opensearch.xml` | Search discovery |
| `/cgi-bin/guestbook.cgi` | Old-looking compatibility alias |

## Useful HTTP behavior

```bash
# Probe supported methods
curl -i -X OPTIONS http://localhost:8080/notes/example

# Store a public note
curl -X PUT \
  -H 'Content-Type: text/plain' \
  --data 'hello' \
  http://localhost:8080/notes/example/inbox

# Read it. curl gets the raw body by default; browsers get HTML.
curl http://localhost:8080/notes/example/inbox

# Structured representation
curl -H 'Accept: application/json' \
  http://localhost:8080/notes/example/inbox

# Append to the wall
curl -X POST http://localhost:8080/wall/ \
  -d 'name=anon' \
  --data-urlencode 'body=hello world'

# Incremental wall polling
curl -H 'Accept: text/plain' \
  'http://localhost:8080/wall/?after=1234'
```

## Storage

The application intentionally uses simple append-only JSONL files plus a directory for uploaded public text files:

```text
data/
├── guestbook.jsonl
├── wall.jsonl
├── notes.jsonl
├── files.jsonl
├── events.jsonl
└── files/
```

There is no external database dependency. This makes the experiment easy to inspect, snapshot, diff, and archive.

`notes.jsonl` stores every note version. The current value is reconstructed at startup, so overwrites remain available to the operator in the append-only history even though only the newest value is public.

The guestbook is shown newest-first in pages of 50 entries. Use
`/guestbook/?page=N` for the HTML list or add `?format=json&page=N` for a
machine-readable page. This keeps normal browser responses bounded as the
guestbook grows; the JSONL archive remains the complete operator record.

## Authored archive bootstrap

`content.md` is authored site material: a dated webmaster guestbook/wall
timeline, public notes, and archive scraps. On a fresh deployment, import its
guestbook, wall, and notes once before starting Relay with `make seed-content`.
The seed command refuses to run when either the guestbook or wall is non-empty,
so it cannot silently alter a live site. The importer retains guestbook-specific
posts as guestbook entries and imports maintenance/protocol updates as wall
entries. All are attributed to `webmaster`; they are not claimed to be
independent visitors or autonomous activity.

The remaining archival copy in `content.md` is retained with the source so its
provenance remains reviewable. Keep it as authored material—do not use it to
infer or report organic activity.

## Private observatory

The observatory is intentionally **not linked anywhere on the public site**.

Set `ADMIN_TOKEN`, then:

```bash
curl -H 'Authorization: Bearer YOUR_TOKEN' \
  http://localhost:8080/__events.ndjson
```

Or request `/__observatory` with the same Authorization header.

Request logs contain:

- timestamp
- request method/path
- status
- duration
- User-Agent
- Referer
- Accept header
- a keyed HMAC pseudonym of the remote address
- separately recorded public write events

Raw remote addresses are not stored by the application. Use a strong private `OBSERVATORY_HMAC_KEY`; changing it prevents hashes from being correlated across deployments.

## Safety defaults

- 64 KiB maximum write size by default.
- Per-source write rate limiting.
- No executable uploads.
- Accepted file types are limited to text, Markdown, CSV, JSON, XML, and SVG.
- Common private keys, bearer tokens, API keys, and credential-shaped strings are rejected before storage.
- User HTML is never executed; message bodies are escaped.
- Uploaded files are served with `nosniff` and the site has a restrictive CSP.
- The server never automatically follows URLs submitted by visitors.
- No client-side JavaScript is required.

This is still an Internet-facing writable service. Put it behind HTTPS, keep the process unprivileged, set resource limits, back up the data directory, and review logs.

## Configuration

| Environment variable | Default | Meaning |
| --- | --- | --- |
| `ADDR` | `:8080` | Listen address |
| `DATA_DIR` | `./data` | Persistent state directory |
| `SITE_NAME` | `Patrick's WWW Page` | Visible site title |
| `BASE_URL` | `http://localhost:8080` | Absolute URL used by feeds/discovery |
| `ADMIN_TOKEN` | unset | Bearer token for private observatory |
| `OBSERVATORY_HMAC_KEY` | `local-dev-key` | Key used to pseudonymize remote addresses |
| `MAX_WRITE_BYTES` | `65536` | Maximum write/upload size |
| `WRITE_RATE_PER_HOUR` | `60` | Per-pseudonymous-source write limit |

## Deploy behind Caddy

See `Caddyfile.example`.

```bash
BASE_URL=https://www2.onnwee.me docker compose up -d --build
```

## Research notes

- `docs/BAIT-MATRIX.md` explains every passive discovery surface and what signal it can produce.
- `docs/RESEARCH-GUARDRAILS.md` lists the deliberately excluded techniques that would muddy the experiment or create unnecessary risk.
- `/reference/` contains webmaster-authored reference sheets linked from the homepage, text index, and sitemap. Their publication is recorded in `/reference/changes.txt`.
- Mark operator trials explicitly and test writes in disposable local state. Recruited trials are separate from ambient discovery.

## Tests

```bash
go test ./...
./scripts/demo.sh
```
