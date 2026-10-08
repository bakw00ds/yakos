// flows-gallery-smoke.js — Node smoke test for dist/flows-gallery.js (K-152).
// Runs the module against a tiny fake DOM and a fake apiFetch; exits non-zero
// on any failure. Invoked by TestFlowsGallerySmoke.
'use strict';
var fs = require('fs');
var path = require('path');
var vm = require('vm');

function node(tag) {
  return {
    tag: tag, children: [], listeners: {}, attrs: {}, className: '', textContent: '', removed: false,
    appendChild: function (c) { this.children.push(c); return c; },
    setAttribute: function (k, v) { this.attrs[k] = v; },
    addEventListener: function (ev, fn) { this.listeners[ev] = fn; },
    remove: function () { this.removed = true; },
  };
}
function walk(n, fn) { fn(n); n.children.forEach(function (c) { walk(c, fn); }); }
function assert(c, m) { if (!c) { console.error('FAIL: ' + m); process.exit(1); } }
function tick() { return new Promise(function (r) { setImmediate(r); }); }

var body = node('body');
var sandbox = { document: { createElement: node, body: body }, window: {} };
sandbox.window = sandbox;
vm.createContext(sandbox);
vm.runInContext(fs.readFileSync(path.join(__dirname, 'flows-gallery.js'), 'utf8'), sandbox);
assert(sandbox.YakosFlowsGallery && typeof sandbox.YakosFlowsGallery.open === 'function', 'module did not register');

var calls = [];
var saved = null;
function resp(status, json) { return { ok: status < 300, status: status, json: function () { return Promise.resolve(json); } }; }
function apiFetch(method, p, b) {
  calls.push(method + ' ' + p);
  if (p === '/flows/api/templates') return Promise.resolve(resp(200, { templates: [{ name: 'nightly-review', description: '<img src=x onerror=1>' }] }));
  if (p.indexOf('/flows/api/templates?name=') === 0) return Promise.resolve(resp(200, { name: 'nightly-review', yaml: 'version: 1\n' }));
  if (p === '/flows/api/workflow') { assert(b.name === 'nightly-review' && b.version === '', 'bad save body'); return Promise.resolve(resp(200, {})); }
  return Promise.resolve(resp(404, null));
}

(async function () {
  var overlay = sandbox.YakosFlowsGallery.open({ apiFetch: apiFetch, onSaved: function (n) { saved = n; } });
  await tick(); await tick();
  var buttons = [], descs = [];
  walk(overlay, function (n) {
    if (n.tag === 'button' && n.attrs['aria-label']) buttons.push(n);
    if (n.className === 'flows-gallery-desc') descs.push(n);
  });
  assert(buttons.length === 1, 'expected one Use button, got ' + buttons.length);
  assert(descs[0].textContent === '<img src=x onerror=1>', 'description must be set as text, verbatim');
  buttons[0].listeners.click();
  for (var i = 0; i < 6; i++) await tick();
  assert(saved === 'nightly-review', 'onSaved not called');
  assert(overlay.removed, 'overlay not closed after save');
  assert(calls.join('|') === 'GET /flows/api/templates|GET /flows/api/templates?name=nightly-review|POST /flows/api/workflow', 'call order: ' + calls.join('|'));
  console.log('flows-gallery smoke OK');
})();
