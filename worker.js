// Worker gone is a tiny Cloudflare Worker that responds to every request with
// HTTP 410 Gone and a self-contained page mirroring the Mastodon error page.
// Ported from main.go — see README.md for the content-negotiation rules.

import LOGO_SVG from "./logo.svg";

// defaultDomain is shown when the request carries no usable Host header.
const defaultDomain = "This site";

const PAGE_TEMPLATE = `<!DOCTYPE html>
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
  max-width: 140px;
  height: auto;
  display: block;
  margin: 0 auto 24px;
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
<canvas id="illustration-overlay" style="position:fixed; top:0; left:0; width:100vw; height:100vh; pointer-events:none; z-index:9999;"></canvas>
<script>
(function () {
  var canvas = document.getElementById('illustration');
  var ctx = canvas.getContext('2d');
  var overlay = document.getElementById('illustration-overlay');
  var octx = overlay.getContext('2d');
  var img = new Image();
  img.src = 'data:image/svg+xml;base64,__LOGO_DATA__';

  // The logo is vector art rasterized at RENDER_SCALE times its intrinsic
  // size, so it stays crisp at the canvas's actual pixel resolution rather
  // than the small source dimensions in the SVG's width/height attributes.
  var RENDER_SCALE = 6;
  var tileSize = 8;
  var tiles = [];
  var progress = 0; // 0 = intact, 1 = fully dissolved; only ever increases
  var target = 0;
  var speed = 1 / 6000; // progress units per ms — a slow, wind-borne drift
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
          // A shared rightward breeze (with per-tile jitter) rather than an
          // outward blast, plus a gentle rise and a perpendicular sway so
          // tiles flutter like leaves caught in the wind.
          windX: window.innerWidth * (0.45 + Math.random() * 0.5),
          windY: -window.innerHeight * (0.1 + Math.random() * 0.25),
          sway: 15 + Math.random() * 25,
          swayFreq: 1.2 + Math.random() * 1.8,
          swayPhase: Math.random() * Math.PI * 2,
          rot: (Math.random() - 0.5) * 2.2
        });
      }
    }
  }

  function resizeOverlay() {
    overlay.width = window.innerWidth;
    overlay.height = window.innerHeight;
  }

  function drawFrame() {
    ctx.clearRect(0, 0, canvas.width, canvas.height);
    octx.clearRect(0, 0, overlay.width, overlay.height);

    // A faint grayscale ghost of the original logo, left behind under
    // whatever hasn't dissolved away yet.
    if (progress > 0) {
      ctx.save();
      ctx.globalAlpha = 0.16;
      ctx.filter = 'grayscale(100%)';
      ctx.drawImage(img, 0, 0, canvas.width / RENDER_SCALE, canvas.height / RENDER_SCALE, 0, 0, canvas.width, canvas.height);
      ctx.restore();
    }

    var rect = canvas.getBoundingClientRect();
    var displayScale = rect.width / canvas.width;
    var originX = rect.left, originY = rect.top;
    for (var i = 0; i < tiles.length; i++) {
      var t = tiles[i];
      var span = 1 - t.delay;
      var p = span > 0 ? (progress - t.delay) / span : progress > t.delay ? 1 : 0;
      p = Math.min(Math.max(p, 0), 1);
      if (p >= 1) continue;
      if (p <= 0) {
        ctx.drawImage(img, t.x / RENDER_SCALE, t.y / RENDER_SCALE, t.w / RENDER_SCALE, t.h / RENDER_SCALE, t.x, t.y, t.w, t.h);
        continue;
      }
      var sway = Math.sin(p * t.swayFreq * Math.PI + t.swayPhase) * t.sway * p;
      octx.save();
      octx.globalAlpha = 1 - p;
      octx.translate(
        originX + (t.x + t.w / 2) * displayScale + t.windX * p + sway,
        originY + (t.y + t.h / 2) * displayScale + t.windY * p
      );
      octx.rotate(t.rot * p + Math.sin(p * t.swayFreq * Math.PI + t.swayPhase) * 0.25);
      var dw = t.w * displayScale, dh = t.h * displayScale;
      octx.drawImage(img, t.x / RENDER_SCALE, t.y / RENDER_SCALE, t.w / RENDER_SCALE, t.h / RENDER_SCALE, -dw / 2, -dh / 2, dw, dh);
      octx.restore();
    }
  }

  function loop(ts) {
    if (lastTs === null) lastTs = ts;
    var dt = ts - lastTs;
    lastTs = ts;
    if (progress < target) {
      progress = Math.min(target, progress + dt * speed);
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
    canvas.width = img.naturalWidth * RENDER_SCALE;
    canvas.height = img.naturalHeight * RENDER_SCALE;
    resizeOverlay();
    buildTiles();
    drawFrame();
  };

  window.addEventListener('resize', function () {
    resizeOverlay();
    buildTiles();
    drawFrame();
  });

  canvas.addEventListener('mouseenter', function () { setTarget(1); });
  canvas.addEventListener('click', function () { setTarget(1); });
})();
</script>
</body>
</html>
`;

// toBase64 encodes a UTF-8 string as base64 without relying on Node's Buffer,
// which isn't available in the Workers runtime by default.
function toBase64(str) {
  const bytes = new TextEncoder().encode(str);
  let binary = "";
  for (const b of bytes) binary += String.fromCharCode(b);
  return btoa(binary);
}

// pageTpl is the self-contained HTML with the logo inlined as a data URI,
// computed once per isolate (mirroring Go's package-level init()). The
// __DOMAIN__ placeholder is filled per request with the requested host, so a
// single deployment can serve any number of domains.
const pageTpl = PAGE_TEMPLATE.replace("__LOGO_DATA__", toBase64(LOGO_SVG));

// escapeHTML escapes the five characters html.EscapeString covers, since the
// domain is interpolated into the page.
function escapeHTML(s) {
  return s
    .replace(/&/g, "&amp;")
    .replace(/'/g, "&#39;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&#34;");
}

// splitHostPort strips a trailing ":<port>" from a host, handling bracketed
// IPv6 literals like "[::1]:8080". Mirrors net.SplitHostPort closely enough
// for display purposes.
function splitHostPort(host) {
  if (host.startsWith("[")) {
    const end = host.indexOf("]");
    if (end !== -1) return host.slice(1, end);
    return host;
  }
  const idx = host.lastIndexOf(":");
  if (idx !== -1 && host.indexOf(":") === idx) return host.slice(0, idx);
  return host;
}

// rawHost returns the requested host, preferring the X-Forwarded-Host header
// (as set by a proxy in front of the Worker) and falling back to the request's
// own Host header. It may still include a port.
function rawHost(request) {
  const url = new URL(request.url);
  let host = request.headers.get("X-Forwarded-Host") || request.headers.get("Host") || url.host;
  const i = host.indexOf(",");
  if (i >= 0) host = host.slice(0, i);
  return host.trim();
}

// domainFromRequest returns the requested host without any port or leading
// "www.", for display. Visitors landing on the www subdomain should still
// see the bare domain, since both point at the same decommissioned service.
function domainFromRequest(request) {
  let host = splitHostPort(rawHost(request));
  if (host.toLowerCase().startsWith("www.")) host = host.slice(4);
  return host === "" ? defaultDomain : host;
}

// wantsAny reports whether a header value mentions any of the given media
// types. Matching is a simple case-insensitive substring test, which is
// enough to distinguish ActivityPub/JSON clients from browsers.
function wantsAny(headerValue, ...mediaTypes) {
  const v = (headerValue || "").toLowerCase();
  return mediaTypes.some((mt) => v.includes(mt));
}

// isHostMetaPath reports whether the request targets the host-meta discovery
// document (RFC 6415) that bootstraps WebFinger. It is served as XRD XML.
function isHostMetaPath(p) {
  return p === "/.well-known/host-meta";
}

// isNodeInfoPath reports whether the request targets NodeInfo discovery or a
// NodeInfo document. Fediverse crawlers and stats sites probe these heavily,
// often without a useful Accept header, so the path is the signal.
function isNodeInfoPath(p) {
  return p === "/.well-known/nodeinfo" || p === "/.well-known/x-nodeinfo2" || p.startsWith("/nodeinfo/");
}

// isJSONDiscoveryPath reports whether the request targets a fediverse JSON
// discovery endpoint (WebFinger, NodeInfo, or OAuth/OIDC server metadata)
// that should answer with JSON regardless of the Accept header.
function isJSONDiscoveryPath(p) {
  return (
    p === "/.well-known/webfinger" ||
    p === "/.well-known/oauth-authorization-server" ||
    p === "/.well-known/openid-configuration" ||
    isNodeInfoPath(p)
  );
}

// isOAuthJSONPath reports whether the request targets an OAuth/OIDC endpoint
// whose clients are machine callers expecting JSON — token exchange,
// revocation, and OIDC userinfo — as opposed to /oauth/authorize, which is
// the interactive browser login page and should still get the HTML page.
// OAuth client libraries often POST to these without an explicit Accept
// header, so the path is the reliable signal.
function isOAuthJSONPath(p) {
  return p === "/oauth/token" || p === "/oauth/revoke" || p === "/oauth/userinfo";
}

// isAPIPath reports whether the request targets the Mastodon REST API. Its
// clients are human-facing apps (the official web UI and third-party
// clients) that read the JSON error's "error" field to show the user an
// alert.
function isAPIPath(p) {
  return p.startsWith("/api/");
}

// isJSONPath reports whether the request should get a JSON response
// regardless of the Accept header: fediverse JSON discovery, OAuth/OIDC
// machine endpoints, and any .json resource.
function isJSONPath(p) {
  return isJSONDiscoveryPath(p) || isOAuthJSONPath(p) || p.endsWith(".json");
}

// isFeedPath reports whether the request is for Mastodon's RSS feed — the
// only syndication format it serves (there is no Atom equivalent).
function isFeedPath(p) {
  return p.endsWith(".rss");
}

// isInboxPath reports whether the request targets an ActivityPub inbox (the
// shared /inbox or a per-actor /users/x/inbox). Federation delivery POSTs land
// here; the delivering server only needs the 410 status to stop delivering, so
// these get an empty body.
function isInboxPath(p) {
  return p.endsWith("/inbox");
}

// isActivityPub reports whether the request is ActivityPub, by either the
// Accept header (actor fetches) or the Content-Type header (inbox deliveries,
// which are POSTed with application/activity+json and may not set Accept).
function isActivityPub(request) {
  return (
    wantsAny(request.headers.get("Accept"), "application/activity+json", "application/ld+json") ||
    wantsAny(request.headers.get("Content-Type"), "application/activity+json", "application/ld+json")
  );
}

// mediaExts maps a media file extension to the Content-Type a bucket of
// images/attachments would have served it as.
const mediaExts = new Map([
  [".jpg", "image/jpeg"],
  [".jpeg", "image/jpeg"],
  [".png", "image/png"],
  [".gif", "image/gif"],
  [".webp", "image/webp"],
  [".avif", "image/avif"],
  [".bmp", "image/bmp"],
  [".svg", "image/svg+xml"],
  [".ico", "image/x-icon"],
  [".heic", "image/heic"],
  [".tif", "image/tiff"],
  [".tiff", "image/tiff"],
  [".mp4", "video/mp4"],
  [".webm", "video/webm"],
  [".mov", "video/quicktime"],
  [".m4v", "video/x-m4v"],
  [".ogv", "video/ogg"],
  [".mp3", "audio/mpeg"],
  [".ogg", "audio/ogg"],
  [".oga", "audio/ogg"],
  [".m4a", "audio/mp4"],
  [".wav", "audio/wav"],
  [".flac", "audio/flac"],
]);

function extOf(p) {
  const slash = p.lastIndexOf("/");
  const base = slash === -1 ? p : p.slice(slash + 1);
  const dot = base.lastIndexOf(".");
  return dot === -1 ? "" : base.slice(dot).toLowerCase();
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
function isMediaRequest(request, p) {
  if (p.startsWith("/media_proxy/") || p.startsWith("/media_attachments/") || p.startsWith("/system/")) {
    return true;
  }
  if (mediaExts.has(extOf(p))) return true;
  const accept = (request.headers.get("Accept") || "").toLowerCase();
  return (
    !accept.includes("text/html") &&
    (accept.includes("image/") || accept.includes("video/") || accept.includes("audio/"))
  );
}

// mediaContentType returns the Content-Type to echo back for a media
// request: the extension's known type if the path has one, otherwise the
// specific image/video/audio token from Accept (for extensionless paths like
// bare /media_proxy/… hits or hotlinked <img> tags with a browser Accept).
function mediaContentType(request, p) {
  const byExt = mediaExts.get(extOf(p));
  if (byExt) return byExt;
  const match = (request.headers.get("Accept") || "")
    .toLowerCase()
    .match(/(?:image|video|audio)\/[a-z0-9.+-]+/);
  return match ? match[0] : "";
}

// renderPage fills the per-request domain into the cached template. The
// replacement is a function so that "$" sequences in the (client-controlled)
// domain are inserted literally instead of being interpreted as replaceAll
// substitution patterns like $& or $'.
function renderPage(domain) {
  const escaped = escapeHTML(domain);
  return pageTpl.replaceAll("__DOMAIN__", () => escaped);
}

// jsonGoneBody is the small JSON error shared by the Mastodon REST API and
// JSON discovery paths. Mastodon's own web client (and third-party apps)
// parse the "error" field to show an alert.
const jsonGoneBody = '{"error":"Gone"}\n';

// xrdGoneBody is the XRD/XML equivalent of jsonGoneBody, for host-meta.
const xrdGoneBody =
  '<?xml version="1.0" encoding="UTF-8"?>\n' +
  '<XRD xmlns="http://docs.oasis-open.org/ns/xri/xrd-1.0"><Error>Gone</Error></XRD>\n';

// writeGone builds a 410 response with the given Content-Type and body. An
// empty contentType or body is skipped, leaving no body written.
function writeGone(contentType, body) {
  const headers = new Headers();
  if (contentType) headers.set("Content-Type", contentType);
  return new Response(body || null, { status: 410, headers });
}

// clientIP returns the originating client address. CF-Connecting-IP is set by
// Cloudflare's edge and can't be spoofed by the client, so it's preferred over
// X-Forwarded-For (which mirrors the App Platform proxy header the original
// Go server trusted).
function clientIP(request) {
  const cf = request.headers.get("CF-Connecting-IP");
  if (cf) return cf;
  const xff = request.headers.get("X-Forwarded-For");
  if (xff) {
    const i = xff.indexOf(",");
    return (i >= 0 ? xff.slice(0, i) : xff).trim();
  }
  return "-";
}

// logRequest logs one line per request so you can see what is being probed,
// visible via `wrangler tail` or Logpush. The response Content-Type indicates
// which branch matched. Health checks are skipped to avoid drowning the log.
// Set the LOG_REQUESTS var to "false" to disable.
function logRequest(request, response, path, env) {
  if (env.LOG_REQUESTS === "false") return;
  if (path === "/healthz") return;
  const ct = response.headers.get("Content-Type") || "-";
  console.log(
    `${response.status} ${request.method} ${path}${new URL(request.url).search} ` +
      `ct="${ct}" host="${rawHost(request)}" ip=${clientIP(request)} ua="${request.headers.get("User-Agent") || ""}"`
  );
}

// handleGone answers every non-health request with HTTP 410 Gone (except
// /robots.txt). The response is chosen from the request path and headers so
// federating servers and API clients get compact machine-readable bodies
// while human browsers get the HTML page.
function handleGone(request) {
  const path = new URL(request.url).pathname;

  // The resource is permanently gone, so let the client hold on to the 410
  // and stop re-requesting. "private" (rather than "public") is deliberate:
  // the body varies by Accept/path, and shared caches like Cloudflare don't
  // key on Vary by default, so a public directive lets one client's response
  // (e.g. a bot's empty body) get served to every other visitor. private
  // confines caching to the requesting client, which does respect Vary.
  // Applies to every branch below.
  const commonHeaders = { "Cache-Control": "private, max-age=86400", Vary: "Accept" };

  let response;
  if (path === "/robots.txt") {
    // Actively steer crawlers away. Unlike everything else this is a live
    // 200 directive, not a 410.
    response = new Response("User-agent: *\nDisallow: /\n", {
      status: 200,
      headers: { "Content-Type": "text/plain; charset=utf-8" },
    });
  } else if (isMediaRequest(request, path)) {
    // Former bucket media (an <img>/<video> tag or a server refetch, which
    // ignores any body) gets an empty 410 rather than the ~9 KB page, echoing
    // back the requested media type as Content-Type.
    response = writeGone(mediaContentType(request, path), "");
  } else if (isHostMetaPath(path)) {
    // host-meta discovery, fetched programmatically. Real Mastodon's own
    // host-meta 410 is a bare `head 410`, but a small XRD error body costs
    // little and mirrors the JSON error given to the other discovery paths.
    response = writeGone("application/xrd+xml; charset=utf-8", xrdGoneBody);
  } else if (isInboxPath(path) || isActivityPub(request)) {
    // Inbox delivery POSTs (by path, since they may lack Accept) and
    // actor/status fetches (by Accept or Content-Type): same JSON error
    // body as the branch below, but kept as application/activity+json since
    // that's the representation these clients actually asked for.
    response = writeGone("application/activity+json; charset=utf-8", jsonGoneBody);
  } else if (
    isAPIPath(path) ||
    isJSONPath(path) ||
    wantsAny(request.headers.get("Accept"), "application/json", "application/jrd+json")
  ) {
    // Mastodon REST API, WebFinger / NodeInfo / OAuth discovery, .json
    // resources (all by path, any Accept), and generic JSON clients all get
    // the same small JSON error body.
    response = writeGone("application/json; charset=utf-8", jsonGoneBody);
  } else if (isFeedPath(path)) {
    // Dead RSS feed: return the matching content type so readers recognise
    // the 410 and stop polling. The body is empty.
    response = writeGone("application/rss+xml; charset=utf-8", "");
  } else {
    response = new Response(renderPage(domainFromRequest(request)), {
      status: 410,
      headers: { "Content-Type": "text/html; charset=utf-8" },
    });
  }

  for (const [k, v] of Object.entries(commonHeaders)) response.headers.set(k, v);
  return response;
}

// handleHealthz responds 200 for platform health checks. Kept separate so
// that real traffic (all 410) never makes the health check fail.
function handleHealthz() {
  return new Response("ok\n", { status: 200 });
}

export default {
  async fetch(request, env, ctx) {
    const path = new URL(request.url).pathname;
    const response = path === "/healthz" ? handleHealthz() : handleGone(request);
    logRequest(request, response, path, env);
    return response;
  },
};
