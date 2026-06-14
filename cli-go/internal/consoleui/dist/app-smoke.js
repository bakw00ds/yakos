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

process.stdout.write(
  'PASS: app.js loaded without error; data-theme="' + _themeAttr +
  '"; DOMContentLoaded registered.\n'
);
process.exit(0);
