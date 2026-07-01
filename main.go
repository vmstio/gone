// Command gone is a tiny HTTP server that responds to every request with
// HTTP 410 Gone and a self-contained page mirroring the Mastodon error page.
package main

import (
	_ "embed"
	"encoding/base64"
	"fmt"
	"html"
	"log"
	"net"
	"net/http"
	"os"
	"path"
	"strings"
)

// oopsPNG is the static illustration shown by default; oopsGIF is the
// animated version swapped in on hover (mirroring the Mastodon error page).
//
//go:embed oops.png
var oopsPNG []byte

//go:embed oops.gif
var oopsGIF []byte

// pageTpl is the self-contained HTML with the illustrations inlined as data
// URIs. The __DOMAIN__ placeholder is filled per request with the requested
// host, so a single deployment can serve any number of domains.
var pageTpl string

// defaultDomain is shown when the request carries no usable Host header.
const defaultDomain = "This site"

const pageTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>__DOMAIN__</title>
<style>
:root {
  --color-bg: #fff;
  --color-text: #21212c;
}
@media (prefers-color-scheme: dark) {
  :root {
    --color-bg: #181820;
    --color-text: #f6f6f9;
  }
}
html, body { height: 100%; }
body {
  margin: 0;
  padding: 0;
  background: var(--color-bg);
  color: var(--color-text);
  font-family: ui-sans-serif, system-ui, -apple-system, "Segoe UI", Roboto, sans-serif;
  text-align: center;
  display: flex;
  justify-content: center;
  align-items: center;
}
.dialog { margin: 20px; }
.dialog__illustration img {
  width: 100%;
  max-width: 470px;
  height: auto;
  margin-top: -120px;
  margin-bottom: -45px;
  display: block;
}
.dialog h1 {
  font-size: 20px;
  font-weight: 400;
  line-height: 28px;
}
</style>
</head>
<body>
<div class="dialog">
<div class="dialog__illustration">
<img id="illustration" alt="Mastodon" draggable="false" src="data:image/png;base64,__PNG_DATA__">
</div>
<div class="dialog__message">
<h1>__DOMAIN__ is HTTP 410 (Gone)</h1>
</div>
</div>
<script>
(function () {
  var img = document.getElementById('illustration');
  var still = img.src;
  var animated = 'data:image/gif;base64,__GIF_DATA__';
  img.addEventListener('mouseenter', function () { img.src = animated; });
  img.addEventListener('mouseleave', function () { img.src = still; });
})();
</script>
</body>
</html>
`

func init() {
	pageTpl = strings.Replace(pageTemplate, "__PNG_DATA__", base64.StdEncoding.EncodeToString(oopsPNG), 1)
	pageTpl = strings.Replace(pageTpl, "__GIF_DATA__", base64.StdEncoding.EncodeToString(oopsGIF), 1)
}

// rawHost returns the requested host, preferring the X-Forwarded-Host header
// set by proxies such as DigitalOcean App Platform and falling back to the
// request Host. It may still include a port.
func rawHost(r *http.Request) string {
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	// X-Forwarded-Host may contain a comma-separated list; use the first.
	if i := strings.IndexByte(host, ','); i >= 0 {
		host = host[:i]
	}
	return strings.TrimSpace(host)
}

// domainFromRequest returns the requested host without any port, for display.
func domainFromRequest(r *http.Request) string {
	host := rawHost(r)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if host == "" {
		return defaultDomain
	}
	return host
}

// requestURL reconstructs the absolute URL that was requested, used as the id
// of the ActivityPub Tombstone. It trusts the proxy's forwarding headers.
func requestURL(r *http.Request) string {
	scheme := r.Header.Get("X-Forwarded-Proto")
	if scheme == "" {
		scheme = "https"
	}
	return scheme + "://" + rawHost(r) + r.URL.RequestURI()
}

// wantsAny reports whether the Accept header mentions any of the given media
// types. Matching is a simple case-insensitive substring test, which is enough
// to distinguish ActivityPub/JSON clients from browsers.
func wantsAny(accept string, mediaTypes ...string) bool {
	accept = strings.ToLower(accept)
	for _, mt := range mediaTypes {
		if strings.Contains(accept, mt) {
			return true
		}
	}
	return false
}

// isMatrixPath reports whether the request targets a Matrix Client-Server or
// Server-Server (federation) API, or Matrix delegation discovery. Matrix
// clients often omit an Accept header, so the path is the reliable signal.
func isMatrixPath(p string) bool {
	return strings.HasPrefix(p, "/_matrix/") || strings.HasPrefix(p, "/.well-known/matrix/")
}

// isHostMetaPath reports whether the request targets the host-meta discovery
// document (RFC 6415) that bootstraps WebFinger. It is served as XRD XML by
// default, with a JSON (JRD) variant at the .json suffix.
func isHostMetaPath(p string) bool {
	return p == "/.well-known/host-meta" || p == "/.well-known/host-meta.json"
}

// isNodeInfoPath reports whether the request targets NodeInfo discovery or a
// NodeInfo document. Fediverse crawlers and stats sites probe these heavily,
// often without a useful Accept header, so the path is the signal.
func isNodeInfoPath(p string) bool {
	return p == "/.well-known/nodeinfo" ||
		p == "/.well-known/x-nodeinfo2" ||
		strings.HasPrefix(p, "/nodeinfo/")
}

// isJSONDiscoveryPath reports whether the request targets a fediverse JSON
// discovery endpoint (WebFinger or NodeInfo) that should answer with JSON
// regardless of the Accept header.
func isJSONDiscoveryPath(p string) bool {
	return p == "/.well-known/webfinger" || isNodeInfoPath(p)
}

// isJSONPath reports whether the request should get a JSON error regardless of
// the Accept header: the Mastodon REST API (whose clients — apps and scrapers —
// often send a browser-style Accept), fediverse JSON discovery, and any .json
// resource.
func isJSONPath(p string) bool {
	return strings.HasPrefix(p, "/api/") ||
		isJSONDiscoveryPath(p) ||
		strings.HasSuffix(p, ".json")
}

// isFeedPath reports whether the request is for an RSS or Atom feed. A dead
// feed answered with a 410 tells readers to stop polling.
func isFeedPath(p string) bool {
	return strings.HasSuffix(p, ".rss") || strings.HasSuffix(p, ".atom")
}

// isHiddenProbe reports whether the request targets a dotfile (a path segment
// starting with "."), as vulnerability scanners hunting for /.env, /.git, and
// the like do. The legitimate /.well-known/ tree is excluded. These get an
// empty 410 instead of the ~150 KB page.
func isHiddenProbe(p string) bool {
	if strings.HasPrefix(p, "/.well-known/") {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if len(seg) > 1 && seg[0] == '.' {
			return true
		}
	}
	return false
}

// isInboxPath reports whether the request targets an ActivityPub inbox (the
// shared /inbox or a per-actor /users/x/inbox). Federation delivery POSTs land
// here; the delivering server only needs the 410 status to stop delivering, so
// these get an empty body.
func isInboxPath(p string) bool {
	return strings.HasSuffix(p, "/inbox")
}

// isActivityPub reports whether the request is ActivityPub, by either the
// Accept header (actor fetches) or the Content-Type header (inbox deliveries,
// which are POSTed with application/activity+json and may not set Accept).
func isActivityPub(r *http.Request) bool {
	return wantsAny(r.Header.Get("Accept"), "application/activity+json", "application/ld+json") ||
		wantsAny(r.Header.Get("Content-Type"), "application/activity+json", "application/ld+json")
}

// fediverseUAs are substrings identifying known fediverse server software and
// relays. Matched case-insensitively against the User-Agent so servers that
// fetch without an explicit ActivityPub Accept (link previews, generic GETs)
// still get a machine-readable response instead of the HTML page.
var fediverseUAs = []string{
	"mastodon", "pleroma", "akkoma", "misskey", "iceshrimp", "sharkey",
	"lemmy", "pixelfed", "peertube", "friendica", "gotosocial",
	"snac", "fedify", "bookwyrm", "mobilizon", "writefreely",
	"hubzilla", "honk", "microblog.pub", "wildebeest",
	"activity-relay", "activityrelay", "activitypub",
	"joinmastodon", "fedi.buzz",
}

// isFediverseServerUA reports whether the User-Agent identifies a known
// fediverse server or relay.
func isFediverseServerUA(ua string) bool {
	ua = strings.ToLower(ua)
	for _, s := range fediverseUAs {
		if strings.Contains(ua, s) {
			return true
		}
	}
	return false
}

// mediaExts are file extensions that a bucket of images/attachments would have
// served. Requests for these get an empty 410 rather than a page body.
var mediaExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true,
	".avif": true, ".bmp": true, ".svg": true, ".ico": true, ".heic": true,
	".tif": true, ".tiff": true,
	".mp4": true, ".webm": true, ".mov": true, ".m4v": true, ".ogv": true,
	".mp3": true, ".ogg": true, ".oga": true, ".m4a": true, ".wav": true, ".flac": true,
}

// isMediaRequest reports whether the request is for image/video/audio media,
// as a decommissioned S3 bucket of attachments would receive. These are
// <img>/<video> subresources or server-side refetches that discard any HTML
// body, so they get an empty 410 to save bandwidth.
//
// A browser page navigation also lists image types in Accept (e.g.
// "text/html,...,image/avif,image/webp"), so an Accept-based match only counts
// when text/html is absent. The file extension and known Mastodon media path
// prefixes are checked as fallbacks for clients that send a browser-style
// Accept (e.g. hotlinked <img> tags pointing at /media_proxy/…).
func isMediaRequest(r *http.Request) bool {
	p := r.URL.Path
	if strings.HasPrefix(p, "/media_proxy/") ||
		strings.HasPrefix(p, "/media_attachments/") ||
		strings.HasPrefix(p, "/system/") {
		return true
	}
	if mediaExts[strings.ToLower(path.Ext(p))] {
		return true
	}
	accept := strings.ToLower(r.Header.Get("Accept"))
	return !strings.Contains(accept, "text/html") &&
		(strings.Contains(accept, "image/") ||
			strings.Contains(accept, "video/") ||
			strings.Contains(accept, "audio/"))
}

// renderPage fills the per-request domain into the cached template.
func renderPage(domain string) []byte {
	return []byte(strings.ReplaceAll(pageTpl, "__DOMAIN__", html.EscapeString(domain)))
}

// statusRecorder wraps http.ResponseWriter to capture the status code and the
// number of body bytes written, for logging.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

// clientIP returns the originating client address, preferring the first entry
// of X-Forwarded-For (set by App Platform's proxy) over the direct RemoteAddr.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	return r.RemoteAddr
}

// logRequests logs one line per request so you can see what is being probed.
// The response Content-Type indicates which branch matched. Health checks are
// skipped to avoid drowning the log. Set LOG_REQUESTS=false to disable.
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		ct := rec.Header().Get("Content-Type")
		if ct == "" {
			ct = "-"
		}
		log.Printf("%d %s %s %dB ct=%q host=%q ip=%s ua=%q",
			rec.status, r.Method, r.URL.RequestURI(), rec.bytes,
			ct, rawHost(r), clientIP(r), r.UserAgent())
	})
}

// handleHealthz responds 200 for platform health checks. Kept separate so that
// real traffic (all 410) never makes the health check fail.
func handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, "ok")
}

// handleGone answers every non-health request with HTTP 410 Gone (except
// /robots.txt). The response is chosen from the request path, headers, and
// User-Agent so federating servers and API clients get compact machine-readable
// bodies while human browsers get the HTML page.
func handleGone(w http.ResponseWriter, r *http.Request) {
	// The resource is permanently gone, so let caches and crawlers hold on
	// to the 410 and stop re-requesting. Applies to every branch below.
	w.Header().Set("Cache-Control", "public, max-age=86400")

	switch {
	case r.URL.Path == "/robots.txt":
		// Actively steer crawlers away. Unlike everything else this is a
		// live 200 directive, not a 410.
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("User-agent: *\nDisallow: /\n"))
	case isHiddenProbe(r.URL.Path):
		// Vulnerability scanners probing for dotfiles (.env, .git, …). Send
		// an empty 410 rather than the ~150 KB page.
		w.WriteHeader(http.StatusGone)
	case isMediaRequest(r):
		// Former bucket media: the client (an <img>/<video> tag or a
		// server refetch) ignores any body, so send an empty 410.
		w.WriteHeader(http.StatusGone)
	case isHostMetaPath(r.URL.Path):
		// host-meta discovery. Honour the requested representation: JSON
		// (JRD) for the .json variant, XRD XML otherwise. The body is empty
		// since the status conveys everything the client needs.
		if strings.HasSuffix(r.URL.Path, ".json") {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
		} else {
			w.Header().Set("Content-Type", "application/xrd+xml; charset=utf-8")
		}
		w.WriteHeader(http.StatusGone)
	case isMatrixPath(r.URL.Path):
		// Matrix standard error response shape. There is no dedicated
		// "gone" errcode, so M_UNKNOWN carries a descriptive message.
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusGone)
		w.Write([]byte(`{"errcode":"M_UNKNOWN","error":"This Matrix homeserver has been decommissioned."}` + "\n"))
	case isInboxPath(r.URL.Path):
		// Inbox delivery POSTs: the remote server only needs the 410 status
		// to stop delivering, so skip the Tombstone body.
		w.Header().Set("Content-Type", "application/activity+json; charset=utf-8")
		w.WriteHeader(http.StatusGone)
	case isActivityPub(r):
		// ActivityStreams Tombstone: the canonical representation of a
		// resource that once existed and is now permanently gone. Fetches
		// of actors, statuses, etc. get the full object.
		body := fmt.Sprintf(`{"@context":"https://www.w3.org/ns/activitystreams","type":"Tombstone","id":%q}`+"\n", requestURL(r))
		w.Header().Set("Content-Type", "application/activity+json; charset=utf-8")
		w.WriteHeader(http.StatusGone)
		w.Write([]byte(body))
	case isJSONPath(r.URL.Path),
		wantsAny(r.Header.Get("Accept"), "application/json", "application/jrd+json"):
		// Mastodon REST API, WebFinger / NodeInfo discovery, .json
		// resources (all by path, any Accept), and generic JSON clients get
		// a small JSON error instead of the ~150 KB HTML page.
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusGone)
		w.Write([]byte(`{"error":"Gone"}` + "\n"))
	case isFeedPath(r.URL.Path):
		// Dead RSS/Atom feed: return the matching feed content type so
		// readers recognise the 410 and stop polling. The body is empty.
		if strings.HasSuffix(r.URL.Path, ".atom") {
			w.Header().Set("Content-Type", "application/atom+xml; charset=utf-8")
		} else {
			w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
		}
		w.WriteHeader(http.StatusGone)
	case strings.HasPrefix(r.URL.Path, "/tags/"):
		// Hashtag pages are crawler traffic, not human visits, so drop them
		// with an empty 410 instead of the ~150 KB page.
		w.WriteHeader(http.StatusGone)
	case isFediverseServerUA(r.UserAgent()):
		// A known fediverse server that didn't send an explicit AP Accept
		// (e.g. link-preview or generic fetch). Give it a Tombstone rather
		// than the HTML page.
		body := fmt.Sprintf(`{"@context":"https://www.w3.org/ns/activitystreams","type":"Tombstone","id":%q}`+"\n", requestURL(r))
		w.Header().Set("Content-Type", "application/activity+json; charset=utf-8")
		w.WriteHeader(http.StatusGone)
		w.Write([]byte(body))
	default:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusGone)
		w.Write(renderPage(domainFromRequest(r)))
	}
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// Route directly rather than through http.ServeMux, which 301-redirects
	// paths it wants to "clean" — e.g. malformed media srcset URLs containing
	// "//". Handling routing ourselves lets those get a direct 410.
	root := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			handleHealthz(w, r)
			return
		}
		handleGone(w, r)
	})

	var handler http.Handler = root
	if os.Getenv("LOG_REQUESTS") != "false" {
		handler = logRequests(root)
	}

	addr := ":" + port
	log.Printf("gone listening on %s", addr)
	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Fatal(err)
	}
}
