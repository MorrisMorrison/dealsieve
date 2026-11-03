// Captures the README screenshots from a demo-data dealsieve instance.
//
// Starts a tiny fake mydealz (feeds + placeholder images), starts the built
// binary against it, waits for two trending polls so the heat curves have two
// points, then drives the page with Playwright. Everything it starts is stopped
// again, also on failure. All data is made up (see ./feeds and ./config.yaml).
//
//   CGO_ENABLED=0 go build -o dealsieve ./cmd/dealsieve   # from the repo root
//   cd screenshots && npm ci && npx playwright install chromium && npm run capture
//
// DEALSIEVE_BIN overrides the binary path; SHOTS_OUT the output folder.
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import fs from 'node:fs';
import http from 'node:http';
import net from 'node:net';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { chromium } from 'playwright';

const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(here, '..');
// 'go build -o dealsieve' may or may not append .exe on Windows, so accept both.
const bin = process.env.DEALSIEVE_BIN
  ?? ['dealsieve.exe', 'dealsieve'].map((n) => path.join(root, n)).find((p) => fs.existsSync(p))
  ?? path.join(root, 'dealsieve');
const outDir = process.env.SHOTS_OUT ?? path.join(root, 'docs', 'screenshots');
const feed = (name) => fs.readFileSync(path.join(here, 'feeds', name), 'utf8');

const palette = ['#2f6fed', '#e0662b', '#17a673', '#8a4fd6', '#c2358a', '#0e8fa3', '#b58a12'];

function placeholder(id) {
  const colour = palette[Number(id) % palette.length];
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100"><rect width="100" height="100" fill="${colour}"/>`
    + '<circle cx="50" cy="50" r="26" fill="none" stroke="#fff" stroke-opacity=".85" stroke-width="7"/>'
    + '<path d="M36 50h28M50 36v28" stroke="#fff" stroke-opacity=".85" stroke-width="7" stroke-linecap="round"/></svg>';
}

function startFakeMydealz() {
  let trendingHits = 0;
  const server = http.createServer((req, res) => {
    const { pathname } = new URL(req.url, 'http://x');
    const base = `http://${req.headers.host}`;
    const xml = (body) => {
      res.writeHead(200, { 'Content-Type': 'application/rss+xml; charset=utf-8' });
      res.end(body.replaceAll('{{BASE}}', base));
    };
    const img = pathname.match(/^\/img\/(\d+)\.svg$/);
    if (pathname === '/rss/trending') {
      trendingHits += 1;
      xml(feed(trendingHits === 1 ? 'trending-1.xml' : 'trending-2.xml')); // later polls: hotter
    } else if (pathname === '/rss/alles') {
      xml(feed('new.xml'));
    } else if (img) {
      res.writeHead(200, { 'Content-Type': 'image/svg+xml' });
      res.end(placeholder(img[1]));
    } else {
      res.writeHead(404).end();
    }
  });
  return new Promise((resolve) => {
    server.listen(0, '127.0.0.1', () => resolve({ server, url: `http://127.0.0.1:${server.address().port}` }));
  });
}

async function freePort() {
  const s = net.createServer();
  s.listen(0, '127.0.0.1');
  await once(s, 'listening');
  const { port } = s.address();
  s.close();
  await once(s, 'close');
  return port;
}

async function waitFor(what, fn, timeoutMs, everyMs = 500) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    try {
      const v = await fn();
      if (v) return v;
    } catch { /* not ready yet */ }
    if (Date.now() > deadline) throw new Error(`timed out waiting for ${what}`);
    await new Promise((r) => setTimeout(r, everyMs));
  }
}

const trendingSuccess = async (base) => {
  const { feeds } = await (await fetch(`${base}/api/status`)).json();
  return feeds.find((f) => f.feed === 'trending')?.last_success ?? null;
};

async function main() {
  if (!fs.existsSync(bin)) throw new Error(`binary not found: ${bin} (build it first, or set DEALSIEVE_BIN)`);
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'dealsieve-shots-'));
  const fake = await startFakeMydealz();
  const port = await freePort();
  const base = `http://127.0.0.1:${port}`;
  let app;
  let browser;
  const logs = [];
  try {
    app = spawn(bin, [], {
      env: {
        ...process.env,
        DEALSIEVE_CONFIG: path.join(here, 'config.yaml'),
        DEALSIEVE_DATA_DIR: path.join(tmp, 'data'),
        DEALSIEVE_LISTEN: `127.0.0.1:${port}`,
        DEALSIEVE_MYDEALZ_URL: fake.url,
      },
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    for (const s of [app.stdout, app.stderr]) s.on('data', (d) => logs.push(String(d)));
    app.on('exit', (code) => logs.push(`[exited with ${code}]`));

    await waitFor('dealsieve to listen', async () => (await fetch(`${base}/health`)).ok, 30_000);
    const first = await waitFor('the first poll', () => trendingSuccess(base), 30_000);
    console.log('first poll done; waiting for the second (poll intervals are at least 1m)...');
    await waitFor('the second poll', async () => (await trendingSuccess(base)) !== first, 150_000, 1000);

    fs.rmSync(outDir, { recursive: true, force: true });
    fs.mkdirSync(outDir, { recursive: true });
    browser = await chromium.launch();
    const shot = (page, name) => page.screenshot({ path: path.join(outDir, name) });

    async function open(context) {
      const page = await context.newPage();
      await page.goto(base);
      await page.waitForSelector('#list .item');
      await page.waitForFunction(() => [...document.images].every((i) => i.complete));
      await page.mouse.move(0, 0);
      return page;
    }

    const desktop = { viewport: { width: 1280, height: 800 }, deviceScaleFactor: 2 };
    for (const scheme of ['light', 'dark']) {
      const context = await browser.newContext({ ...desktop, colorScheme: scheme });
      const page = await open(context);
      await page.waitForSelector('#detail .detail-title');
      await shot(page, `inbox-${scheme}.png`);
      if (scheme === 'light') {
        await page.click('#tab-blocked');
        const recurring = page.locator('#list .item', { hasText: 'Example Air' }).filter({ has: page.locator('.chip.block') }).first();
        await recurring.waitFor();
        console.log('recurring chip:', await recurring.locator('.chip').textContent());
        await recurring.click();
        await page.waitForSelector('#detail .why');
        await page.mouse.move(0, 0);
        await shot(page, 'blocked.png');
      }
      await context.close();
    }

    const phone = await browser.newContext({
      viewport: { width: 375, height: 812 }, deviceScaleFactor: 2, isMobile: true, hasTouch: true, colorScheme: 'light',
    });
    const page = await open(phone);
    await shot(page, 'phone.png');
    await page.locator('#list .item').first().click();
    await page.waitForSelector('body.sheet-open #detail .detail-title');
    await shot(page, 'phone-detail.png');
    await phone.close();
    console.log(`wrote ${fs.readdirSync(outDir).join(', ')} to ${outDir}`);
  } catch (err) {
    console.error(logs.join('').slice(-4000));
    try {
      const { deals } = await (await fetch(`${base}/api/deals?view=blocked`)).json();
      console.error('blocked deals:', deals.map((d) => `${d.title} => ${d.reasons[0]}`));
    } catch { /* the app may not be up */ }
    throw err;
  } finally {
    await browser?.close();
    if (app && app.exitCode === null) {
      app.kill();
      await Promise.race([once(app, 'exit'), new Promise((r) => setTimeout(r, 5000))]);
      if (app.exitCode === null) app.kill('SIGKILL');
    }
    fake.server.closeAllConnections();
    fake.server.close();
    fs.rmSync(tmp, { recursive: true, force: true });
  }
}

main().catch((err) => {
  console.error(err);
  process.exitCode = 1;
});
