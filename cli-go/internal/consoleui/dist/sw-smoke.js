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
// Early-return detection: the FetchEvent stub tracks whether respondWith
// was called via an explicit boolean flag — no timing-dependent heuristic.
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

// Request: records url, method, headers, body, redirect for assertions.
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

// ── Helper: dispatch a synthetic FetchEvent.
//
//    Returns a Promise that resolves to { fetched, respondWithCalled } where:
//      fetched           — the Request passed to fetch(), or null if not called
//      respondWithCalled — true iff the SW called event.respondWith()
//
//    Early-return detection uses an explicit boolean flag set inside
//    event.respondWith rather than a timing-dependent setTimeout heuristic.
// ─────────────────────────────────────────────────────────────────────────────

function dispatchFetch(requestOpts) {
  return new Promise(function(resolve, reject) {
    var req = new global.Request(
      requestOpts.url || 'http://127.0.0.1:7890/api/test',
      requestOpts
    );
    // Reset captured fetch target before each dispatch.
    lastFetchedRequest = null;

    var respondWithCalled = false;

    var event = {
      request: req,
      respondWith: function(promiseOrValue) {
        respondWithCalled = true;
        Promise.resolve(promiseOrValue).then(function() {
          resolve({ fetched: lastFetchedRequest, respondWithCalled: true });
        }).catch(reject);
      },
    };

    listeners['fetch'](event);

    // If respondWith was NOT called the SW took an early-return path.
    // Resolve synchronously — no timing assumption needed.
    if (!respondWithCalled) {
      resolve({ fetched: null, respondWithCalled: false });
    }
  });
}

// ── Inject a token (used by all tests except the !token test below) ──────────

listeners['message']({ data: { type: 'SET_TOKEN', token: 'tok-abc123' } });

// ── Test 1: POST with JSON body — body, Authorization, redirect:'error' ───────

var jsonBody = typeof TextEncoder !== 'undefined'
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
}).then(function(result) {
  assert(result.respondWithCalled,
    'POST /api/add: SW must call respondWith()');
  assert(result.fetched !== null,
    'POST /api/add: SW must call fetch()');
  if (result.fetched) {
    var auth = result.fetched.headers.get('Authorization');
    assert(auth === 'Bearer tok-abc123',
      'POST /api/add: Authorization is "Bearer tok-abc123" (got "' + auth + '")');
    assert(result.fetched._body === jsonBody,
      'POST /api/add: body is preserved (got ' + result.fetched._body + ')');
    assert(result.fetched.redirect === 'error',
      'POST /api/add: redirect is "error" (got "' + result.fetched.redirect + '")');
  }

// ── Test 2: GET iframe navigate — auth injected, no body, redirect:'follow' ──

  return dispatchFetch({
    url:         'http://127.0.0.1:7890/kanban/',
    method:      'GET',
    headers:     new global.Headers({}),
    mode:        'navigate',
    destination: 'iframe',
    body:        null,
  });
}).then(function(result) {
  assert(result.respondWithCalled,
    'GET /kanban/ (iframe navigate): SW must call respondWith()');
  assert(result.fetched !== null,
    'GET /kanban/ (iframe navigate): SW must call fetch()');
  if (result.fetched) {
    var auth = result.fetched.headers.get('Authorization');
    assert(auth === 'Bearer tok-abc123',
      'GET /kanban/: Authorization is "Bearer tok-abc123" (got "' + auth + '")');
    assert(result.fetched._body === null || result.fetched._body === undefined,
      'GET /kanban/: no body on GET (got ' + result.fetched._body + ')');
    assert(result.fetched.redirect === 'follow',
      'GET /kanban/: redirect is "follow" (got "' + result.fetched.redirect + '")');
  }

// ── Test 3: Top-level document navigation — SW must NOT intercept ─────────────

  return dispatchFetch({
    url:         'http://127.0.0.1:7890/',
    method:      'GET',
    headers:     new global.Headers({}),
    mode:        'navigate',
    destination: 'document',
    body:        null,
  });
}).then(function(result) {
  assert(!result.respondWithCalled,
    'GET / (top-level navigate): SW must NOT call respondWith() (early-return)');
  assert(result.fetched === null,
    'GET / (top-level navigate): SW must NOT call fetch()');

// ── Test 4: Request already carrying Authorization — passed through ────────────

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
}).then(function(result) {
  assert(!result.respondWithCalled,
    'POST with existing Authorization: SW must NOT call respondWith() (early-return)');
  assert(result.fetched === null,
    'POST with existing Authorization: SW must NOT call fetch()');

// ── Test 5: Cross-origin request — SW must pass through ───────────────────────

  return dispatchFetch({
    url:         'https://example.com/api',
    method:      'POST',
    headers:     new global.Headers({}),
    mode:        'cors',
    destination: '',
    body:        jsonBody,
  });
}).then(function(result) {
  assert(!result.respondWithCalled,
    'Cross-origin POST: SW must NOT call respondWith() (early-return)');
  assert(result.fetched === null,
    'Cross-origin POST: SW must NOT call fetch()');

// ── Test 6: No token — SW must pass through unmodified ────────────────────────
//
//    Simulate the pre-SET_TOKEN state by clearing the token via a fake message
//    that sets it to null, dispatching, then restoring for any later tests.

  listeners['message']({ data: { type: 'SET_TOKEN', token: null } });

  return dispatchFetch({
    url:         'http://127.0.0.1:7890/api/board',
    method:      'GET',
    headers:     new global.Headers({}),
    mode:        'cors',
    destination: '',
    body:        null,
  });
}).then(function(result) {
  // Restore the token before assertions so any accidental further use is safe.
  listeners['message']({ data: { type: 'SET_TOKEN', token: 'tok-abc123' } });

  assert(!result.respondWithCalled,
    'GET /api/board (no token): SW must NOT call respondWith() (early-return)');
  assert(result.fetched === null,
    'GET /api/board (no token): SW must NOT call fetch(); no Authorization injected');

// ── Results ───────────────────────────────────────────────────────────────────

  if (failures.length > 0) {
    process.stderr.write(failures.length + ' assertion(s) failed.\n');
    process.exit(1);
  }
  process.stdout.write(
    'PASS: sw.js smoke tests passed (POST body+redirect preserved, ' +
    'GET redirect:follow, early-returns intact, !token pass-through).\n'
  );
  process.exit(0);
}).catch(function(err) {
  process.stderr.write('FAIL: unexpected error in sw-smoke.js: ' + err + '\n');
  process.stderr.write(err.stack + '\n');
  process.exit(1);
});
