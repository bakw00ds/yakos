// app-smoke.js — Node.js load-smoke test for dist/app.js.
//
// Purpose: verify that the outer IIFE in app.js executes without throwing a
// runtime error (TDZ crash, undefined-reference crash, etc.). node --check
// only parses; it cannot catch runtime errors like the TDZ crash from the
// pre-paint theme-init block that caused the black-screen regression.
//
// Strategy: provide the minimal global stubs required for the IIFE to reach
// the DOMContentLoaded registration without throwing. We do NOT need a full
// DOM — just enough that every top-level side-effecting call in the IIFE body
// (extractAndStripToken, registerServiceWorker, the pre-paint theme init, the
// connectWS call guard) can execute without reference errors.
//
// This file is invoked by TestAppJSLoadSmoke in app_smoke_test.go via:
//   node dist/app-smoke.js
// Must exit 0 on success, non-zero (+ stderr message) on any failure.
//
// Node version notes:
//   - crypto: Node 19+ exposes globalThis.crypto (Web Crypto API) natively
//     as a read-only getter; no stub needed and attempting to override it
//     throws TypeError in Node 22+/25+. We leave it alone.
//   - navigator: read-only getter in Node 22+/25+; use defineProperty.
//   - location, localStorage, history: writable assignments work in Node 25.

'use strict';

// ── Minimal DOM stubs ────────────────────────────────────────────────────────

var _themeAttr = 'og'; // matches index.html server-side default

var _classList = {
  add: function() {},
  remove: function() {},
  contains: function() { return false; },
};

var _htmlEl = {
  setAttribute: function(k, v) { if (k === 'data-theme') _themeAttr = v; },
  getAttribute: function(k) { return k === 'data-theme' ? _themeAttr : null; },
  classList: _classList,
};

// Minimal document stub — enough for the IIFE's top-level side effects.
global.document = {
  documentElement: _htmlEl,
  // cookie: empty string → readCookie() returns '' → AUTH_MODE='session',
  // CSRF_TOKEN=''. This is the safe default for the smoke test (no cookie
  // access in Node, so we stub it as an empty string).
  cookie: '',
  getElementById: function() { return null; },
  querySelector: function() { return null; },
  querySelectorAll: function() { return []; },
  addEventListener: function(ev, fn) {
    if (ev === 'DOMContentLoaded') {
      // This registration is the key assertion: if the IIFE threw before
      // reaching line ~3134, this never fires and the test fails.
      global._domContentLoadedRegistered = true;
      global._buildPageFn = fn;
    }
  },
  createElement: function() {
    return {
      className: '',
      setAttribute: function() {},
      textContent: '',
      addEventListener: function() {},
      appendChild: function() {},
      style: {},
      src: '',
      onerror: null,
      onload: null,
    };
  },
  head: { appendChild: function() {} },
  body: {},
};

// window must point to global so `window.matchMedia` etc. resolve.
global.window = global;

// location — writable in Node 25.
global.location = {
  hash: '',
  protocol: 'http:',
  host: '127.0.0.1:7890',
  pathname: '/',
  search: '',
  origin: 'http://127.0.0.1:7890',
};

global.history = { replaceState: function() {} };

// navigator — read-only getter in Node 22+/25+; must use defineProperty.
Object.defineProperty(global, 'navigator', {
  value: {
    // No serviceWorker → registerServiceWorker() returns early (no-op; safe).
    serviceWorker: undefined,
    // undefined → falsy → perf-low-end probe does nothing (safe).
    deviceMemory: undefined,
    hardwareConcurrency: undefined,
  },
  writable: true,
  configurable: true,
});

// localStorage — writable in Node 25.
var _ls = {};
global.localStorage = {
  getItem: function(k) {
    return Object.prototype.hasOwnProperty.call(_ls, k) ? _ls[k] : null;
  },
  setItem: function(k, v) { _ls[k] = String(v); },
  removeItem: function(k) { delete _ls[k]; },
};

// matchMedia — returns no match → defaultTheme() returns 'og' (dark default).
global.matchMedia = function(q) {
  return { matches: false, media: q, addEventListener: function() {} };
};

// crypto — Node 19+ provides globalThis.crypto natively (Web Crypto API)
// with getRandomValues. Do NOT attempt to override it (read-only getter in
// Node 22+/25+). The native implementation is fully compatible.

// WebSocket — TOKEN is '' so connectWS() returns early without constructing
// one; stub defensively in case guard logic changes.
global.WebSocket = function() {
  return { addEventListener: function() {}, send: function() {} };
};

// Promise, setTimeout, setInterval, console: provided natively by Node.

// ── Execute app.js ───────────────────────────────────────────────────────────

global._domContentLoadedRegistered = false;

try {
  require('./app.js');
} catch (e) {
  process.stderr.write('FAIL: app.js threw during load: ' + e + '\n');
  process.stderr.write(e.stack + '\n');
  process.exit(1);
}

// Assert that DOMContentLoaded was registered (buildPage is wired up).
if (!global._domContentLoadedRegistered) {
  process.stderr.write(
    'FAIL: DOMContentLoaded listener was NOT registered — buildPage unreachable.\n'
  );
  process.exit(1);
}

// Assert the pre-paint theme was applied with a valid value.
var validThemes = ['og', 'light', 'ops', 'fluid'];
if (validThemes.indexOf(_themeAttr) === -1) {
  process.stderr.write(
    'FAIL: unexpected data-theme value after load: "' + _themeAttr + '"\n'
  );
  process.exit(1);
}

// ── Build-watch (stale-console banner) compare/dismiss logic ────────────────
var bw = global.__yakosBuildWatch;
function fail(m) { process.stderr.write('FAIL: build-watch: ' + m + '\n'); process.exit(1); }
if (!bw) fail('window.__yakosBuildWatch not exposed');
var appended = [];
var els = [];
document.body = { appendChild: function(e) { appended.push(e); } };
document.createElement = function() {
  var e = { listeners: {}, children: [], attrs: {}, parentNode: null,
    setAttribute: function(k, v) { this.attrs[k] = v; },
    addEventListener: function(t, f) { this.listeners[t] = f; },
    appendChild: function(c) { this.children.push(c); } };
  els.push(e); return e;
};
var st = function() { return bw._state(); };
bw.observeId('');                      // missing header: ignored
if (st().baseline !== null) fail('empty id must not set baseline');
bw.observeId('A');                     // first id = baseline, no banner
if (st().baseline !== 'A' || st().shown) fail('first id must set baseline silently');
bw.observeId('A');
if (st().shown) fail('same id must not show banner');
bw.observeId('B');                     // rebuild -> banner
if (!st().shown || appended.length !== 1) fail('changed id must show banner once');
if (appended[0].attrs.role !== 'status') fail('banner needs role=status');
bw.observeId('B');
if (appended.length !== 1) fail('banner must not duplicate');
appended[0].listeners.keydown({ key: 'Escape' });  // Escape dismisses
if (st().shown || st().dismissed !== 'B') fail('Escape must dismiss');
bw.observeId('B');
if (st().shown) fail('dismissed id must not re-show');
bw.observeId('C');                     // further rebuild re-shows
if (!st().shown) fail('new id after dismissal must re-show');
bw.dismiss();

// ── K-110: files.changed "rescanned" refreshes the file-tree subtree ─────────
function ftFail(m) { process.stderr.write('FAIL: tree-rescan: ' + m + '\n'); process.exit(1); }
function mkEl(tag) {
  return { tag: tag, children: [], attrs: {}, style: {}, listeners: {}, className: '',
    textContent: '', parentNode: null,
    setAttribute: function(k, v) { this.attrs[k] = String(v); },
    getAttribute: function(k) { return this.attrs[k]; },
    addEventListener: function(t, f) { this.listeners[t] = f; },
    querySelector: function() { return null; },
    appendChild: function(c) { c.parentNode = this; this.children.push(c); return c; },
    removeChild: function(c) { var i = this.children.indexOf(c); if (i >= 0) this.children.splice(i, 1); return c; },
    contains: function(c) { return this.children.indexOf(c) >= 0; } };
}
var treeRoot = mkEl('div');
document.createElement = mkEl;
document.getElementById = function(id) { return id === 'ide-tree-root' ? treeRoot : null; };
var fsTree = {
  '.':   [{ type: 'dir', name: 'src', path: 'src' }, { type: 'dir', name: 'docs', path: 'docs' }],
  'src': [{ type: 'file', name: 'a.go', path: 'src/a.go' }],
  'docs': [{ type: 'file', name: 'x.md', path: 'docs/x.md' }],
};
var fetched = [];
global.fetch = function(url) {
  var m = /dir=([^&]*)/.exec(url), d = decodeURIComponent(m ? m[1] : '.');
  fetched.push(d);
  return Promise.resolve({ ok: true, status: 200, json: function() { return Promise.resolve({ entries: fsTree[d] || [] }); } });
};
function tick() { return new Promise(function(r) { setTimeout(r, 5); }); }
function names(li) { // file/dir names rendered under a directory row
  var out = [];
  li.children.forEach(function(c) {
    if (c.className === 'ide-tree-list') c.children.forEach(function(row) {
      var b = row.children[0]; out.push(b.children[1].textContent);
    });
  });
  return out;
}
async function treeRescanTest() {
  var it = global.__yakosIdeTree;
  if (!it) ftFail('window.__yakosIdeTree not exposed');
  var ul = it.buildTreeList(fsTree['.'], 0);
  treeRoot.appendChild(ul);
  var srcLi = ul.children[0], docsLi = ul.children[1];
  srcLi.children[0].listeners.click();            // expand src: lazy-loads it
  await tick();
  if (names(srcLi).join() !== 'a.go') ftFail('initial src listing: ' + names(srcLi));

  // Never-loaded directory: nothing stale, no fetch.
  fetched.length = 0;
  it.handleFilesChanged({ path: 'docs', action: 'rescanned', count: 80 });
  await tick();
  if (fetched.length !== 0) ftFail('never-loaded dir must not refetch: ' + fetched);

  // Open, loaded directory: contents replaced with fresh data.
  fsTree.src = [{ type: 'file', name: 'a.go', path: 'src/a.go' }, { type: 'file', name: 'new.go', path: 'src/new.go' }];
  it.handleFilesChanged({ path: 'src', action: 'rescanned', count: 120 });
  await tick();
  if (fetched.join() !== 'src') ftFail('rescanned src must refetch src once: ' + fetched);
  if (names(srcLi).join() !== 'a.go,new.go') ftFail('src not refreshed: ' + names(srcLi));
  if (srcLi.children.filter(function(c) { return c.className === 'ide-tree-list'; }).length !== 1) ftFail('stale list not removed');

  // A NEW directory the tree has not rendered: refresh its nearest rendered ancestor.
  fetched.length = 0;
  fsTree.src.push({ type: 'dir', name: 'pkg', path: 'src/pkg' });
  it.handleFilesChanged({ path: 'src/pkg', action: 'rescanned', count: 60 });
  await tick();
  if (fetched.join() !== 'src') ftFail('new dir must refresh ancestor src: ' + fetched);
  if (names(srcLi).join() !== 'a.go,new.go,pkg') ftFail('new dir not listed: ' + names(srcLi));

  // Collapsed loaded directory: dropped now, lazy-loaded fresh on next expand.
  srcLi.children[0].listeners.click();            // collapse
  fetched.length = 0;
  fsTree.src = [{ type: 'file', name: 'only.go', path: 'src/only.go' }];
  it.handleFilesChanged({ path: 'src', action: 'rescanned', count: 10 });
  await tick();
  if (fetched.length !== 0) ftFail('collapsed dir must not fetch until expanded: ' + fetched);
  srcLi.children[0].listeners.click();            // expand
  await tick();
  if (names(srcLi).join() !== 'only.go') ftFail('expand after rescan shows stale data: ' + names(srcLi));

  // Nothing rendered above the path: the root listing is what changed.
  fetched.length = 0;
  it.handleFilesChanged({ path: 'brand-new', action: 'rescanned', count: 99 });
  await tick();
  if (fetched.join() !== '.') ftFail('unrendered top-level dir must reload the root: ' + fetched);
}

treeRescanTest().then(function() {
  process.stdout.write(
    'PASS: app.js loaded without error; data-theme="' + _themeAttr +
    '"; DOMContentLoaded registered.\n'
  );
  process.exit(0);
}, function(e) { ftFail(String(e && e.stack || e)); });
