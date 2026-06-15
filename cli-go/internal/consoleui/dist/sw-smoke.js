// sw-smoke.js — Node.js smoke test for dist/sw.js.
//
// Purpose: verify that the service worker's fetch handler correctly
// injects the Authorization header and — crucially — preserves the
// request body for non-GET/HEAD methods (POST, etc.) in bearer mode;
// AND that in session mode the SW passes through GET/HEAD requests
// untouched and injects X-CSRF-Token on mutations without rebuilding
// the request (so the session cookie is not stripped).
//
// Tests:
//   bearer mode (unchanged from Phase 3c):
//     1. POST with JSON body — Authorization + body + redirect:'error'
//     2. GET iframe navigate — Authorization + no body + redirect:'follow'
//     3. Top-level document navigation — SW must NOT intercept
//     4. Request already carrying Authorization — passed through
//     5. Cross-origin request — passed through
//     6. No token — passed through unmodified
//
//   session mode (new in Phase 3d):
//     7. GET in session mode — SW must NOT intercept (pass through)
//     8. GET navigate (iframe) in session mode — SW must NOT intercept
//     9. POST mutation in session mode — SW injects X-CSRF-Token;
//        does NOT set Authorization; credentials:'same-origin';
//        redirect:'error'; body preserved
//    10. POST mutation in session mode, no CSRF token yet — passed through
//        (server will 403; better than mangling credentials)
//    11. Top-level navigate in session mode — SW must NOT intercept
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

// source: {} simulates a WindowClient — truthy, satisfying the origin guard
// that requires e.source to be non-null.  e.origin is omitted so the
// "e.origin && e.origin !== self.location.origin" check is falsy (passes).
listeners['message']({ data: { type: 'SET_TOKEN', token: 'tok-abc123' }, source: {} });

// ── Test body buffers (module-scope so they're accessible across all .then()) ──

var jsonBody = typeof TextEncoder !== 'undefined'
  ? new TextEncoder().encode(JSON.stringify({ title: 'my task' })).buffer
  : Buffer.from(JSON.stringify({ title: 'my task' }));

// sessionBody is used in session-mode tests (tests 9 and 10).
var sessionBody = typeof TextEncoder !== 'undefined'
  ? new TextEncoder().encode(JSON.stringify({ title: 'task' })).buffer
  : Buffer.from(JSON.stringify({ title: 'task' }));

var postHeaders = new global.Headers({ 'Content-Type': 'application/json' });

// ── Test 1: POST with JSON body — body, Authorization, redirect:'error' ───────

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

  listeners['message']({ data: { type: 'SET_TOKEN', token: null }, source: {} });

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
  // source:{} satisfies the origin guard in sw.js (e.source must be truthy).
  listeners['message']({ data: { type: 'SET_TOKEN', token: 'tok-abc123' }, source: {} });

  assert(!result.respondWithCalled,
    'GET /api/board (no token): SW must NOT call respondWith() (early-return)');
  assert(result.fetched === null,
    'GET /api/board (no token): SW must NOT call fetch(); no Authorization injected');

// ── Switch to session mode for tests 7–11 ────────────────────────────────────

  listeners['message']({ data: { type: 'SET_AUTH_MODE', mode: 'session' }, source: {} });
  listeners['message']({ data: { type: 'SET_CSRF_TOKEN', token: 'csrf-xyz789' }, source: {} });

// ── Test 7: Session mode — GET request must pass through (no intercept) ───────

  return dispatchFetch({
    url:         'http://127.0.0.1:7890/api/presence',
    method:      'GET',
    headers:     new global.Headers({}),
    mode:        'cors',
    destination: '',
    body:        null,
  });
}).then(function(result) {
  assert(!result.respondWithCalled,
    'session GET /api/presence: SW must NOT call respondWith() — pass through for cookie');
  assert(result.fetched === null,
    'session GET /api/presence: SW must NOT call fetch() — let browser send cookie');

// ── Test 8: Session mode — GET iframe navigate must pass through ──────────────

  return dispatchFetch({
    url:         'http://127.0.0.1:7890/kanban/',
    method:      'GET',
    headers:     new global.Headers({}),
    mode:        'navigate',
    destination: 'iframe',
    body:        null,
  });
}).then(function(result) {
  assert(!result.respondWithCalled,
    'session GET /kanban/ (iframe navigate): SW must NOT call respondWith()');
  assert(result.fetched === null,
    'session GET /kanban/ (iframe navigate): SW must NOT call fetch() — cookie rides automatically');

// ── Test 9: Session mode — POST mutation gets X-CSRF-Token + same-origin creds ─
//    sessionBody is declared at module scope (above) so test 10 can reuse it.

  return dispatchFetch({
    url:         'http://127.0.0.1:7890/api/add',
    method:      'POST',
    headers:     new global.Headers({ 'Content-Type': 'application/json' }),
    mode:        'cors',
    destination: '',
    body:        sessionBody,
  });
}).then(function(result) {
  assert(result.respondWithCalled,
    'session POST /api/add: SW must call respondWith()');
  assert(result.fetched !== null,
    'session POST /api/add: SW must call fetch()');
  if (result.fetched) {
    var csrf = result.fetched.headers.get('X-CSRF-Token');
    assert(csrf === 'csrf-xyz789',
      'session POST /api/add: X-CSRF-Token is "csrf-xyz789" (got "' + csrf + '")');
    var auth = result.fetched.headers.get('Authorization');
    assert(auth === null || auth === undefined || auth === '',
      'session POST /api/add: Authorization must NOT be set in session mode (got "' + auth + '")');
    assert(result.fetched.credentials === 'same-origin',
      'session POST /api/add: credentials must be "same-origin" (got "' + result.fetched.credentials + '")');
    assert(result.fetched.redirect === 'error',
      'session POST /api/add: redirect must be "error" (got "' + result.fetched.redirect + '")');
    assert(result.fetched._body === sessionBody,
      'session POST /api/add: body must be preserved');
  }

// ── Test 10: Session mode — POST with no CSRF token must pass through ─────────

  listeners['message']({ data: { type: 'SET_CSRF_TOKEN', token: null }, source: {} });

  return dispatchFetch({
    url:         'http://127.0.0.1:7890/api/add',
    method:      'POST',
    headers:     new global.Headers({ 'Content-Type': 'application/json' }),
    mode:        'cors',
    destination: '',
    body:        sessionBody,
  });
}).then(function(result) {
  // Restore CSRF token.
  listeners['message']({ data: { type: 'SET_CSRF_TOKEN', token: 'csrf-xyz789' }, source: {} });

  assert(!result.respondWithCalled,
    'session POST (no CSRF token): SW must NOT call respondWith() — pass through (server will 403)');
  assert(result.fetched === null,
    'session POST (no CSRF token): SW must NOT call fetch()');

// ── Test 11: Session mode — top-level document navigate must NOT be intercepted ─

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
    'session GET / (top-level navigate): SW must NOT call respondWith() (early-return)');
  assert(result.fetched === null,
    'session GET / (top-level navigate): SW must NOT call fetch()');

// ── Restore bearer mode for clean-up ─────────────────────────────────────────

  listeners['message']({ data: { type: 'SET_AUTH_MODE', mode: 'bearer' }, source: {} });
  listeners['message']({ data: { type: 'SET_TOKEN', token: 'tok-abc123' }, source: {} });

// ── Test 12: Origin guard — cross-origin message must be ignored ───────────────
// Send SET_AUTH_MODE from a different origin; authMode must stay 'bearer'.
// We verify by checking that a GET request in the supposed 'session' mode
// would not pass-through (because the mode never changed) — but since bearer
// mode also passes GETs through (token is set), we instead verify that a
// cross-origin SET_CSRF_TOKEN + SET_AUTH_MODE combination doesn't flip state
// by attempting a session-mode mutation (which should use bearer, not CSRF).

  listeners['message']({
    data: { type: 'SET_AUTH_MODE', mode: 'session' },
    origin: 'https://attacker.example.com',
    source: {},
  });
  listeners['message']({
    data: { type: 'SET_CSRF_TOKEN', token: 'evil-csrf' },
    origin: 'https://attacker.example.com',
    source: {},
  });

  // Verify authMode was NOT changed by dispatching a POST; in bearer mode the
  // SW should reconstruct with Authorization: Bearer (not CSRF injection).
  return dispatchFetch({
    url:         'http://127.0.0.1:7890/api/add',
    method:      'POST',
    headers:     new global.Headers({ 'Content-Type': 'application/json' }),
    mode:        'cors',
    destination: '',
    body:        jsonBody,
  });
}).then(function(result) {
  assert(result.respondWithCalled,
    'origin guard: cross-origin SET_AUTH_MODE must be ignored; bearer POST should still call respondWith()');
  if (result.fetched) {
    var auth = result.fetched.headers.get('Authorization');
    assert(auth === 'Bearer tok-abc123',
      'origin guard: authMode must still be bearer; Authorization should be set (got "' + auth + '")');
    var csrf = result.fetched.headers.get('X-CSRF-Token');
    assert(!csrf,
      'origin guard: X-CSRF-Token must NOT be set in bearer mode after cross-origin message (got "' + csrf + '")');
  }

  // Also verify that a message with no source is rejected (defense-in-depth).
  listeners['message']({ data: { type: 'SET_AUTH_MODE', mode: 'session' } }); // no source — should be ignored

  // If the no-source message was rejected, authMode is still bearer;
  // a GET should use the normal bearer path (respondWith called for bearer+token).
  return dispatchFetch({
    url:         'http://127.0.0.1:7890/api/presence',
    method:      'GET',
    headers:     new global.Headers({}),
    mode:        'cors',
    destination: '',
    body:        null,
  });
}).then(function(result) {
  // In bearer mode with a token, GET is intercepted and Authorization injected.
  assert(result.respondWithCalled,
    'origin guard (no-source): authMode must still be bearer after sourceless message; GET should call respondWith()');
  if (result.fetched) {
    var auth = result.fetched.headers.get('Authorization');
    assert(auth === 'Bearer tok-abc123',
      'origin guard (no-source): Authorization header present confirms bearer mode unchanged (got "' + auth + '")');
  }

// ── Results ───────────────────────────────────────────────────────────────────

  if (failures.length > 0) {
    process.stderr.write(failures.length + ' assertion(s) failed.\n');
    process.exit(1);
  }
  process.stdout.write(
    'PASS: sw.js smoke tests passed (bearer: POST body+redirect preserved, ' +
    'GET redirect:follow, early-returns intact, !token pass-through; ' +
    'session: GET pass-through, POST CSRF injection, no-CSRF pass-through; ' +
    'origin guard: cross-origin + no-source messages ignored).\n'
  );
  process.exit(0);
}).catch(function(err) {
  process.stderr.write('FAIL: unexpected error in sw-smoke.js: ' + err + '\n');
  process.stderr.write(err.stack + '\n');
  process.exit(1);
});
