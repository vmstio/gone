import assert from "node:assert/strict";
import test from "node:test";
import { Miniflare } from "miniflare";

const mf = new Miniflare({
  modules: true,
  scriptPath: "worker.js",
  modulesRules: [{ type: "Text", include: ["**/*.svg"] }],
  bindings: { LOG_REQUESTS: "false" },
});

async function request(path, init = {}) {
  return mf.dispatchFetch(`https://retired.example${path}`, init);
}

test.after(async () => {
  await mf.dispose();
});

test("WebFinger returns a cacheable JSON 410", async () => {
  const response = await request("/.well-known/webfinger?resource=acct:alice@retired.example", {
    headers: { Accept: "application/jrd+json, application/json" },
  });

  assert.equal(response.status, 410);
  assert.equal(response.headers.get("Content-Type"), "application/json; charset=utf-8");
  assert.equal(response.headers.get("Cache-Control"), "private, max-age=86400");
  assert.equal(response.headers.get("Vary"), "Accept, Content-Type");
  assert.equal(await response.text(), '{"error":"Gone"}\n');
});

test("ActivityPub actor and inbox requests receive an ActivityPub 410", async () => {
  const actor = await request("/users/alice", { headers: { Accept: "application/activity+json" } });
  const inbox = await request("/inbox", { method: "POST", body: "{}" });

  for (const [name, response] of Object.entries({ actor, inbox })) {
    assert.equal(response.status, 410, name);
    assert.equal(response.headers.get("Content-Type"), "application/activity+json; charset=utf-8", name);
    assert.equal(await response.text(), '{"error":"Gone"}\n', name);
  }
});

test("ActivityPub is recognized by Content-Type on non-inbox paths", async () => {
  const response = await request("/users/alice/outbox", {
    method: "POST",
    headers: { "Content-Type": "application/ld+json" },
    body: "{}",
  });

  assert.equal(response.status, 410);
  assert.equal(response.headers.get("Content-Type"), "application/activity+json; charset=utf-8");
  assert.equal(await response.text(), '{"error":"Gone"}\n');
});

test("media and rejected JSON ranges do not receive the HTML page", async () => {
  const media = await request("/media_attachments/1/image.png", { headers: { Accept: "image/png" } });
  const browser = await request("/users/alice", { headers: { Accept: "text/html, application/json;q=0" } });

  assert.equal(media.status, 410);
  assert.equal(media.headers.get("Content-Type"), "image/png");
  assert.equal(await media.text(), "");
  assert.equal(browser.headers.get("Content-Type"), "text/html; charset=utf-8");

  // The common 410 and safety headers apply to machine branches too, not
  // just the HTML page.
  assert.equal(media.headers.get("Cache-Control"), "private, max-age=86400");
  assert.equal(media.headers.get("Vary"), "Accept, Content-Type");
  assert.equal(media.headers.get("X-Content-Type-Options"), "nosniff");
  assert.equal(media.headers.get("Referrer-Policy"), "no-referrer");
});

test("extensionless media requests fall back to the Accept header", async () => {
  const specific = await request("/some/attachment", { headers: { Accept: "image/webp" } });
  const wildcard = await request("/some/attachment", { headers: { Accept: "image/*" } });
  const browser = await request("/some/attachment", {
    headers: { Accept: "text/html,application/xhtml+xml,image/avif,image/webp,*/*;q=0.8" },
  });

  assert.equal(specific.status, 410);
  assert.equal(specific.headers.get("Content-Type"), "image/webp");
  assert.equal(await specific.text(), "");

  // A wildcard still marks the request as media, but is not echoed back as a
  // concrete response type.
  assert.equal(wildcard.status, 410);
  assert.equal(wildcard.headers.get("Content-Type"), null);
  assert.equal(await wildcard.text(), "");

  // A browser navigation lists image types alongside text/html and must not
  // be mistaken for a media subresource.
  assert.equal(browser.headers.get("Content-Type"), "text/html; charset=utf-8");
});

test("JSON paths answer with JSON even without an Accept header", async () => {
  const paths = ["/api/v1/instance", "/.well-known/webfinger", "/nodeinfo/2.0", "/oauth/token", "/statuses/123.json"];

  for (const path of paths) {
    const response = await request(path);
    assert.equal(response.status, 410, path);
    assert.equal(response.headers.get("Content-Type"), "application/json; charset=utf-8", path);
    assert.equal(await response.text(), '{"error":"Gone"}\n', path);
  }
});

test("host-meta returns an XRD 410", async () => {
  const response = await request("/.well-known/host-meta");

  assert.equal(response.status, 410);
  assert.equal(response.headers.get("Content-Type"), "application/xrd+xml; charset=utf-8");
  assert.match(await response.text(), /<XRD xmlns=.*<Error>Gone<\/Error><\/XRD>/);
});

test("RSS feeds receive an empty 410 with the feed content type", async () => {
  const response = await request("/@alice.rss");

  assert.equal(response.status, 410);
  assert.equal(response.headers.get("Content-Type"), "application/rss+xml; charset=utf-8");
  assert.equal(await response.text(), "");
});

test("the retirement page has document protections and uses the routed host", async () => {
  const response = await request("/", { headers: { "X-Forwarded-Host": "attacker.example" } });
  const page = await response.text();

  assert.equal(response.status, 410);
  assert.equal(response.headers.get("X-Robots-Tag"), "noindex, noarchive, nosnippet");
  assert.equal(response.headers.get("X-Content-Type-Options"), "nosniff");
  assert.equal(response.headers.get("Referrer-Policy"), "no-referrer");
  assert.match(response.headers.get("Content-Security-Policy"), /default-src 'none'/);
  assert.match(page, /retired\.example is HTTP 410 \(Gone\)/);
  assert.doesNotMatch(page, /attacker\.example/);
});

test("the displayed domain drops a www prefix and any port", async () => {
  const response = await mf.dispatchFetch("https://www.retired.example:8443/");
  const page = await response.text();

  assert.equal(response.status, 410);
  assert.match(page, /retired\.example is HTTP 410 \(Gone\)/);
  assert.doesNotMatch(page, /www\.retired\.example|8443/);
});

test("robots and health endpoints have endpoint-specific cache directives", async () => {
  const robots = await request("/robots.txt");
  const health = await request("/healthz");

  assert.equal(robots.status, 200);
  assert.equal(robots.headers.get("Content-Type"), "text/plain; charset=utf-8");
  assert.equal(robots.headers.get("Cache-Control"), "public, max-age=86400");
  assert.equal(robots.headers.get("Vary"), null);
  assert.equal(await robots.text(), "User-agent: *\nDisallow: /\n");
  assert.equal(health.status, 200);
  assert.equal(health.headers.get("Cache-Control"), "no-store");
  assert.equal(await health.text(), "ok\n");
});

test("request logging does not break responses when enabled", async () => {
  // The shared instance disables LOG_REQUESTS, so logRequest's real code
  // path (URL parsing, header reads, clientIP) never runs there.
  const logging = new Miniflare({
    modules: true,
    scriptPath: "worker.js",
    modulesRules: [{ type: "Text", include: ["**/*.svg"] }],
    bindings: {},
  });

  try {
    const response = await logging.dispatchFetch("https://retired.example/users/alice", {
      headers: { "X-Forwarded-For": "203.0.113.7", "User-Agent": "probe/1.0" },
    });
    assert.equal(response.status, 410);
  } finally {
    await logging.dispose();
  }
});
