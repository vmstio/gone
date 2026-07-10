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

  for (const response of [actor, inbox]) {
    assert.equal(response.status, 410);
    assert.equal(response.headers.get("Content-Type"), "application/activity+json; charset=utf-8");
    assert.equal(await response.text(), '{"error":"Gone"}\n');
  }
});

test("media and rejected JSON ranges do not receive the HTML page", async () => {
  const media = await request("/media_attachments/1/image.png", { headers: { Accept: "image/png" } });
  const browser = await request("/users/alice", { headers: { Accept: "text/html, application/json;q=0" } });

  assert.equal(media.status, 410);
  assert.equal(media.headers.get("Content-Type"), "image/png");
  assert.equal(await media.text(), "");
  assert.equal(browser.headers.get("Content-Type"), "text/html; charset=utf-8");
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

test("robots and health endpoints have endpoint-specific cache directives", async () => {
  const robots = await request("/robots.txt");
  const health = await request("/healthz");

  assert.equal(robots.status, 200);
  assert.equal(robots.headers.get("Cache-Control"), "public, max-age=86400");
  assert.equal(robots.headers.get("Vary"), null);
  assert.equal(health.status, 200);
  assert.equal(health.headers.get("Cache-Control"), "no-store");
});
