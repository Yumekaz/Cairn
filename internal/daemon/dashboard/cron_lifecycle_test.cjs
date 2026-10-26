// Hermetic execution of the production service-detail/button code: no browser,
// network, runtime, timers, packages, or API mutations.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { test } = require('node:test');
const source = fs.readFileSync(path.join(__dirname, 'index.js'), 'utf8');
const start = source.indexOf('  async function showServiceDetail(');
const end = source.indexOf('  async function runLifecycle(', start);
assert(start >= 0 && end > start, 'production service-detail/action functions not found');
const production = source.slice(start, end);

async function detail(kind, state, online = true) {
  const elements = new Map();
  function element(id) {
    return {
      id, dataset: {}, disabled: false, textContent: '', innerHTML: '', className: '',
      classList: { contains: () => false },
      handlers: {},
      cloneNode() { return element(id); },
      parentNode: { replaceChild(next) { elements.set(id, next); } },
      addEventListener(event, handler) { this.handlers[event] = handler; },
    };
  }
  const calls = [];
  const lifecycle = [];
  const service = { name: 'cron-button-fixture', kind, actual_state: state, runtime_id: '' };
  const context = vm.createContext({
    document: { getElementById(id) { if (!elements.has(id)) elements.set(id, element(id)); return elements.get(id); } },
    isOnline: online, activeServiceName: null, activeLogsInterval: null, lastLogsText: '', servicesCache: [], LOGS_POLL_MS: 3000,
    apiCall: async (...args) => { calls.push(args); return service; },
    loadDeployHistory() {}, loadConsoleLogs() {}, sanitizeBadgeClass: value => value,
    activateModalTrap() {}, setInterval: () => 1, clearInterval() {},
    runLifecycle: async (...args) => lifecycle.push(args.slice(0, 2)),
    showToast() { throw new Error('unexpected toast'); }, console,
  });
  vm.runInContext(production, context);
  await vm.runInContext('showServiceDetail("cron-button-fixture")', context);
  assert.equal(calls.length, 1, 'state rendering made extra API calls');
  assert.deepEqual(calls[0], ['/services/cron-button-fixture']);
  assert.equal(lifecycle.length, 0, 'rendering dispatched a lifecycle operation');
  return { elements, lifecycle };
}

test('active cron disables Start and exposes pause-future-runs semantics', async () => {
  const { elements, lifecycle } = await detail('cron', 'active');
  assert.equal(elements.get('btn-action-start').disabled, true);
  assert.equal(elements.get('btn-action-start').dataset.stateDisabled, '1');
  assert.equal(elements.get('btn-action-stop').disabled, false);
  assert.equal(elements.get('btn-action-restart').disabled, false);
  assert.match(elements.get('lifecycle-hint').textContent, /schedule.*active/i);
  assert.match(elements.get('lifecycle-hint').textContent, /future/i);
  assert.match(elements.get('lifecycle-hint').textContent, /running jobs/i);
  await elements.get('btn-action-stop').handlers.click();
  assert.deepEqual(lifecycle, [['cron-button-fixture', 'stop']]);
});

test('stopped cron enables Start/Restart without a runtime ID', async () => {
  const { elements, lifecycle } = await detail('cron', 'stopped');
  assert.equal(elements.get('btn-action-start').disabled, false);
  assert.equal(elements.get('btn-action-stop').disabled, true);
  assert.equal(elements.get('btn-action-restart').disabled, false);
  assert.match(elements.get('lifecycle-hint').textContent, /schedule.*stopped/i);
  assert.match(elements.get('lifecycle-hint').textContent, /resume/i);
  await elements.get('btn-action-start').handlers.click();
  await elements.get('btn-action-restart').handlers.click();
  assert.deepEqual(lifecycle, [['cron-button-fixture', 'start'], ['cron-button-fixture', 'restart']]);
});

test('cron controls remain disabled offline and retain active-state guards', async () => {
  const { elements } = await detail('cron', 'active', false);
  for (const id of ['btn-action-start', 'btn-action-stop', 'btn-action-restart']) assert.equal(elements.get(id).disabled, true);
  assert.equal(elements.get('btn-action-start').dataset.stateDisabled, '1');
  assert.match(elements.get('lifecycle-hint').textContent, /offline/i);
});

test('ordinary web/worker lifecycle states keep existing guards and hints', async () => {
  for (const kind of ['web', 'worker']) {
    for (const state of ['running', 'starting', 'stopped', 'failed', 'active']) {
      const { elements } = await detail(kind, state);
      assert.equal(elements.get('btn-action-start').disabled, ['running', 'starting'].includes(state));
      assert.equal(elements.get('btn-action-stop').disabled, ['stopped', 'failed'].includes(state));
      assert.equal(elements.get('btn-action-restart').disabled, false);
      assert.doesNotMatch(elements.get('lifecycle-hint').textContent, /schedule/i);
    }
  }
});
