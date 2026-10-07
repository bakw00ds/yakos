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
  require('./chat-routing.js');
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

// ── K-132 (P0a): runtime pins — pane runtime/model selectors + dispatch body ──
function chatFail(m) { process.stderr.write('FAIL: chat-pane: ' + m + '\n'); process.exit(1); }
function chatPaneTest() {
  var cp = global.__yakosChatPanes;
  if (!cp) chatFail('window.__yakosChatPanes not exposed');
  function same(what, got, want) {
    if (JSON.stringify(got) !== JSON.stringify(want)) {
      chatFail(what + ': got ' + JSON.stringify(got) + ', want ' + JSON.stringify(want));
    }
  }
  function has(what, hay, needle) {
    if (hay.indexOf(needle) < 0) chatFail(what + ': missing ' + needle + ' in ' + hay);
  }
  function lacks(what, hay, needle) {
    if (hay.indexOf(needle) >= 0) chatFail(what + ': unexpected ' + needle + ' in ' + hay);
  }
  function mkPane(id, runtime, model) {
    var p = cp.makePane(id, 'conv-' + id);
    if (runtime !== undefined) p.runtime = runtime;
    if (model !== undefined) p.model = model;
    return p;
  }
  // The model <select> contents alone, so "no opus" cannot be satisfied by another control.
  function modelSelect(html) {
    var m = /<select class="pane-model-select"[^>]*>([\s\S]*?)<\/select>/.exec(html);
    if (!m) chatFail('no model select in header: ' + html);
    return m[1];
  }

  // Constants: 'auto' first (the default), 'gemini' gone.
  same('RUNTIMES', cp.RUNTIMES, ['auto', 'claude', 'codex', 'agy']);
  same('MODEL_TIERS', cp.MODEL_TIERS, ['haiku', 'sonnet', 'opus', 'fable']);
  same('MODEL_ALIASES', cp.MODEL_ALIASES, ['cheap', 'balanced', 'best', 'reasoning', 'frontier']);

  // New panes follow the agent's own pin: runtime 'auto', no model override.
  var fresh = mkPane('smoke-fresh');
  same('makePane defaults', [fresh.runtime, fresh.model], ['auto', '']);

  // modelOptionsFor: tiers are claude-only; aliases are valid on every runtime.
  ['claude', 'codex', 'agy', 'auto', 'gemini', undefined].forEach(function(r) {
    var o = cp.modelOptionsFor(r);
    same('modelOptionsFor(' + r + ') leads with the default', o[0], '');
    if (o.indexOf('balanced') < 0) chatFail('modelOptionsFor(' + r + ') lacks the balanced alias: ' + o);
    if ((o.indexOf('opus') >= 0) !== (r === 'claude')) chatFail('modelOptionsFor(' + r + ') has the wrong tier set: ' + o);
  });

  // normalizePaneItem: a persisted pane from an older build is made valid again.
  [
    // [label, persisted record, runtime, model]
    ['stale gemini pane', { runtime: 'gemini', model: 'opus' }, 'auto', ''],
    ['codex pane holding a claude tier', { runtime: 'codex', model: 'opus' }, 'codex', ''],
    ['codex pane keeps an alias', { runtime: 'codex', model: 'balanced' }, 'codex', 'balanced'],
    ['claude pane keeps its tier', { runtime: 'claude', model: 'opus' }, 'claude', 'opus'],
    ['claude pane keeps an alias', { runtime: 'claude', model: 'cheap' }, 'claude', 'cheap'],
    ['legacy claude/sonnet pane', { runtime: 'claude', model: 'sonnet' }, 'claude', 'sonnet'],
    ['auto pane holding a tier', { runtime: 'auto', model: 'sonnet' }, 'auto', ''],
    ['missing runtime', { model: 'balanced' }, 'auto', 'balanced'],
    ['unknown model id', { runtime: 'claude', model: 'gpt-5' }, 'claude', ''],
    ['non-string fields', { runtime: 7, model: {} }, 'auto', ''],
    ['empty record', {}, 'auto', ''],
    ['null record', null, 'auto', ''],
  ].forEach(function(c) {
    var n = cp.normalizePaneItem(c[1]);
    same('normalizePaneItem: ' + c[0], [n.runtime, n.model], [c[2], c[3]]);
  });
  function rest(n) { return [n.agent, n.effort, n.interactive]; }
  same('normalizePaneItem defaults', rest(cp.normalizePaneItem({})), ['claude', '', false]);
  same('normalizePaneItem keeps agent/effort/interactive',
    rest(cp.normalizePaneItem({ agent: 'reviewer', effort: 'high', interactive: true })), ['reviewer', 'high', true]);
  same('normalizePaneItem rejects an unknown effort', cp.normalizePaneItem({ effort: 'extreme' }).effort, '');
  // Every selection the header can offer survives a save + reload unchanged.
  cp.RUNTIMES.forEach(function(r) {
    cp.modelOptionsFor(r).forEach(function(m) {
      var n = cp.normalizePaneItem({ runtime: r, model: m });
      same('reload round-trip ' + r + '/' + (m || 'default'), [n.runtime, n.model], [r, m]);
    });
  });

  // Restore from localStorage goes through normalizePaneItem: a pane persisted
  // by an older build (gemini, or a claude tier on a codex pane) comes back valid.
  localStorage.setItem('yakos_chat_panes_v1', JSON.stringify([
    { id: 'p-old', conversationId: 'c-old', runtime: 'gemini', model: 'opus', agent: 'claude' },
    { id: 'p-mix', conversationId: 'c-mix', runtime: 'codex', model: 'opus', agent: 'reviewer', effort: 'high', interactive: true },
    { id: 'p-ok', conversationId: 'c-ok', runtime: 'claude', model: 'sonnet' },
    { id: 'p-new', conversationId: 'c-new', runtime: 'auto', model: 'balanced' },
    { runtime: 'codex', model: 'balanced' },
  ]));
  var restored = cp.loadPanes();
  localStorage.removeItem('yakos_chat_panes_v1');
  same('restored panes', restored.map(function(p) { return [p.id, p.runtime, p.model, p.agent, p.effort, p.interactive]; }), [
    ['p-old', 'auto', '', 'claude', '', false],
    ['p-mix', 'codex', '', 'reviewer', 'high', true],
    ['p-ok', 'claude', 'sonnet', 'claude', '', false],
    ['p-new', 'auto', 'balanced', 'claude', '', false],
  ]);

  // Header: runtime options come from RUNTIMES, model options from the runtime.
  var autoHtml = cp.buildPaneHeaderHTML(fresh);
  has('default header', autoHtml, '<option value="auto" selected title="');
  has('default header', autoHtml, '<option value="claude">claude</option>');
  has('default header', autoHtml, 'value="codex"');
  has('default header', autoHtml, 'value="agy"');
  lacks('default header', autoHtml, 'value="gemini"');
  has('default header', autoHtml, 'aria-label="Model">');
  var autoModels = modelSelect(autoHtml);
  has('default pane model options', autoModels, '<option value="" selected>default</option>');
  has('default pane model options', autoModels, 'value="balanced"');
  lacks('default pane model options', autoModels, 'value="opus"');
  var codexHtml = cp.buildPaneHeaderHTML(mkPane('smoke-codex', 'codex', 'balanced'));
  has('codex header', codexHtml, '<option value="codex" selected');
  var codexModels = modelSelect(codexHtml);
  has('codex model options', codexModels, '<option value="">default</option>');
  has('codex model options', codexModels, '<option value="balanced" selected>balanced</option>');
  lacks('codex model options', codexModels, 'value="opus"');
  var claudeModels = modelSelect(cp.buildPaneHeaderHTML(mkPane('smoke-claude', 'claude', 'opus')));
  has('claude model options', claudeModels, '<option value="opus" selected>opus</option>');
  has('claude model options', claudeModels, 'value="balanced"');
  // 'auto' counts as a streaming runtime: the idle cost label is the en-dash,
  // not "cost unavailable" (the real runtime is unknown until dispatch).
  has('auto pane cost label', autoHtml, 'id="pane-cost-smoke-fresh">–</span>');
  has('codex pane cost label', codexHtml, 'id="pane-cost-smoke-codex">cost unavailable</span>');

  // Runtime change: a model the new runtime rejects resets to default and one it
  // still accepts is kept; the pick is persisted and the header re-rendered so
  // the model select shows the new runtime's options.  A fake header + runtime
  // select stand in for the DOM.
  var fakeHeader = { innerHTML: '' };
  var fakeSel = { focused: 0, addEventListener: function() {}, focus: function() { this.focused++; } };
  var realGetById = document.getElementById;
  document.getElementById = function(id) {
    if (id === 'pane-header-smoke-rt') return fakeHeader;
    if (id === 'pane-runtime-smoke-rt') return fakeSel;
    return null;
  };
  try {
    var rt = mkPane('smoke-rt', 'claude', 'opus');
    cp.changeRuntime(rt, 'codex');
    same('claude->codex', [rt.runtime, rt.model], ['codex', '']);
    has('header re-rendered', fakeHeader.innerHTML, '<option value="codex" selected');
    lacks('re-rendered model options', modelSelect(fakeHeader.innerHTML), 'value="opus"');
    var savedRaw = localStorage.getItem('yakos_chat_panes_v1');
    if (!savedRaw) chatFail('runtime change did not persist the pane state');
    var saved = JSON.parse(savedRaw);
    same('persisted pane', [saved[0].id, saved[0].runtime, saved[0].model], ['smoke-rt', 'codex', '']);
    rt.model = 'balanced';
    cp.changeRuntime(rt, 'agy');
    same('codex->agy keeps an alias', [rt.runtime, rt.model], ['agy', 'balanced']);
    cp.changeRuntime(rt, 'claude');
    same('agy->claude keeps an alias', [rt.runtime, rt.model], ['claude', 'balanced']);
    rt.model = 'haiku';
    cp.changeRuntime(rt, 'claude');
    same('re-picking claude keeps its tier', [rt.runtime, rt.model], ['claude', 'haiku']);
    cp.changeRuntime(rt, 'auto');
    same('claude->auto drops a tier', [rt.runtime, rt.model], ['auto', '']);
    // Re-rendering replaces the runtime select, so focus must go back to it, but
    // only when it was the focused control.
    document.activeElement = { id: 'pane-runtime-smoke-rt' };
    cp.changeRuntime(rt, 'codex');
    same('focus restored to the runtime select', fakeSel.focused, 1);
    document.activeElement = { id: 'pane-model-smoke-rt' };
    cp.changeRuntime(rt, 'agy');
    same('focus not taken from another control', fakeSel.focused, 1);
  } finally {
    document.getElementById = realGetById;
    delete document.activeElement;
    localStorage.removeItem('yakos_chat_panes_v1');
  }

  // Dispatch body: 'auto' is sent as '' so the server resolves the runtime.
  var b = cp.buildDispatchBody(fresh, 'write tests', 'sess-1');
  same('auto pane dispatches runtime ""', b.runtime, '');
  same('default pane dispatches model ""', b.model, '');
  same('base dispatch fields', Object.keys(b).sort(),
    ['agent', 'conversationId', 'model', 'operatorId', 'runtime', 'sessionId', 'task']);
  same('carried dispatch fields', [b.agent, b.task, b.sessionId, b.conversationId],
    ['claude', 'write tests', 'sess-1', 'conv-smoke-fresh']);
  if (typeof b.operatorId !== 'string' || !b.operatorId) chatFail('dispatch body lacks operatorId: ' + b.operatorId);
  [['claude', 'opus'], ['codex', 'balanced'], ['agy', '']].forEach(function(c) {
    var d = cp.buildDispatchBody(mkPane('smoke-d-' + c[0], c[0], c[1]), 't', 's');
    same(c[0] + ' pane dispatches its runtime and model', [d.runtime, d.model], c);
  });
  var ep = mkPane('smoke-eff', 'claude', 'sonnet');
  ep.effort = 'high';
  ep.interactive = true;
  var eb = cp.buildDispatchBody(ep, 't', 's');
  same('effort + interactive are sent when set', [eb.effort, eb.interactive], ['high', true]);
  ep.effort = '';
  ep.interactive = false;
  eb = cp.buildDispatchBody(ep, 't', 's');
  same('effort + interactive are omitted when unset', ['effort' in eb, 'interactive' in eb], [false, false]);
  // worktreeMode is IDE-pane-only and needs review mode (off here): never sent.
  ep.ideEmbedded = true;
  same('no worktreeMode without review mode', 'worktreeMode' in cp.buildDispatchBody(ep, 't', 's'), false);

  // Send path: sendPaneMessage must dispatch the buildDispatchBody result, and
  // show the "tool output not available" notice for an explicit non-claude
  // runtime only ('auto' may resolve to claude, so it claims nothing).
  var posted = [];
  var realFetch = global.fetch;
  global.fetch = function(url, o) {
    posted.push({ url: url, body: JSON.parse(o.body) });
    return Promise.resolve({ ok: true, status: 202, headers: { get: function() { return null; } } });
  };
  var realGetById2 = document.getElementById;
  document.getElementById = function(id) { return /^pane-input-/.test(id) ? { value: 'ship it' } : null; };
  try {
    [['auto', '', 0], ['claude', 'claude', 0], ['codex', 'codex', 1], ['agy', 'agy', 1]].forEach(function(c) {
      var sp = mkPane('smoke-s-' + c[0], c[0], '');
      cp.sendPaneMessage(sp);
      clearInterval(sp.elapsedTimer);
      var last = posted[posted.length - 1];
      same(c[0] + ' send posts to the dispatch endpoint', last && last.url, '/api/chat/dispatch');
      same(c[0] + ' send posts the runtime the server should resolve', [last.body.runtime, last.body.task, last.body.conversationId],
        [c[1], 'ship it', 'conv-smoke-s-' + c[0]]);
      same(c[0] + ' send shows the buffered-runtime notice only for explicit non-claude runtimes',
        sp.messages.filter(function(m) { return m._isRuntimeAffordance; }).length, c[2]);
    });
  } finally {
    global.fetch = realFetch;
    document.getElementById = realGetById2;
  }
}
try { chatPaneTest(); } catch (e) { chatFail(String(e && e.stack || e)); }


// ── K-148: routing module — @prefix, registry selects, mode, route chip, banner ──
function rtFail(m) { process.stderr.write('FAIL: chat-routing: ' + m + '\n'); process.exit(1); }
function routingTest() {
  var cp = global.__yakosChatPanes, yr = global.YakChatRouting;
  if (!yr) rtFail('window.YakChatRouting not exposed');
  function same(what, got, want) {
    if (JSON.stringify(got) !== JSON.stringify(want)) rtFail(what + ': got ' + JSON.stringify(got) + ', want ' + JSON.stringify(want));
  }
  function has(what, hay, needle) { if (hay.indexOf(needle) < 0) rtFail(what + ': missing ' + needle + ' in ' + hay); }
  function lacks(what, hay, needle) { if (hay.indexOf(needle) >= 0) rtFail(what + ': unexpected ' + needle + ' in ' + hay); }

  // @prefix: only a well-formed prefix with a task is an override.
  same('plain runtime', yr.parseOverride('@codex fix the bug'), { runtime: 'codex', model: '', task: 'fix the bug' });
  same('runtime and model', yr.parseOverride('@codex:gpt-5 fix it'), { runtime: 'codex', model: 'gpt-5', task: 'fix it' });
  same('model with dots and colon', yr.parseOverride('  @agy:gemini-3.8-flash-high:x go\nmore').model, 'gemini-3.8-flash-high:x');
  same('multi-line task kept', yr.parseOverride('@claude a\nb').task, 'a\nb');
  ['@codex', '@codex   ', '@gemini hi', '@Codex hi', '@codexx hi', '@codex:Bad hi', '@codex: hi', '@codex:<img> hi',
   'hello @codex hi', '', null, undefined, '@codex:' + 'a'.repeat(65) + ' hi'].forEach(function(t) {
    same('not an override: ' + JSON.stringify(t), yr.parseOverride(t), null);
  });

  // The dispatch body: an override adds two fields, no override adds none.
  var pane = cp.makePane('rt-1', 'conv-rt-1');
  pane.runtime = 'claude'; pane.model = 'opus';
  var plain = cp.buildDispatchBody(pane, 't', 's');
  same('no override: no routing fields', ['overrideRuntime' in plain, 'overrideModel' in plain], [false, false]);
  var ob = cp.buildDispatchBody(pane, 't', 's', yr.parseOverride('@codex:gpt-5 x'));
  same('override fields', [ob.overrideRuntime, ob.overrideModel, ob.runtime, ob.model], ['codex', 'gpt-5', 'claude', 'opus']);
  same('override without a model omits it', 'overrideModel' in cp.buildDispatchBody(pane, 't', 's', yr.parseOverride('@agy x')), false);

  // Send path: the prefix is stripped from the task and the override rides along.
  var posted = [];
  var realFetch = global.fetch, realGet = document.getElementById;
  global.fetch = function(url, o) {
    posted.push({ url: url, body: JSON.parse(o.body) });
    return Promise.resolve({ ok: true, status: 202, headers: { get: function() { return null; } } });
  };
  var typed = '@codex:gpt-5 fix it';
  document.getElementById = function(id) { return /^pane-input-/.test(id) ? { value: typed } : null; };
  try {
    var sp = cp.makePane('rt-send', 'conv-rt-send');
    cp.sendPaneMessage(sp);
    clearInterval(sp.elapsedTimer);
    var last = posted[posted.length - 1];
    same('prefix stripped, override sent', [last.body.task, last.body.overrideRuntime, last.body.overrideModel, last.body.runtime],
      ['fix it', 'codex', 'gpt-5', '']);
    same('the user turn shows what was typed', sp.messages[0].text, typed);
    typed = 'hello @codex there';
    var sp2 = cp.makePane('rt-send2', 'conv-rt-send2');
    cp.sendPaneMessage(sp2);
    clearInterval(sp2.elapsedTimer);
    last = posted[posted.length - 1];
    same('an inner @ is not an override', ['overrideRuntime' in last.body, last.body.task], [false, 'hello @codex there']);
    // A live interactive pane cannot switch runtime: nothing is sent.
    typed = '@agy hi';
    var n = posted.length;
    var ip = cp.makePane('rt-int', 'conv-rt-int');
    ip.interactive = true; ip.interactiveLive = true;
    cp.sendPaneMessage(ip);
    same('live interactive pane refuses an override', posted.length, n);
    same('and says why', ip.messages.length === 1 && ip.messages[0].role, 'system');
  } finally {
    global.fetch = realFetch;
    document.getElementById = realGet;
  }

  // Registry-driven selects: ids come from /api/models; unusable and malformed
  // ones never reach an <option>.
  same('no registry: static list', cp.modelOptionsFor('codex').indexOf('gpt-5.6-sol'), -1);
  yr._setModels([
    { id: 'gpt-5.6-sol', harness: 'codex', usable: true },
    { id: 'gpt-reserve', harness: 'codex', usable: false },
    { id: 'gemini-3.8-flash-high', harness: 'agy', usable: true },
    { id: 'haiku', harness: 'claude', usable: true },
    { id: '"><img src=x onerror=alert(1)>', harness: 'codex', usable: true },
    { id: 'UPPER', harness: 'codex', usable: true },
    { id: 'x', harness: 'gemini', usable: true },
  ]);
  var codexOpts = cp.modelOptionsFor('codex');
  same('codex options from the registry', codexOpts.slice(0, 2), ['', 'gpt-5.6-sol']);
  same('unusable and malformed ids are dropped', codexOpts.filter(function(m) { return /reserve|img|UPPER/.test(m); }), []);
  same('aliases still offered', codexOpts.indexOf('balanced') >= 0, true);
  same('agy options', cp.modelOptionsFor('agy').indexOf('gemini-3.8-flash-high') >= 0, true);
  same('claude keeps the registry tiers', cp.modelOptionsFor('claude').indexOf('haiku') >= 0, true);
  same('auto stays on the static list', cp.modelOptionsFor('auto').indexOf('gpt-5.6-sol'), -1);
  var cpane = cp.makePane('rt-hdr', 'conv-rt-hdr');
  cpane.runtime = 'codex'; cpane.model = 'gpt-5.6-sol';
  var hdr = cp.buildPaneHeaderHTML(cpane);
  has('header offers the registry id', hdr, '<option value="gpt-5.6-sol" selected>gpt-5.6-sol</option>');
  lacks('header never carries a raw tag from the registry', hdr, '<img');
  has('header shows the routing mode', hdr, 'pane-route-mode-pinned');
  yr._setModels(null);

  // Pane routing mode: auto | runtime | pinned.
  same('mode auto', yr.routeMode({ runtime: 'auto', model: 'balanced' }), 'auto');
  same('mode runtime', yr.routeMode({ runtime: 'codex', model: '' }), 'runtime');
  same('mode pinned', yr.routeMode({ runtime: 'codex', model: 'gpt-5' }), 'pinned');
  has('badge for a default pane', cp.buildPaneHeaderHTML(cp.makePane('rt-b', 'c')), 'pane-route-mode-auto');

  // Route chip and handoff banner: server text goes in as text, never as markup.
  function fakeDoc() {
    return { createElement: function(tag) {
      var e = { tag: tag, className: '', attrs: {}, children: [], textContent: '',
        setAttribute: function(k, v) { this.attrs[k] = String(v); },
        appendChild: function(c) { this.children.push(c); } };
      Object.defineProperty(e, 'innerHTML', { set: function() { rtFail('innerHTML written by the routing module'); }, get: function() { return ''; } });
      return e;
    } };
  }
  var evil = '<img src=x onerror=alert(1)>';
  var chip = yr.buildElement({ role: 'route', route: { runtime: 'codex', model: 'gpt-5', reason: evil, rule_id: 'R1', pinned: 'override', fallback_from: 'claude' } }, fakeDoc());
  var chipText = chip.children.map(function(c) { return c.textContent; }).join('');
  has('chip says where', chipText, 'codex / gpt-5');
  has('chip says who set it', chipText, 'set by @prefix');
  has('chip says why (as text)', chipText, evil);
  has('chip names the fallback', chipText, 'fell back from claude');
  has('chip names the rule', chipText, '[R1]');
  same('chip title holds the reason', chip.attrs.title, evil);
  var banner = yr.buildElement({ role: 'handoff', handoff: { from: evil, to: 'codex', turns: 3, digest_bytes: 812, redactions: 1 } }, fakeDoc());
  has('banner wording', banner.textContent, 'Context reset (cache): moved from ' + evil + ' to codex');
  has('banner counts', banner.textContent, '3 earlier turns (812 bytes, 1 secret-like value redacted)');
  same('other roles are not ours', yr.buildElement({ role: 'assistant', text: 'x' }, fakeDoc()), null);

  // SSE events and transcript turns become the same messages; route lands before tokens.
  var p = cp.makePane('rt-sse', 'conv-rt-sse');
  cp.handleSSE(p, { type: 'route', session_id: 's1', ts: 't0', route: { runtime: 'agy', reason: 'r', pinned: 'router' } });
  cp.handleSSE(p, { type: 'handoff', session_id: 's1', ts: 't1', handoff: { from: 'claude', to: 'agy', turns: 1, digest_bytes: 10, redactions: 0 } });
  cp.handleSSE(p, { type: 'token', session_id: 's1', ts: 't2', text: 'hi' });
  same('message order', p.messages.map(function(m) { return m.role; }), ['route', 'handoff', 'assistant']);
  cp.handleSSE(p, { type: 'route', session_id: 's1' });  // malformed: no route object
  same('a route event without data adds nothing', p.messages.length, 3);
  var tm = yr.fromTranscript({ role: 'route', ts: 't', session_id: 's', runtime: 'codex', model: 'gpt-5', text: 'why', rule_id: 'R2', pinned: 'pane' });
  same('transcript route turn', [tm.role, tm.route.runtime, tm.route.model, tm.route.reason, tm.route.rule_id, tm.route.pinned], ['route', 'codex', 'gpt-5', 'why', 'R2', 'pane']);
  same('other transcript roles are ignored', yr.fromTranscript({ role: 'assistant' }), null);
  var el = cp.buildMessageElement(p.messages[0], p, 'rt-sse');
  same('app.js renders the chip through the module', el.className, 'chat-msg chat-route-chip');
}
try { routingTest(); } catch (e) { rtFail(String(e && e.stack || e)); }

treeRescanTest().then(function() {
  process.stdout.write(
    'PASS: app.js loaded without error; data-theme="' + _themeAttr +
    '"; DOMContentLoaded registered.\n'
  );
  process.exit(0);
}, function(e) { ftFail(String(e && e.stack || e)); });
