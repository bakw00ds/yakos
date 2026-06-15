// yakOS Console Service Worker
//
// Served from /sw.js (same-origin, token-exempt) at scope '/'.
//
// authMode: 'bearer' (default / loopback) or 'session' (networked human).
// Delivered via postMessage by app.js at registration time.
//
// bearer mode (loopback, today — unchanged):
//   Receives the bearer token via { type: 'SET_TOKEN', token: '<hex>' }.
//   On every same-origin sub-resource fetch that lacks an Authorization header,
//   the SW reconstructs the request with credentials:'omit' and injects
//   'Authorization: Bearer <token>'.  This covers iframe document loads for
//   /kanban/, /cost/, /perf/ sub-dashboards and all other sub-resources.
//
// session mode (networked human):
//   Receives { type: 'SET_AUTH_MODE', mode: 'session' } and
//   { type: 'SET_CSRF_TOKEN', token: '<hex>' } from app.js.
//   The SW does NOT reconstruct/rebuild the request — doing so forces
//   credentials:'omit' and strips the session cookie.  Instead:
//     - GET/HEAD: pass the original request through untouched so the browser
//       sends the HttpOnly session cookie via credentials:'same-origin'.
//     - POST/PUT/PATCH/DELETE: clone and inject the X-CSRF-Token header;
//       do NOT change credentials or URL; pass to fetch().
//   No Authorization header is ever injected in session mode.
//
// Navigation request handling:
//   destination === 'document'  — top-level page load.  Skipped: we cannot
//     reconstruct a navigate-mode Request with different headers (the browser
//     throws on new Request(navigateReq, {...mode:'same-origin'})). The top-
//     level routes '/' and '/ide/editor' are token-exempt anyway.
//   destination === 'iframe' | 'frame'  — iframe document loads (kanban,
//     cost, perf).  These hit requireTokenForNonStatic and need the header.
//     IMPORTANT: these also use mode==='navigate', so we CANNOT reconstruct
//     them via `new Request(e.request, {...})` either — that also throws for
//     navigate-mode.  Instead, we build a fresh Request from the URL only
//     (see "URL-built request" comment below), which avoids the restriction.
//   All other requests (XHR, fetch, scripts, styles, fonts) use non-navigate
//     mode and can be reconstructed normally; we use the URL-build path for
//     all of them too (simpler, uniform, no edge-case).
//
//   In session mode: iframe loads are GETs — they pass through untouched and
//   the browser sends the HttpOnly session cookie automatically.
//
// Auto-activation:
//   skipWaiting() in the install handler + clients.claim() in activate mean
//   a new SW version takes control on the very next page reload, without
//   requiring the operator to close all tabs.  One reload after a SW update
//   is sufficient.
//
// Security notes:
//   bearer mode:
//     - token is an in-memory variable in the SW's global scope.  It is
//       cleared when the SW is terminated and not persisted anywhere.
//     - The URL-built request explicitly sets credentials:'omit'.  No cookies
//       are sent on the bearer path.
//   session mode:
//     - csrfToken is in-memory in the SW scope.  It is the value of the
//       non-HttpOnly yakos_csrf cookie, delivered via postMessage so the SW
//       can inject it as X-CSRF-Token on mutations without reading cookies
//       (SWs have no access to document.cookie).
//     - The session cookie (yakos_session, HttpOnly) is never accessible in
//       JS; it rides on requests via the browser's own credentials:'same-origin'
//       default.  We never touch it in the SW.
//   both modes:
//     - Requests that already carry Authorization are passed through unmodified.
//     - Only same-origin requests are intercepted.

'use strict';

// In-memory auth state — cleared on SW termination.
let token = null;        // bearer token (loopback mode only)
let authMode = 'bearer'; // 'bearer' | 'session'
let csrfToken = null;    // CSRF token for session mode (from non-HttpOnly cookie)

// Auto-activate: take control immediately on install so a single reload after
// a SW update is sufficient to pick up the new version.
self.addEventListener('install', () => {
  self.skipWaiting();
});

// Auto-claim: once activated, claim all open clients (tabs) so the new SW
// handles their subsequent fetches without requiring those tabs to reload again.
self.addEventListener('activate', (e) => {
  e.waitUntil(self.clients.claim());
});

self.addEventListener('message', (e) => {
  if (!e.data) return;

  // Origin guard: only accept messages from controlled same-origin clients.
  //
  // e.origin is the origin of the page that posted the message.  For messages
  // from the page script this is always the same origin as the SW itself
  // (same-origin policy).  Cross-origin iframes cannot postMessage to the SW
  // because the SW scope is same-origin.
  //
  // We additionally check e.source is truthy (null for non-client senders such
  // as other SWs) as defense-in-depth.  Both checks together ensure SET_TOKEN,
  // SET_AUTH_MODE, and SET_CSRF_TOKEN can only be delivered by a page/client
  // that shares our origin.
  if (e.origin && e.origin !== self.location.origin) return;
  if (!e.source) return;

  if (e.data.type === 'SET_TOKEN') {
    token = e.data.token;
  } else if (e.data.type === 'SET_AUTH_MODE') {
    authMode = e.data.mode === 'session' ? 'session' : 'bearer';
  } else if (e.data.type === 'SET_CSRF_TOKEN') {
    csrfToken = e.data.token;
  }
});

self.addEventListener('fetch', (e) => {
  const url = new URL(e.request.url);
  // Only intercept same-origin requests.
  if (url.origin !== self.location.origin) return;
  // Pass through requests that already carry Authorization.
  if (e.request.headers.get('Authorization')) return;
  // Top-level page navigations: skip in all modes.  These are token-exempt routes
  // ('/' serves the console shell; '/ide/editor' serves the Monaco iframe host).
  // We cannot reconstruct navigate-mode Requests with new headers anyway.
  if (e.request.mode === 'navigate' && e.request.destination === 'document') return;

  // ---- session mode -----------------------------------------------------------
  //
  // In session mode we do NOT reconstruct the request because reconstructing it
  // forces credentials:'omit' which strips the HttpOnly session cookie.  Instead:
  //
  //   - GET/HEAD: pass through untouched — the browser sends the session cookie
  //     automatically via its default credentials:'same-origin' behaviour.
  //
  //   - Mutations (POST/PUT/PATCH/DELETE): clone the request with the
  //     X-CSRF-Token header added; do NOT alter credentials or any other field.
  //     The browser will still attach the session cookie.
  //
  // If no CSRF token is available, mutations pass through without the header —
  // the server will reject them with 403, which is the correct fail-safe
  // (better to surface a CSRF error than to strip the mutation body or silently
  // change credentials behaviour).
  //
  // No Authorization header is ever injected in session mode.

  if (authMode === 'session') {
    const isBodyMethod = e.request.method !== 'GET' && e.request.method !== 'HEAD';
    if (!isBodyMethod) {
      // GET/HEAD — pass through; browser attaches session cookie.
      return;
    }
    // Mutation: inject X-CSRF-Token if we have it.
    if (!csrfToken) {
      // No token yet — pass through; server will 403.
      return;
    }
    e.respondWith((async () => {
      const headers = new Headers(e.request.headers);
      headers.set('X-CSRF-Token', csrfToken);
      const init = {
        method:  e.request.method,
        headers: headers,
        // credentials:'same-origin' is the browser default — explicitly set so
        // the cloned request carries the session cookie.
        credentials: 'same-origin',
        // mode:'same-origin' is intentional: all console APIs are same-origin
        // endpoints (the server and the browser page share the same host:port).
        // Clamping to same-origin prevents the browser from issuing a CORS
        // preflight on the cloned request and keeps the session cookie in scope.
        // Cross-origin mutations cannot reach here because the fetch handler
        // already returns early for non-same-origin requests (top of handler).
        mode: 'same-origin',
        // Mutation POSTs must not follow redirects — same rationale as bearer mode.
        redirect: 'error',
        body: await e.request.clone().arrayBuffer(),
      };
      return fetch(new Request(e.request.url, init));
    })());
    return;
  }

  // ---- bearer mode (loopback/default — unchanged) ----------------------------
  //
  // If we don't have a token yet, let the request proceed unmodified.
  if (!token) return;

  // URL-built request: construct a fresh Request from the URL string rather
  // than cloning e.request.  This sidesteps the browser restriction that
  // throws TypeError when you pass {mode:'same-origin'} to new Request() for
  // a navigate-mode request (which is what iframe document loads use).
  //
  // We copy method and headers from the original request, then inject the
  // Authorization header.  For non-GET/HEAD requests (POST, PUT, PATCH,
  // DELETE, etc.) we also copy the body — otherwise mutations from the
  // kanban iframe (api/add, api/move, api/notes, api/delete) arrive at the
  // server with an empty body, causing a 400 "title required" error.
  //
  // The kanban iframe's post() sets only Content-Type, not Authorization, so
  // its POSTs reach this SW branch and must have their body preserved.
  // Navigate-mode requests (iframe document loads) are always GET and have no
  // body; only non-GET/HEAD requests need the body clone.
  e.respondWith((async () => {
    const headers = new Headers(e.request.headers);
    headers.set('Authorization', 'Bearer ' + token);
    const isBodyMethod = e.request.method !== 'GET' && e.request.method !== 'HEAD';
    const init = {
      method:      e.request.method,
      headers:     headers,
      mode:        'same-origin',
      credentials: 'omit',
      // Mutation POSTs (api/add, api/move, api/notes, api/delete, etc.) never
      // legitimately return a 3xx; fail loudly on an unexpected redirect so
      // the body + injected bearer token are never silently replayed to a
      // different origin.  GET/HEAD iframe and sub-resource loads must still
      // follow same-origin redirects (e.g. /kanban → /kanban/ trailing-slash
      // from the Go mux), so those keep 'follow'.
      redirect:    isBodyMethod ? 'error' : 'follow',
    };
    // Copy the body for methods that carry one.  arrayBuffer() preserves the
    // exact bytes (JSON payload); Content-Type is already in the headers clone
    // so the server parses it correctly.  Bodies on this path originate
    // exclusively from the first-party console UI (small JSON mutations), so
    // unbounded buffering is not a concern.
    if (isBodyMethod) {
      init.body = await e.request.clone().arrayBuffer();
    }
    return fetch(new Request(e.request.url, init));
  })());
});
