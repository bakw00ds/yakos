// yakOS Unified Console — Phase 1 SPA shell
//
// Token handling:
//   The console URL is opened as http://127.0.0.1:7890/#token=<hex>.
//   On load: the token is read from location.hash, history.replaceState()
//   strips it from the URL immediately so it never appears in browser history
//   or referrer headers.  The token is held in memory only.
//
// Iframe / same-origin auth:
//   All sub-dashboards (/kanban/, /cost/, /perf/) are mounted at the SAME
//   origin (127.0.0.1:7890) and the entire mux is wrapped by a single edge
//   RequireToken middleware.  The browser sends cookies on same-origin
//   requests — but we do NOT use cookies.
//
//   Instead, every iframe sub-request must carry "Authorization: Bearer".
//   A plain <iframe> cannot inject request headers.  We solve this via a
//   Service Worker registered at the console origin (served from /sw.js,
//   scope /) that intercepts all sub-resource fetch requests and injects
//   the Authorization header.  The token is delivered to the SW via
//   postMessage only — it is never stored in cookies or localStorage.
//
//   If Service Workers are unavailable (e.g. private/incognito mode on some
//   browsers), the iframes will receive a 401 and the auth-error UI is shown
//   instead.  There is no query-param fallback — tokens must never appear in
//   URLs or browser history.
//
//   NOTE: /, /app.js, /styles.css, and /sw.js are token-exempt so the
//   browser can load the shell and register the SW before the token is
//   available.  All data APIs and sub-dashboard paths require the token.
//
// WebSocket:
//   The /v1/events WS endpoint is also at the console origin.  JS sends
//   the token via the Authorization header in the WebSocket upgrade request.
//   (Standard browser WebSocket API does not support custom headers; we use
//   the Sec-WebSocket-Protocol header trick or fall back to the first-message
//   auth pattern.  For Phase 1 the WS is wired but the Overview tab is a
//   placeholder — WS live data lands in Phase 2.5.)

(function () {
  'use strict';

  // ---- 1. Token extraction ---------------------------------------------------

  let TOKEN = '';

  function extractAndStripToken() {
    const hash = location.hash;
    if (hash && hash.startsWith('#token=')) {
      TOKEN = hash.slice(7);
      // Strip the fragment from the URL and history immediately.
      history.replaceState(null, '', location.pathname + location.search);
    }
  }

  extractAndStripToken();

  // ---- 2. Service Worker registration ----------------------------------------
  // The SW is served from /sw.js (same-origin, token-exempt) at scope '/'.
  // It intercepts same-origin sub-resource fetches and injects
  // "Authorization: Bearer <token>" so iframe-loaded dashboards pass auth.
  // The token is delivered via postMessage only — never stored.

  let swReady = false;

  function registerServiceWorker() {
    if (!TOKEN) return;
    if (!('serviceWorker' in navigator)) {
      console.warn('[console] Service Worker unavailable — iframe auth will fail');
      return;
    }

    // Register the SW from its real same-origin path /sw.js.
    // Blob-URL registration is rejected by Chrome/Firefox at scope '/'.
    navigator.serviceWorker.register('/sw.js', { scope: '/' })
      .then((reg) => {
        // Send token to the SW via postMessage (memory-only; never persisted).
        const target = reg.installing || reg.waiting || reg.active;
        if (target) {
          target.postMessage({ type: 'SET_TOKEN', token: TOKEN });
        }
        navigator.serviceWorker.addEventListener('controllerchange', () => {
          if (navigator.serviceWorker.controller) {
            navigator.serviceWorker.controller.postMessage({ type: 'SET_TOKEN', token: TOKEN });
          }
        });
        swReady = true;
      })
      .catch((err) => {
        console.warn('[console] SW registration failed:', err);
      });
  }

  registerServiceWorker();

  // ---- 3. Tab management -----------------------------------------------------

  const TABS = [
    { id: 'overview',  label: 'Overview',    src: null,       phase: '2.5' },
    { id: 'kanban',    label: 'Kanban',       src: '/kanban/', phase: null },
    { id: 'cost',      label: 'Cost',         src: '/cost/',   phase: null },
    { id: 'perf',      label: 'Performance',  src: '/perf/',   phase: null },
    // Chat and Flows are Phase 3+ — listed as placeholders.
    { id: 'chat',      label: 'Chat',         src: null,       phase: '3',  disabled: true },
    { id: 'flows',     label: 'Flows',        src: null,       phase: '4',  disabled: true },
  ];

  let activeTab = 'overview';
  const loadedTabs = new Set();

  function renderTabs() {
    const bar = document.getElementById('tab-bar-tabs');
    bar.innerHTML = '';
    for (const tab of TABS) {
      const el = document.createElement('button');
      el.className = 'tab' + (tab.id === activeTab ? ' active' : '') + (tab.disabled ? ' disabled' : '');
      el.setAttribute('data-tab', tab.id);
      el.setAttribute('type', 'button');
      if (tab.disabled) {
        el.setAttribute('title', 'Coming in Phase ' + tab.phase);
        el.setAttribute('aria-disabled', 'true');
      }
      el.textContent = tab.label;
      if (!tab.disabled) {
        el.addEventListener('click', () => switchTab(tab.id));
      }
      bar.appendChild(el);
    }
  }

  function switchTab(id) {
    if (id === activeTab) return;
    activeTab = id;

    // Update tab bar.
    document.querySelectorAll('.tab').forEach((el) => {
      el.classList.toggle('active', el.getAttribute('data-tab') === id);
    });

    // Show/hide panels.
    document.querySelectorAll('.tab-panel').forEach((el) => {
      el.classList.toggle('active', el.id === 'panel-' + id);
    });

    // Lazy-load iframe on first activation.
    const tab = TABS.find((t) => t.id === id);
    if (tab && tab.src && !loadedTabs.has(id)) {
      loadedTabs.add(id);
      const panel = document.getElementById('panel-' + id);
      const iframe = panel && panel.querySelector('iframe');
      if (iframe) {
        if (!swReady) {
          // SW not yet ready or unavailable — show auth error instead of
          // loading a tab that will receive 401.  The token must never appear
          // in a URL or query string.
          document.getElementById('auth-error').classList.add('visible');
          return;
        }
        // SW is registered; it will inject Authorization: Bearer automatically.
        iframe.src = tab.src;
      }
    }
  }

  // ---- 4. Page rendering ------------------------------------------------------

  function buildPage() {
    const app = document.getElementById('app');
    app.innerHTML = `
      <div class="tab-bar">
        <span class="tab-bar-brand">yakOS</span>
        <div id="tab-bar-tabs" style="display:flex;"></div>
      </div>
      <div class="tab-content">
        <div id="panel-overview" class="tab-panel active">
          <div class="overview-placeholder">
            <h2>Overview</h2>
            <p>Presence &amp; activity land in Phase 2.5.
               For now, use the tabs above to navigate to Kanban, Cost, or Performance.</p>
          </div>
        </div>
        <div id="panel-kanban" class="tab-panel">
          <iframe title="Kanban board" allow="same-origin"></iframe>
        </div>
        <div id="panel-cost" class="tab-panel">
          <iframe title="Cost dashboard" allow="same-origin"></iframe>
        </div>
        <div id="panel-perf" class="tab-panel">
          <iframe title="Performance dashboard" allow="same-origin"></iframe>
        </div>
        <div id="panel-chat" class="tab-panel">
          <div class="overview-placeholder">
            <h2>Chat</h2>
            <p>Streaming Chat REPLs land in Phase 3.</p>
          </div>
        </div>
        <div id="panel-flows" class="tab-panel">
          <div class="overview-placeholder">
            <h2>Flows</h2>
            <p>Visual DAG orchestration lands in Phase 4/5.</p>
          </div>
        </div>
      </div>
      <div id="auth-error">
        <div class="auth-error-box">
          <h2>Authentication required</h2>
          <p>Open the console URL with the token in the fragment:<br>
             <code>http://127.0.0.1:7890/#token=&lt;your-token&gt;</code><br><br>
             The token is printed at daemon startup.</p>
        </div>
      </div>
    `;

    renderTabs();

    if (!TOKEN) {
      document.getElementById('auth-error').classList.add('visible');
    }
  }

  // ---- 5. Init ----------------------------------------------------------------

  document.addEventListener('DOMContentLoaded', buildPage);

})();
