// GOG / dual-store UI tests: the real index.html in headless Chromium against the real Go back end
// (cmd/uipreview). Self-contained: the fake Steam and GOG folders are built here, no game data is needed.
//
//   node tests/ui/gog_test.mjs <uipreview binary> <screenshots dir>
import path from "node:path";
import fs from "node:fs";
import os from "node:os";
import assert from "node:assert/strict";
import { chromium, startServer, newPage, text, contEnabled, makeRunner } from "./lib.mjs";

const [bin, shots] = process.argv.slice(2);
if (!bin || !shots) { console.error("usage: gog_test.mjs <uipreview> <shots>"); process.exit(2); }
fs.mkdirSync(shots, { recursive: true });
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "gogtest-"));
const R = makeRunner(); const step = R.step;

const w = (p, c = "x") => { fs.mkdirSync(path.dirname(p), { recursive: true }); fs.writeFileSync(p, c); };

// a minimal LTAR v3 archive (stored entries, one root directory), the same layout the Go reader parses
function ltar(file, entries) {
  const names = Object.keys(entries).sort();
  let str = Buffer.from([0]); const ofs = {};
  for (const n of names) { ofs[n] = str.length; str = Buffer.concat([str, Buffer.from(n + "\0")]); }
  const hdr = 0x30 + str.length + names.length * 32 + 16;
  const ent = Buffer.alloc(names.length * 32); let off = hdr; const bodies = [];
  names.forEach((n, i) => {
    const b = Buffer.from(entries[n]); ent.writeUInt32LE(ofs[n], i * 32); ent.writeBigUInt64LE(BigInt(off), i * 32 + 4);
    ent.writeBigUInt64LE(BigInt(b.length), i * 32 + 12); ent.writeBigUInt64LE(BigInt(b.length), i * 32 + 20); off += b.length; bodies.push(b);
  });
  const h = Buffer.alloc(0x30); h.write("LTAR"); h.writeUInt32LE(3, 4); h.writeUInt32LE(str.length, 8); h.writeUInt32LE(1, 12); h.writeUInt32LE(names.length, 16);
  const d = Buffer.alloc(16); d.writeUInt32LE(names.length, 12);
  fs.mkdirSync(path.dirname(file), { recursive: true });
  fs.writeFileSync(file, Buffer.concat([h, str, ent, d, ...bodies]));
}

const STOCK = []; for (const b of ["FEAR", "FEARA", "FEARL", "FEARE"]) STOCK.push(b + ".Arch00");
for (let i = 1; i <= 8; i++) for (const b of ["FEAR", "FEARA", "FEARL", "FEARE"]) STOCK.push(`${b}_${i}.Arch00`);
const stockCfg = STOCK.join("\r\n");                     // 557 bytes, no final break: the Steam file and, by size, the GOG file

// GOG layout from the depot's file list (names only)
function gogGame(dir, { echo = false, cfg = stockCfg, gameClient = "different build" } = {}) {
  for (const f of ["FEAR.exe", "FEAR.Arch00", "FEAR_8.Arch00", "FEARL_8.Arch00", "d3d9.dll", "dxwrapper.dll", "FEARXP/FEARXP.exe", "FEARXP2/FEARXP2.exe"]) w(path.join(dir, f), "MZ");
  w(path.join(dir, "dxwrapper.ini"), "[d3d9]\nLimitPerFrameFPS = 60\n");
  w(path.join(dir, "dinput8.dll"), echo ? "MZ EchoPatch" : "MZ G\0O\0G\0.\0c\0o\0m\0 \0I\0n\0p\0u\0t\0 \0w\0r\0a\0p\0p\0e\0r\0");
  if (cfg !== null) w(path.join(dir, "Default.archcfg"), cfg);
  if (gameClient !== null) ltar(path.join(dir, "FEARE_8.Arch00"), { "GameClient.dll": gameClient });
  return dir;
}
const steamRoot = path.join(tmp, "Steam");
const steamGame = path.join(steamRoot, "steamapps/common/FEAR Ultimate Shooter Edition");
for (const f of ["FEAR.exe", "FEAR.Arch00", "FEAR_8.Arch00"]) w(path.join(steamGame, f), "MZ");
w(path.join(steamGame, "dinput8.dll"), "MZ EchoPatch");
w(path.join(steamGame, "Default.archcfg"), stockCfg);
const gogRoot = path.join(tmp, "GOG Games");
const gog = gogGame(path.join(gogRoot, "F.E.A.R. Platinum"));
const gogOnly = path.join(tmp, "gog-only"); gogGame(path.join(gogOnly, "F.E.A.R. Platinum Collection"), { echo: true, gameClient: null });

const browser = await chromium.launch();
const open = async (srvOpts, fn, viewport = { width: 1280, height: 800 }) => {
  const srv = await startServer({ bin, ...srvOpts });
  try { const h = await newPage(browser, srv, viewport); try { return await fn(h.page, srv, h); } finally { await h.ctx.close(); } } finally { srv.kill(); }
};
const hint = p => text(p, "#pchint");
// The plan exactly as the page asks for it (the dry run of the install): same RPC, same option shape as options() in index.html.
const planOf = page => page.evaluate(() => window.be_plan({ usePs3: false, useX360: false }));

// ---------------------------------------------------------------- GOG only
await open({ steam: "", env: { FEAR_GOG_ROOT: gogRoot } }, async (page, srv, h) => {
  await step("GOG only: the installation is found and selected without a selector", async () => {
    await page.waitForFunction(() => document.getElementById("pcpath").value.includes("F.E.A.R. Platinum"));
    assert.equal(await page.evaluate(() => document.getElementById("pcchoices").classList.contains("hidden")), true);
    assert.match(await hint(page), /F\.E\.A\.R\. found \(GOG version\)/);
  });
  await step("GOG: GOG's own dinput8.dll (input wrapper) is explained, not called 'another mod', and blocks CONTINUE with the right reason", async () => {
    const echo = await text(page, "#echo");
    assert.match(echo, /EchoPatch is required/); assert.match(echo, /GOG\.com Input wrapper/); assert.match(echo, /choose Replace/); assert.doesNotMatch(echo, /another mod/);
    assert.equal(await contEnabled(page), false);
    assert.match(await text(page, "#blockers"), /GOG's own input wrapper, not EchoPatch/);
  });
  await step("untested game version: a clear warning, the install is not blocked by it", async () => {
    const t = await text(page, "#pcwarn");
    assert.match(t, /This F\.E\.A\.R\. version has not been tested; the objective display and Bonus mission menu may switch themselves off/);
    assert.equal(await page.evaluate(() => document.getElementById("pchint").className.includes("warn")), true);
    assert.doesNotMatch(await text(page, "#blockers"), /tested/);
  });
  await step("EchoPatch installed next to dxwrapper is accepted (Verify), text says so", async () => {
    w(path.join(gog, "dinput8.dll"), "MZ EchoPatch");
    await page.click("#recheck");
    await page.waitForFunction(() => /EchoPatch by Wemino \(GitHub\) found/.test(document.getElementById("echo").textContent));
    assert.match(await text(page, "#echo"), /EchoPatch by Wemino \(GitHub\) found\./);
    await page.waitForFunction(() => document.getElementById("continue").getAttribute("aria-disabled") === "false");
    assert.equal(await contEnabled(page), true, "step 01 only needs a valid folder with EchoPatch; the copies come in step 02 (console content)");
    assert.equal(await text(page, "#blockers"), "");
    w(path.join(gog, "dinput8.dll"), "MZ stub for dxwrapper.dll");
  });
  await step("an expansion sub-folder is refused with a pointer to the main folder", async () => {
    await page.fill("#pcpath", path.join(gog, "FEARXP")); await page.press("#pcpath", "Enter");
    await page.waitForFunction(() => /expansion folder/.test(document.getElementById("pchint").textContent));
    assert.ok((await hint(page)).includes(gog));
    assert.equal(await contEnabled(page), false);
  });
  await step("the page has no support box or licence UI, and the removed back-end calls are not bound", async () => {
    await page.fill("#pcpath", gog); await page.press("#pcpath", "Enter");
    await page.waitForFunction(g => document.getElementById("pcpath").value === g, gog);
    await page.waitForTimeout(250);
    assert.equal(await page.evaluate(() => ["diagrow", "showdiag", "diag", "diagtext", "lic"].some(id => document.getElementById(id))), false, "the removed support/licence UI is gone");
    assert.equal(await page.evaluate(() => ["be_diag", "be_licenses", "be_art"].some(n => n in window)), false, "the unused back-end calls are gone");
  });
  await step("step 03 review dry run: the 557-byte Default.archcfg gets the mod folder as the last line, a note about the missing final line break (plan RPC)", async () => {
    const p = await planOf(page), a = p.archcfg;
    assert.ok(a, "the plan carries the archcfg dry run");
    assert.equal(a.error, ""); assert.equal(a.exists, true); assert.equal(a.changed, true);
    assert.equal(a.added, "FEAR_ConsoleRestoration");
    assert.equal(a.tail[a.tail.length - 1], "FEAR_ConsoleRestoration", "the added entry is last");
    assert.ok(a.tail.includes("FEARE_8.Arch00"), "the stock mounts stay above it");
    assert.equal(a.encoding, "UTF-8"); assert.equal(a.lineEnding, "CRLF"); assert.equal(a.entries, 36);
    assert.ok(a.notes.some(n => /no line break at the end/.test(n)), JSON.stringify(a.notes));
    assert.equal(a.summary, "Add FEAR_ConsoleRestoration as the last mount.");
    assert.ok(p.changes.some(c => /Back up Default\.archcfg/.test(c)), "the backup is planned");
    assert.ok(p.warnings.some(c => /has not been tested/.test(c)), JSON.stringify(p.warnings));
    await page.evaluate(() => go(3));
    await page.waitForFunction(() => /has not been tested/.test(document.getElementById("planwarn").textContent));
    assert.equal(await page.evaluate(() => ["cfgsec", "cfgmeta", "cfgsum", "cfgtail", "cfgnotes", "changes", "reads"].some(id => document.getElementById(id))), false, "the review no longer shows the archcfg box, NOTES or Read from");
    await page.screenshot({ path: path.join(shots, "gog_step3_dryrun.png") });
    await page.evaluate(() => go(1));
  });
  await step("no page errors (GOG only)", async () => assert.deepEqual(h.errors, []));
});

// ---------------------------------------------------------------- both stores
await open({ steam: steamRoot, env: { FEAR_GOG_ROOT: gogRoot } }, async (page, srv, h) => {
  await step("Steam and GOG both present: both are listed under the path box and none is chosen for the user", async () => {
    await page.waitForFunction(() => document.querySelectorAll("#pcopts .choice").length === 2);
    assert.equal(await page.inputValue("#pcpath"), "");
    const labels = await page.$$eval("#pcopts .choice", els => els.map(e => [e.querySelector("b").textContent, e.querySelector("span").textContent, e.getAttribute("aria-checked")]));
    assert.equal(labels[0][0], "STEAM"); assert.equal(labels[1][0], "GOG");
    assert.ok(labels[0][1].endsWith("FEAR Ultimate Shooter Edition")); assert.ok(labels[1][1].endsWith("F.E.A.R. Platinum"));
    assert.deepEqual([labels[0][2], labels[1][2]], ["false", "false"]);
    assert.match(await hint(page), /Found 2 F\.E\.A\.R\. installations/);
    assert.equal(await contEnabled(page), false);
    assert.match(await text(page, "#blockers"), /Choose which F\.E\.A\.R\. installation to use/);
    await page.screenshot({ path: path.join(shots, "gog_both_found.png") });
  });
  await step("choosing GOG, then Steam, switches the folder, the edition text and the selected state", async () => {
    await page.click("#pcopts .choice:nth-child(2)");
    await page.waitForFunction(() => document.getElementById("pcpath").value.includes("F.E.A.R. Platinum"));
    assert.match(await hint(page), /GOG version/);
    assert.equal(await page.getAttribute("#pcopts .choice:nth-child(2)", "aria-checked"), "true");
    assert.equal(await page.getAttribute("#pcopts .choice:nth-child(1)", "aria-checked"), "false");
    await page.click("#pcopts .choice:nth-child(1)");
    await page.waitForFunction(() => document.getElementById("pcpath").value.includes("FEAR Ultimate Shooter Edition"));
    assert.match(await hint(page), /Steam version/);
    assert.equal(await page.getAttribute("#pcopts .choice:nth-child(1)", "aria-checked"), "true");
    assert.equal(await page.evaluate(() => document.getElementById("pcwarn").classList.contains("hidden")), true, "no warning for the Steam fixture (no FEARE_8, nothing to compare)");
    assert.match(await text(page, "#echo"), /EchoPatch by Wemino \(GitHub\) found/);
  });
  await step("the choice survives 'Verify' and a typed path that equals a listed one", async () => {
    await page.click("#recheck"); await page.waitForTimeout(400);
    assert.equal(await page.getAttribute("#pcopts .choice:nth-child(1)", "aria-checked"), "true");
    await page.fill("#pcpath", gog); await page.press("#pcpath", "Enter");
    await page.waitForFunction(() => document.querySelector("#pcopts .choice:nth-child(2)").getAttribute("aria-checked") === "true");
  });
  await step("no page errors (both stores)", async () => assert.deepEqual(h.errors, []));
});

// ---------------------------------------------------------------- Default.archcfg variants in the dry run
// The review no longer prints the archcfg box; the dry run is checked on the plan the back end returns for the page
// (plan.archcfg), and an unusable file on what the user sees: the blocker in the action bar.
async function dryRun(page, dir) {
  await page.fill("#pcpath", dir); await page.press("#pcpath", "Enter");
  await page.waitForFunction(d => document.getElementById("pcpath").value === d, dir);
  await page.waitForTimeout(250);
  const plan = await planOf(page);
  await page.evaluate(() => go(3));
  await page.waitForTimeout(150);
  const r = { plan, a: plan.archcfg, blockers: await text(page, "#blockers"), cont: await contEnabled(page),
    inst: await page.evaluate(() => document.getElementById("install").getAttribute("aria-disabled") === "true" || document.getElementById("install").disabled === true) };
  await page.evaluate(() => go(1));
  return r;
}
const lastIs = (a, s) => assert.equal(a.tail[a.tail.length - 1], s);
await open({ steam: "" }, async (page, srv, h) => {
  const variants = {
    glued: { cfg: STOCK.slice(0, 35).join("\r\n") + "\r\nFEARE_8.Arch00ButtonMod\r\nButtonMod\r\n", check: r => { assert.ok(r.a.notes.some(n => /Split the joined line "FEARE_8\.Arch00ButtonMod"/.test(n)), JSON.stringify(r.a.notes)); assert.equal(r.a.tail.at(-2), "ButtonMod"); lastIs(r.a, "FEAR_ConsoleRestoration"); } },
    bom16: { cfg: Buffer.concat([Buffer.from([0xFF, 0xFE]), Buffer.from(stockCfg + "\r\n", "utf16le")]), check: r => { assert.match(r.a.encoding, /UTF-16 LE/); assert.equal(r.a.added, "FEAR_ConsoleRestoration"); lastIs(r.a, "FEAR_ConsoleRestoration"); } },
    bom8: { cfg: Buffer.concat([Buffer.from([0xEF, 0xBB, 0xBF]), Buffer.from(stockCfg + "\r\n")]), check: r => { assert.match(r.a.encoding, /UTF-8 with BOM/); lastIs(r.a, "FEAR_ConsoleRestoration"); } },
    lf: { cfg: STOCK.join("\n") + "\n", check: r => { assert.equal(r.a.encoding, "UTF-8"); assert.equal(r.a.lineEnding, "LF"); lastIs(r.a, "FEAR_ConsoleRestoration"); } },
    extra: { cfg: "; my mods\r\n" + stockCfg + "\r\nSomeOtherMod\r\n", check: r => { assert.equal(r.a.tail.at(-2), "SomeOtherMod"); lastIs(r.a, "FEAR_ConsoleRestoration"); assert.equal(r.a.added, "FEAR_ConsoleRestoration"); } },
    already: { cfg: stockCfg + "\r\nFEAR_ConsoleRestoration\r\n", check: r => { assert.match(r.a.summary, /already the last mount/); assert.equal(r.a.changed, false); assert.equal(r.a.added, "", "nothing is marked as added when it is already there"); } },
    missing: { cfg: null, check: r => { assert.match(r.a.summary, /missing/); assert.equal(r.a.exists, false); assert.equal(r.a.error, ""); assert.equal(r.a.added, "FEAR_ConsoleRestoration"); lastIs(r.a, "FEAR_ConsoleRestoration"); } },
    binary: { cfg: Buffer.from([0x4d, 0x5a, 0, 1, 2, 0, 0, 9, 9]), check: r => { assert.equal(r.a.summary, "Default.archcfg cannot be used."); assert.match(r.a.error, /not a text file/); assert.ok(r.plan.blockers.some(b => /Default\.archcfg in the game folder cannot be used/.test(b)), JSON.stringify(r.plan.blockers)); assert.match(r.blockers, /Default\.archcfg in the game folder cannot be used/, "the blocker is shown in the action bar"); assert.equal(r.inst, true, "INSTALL stays disabled on the review"); } },
  };
  for (const [name, v] of Object.entries(variants)) {
    await step(`dry run, Default.archcfg ${name}`, async () => {
      const d = gogGame(path.join(tmp, "var-" + name, "F.E.A.R. Platinum"), { echo: true, cfg: null, gameClient: null });
      if (v.cfg !== null) w(path.join(d, "Default.archcfg"), v.cfg);
      const r = await dryRun(page, d);
      v.check(r);
      if (name !== "binary") assert.doesNotMatch(r.blockers, /archcfg/i, "a usable (or missing) file never shows an archcfg blocker");
      else assert.ok(r.plan.gates.install.some(b => /Default\.archcfg/.test(b)), "the blocker belongs to the install gate");
    });
  }
  await step("dry run: a read-only Default.archcfg is noted, not an obstacle", async () => {
    const d = gogGame(path.join(tmp, "var-ro", "F.E.A.R. Platinum"), { echo: true, gameClient: null });
    fs.chmodSync(path.join(d, "Default.archcfg"), 0o444);
    const r = await dryRun(page, d);
    assert.equal(r.a.readOnly, true); assert.ok(r.a.notes.some(n => /read-only; the installer clears that/.test(n)), JSON.stringify(r.a.notes));
    assert.equal(r.a.error, "", "a read-only file is noted, not an obstacle"); assert.ok(!r.plan.blockers.some(b => /archcfg/i.test(b)));
    assert.equal(fs.readFileSync(path.join(d, "Default.archcfg"), "utf8"), stockCfg, "the preview never writes");
  });
  await step("dry run: another mod's dinput8_hook.dll is announced and its backup planned", async () => {
    const d = gogGame(path.join(tmp, "var-hook", "F.E.A.R. Platinum"), { echo: true, gameClient: null });
    w(path.join(d, "dinput8_hook.dll"), "MZ somebody else's chain");
    await page.fill("#pcpath", d); await page.press("#pcpath", "Enter");
    await page.waitForFunction(() => /does not come from this mod/.test(document.getElementById("pcwarn").textContent));
    await page.evaluate(() => go(3));
    const p = await planOf(page);
    assert.ok(p.changes.some(c => /dinput8_hook\.dll\.CR_backup/.test(c)), JSON.stringify(p.changes));
    assert.ok(p.warnings.some(c => /does not come from this mod/.test(c)), JSON.stringify(p.warnings));
  });
  await step("no page errors (archcfg variants)", async () => assert.deepEqual(h.errors, []));
});

await browser.close();
const { passed, failed, fails } = R.summary();
console.log(failed ? `${failed} GOG UI test(s) FAILED: ${fails.join(" | ")}` : `all ${passed} GOG UI tests passed`);
fs.rmSync(tmp, { recursive: true, force: true });
process.exit(failed ? 1 : 0);
