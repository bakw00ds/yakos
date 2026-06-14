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
// Navigation requests (e.g. top-level or iframe document navigations) are
// passed through unmodified.  Reconstructing a navigate-mode request with
// mode:'same-origin' and credentials:'omit' is rejected by the browser before
// it reaches the network.  Navigation to any gated route is handled by the
// edge requireTokenForNonStatic middleware, not the Service Worker, because the
// browser does not send Authorization headers on navigations — the SW injects
// the header only for sub-resource (XHR/fetch) requests inside a loaded page.
// The /ide/editor route is accessed as an iframe sub-resource by the console
// (the normal path); direct top-level navigations to gated routes are not the
// primary pattern and cannot use SW-injected headers.
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
  // Navigation requests (mode === 'navigate') cannot be reconstructed with
  // mode:'same-origin' + credentials:'omit' — the browser rejects such a
  // Request() constructor call for navigations.  Pass navigate requests
  // through unmodified; token injection for navigations is not needed
  // (the edge middleware gates non-static routes; browsers do not send
  // Authorization on navigation requests regardless).
  if (e.request.mode === 'navigate') return;

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
