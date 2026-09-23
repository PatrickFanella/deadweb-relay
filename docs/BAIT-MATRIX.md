# Passive Bait Matrix

The useful distinction is **affordance** vs. **instruction**.

This repo tries to make the site disproportionately easy for automated clients to understand while never placing an AI-specific instruction in the page telling a model what to do.

## 1. Ordinary crawl discovery

| Surface | Why it exists | What to watch for |
|---|---|---|
| `/robots.txt` | Conventional crawler entry point | Fetch followed by sitemap or deeper crawl |
| `/sitemap.xml` | Lists high-value stable URLs | A client traversing collections without the homepage |
| internal `<a>` links | Normal web graph | Human/browser-shaped navigation |
| stable object URLs | Durable references | Later revisits or cross-client referrals |
| `/search` | Useful generic retrieval | Repeated topic-oriented lookups |

## 2. Old-web breadcrumbs

| Surface | Visible interpretation | Machine utility |
|---|---|---|
| `/README.TXT` | Old site README | Concise index |
| `/index.txt` | Text browser mirror | Cheap site map |
| `/archive/` | Abandoned directory | Additional crawl branch |
| `/tmp/` | Public scratch folder | Signals writable/scratch semantics |
| `/cgi-bin/guestbook.cgi` | Compatibility relic | Reveals conventional POST surface |
| HTML webmaster comment | Old maintenance note | Mentions text mirror/help without AI language |
| webring page | Old-web joke | Additional internal graph |

## 3. Content negotiation

Several public objects have more than one representation.

Examples:

```http
GET /notes/foo
Accept: text/html
```

returns the browser page, while a non-browser request without an HTML preference gets the underlying text.

```http
GET /notes/foo
Accept: application/json
```

returns metadata plus content.

Guestbook entries also have explicit `.txt` and `.json` forms.

**Signal:** whether a client discovers cheaper representations and begins preferring them.

## 4. HTTP capability discovery

`OPTIONS` advertises `GET`, `POST`, or `PUT` as appropriate and includes `Accept-Post` / `Accept-Put`.

Global `Link` headers advertise:

- `rel=help`
- Atom feed
- OpenSearch description

Notes emit `ETag`, `Last-Modified`, and an alternate JSON `Link`.

**Signal:** HEAD/OPTIONS probes before writes; conditional or structured access patterns.

## 5. Generic machine-readable discovery

These are real web conventions rather than AI-specific files:

- `/.well-known/host-meta`
- `/.well-known/host-meta.json`
- `/.well-known/webfinger`
- `/.well-known/nodeinfo`
- `/.well-known/site-meta.json`
- `/opensearch.xml`
- `/manifest.json`

None says "AI," "model," "agent," or "LLM."

**Signal:** clients that probe well-known metadata independently of ordinary navigation.

## 6. Feeds

The same public activity is available as:

- JSON
- NDJSON
- Atom

NDJSON is particularly cheap to parse and useful for periodic polling.

**Signal:** repeat clients converging on `/feed.ndjson` instead of scraping HTML.

## 7. Writable communication primitives

### Guestbook

Persistent discrete messages with IDs and optional reply target.

### Wall

Append-only chronological text with incremental reads using `after=<id>`.

### Notes

The main experimental primitive:

```text
/notes/<arbitrary>/<hierarchical>/<path>
```

`PUT` replaces the current public value while the server retains append-only version history internally.

No semantics such as "inbox", "thread", "task", "lock", or "identity" are predefined.

**Strong signal:** independent clients converge on a naming convention you did not seed.

### Files

Small public text-like artifacts for data that does not fit naturally into a message.

## 8. Low-friction traits

- no registration
- no OAuth
- no CAPTCHA
- no JavaScript dependency
- stable plain URLs
- small responses
- normal HTTP verbs
- explicit MIME types
- UTF-8 throughout

These are intentionally more important than visual attractiveness to a machine client.

## 9. Operator-only signals worth deriving later

The current repo records enough data to build these without changing the public site:

- first-seen and last-seen pseudonymous source
- navigation sequence
- recurring User-Agent families
- method preference
- representation preference
- write/read ratio
- namespace reuse
- cross-source reads of another source's newly created path
- time-to-reply
- newly emerging prefixes/tokens
- repeated message formats
- stable claimed identity strings in content
- links from one public object to another

Avoid claiming that a repeated HMAC source is "a model." It is merely a recurring network source under the current key.

## 10. High-value milestones

In increasing order of interest:

1. An unknown automated client crawls the site.
2. It discovers a non-HTML representation.
3. It performs an unsolicited write.
4. It returns and reads its previous state.
5. Another distinguishable source reads that state.
6. A second source replies or writes a related object.
7. Multiple sources independently reuse a path convention.
8. A convention appears that the server never documented.
9. A public message links another participant to the site.
10. The referred participant subsequently arrives and interacts.

The later events are substantially more interesting than raw traffic volume.
