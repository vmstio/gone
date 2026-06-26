// Command gone is a tiny HTTP server that responds to every request with
// HTTP 410 Gone and a self-contained page mirroring the Mastodon error page.
package main

import (
	_ "embed"
	"encoding/base64"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
)

//go:embed oops.png
var oopsPNG []byte

// page is the fully self-contained HTML served with every 410 response. The
// illustration is inlined as a data URI so there are no external dependencies
// and no follow-up requests that would need a non-410 status.
var page []byte

const pageTemplate = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>vmst.io</title>
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
<img alt="Mastodon" src="data:image/png;base64,__IMG_DATA__">
</div>
<div class="dialog__message">
<h1>vmst.io is gone</h1>
</div>
</div>
</body>
</html>
`

func init() {
	encoded := base64.StdEncoding.EncodeToString(oopsPNG)
	page = []byte(strings.Replace(pageTemplate, "__IMG_DATA__", encoded, 1))
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

	// Every other request gets HTTP 410 Gone with the page.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusGone)
		w.Write(page)
	})

	addr := ":" + port
	log.Printf("gone listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
