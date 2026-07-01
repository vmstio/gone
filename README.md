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
traffic comes from other servers rather than browsers. The response body is
chosen from the request path (Matrix) and the `Accept` header (ActivityPub /
JSON) — the status is always `410 Gone`:

| Match                                             | Response body |
|---------------------------------------------------|---------------|
| Media request (see below)                         | empty body — the client discards it anyway |
| Path `/_matrix/…` or `/.well-known/matrix/…`      | Matrix error `{"errcode":"M_UNKNOWN","error":"…"}` (Matrix clients often send no `Accept`, so the path is the signal) |
| `Accept: application/activity+json` or `application/ld+json` | ActivityStreams [`Tombstone`](https://www.w3.org/TR/activitystreams-vocabulary/#dfn-tombstone) whose `id` is the requested URL |
| `Accept: application/json` or `application/jrd+json`         | `{"error":"Gone"}` (covers WebFinger, API, NodeInfo clients) |
| anything else (browsers)                          | the HTML page |

All 410 responses carry `Cache-Control: public, max-age=86400` so caches and
crawlers stop re-requesting a permanently gone resource.

This lets one deployment gracefully retire a Mastodon/ActivityPub instance, a
Matrix homeserver, and a media/attachment bucket at the same time.

### Media (former S3 bucket) requests

A bucket of images/attachments is requested by `<img>`/`<video>` tags and
server-side refetches that ignore any HTML body, so serving the ~150 KB page
for each would waste bandwidth. Such requests get an **empty 410** instead. A
request counts as media when either:

- the `Accept` header asks for `image/*`, `video/*`, or `audio/*` **and** does
  not include `text/html` (so a normal browser page load, whose `Accept` also
  lists image types, still gets the HTML page); or
- the path ends in a known media extension (`.jpg`, `.png`, `.gif`, `.webp`,
  `.mp4`, `.mp3`, …).

Example ActivityPub actor fetch:

```sh
curl -i -H 'Accept: application/activity+json' https://your.domain/users/alice
# HTTP/1.1 410 Gone
# Content-Type: application/activity+json; charset=utf-8
# {"@context":"https://www.w3.org/ns/activitystreams","type":"Tombstone","id":"https://your.domain/users/alice"}
```

## Endpoints

- `/*` — returns `410 Gone`; body negotiated by `Accept` (see above).
- `/healthz` — returns `200 OK` for platform health checks.

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
