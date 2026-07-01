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

Since this is meant to stand in for a decommissioned Mastodon server, most
traffic comes from other ActivityPub servers rather than browsers. The response
body is negotiated from the `Accept` header — the status is always `410 Gone`:

| Client `Accept`                                  | Response body |
|--------------------------------------------------|---------------|
| `application/activity+json`, `application/ld+json` | ActivityStreams [`Tombstone`](https://www.w3.org/TR/activitystreams-vocabulary/#dfn-tombstone) whose `id` is the requested URL |
| `application/json`, `application/jrd+json`         | `{"error":"Gone"}` (covers WebFinger, API, NodeInfo clients) |
| anything else (browsers)                          | the HTML page |

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
