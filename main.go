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

// renderPage fills the per-request domain into the cached template.
func renderPage(domain string) []byte {
	return []byte(strings.ReplaceAll(pageTpl, "__DOMAIN__", html.EscapeString(domain)))
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	mux := http.NewServeMux()

	// Health check endpoint returns 200 so platform health checks pass while
	// all real traffic still receives 410. App Platform treats 410 as
	// unhealthy, so point the service's health check at this path.
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})

	// Every other request gets HTTP 410 Gone. The body is negotiated from the
	// Accept header so federating ActivityPub servers receive JSON while
	// browsers get the HTML page. The status is always 410.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case wantsAny(r.Header.Get("Accept"), "application/activity+json", "application/ld+json"):
			// ActivityStreams Tombstone: the canonical representation of a
			// resource that once existed and is now permanently gone.
			body := fmt.Sprintf(`{"@context":"https://www.w3.org/ns/activitystreams","type":"Tombstone","id":%q}`+"\n", requestURL(r))
			w.Header().Set("Content-Type", "application/activity+json; charset=utf-8")
			w.WriteHeader(http.StatusGone)
			w.Write([]byte(body))
		case wantsAny(r.Header.Get("Accept"), "application/json", "application/jrd+json"):
			// Generic API / WebFinger clients get a small JSON error.
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusGone)
			w.Write([]byte(`{"error":"Gone"}` + "\n"))
		default:
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusGone)
			w.Write(renderPage(domainFromRequest(r)))
		}
	})

	addr := ":" + port
	log.Printf("gone listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
