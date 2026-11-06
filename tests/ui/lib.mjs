// Shared helpers for the UI tests: start the preview server (same Go back end as the
// installer), open pages, and a few DOM probes. Nothing here touches the user's files.
// Playwright is resolved from PLAYWRIGHT_MODULE (path to its index.mjs) or, failing that, as the "playwright" package.
const { chromium } = await import(process.env.PLAYWRIGHT_MODULE || "playwright");
import { spawn } from "node:child_process";
import net from "node:net";
import http from "node:http";
import fs from "node:fs";

export { chromium };
export const sleep = ms => new Promise(r => setTimeout(r, ms));

function freePort() {
  return new Promise((res, rej) => {
    const s = net.createServer(); s.listen(0, "127.0.0.1", () => { const p = s.address().port; s.close(() => res(p)); }); s.on("error", rej);
  });
}

// startServer({bin, steam, dev, args}) -> {base, port, token, kill(), log}
// args: extra command-line flags, e.g. ["-slow-install", "4ms"] (pause after every MiB the install job reads)
export async function startServer({ bin, steam = "", dev = true, addr = "", env = {}, args: extra = [] }) {
  const port = await freePort();
  const a = addr || `127.0.0.1:${port}`;
  const args = ["-addr", a]; if (steam) args.push("-steam", steam); if (dev) args.push("-dev"); args.push(...extra);
  const p = spawn(bin, args, { env: { ...process.env, ...env } });
  let log = ""; p.stdout.on("data", d => log += d); p.stderr.on("data", d => log += d);
  const srv = { port, base: `http://${a}`, addr: a, proc: p, get log() { return log; }, token: "", kill() { p.kill(); } };
  for (let i = 0; i < 100; i++) {
    const m = /DEV_TOKEN=(\w+)/.exec(log);
    if (m) srv.token = m[1];
    if (log.includes("UI preview on")) break;
    if (p.exitCode !== null) break;
    await sleep(50);
  }
  return srv;
}

export function rawRequest(srv, { path, method = "GET", headers = {} }) {
  return new Promise((res, rej) => {
    const [host, port] = srv.addr.split(":");
    const r = http.request({ host, port, path, method, headers }, resp => { resp.resume(); resp.on("end", () => res(resp.statusCode)); });
    r.on("error", rej); r.end();
  });
}

export async function newPage(browser, srv, viewport = { width: 1280, height: 900 }, opts = {}) {
  const ctx = await browser.newContext({ viewport, deviceScaleFactor: opts.dsf || 1 });
  const page = await ctx.newPage();
  const errors = [];
  page.on("pageerror", e => errors.push(String(e)));
  page.on("console", m => { if (m.type() === "error") errors.push(m.text()); });
  await page.goto(srv.base + "/");
  await page.waitForFunction(() => document.getElementById("pcpath") && window.__fearState);
  await page.waitForTimeout(300);
  return { page, ctx, errors };
}

export const pick = (srv, p) => fetch(`${srv.base}/dev/pick?path=${encodeURIComponent(p)}`, { method: "POST", headers: { "X-Dev-Token": srv.token } });
export async function state(srv) { return (await fetch(srv.base + "/rpc/snapshot", { method: "POST", body: "[]" })).json(); }

export async function waitSrc(srv, page, kind, p, timeout = 90000) {
  const t = Date.now();
  for (;;) {
    const k = (await state(srv))[kind];
    if (k.path === p && k.phase === "done") { await page.waitForTimeout(250); return k; }
    if (Date.now() - t > timeout) throw new Error(`timeout waiting for ${kind} ${p}: ${JSON.stringify(k)}`);
    await sleep(40);
  }
}
export async function browse(srv, page, kind, p) {
  await pick(srv, p);
  if (kind === "pc") await page.click("#pcbrowse");
  else await page.click(`#browse-${kind}`);         // the page only ever browses for a folder
}
export const text = (page, sel) => page.locator(sel).innerText();
export const contEnabled = page => page.evaluate(() => document.getElementById("continue").getAttribute("aria-disabled") !== "true");
export const visibleStep = page => page.evaluate(() => [1, 2, 3, 4, 5].find(n => !document.getElementById("step" + n).classList.contains("hidden")));
// show a step directly (the page's own go(); no gating) - for layout and rendering checks
export const gotoStep = async (page, n) => { await page.evaluate(k => go(k), n); await page.waitForTimeout(60); };
export const install = async srv => (await state(srv)).install;
// the install job is done / in a given state
export async function waitInstall(srv, want, timeout = 90000) {
  const t = Date.now();
  for (;;) {
    const ip = (await state(srv)).install;
    if (ip.state === want) return ip;
    if (Date.now() - t > timeout) throw new Error(`timeout waiting for install state ${want}: ${JSON.stringify(ip)}`);
    await sleep(30);
  }
}
// does the page need to scroll (inside its content area or as a whole)?
export const scrollMetrics = page => page.evaluate(() => {
  const sc = document.getElementById("scroll"), ab = document.getElementById("actionbar");
  return { sh: sc.scrollHeight, ch: sc.clientHeight, sw: sc.scrollWidth, cw: sc.clientWidth, docW: document.documentElement.scrollWidth, docH: document.documentElement.scrollHeight, iw: innerWidth, ih: innerHeight,
    barBottom: ab.classList.contains("hidden") ? null : ab.getBoundingClientRect().bottom };
});

export function makeRunner() {
  let failed = 0, passed = 0; const fails = [];
  const step = async (name, fn) => {
    try { await fn(); passed++; console.log("PASS", name); }
    catch (e) { failed++; fails.push(name); console.log("FAIL", name, "\n   ", String(e.message).split("\n").slice(0, 8).join("\n    ")); }
  };
  return { step, summary: () => ({ failed, passed, fails }) };
}

// Contrast probe: runs in the page, returns elements whose text contrast is below `min`.
export const contrastProbe = (min) => {
  const parse = c => { const m = c.match(/rgba?\(([^)]+)\)/); if (!m) return [0, 0, 0, 0]; const v = m[1].split(/[ ,\/]+/).filter(Boolean).map(Number); return [v[0], v[1], v[2], v.length > 3 ? v[3] : 1]; };
  const over = (f, b) => { const a = f[3]; return [f[0] * a + b[0] * (1 - a), f[1] * a + b[1] * (1 - a), f[2] * a + b[2] * (1 - a), 1]; };
  const lum = c => { const f = v => { v /= 255; return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4); }; return 0.2126 * f(c[0]) + 0.7152 * f(c[1]) + 0.0722 * f(c[2]); };
  const ratio = (a, b) => { const x = lum(a), y = lum(b); return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05); };
  const bgOf = el => {
    const layers = [];
    for (let e = el; e; e = e.parentElement) { const c = parse(getComputedStyle(e).backgroundColor); if (c[3] > 0) { layers.push(c); if (c[3] >= 0.999) break; } }
    let base = [8, 10, 11, 1];
    const b = parse(getComputedStyle(document.body).backgroundColor); if (b[3] > 0.99) base = b;
    for (let i = layers.length - 1; i >= 0; i--) base = over(layers[i], base);
    return base;
  };
  const opacityOf = el => { let o = 1; for (let e = el; e; e = e.parentElement) o *= parseFloat(getComputedStyle(e).opacity); return o; };
  const bad = [];
  const all = [...document.querySelectorAll("body *")];
  for (const el of all) {
    const own = [...el.childNodes].some(n => n.nodeType === 3 && n.textContent.trim());
    if (!own) continue;
    if (el.closest("[aria-hidden='true'], .hidden, .sr, script, style, dialog:not([open]), .menu:not(.open)")) continue;
    const r = el.getBoundingClientRect(); if (r.width < 1 || r.height < 1) continue;
    const cs = getComputedStyle(el); if (cs.visibility === "hidden" || cs.display === "none") continue;
    // A gradient background (the gold buttons) is judged by its worst colour stop.
    let bgs = [bgOf(el)];
    for (let e = el; e; e = e.parentElement) {
      const bi = getComputedStyle(e).backgroundImage;
      if (bi && bi.includes("gradient")) { bgs = (bi.match(/rgba?\([^)]+\)/g) || []).map(c => over(parse(c), [8, 10, 11, 1])); break; }
      if (parse(getComputedStyle(e).backgroundColor)[3] >= 0.999) break;
    }
    const fgRaw = parse(cs.color); fgRaw[3] *= opacityOf(el);
    const rt = Math.min(...bgs.map(bg => ratio(over(fgRaw, bg), bg)));
    if (rt < min) bad.push({ el: (el.id ? "#" + el.id : el.tagName + "." + el.className), text: el.textContent.trim().slice(0, 40), ratio: Math.round(rt * 100) / 100 });
  }
  return bad;
};
