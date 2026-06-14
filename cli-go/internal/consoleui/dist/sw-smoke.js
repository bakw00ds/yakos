// sw-smoke.js — Node.js smoke test for dist/sw.js.
//
// Purpose: verify that the service worker's fetch handler correctly
// injects the Authorization header and — crucially — preserves the
// request body for non-GET/HEAD methods (POST, etc.).
//
// The bug this test guards against: when the SW reconstructed a request
// to inject the auth header it omitted the body, so kanban iframe POSTs
// (api/add, api/move, api/notes, api/delete) arrived at the server with
// an empty body, causing a 400 "title required" error.
//
// Strategy: stub the ServiceWorker globals (self, Headers, Request,
// fetch), load sw.js, then dispatch synthetic FetchEvents and assert
// the reconstructed Request received by fetch() carries both the Bearer
// header and the original body bytes.
//
// This file is invoked by TestSWJSSmokeTest in app_smoke_test.go via:
//   node dist/sw-smoke.js
// Must exit 0 on success, non-zero (+ stderr message) on any failure.

'use strict';

// ── Test helpers ─────────────────────────────────────────────────────────────

var failures = [];
function assert(cond, msg) {
  if (!cond) {
    failures.push(msg);
    process.stderr.write('FAIL: ' + msg + '\n');
  }
}

// ── Minimal ServiceWorker global stubs ───────────────────────────────────────

// Captured event listeners keyed by event type.
var listeners = {};

global.self = {
  skipWaiting: function() {},
  clients: { claim: function() { return Promise.resolve(); } },
  location: { origin: 'http://127.0.0.1:7890' },
  addEventListener: function(type, fn) {
    listeners[type] = fn;
  },
};

// Headers: a thin map wrapper that mirrors the Web API surface used by sw.js.
global.Headers = function(init) {
  this._map = {};
  if (init instanceof global.Headers) {
    var src = init;
    for (var k in src._map) {
      if (Object.prototype.hasOwnProperty.call(src._map, k)) {
        this._map[k] = src._map[k];
      }
    }
  } else if (init && typeof init === 'object') {
    for (var key in init) {
      if (Object.prototype.hasOwnProperty.call(init, key)) {
        this._map[key.toLowerCase()] = init[key];
      }
    }
  }
};
Headers.prototype.get = function(name) {
  return Object.prototype.hasOwnProperty.call(this._map, name.toLowerCase())
    ? this._map[name.toLowerCase()]
    : null;
};
Headers.prototype.set = function(name, value) {
  this._map[name.toLowerCase()] = value;
};

// Track the last Request constructed by sw.js when calling fetch().
var lastFetchedRequest = null;

// Request: records url, method, headers, body for assertions.
global.Request = function(url, init) {
  this.url = url;
  this.method = (init && init.method) || 'GET';
  this.headers = (init && init.headers) || new global.Headers();
  this.mode = (init && init.mode) || 'cors';
  this.credentials = (init && init.credentials) || 'same-origin';
  this.redirect = (init && init.redirect) || 'follow';
  this.destination = (init && init.destination) || '';
  this._body = (init && init.body) || null;

  // clone() is used by sw.js to read the body without consuming it.
  this.clone = function() {
    var self = this;
    return {
      arrayBuffer: function() {
        return Promise.resolve(self._body || new ArrayBuffer(0));
      }
    };
  };
};

// fetch() stub: captures the request and returns a resolved Response-like.
global.fetch = function(req) {
  lastFetchedRequest = req;
  return Promise.resolve({ status: 200, ok: true });
};

// URL stub: parses origin from a full URL string.
global.URL = function(href) {
  var m = /^(https?:\/\/[^/]+)/.exec(href);
  this.origin = m ? m[1] : '';
};

// ── Load sw.js ───────────────────────────────────────────────────────────────

try {
  require('./sw.js');
} catch (e) {
  process.stderr.write('FAIL: sw.js threw during load: ' + e + '\n');
  process.stderr.write(e.stack + '\n');
  process.exit(1);
}

// Assert the expected handlers are registered.
assert(typeof listeners['install']  === 'function', 'install listener registered');
assert(typeof listeners['activate'] === 'function', 'activate listener registered');
assert(typeof listeners['message']  === 'function', 'message listener registered');
assert(typeof listeners['fetch']    === 'function', 'fetch listener registered');

// ── Inject a token ───────────────────────────────────────────────────────────

listeners['message']({ data: { type: 'SET_TOKEN', token: 'tok-abc123' } });

// ── Helper: dispatch a synthetic FetchEvent and return a Promise that
//    resolves once the respondWith callback settles. ─────────────────────────

function dispatchFetch(requestOpts) {
  return new Promise(function(resolve, reject) {
    var req = new global.Request(
      requestOpts.url || 'http://127.0.0.1:7890/api/test',
      requestOpts
    );
    // Reset captured request before each dispatch.
    lastFetchedRequest = null;

    var event = {
      request: req,
      respondWith: function(promiseOrValue) {
        Promise.resolve(promiseOrValue).then(function() {
          resolve(lastFetchedRequest);
        }).catch(reject);
      },
      // SW may return early (no respondWith call) — treat as pass-through.
    };

    var result = listeners['fetch'](event);

    // If respondWith was never called (early-return path), resolve with null.
    if (lastFetchedRequest === null && result === undefined) {
      // Give async paths (like the body-cloning IIFE) a tick to settle.
      setTimeout(function() {
        resolve(lastFetchedRequest);
      }, 50);
    }
  });
}

// ── Test 1: POST with JSON body — body AND Authorization must be preserved ───

var jsonBody = new TextEncoder
  ? new TextEncoder().encode(JSON.stringify({ title: 'my task' })).buffer
  : Buffer.from(JSON.stringify({ title: 'my task' }));

var postHeaders = new global.Headers({ 'Content-Type': 'application/json' });

dispatchFetch({
  url:         'http://127.0.0.1:7890/api/add',
  method:      'POST',
  headers:     postHeaders,
  mode:        'cors',
  destination: '',
  body:        jsonBody,
}).then(function(fetched) {
  assert(fetched !== null, 'POST /api/add: SW called fetch() (not passed through)');
  if (fetched) {
    var auth = fetched.headers.get('Authorization');
    assert(auth === 'Bearer tok-abc123',
      'POST /api/add: Authorization header is "Bearer tok-abc123" (got "' + auth + '")');

    // The body must be the same ArrayBuffer we supplied.
    assert(fetched._body === jsonBody,
      'POST /api/add: body is preserved (got ' + fetched._body + ')');
  }

// ── Test 2: GET (navigate iframe) — body must NOT be set, header injected ───

  return dispatchFetch({
    url:         'http://127.0.0.1:7890/kanban/',
    method:      'GET',
    headers:     new global.Headers({}),
    mode:        'navigate',
    destination: 'iframe',
    body:        null,
  });
}).then(function(fetched) {
  assert(fetched !== null, 'GET /kanban/ (iframe navigate): SW called fetch()');
  if (fetched) {
    var auth = fetched.headers.get('Authorization');
    assert(auth === 'Bearer tok-abc123',
      'GET /kanban/: Authorization header is "Bearer tok-abc123" (got "' + auth + '")');

    assert(fetched._body === null || fetched._body === undefined,
      'GET /kanban/: no body on GET (got ' + fetched._body + ')');
  }

// ── Test 3: Top-level document navigation — SW must pass through unmodified ──

  return dispatchFetch({
    url:         'http://127.0.0.1:7890/',
    method:      'GET',
    headers:     new global.Headers({}),
    mode:        'navigate',
    destination: 'document',
    body:        null,
  });
}).then(function(fetched) {
  // Early-return path: no respondWith, SW lets the browser handle it.
  assert(fetched === null,
    'GET / (top-level navigate): SW must NOT intercept (fetched=' + fetched + ')');

// ── Test 4: Request already carrying Authorization — passed through ───────────

  var authedHeaders = new global.Headers({
    'Authorization': 'Bearer already-set',
    'Content-Type':  'application/json',
  });
  return dispatchFetch({
    url:         'http://127.0.0.1:7890/api/some',
    method:      'POST',
    headers:     authedHeaders,
    mode:        'cors',
    destination: '',
    body:        jsonBody,
  });
}).then(function(fetched) {
  assert(fetched === null,
    'POST with existing Authorization: SW must NOT re-wrap (fetched=' + fetched + ')');

// ── Test 5: Cross-origin request — SW must pass through ──────────────────────

  return dispatchFetch({
    url:         'https://example.com/api',
    method:      'POST',
    headers:     new global.Headers({}),
    mode:        'cors',
    destination: '',
    body:        jsonBody,
  });
}).then(function(fetched) {
  assert(fetched === null,
    'Cross-origin POST: SW must NOT intercept (fetched=' + fetched + ')');

// ── Results ──────────────────────────────────────────────────────────────────

  if (failures.length > 0) {
    process.stderr.write(failures.length + ' assertion(s) failed.\n');
    process.exit(1);
  }
  process.stdout.write(
    'PASS: sw.js smoke tests passed (POST body preserved, GET passthrough, ' +
    'early-returns intact).\n'
  );
  process.exit(0);
}).catch(function(err) {
  process.stderr.write('FAIL: unexpected error in sw-smoke.js: ' + err + '\n');
  process.stderr.write(err.stack + '\n');
  process.exit(1);
});
