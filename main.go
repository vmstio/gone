// Command gone is a tiny HTTP server that responds to every request with
// HTTP 410 Gone and a self-contained page mirroring the Mastodon error page.
package main

import (
	"compress/gzip"
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

// oopsPNG is the static illustration, disintegrated into flying tiles with a
// "Thanos snap" effect on hover/click (mirroring the Mastodon error page,
// but with a bit more flair).
//
//go:embed oops.png
var oopsPNG []byte

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
.dialog__illustration canvas {
  width: 100%;
  max-width: 470px;
  height: auto;
  margin-top: -120px;
  margin-bottom: -45px;
  display: block;
  cursor: pointer;
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
<canvas id="illustration" role="img" aria-label="Mastodon"></canvas>
</div>
<div class="dialog__message">
<h1>__DOMAIN__ is HTTP 410 (Gone)</h1>
</div>
</div>
<script>
(function () {
  var canvas = document.getElementById('illustration');
  var ctx = canvas.getContext('2d');
  var img = new Image();
  img.src = 'data:image/png;base64,__PNG_DATA__';

  var tileSize = 10;
  var tiles = [];
  var progress = 0; // 0 = intact, 1 = fully dissolved
  var target = 0;
  var speed = 1 / 700; // progress units per ms
  var lastTs = null;
  var running = false;

  function buildTiles() {
    var cols = Math.ceil(canvas.width / tileSize);
    var rows = Math.ceil(canvas.height / tileSize);
    tiles = [];
    for (var y = 0; y < rows; y++) {
      for (var x = 0; x < cols; x++) {
        tiles.push({
          x: x * tileSize,
          y: y * tileSize,
          w: Math.min(tileSize, canvas.width - x * tileSize),
          h: Math.min(tileSize, canvas.height - y * tileSize),
          delay: (x / cols) * 0.5 + Math.random() * 0.3,
          dx: (Math.random() - 0.2) * 90,
          dy: -Math.random() * 110 - 20,
          rot: (Math.random() - 0.5) * 1.4
        });
      }
    }
  }

  function drawFrame() {
    ctx.clearRect(0, 0, canvas.width, canvas.height);
    for (var i = 0; i < tiles.length; i++) {
      var t = tiles[i];
      var span = 1 - t.delay;
      var p = span > 0 ? (progress - t.delay) / span : progress > t.delay ? 1 : 0;
      p = Math.min(Math.max(p, 0), 1);
      if (p >= 1) continue;
      if (p <= 0) {
        ctx.drawImage(img, t.x, t.y, t.w, t.h, t.x, t.y, t.w, t.h);
        continue;
      }
      ctx.save();
      ctx.globalAlpha = 1 - p;
      ctx.translate(t.x + t.w / 2 + t.dx * p, t.y + t.h / 2 + t.dy * p);
      ctx.rotate(t.rot * p);
      ctx.drawImage(img, t.x, t.y, t.w, t.h, -t.w / 2, -t.h / 2, t.w, t.h);
      ctx.restore();
    }
  }

  function loop(ts) {
    if (lastTs === null) lastTs = ts;
    var dt = ts - lastTs;
    lastTs = ts;
    if (progress < target) {
      progress = Math.min(target, progress + dt * speed);
    } else if (progress > target) {
      progress = Math.max(target, progress - dt * speed);
    }
    drawFrame();
    if (progress !== target) {
      requestAnimationFrame(loop);
    } else {
      running = false;
      lastTs = null;
    }
  }

  function setTarget(t) {
    target = t;
    if (!running) {
      running = true;
      lastTs = null;
      requestAnimationFrame(loop);
    }
  }

  img.onload = function () {
    canvas.width = img.naturalWidth;
    canvas.height = img.naturalHeight;
    buildTiles();
    drawFrame();
  };

  canvas.addEventListener('mouseenter', function () { setTarget(1); });
  canvas.addEventListener('mouseleave', function () { setTarget(0); });
  canvas.addEventListener('click', function () { setTarget(target > 0.5 ? 0 : 1); });
})();
</script>
</body>
</html>
`

func init() {
	pageTpl = strings.Replace(pageTemplate, "__PNG_DATA__", base64.StdEncoding.EncodeToString(oopsPNG), 1)
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
// discovery endpoint (WebFinger, NodeInfo, or OAuth/OIDC server metadata)
// that should answer with JSON regardless of the Accept header.
func isJSONDiscoveryPath(p string) bool {
	return p == "/.well-known/webfinger" ||
		p == "/.well-known/oauth-authorization-server" ||
		p == "/.well-known/openid-configuration" ||
		isNodeInfoPath(p)
}

// isOAuthJSONPath reports whether the request targets an OAuth/OIDC endpoint
// whose clients are machine callers expecting JSON — token exchange,
// revocation, and OIDC userinfo — as opposed to /oauth/authorize, which is
// the interactive browser login page and should still get the HTML page.
// OAuth client libraries often POST to these without an explicit Accept
// header, so the path is the reliable signal.
func isOAuthJSONPath(p string) bool {
	return p == "/oauth/token" || p == "/oauth/revoke" || p == "/oauth/userinfo"
}

// isJSONPath reports whether the request should get a JSON error regardless of
// the Accept header: the Mastodon REST API (whose clients — apps and scrapers —
// often send a browser-style Accept), fediverse JSON discovery, OAuth/OIDC
// machine endpoints, and any .json resource.
func isJSONPath(p string) bool {
	return strings.HasPrefix(p, "/api/") ||
		isJSONDiscoveryPath(p) ||
		isOAuthJSONPath(p) ||
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

// writeHTML sends the rendered page, gzip-compressing it when the client
// advertises support. The HTML page (~20 KB, mostly the embedded PNG) dwarfs
// every other response this server sends (a few bytes to a few hundred), so
// it's the only branch worth paying the compression overhead for — gzipping
// the other branches' already-minimal bodies would net negative.
func writeHTML(w http.ResponseWriter, r *http.Request, status int, body []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Vary", "Accept, Accept-Encoding")
	if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		w.WriteHeader(status)
		w.Write(body)
		return
	}
	w.Header().Set("Content-Encoding", "gzip")
	w.WriteHeader(status)
	gz := gzip.NewWriter(w)
	gz.Write(body)
	gz.Close()
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
// /robots.txt). The response is chosen from the request path and headers so
// federating servers and API clients get compact machine-readable bodies
// while human browsers get the HTML page.
func handleGone(w http.ResponseWriter, r *http.Request) {
	// The resource is permanently gone, so let the client hold on to the
	// 410 and stop re-requesting. "private" (rather than "public") is
	// deliberate: the body varies by Accept/path, and shared caches like
	// Cloudflare don't key on Vary by default, so a public directive lets
	// one client's response (e.g. a bot's empty body) get served to every
	// other visitor. private confines caching to the requesting client,
	// which does respect Vary. Applies to every branch below.
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("Vary", "Accept")

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
	default:
		writeHTML(w, r, http.StatusGone, renderPage(domainFromRequest(r)))
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
