// End-to-end UI tests: index.html in headless Chromium (same engine as WebView2) against the
// real Go back end served by cmd/uipreview. The page is a four-step flow (01 GAME, 02 CONSOLE CONTENT,
// 03 INSTALL, 04 PLAY; step 02 holds the copies and, inside each console box, its static content cards).
//
//   node tests/ui/ui_test.mjs <uipreview binary> <fixtures dir> <extra fixtures dir (mkfix2.sh)> <screenshots dir>
//
// Fixtures come from cmd/mkfixtures (+ tests/ui/mkfix2.sh). Native file dialogs are
// replaced by the preview server's /dev/pick queue (needs -dev and the random token it prints).
// The install job is the preview server's job (it reads and checks, writes nothing); the "slow" server
// pauses after every MiB it reads so that the progress screen can be watched and cancelled.
import path from "node:path";
import fs from "node:fs";
import os from "node:os";
import assert from "node:assert/strict";
import {
  chromium, startServer, newPage, pick, state, waitSrc, browse, text, contEnabled,
  makeRunner, contrastProbe, rawRequest, sleep, gotoStep, install, waitInstall, scrollMetrics,
} from "./lib.mjs";

// (lib.mjs still counts five steps; this page has four)
const visibleStep = page => page.evaluate(() => [1, 2, 3, 4].find(n => !document.getElementById("step" + n).classList.contains("hidden")));
const [bin, fix, fix2, shots] = process.argv.slice(2);
if (!bin || !fix || !fix2 || !shots) { console.error("usage: ui_test.mjs <uipreview> <fix> <fix2> <shots>"); process.exit(2); }
fs.mkdirSync(shots, { recursive: true });
const F = n => path.join(fix, n), G = n => path.join(fix2, n);
const steam = F("Steam");
const GAME = path.join(steam, "steamapps/common/FEAR Ultimate Shooter Edition");
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "uitest-"));
const R = makeRunner(); const step = R.step;
const browser = await chromium.launch();
const servers = [];
const SLOW = ["-slow-install", "4ms"];
// Click INSTALL once the review is ready for it, and wait for the progress view.
const startJob = async page => {
  await page.waitForFunction(() => document.getElementById("install").getAttribute("aria-disabled") === "false" && !document.getElementById("rev4").classList.contains("hidden"));
  await page.click("#install");
  await page.waitForFunction(() => !document.getElementById("prog4").classList.contains("hidden"));
};
const withServer = async (opts, fn) => { const s = await startServer({ bin, steam, ...opts }); servers.push(s); try { return await fn(s); } finally { s.kill(); } };
const withPage = async (srv, viewport, fn, opts) => { const h = await newPage(browser, srv, viewport, opts); try { return await fn(h.page, h); } finally { await h.ctx.close(); } };
const visibleText = page => page.evaluate(() => document.body.innerText);
const stepsState = page => page.evaluate(() => [1, 2, 3, 4].map(n => { const li = document.getElementById("st" + n); return li.className + (li.querySelector("button") ? "+btn" : "") + (li.getAttribute("aria-current") ? "*" : ""); }));
const heading = page => page.evaluate(() => [...document.querySelectorAll("h1")].filter(h => !h.closest(".hidden")).map(h => h.textContent));
const activeId = page => page.evaluate(() => document.activeElement.id || document.activeElement.tagName);
const SIZES = [[1280, 720], [1366, 768]];
// Restyle helpers: sr-only = clipped to ~1px but the text is still in the DOM; contrast probe at a given state.
const srOnly = (page, sel) => page.evaluate(s => { const e = document.querySelector(s); const r = e.getBoundingClientRect(); const cs = getComputedStyle(e); return { w: r.width, h: r.height, text: e.textContent.trim(), clip: cs.clip || cs.clipPath }; }, sel);
const contrastAt = async (page, label, min = 4.5) => { const bad = await page.evaluate(contrastProbe, min); assert.deepEqual(bad, [], `${label}: contrast < ${min}: ` + JSON.stringify(bad)); };

// ===================================================================== layout
await withServer({}, srv => withPage(srv, { width: 1280, height: 900 }, async (page, h) => {
  await step("the action bar (the step's button and the reason line) is on screen at 1280x720, 1366x768, 1280x640, 1000x600 and 960x600 (the window minimum)", async () => {
    await gotoStep(page, 2);          // no console copy yet: there is a reason to show
    for (const [w, hh] of [[1280, 720], [1366, 768], [1280, 640], [1000, 600], [960, 600], [1280, 900]]) {
      await page.setViewportSize({ width: w, height: hh }); await page.waitForTimeout(150);
      const m = await page.evaluate(() => {
        const r = id => document.getElementById(id).getBoundingClientRect();
        return { c: r("continue").toJSON(), b: r("blockers").toJSON(), bl: document.querySelectorAll("#blockers li").length, ow: document.documentElement.scrollWidth > innerWidth, ih: innerHeight, iw: innerWidth };
      });
      assert.ok(m.bl > 0, "blockers listed");
      assert.ok(m.c.top >= 0 && m.c.bottom <= m.ih, `${w}x${hh}: CONTINUE not fully visible ${JSON.stringify(m.c)}`);
      assert.ok(m.b.top >= 0 && m.b.bottom <= m.ih, `${w}x${hh}: blockers not fully visible ${JSON.stringify(m.b)}`);
      assert.ok(m.c.right <= m.iw && m.b.right <= m.iw, "inside the width");
      assert.equal(m.ow, false, `${w}x${hh}: horizontal page overflow`);
    }
  });
  await step("the action bar stays visible while the page content scrolls", async () => {
    await page.setViewportSize({ width: 960, height: 600 });
    for (const n of [1, 2, 3, 4]) {
      await gotoStep(page, n);
      await page.evaluate(() => { document.getElementById("scroll").scrollTop = 9999; });
      const b = await page.evaluate(() => document.getElementById("actionbar").getBoundingClientRect().bottom);
      assert.ok(b <= 600 && b > 400, `step ${n}: action bar bottom ${b}`);
    }
    await gotoStep(page, 1);
  });
  await step("step 01 GAME is highlighted on first load (and exposes aria-current); the later steps are dim", async () => {
    const fresh = await newPage(browser, srv, { width: 1280, height: 720 });
    try {
      const r = await fresh.page.evaluate(() => ({ cls: document.getElementById("st1").className, cur: document.getElementById("st1").getAttribute("aria-current"), c2: document.getElementById("st2").className, t: document.getElementById("steps").innerText.replace(/\s+/g, " ") }));
      assert.equal(r.cls, "on"); assert.equal(r.cur, "step"); assert.equal(r.c2, "future");
      assert.match(r.t, /^01 GAME 02 CONSOLE CONTENT 03 INSTALL 04 PLAY$/);
      const n = await fresh.page.evaluate(() => ({ title: document.title, tag: document.querySelector(".tagline").innerText.trim(), h1s: [...document.querySelectorAll("h1")].map(h => h.textContent).join("|") }));
      assert.equal(n.title, "F.E.A.R. Exclusive Console Content"); assert.equal(n.tag, "EXCLUSIVE CONSOLE CONTENT");
      assert.doesNotMatch(n.h1s, /Restoration/i);
    } finally { await fresh.ctx.close(); }
  });
  await step("one wallpaper on every step, #stage always compact, brand = wordmark + eyebrow/tagline, Browse on step 01 is the gold button", async () => {
    for (const n of [1, 2, 3, 4]) {
      await gotoStep(page, n);
      const r = await page.evaluate(() => { const bg = document.getElementById("bgart"); const cs = getComputedStyle(bg); return { compact: document.getElementById("stage").classList.contains("compact"), bgs: document.querySelectorAll("#bgart").length, ds: bg.dataset.step, img: cs.backgroundImage.startsWith("url("), wm: !!document.querySelector(".brand > .wordmark"), lines: !!document.querySelector(".brand > .lines > .eyebrow"), tag: !!document.querySelector(".brand > .lines > .tagline") }; });
      assert.deepEqual([r.compact, r.bgs, r.img, r.wm, r.lines, r.tag], [true, 1, true, true, true, true], `step ${n}: ` + JSON.stringify(r));
      assert.ok(r.ds, "data-step still set");
    }
    // the same background image on every step
    const imgs = [];
    for (const n of [1, 2, 3, 4]) { await gotoStep(page, n); imgs.push(await page.evaluate(() => getComputedStyle(document.getElementById("bgart")).backgroundImage.length)); }
    assert.equal(new Set(imgs).size, 1, "one wallpaper: " + imgs.join(","));
    await gotoStep(page, 1);
    assert.equal(await page.evaluate(() => /\bgold\b/.test(document.getElementById("pcbrowse").className) && document.getElementById("pcbrowse").classList.contains("btn")), true);
  });
  await step("no footer and no keyboard hints; license link and dialog, support row and dialog, NOTES / Read from / archcfg boxes are gone; the 4 content cards are static", async () => {
    await gotoStep(page, 1);
    const r = await page.evaluate(() => ({
      foot: document.querySelectorAll("footer, .footer, .keys, kbd").length,
      gone: ["showlic", "lic", "liclose", "lictext", "diagrow", "showdiag", "diag", "changes", "reads", "cfgsec", "cfgmeta", "cfgsum", "cfgtail", "cfgnotes"].filter(i => document.getElementById(i)),
      cards: ["sfod", "sas12", "holiday", "sm15"].map(k => { const b = document.getElementById("use-" + k); return { tag: b.tagName, role: b.getAttribute("role"), checked: b.getAttribute("aria-checked"), tab: b.getAttribute("tabindex"), btn: !!b.querySelector("button,input,a") }; }),
      all: document.body.textContent,
    }));
    assert.equal(r.foot, 0, "no footer / key hints");
    assert.deepEqual(r.gone, []);
    for (const c of r.cards) assert.deepEqual([c.tag, c.role, c.checked, c.tab, c.btn], ["DIV", null, null, null, false], JSON.stringify(c));
    assert.doesNotMatch(r.all, /Community mod|Unofficial installer|Open-source licenses|Details for support|Not found, or something looks off/);
  });
  await step("item titles / names / statuses / reasons do not overflow between 1000 and 1280 px (step 02)", async () => {
    for (const w of [1000, 1040, 1060, 1100, 1200, 1280]) {
      await page.setViewportSize({ width: w, height: 800 });
      for (const n of [2]) {
        await gotoStep(page, n);
        const bad = await page.evaluate(() => [...document.querySelectorAll(".item .t, .item .d, .item .why, .src .name, .src .sub, .status")].filter(e => e.getClientRects().length).map(e => {
          const p = e.closest(".item,.src"); const pr = p.getBoundingClientRect(), er = e.getBoundingClientRect();
          return { t: e.textContent.slice(0, 24), over: er.right > pr.right + 0.5 || (e.scrollWidth > e.clientWidth + 1 && getComputedStyle(e).overflow === "visible" && er.right > pr.right) };
        }).filter(x => x.over));
        assert.deepEqual(bad, [], `${w}px step ${n}`);
      }
    }
  });
  await step("all visible text has a contrast ratio of at least 4.5:1 on every step (placeholder and disabled buttons included)", async () => {
    for (const [w, hh] of SIZES) {
      await page.setViewportSize({ width: w, height: hh });
      for (const n of [1, 2, 3, 4]) {
        await gotoStep(page, n);
        await contrastAt(page, `${w}x${hh} step ${n}`);
      }
    }
    await page.setViewportSize({ width: 1366, height: 768 });
    await gotoStep(page, 1);
    const ph = await page.evaluate(() => getComputedStyle(document.getElementById("pcpath"), "::placeholder").color);
    const ratio = await page.evaluate(c => {
      const p = c.match(/[\d.]+/g).map(Number); const a = p.length > 3 ? p[3] : 1; const bg = [8, 10, 11];
      const mix = [0, 1, 2].map(i => p[i] * a + bg[i] * (1 - a));
      const L = v => { v /= 255; return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4); };
      const lum = x => 0.2126 * L(x[0]) + 0.7152 * L(x[1]) + 0.0722 * L(x[2]);
      const a1 = lum(mix), b1 = lum(bg); return (Math.max(a1, b1) + 0.05) / (Math.min(a1, b1) + 0.05);
    }, ph);
    assert.ok(ratio >= 4.5, "placeholder contrast " + ratio);
  });
  await step("US spelling in all visible text of every step", async () => {
    for (const n of [1, 2, 3, 4]) {
      await gotoStep(page, n);
      assert.doesNotMatch(await visibleText(page), /recognis|colour|behaviour|licen[c]e|centre|favour|catalogue|initialis|organis|analys(e|ing)\b|cancell|grey/i, `step ${n}`);
    }
  });
  await step("structure - one landmark main, only the wide-window summary aside, live region, unique Browse names, menu roles, describedby, one visible h1", async () => {
    await gotoStep(page, 2);
    const r = await page.evaluate(() => ({
      main: document.querySelectorAll("main").length, aside: [...document.querySelectorAll("aside")].map(a => a.id), asideShown: innerWidth < 1500 ? getComputedStyle(document.getElementById("summary")).display : "n/a",
      live: document.getElementById("live").getAttribute("role"),
      names: ["pcbrowse", "browse-ps3", "browse-x360"].map(i => document.getElementById(i).getAttribute("aria-label")),
      expanded: document.getElementById("browse-ps3").getAttribute("aria-expanded"),
      menu: document.getElementById("menu-ps3").getAttribute("role"), items: [...document.querySelectorAll("#menu-ps3 [role=menuitem]")].map(b => b.textContent),
      desc: document.getElementById("continue").getAttribute("aria-describedby"), descI: document.getElementById("install").getAttribute("aria-describedby"),
      bars: [...document.querySelectorAll("[role=progressbar]")].map(b => [b.id, b.getAttribute("aria-valuemin"), b.getAttribute("aria-valuemax"), b.getAttribute("aria-valuenow") !== null]),
      statusRoles: ["pchint", "echo", "msg-ps3", "msg-x360"].map(i => document.getElementById(i).getAttribute("role")),
      h1: [...document.querySelectorAll("h1")].filter(e => !e.closest(".hidden")).length,
      steps: document.querySelector("nav.steps").getAttribute("aria-label"),
    }));
    assert.equal(r.main, 1); assert.deepEqual(r.aside, ["summary"]); assert.ok(r.asideShown === "none" || r.asideShown === "n/a", "summary panel must stay hidden below 1500 px: " + r.asideShown); assert.equal(r.live, "status");
    assert.equal(new Set(r.names).size, 3, JSON.stringify(r.names)); assert.ok(r.names.every(Boolean));
    assert.equal(r.expanded, "false"); assert.equal(r.menu, "menu");
    assert.equal(r.items.length, 2); assert.match(r.items[0], /folder/i); assert.match(r.items[1], /ISO/);
    assert.equal(r.desc, "barinfo"); assert.equal(r.descI, "barinfo");
    assert.deepEqual(r.bars.map(b => b[0]), ["barwrap-ps3", "barwrap-x360", "pbar"]);
    assert.ok(r.bars.every(b => b[1] === "0" && b[2] === "100" && b[3]), JSON.stringify(r.bars));
    assert.ok(r.statusRoles.every(x => x === "status"), JSON.stringify(r.statusRoles));
    assert.equal(r.h1, 1); assert.equal(r.steps, "Steps");
  });
  await step("a visible focus ring on buttons, inputs and step buttons (the content cards are static, not focusable)", async () => {
    const ring = async sel => {
      await page.focus(sel); await page.keyboard.press("Shift+Tab"); await page.keyboard.press("Tab"); // keyboard focus => :focus-visible
      const f = await page.evaluate(s => { const cs = getComputedStyle(document.querySelector(s)); const box = s === "#pcpath" ? getComputedStyle(document.getElementById("pcbox")) : cs;
        return { o: cs.outlineStyle + " " + cs.outlineWidth, sh: cs.boxShadow, bo: box.boxShadow, bc: box.borderColor }; }, sel);
      const has = (!/^none/.test(f.o) && !/ 0px$/.test(f.o)) || (f.sh && f.sh !== "none") || (f.bo && f.bo !== "none");
      assert.ok(has, `${sel}: ${JSON.stringify(f)}`);
    };
    await gotoStep(page, 1); for (const sel of ["#pcpath", "#pcbrowse", "#recheck"]) await ring(sel);
    await gotoStep(page, 2); for (const sel of ["#browse-x360", "#back"]) await ring(sel);
    await gotoStep(page, 3); await ring("#install");
    await gotoStep(page, 4); await ring("#close");
    await gotoStep(page, 3); await ring('#steps button[data-step="1"]');
  });
  await step("a step change moves focus to the new heading and announces it", async () => {
    for (const n of [2, 3, 4, 1]) {
      await page.evaluate(k => go(k), n);
      assert.equal(await activeId(page), "h1-" + n);
      const t = await page.evaluate(() => new Promise(r => setTimeout(() => r(document.getElementById("live").textContent), 120)));
      assert.match(t, new RegExp(`Step ${n} of 4`));
    }
  });
  await step("drops, reload, find, zoom and print shortcuts are blocked in the page", async () => {
    const r = await page.evaluate(() => {
      const out = {};
      const dt = new DataTransfer(); dt.items.add(new File(["x"], "x.iso"));
      for (const t of ["dragenter", "dragover", "drop"]) { const e = new DragEvent(t, { bubbles: true, cancelable: true, dataTransfer: dt }); document.body.dispatchEvent(e); out[t] = e.defaultPrevented; }
      for (const [k, o] of [["F5", {}], ["r", { ctrlKey: true }], ["f", { ctrlKey: true }], ["+", { ctrlKey: true }], ["-", { ctrlKey: true }], ["0", { ctrlKey: true }], ["p", { ctrlKey: true }], ["ArrowLeft", { altKey: true }]]) {
        const e = new KeyboardEvent("keydown", { key: k, bubbles: true, cancelable: true, ...o }); document.body.dispatchEvent(e); out[(o.ctrlKey ? "Ctrl+" : o.altKey ? "Alt+" : "") + k] = e.defaultPrevented;
      }
      const w = new WheelEvent("wheel", { ctrlKey: true, bubbles: true, cancelable: true }); document.body.dispatchEvent(w); out.ctrlWheel = w.defaultPrevented;
      const c = new MouseEvent("contextmenu", { bubbles: true, cancelable: true }); document.body.dispatchEvent(c); out.contextmenu = c.defaultPrevented;
      return out;
    });
    for (const [k, v] of Object.entries(r)) assert.equal(v, true, k + " not blocked: " + JSON.stringify(r));
    assert.equal(page.url().startsWith(srv.base), true);
  });
  await step("the license texts are still embedded and served by the back end (RPC); the page has no UI for them", async () => {
    const r = await fetch(srv.base + "/rpc/licenses", { method: "POST", body: "[]" });
    assert.equal(r.status, 200);
    const t = await r.text();
    for (const w of ["Go standard library", "MIT License", "ISC License", "Microsoft", "WebView2Loader", "c4674acf95f0800793a4a6d61132adf5dfa694c218e482d86093494c4e84100a",
                     "GNU LESSER GENERAL PUBLIC LICENSE", "The FreeType Project LICENSE"]) assert.ok(t.includes(w), w);
    await page.keyboard.press("Escape");
    assert.equal(await visibleStep(page), 1);
  });
  await step("no page errors (layout group)", async () => assert.deepEqual(h.errors, []));
}));

// ============================================================ the dev-only endpoints
await step("/dev/pick exists only with -dev, needs the random token, loopback only, Host/Origin checked", async () => {
  await withServer({ dev: false }, async s => {
    assert.equal(s.token, "");
    assert.equal(await rawRequest(s, { path: "/dev/pick?path=x", method: "POST" }), 404);
  });
  await withServer({}, async s => {
    assert.match(s.token, /^[0-9a-f]{32}$/);
    assert.equal(await rawRequest(s, { path: "/dev/pick?path=", method: "POST" }), 403, "no token");
    assert.equal(await rawRequest(s, { path: "/dev/pick?path=", method: "POST", headers: { "X-Dev-Token": "00" } }), 403, "wrong token");
    assert.equal(await rawRequest(s, { path: "/dev/pick?path=", method: "GET", headers: { "X-Dev-Token": s.token } }), 403, "GET");
    assert.equal(await rawRequest(s, { path: "/dev/pick?path=", method: "POST", headers: { "X-Dev-Token": s.token, Host: "evil.example" } }), 403, "foreign Host");
    assert.equal(await rawRequest(s, { path: "/rpc/snapshot", method: "POST", headers: { Origin: "http://evil.example" } }), 403, "foreign Origin");
    assert.equal(await rawRequest(s, { path: "/rpc/install", method: "POST", headers: { Origin: "http://evil.example" } }), 403, "foreign Origin on the install call");
    assert.equal(await rawRequest(s, { path: "/", headers: { Host: "evil.example" } }), 403, "DNS rebinding Host");
    assert.equal(await rawRequest(s, { path: "/dev/pick?path=", method: "POST", headers: { "X-Dev-Token": s.token } }), 200, "good request");
    assert.equal(await rawRequest(s, { path: "/rpc/snapshot", method: "GET" }), 405);
  });
  const bad = await startServer({ bin, steam, addr: "0.0.0.0:0" });
  await sleep(300);
  assert.notEqual(bad.proc.exitCode, null, "must refuse a non-loopback address"); assert.match(bad.log, /only loopback/);
  const bad2 = await startServer({ bin, steam, addr: "192.0.2.1:9" }); await sleep(300);
  assert.notEqual(bad2.proc.exitCode, null);
});

// ================================================================ PC folder + EchoPatch (step 01)
await withServer({}, srv => withPage(srv, { width: 1366, height: 768 }, async (page, h) => {
  const typePC = async (v, { enter = true } = {}) => { await page.fill("#pcpath", v); if (enter) await page.press("#pcpath", "Enter"); await page.waitForFunction(() => document.getElementById("continue").getAttribute("aria-describedby") && !/Checking the folder/.test(document.getElementById("blockers").textContent), null, { timeout: 8000 }); };
  await step("auto-detect: the Steam library game folder is found and EchoPatch is recognized; step 01 shows nothing about console copies", async () => {
    await page.waitForFunction(() => document.getElementById("pcpath").value.includes("FEAR Ultimate Shooter Edition"));
    assert.match(await text(page, "#pchint"), /F\.E\.A\.R\. found/);
    assert.deepEqual(await heading(page), ["Find your copy of F.E.A.R."]);
    assert.equal(await page.isVisible("#row-ps3"), false); assert.equal(await page.isVisible("#use-sfod"), false);
    assert.equal(await page.isVisible("#recheck"), false); assert.equal(await page.locator("#showdiag, #diagrow").count(), 0);   // Verify only shows while the folder has a problem
    // Verify (was "Check again") and Uninstall live in the bottom bar, Uninstall last, both styled like Continue
    assert.equal(await text(page, "#recheck"), "Verify");
    assert.deepEqual(await page.evaluate(() => [...document.getElementById("actionbar").children].slice(-2).map(e => e.id)), ["recheck", "uninstall"]);
    assert.equal(await page.evaluate(() => ["recheck", "uninstall"].every(id => document.getElementById(id).classList.contains("go"))), true);
    assert.equal(await page.locator("#modbox button").count(), 0);
  });
  await step("EchoPatch message names the project once and does not repeat itself", async () => {
    const t = await text(page, "#echo");
    assert.match(t, /EchoPatch by Wemino \(GitHub\)/); assert.equal(t.match(/EchoPatch/g).length >= 1, true);
    assert.doesNotMatch(t, /EchoPatch:\s*EchoPatch|EchoPatch found\.\s*EchoPatch found/);
    assert.equal((t.match(/Wemino/g) || []).length, 1);
    await typePC(G("pc_noecho"));
    const t2 = await text(page, "#echo");
    assert.match(t2, /EchoPatch is required\./); assert.equal((t2.match(/EchoPatch by Wemino \(GitHub\) is not installed/g) || []).length, 1);
  });
  await step("foreign dinput8.dll and ini-only get accurate blocker text (not 'not found'); CONTINUE stays off", async () => {
    await typePC(G("pc_foreign"));
    let b = await text(page, "#blockers");
    assert.match(b, /dinput8\.dll in the game folder is not EchoPatch/); assert.doesNotMatch(b, /not found|not installed/);
    await typePC(G("pc_ini_only"));
    b = await text(page, "#blockers");
    assert.match(b, /dinput8\.dll is missing/); assert.doesNotMatch(b, /not found|not installed/);
    await typePC(G("pc_noecho")); b = await text(page, "#blockers");
    assert.match(b, /not installed/);
    assert.equal(await contEnabled(page), false);
    assert.doesNotMatch(b, /console|PlayStation|Xbox/, "step 01 lists only its own blockers");
  });
  await step("quotes are stripped; a path to FEAR.exe uses its folder; a file says 'file, not a folder'; bare drive and %VAR% rejected clearly", async () => {
    await typePC('"' + G("pc_upper") + '"');
    assert.equal(await page.inputValue("#pcpath"), G("pc_upper")); assert.match(await text(page, "#pchint"), /F\.E\.A\.R\. found/);
    await typePC("\u201c" + G("pc_upper") + "\u201d");
    assert.equal(await page.inputValue("#pcpath"), G("pc_upper"), "curly quotes");
    await typePC(path.join(GAME, "FEAR.exe"));
    assert.equal(await page.inputValue("#pcpath"), GAME); assert.match(await text(page, "#pchint"), /found/);
    await typePC(G("not_a_folder.txt")); assert.match(await text(page, "#pchint"), /That is a file, not a folder/);
    await typePC("D:"); assert.match(await text(page, "#pchint"), /full path/);
    await typePC("%USERPROFILE%"); assert.match(await text(page, "#pchint"), /%USERPROFILE%.*(not defined|full path)|full path/);
    await typePC("~"); const v = await page.inputValue("#pcpath"); assert.ok(v.startsWith("/") && v !== "~", "~ expanded: " + v);
    await typePC(GAME + "/");
    assert.equal(await page.inputValue("#pcpath"), GAME, "trailing separator removed");
    assert.equal(await contEnabled(page), true, "a valid game folder with EchoPatch is all step 01 needs, and there is no stuck 'Checking'");
    assert.doesNotMatch(await text(page, "#blockers"), /Checking the folder/);
  });
  await step("typing an invalid path switches CONTINUE off at once and it stays off after the check", async () => {
    await typePC(GAME);
    assert.equal(await contEnabled(page), true);
    await page.fill("#pcpath", "C:\\definitely\\not\\here");
    assert.equal(await contEnabled(page), false, "disabled from the first keystroke");
    assert.match(await text(page, "#blockers"), /Checking the folder you typed|Select your F\.E\.A\.R\./);
    await page.waitForFunction(() => /does not exist|full path|not found/.test(document.getElementById("pchint").textContent));
    assert.equal(await contEnabled(page), false);
    assert.equal(await page.getAttribute("#pcpath", "aria-invalid"), "true");
  });
  await step("fx: every page effect has its own switch, and the install readout is hidden from screen readers", async () => {
    const cls = await page.evaluate(() => document.documentElement.className);
    for (const k of ["flashlight", "flicker", "readout", "lockon", "stepline", "tilt", "poweron", "sound"]) assert.match(cls, new RegExp("\\bfx-" + k + "\\b"));
    // the install bar's sounds travel inside the page (ui/sounds/inline_sounds.py), each one a WAV
    const sfx = await page.evaluate(() => {
      const d = JSON.parse(document.getElementById("sfxdata").textContent);
      return Object.keys(d).sort().map(k => k + ":" + atob(d[k]).slice(0, 4) + atob(d[k]).slice(8, 12)).join(",");
    });
    assert.equal(sfx, ["finish", "segment", "spark1", "spark2", "spark3", "spark4"].map(k => k + ":RIFFWAVE").join(","));
    // an install takes seconds, so there is no mute button; FX.sound alone turns the sounds off
    assert.equal(await page.locator("#sound").count(), 0);
    assert.equal(await page.getAttribute("#readout", "aria-hidden"), "true");
    // a switch turned off brings back the plain page: here the pop-up opens with its old fade
    const anim = await page.evaluate(() => {
      const h = document.documentElement, d = document.getElementById("cmds");
      h.classList.remove("fx-poweron"); d.showModal(); const n = getComputedStyle(d).animationName; d.close(); h.classList.add("fx-poweron");
      return n;
    });
    assert.equal(anim, "cmdin");
  });
  await step("'Verify' picks up EchoPatch installed outside the program; so does returning to the window", async () => {
    const d = fs.mkdtempSync(path.join(tmp, "late-")); fs.writeFileSync(path.join(d, "FEAR.exe"), "MZ"); fs.writeFileSync(path.join(d, "FEAR_8.Arch00"), "x");
    await typePC(d);
    assert.match(await text(page, "#echo"), /EchoPatch is required/);
    // "How to get EchoPatch" opens the EchoPatch panel (same style as the other panels); Esc closes it and stays on step 01
    await page.click("#echoget");
    assert.equal(await page.evaluate(() => document.getElementById("echopanel").open), true);
    assert.equal(await text(page, "#echopanel-open"), "Open EchoPatch page ↗");
    await page.keyboard.press("Escape");
    assert.equal(await page.evaluate(() => document.getElementById("echopanel").open), false);
    assert.equal(await page.isVisible("#step1"), true);
    fs.writeFileSync(path.join(d, "dinput8.dll"), "..EchoPatch..");
    await page.click("#recheck");
    await page.waitForFunction(() => /EchoPatch by Wemino \(GitHub\) found/.test(document.getElementById("echo").textContent));
    await page.waitForFunction(() => document.getElementById("continue").getAttribute("aria-disabled") !== "true", null, { timeout: 5000 }).catch(() => {});   // the plan follows the echo text by a moment
    assert.equal(await contEnabled(page), true, "EchoPatch was the only blocker");
    assert.equal(await page.isVisible("#recheck"), false, "Verify goes once the folder checks out");
    // second path: remove it, return to the window (focus event)
    fs.rmSync(path.join(d, "dinput8.dll"));
    await page.waitForTimeout(1100);
    await page.evaluate(() => window.dispatchEvent(new Event("focus")));
    await page.waitForFunction(() => /EchoPatch is required/.test(document.getElementById("echo").textContent));
    await page.waitForFunction(() => document.getElementById("continue").getAttribute("aria-disabled") === "true", null, { timeout: 5000 }).catch(() => {});
    assert.equal(await contEnabled(page), false);
  });
  await step("paths shown in messages use one separator style", async () => {
    await typePC(GAME);
    const t = await visibleText(page); const m = t.match(/(?:\/[^\s]+){2,}/g) || [];
    for (const p of m) assert.doesNotMatch(p, /\\/);
  });
  await step("no page errors (PC group)", async () => assert.deepEqual(h.errors, []));
}));

// ================================================================ step 02: console copies and their content
await withServer({}, srv => withPage(srv, { width: 1366, height: 768 }, async (page, h) => {
  const stat = k => page.locator(`#status-${k}`).innerText();
  const cont = () => contEnabled(page);
  const card = (k) => page.evaluate(id => { const b = document.getElementById("use-" + id); return { checked: String(b.classList.contains("on")), disabled: b.getAttribute("aria-disabled"), why: document.getElementById("why-" + id).textContent, title: b.title, ariaChecked: b.getAttribute("aria-checked"), tab: b.getAttribute("tabindex"), role: b.getAttribute("role") }; }, k);   // checked = class "on" (included)
  await page.waitForFunction(() => document.getElementById("pcpath").value.includes("FEAR"));

  await step("01 -> 02: CONTINUE on step 01 is enabled for a valid folder with EchoPatch and moves to 02; the indicator follows", async () => {
    assert.equal(await cont(), true);
    assert.deepEqual(await stepsState(page), ["on*", "future", "future", "future"]);
    await page.click("#continue"); await page.waitForFunction(() => !document.getElementById("step2").classList.contains("hidden"));
    assert.deepEqual(await heading(page), ["Choose your console content"]);
    assert.equal(await activeId(page), "h1-2");
    assert.deepEqual(await stepsState(page), ["done+btn", "on*", "future", "future"]);
    assert.equal(await page.isVisible("#back"), true);
    assert.equal(await page.evaluate(() => ["use-ps3", "use-x360"].map(i => document.getElementById(i))).then(x => x.every(e => e === null)), true, "no card for the copy itself: the cards are the content items");
    assert.equal(await page.evaluate(() => ["row-ps3", "row-x360"].map(r => [...document.querySelectorAll(`#${r} .cardbtn.item`)].map(b => b.id).join(","))).then(x => x.join("|")), "use-sfod,use-sas12|use-holiday,use-sm15,use-ia", "each console box holds its own items");
    assert.equal(await page.evaluate(() => ["always3", "mission"].map(i => document.getElementById(i) || document.querySelector(".mission"))).then(x => x.every(e => e === null)), true, "the old mission cards are gone");
    assert.equal(await page.evaluate(() => document.querySelectorAll("#bgart").length), 1);
    assert.equal(await text(page, "#step2 .lead"), "Add both console copies. Everything they provide is installed.");
    assert.equal(await text(page, "#step2 .note"), "Game assets are not included. Your own console copies are required.");
  });
  await step("02: with no copy picked CONTINUE is off and says what is missing; a click on it stays on 02", async () => {
    assert.equal(await cont(), false);
    assert.match(await text(page, "#blockers"), /Select your PlayStation 3 or Xbox 360 copy/);
    assert.equal(await text(page, "#fine"), "", "no 'Ready.' and no preview text while the step cannot continue");
    assert.equal(await page.evaluate(() => document.getElementById("barinfo").classList.contains("empty")), false, "barinfo holds the blocker reason, so it is not 'empty'");
    await page.click("#continue", { force: true }); await page.waitForTimeout(250);
    assert.equal(await visibleStep(page), 2);
  });
  await step("Browse returns at once, the row shows 'Waiting for the file dialog', cancel leaves everything unchanged", async () => {
    await page.click("#browse-ps3");
    await page.waitForFunction(() => /Waiting for the file dialog/.test(document.getElementById("status-ps3").textContent));
    assert.equal((await state(srv)).dialog, "ps3");
    assert.equal(await cont(), false);
    assert.match(await text(page, "#blockers"), /Waiting for the file dialog/);
    assert.equal(await page.evaluate(() => document.getElementById("browse-x360").disabled), true, "second dialog cannot be started");
    await pick(srv, "");           // the user presses Cancel
    await page.waitForFunction(() => /No folder selected/.test(document.getElementById("status-ps3").textContent));
    assert.equal((await state(srv)).dialog || "", ""); assert.equal((await state(srv)).ps3.path, "");
  });
  await step("the picked name is shown under the row ('Selected: ...') with the full path as tooltip", async () => {
    await browse(srv, page, "ps3", F("ps3_disc_folder"));
    await page.waitForFunction(() => /Selected:/.test(document.getElementById("pick-ps3").textContent));
    assert.equal(await text(page, "#pick-ps3"), "Selected: ps3_disc_folder");
    assert.match(await page.getAttribute("#pick-ps3", "title"), /ps3_disc_folder/);
    assert.equal(await page.isVisible("#pick-ps3"), false, "the 'Selected' line is hidden by CSS (still in the DOM)");
    await waitSrc(srv, page, "ps3", F("ps3_disc_folder"));
  });
  await step("verified label is just 'Verified' (full message + path in the title); the catalog-size caveat is no longer shown as a note (it stays on the hidden VERIFIED badge's tooltip)", async () => {
    assert.equal(await stat("ps3"), "Verified");
    assert.match(await page.getAttribute("#status-ps3", "title"), /Verified — F\.E\.A\.R\. \(USA, PlayStation 3\)[\s\S]*ps3_disc_folder/, "the full message and the path are in the title");
    assert.equal(await text(page, "#msg-ps3"), "", "caveat notes are not shown");
    assert.doesNotMatch(await visibleText(page), /Note: |ubercatalog\.cat was checked only/);
    assert.match(await page.getAttribute("#v-ps3", "title"), /ubercatalog\.cat was checked only for being present and at least [\d,]+ bytes, not by checksum/);
    assert.equal(await page.evaluate(() => document.getElementById("row-ps3").classList.contains("verified")), true);
  });
  await step("02: a verified copy includes its items (class 'on') and CONTINUE is on as soon as one copy is verified", async () => {
    await page.waitForFunction(() => document.getElementById("continue").getAttribute("aria-disabled") !== "true");
    assert.equal(await cont(), true);
    assert.equal(await text(page, "#fine"), "Ready.");
    assert.equal(await text(page, "#blockers"), "");
    assert.equal(await page.evaluate(() => document.getElementById("barinfo").classList.contains("empty")), false, "barinfo shows 'Ready.'");
    assert.equal((await card("sfod")).checked, "true"); assert.equal((await card("sas12")).checked, "true");
  });
  await step("02: the content cards are static - not in the tab order, no checkbox role, a click changes nothing", async () => {
    for (const k of ["sfod", "sas12"]) {
      const c = await card(k);
      assert.deepEqual([c.disabled, c.ariaChecked, c.role, c.tab, c.checked], ["false", null, null, null, "true"], k);
      assert.equal(await page.evaluate(id => document.getElementById(id).matches("a,button,input,[tabindex]"), "use-" + k), false);
      await page.click("#use-" + k);
      assert.equal((await card(k)).checked, "true", k + " must stay included");
    }
    const tabbed = [];
    await page.focus("#browse-ps3");
    for (let i = 0; i < 8; i++) { await page.keyboard.press("Tab"); tabbed.push(await activeId(page)); }
    assert.ok(!tabbed.some(x => /^use-/.test(x)), "a card took keyboard focus: " + tabbed.join(","));
    await page.evaluate(() => document.activeElement.blur());
  });
  await step("02 with one copy: the other console's items stay in its box, are disabled and say why; clicking them does nothing; the static Note is on the page", async () => {
    assert.equal(await visibleStep(page), 2);
    assert.deepEqual(await stepsState(page), ["done+btn", "on*", "future", "future"]);
    const [sfod, sas, hol, sm] = [await card("sfod"), await card("sas12"), await card("holiday"), await card("sm15")];
    assert.deepEqual([sfod.checked, sfod.disabled, sfod.why], ["true", "false", ""]); assert.deepEqual([sas.checked, sas.disabled, sas.why], ["true", "false", ""]);
    assert.deepEqual([hol.checked, hol.disabled], ["false", "true"]); assert.deepEqual([sm.checked, sm.disabled], ["false", "true"]);
    assert.equal(hol.why, "Add your Xbox 360 copy above to include this"); assert.equal(sm.why, "Add your Xbox 360 copy above to include this");
    assert.equal(sm.role, null); assert.equal(sm.ariaChecked, null); assert.match(sm.title, /Xbox 360 copy/);
    // restyle: description and reason are visually hidden (sr-only: clipped to 1px) but still hold their text
    for (const sel of ["#why-holiday", "#use-holiday .d", "#use-sfod .d"]) { const o = await srOnly(page, sel); assert.ok(o.w <= 2 && o.h <= 2, sel + " should be clipped to ~1px: " + JSON.stringify(o)); assert.ok(o.text.length > 3, sel + " keeps its text"); }
    // the content names stand alone: no tick squares next to them
    assert.equal(await page.evaluate(() => document.querySelectorAll("#step2 .cardbtn.item .tick").length), 0, "no boxes next to the content names");
    assert.equal(await page.evaluate(() => document.getElementById("use-holiday").closest("#row-x360") !== null && document.getElementById("use-sfod").closest("#row-ps3") !== null), true);
    await page.click("#use-holiday", { force: true }); await page.click("#use-sm15", { force: true });
    assert.equal((await card("holiday")).checked, "false");
    assert.equal(await cont(), true, "the PS3 items are enough");
    assert.equal(await text(page, "#step2 .note"), "Game assets are not included. Your own console copies are required.");
    assert.equal(await page.evaluate(() => document.getElementById("always3")), null);
  });
  await step("02: a rejected copy says 'verified'; a copy still being checked says so (the item reasons)", async () => {
    await browse(srv, page, "x360", F("ps3_disc_folder")); await waitSrc(srv, page, "x360", F("ps3_disc_folder"));
    assert.equal(await stat("x360"), "Wrong console");
    assert.equal(await cont(), true, "a rejected extra copy does not block the verified one");
    assert.equal((await card("holiday")).why, "Needs a verified Xbox 360 copy (see above)");
    assert.equal((await card("sm15")).disabled, "true");
    await browse(srv, page, "x360", F("x360_xgd2.iso"));
    await page.waitForFunction(() => /Checking/.test(document.getElementById("status-x360").textContent));
    assert.equal((await card("holiday")).why, "Waiting for your Xbox 360 copy to be checked");
    assert.equal((await card("holiday")).disabled, "true");
  });
  await step("Clear and Cancel - Cancel stops a running check, Clear forgets a picked copy", async () => {
    await page.waitForFunction(() => /Checking/.test(document.getElementById("status-x360").textContent));
    assert.equal(await text(page, "#clear-x360"), "Cancel");
    assert.equal(await cont(), false, "still checking: CONTINUE waits");
    assert.match(await text(page, "#blockers"), /Still checking your Xbox 360 copy/);
    await page.click("#clear-x360");
    await page.waitForFunction(() => /No folder selected/.test(document.getElementById("status-x360").textContent));
    let st = await state(srv); assert.equal(st.x360.path, ""); assert.notEqual(st.x360.phase, "checking");
    assert.equal(await page.isVisible("#clear-x360"), false);
    assert.equal(await cont(), true, "the PS3 copy alone is fine again");
    await page.click("#clear-ps3"); assert.equal((await state(srv)).ps3.path, "");
    await page.waitForFunction(() => /No folder selected/.test(document.getElementById("status-ps3").textContent));
    assert.equal(await cont(), false);
    await browse(srv, page, "ps3", F("ps3_disc_folder")); await waitSrc(srv, page, "ps3", F("ps3_disc_folder"));
  });
  await step("while a copy is checked there is a progressbar with a value", async () => {
    await browse(srv, page, "x360", F("x360_xgd2.iso"));
    await page.waitForFunction(() => /Checking… \d+%/.test(document.getElementById("status-x360").textContent), null, { timeout: 15000 });
    const r = await page.evaluate(() => { const b = document.getElementById("barwrap-x360"); return { hidden: b.getAttribute("aria-hidden"), now: b.getAttribute("aria-valuenow"), role: b.getAttribute("role"), busy: document.getElementById("row-x360").getAttribute("aria-busy") }; });
    assert.deepEqual([r.hidden, r.role, r.busy], ["false", "progressbar", "true"]); assert.ok(+r.now >= 0);
  });
  await step("progress pushes do not re-plan - CONTINUE changes at most twice while the second copy is hashed (off when checking starts, on when it is done)", async () => {
    await page.click("#clear-x360");
    await page.waitForFunction(() => /No folder selected/.test(document.getElementById("status-x360").textContent));
    await page.waitForFunction(() => document.getElementById("continue").getAttribute("aria-disabled") !== "true");
    await page.evaluate(() => { window.__tog = 0; new MutationObserver(m => { const b = document.getElementById("continue"); for (const r of m) if (r.oldValue !== b.getAttribute(r.attributeName)) window.__tog++; }).observe(document.getElementById("continue"), { attributes: true, attributeOldValue: true, attributeFilter: ["aria-disabled", "disabled"] }); });
    await browse(srv, page, "x360", F("x360_xgd2.iso"));
    await waitSrc(srv, page, "x360", F("x360_xgd2.iso"));
    await page.waitForFunction(() => document.getElementById("continue").getAttribute("aria-disabled") !== "true");
    assert.ok(await page.evaluate(() => window.__tog) <= 2, "CONTINUE flickered while the second copy was hashed: " + await page.evaluate(() => window.__tog));
  });
  await step("Xbox 360 label; with both copies every item is enabled and included, the verified labels and the console logos show", async () => {
    assert.equal(await stat("x360"), "Verified");
    assert.match(await page.getAttribute("#status-x360", "title"), /Verified — F\.E\.A\.R\. \(Xbox 360\)[\s\S]*x360_xgd2\.iso/, "the full message and the path are in the title: " + await page.getAttribute("#status-x360", "title"));
    assert.equal(await page.evaluate(() => document.getElementById("status-x360").className.includes("ok") && document.getElementById("status-ps3").className.includes("ok")), true);
    assert.equal(await visibleStep(page), 2);
    for (const k of ["sfod", "holiday", "sas12", "sm15"]) { const c = await card(k); assert.deepEqual([c.checked, c.disabled, c.why], ["true", "false", ""], k); }
    // (the separate VERIFIED badge is hidden by CSS in the merged layout; the row class and the status label say it)
    assert.equal(await page.evaluate(() => ["ps3", "x360"].every(k => document.getElementById("row-" + k).classList.contains("verified") && !document.getElementById("v-" + k).classList.contains("hidden"))), true);
    // the console logos replace the disc art: one logo image per row, never the disc picture
    assert.equal(await page.evaluate(() => ["ps3", "x360"].every(k => { const t = document.getElementById("art-" + k); return t.classList.contains("thumb") && !t.classList.contains("real") && t.querySelectorAll("img.logo").length === 1 && t.querySelector("img.logo").naturalWidth > 0; })), true, "console logos");
    assert.equal(await page.locator("#art-ps3 .tag, #art-x360 .tag").count(), 0, "the thumbnails carry no PS3/X360 labels");
    assert.equal(await cont(), true);
    await contrastAt(page, "1366x768 step 02 with both copies verified");
  });
  await step("CONTINUE is never enabled while the page shows a rejected PC folder (no stale plan between pushes)", async () => {
    await gotoStep(page, 1);
    await page.evaluate(() => { window.__bad = 0; window.__seen = 0;
      new MutationObserver(() => { const h = document.getElementById("pchint"), c = document.getElementById("continue"); window.__seen++;
        if (/\bbad\b/.test(h.className) && c.getAttribute("aria-disabled") !== "true") window.__bad++; })
        .observe(document.body, { subtree: true, childList: true, attributes: true, characterData: true }); });
    for (let i = 0; i < 4; i++) {
      await page.fill("#pcpath", "/definitely/not/a/game" + i); await page.press("#pcpath", "Enter");
      await page.waitForFunction(() => /does not exist/.test(document.getElementById("pchint").textContent));
      await page.waitForTimeout(150);
      await page.fill("#pcpath", GAME); await page.press("#pcpath", "Enter");
      await page.waitForFunction(() => document.getElementById("continue").getAttribute("aria-disabled") !== "true");
    }
    const r = await page.evaluate(() => [window.__bad, window.__seen]);
    assert.equal(r[0], 0, "CONTINUE was enabled next to a rejected folder"); assert.ok(r[1] > 10);
  });
  await step("with everything valid, typing an invalid path and clicking CONTINUE straight away never advances", async () => {
    for (let i = 0; i < 4; i++) {
      await page.fill("#pcpath", "/definitely/not/a/game" + i);
      await page.click("#continue", { force: true });   // (a disabled-looking button can still be clicked by a user) mousedown blurs the field -> change; click runs at once
      await page.waitForTimeout(500);
      assert.equal(await visibleStep(page), 1, "advanced with an invalid PC folder (try " + i + ")");
      await page.fill("#pcpath", GAME); await page.press("#pcpath", "Enter");
      await page.waitForFunction(() => document.getElementById("continue").getAttribute("aria-disabled") !== "true");
    }
    // The same through the keyboard: type, Enter is consumed by the field (validates), a second Enter continues.
    await page.fill("#pcpath", "/nope/nothing"); await page.press("#pcpath", "Enter"); await page.waitForTimeout(400);
    assert.equal(await visibleStep(page), 1);
    await page.fill("#pcpath", GAME); await page.press("#pcpath", "Enter");
    await page.waitForFunction(() => document.getElementById("continue").getAttribute("aria-disabled") !== "true");
  });
  await step("after clicking a (static) content card, Enter continues; Esc returns; the card state is unchanged", async () => {
    await gotoStep(page, 2);
    await page.click("#use-sas12");                      // static: nothing happens
    assert.equal((await card("sas12")).checked, "true");
    await page.keyboard.press("Enter");
    await page.waitForTimeout(300);
    assert.equal(await visibleStep(page), 3, "Enter did not continue");
    await page.keyboard.press("Escape"); assert.equal(await visibleStep(page), 2);
    assert.equal((await card("sas12")).checked, "true");
    await page.evaluate(() => document.activeElement.blur());
  });
  await step("a held Enter goes 01 -> 02 only; a fresh Enter goes on; a held Enter on 02 lands on the review and stops; on 03 Enter never starts the install", async () => {
    await gotoStep(page, 1);
    await page.mouse.click(1200, 400);
    await page.keyboard.down("Enter"); await page.keyboard.down("Enter"); await page.keyboard.down("Enter"); await page.keyboard.up("Enter");
    await page.waitForTimeout(300);
    assert.equal(await visibleStep(page), 2, "must stop on step 2");
    await page.keyboard.press("Enter"); await page.waitForTimeout(250); assert.equal(await visibleStep(page), 3);
    await page.keyboard.press("Escape"); assert.equal(await visibleStep(page), 2);
    await page.keyboard.down("Enter"); for (let i = 0; i < 6; i++) await page.keyboard.down("Enter"); await page.keyboard.up("Enter");
    await page.waitForTimeout(300);
    assert.equal(await visibleStep(page), 3, "a held Enter on step 2 lands on the review and stops there");
    assert.equal((await install(srv)).state, "idle", "no install was started by the held key");
    await page.keyboard.press("Enter"); await page.waitForTimeout(300);
    assert.equal(await visibleStep(page), 3); assert.equal((await install(srv)).state, "idle");
    assert.equal(await page.isVisible("#install"), true);
  });
  await step("04: INSTALL ignores Enter (also on the focused button) and a repeating key; only a click starts it", async () => {
    for (const k of ["Enter", "Space"]) {
      await page.focus("#install");
      if (k === "Enter") { await page.keyboard.press("Enter"); await page.keyboard.down("Enter"); await page.keyboard.down("Enter"); await page.keyboard.up("Enter"); }
      else { await page.keyboard.down("Space"); await page.keyboard.down("Space"); await page.keyboard.down("Space"); /* held, not released: nothing may start */ }
      await page.waitForTimeout(350);
      assert.equal((await install(srv)).state, "idle", k + " started the install");
      assert.equal(await visibleStep(page), 3);
      if (k === "Space") await page.evaluate(() => document.activeElement.blur());   // (the keyup would be the explicit Space press; skip it)
    }
    await page.keyboard.up("Space").catch(() => {});
    assert.match(await page.locator("#live").innerText() + await page.evaluate(() => new Promise(r => setTimeout(() => r(document.getElementById("live").textContent), 150))), /Click Install|Step 3/);
  });
  await step("step navigation: Back and Esc go one step back, the indicator buttons jump back, later steps are not clickable", async () => {
    await page.click("#back"); assert.equal(await visibleStep(page), 2);
    await page.keyboard.press("Escape"); assert.equal(await visibleStep(page), 1);
    await page.keyboard.press("Escape"); assert.equal(await visibleStep(page), 1, "Esc on 01 does nothing");
    assert.equal(await page.isVisible("#back"), false);
    await page.click("#continue"); await page.click("#continue"); await page.waitForTimeout(200);
    assert.equal(await visibleStep(page), 3);
    assert.deepEqual(await stepsState(page), ["done+btn", "done+btn", "on*", "future"]);
    await page.click('#steps button[data-step="1"]'); assert.equal(await visibleStep(page), 1);
    assert.deepEqual(await stepsState(page), ["on*", "future", "future", "future"]);
    assert.equal(await page.locator("#steps button").count(), 0, "no future step is a button");
    await page.click("#continue"); await page.click("#continue"); await page.waitForTimeout(200);
    assert.equal(await visibleStep(page), 3);
    await page.click('#steps button[data-step="2"]'); assert.equal(await visibleStep(page), 2);
    assert.deepEqual(await stepsState(page), ["done+btn", "on*", "future", "future"]);
    await gotoStep(page, 3);
  });
  await step("no scrolling: every step fits at 1280x720 and 1366x768 (no vertical or horizontal scrolling) with the action bar on screen", async () => {
    for (const [w, hh] of SIZES) {
      await page.setViewportSize({ width: w, height: hh }); await page.waitForTimeout(120);
      for (const n of [1, 2, 3, 4]) {
        await gotoStep(page, n);
        const m = await scrollMetrics(page);
        assert.ok(m.sh <= m.ch, `${w}x${hh} step ${n}: content needs scrolling (${m.sh} > ${m.ch})`);
        assert.ok(m.sw <= m.cw && m.docW <= m.iw && m.docH <= m.ih, `${w}x${hh} step ${n}: overflow ${JSON.stringify(m)}`);
        assert.ok(m.barBottom <= hh && m.barBottom > hh - 130, `${w}x${hh} step ${n}: action bar at ${m.barBottom}`);
        const btn = await page.evaluate(() => { const id = ["continue", "install", "close"].find(i => !document.getElementById(i).classList.contains("hidden")); const r = document.getElementById(id).getBoundingClientRect(); return { id, top: r.top, bottom: r.bottom }; });
        assert.ok(btn.top >= 0 && btn.bottom <= hh, `${w}x${hh} step ${n}: ${btn.id}`);
      }
    }
    await page.setViewportSize({ width: 1366, height: 768 });
  });
  await step("step 04 shows How to play in groups over the full width, and the weapon commands panel (Watson and SM15, no Copy button)", async () => {
    await gotoStep(page, 4);
    const t = await text(page, "#step4");
    for (const w of ["Main Menu", "Bonus mission", "Instant action", "HUD layout", "guns", "gimmegun zSFODD_AutoShotgun", "gimmegun zSFODD_Machine Pistol", "Nothing has been installed yet.", "Avoid using older save files created before installing or updating this mod, as they may cause missing content, unexpected behavior, or bugs."]) assert.ok(t.includes(w), w);
    assert.match(await text(page, "#step3"), /Tip: start a new game after installing or updating\..*Your saves are not changed\./s, "fresh-save notice on the review");
    assert.doesNotMatch(t, /cheat (guns|gimmegun)/);
    assert.deepEqual(await page.$$eval("#step4 .hg h3", l => l.map(h => h.textContent)), ["Getting started", "Bonus missions & Instant Action", "Xbox 360 HUD", "Console weapons"]);
    // the two weapon commands and the "guns" box start at the same x; the commands sit beside or under their names, never mixed
    const weap = await page.$eval("#step4 .weap", sec => {
      const rows = [...sec.querySelectorAll("li")].map(li => {
        const s = li.querySelector("span").getBoundingClientRect(), c = li.querySelector("code").getBoundingClientRect();
        return { left: Math.round(c.left), under: c.top >= s.bottom - 1 };
      });
      return { rows, guns: Math.round(sec.querySelector(".hnote code").getBoundingClientRect().left) };
    });
    assert.equal(weap.rows.length, 2);
    assert.equal(weap.rows[0].left, weap.rows[1].left); assert.equal(weap.guns, weap.rows[0].left);
    assert.equal(weap.rows[0].under, weap.rows[1].under);
    assert.equal(await page.evaluate(() => document.getElementById("groups5")), null, "the selected-items column is gone");
    assert.doesNotMatch(t, /see the guide|guide\.(pdf|html|txt)|included guide/i);
    assert.equal(await page.isVisible("#close"), true); assert.equal(await page.isVisible("#install"), false);
    // Weapon commands sits in the bottom bar with Close, only on step 04
    assert.equal(await page.isVisible("#actionbar #cmdopen"), true);
    assert.equal(await page.evaluate(() => document.querySelector("#step4 #cmdopen")), null);
    assert.equal(await page.isVisible("#playnote"), false, "a fresh install does not say 'already installed'");
    assert.equal(await page.evaluate(() => document.getElementById("barinfo").classList.contains("empty")), true, "step 04 has nothing to say in the bar: barinfo.empty");
    await contrastAt(page, "1366x768 step 04");
    // the panel: opens centred over a blurred page, shows the Watson and SM15 commands, closes by button, Esc and outside click
    const open = () => page.evaluate(() => document.getElementById("cmds").open);
    await page.click("#cmdopen"); assert.equal(await open(), true);
    assert.deepEqual(await page.$$eval("#cmds code.cmd", l => l.map(c => c.textContent)), ["gimmegun zSFODD_AutoShotgun", "gimmegun zSFODD_Machine Pistol"]);
    assert.doesNotMatch(await text(page, "#cmds"), /copy/i, "no Copy button: the game console takes no paste");
    assert.match(await page.evaluate(() => getComputedStyle(document.getElementById("cmds"), "::backdrop").backdropFilter), /blur/);
    await page.click("#cmds-close"); assert.equal(await open(), false);
    await page.click("#cmdopen"); await page.keyboard.press("Escape"); assert.equal(await open(), false);
    assert.equal(await visibleStep(page), 4, "Esc closed the panel and did not go back a step");
    await page.click("#cmdopen"); await page.mouse.click(8, 8); assert.equal(await open(), false, "a click outside closes it");
    await gotoStep(page, 3);
  });
  await step("03 review: the install location, the selected items per console (Xbox 360 HUD under Instant Action, a third Both consoles column, no Always included section); the NOTES, Read from and Default.archcfg boxes are gone", async () => {
    await page.waitForFunction(() => document.getElementById("modloc").textContent.endsWith("FEAR_ConsoleRestoration"));
    assert.ok((await text(page, "#modloc")).startsWith(GAME));
    const grouped = sel => page.$$eval(sel + " li", l => l.map(x => ({ cls: x.className, t: x.innerText.replace(/\s+/g, " ").trim(), hasTag: !!x.querySelector(".gs") && x.className === "gr" })));
    const g = await grouped("#groups");
    assert.deepEqual(g.map(x => x.cls), ["gh", "gr", "gr", "gh", "gr", "gr", "gr", "gr", "gh", "gr", "gr", "gr"], JSON.stringify(g));
    assert.deepEqual(g.filter(x => x.cls === "gh").map(x => x.t), ["PLAYSTATION 3", "XBOX 360", "BOTH CONSOLES"], "three column headers, PS3 first");
    assert.ok(g.every(x => !x.hasTag), "no per-row source tag any more");
    assert.match(g[1].t, /SFOD-D/); assert.match(g[2].t, /Watson/); assert.match(g[4].t, /Holiday/); assert.match(g[5].t, /SM15/); assert.match(g[6].t, /Instant Action/); assert.match(g[7].t, /Xbox 360 HUD/); assert.match(g[8].t, /BOTH CONSOLES/); assert.match(g[9].t, /Bonus mission menu/);
    assert.equal(await page.evaluate(() => document.querySelectorAll("#groups li.gh .gi img").length), 2, "console logos");
    assert.equal(await page.evaluate(() => document.querySelectorAll("#groups li.gr .gt").length), 9);
    assert.equal(await page.evaluate(() => document.getElementById("modloc").tagName + "/" + document.getElementById("modloc").parentElement.className.trim()), "SPAN/loc");
    assert.equal(await page.evaluate(() => !!document.querySelector(".loc svg")), true);
    await contrastAt(page, "1366x768 step 03 review with copies");
    assert.equal(await page.evaluate(() => document.getElementById("alwayslist")), null, "no Always included section");
    assert.equal(await text(page, "#step3 .preview-banner"), "Check only: your copies will be read and checked, but nothing will be installed.");
    assert.equal(await page.evaluate(() => ["changes", "reads", "cfgsec", "cfgmeta", "cfgsum", "cfgtail", "cfgnotes"].filter(i => document.getElementById(i)).length), 0);
    assert.doesNotMatch(await text(page, "#step3"), /Default\.archcfg|Read from|NOTES/);
    assert.equal(await page.isVisible("#cancelnote"), false);
  });
  await step("rev: an older pushed state never replaces a newer one", async () => {
    await gotoStep(page, 2);
    const before = await stat("ps3");
    await page.evaluate(() => { const s = JSON.parse(JSON.stringify(S)); s.rev = S.rev - 1; s.ps3.phase = "idle"; s.ps3.result = null; s.ps3.path = ""; window.__fearState(s); });
    await page.waitForTimeout(150); assert.equal(await stat("ps3"), before);
  });
  await step("encrypted, wrong-console, junk and truncated images get one short plain sentence, the detailed finding is in the message's title", async () => {
    const trunc = path.join(tmp, "ps3_truncated.iso");
    const fd = fs.openSync(F("ps3_decrypted.iso"), "r"); const buf = Buffer.alloc(300 * 1024 * 1024); fs.readSync(fd, buf, 0, buf.length, 0); fs.closeSync(fd); fs.writeFileSync(trunc, buf);
    // [kind, path, status label, short visible sentence, detail (in the .msg title attribute)]
    const E = "This copy is encrypted. Decrypt it first, then select it again.", W = "This is a copy for the other console.", U = "No F.E.A.R. game files were found here.";
    const cases = [
      ["ps3", F("ps3_encrypted.iso"), /Encrypted/, E, /decrypt it first/],
      ["ps3", F("x360_folder"), /Wrong console/, W, /Xbox 360 copy, not a PlayStation 3/],
      ["ps3", F("junk.iso"), /Not recognized/, U, /not a disc image/],
      ["ps3", trunc, /Unreadable/, "The file could not be read completely.", /truncated or unreadable/],
      ["x360", F("ps3_decrypted.iso"), /Wrong console/, W, /PlayStation 3 copy, not an Xbox 360/],
      ["x360", F("junk.iso"), /Not recognized/, U, /not a disc image/],
      ["ps3", G("ps3_nouber"), /Incomplete/, "The game files are incomplete.", /ubercatalog\.cat is missing/],
      ["ps3", G("ps3_flipped"), /Unsupported version/, "This version of the game is not supported.", /differ from the original disc/],
      ["ps3", G("empty_folder"), /Not recognized/, U, /No PlayStation 3 F\.E\.A\.R\. files were found/],
      ["ps3", path.join(tmp, "does_not_exist"), /Could not read/, "This copy could not be read.", /does not exist/],
    ];
    const RAW = /\b(errno|no such file|syscall|invalid argument|runtime error|panic|goroutine|unexpected EOF|0x[0-9a-f]{4,}|open \/|read \/)\b/i;
    for (const [k, p, st, short, detail] of cases) {
      await browse(srv, page, k, p); await waitSrc(srv, page, k, p);
      await page.waitForFunction(([kk, re]) => new RegExp(re).test(document.getElementById("status-" + kk).textContent), [k, st.source], { timeout: 8000 }).catch(() => {});   // the page renders the pushed verdict a moment after the back end has it
      const s = await stat(k), m = await text(page, "#msg-" + k), t = await page.getAttribute("#msg-" + k, "title");
      assert.match(s, st, p + " status=" + JSON.stringify(s)); assert.ok(s.split(/\s+/).length <= 3, "short status label: " + s); assert.equal(m, short, p + ": " + m);
      assert.match(t || "", detail, p + " (title): " + t);
      assert.doesNotMatch(s + m, RAW, "raw error text for " + p + ": " + m);
      if (/truncated/.test(p)) assert.doesNotMatch(m + t, /differ from the original/, "truncated image mis-reported");
    }
    await browse(srv, page, "ps3", F("ps3_disc_folder")); await waitSrc(srv, page, "ps3", F("ps3_disc_folder"));
    await page.click("#clear-x360");
  });
  await step("US spelling in every message the back end produced above", async () => {
    const t = await visibleText(page);
    assert.doesNotMatch(t, /recognis|colour|behaviour|centre|catalogue|cancell/i);
  });
  await step("no page errors (step 02 group)", async () => assert.deepEqual(h.errors, []));
}));

// ================================================================ step 03 and 04: the install job (slow server)
// Copies are picked on the first page; later pages of the same server see them (the back end keeps them).
await withServer({ args: SLOW }, async srv => {
  const dataOf = async page => page.evaluate(() => ({
    h1: document.getElementById("h1-3p").textContent, phase: document.getElementById("phase").textContent,
    now: +document.getElementById("pbar").getAttribute("aria-valuenow"), text: document.getElementById("pbytes").textContent, pct: document.getElementById("ppct").textContent,
    file: document.getElementById("pfile").textContent, fillW: document.getElementById("pfill").style.width,
  }));
  const toReview = async page => {
    await browse(srv, page, "ps3", F("ps3_disc_folder")); await waitSrc(srv, page, "ps3", F("ps3_disc_folder"));
    await browse(srv, page, "x360", F("x360_folder")); await waitSrc(srv, page, "x360", F("x360_folder"));
  };
  await withPage(srv, { width: 1366, height: 768 }, async (page, h) => {
    await page.waitForFunction(() => document.getElementById("pcpath").value.includes("FEAR"));
    await page.click("#continue"); await page.waitForFunction(() => !document.getElementById("step2").classList.contains("hidden"));
    await toReview(page);
    await page.waitForFunction(() => document.getElementById("continue").getAttribute("aria-disabled") !== "true");
    await page.click("#continue"); await page.waitForTimeout(250);
    assert.equal(await visibleStep(page), 3);

    await step("INSTALL starts the job on a click; the step becomes the progress view (Installing, a plain sentence, bar, bytes, percent, file, Cancel)", async () => {
      assert.deepEqual(await heading(page), ["Ready to install"]);
      await page.click("#install");
      await page.waitForFunction(() => !document.getElementById("prog4").classList.contains("hidden"));
      assert.equal(await activeId(page), "h1-3p");
      assert.equal(await page.isVisible("#rev4"), false); assert.equal(await page.isVisible("#install"), false); assert.equal(await page.isVisible("#actionbar"), false, "no action bar while installing");
      assert.equal(await page.isVisible("#cancel"), true);
      assert.deepEqual(await stepsState(page), ["done", "done", "on*", "future"], "no past step is a button while the job runs");
      assert.equal(await page.getAttribute("#pbar", "role"), "progressbar");
      const pc = await page.evaluate(() => { const w = document.getElementById("pbarwrap"); const pf = document.getElementById("pfile"); return { card: w.classList.contains("pcard"), done: w.classList.contains("done"), icon: !!w.querySelector(":scope > .picon svg"), main: !!w.querySelector(":scope > .pmain #pbar"), meta: !!w.querySelector(":scope > .pmain .pmeta"), pfileOutside: !w.contains(pf) && w.nextElementSibling === pf, cancelOutlined: getComputedStyle(document.getElementById("cancel")).backgroundColor, cancelBorder: getComputedStyle(document.getElementById("cancel")).borderTopWidth }; });
      assert.deepEqual([pc.card, pc.done, pc.icon, pc.main, pc.meta, pc.pfileOutside], [true, false, true, true, true, true], JSON.stringify(pc));
      assert.notEqual(pc.cancelBorder, "0px", "Cancel is an outlined button");
      await contrastAt(page, "1366x768 install progress");
      const d = await dataOf(page); assert.equal(d.h1, "Installing");
      assert.match(await text(page, "#honest"), /^Check only: your copies are being read and checked, and nothing is written\.$/);
    });
    await step("the progress bar advances: aria-valuenow rises, the bytes text and the percent follow, phases read plainly, only relative file names show", async () => {
      const samples = [];
      for (let i = 0; i < 400; i++) {
        const d = await dataOf(page); samples.push(d);
        if (d.h1 === "Done") break;
        await sleep(60);
      }
      const nows = samples.map(s => s.now);
      for (let i = 1; i < nows.length; i++) assert.ok(nows[i] >= nows[i - 1], "valuenow went backwards: " + nows.join(","));
      assert.ok(new Set(nows).size >= 4, "the bar should pass through several values: " + [...new Set(nows)].join(","));
      assert.ok(nows.some(n => n > 0 && n < 100), "an in-between value was seen");
      const mid = samples.filter(s => s.h1 === "Installing" && s.now > 0);
      for (const s of mid) {
        assert.equal(s.pct, s.now + "%"); assert.equal(s.fillW, s.now + "%");
        assert.match(s.text, /^\d+(\.\d)? of \d+(\.\d)? (MB|GB)$/, s.text);
      }
      const phases = new Set(samples.map(s => s.phase));
      assert.ok(phases.has("Reading your PlayStation 3 copy\u2026"), [...phases].join("|"));
      assert.ok(phases.has("Reading your Xbox 360 copy\u2026"), [...phases].join("|"));
      for (const s of samples) { assert.doesNotMatch(s.file, /^\/|\\|\/tmp|\/home|:\\/, "full path in the file name: " + s.file); }
      assert.ok(samples.some(s => /\.(cat|gamedb|wld|wldclnt|wldsrvr|xwb|anmtree|matCat|texCat)$/i.test(s.file)), "a current file name is shown");
      const last = samples[samples.length - 1];
      assert.equal(last.h1, "Done"); assert.equal(last.now, 100);
      assert.equal(await page.evaluate(() => document.getElementById("pbarwrap").classList.contains("done")), true, "#pbarwrap.done after completion");
    });
    await step("done: 'Done', the honest preview line, CONTINUE goes to 04 (Enter does not), the job read everything and wrote nothing", async () => {
      assert.equal(await activeId(page), "h1-3p");
      assert.equal(await text(page, "#honest"), "Check finished: your copies were read and checked, and nothing was written.");
      const ip = await install(srv);
      assert.equal(ip.state, "done"); assert.equal(ip.error, ""); assert.equal(ip.doneBytes, ip.totalBytes); assert.ok(ip.totalBytes > 400 << 20, "totals " + ip.totalBytes); assert.equal(ip.preview, true);
      assert.match(await text(page, "#pbytes"), /^\d+\.\d of \d+\.\d GB$/);
      assert.equal(await page.evaluate(() => document.getElementById("pbarwrap").classList.contains("done") && getComputedStyle(document.querySelector("#pbarwrap .i-ok")).display !== "none" && getComputedStyle(document.querySelector("#pbarwrap .i-dl")).display === "none"), true, "done card shows the check icon, not the download icon");
      await contrastAt(page, "1366x768 install done");
      assert.equal(await page.isVisible("#continue"), true); assert.equal(await page.isVisible("#cancel"), false); assert.equal(await page.isVisible("#back"), true);
      assert.deepEqual(await stepsState(page), ["done+btn", "done+btn", "on*", "future"]);
      await page.mouse.click(1200, 400); await page.keyboard.press("Enter"); await page.waitForTimeout(250);
      assert.equal(await visibleStep(page), 3, "Enter does not leave the finished install");
      await page.click("#continue");
      await page.waitForFunction(() => !document.getElementById("step4").classList.contains("hidden"));
      assert.deepEqual(await heading(page), ["Play"]); assert.equal(await activeId(page), "h1-4");
      assert.deepEqual(await stepsState(page), ["done+btn", "done+btn", "done+btn", "on*"]);
      assert.match(await text(page, "#step4"), /Nothing has been installed yet\./);
    });
    await step("04: Enter right after arriving does nothing; Close asks the host to close the window; Esc goes back to the review", async () => {
      await page.keyboard.press("Enter"); await page.waitForTimeout(200);
      assert.ok(!/QUIT requested/.test(srv.log), "Enter quit the program immediately");
      await page.click("#close");
      await page.waitForFunction(() => true); await sleep(300);
      assert.match(srv.log, /QUIT requested by the page/);
      await page.keyboard.press("Escape");
      assert.equal(await visibleStep(page), 3); assert.deepEqual(await heading(page), ["Ready to install"], "a fresh review after a finished install");
    });
    await step("cancel: Cancel stops the job, returns to the review with 'Cancelled — nothing was changed', and the job can be started again", async () => {
      await startJob(page);
      assert.equal(await page.evaluate(() => document.getElementById("pbarwrap").classList.contains("done")), false, "a new job resets the done class");
      await page.waitForFunction(() => document.getElementById("pbar").getAttribute("aria-valuenow") > 5);
      const t0 = Date.now();
      await page.click("#cancel");
      await page.waitForFunction(() => !document.getElementById("rev4").classList.contains("hidden"));
      assert.ok(Date.now() - t0 < 2500, "cancel took " + (Date.now() - t0));
      assert.equal((await install(srv)).state, "cancelled");
      assert.equal(await text(page, "#cancelnote"), "Cancelled \u2014 nothing was changed.");
      assert.equal(await activeId(page), "h1-3"); assert.equal(await page.isVisible("#install"), true); assert.equal(await page.isVisible("#actionbar"), true);
      assert.equal(await page.locator("#install").getAttribute("aria-disabled"), "false");
      const ip = await install(srv); assert.ok(ip.doneBytes < ip.totalBytes);
      assert.deepEqual(await stepsState(page), ["done+btn", "done+btn", "on*", "future"]);
    });
    await step("cancel with Esc; Back and the step buttons do nothing while the job runs", async () => {
      await startJob(page);
      await page.waitForFunction(() => document.getElementById("pbar").getAttribute("aria-valuenow") > 3);
      await page.evaluate(() => { go(2); document.getElementById("back").click(); });   // both must be ignored
      assert.equal(await visibleStep(page), 3); assert.equal(await page.isVisible("#prog4"), true);
      assert.equal(await page.locator("#steps button").count(), 0);
      await page.keyboard.press("Escape");
      await page.waitForFunction(() => !document.getElementById("rev4").classList.contains("hidden"));
      assert.equal((await install(srv)).state, "cancelled");
      assert.equal(await page.isVisible("#cancelnote"), true);
      await page.click("#back"); assert.equal(await visibleStep(page), 2);
      await gotoStep(page, 3); assert.equal(await page.isVisible("#cancelnote"), false, "the note belongs to that attempt only");
    });
    await step("reload during the job: the page picks the running job up again; Esc then cancels it", async () => {
      await startJob(page);
      await page.waitForFunction(() => document.getElementById("pbar").getAttribute("aria-valuenow") > 3);
      await page.reload(); await page.waitForFunction(() => window.__fearState && document.getElementById("pcpath"));
      await page.waitForFunction(() => !document.getElementById("prog4").classList.contains("hidden"), null, { timeout: 8000 })
        .catch(async e => { throw new Error(e.message + " " + await page.evaluate(() => JSON.stringify({ st: S && S.install, job: ui.job, step: ui.step }))); });
      assert.equal(await visibleStep(page), 3); assert.equal((await dataOf(page)).h1, "Installing");
      await page.keyboard.press("Escape");
      await page.waitForFunction(() => !document.getElementById("rev4").classList.contains("hidden"));
      assert.equal((await install(srv)).state, "cancelled");
    });
    await step("no full path of the user's files was ever pushed in the install state; no page errors (install group)", async () => {
      const ip = JSON.stringify(await install(srv));
      assert.doesNotMatch(ip, /\/tmp\/|\/home\/|uitest|fix/);
      assert.deepEqual(h.errors, []);
    });
  });
  await step("closing: a job that is running when the program is told to stop ends cleanly (exit code 0, no panic or race text)", async () => {
    await withPage(srv, { width: 1366, height: 768 }, async page => {
      await gotoStep(page, 3);
      await startJob(page);
      await page.waitForFunction(() => document.getElementById("pbar").getAttribute("aria-valuenow") > 2);
    });
    const code = await new Promise(res => { srv.proc.once("exit", c => res(c)); srv.proc.kill("SIGTERM"); setTimeout(() => res("timeout"), 8000); });
    assert.equal(code, 0, "exit code " + code + "\n" + srv.log.slice(-600));
    assert.doesNotMatch(srv.log, /panic|DATA RACE|goroutine \d+/);
  });
});

// a copy that changes after it was verified: the job stops with a plain-language message and Back returns to the review
await withServer({ args: SLOW }, srv => withPage(srv, { width: 1366, height: 768 }, async (page, h) => {
  // a private copy of the PS3 fixture folder (symlinks stay symlinks; the damaged file is a new regular file, never written through a link)
  const copy = path.join(tmp, "ps3_changing"); fs.cpSync(F("ps3_disc_folder"), copy, { recursive: true, verbatimSymlinks: true });
  await step("error: a file that changed after the check stops the job with a plain message and Back returns to the review", async () => {
    await page.waitForFunction(() => document.getElementById("pcpath").value.includes("FEAR"));
    await gotoStep(page, 2);
    await browse(srv, page, "ps3", copy); await waitSrc(srv, page, "ps3", copy);
    await page.click("#continue"); await page.waitForTimeout(250);
    assert.equal(await visibleStep(page), 3);
    const f = path.join(copy, "PS3_GAME/USRDIR/game/minicatalog.cat");
    const data = fs.readFileSync(f); fs.rmSync(f); data[100] ^= 0xff; fs.writeFileSync(f, data);
    await page.click("#install");
    await page.waitForFunction(() => document.getElementById("h1-3p").textContent === "The installation stopped", null, { timeout: 60000 });
    const e = await text(page, "#errbox");
    assert.match(e, /minicatalog\.cat/); assert.match(e, /select the copy again/i);
    assert.doesNotMatch(e, /\/tmp|\/home|errno|panic|goroutine/i);
    assert.equal(await page.getAttribute("#errbox", "role"), "alert");
    assert.equal(await page.isVisible("#back"), true); assert.equal(await page.isVisible("#continue"), false); assert.equal(await page.isVisible("#pbarwrap"), false);
    assert.equal((await install(srv)).state, "error");
    await page.click("#back");
    assert.equal(await visibleStep(page), 3); assert.deepEqual(await heading(page), ["Ready to install"]);
    assert.equal(await page.isVisible("#install"), true);
    fs.rmSync(copy, { recursive: true, force: true });
  });
  await step("no page errors (error group)", async () => assert.deepEqual(h.errors, []));
}));

// ============================================================ screenshots (paths masked)
// The fixtures are renamed in the DOM to Windows-style example paths before every picture is taken, so no
// machine-specific path appears. A game folder with the real archives is used when the reference PC folder is available.
const pcRef = process.env.FEAR_REF_PCGAME;
let shotSteam = steam, shotGame = GAME;
if (pcRef && fs.existsSync(path.join(pcRef, "FEARE_8.Arch00"))) {
  shotSteam = path.join(tmp, "shotsteam"); shotGame = path.join(shotSteam, "steamapps/common/FEAR Ultimate Shooter Edition"); fs.mkdirSync(shotGame, { recursive: true });
  for (const n of ["FEAR.exe", "FEAR_8.Arch00", "FEARE_8.Arch00", "FEARL_8.Arch00", "dinput8.dll", "EchoPatch.ini"]) if (fs.existsSync(path.join(pcRef, n))) fs.symlinkSync(path.join(pcRef, n), path.join(shotGame, n));
  fs.writeFileSync(path.join(shotGame, "Default.archcfg"), ["FEAR.Arch00", "FEARA.Arch00", "FEARL.Arch00", "FEARE.Arch00", "FEAR_1.Arch00", "FEARA_1.Arch00", "FEARL_1.Arch00", "FEARE_1.Arch00", "FEAR_8.Arch00", "FEARA_8.Arch00", "FEARL_8.Arch00", "FEARE_8.Arch00"].join("\r\n"));
}
await withServer({ steam: shotSteam, args: SLOW }, async srv => {
  const maskOf = () => (page, extra = []) => page.evaluate(([game, root, src, extra]) => {
    const rep = s => extra.reduce((t, x) => t.split(x).join(game), s).split(game).join("C:\\Games\\F.E.A.R.").replace(/C:\\Games\\F\.E\.A\.R\.([^\s]*)/g, (m, b) => "C:\\Games\\F.E.A.R." + b.replace(/\//g, "\\"))
      .split(src).join("D:\\Backups").replace(/D:\\Backups([^\s]*)/g, (m, b) => "D:\\Backups" + b.replace(/\//g, "\\"));
    const w = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT); let n;
    while ((n = w.nextNode())) { if (n.parentElement.closest("pre,script")) continue; const t = rep(n.textContent); if (t !== n.textContent) n.textContent = t; }
    const i = document.getElementById("pcpath"); i.value = rep(i.value);
    document.querySelectorAll("[title]").forEach(e => { e.title = rep(e.title); });
  }, [shotGame, shotSteam, fix, extra]);
  const mask = maskOf();
  const leak = async page => { const t = await page.evaluate(() => document.body.innerText + "\n" + document.getElementById("pcpath").value + "\n" + [...document.querySelectorAll("[title]")].map(e => "[" + e.id + "] " + e.title).join("\n")); assert.doesNotMatch(t, /\/tmp\/|\/home\/|\/mnt\/|uitest-|scratchpad|claude/i, "a machine-specific path is visible: " + (t.match(/.{0,80}(\/tmp\/|\/home\/|\/mnt\/|uitest-|scratchpad|claude).{0,160}/i) || [""])[0]); };
  const shoot = async (page, name, vw, vh) => { await mask(page); await leak(page); await page.screenshot({ path: path.join(shots, `${name}_${vw}x${vh}.png`) }); };
  let copiesReady = false;
  for (const [vw, vh] of [[1366, 768], [2000, 1773]]) {
    await withPage(srv, { width: vw, height: vh }, async (page, h) => {
      await step(`screenshots: every step at ${vw}x${vh} (01 game, 02 console content, 03 review, 03 installing, 03 done, 04 play; machine-specific paths masked)`, async () => {
        await page.waitForFunction(() => document.getElementById("pcpath").value.includes("FEAR"));
        await shoot(page, "01_game", vw, vh);
        await page.click("#continue"); await page.waitForFunction(() => !document.getElementById("step2").classList.contains("hidden"));
        if (!copiesReady) {
          await browse(srv, page, "ps3", F("ps3_disc_folder")); await waitSrc(srv, page, "ps3", F("ps3_disc_folder"));
          await browse(srv, page, "x360", F("x360_folder")); await waitSrc(srv, page, "x360", F("x360_folder"));
          copiesReady = true;
        }
        await page.waitForFunction(() => document.getElementById("continue").getAttribute("aria-disabled") !== "true");
        await shoot(page, "02_console_content", vw, vh);
        await page.click("#continue"); await page.waitForFunction(() => !document.getElementById("step3").classList.contains("hidden"));
        await page.waitForFunction(() => document.getElementById("modloc").textContent.endsWith("FEAR_ConsoleRestoration"));
        await shoot(page, "03_review", vw, vh);
        await page.click("#install");
        await page.waitForFunction(() => { const n = +document.getElementById("pbar").getAttribute("aria-valuenow"); return n >= 38 && n <= 70; }, null, { timeout: 60000, polling: 40 });
        await shoot(page, "03_installing", vw, vh);
        await page.waitForFunction(() => document.getElementById("h1-3p").textContent === "Done", null, { timeout: 60000 });
        await shoot(page, "03_done", vw, vh);
        await page.click("#continue"); await page.waitForFunction(() => !document.getElementById("step4").classList.contains("hidden"));
        await shoot(page, "04_play", vw, vh);
      });
      await step(`screenshots: step 02 with one console copy missing, and a blocked step 01, at ${vw}x${vh}`, async () => {
        await gotoStep(page, 2);
        await page.click("#clear-x360"); await page.waitForFunction(() => /No folder selected/.test(document.getElementById("status-x360").textContent));
        await shoot(page, "02_console_content_one_copy", vw, vh);
        await browse(srv, page, "x360", F("x360_folder")); await waitSrc(srv, page, "x360", F("x360_folder"));
        await gotoStep(page, 1);
        await page.fill("#pcpath", G("pc_foreign")); await page.press("#pcpath", "Enter");
        await page.waitForFunction(() => /not EchoPatch/.test(document.getElementById("blockers").textContent));
        await mask(page, [G("pc_foreign")]); await leak(page);
        await page.screenshot({ path: path.join(shots, `01_game_blocked_${vw}x${vh}.png`) });
      });
      await step(`no page errors (screenshots ${vw}x${vh})`, async () => assert.deepEqual(h.errors, []));
    });
  }
});

await browser.close();
const { passed, failed, fails } = R.summary();
console.log(failed ? `${failed} UI test(s) FAILED: ${fails.join(" | ")}` : `all ${passed} UI tests passed`);
fs.rmSync(tmp, { recursive: true, force: true });
process.exit(failed ? 1 : 0);
