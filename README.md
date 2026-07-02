# gone

A tiny Go web server that responds to **every** request with `HTTP 410 Gone`
and a self-contained page mirroring the Mastodon error page
(https://vmst.io/500.htm).

The illustrations are embedded into the binary and inlined into the page as
base64 data URIs, so the app has no external dependencies and serves a single
410 response per request. A static image shows by default and swaps to the
animated version on hover, mirroring the Mastodon error page. Dark mode follows
the browser's `prefers-color-scheme`.

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
from the request path and headers. Every response is `410 Gone` **except**
`robots.txt`, which is a live `200` directive:

| Match (in order)                                  | Response |
|---------------------------------------------------|----------|
| Path `/robots.txt`                                | `200` `text/plain` — `User-agent: *` / `Disallow: /`, to steer crawlers away |
| Dotfile probe (a path segment starting with `.`, e.g. `/.env`, `/.git/HEAD`; `/.well-known/…` excluded) | empty body — vulnerability scanner noise |
| Media request (see below)                         | empty body — the client discards it anyway |
| Path `/.well-known/host-meta[.json]`              | empty body with `application/xrd+xml` (or `application/json` for the `.json` variant) — WebFinger discovery document |
| Path `/_matrix/…` or `/.well-known/matrix/…`      | Matrix error `{"errcode":"M_UNKNOWN","error":"…"}` (Matrix clients often send no `Accept`, so the path is the signal) |
| Path ending `/inbox` (shared `/inbox` or `/users/x/inbox`)         | empty body with `application/activity+json` — federation delivery POSTs only need the 410 status to stop delivering, so the Tombstone body is skipped |
| ActivityPub: `Accept` **or** `Content-Type` is `application/activity+json` / `application/ld+json` | ActivityStreams [`Tombstone`](https://www.w3.org/TR/activitystreams-vocabulary/#dfn-tombstone) whose `id` is the requested URL, for actor/status fetches. `Content-Type` matching also covers AP POSTs that aren't to an inbox. |
| Path `/api/…`, `/.well-known/webfinger`, `/.well-known/nodeinfo`, `/nodeinfo/…`, any `*.json`, or `Accept: application/json` / `application/jrd+json` | `{"error":"Gone"}`. The Mastodon REST API is matched **by path** because its clients (apps and scrapers alike) usually send a browser-style `Accept` — this is the highest-volume traffic, so answering with a 17-byte JSON body instead of the ~150 KB page is the single biggest bandwidth saving. |
| Path ending `.rss` / `.atom`                      | empty body with `application/rss+xml` / `application/atom+xml` — dead feed, so readers stop polling |
| Path `/tags/…`                                    | empty body — hashtag pages are crawler traffic, not human visits |
| anything else (real browsers, and any other bot/crawler by request path or `Accept`) | the HTML page |

All 410 responses carry `Cache-Control: private, max-age=86400` so the
requesting client holds on to the 410 and stops re-requesting a permanently
gone resource, without a shared cache serving one client's response (e.g. a
bot's empty body) to every other visitor.

This lets one deployment gracefully retire a Mastodon/ActivityPub instance, a
Matrix homeserver, and a media/attachment bucket at the same time.

### Media (former S3 bucket) requests

A bucket of images/attachments is requested by `<img>`/`<video>` tags and
server-side refetches that ignore any HTML body, so serving the ~150 KB page
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
