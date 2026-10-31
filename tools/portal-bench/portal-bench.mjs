#!/usr/bin/env node
// ISUCON portal benchmark driver. login / run [--label T] / status [job]
import { chromium } from 'playwright';
import os from 'node:os';
import path from 'node:path';
import fs from 'node:fs';

// ---- 調整用セレクタ・定数 (UIが違えばここだけ直す) ----
const BASE = 'https://portal.isucon.net';
const JOBS_URL = `${BASE}/contestant/benchmark_jobs`;
const JOB_URL = (id) => `${JOBS_URL}/${id}`;
const JOB_LINK = 'a[href*="/contestant/benchmark_jobs/"]';       // 一覧内のジョブ詳細リンク
const JOB_ID_RE = /\/benchmark_jobs\/(\d+)/;
const REQUEST_BUTTON = { role: 'button', name: /request|enqueue|ベンチマーク.*(実行|依頼)|(実行|依頼)/i }; // 見つからなければlinkも試す
const RESULT_JSON = 'pre';                                  // 結果JSONを含む要素
const BUSY_RE = /\b(WAITING|PENDING|RUNNING)\b/;
const POLL_MS = 5000, TIMEOUT_MS = 5 * 60 * 1000;
// -------------------------------------------------------
const PROFILE = path.join(os.homedir(), '.config/isu-portal/profile');
const COOKIE_FILE = path.join(os.homedir(), '.config/isu-portal/portal-cookies.json');
const [cmd, ...rest] = process.argv.slice(2);

const die = (code, msg) => { console.error(msg); process.exit(code); };

async function open(headless) {
  fs.mkdirSync(PROFILE, { recursive: true });
  const ctx = await chromium.launchPersistentContext(PROFILE, { headless });
  // ポータルのCookieはsession cookieで終了時に消える。再OAuthが続くとGitHubに止められるので保存する。
  if (fs.existsSync(COOKIE_FILE)) await ctx.addCookies(JSON.parse(fs.readFileSync(COOKIE_FILE, 'utf8')));
  const close = ctx.close.bind(ctx);
  ctx.close = async () => {
    const cookies = await ctx.cookies(BASE).catch(() => []);
    if (cookies.length) fs.writeFileSync(COOKIE_FILE, JSON.stringify(cookies.map((c) => ({ ...c, expires: Math.floor(Date.now() / 1000) + 86400 }))), { mode: 0o600 });
    await close();
  };
  const page = ctx.pages()[0] ?? await ctx.newPage();
  return { ctx, page };
}
const onPortal = (p) => new URL(p.url()).origin === BASE && /\/contestant/.test(p.url());

async function mismatch(ctx, page, why) {
  const t = Date.now();
  const png = `/tmp/portal-bench-${t}.png`, html = `/tmp/portal-bench-${t}.html`;
  try { await page.screenshot({ path: png, fullPage: true }); fs.writeFileSync(html, await page.content()); } catch {}
  await ctx.close();
  die(2, `UI mismatch: ${why}\n  screenshot: ${png}\n  html: ${html}\n  セレクタは portal-bench.mjs 冒頭を修正`);
}

async function gotoJobs(ctx, page) {
  await page.goto(JOBS_URL, { waitUntil: 'domcontentloaded' });
  await page.waitForLoadState('networkidle').catch(() => {});
  // ポータルのセッション切れはGitHub経由で自動再認証される。戻るまで待つ。
  if (!onPortal(page)) await page.waitForURL((u) => u.origin === BASE && /\/contestant/.test(u.pathname), { timeout: 20000 }).catch(() => {});
  if (!onPortal(page)) { await ctx.close(); die(2, `未ログイン: まず \`node portal-bench.mjs login\` を人間が実行してください (現在URL: ${page.url()})`); }
}

async function jobIds(page) {
  const hrefs = await page.$$eval(JOB_LINK, (as) => as.map((a) => a.getAttribute('href')));
  return [...new Set(hrefs.map((h) => JOB_ID_RE.exec(h)?.[1]).filter(Boolean).map(Number))].sort((a, b) => b - a);
}

async function readJob(page, id) {
  await page.goto(JOB_URL(id), { waitUntil: 'domcontentloaded' });
  await page.waitForLoadState('networkidle').catch(() => {});
  const text = await page.innerText('body');
  const status = (/Status:\s*(WAITING|PENDING|RUNNING|FINISHED|ERRORED|ABORTED|FAILED|CANCELED|CANCELLED)\b/.exec(text) || [])[1] ?? null;
  let res = null;
  for (const t of await page.$$eval(RESULT_JSON, (els) => els.map((e) => e.textContent))) {
    try { const j = JSON.parse(t.slice(t.lastIndexOf('\n{') + 1)); if (j && 'pass' in j) { res = j; break; } } catch {}
  }
  const grab = (k) => (new RegExp(`${k}\\s*[:：]?\\s*([^\\n]*)`, 'i').exec(text) || [])[1]?.trim() || null;
  return {
    job: id, status,
    pass: res ? !!res.pass : (/Passed/.test(text) ? true : /Failed|ERRORED/.test(text) ? false : null),
    score: res?.score ?? (Number((/Score:\s*(\d+)/.exec(text) || [])[1]) || null), messages: res?.messages ?? null,
    started: grab('Started At'), finished: grab('Finished At'),
    done: status !== null && !BUSY_RE.test(status),
  };
}
const out = (r, label) => { const { done, ...o } = r; console.log(JSON.stringify({ ...o, label: label ?? null })); };

async function main() {
  if (cmd === 'login') {
    const { ctx, page } = await open(false);
    await page.goto(JOBS_URL);
    console.error('ブラウザでGitHubログインを完了してください (最大10分待機)...');
    try { await page.waitForURL((u) => u.origin === BASE && /\/contestant/.test(u.pathname), { timeout: 600000 }); }
    catch { await ctx.close(); die(2, 'ログイン待ちタイムアウト'); }
    await page.waitForLoadState('networkidle').catch(() => {});
    await ctx.close(); console.error('ログイン完了。profile保存済み'); return;
  }
  if (cmd === 'status') {
    const { ctx, page } = await open(true);
    await gotoJobs(ctx, page);
    let id = rest[0] ? Number(rest[0]) : (await jobIds(page))[0];
    if (!id) await mismatch(ctx, page, 'ジョブが見つからない(JOB_LINK)');
    const r = await readJob(page, id);
    if (!r.status) await mismatch(ctx, page, 'status が読み取れない');
    out(r); await ctx.close(); return;
  }
  if (cmd === 'run') {
    const li = rest.indexOf('--label'); const label = li >= 0 ? rest[li + 1] : null;
    const { ctx, page } = await open(true);
    await gotoJobs(ctx, page);
    const before = await jobIds(page);
    const last = before[0] ?? 0;
    if (last) {
      const r = await readJob(page, last);
      if (!r.status) await mismatch(ctx, page, '最新ジョブのstatusが読み取れない');
      if (!r.done) { await ctx.close(); die(1, `ジョブ #${last} が ${r.status} のため依頼しません。終了後に再実行してください`); }
      await gotoJobs(ctx, page);
    }
    let btn = page.getByRole(REQUEST_BUTTON.role, { name: REQUEST_BUTTON.name }).first();
    if (!(await btn.count())) btn = page.getByRole('link', { name: REQUEST_BUTTON.name }).first();
    if (!(await btn.count())) await mismatch(ctx, page, '依頼ボタンが見つからない(REQUEST_BUTTON)');
    await btn.click();
    let id = null; const t0 = Date.now();
    while (Date.now() - t0 < 30000 && !id) {
      await page.waitForTimeout(2000);
      await gotoJobs(ctx, page);
      id = (await jobIds(page)).find((i) => i > last) ?? null;
    }
    if (!id) await mismatch(ctx, page, '依頼後に新しいジョブIDが現れない');
    let r;
    while (true) {
      r = await readJob(page, id);
      if (r.done) break;
      if (Date.now() - t0 > TIMEOUT_MS) { out(r, label); await ctx.close(); die(1, `タイムアウト: #${id} は ${r.status}`); }
      await page.waitForTimeout(POLL_MS);
    }
    out(r, label); await ctx.close(); process.exit(r.pass === true ? 0 : 1);
  }
  die(2, 'usage: node portal-bench.mjs login | run [--label TEXT] | status [job]');
}
main().catch((e) => die(2, `error: ${e.message}`));
