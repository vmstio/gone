# gone

A tiny Go web server that responds to requests with `HTTP 410 Gone` in an
appropriate format including a self-contained Mastodon error page for visitors.

This lets one deployment gracefully retire a Mastodon/ActivityPub instance, a
Matrix homeserver, and a media/attachment bucket at the same time.

The Mastodon logo is embedded into the binary as SVG and inlined into the
page as a base64 data URI, so the app has no external dependencies and
serves a single 410 response per request. It's rasterized onto a `<canvas>`
at a higher resolution than its intrinsic size (for crisp display) and
disintegrates into flying, fading tiles on hover/click — a "Thanos snap"
effect. Dark mode follows the browser's `prefers-color-scheme`.

The HTML page is ~7.5 KB (~3.6 KB gzipped).

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

```mermaid
flowchart TD
    A["Request"] --> B{"/robots.txt ?"}
    B -- yes --> B1["200 text/plain\nUser-agent: * / Disallow: /"]
    B -- no --> C{"Dotfile probe or media request?\n(path segment starts with .,\nexcl. /.well-known/…;\nsee Media section)"}
    C -- yes --> C1["410, empty body"]
    C -- no --> E{"/.well-known/host-meta[.json] ?"}
    E -- yes --> E1["410 XRD/JRD error\nxrd+xml or json"]
    E -- no --> F{"Matrix?\n/_matrix/… or\n/.well-known/matrix/…"}
    F -- yes --> F1["410 Matrix error JSON\n{errcode: M_UNKNOWN}"]
    F -- no --> H{"ActivityPub?\npath ends /inbox, or\nAccept/Content-Type is\nactivity+json / ld+json"}
    H -- yes --> H1["410 {#quot;error#quot;:#quot;Gone#quot;}\napplication/activity+json"]
    H -- no --> I{"JSON API / discovery path?\n/api/…, webfinger, nodeinfo,\noauth metadata & endpoints,\n*.json, or Accept: json"}
    I -- yes --> I1["410 {#quot;error#quot;:#quot;Gone#quot;}\njson"]
    I -- no --> J{"Feed?\npath ends .rss / .atom"}
    J -- yes --> J1["410, empty body\nrss+xml / atom+xml"]
    J -- no --> L["410, HTML page"]
```

Every branch returns `410 Gone` except `/robots.txt`, which is a live `200`. A few
notes that don't fit in the diagram:

- **Media** requests are covered in [Media](#media-former-s3-bucket-requests)
  below.
- **ActivityPub inbox** and **ActivityPub** actor/status fetches both get the
  same plain JSON error as JSON API and discovery below — real Mastodon only
  branches on the `410` status when dereferencing (it parses the response
  body solely on `200`), so an ActivityStreams `Tombstone` object buys
  nothing over a generic error body.
- **ActivityPub** `Content-Type` matching also covers AP POSTs that aren't to
  an inbox.
- **JSON API and discovery** is matched **by path**, since these clients
  (apps, scrapers, OAuth libraries) often send a browser-style `Accept` or
  none at all. `/api/…` is the Mastodon REST API and the highest-volume
  traffic this server sees, so a 17-byte JSON body instead of the ~15 KB page
  is the single biggest bandwidth saving. `/oauth/authorize` is deliberately
  excluded from the OAuth endpoints, since it's the interactive browser login
  page and still gets the HTML page.
- **Feed** matches are a dead end for readers that would otherwise keep
  polling.

All 410 responses carry `Cache-Control: private, max-age=86400` so the
requesting client holds on to the 410 and stops re-requesting a permanently
gone resource, without a shared cache serving one client's response (e.g. a
bot's empty body) to every other visitor.

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
# {"error":"Gone"}
```

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
