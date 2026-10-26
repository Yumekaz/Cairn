// Executes production polling/cleanup blocks with API/DOM/IO doubles only.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { test } = require('node:test');
const source = fs.readFileSync(path.join(__dirname, 'dashboard_events_acceptance.cjs'), 'utf8');
const serviceName = 'notes-proof-1234abcd';
const message = 'Started service ' + serviceName;
const oldEvent = { id: 'old-id', service_id: 'owned-id', type: 'ServiceStarted', message, created_at: '2026-10-26T06:00:00Z' };
const newEvent = { ...oldEvent, id: 'new-id', created_at: '2026-10-26T06:01:00Z' };

async function polling({ fresh = true, render = true, reload = false, foreign = false, wrongText = false, browserFresh = true } = {}) {
  const after = fresh ? [{ ...newEvent, service_id: foreign ? 'foreign-id' : 'owned-id' }, oldEvent] : [oldEvent];
  const rendered = render ? after : [oldEvent];
  let eventReads = 0;
  let evaluations = 0;
  let evidence;
  const formatTime = value => {
    const date = new Date(value);
    return new Intl.DateTimeFormat(undefined, { month: 'short', day: 'numeric' }).format(date) + ' ' +
      new Intl.DateTimeFormat(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit' }).format(date);
  };
  const document = {
    getElementById: () => ({ value: 'all' }),
    querySelectorAll: () => rendered.map(event => ({ querySelector(selector) {
      return { textContent: selector === '.event-type-pill' ? event.type : selector === '.timeline-meta' ? formatTime(event.created_at) : wrongText ? 'wrong text' : event.message };
    } })),
  };
  const page = {
    locator: () => ({ selectOption: async () => {}, filter() { return this; }, first() { return this; }, waitFor: async () => {} }),
    evaluate: async () => ++evaluations === 1 || !reload ? 123 : 456,
    waitForResponse: async predicate => {
      const response = { url: () => 'http://127.0.0.1:2476/events', status: () => 200, request: () => ({ method: () => 'GET' }), json: async () => browserFresh ? after : [oldEvent] };
      if (!await predicate(response)) throw new Error('no fresh browser poll');
      return response;
    },
    waitForFunction: async (predicate, argument) => {
      const context = vm.createContext({ document, Intl, Date, argument });
      const value = vm.runInContext('(' + predicate.toString() + ')(argument)', context);
      if (!value) throw new Error('new event not rendered');
      return { jsonValue: async () => JSON.parse(JSON.stringify(value)) };
    },
  };
  const context = vm.createContext({
    assert, URL, page, fixture: { service: serviceName }, base: 'http://127.0.0.1:2476',
    body: { evaluate: async () => {} }, rowsReady: async () => {}, positions: async () => ({ top: 0 }),
    lifecycle: async action => { assert.equal(action, 'start'); },
    apiGet: async route => route === '/events' ? (++eventReads === 1 ? [oldEvent] : after) : { id: 'owned-id', name: serviceName },
    check: async (_, action) => { evidence = await action(); },
  });
  const helperStart = source.indexOf('async function proveFreshStartEvent(');
  if (helperStart >= 0) vm.runInContext(source.slice(helperStart, source.indexOf('\nasync function check(', helperStart)), context);
  const start = source.indexOf("    await check('Automatic polling");
  const end = source.indexOf("    result.state = 'passed';", start);
  await vm.runInContext('(async () => {\n' + source.slice(start, end) + '\n})()', context);
  return evidence;
}

test('old backend event cannot satisfy the polling proof', async () => {
  await assert.rejects(polling({ fresh: false }));
});
test('fresh backend response with stale DOM cannot pass', async () => {
  await assert.rejects(polling({ render: false }));
});
test('direct backend read cannot substitute for a fresh browser poll', async () => {
  await assert.rejects(polling({ browserFresh: false }));
});
test('fresh exact event is correlated to rendered row and retained', async () => {
  const evidence = await polling();
  assert.equal(evidence.new_event_id, 'new-id');
  assert.equal(evidence.rendered_event.id, 'new-id');
  assert.equal(evidence.rendered_event.text, message);
  assert.equal(evidence.page_not_reloaded, true);
});
test('wrong owner, wrong rendered text, and reload are rejected', async () => {
  await assert.rejects(polling({ foreign: true }));
  await assert.rejects(polling({ wrongText: true }));
  await assert.rejects(polling({ reload: true }));
});

async function cleanup({ stopError = false, eventError = false, closeError = false, seeded = true } = {}) {
  const calls = [];
  const result = { state: 'passed' };
  const start = source.lastIndexOf('  } finally {') + '  } finally {'.length;
  const end = source.lastIndexOf('\n  }\n})();');
  assert(start > 0 && end > start);
  const context = vm.createContext({
    result, seeded, seedAttempted: true, metadataFile: '/fake/fresh-output/metadata-fixture.json', output: '/fake/fresh-output',
    fs: { existsSync: () => true },
    lifecycle: async action => { calls.push(action); if (stopError) throw new Error('Stop failure'); },
    helper: action => { calls.push(action); if (eventError) throw new Error('event removal failure'); },
    browser: { close: async () => { calls.push('close'); if (closeError) throw new Error('close failure'); } },
    scrub: String, save: () => calls.push('save'), process: {}, console: { log() {} }, Date, JSON,
  });
  await vm.runInContext('(async () => {' + source.slice(start, end) + '})()', context);
  return { calls, result };
}
test('Stop failure cannot suppress exact metadata-event removal', async () => {
  const { calls, result } = await cleanup({ stopError: true });
  assert(calls.includes('remove'));
  assert.equal(result.state, 'failed');
});
test('all cleanup failures survive and final evidence is saved', async () => {
  const { calls, result } = await cleanup({ stopError: true, eventError: true, closeError: true });
  assert.equal(result.cleanup_errors.length, 3);
  assert(calls.includes('save'));
});
test('committed seed with lost helper response still gets exact cleanup', async () => {
  const { calls } = await cleanup({ seeded: false });
  assert(calls.includes('remove'));
});
