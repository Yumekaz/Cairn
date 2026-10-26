#!/usr/bin/env node
// Authenticated, real-browser replay against ONLY a named notes-proof fixture.
// No credential URL, trace/HAR recording, existing browser, or host daemon stop.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const http = require('node:http');
const { chromium } = require(process.env.CAIRN_PLAYWRIGHT_MODULE || 'playwright');

const options = Object.fromEntries(Array.from({ length: (process.argv.length - 2) / 2 }, (_, i) =>
  [process.argv[2 + i * 2], process.argv[3 + i * 2]]));
assert(options['--fixture-json'] && options['--output'] && options['--port'], 'fixture JSON, fresh output and port are required');
const fixture = JSON.parse(fs.readFileSync(options['--fixture-json'], 'utf8'));
assert.match(fixture.service, /^notes-proof-[0-9a-f]{8}$/);
assert.equal(fixture.data_volume, fixture.service + '-data');
const port = Number(options['--port']);
assert(Number.isInteger(port) && port > 1024 && port < 65536);
const output = path.resolve(options['--output']);
assert(!fs.existsSync(output), 'never overwrite an earlier replay');
fs.mkdirSync(output, { recursive: true, mode: 0o700 });
const tokenPath = path.join(os.homedir(), '.cairn/admin.token');
assert(!fs.lstatSync(tokenPath).isSymbolicLink() && !(fs.statSync(tokenPath).mode & 0o077));
const credential = fs.readFileSync(tokenPath, 'utf8').trim();
const base = 'http://127.0.0.1:2476';
const results = { state: 'running', service: fixture.service, checks: [], started: new Date().toISOString() };
const save = () => fs.writeFileSync(path.join(output, 'result.json'), JSON.stringify(results, null, 2), { mode: 0o600 });
const scrub = value => String(value).split(credential).join('[REDACTED]');

function request(method, route, body, application = false) {
  return new Promise((resolve, reject) => {
    const settings = application
      ? { hostname: '127.0.0.1', port, path: '/cgi-bin/notes', method }
      : { socketPath: path.join(os.homedir(), '.cairn/cairnd.sock'), path: route, method };
    const call = http.request(settings, response => {
      const chunks = [];
      response.on('data', chunk => chunks.push(chunk));
      response.on('end', () => {
        const text = Buffer.concat(chunks).toString();
        if (response.statusCode >= 400) return reject(new Error(`${method} ${route}: ${response.statusCode}`));
        resolve(application ? text : text ? JSON.parse(text) : null);
      });
    });
    call.setTimeout(90000, () => call.destroy(new Error('bounded request deadline')));
    call.on('error', reject);
    if (body !== undefined) call.write(body);
    call.end();
  });
}
async function eventually(check, timeout = 90000) {
  const deadline = Date.now() + timeout;
  let error;
  do {
    try { return await check(); } catch (caught) { error = caught; }
    await new Promise(resolve => setTimeout(resolve, 200));
  } while (Date.now() < deadline);
  throw error;
}
const service = () => request('GET', `/services/${fixture.service}`);
const backups = () => request('GET', `/volumes/${fixture.data_volume}/backups`);
const journal = (method = 'GET', text) => request(method, '', text, true);
const expectData = text => eventually(async () => assert.equal(await journal(), text));
async function check(name, action) {
  const record = { name, state: 'running', started: new Date().toISOString() };
  results.checks.push(record); save();
  try { await action(); record.state = 'passed'; }
  catch (error) { record.state = 'failed'; record.error = scrub(error.message); throw error; }
  finally { record.ended = new Date().toISOString(); save(); }
}

(async () => {
  let browser, context, page;
  try {
    const before = await service();
    assert.equal(before.name, fixture.service);
    assert((await backups()).some(item => item.id === fixture.backup && item.status === 'success'));
    browser = await chromium.launch({ headless: true, chromiumSandbox: true,
      ...(process.env.CAIRN_CHROMIUM_EXECUTABLE ? { executablePath: process.env.CAIRN_CHROMIUM_EXECUTABLE } : {}) });
    context = await browser.newContext({ viewport: { width: 1440, height: 1000 },
      httpCredentials: { username: 'cairn', password: credential, origin: base } });
    page = await context.newPage();
    page.setDefaultTimeout(30000);
    const visible = id => page.locator('#' + id).waitFor({ state: 'visible' });
    const enabled = id => page.waitForFunction(id => !document.getElementById(id).disabled, id);
    const disabled = id => page.waitForFunction(id => document.getElementById(id).disabled, id);
    const connected = () => page.waitForFunction(() => document.getElementById('daemon-connection-text').textContent.trim() === 'Connected');
    const hidden = id => page.locator('#' + id).waitFor({ state: 'hidden' });
    async function inspect() {
      await page.locator('#nav-services').click();
      await page.locator('#input-search-services').fill(fixture.service);
      const card = page.locator('#services-container .service-card').filter({ hasText: fixture.service });
      await card.locator('.btn-inspect').click();
      await visible('modal-service-detail');
      await eventually(async () => assert.equal((await page.locator('#detail-service-id').textContent()).trim(), before.id));
    }
    await check('Authenticated navigation and live overview', async () => {
      const response = await page.goto(base + '/dashboard/#/overview');
      assert.equal(response.status(), 200); await connected();
      await page.waitForFunction(() => /^\d+$/.test(document.getElementById('overview-services-count').textContent.trim()));
    });
    await check('Service search, empty result and inspection', async () => {
      await page.locator('#nav-services').click();
      await page.locator('#input-search-services').fill('no-such-' + fixture.service);
      await eventually(async () => assert.equal((await page.locator('#services-container .empty-title').innerText()).trim(), 'No matches'));
      await inspect();
    });
    await check('Start and Start-disabled state match actual serving/data', async () => {
      if ((await service()).actual_state === 'running') {
        await enabled('btn-action-stop'); await page.locator('#btn-action-stop').click();
        await eventually(async () => assert.equal((await service()).actual_state, 'stopped'));
      }
      await enabled('btn-action-start'); await page.locator('#btn-action-start').click();
      await expectData('first\n'); await disabled('btn-action-start');
      await journal('POST', 'dashboard-retained'); await expectData('first\ndashboard-retained\n');
    });
    await check('Restart preserves actual journal data', async () => {
      await enabled('btn-action-restart'); await page.locator('#btn-action-restart').click();
      await expectData('first\ndashboard-retained\n');
      await eventually(async () => assert.equal((await service()).actual_state, 'running'));
    });
    await check('Log follow, wrap, refresh and local-only Clear', async () => {
      await page.locator('#chk-logs-follow').uncheck(); assert.equal(await page.locator('#chk-logs-follow').isChecked(), false);
      const previous = await page.locator('#service-logs-console').evaluate(node => node.classList.contains('nowrap'));
      await page.locator('#btn-logs-wrap').click();
      assert.notEqual(await page.locator('#service-logs-console').evaluate(node => node.classList.contains('nowrap')), previous);
      await enabled('btn-logs-refresh'); await page.locator('#btn-logs-refresh').click();
      await eventually(async () => assert.match(await page.locator('#service-logs-console').innerText(), /Starting:|httpd|Mounting volume/));
      await page.locator('#btn-logs-clear').click();
      await eventually(async () => assert.match(await page.locator('#service-logs-console').innerText(), /Console cleared.*local view only/));
      await page.locator('#btn-logs-refresh').click();
      await eventually(async () => assert.match(await page.locator('#service-logs-console').innerText(), /Starting:|httpd|Mounting volume/));
      await page.locator('#chk-logs-follow').check(); assert.equal(await page.locator('#chk-logs-follow').isChecked(), true);
    });
    await check('Stop actually stops serving; Start restores persistent data', async () => {
      await enabled('btn-action-stop'); await page.locator('#btn-action-stop').click();
      await eventually(async () => assert.equal((await service()).actual_state, 'stopped'));
      let serving = true; try { await journal(); } catch { serving = false; } assert.equal(serving, false);
      await enabled('btn-action-start'); await page.locator('#btn-action-start').click();
      await expectData('first\ndashboard-retained\n');
    });
    await check('Inspection close and keyboard Escape return focus', async () => {
      await page.locator('#btn-close-service-modal').click(); await hidden('modal-service-detail');
      await inspect(); await page.keyboard.press('Escape'); await hidden('modal-service-detail');
      await eventually(async () => assert.equal(await page.evaluate(() => document.activeElement.dataset.serviceName), fixture.service));
    });
    await page.locator('#nav-volumes').click();
    await page.locator(`#volumes-list-body tr[data-volume-name="${fixture.data_volume}"] .btn-inspect-vol`).click();
    await check('Backup Now creates a backend-successful snapshot', async () => {
      const initial = (await backups()).length;
      await enabled('btn-create-backup'); await page.locator('#btn-create-backup').click();
      await eventually(async () => assert((await backups()).length > initial));
      assert((await backups()).every(item => item.status === 'success'));
    });
    const restore = () => page.locator(`.btn-restore[data-backup-id="${fixture.backup}"][data-volume-name="${fixture.data_volume}"]`).click();
    await check('Restore acknowledgement, reverse Tab, Cancel and close do not mutate data', async () => {
      await restore(); await visible('modal-confirm-action'); await disabled('btn-confirm-proceed');
      await page.locator('#btn-close-confirm-modal').focus(); await page.keyboard.press('Shift+Tab');
      assert.equal(await page.evaluate(() => document.activeElement.id), 'btn-confirm-cancel');
      await page.locator('#btn-confirm-cancel').click(); await hidden('modal-confirm-action');
      await expectData('first\ndashboard-retained\n');
      await restore(); await page.locator('#btn-close-confirm-modal').click(); await hidden('modal-confirm-action');
      await expectData('first\ndashboard-retained\n');
    });
    await check('Acknowledged offline/reconnect preserves dialog and disables mutations', async () => {
      await restore(); await page.locator('#chk-confirm-understand').check(); await enabled('btn-confirm-proceed');
      const origin = await page.evaluate(() => performance.timeOrigin);
      await context.setOffline(true); await visible('global-banner'); await disabled('btn-confirm-proceed');
      assert.equal(await page.locator('#chk-confirm-understand').isChecked(), true);
      await page.locator('#btn-retry-connection').click(); await disabled('btn-confirm-proceed');
      await context.setOffline(false); await connected(); await visible('modal-confirm-action');
      assert.equal(await page.locator('#chk-confirm-understand').isChecked(), true); await enabled('btn-confirm-proceed');
      assert.equal(await page.evaluate(() => performance.timeOrigin), origin, 'reconnect must not reload the page');
      await page.screenshot({ path: path.join(output, 'acknowledged-reconnect.png') });
    });
    await check('Confirmed Restore recovers the selected snapshot bytes', async () => {
      await page.locator('#btn-confirm-proceed').click(); await hidden('modal-confirm-action');
      await expectData('first\n');
    });
    await check('Rollback selects the intended release without undoing data', async () => {
      await inspect();
      const target = await page.locator('.btn-rollback').first().getAttribute('data-deploy-id');
      assert(target); await page.locator(`.btn-rollback[data-deploy-id="${target}"]`).click();
      await visible('modal-confirm-action'); await page.locator('#chk-confirm-understand').check();
      await enabled('btn-confirm-proceed'); await page.locator('#btn-confirm-proceed').click();
      await eventually(async () => assert.equal((await service()).current_deploy_id, target));
      await expectData('first\n'); await page.locator('#btn-close-service-modal').click();
    });
    await check('Events filter, Refresh and auto-scroll controls', async () => {
      await page.locator('#nav-events').click(); await visible('timeline-events-body');
      await page.locator('#btn-refresh-events').click();
      await page.waitForFunction(() => document.querySelectorAll('#events-type-filter option').length > 1);
      const type = await page.locator('#events-type-filter option').nth(1).getAttribute('value');
      await page.locator('#events-type-filter').selectOption(type);
      await eventually(async () => {
        const shown = await page.locator('#timeline-events-body .event-type-pill').allTextContents();
        assert(shown.length); assert(shown.every(value => value.trim() === type));
      });
      await page.locator('#chk-events-autoscroll').uncheck(); assert.equal(await page.locator('#chk-events-autoscroll').isChecked(), false);
      await page.locator('#chk-events-autoscroll').check(); assert.equal(await page.locator('#chk-events-autoscroll').isChecked(), true);
      await page.locator('#events-type-filter').selectOption('all');
    });
    results.state = 'passed';
  } catch (error) {
    results.state = 'failed'; results.failure = scrub(error.message);
    if (page) await page.screenshot({ path: path.join(output, 'failure.png') }).catch(() => {});
    process.exitCode = 1;
  } finally {
    if (context) await context.setOffline(false).catch(() => {});
    await request('POST', `/services/${fixture.service}/stop`).catch(error => {
      results.cleanup_error = scrub(error.message); results.state = 'failed'; process.exitCode = 1;
    });
    if (browser) await browser.close();
    results.finished = new Date().toISOString(); save();
    console.log(JSON.stringify({ state: results.state, checks: results.checks.map(({ name, state }) => ({ name, state })), output }));
  }
})();
