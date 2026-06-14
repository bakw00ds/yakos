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
//      request goes to the network.  This covers iframe sub-resource loads for
//      /kanban/, /cost/, /perf/, /ide/editor, etc., without those frames
//      needing to know the token themselves.
//
// Navigation requests are split by destination:
//   destination === 'document'  — top-level page load (e.g. navigating to '/').
//     These are passed through unmodified: (a) a navigate-mode Request cannot
//     be reconstructed with mode:'same-origin' + credentials:'omit' without the
//     browser rejecting it, and (b) '/' and '/ide/editor' are token-exempt.
//   destination === 'iframe' | 'frame' — iframe document loads (kanban, cost,
//     perf sub-dashboards). These DO need the Authorization header injected
//     because they hit requireTokenForNonStatic middleware.  They fall through
//     to the injection path below.
// Sub-resources (XHR, fetch, scripts, styles) always reach the injection path.
//
// Security notes:
//   - self.token is an in-memory variable in the SW's global scope.  It is
//     cleared when the SW is terminated and not persisted anywhere.
//   - Requests that already carry Authorization are passed through unmodified.
//   - Only same-origin requests are intercepted (cross-origin fetch events are
//     returned without modification).

'use strict';

// In-memory token storage — cleared on SW termination.
let token = null;

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
  // Top-level document navigations (destination === 'document') cannot be
  // reconstructed with mode:'same-origin' + credentials:'omit' — the
  // browser rejects such a Request() for navigations.  The '/' and
  // '/ide/editor' routes are token-exempt anyway; top-level nav is not
  // the injection target.
  //
  // Iframe and frame navigations (destination === 'iframe' | 'frame') are
  // different: the kanban/cost/perf sub-dashboards load as iframes and need
  // the Authorization header injected, because their document requests go
  // through the same requireTokenForNonStatic middleware.  Checking
  // destination lets us skip only top-level page navigations while still
  // injecting for iframes.
  if (e.request.mode === 'navigate' && e.request.destination === 'document') return;

  // Clone the request, injecting the Authorization header.
  const headers = new Headers(e.request.headers);
  headers.set('Authorization', 'Bearer ' + token);
  const modified = new Request(e.request, {
    headers: headers,
    mode: 'same-origin',
    credentials: 'omit',
  });
  e.respondWith(fetch(modified));
});
