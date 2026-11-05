// Real provider editor and import confirmation, with saved API data and
// clipboard isolated from the machine. All keys below are test fixtures.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { test } = require("node:test");
const { chromium, webkit } = require("playwright");
const assets = process.env.ASSET_DIR || path.resolve(__dirname, "../assets");
const linked = { id: "relay", name: "Relay", chat: "https://relay.example/v1", modelsURL: "https://catalog.example/models?region=test", iconUrl: "https://cdn.example/logo.svg", key: "sk-first", keyName: "Main", keys: [{ name: "Spare", key: "sk-second", off: true }], headers: { "X-Org": "demo" }, models: ["m1"], importKeys: true };
const link = "magpie://import?format=1&data=" + Buffer.from(JSON.stringify(linked)).toString("base64url");
const words = {
  en: { share: "Share", copy: "Copy link", cancel: "Cancel", add: "Add", keys: "2 API keys", paused: "Paused", modelsURL: "Models URL" },
  zh: { share: "分享", copy: "复制链接", cancel: "取消", add: "添加", keys: "2 个 API 密钥", paused: "已暂停", modelsURL: "模型列表地址" },
  "zh-TW": { share: "分享", copy: "複製連結", cancel: "取消", add: "新增", keys: "2 個 API 金鑰", paused: "已暫停", modelsURL: "模型列表位址" },
  ja: { share: "共有", copy: "リンクをコピー", cancel: "キャンセル", add: "追加", keys: "2 個の API キー", paused: "一時停止中", modelsURL: "モデル URL" },
  de: { share: "Teilen", copy: "Link kopieren", cancel: "Abbrechen", add: "Hinzufügen", keys: "2 API-Schlüssel", paused: "Pausiert", modelsURL: "Modelllisten-URL" },
};

async function fixture(t, engine, lang, importing = false) {
  const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
  t.after(() => browser.close());
  const page = await browser.newPage({ viewport: { width: 420, height: 900 }, reducedMotion: "reduce" });
  page.setDefaultTimeout(6000);
  page.setDefaultNavigationTimeout(60000);
  const errors = [], posts = [];
  const importedIcon = importing ? page.waitForResponse(r => new URL(r.url()).pathname === "/api/import/icon") : null;
  const reply = { fail: false, hold: false, release: null, reached: null, held: null, markReached: null };
  page.on("pageerror", e => { if (!/^ResizeObserver loop/.test(e.message)) errors.push(e.message); });
  const p = { ...linked, canShare: true, keys: undefined, key: { set: true, masked: "sk-…irst" }, keyList: [], icon: "generic", models: [{ id: "m1", name: "m1", on: true }], agents: [], ready: true, fallback: [] };
  const other = { ...p, id: "relay-2", name: "Second Relay", modelsURL: "https://catalog-two.example/models", key: { set: true, masked: "sk-…cond" } };
  const state = { providers: [p, other], presets: [], excluded: [], gateway: { running: true, window: true } };
  await page.route("**/*", async route => {
    const req = route.request(), url = new URL(req.url());
    if (url.pathname === "/boot.js") return route.fulfill({ contentType: "text/javascript", body: `window.bootPrefs={lang:${JSON.stringify(lang)},theme:"light",web:true};` });
    if (url.pathname === "/api/state") return route.fulfill({ json: { agents: [], profiles: [], settings: { lang, theme: "light" } } });
    if (url.pathname === "/api/providers") return route.fulfill({ json: state });
    if (url.pathname === "/api/groups") return route.fulfill({ json: { groups: [] } });
    if (url.pathname === "/api/import/shared") return route.fulfill({ json: { provider: linked, shared: true } });
    if (url.pathname === "/api/import/icon") return route.fulfill({ json: { icon: "file:0123456789abcdef.svg" } });
    if (req.method() === "POST" && url.pathname.startsWith("/api/")) posts.push({ action: url.pathname, body: req.postDataJSON() });
    if (url.pathname === "/api/provider/share") {
      if (reply.hold) {
        reply.markReached();
        await reply.held;
      }
      try {
        return await route.fulfill(reply.fail ? { status: 400, json: { error: "Share unavailable" } } : { json: { link, keys: 2, credentials: true } });
      } catch (error) {
        if (!req.failure()) throw error;
      }
    }
    if (url.pathname === "/api/provider/save") return route.fulfill({ json: state });
    if (url.pathname.startsWith("/api/")) return route.fulfill({ json: {} });
    const file = path.join(assets, url.pathname === "/" ? "index.html" : url.pathname);
    const contentType = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png" }[path.extname(file)];
    try { return await route.fulfill({ body: await fs.readFile(file), contentType }); } catch { return route.fulfill({ status: 404, body: "" }); }
  });
  await page.goto("http://magpie.test/?view=providers" + (importing ? "&import=shared" : ""));
  if (importedIcon) await importedIcon;
  if (!importing) await page.locator('.row.provider[data-id="relay"]').click();
  await page.locator(".editor .bar").waitFor();
  return { page, posts, errors, reply };
}

async function nextPaint(page) {
  await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => resolve())));
}

function shareRequestFailed(page) {
  return page.waitForEvent("requestfailed", { predicate: request => new URL(request.url()).pathname === "/api/provider/share" });
}

function holdShare(reply) {
  let markReached, release;
  reply.hold = true;
  reply.reached = new Promise(resolve => { markReached = resolve; });
  reply.held = new Promise(resolve => { release = resolve; });
  reply.markReached = markReached;
  reply.release = release;
}

for (const engine of process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"]) {
  test(`${engine} en: a share reply after closing the editor opens nothing`, async t => {
    const { page, reply } = await fixture(t, engine, "en");
    holdShare(reply);
    const requested = page.waitForRequest(request => new URL(request.url()).pathname === "/api/provider/share");
    await page.locator(".editor .bar").getByRole("button", { name: "Share", exact: true }).click();
    await requested;
    await reply.reached;
    assert.equal(typeof reply.release, "function", "share request is held");
    const failed = shareRequestFailed(page);
    await page.locator(".editor .bar").getByRole("button", { name: "Cancel", exact: true }).click();
    await page.waitForFunction(() => document.querySelector("#modal").hidden);
    await failed;
    reply.release();
    await nextPaint(page);
    assert.equal(await page.locator(".provider-share").count(), 0);
  });
}

for (const engine of process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"]) {
  test(`${engine} en: a held share survives a provider refresh`, async t => {
    const { page, errors, reply } = await fixture(t, engine, "en");
    holdShare(reply);
    await page.locator(".editor .bar").getByRole("button", { name: "Share", exact: true }).click();
    await reply.reached;
    await page.evaluate(() => { window.reviewDraft = draft; renderProviders(true); });
    assert.equal(await page.evaluate(() => draft === window.reviewDraft), true, "the same draft remains open");
    const share = page.locator(".editor .bar").getByRole("button", { name: "Share", exact: true });
    await share.waitFor();
    assert.equal(await share.isDisabled(), true, "the redrawn Share button stays disabled");
    assert.equal(await share.getAttribute("aria-busy"), "true");
    const received = page.waitForResponse(response => new URL(response.url()).pathname === "/api/provider/share");
    reply.release();
    await received;
    const text = page.locator(".provider-share textarea");
    await text.waitFor();
    assert.equal(await text.inputValue(), link);
    assert.deepEqual(errors, []);
  });

  test(`${engine} en: a held share failure lands in the refreshed editor`, async t => {
    const { page, errors, reply } = await fixture(t, engine, "en");
    holdShare(reply);
    reply.fail = true;
    await page.locator(".editor .bar").getByRole("button", { name: "Share", exact: true }).click();
    await reply.reached;
    await page.evaluate(() => renderProviders(true));
    const share = page.locator(".editor .bar").getByRole("button", { name: "Share", exact: true });
    assert.equal(await share.isDisabled(), true);
    const received = page.waitForResponse(response => new URL(response.url()).pathname === "/api/provider/share");
    reply.release();
    await received;
    await page.waitForFunction(() => document.querySelector(".editor-error")?.textContent.includes("Share unavailable"));
    assert.equal(await page.locator(".provider-share").count(), 0);
    assert.equal(await share.isDisabled(), false, "the refreshed Share button is usable again");
    assert.equal(await share.getAttribute("aria-busy"), null);
    assert.deepEqual(errors, []);
  });

  test(`${engine} en: cancelling a held share discards its reply`, async t => {
    const { page, reply } = await fixture(t, engine, "en");
    holdShare(reply);
    await page.locator(".editor .bar").getByRole("button", { name: "Share", exact: true }).click();
    await reply.reached;
    const failed = shareRequestFailed(page);
    await page.locator(".editor .bar").getByRole("button", { name: "Cancel", exact: true }).click();
    await page.waitForFunction(() => document.querySelector("#modal").hidden);
    await failed;
    reply.release();
    await nextPaint(page);
    assert.equal(await page.locator(".provider-share").count(), 0);
  });

  test(`${engine} en: changing providers discards the old share reply`, async t => {
    const { page, reply } = await fixture(t, engine, "en");
    holdShare(reply);
    await page.locator(".editor .bar").getByRole("button", { name: "Share", exact: true }).click();
    await reply.reached;
    const failed = shareRequestFailed(page);
    await page.evaluate(() => { editing = "relay-2"; draft = null; renderProviders(); });
    await page.locator(".editor .ehead b", { hasText: "Second Relay" }).waitFor();
    await failed;
    reply.release();
    await nextPaint(page);
    assert.equal(await page.locator(".provider-share").count(), 0);
    assert.equal(await page.locator(".editor .ehead b").textContent(), "Second Relay");
    assert.equal(await page.locator(".editor .bar").getByRole("button", { name: "Share", exact: true }).getAttribute("aria-busy"), null);
  });

  test(`${engine} en: a held share does not overwrite an unrelated modal`, async t => {
    const { page, reply } = await fixture(t, engine, "en");
    holdShare(reply);
    await page.locator(".editor .bar").getByRole("button", { name: "Share", exact: true }).click();
    await reply.reached;
    const failed = shareRequestFailed(page);
    await page.evaluate(() => {
      const editor = el("div", "editor unrelated");
      editor.append(el("b", "", "Another question"));
      confirmAsk = editor;
      openModal(editor);
    });
    await failed;
    reply.release();
    await nextPaint(page);
    assert.equal(await page.locator(".editor.unrelated").count(), 1);
    assert.equal(await page.locator(".provider-share").count(), 0);
  });
}

for (const engine of process.env.BROWSER ? [process.env.BROWSER] : ["chromium", "webkit"]) {
  for (const [lang, w] of Object.entries(words)) {
    test(`${engine} ${lang}: shares saved provider with every key`, async t => {
      const { page, posts, errors, reply } = await fixture(t, engine, lang);
      const share = page.locator(".editor .bar").getByRole("button", { name: w.share, exact: true });
      await share.waitFor();
      const top = await page.locator("#view-providers").evaluate(e => e.scrollTop);
      reply.fail = true;
      await share.click();
      await page.waitForFunction(() => document.body.textContent.includes("Share unavailable"));
      assert.equal(await page.locator(".provider-share textarea").count(), 0);
      reply.fail = false;
      await share.click();
      const text = page.locator(".provider-share textarea");
      await text.waitFor();
      assert.equal(await text.inputValue(), link);
      assert(await text.getAttribute("readonly") !== null);
      assert((await page.locator(".provider-share").innerText()).includes(w.keys));
      assert((await page.locator(".provider-share .warnbox").innerText()).length > 0, "credentials warning");
      assert.equal(await page.locator("#view-providers").evaluate(e => e.scrollTop), top, "sharing scrolls no page");
      await page.evaluate(() => renderProviders(true));
      assert.equal(await text.inputValue(), link, "a provider refresh keeps the share sheet");
      const copied = page.waitForResponse(response => new URL(response.url()).pathname === "/api/copy");
      await page.locator(".provider-share").getByRole("button", { name: w.copy, exact: true }).click();
      await copied;
      assert.deepEqual(posts.filter(p => p.action === "/api/provider/share").map(p => p.body), [{ id: "relay" }, { id: "relay" }]);
      assert.equal(posts.find(p => p.action === "/api/copy")?.body.text, link);
      assert(!posts.some(p => p.action === "/api/provider/save"), "share does not save the draft");
      if (process.env.ARTIFACT_DIR) {
        await fs.mkdir(process.env.ARTIFACT_DIR, { recursive: true });
        await page.screenshot({ path: path.join(process.env.ARTIFACT_DIR, `${engine}-${lang}-provider-share.png`) });
      }
      const overflow = await page.locator(".provider-share").evaluate(e => e.scrollWidth > e.clientWidth + 1);
      assert(!overflow, "link fits the narrow editor");
      await page.locator(".editor .bar").getByRole("button", { name: w.cancel, exact: true }).click();
      assert.equal(await page.locator(".provider-share").count(), 0);
      assert.equal(await page.locator(".editor .bar").getByRole("button", { name: w.share, exact: true }).count(), 1, "Cancel returns to provider details");
      assert.deepEqual(errors, []);
    });

    test(`${engine} ${lang}: confirms and imports additional keys`, async t => {
      const { page, posts, errors } = await fixture(t, engine, lang, true);
      const more = page.locator(".import-keys");
      await more.waitFor();
      await page.locator(".editor.import > .hint").waitFor({ state: "detached" });
      assert((await more.innerText()).includes("Spare"));
      assert(!(await more.innerText()).includes("sk-second"), "extra keys stay masked");
      assert((await page.locator(".editor.import .warnbox").innerText()).includes("catalog.example"), "model-list host is named in the warning");
      const endpoints = await page.locator(".editor.import .eps").innerText();
      assert(endpoints.includes(w.modelsURL), "localized Models URL label");
      assert(endpoints.includes(linked.modelsURL), "model-list endpoint is shown before confirmation");
      assert.equal(posts.length, 0, "preview saves nothing");
      await page.locator(".editor .bar").getByRole("button", { name: w.add, exact: true }).click();
      await page.waitForFunction(() => document.querySelector("#modal").hidden);
      const saved = posts.find(p => p.action === "/api/provider/save").body;
      assert.deepEqual(saved.keys, linked.keys);
      assert.equal(saved.importKeys, true);
      assert.equal(saved.keyName, "Main");
      assert.deepEqual(saved.headers, linked.headers);
      assert.equal(saved.icon, "file:0123456789abcdef.svg");
      assert.equal(saved.iconFrom, linked.iconUrl);
      assert.deepEqual(errors, []);
    });
  }
}
