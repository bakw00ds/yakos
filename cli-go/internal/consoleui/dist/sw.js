// yakOS Console Service Worker
//
// Served from /sw.js (same-origin, token-exempt) at scope '/'.
// Receives the bearer token via postMessage only — it is never stored in
// cookies, localStorage, IndexedDB, or any persistent storage.
//
// Lifecycle:
//   1. app.js registers this SW at scope '/'.
//   2. app.js posts { type: 'SET_TOKEN', token: '<hex>' } after registration.
//   3. On every same-origin sub-resource fetch that lacks an Authorization
//      header, the SW injects 'Authorization: Bearer <token>' before the
//      request goes to the network.  This covers iframe document loads for
//      /kanban/, /cost/, /perf/ sub-dashboards, and all other sub-resources.
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
// Auto-activation:
//   skipWaiting() in the install handler + clients.claim() in activate mean
//   a new SW version takes control on the very next page reload, without
//   requiring the operator to close all tabs.  One reload after a SW update
//   is sufficient.
//
// Security notes:
//   - self.token is an in-memory variable in the SW's global scope.  It is
//     cleared when the SW is terminated and not persisted anywhere.
//   - Requests that already carry Authorization are passed through unmodified.
//   - Only same-origin requests are intercepted.
//   - The URL-built request explicitly sets mode:'same-origin' and
//     credentials:'omit'.  No cookies are sent.

'use strict';

// In-memory token storage — cleared on SW termination.
let token = null;

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
  if (e.data && e.data.type === 'SET_TOKEN') {
    token = e.data.token;
  }
});

self.addEventListener('fetch', (e) => {
  const url = new URL(e.request.url);
  // Only intercept same-origin requests.
  if (url.origin !== self.location.origin) return;
  // Pass through requests that already carry Authorization.
  if (e.request.headers.get('Authorization')) return;
  // If we don't have a token yet, let the request proceed unmodified.
  if (!token) return;
  // Top-level page navigations: skip injection.  These are token-exempt routes
  // ('/' serves the console shell; '/ide/editor' serves the Monaco iframe host).
  // We cannot reconstruct navigate-mode Requests with new headers anyway.
  if (e.request.mode === 'navigate' && e.request.destination === 'document') return;

  // URL-built request: construct a fresh Request from the URL string rather
  // than cloning e.request.  This sidesteps the browser restriction that
  // throws TypeError when you pass {mode:'same-origin'} to new Request() for
  // a navigate-mode request (which is what iframe document loads use).
  //
  // We copy method and headers from the original request, then add the
  // Authorization header.  Body is omitted — iframe document loads and
  // virtually all sub-resource GETs have no body; POST API calls already
  // carry their own Authorization header (checked above) and are not
  // modified here.
  const headers = new Headers(e.request.headers);
  headers.set('Authorization', 'Bearer ' + token);
  const modified = new Request(e.request.url, {
    method:      e.request.method,
    headers:     headers,
    mode:        'same-origin',
    credentials: 'omit',
    redirect:    'follow',
  });
  e.respondWith(fetch(modified));
});
