# gone

A tiny Go web server that responds to **every** request with `HTTP 410 Gone`
and a self-contained page mirroring the Mastodon error page
(https://vmst.io/500.htm).

The Mastodon logo is embedded into the binary as SVG and inlined into the
page as a base64 data URI, so the app has no external dependencies and
serves a single 410 response per request. It's rasterized onto a `<canvas>`
at a higher resolution than its intrinsic size (for crisp display) and
disintegrates into flying, fading tiles on hover/click — a "Thanos snap"
effect. Dark mode follows the browser's `prefers-color-scheme`.

The HTML page is ~7.5 KB (~3.6 KB gzipped). See [Page size](#page-size) for
how it's kept small.

The displayed domain is taken from the request (`X-Forwarded-Host`, falling
back to `Host`, with any port stripped and the value HTML-escaped), so a single
deployment can serve any number of domains.

## Run locally

```sh
go run .
# then visit http://localhost:8080
```

Listens on `$PORT` (default `8080`).

```sh
curl -i http://localhost:8080/
# HTTP/1.1 410 Gone
```

## Content negotiation

Since this is meant to stand in for a decommissioned federated server, most
traffic comes from other servers rather than browsers. The response is chosen
from the request path and headers, checked in this order. Every response is
`410 Gone` **except** `/robots.txt`, which is a live `200` directive:

1. **`/robots.txt`** → `200 text/plain` — `User-agent: *` / `Disallow: /`, to
   steer crawlers away.
2. **Dotfile probe** — a path segment starting with `.` (e.g. `/.env`,
   `/.git/HEAD`; `/.well-known/…` is excluded from this rule) → empty body —
   vulnerability scanner noise.
3. **Media request** (see [Media](#media-former-s3-bucket-requests) below) →
   empty body — the client discards it anyway.
4. **`/.well-known/host-meta[.json]`** → empty body, `application/xrd+xml`
   (or `application/json` for the `.json` variant) — WebFinger discovery
   document.
5. **Matrix** — `/_matrix/…` or `/.well-known/matrix/…` → Matrix error
   `{"errcode":"M_UNKNOWN","error":"…"}`. Matched by path, since Matrix
   clients often send no `Accept` header.
6. **ActivityPub inbox** — path ending `/inbox` (shared `/inbox` or
   `/users/x/inbox`) → empty body, `application/activity+json`. Federation
   delivery POSTs only need the 410 status to stop delivering, so the
   Tombstone body is skipped.
7. **ActivityPub** — `Accept` **or** `Content-Type` is
   `application/activity+json` / `application/ld+json` → ActivityStreams
   [`Tombstone`](https://www.w3.org/TR/activitystreams-vocabulary/#dfn-tombstone)
   whose `id` is the requested URL, for actor/status fetches.
   `Content-Type` matching also covers AP POSTs that aren't to an inbox.
8. **JSON API and discovery** → `{"error":"Gone"}`. Matched **by path**,
   since these clients (apps, scrapers, OAuth libraries) often send a
   browser-style `Accept` or none at all:
   - `/api/…` — the Mastodon REST API, and the highest-volume traffic this
     server sees, so a 17-byte JSON body instead of the ~15 KB page is the
     single biggest bandwidth saving
   - `/.well-known/webfinger`, `/.well-known/nodeinfo`, `/nodeinfo/…` —
     fediverse discovery
   - `/.well-known/oauth-authorization-server`,
     `/.well-known/openid-configuration` — OAuth/OIDC server metadata
   - `/oauth/token`, `/oauth/revoke`, `/oauth/userinfo` — OAuth/OIDC machine
     endpoints (`/oauth/authorize` is deliberately excluded, since it's the
     interactive browser login page and still gets the HTML page)
   - any `*.json` path, or `Accept: application/json` / `application/jrd+json`
9. **Feed** — path ending `.rss` / `.atom` → empty body,
   `application/rss+xml` / `application/atom+xml` — dead feed, so readers
   stop polling.
10. **`/tags/…`** → empty body — hashtag pages are crawler traffic, not
    human visits.
11. **anything else** (real browsers, and any other bot/crawler not caught
    above) → the HTML page.

All 410 responses carry `Cache-Control: private, max-age=86400` so the
requesting client holds on to the 410 and stops re-requesting a permanently
gone resource, without a shared cache serving one client's response (e.g. a
bot's empty body) to every other visitor.

This lets one deployment gracefully retire a Mastodon/ActivityPub instance, a
Matrix homeserver, and a media/attachment bucket at the same time.

### Media (former S3 bucket) requests

A bucket of images/attachments is requested by `<img>`/`<video>` tags and
server-side refetches that ignore any HTML body, so serving the ~7.5 KB page
for each would waste bandwidth. Such requests get an **empty 410** instead. A
request counts as media when any of:

- the path starts with a Mastodon media prefix — `/media_proxy/`,
  `/media_attachments/`, or `/system/` (these often have no file extension and
  come with a browser-style `Accept`, e.g. hotlinked images); or
- the path ends in a known media extension (`.jpg`, `.png`, `.gif`, `.webp`,
  `.mp4`, `.mp3`, …); or
- the `Accept` header asks for `image/*`, `video/*`, or `audio/*` **and** does
  not include `text/html` (so a normal browser page load, whose `Accept` also
  lists image types, still gets the HTML page).

Example ActivityPub actor fetch:

```sh
curl -i -H 'Accept: application/activity+json' https://your.domain/users/alice
# HTTP/1.1 410 Gone
# Content-Type: application/activity+json; charset=utf-8
# {"@context":"https://www.w3.org/ns/activitystreams","type":"Tombstone","id":"https://your.domain/users/alice"}
```

## Page size

The full HTML page (the default branch above, sent to real browsers) is the
only response worth optimizing for size — every other branch is already an
empty body or a body measured in bytes. It's currently **~7.5 KB raw, ~3.6 KB
gzipped**, down from an initial ~150 KB when the illustration was a raster
PNG with a separate animated GIF for the hover state. That came from a few
changes, in order of impact:

1. **Drop the embedded GIF.** The hover/click animation used to swap the
   `<img>` source to a separate ~93 KB animated GIF. Replacing it with a
   canvas-drawn "Thanos snap" tile-dissolve effect (see above) removed that
   asset entirely.
2. **Switch the illustration from PNG to inline SVG.** The Mastodon logo is
   ~2.5 KB of vector source (~3.3 KB as base64) versus an optimized PNG that
   still needed several kilobytes after quantization and resizing — vector
   art wins outright for flat-color marks like this, with no
   resolution/quality tradeoff, since the canvas rasterizes it at whatever
   resolution it's drawn at (see `RENDER_SCALE` in the page script).
3. **Compress the response.** `writeHTML` gzips the page when the client
   sends `Accept-Encoding: gzip`. The other branches' bodies are already only
   a few bytes to a few hundred, where gzip's per-response overhead would net
   negative, so only this branch compresses.

Before switching to SVG, the PNG illustration itself was optimized:
requantized with `pngquant` and recompressed with `zopflipng`, then
downscaled to match its CSS display size using nearest-neighbor resampling
(smooth filters like Lanczos paradoxically made the file *larger*, since
anti-aliased edge pixels add colors that hurt palette-based compression more
than the pixel-count reduction saves). That workflow is moot now that the
asset is vector, but it's the right approach for any raster art added here
in the future. WebP/AVIF conversion was also evaluated for the PNG and
rejected — lossless WebP only saved ~6% over the optimized PNG, and lossy
WebP came out larger on this flat-color art.

## Logging

One line is logged per request (to stdout, where App Platform collects it) so
you can see what is being probed. The Content-Type shows which branch matched:

```
410 GET /users/alice 112B ct="application/activity+json; charset=utf-8" host="fedi.example" ip=203.0.113.5 ua="TestBot/1.0"
200 GET /robots.txt 26B ct="text/plain; charset=utf-8" host="example.com" ip=203.0.113.9 ua="Googlebot/2.1"
```

Fields: status, method, path, response bytes, `ct` (Content-Type), `host`
(requested host), `ip` (first `X-Forwarded-For` entry, falling back to the
remote address), and `ua` (User-Agent). Health checks (`/healthz`) are not
logged. Set `LOG_REQUESTS=false` to disable request logging entirely.

## Endpoints

- `/*` — returns `410 Gone`; response chosen by path/headers (see above).
- `/robots.txt` — returns `200 OK` with a disallow-all directive.
- `/healthz` — returns `200 OK` for platform health checks (not logged).

## Deploy to DigitalOcean App Platform

App Platform's native Go buildpack builds this with no Dockerfile. The included
[`.do/app.yaml`](.do/app.yaml) app spec configures the service and points the
health check at `/healthz` (App Platform treats a 410 as unhealthy, so the
default `/` health check would fail).

Create the app from the dashboard (pointing at this repo) or with the CLI:

```sh
doctl apps create --spec .do/app.yaml
```

Update the `github.repo` field in `.do/app.yaml` to match your repository.
