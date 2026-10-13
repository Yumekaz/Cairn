const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, '../internal/daemon/dashboard/index.js'), 'utf8');
// Exercise the production focus functions, not a second implementation.
const start = source.indexOf('  const FOCUSABLE_SEL');
const end = source.indexOf('  async function showServiceDetail', start);
assert(start >= 0 && end > start);

class Element {
  constructor(dataset = {}, id = '') { this.dataset = dataset; this.id = id; this.offsetWidth = 1; }
  getAttribute() { return null; }
  closest() { return null; }
  getClientRects() { return [1]; }
  focus() { this.focused = true; }
}

function closeAfterReplacement(opener, replacements, byId = {}) {
  const modal = { classList: { add() {}, remove() {} }, querySelectorAll() { return []; } };
  const document = {
    activeElement: opener,
    contains(element) { return Object.values(replacements).flat().includes(element); },
    querySelectorAll(selector) { return replacements[selector] || []; },
    getElementById(id) { return byId[id] || null; },
  };
  vm.runInNewContext(source.slice(start, end) + '\nactivateModalTrap(modal); deactivateModalTrap(modal);',
    { document, HTMLElement: Element, requestAnimationFrame() {}, modal });
}

test('restore focus survives backup-list replacement and keeps volume identity', () => {
  const opener = new Element({ backupId: 'backup', volumeName: 'selected' });
  const wrong = new Element({ backupId: 'backup', volumeName: 'other' });
  const replacement = new Element({ backupId: 'backup', volumeName: 'selected' });
  closeAfterReplacement(opener, { '.btn-restore': [wrong, replacement] });
  assert.equal(replacement.focused, true);
  assert.equal(wrong.focused, undefined);
  assert.equal(opener.focused, undefined);
});

test('lifecycle focus follows a replaced button with the same stable id', () => {
  const opener = new Element({}, 'btn-action-stop');
  const replacement = new Element({}, 'btn-action-stop');
  closeAfterReplacement(opener, { buttons: [replacement] }, { 'btn-action-stop': replacement });
  assert.equal(replacement.focused, true);
});

test('rollback focus follows the selected deployment after history refresh', () => {
  const opener = new Element({ deployId: 'selected' });
  const wrong = new Element({ deployId: 'other' });
  const replacement = new Element({ deployId: 'selected' });
  closeAfterReplacement(opener, { '.btn-rollback': [wrong, replacement] });
  assert.equal(replacement.focused, true);
  assert.equal(wrong.focused, undefined);
});
