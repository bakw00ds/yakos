// yakOS Console Service Worker
//
// Served from /sw.js (same-origin, token-exempt) at scope '/'.
// Receives the bearer token via postMessage only — it is never stored in
// cookies, localStorage, IndexedDB, or any persistent storage.
//
// Lifecycle:
//   1. app.js registers this SW at scope '/'.
//   2. app.js posts { type: 'SET_TOKEN', token: '<hex>' } after registration.
//   3. On every same-origin fetch that lacks an Authorization header, the SW
//      injects 'Authorization: Bearer <token>' before the request goes to the
//      network.  This covers iframe sub-resource loads for /kanban/, /cost/,
//      and /perf/ without requiring those frames to know the token themselves.
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

  // Clone the request, injecting the Authorization header.
  const headers = new Headers(e.request.headers);
  headers.set('Authorization', 'Bearer ' + token);
  const modified = new Request(e.request, {
    headers: headers,
    mode: 'same-origin',
    credentials: 'omit',
  });
  // Fall back to the original unmodified request if the modified fetch fails
  // (e.g. navigation requests with mode:'same-origin' + credentials:'omit' can
  // be rejected by the browser before hitting the network).  Without the catch,
  // a rejected Promise here becomes an uncaught rejection and the navigation fails.
  e.respondWith(fetch(modified).catch(function() { return fetch(e.request); }));
});
