#!/usr/bin/env node
// Supplemental real-browser event controls; only a labelled test event and
// the explicitly named disposable service are mutated. No trace or HAR/token.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const http = require('node:http');
const child = require('node:child_process');
const { chromium } = require(process.env.CAIRN_PLAYWRIGHT_MODULE || 'playwright');
const options = Object.fromEntries(Array.from({ length: (process.argv.length - 2) / 2 }, (_, i) =>
  [process.argv[2 + i * 2], process.argv[3 + i * 2]]));
assert(options['--fixture-json'] && options['--output']);
const fixture = JSON.parse(fs.readFileSync(options['--fixture-json'], 'utf8'));
assert.match(fixture.service, /^notes-proof-[0-9a-f]{8}$/);
const output = path.resolve(options['--output']);
assert(!fs.existsSync(output));
fs.mkdirSync(output, { recursive: true, mode: 0o700 });
const tokenFile = path.join(os.homedir(), '.cairn/admin.token');
assert(!fs.lstatSync(tokenFile).isSymbolicLink() && !(fs.statSync(tokenFile).mode & 0o077));
const credential = fs.readFileSync(tokenFile, 'utf8').trim();
const base = 'http://127.0.0.1:2476';
const result = { state: 'running', service: fixture.service, checks: [], started: new Date().toISOString() };
const save = () => fs.writeFileSync(path.join(output, 'result.json'), JSON.stringify(result, null, 2), { mode: 0o600 });
const scrub = value => String(value).split(credential).join('[REDACTED]');
const metadataFile = path.join(output, 'metadata-fixture.json');
const helper = action => child.execFileSync('/usr/bin/python3', [path.join(__dirname, 'dashboard_metadata_fixture.py'),
  action, '--service', fixture.service, '--evidence', metadataFile], { timeout: 15000, encoding: 'utf8' });
function lifecycle(action) {
  return new Promise((resolve, reject) => {
    const call = http.request({ socketPath: path.join(os.homedir(), '.cairn/cairnd.sock'),
      path: `/services/${fixture.service}/${action}`, method: 'POST' }, response => {
      response.resume(); response.on('end', () => response.statusCode === 200 ? resolve() : reject(new Error(`owned fixture ${action}: ${response.statusCode}`)));
    });
    call.setTimeout(90000, () => call.destroy(new Error('bounded lifecycle deadline')));
    call.on('error', reject); call.end();
  });
}
function apiGet(route) {
  return new Promise((resolve, reject) => {
    const call = http.request({ socketPath: path.join(os.homedir(), '.cairn/cairnd.sock'), path: route, method: 'GET' }, response => {
      const chunks = [];
      response.on('data', chunk => chunks.push(chunk));
      response.on('error', reject);
      response.on('end', () => {
        if (response.statusCode !== 200) return reject(new Error(`GET ${route}: ${response.statusCode}`));
        try { resolve(JSON.parse(Buffer.concat(chunks).toString())); } catch (error) { reject(error); }
      });
    });
    call.setTimeout(90000, () => call.destroy(new Error('bounded read deadline')));
    call.on('error', reject); call.end();
  });
}
async function proveFreshStartEvent(page) {
  const service = await apiGet(`/services/${fixture.service}`);
  assert.equal(service.name, fixture.service);
  assert(service.id);
  const before = await apiGet('/events') || [];
  const beforeIds = new Set(before.map(event => event.id));
  const text = 'Started service ' + fixture.service;
  const origin = await page.evaluate(() => performance.timeOrigin);
  await lifecycle('start');
  const after = await apiGet('/events') || [];
  const fresh = after.filter(event => event.id && !beforeIds.has(event.id) && event.service_id === service.id && event.type === 'ServiceStarted' && event.message === text);
  assert.equal(fresh.length, 1, 'require exactly one new owned ServiceStarted backend event');
  const event = fresh[0];
  // Observe the browser's own poll, not just the direct backend read. No
  // refresh/navigation/fetch is injected into the page to manufacture success.
  const reply = await page.waitForResponse(async response => {
    const url = new URL(response.url());
    if (url.origin !== base || url.pathname !== '/events' || response.request().method() !== 'GET' || response.status() !== 200) return false;
    const rows = await response.json() || [];
    return rows.some(row => row.id === event.id && row.service_id === service.id && row.type === event.type && row.message === text && row.created_at === event.created_at);
  }, { timeout: 30000 });
  const rows = await reply.json();
  assert.equal(new Set(rows.map(row => row.id)).size, rows.length, 'duplicate event IDs in browser response');
  // The existing UI has no DOM event-ID attribute. Bind the exact response ID
  // to its row by checking the ENTIRE rendered response order, text/type/time.
  // Record this correlation explicitly, never claim an ID was present in DOM.
  const handle = await page.waitForFunction(({ rows, id }) => {
    if (document.getElementById('events-type-filter').value !== 'all') return false;
    const nodes = Array.from(document.querySelectorAll('#timeline-events-body .timeline-node'));
    if (nodes.length !== rows.length) return false;
    const dateFormat = new Intl.DateTimeFormat(undefined, { month: 'short', day: 'numeric' });
    const timeFormat = new Intl.DateTimeFormat(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit' });
    for (let index = 0; index < rows.length; index++) {
      const row = rows[index], node = nodes[index], date = new Date(row.created_at);
      const time = Number.isNaN(date.getTime()) ? String(row.created_at) : `${dateFormat.format(date)} ${timeFormat.format(date)}`;
      if (node.querySelector('.timeline-desc')?.textContent !== (row.message || '') ||
          node.querySelector('.event-type-pill')?.textContent !== (row.type || 'event') ||
          node.querySelector('.timeline-meta')?.textContent !== time) return false;
    }
    const index = rows.findIndex(row => row.id === id);
    if (index < 0) return false;
    return { id, text: nodes[index].querySelector('.timeline-desc').textContent,
      row_index: index, correlation: 'backend response ID/order plus exact rendered text/type/time' };
  }, { rows, id: event.id }, { timeout: 30000 });
  const rendered = await handle.jsonValue();
  assert.equal(rendered.id, event.id); assert.equal(rendered.text, text);
  assert.equal(await page.evaluate(() => performance.timeOrigin), origin);
  return { new_event_id: event.id, new_event: text, backend_event: event,
    absent_before_action: !beforeIds.has(event.id), rendered_event: rendered, page_not_reloaded: true };
}
async function check(name, action) {
  const record = { name, state: 'running' }; result.checks.push(record); save();
  try { record.evidence = await action(); record.state = 'passed'; }
  catch (error) { record.state = 'failed'; record.error = scrub(error.message); throw error; }
  finally { save(); }
}
(async () => {
  let browser, page, seeded = false, seedAttempted = false;
  try {
    seedAttempted = true;
    helper('create'); seeded = true;
    const metadata = JSON.parse(fs.readFileSync(metadataFile, 'utf8'));
    result.metadata_event_id = metadata.event_id; save();
    browser = await chromium.launch({ headless: true, chromiumSandbox: true,
      ...(process.env.CAIRN_CHROMIUM_EXECUTABLE ? { executablePath: process.env.CAIRN_CHROMIUM_EXECUTABLE } : {}) });
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 },
      httpCredentials: { username: 'cairn', password: credential, origin: base } });
    page = await context.newPage(); page.setDefaultTimeout(30000);
    const dialogs = []; page.on('dialog', async dialog => { dialogs.push(dialog.message()); await dialog.dismiss(); });
    assert.equal((await page.goto(base + '/dashboard/#/events')).status(), 200);
    await page.waitForFunction(() => document.getElementById('daemon-connection-text').textContent.trim() === 'Connected');
    const body = page.locator('#timeline-events-body');
    const rowsReady = () => page.waitForFunction(() => document.querySelectorAll('#timeline-events-body .timeline-node').length > 10);
    const positions = () => body.evaluate(node => ({ top: node.scrollTop, height: node.scrollHeight, viewport: node.clientHeight }));
    await rowsReady();
    await check('Auto-scroll performs actual bottom scrolling on Refresh; off does not jump to the bottom', async () => {
      await page.locator('#chk-events-autoscroll').check();
      await body.evaluate(node => { node.scrollTop = 0; });
      await page.locator('#btn-refresh-events').click(); await rowsReady();
      await page.waitForFunction(() => { const node = document.getElementById('timeline-events-body'); return node.scrollHeight > node.clientHeight && Math.abs(node.scrollHeight - node.clientHeight - node.scrollTop) < 5; });
      const followed = await positions();
      await page.locator('#chk-events-autoscroll').uncheck();
      await body.evaluate(node => { node.scrollTop = 0; });
      await page.locator('#btn-refresh-events').click(); await rowsReady();
      assert.equal((await positions()).top, 0);
      return { followed, off: await positions() };
    });
    await check('Timeline supports real wheel and keyboard scrolling', async () => {
      await body.hover(); await page.mouse.wheel(0, 500);
      await page.waitForFunction(() => document.getElementById('timeline-events-body').scrollTop > 0);
      const wheel = await positions();
      await body.focus(); await page.keyboard.press('Home');
      await page.waitForFunction(() => document.getElementById('timeline-events-body').scrollTop === 0);
      return { wheel, keyboard_home: await positions() };
    });
    await check('Metadata Inspect/Hide displays escaped JSON and survives refresh/filter replacement', async () => {
      await page.locator('#events-type-filter').selectOption('AcceptanceMetadataProbe');
      const probe = () => page.locator('#timeline-events-body .timeline-node')
        .filter({ has: page.locator('.event-type-pill').filter({ hasText: /^AcceptanceMetadataProbe$/ }) })
        .filter({ hasText: fixture.service });
      await probe().waitFor({ state: 'visible' });
      await probe().locator('.toggle-metadata').click();
      await probe().locator('pre.timeline-json').waitFor({ state: 'visible' });
      assert.deepEqual(JSON.parse(await probe().locator('pre.timeline-json').textContent()), metadata.metadata);
      assert.equal(await probe().locator('pre img').count(), 0); assert.deepEqual(dialogs, []);
      await page.locator('#btn-refresh-events').click();
      await probe().locator('pre.timeline-json').waitFor({ state: 'visible' });
      await page.locator('#events-type-filter').selectOption('all'); await rowsReady();
      await page.locator('#events-type-filter').selectOption('AcceptanceMetadataProbe');
      await probe().locator('pre.timeline-json').waitFor({ state: 'visible' });
      await page.screenshot({ path: path.join(output, 'escaped-metadata-after-refresh.png') });
      await probe().locator('.toggle-metadata').click();
      await probe().locator('pre.timeline-json').waitFor({ state: 'hidden' });
      return { event_id: metadata.event_id, exact_json: true, escaped_markup: true, expansion_persisted: true };
    });
    await check('Automatic polling renders a real new backend event without reloading', async () => {
      await page.locator('#events-type-filter').selectOption('all'); await rowsReady();
      await body.evaluate(node => { node.scrollTop = 0; });
      const evidence = await proveFreshStartEvent(page);
      assert.equal((await positions()).top, 0);
      return { ...evidence, off_did_not_follow_to_bottom: true };
    });
    result.state = 'passed';
  } catch (error) {
    result.state = 'failed'; result.failure = scrub(error.message); process.exitCode = 1;
    if (page) await page.screenshot({ path: path.join(output, 'failure.png') }).catch(() => {});
  } finally {
    const errors = [];
    const attempt = async (step, action) => {
      try { await action(); }
      catch (error) { errors.push({ step, error: scrub(error.message) }); result.state = 'failed'; process.exitCode = 1; }
    };
    await attempt('stop', () => lifecycle('stop'));
    await attempt('remove_metadata_event', () => {
      // A helper may commit the event and lose its acknowledgement. The
      // original helper validates the saved exact ID/service/type/message.
      if (seeded || (seedAttempted && fs.existsSync(metadataFile))) helper('remove');
      else if (seedAttempted) throw new Error('metadata creation outcome unknown; no exact event evidence for cleanup');
    });
    if (browser) await attempt('close_browser', () => browser.close());
    result.cleanup_errors = errors;
    if (errors.length) result.cleanup_error = errors.map(item => `${item.step}: ${item.error}`).join('; ');
    else result.cleanup = 'owned service stopped; only exact labelled test event removed; evidence retained';
    result.finished = new Date().toISOString(); save();
    console.log(JSON.stringify({ state: result.state, checks: result.checks, output }));
  }
})();
