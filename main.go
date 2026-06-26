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

// domainFromRequest returns the requested host (without any port), suitable
// for display. It prefers the X-Forwarded-Host header set by proxies such as
// DigitalOcean App Platform, falling back to the request Host.
func domainFromRequest(r *http.Request) string {
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}
	// X-Forwarded-Host may contain a comma-separated list; use the first.
	if i := strings.IndexByte(host, ','); i >= 0 {
		host = host[:i]
	}
	host = strings.TrimSpace(host)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if host == "" {
		return defaultDomain
	}
	return host
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

	// Every other request gets HTTP 410 Gone with the page, with the heading
	// populated from the requested domain.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusGone)
		w.Write(renderPage(domainFromRequest(r)))
	})

	addr := ":" + port
	log.Printf("gone listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
