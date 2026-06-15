// yakOS Unified Console — Phase 3c SPA
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
//   SW activation-race fix (Phase 2.5): iframe tab load is gated on
//   navigator.serviceWorker.ready instead of the swReady boolean, eliminating
//   the race where the first tab click fires before the SW has activated.
//
//   If Service Workers are unavailable (e.g. private/incognito mode on some
//   browsers), the iframes will receive a 401 and the auth-error UI is shown
//   instead.  There is no query-param fallback — tokens must never appear in
//   URLs or browser history.
//
// WebSocket (Phase 2.5 subprotocol bearer auth):
//   Browsers cannot set Authorization on WS upgrade requests (the standard
//   API doesn't support custom headers).  Phase 2.5 uses the
//   Sec-WebSocket-Protocol header trick:
//
//     new WebSocket(url, ['yakos-bearer', TOKEN])
//
//   The server validates the token constant-time and responds with protocol
//   "yakos-bearer".  The token NEVER appears in the URL or query string.
//
// Chat SSE auth (Phase 3c):
//   GET /api/chat/stream uses fetch + ReadableStream (NOT EventSource) so
//   the Service Worker can inject the Authorization: Bearer header.
//   EventSource does not support custom headers, hence the fetch approach.
//   The SSE stream is multiplexed by sessionId; the browser demuxes events
//   into the correct pane.
//
// XSS discipline:
//   esc() is called on every server/operator/model-supplied string before
//   DOM insertion.  No innerHTML without esc(); no eval(); no inline scripts.
//   Transcript text, agent/model/operator names, and error messages all go
//   through esc().

(function () {
  'use strict';

  // ---- 0. Theme system -------------------------------------------------------
  //
  // Theme persistence: localStorage key 'yakos_theme'.
  // Valid values: 'ops', 'fluid', 'og', 'light'.
  // Default: derived from prefers-color-scheme (dark → 'og', light → 'light').
  //
  // The data-theme attribute is set on <html> as early as possible here — at
  // the top of the IIFE before DOMContentLoaded — so it fires before first
  // paint. The server-side index.html already sets data-theme="og" as a
  // no-inline-script FOUC hedge; this code overrides it immediately if the
  // user has a different preference stored.
  //
  // The console CSP forbids inline scripts (script-src 'self'), so we cannot
  // use a <script> block in <head>. app.js loads via <script src="/app.js">
  // which the browser executes before rendering the body, giving us an early
  // enough execution point to avoid visible FOUC in most cases.
  //
  // perf-low-end probe: if deviceMemory <= 2 GB or hardwareConcurrency <= 2,
  // we set html.perf-low-end which CSS uses to kill FLUID aurora animations.
  // This mirrors the PandaOS field-decorations-init.ts probe logic.

  var THEME_LS_KEY = 'yakos_theme';
  var VALID_THEMES = ['ops', 'fluid', 'og', 'light'];

  // Belt-and-suspenders TDZ guard: hoist IDE editor state vars to var so that
  // even if any pre-paint code path ever reaches syncMonacoTheme(), it sees
  // `undefined` (falsy) rather than a TDZ ReferenceError that aborts the IIFE.
  // The authoritative declarations (with comments) remain near the IDE section
  // below; these var hoists shadow nothing — they produce the same initial value.
  var ideTabInitialized = false;
  var ideEditorWindow = null;
  var ideEditorReady = false;
  var ideQueuedOpen = null; // re-assigned to [] in IDE section init (B2: array queue)
  var ideMessageHandlerRegistered = false;
  // Multi-file tab state (hoisted for TDZ safety):
  //   ideOpenFiles: Map<path, {version, dirty, editable, saving, saveStatusTimer}>
  //   ideActiveTabPath: workspace-relative path of the currently active tab
  var ideOpenFiles = null;   // set to new Map() in the IDE section init
  var ideActiveTabPath = '';
  // Legacy single-file save state vars — hoisted for TDZ / syncMonacoTheme safety.
  // Still used as mirrors of the active-tab state so existing callers
  // (triggerSave, handleIdeEditorMessage save/content handlers) work unchanged.
  var ideCurrentPath = '';
  var ideCurrentVersion = '';
  var ideEditable = false;
  var ideIsDirty = false;
  // ideIsSaving: true while a POST /api/files/write fetch is in-flight.
  // Guards both triggerSave() and the 'save' message handler to prevent a
  // double-POST race where two concurrent ⌘S presses race their success
  // handlers and corrupt ideCurrentVersion.
  var ideIsSaving = false;
  // Timer handle for the transient save-status message auto-clear.
  // Hoisted (var) to match the TDZ-safety pattern for all IDE state.
  var ideSaveStatusTimer = null;
  // Layout persistence (resizable/collapsible panels).
  var IDE_LAYOUT_LS_KEY = 'yakos_ide_layout';

  function defaultTheme() {
    try {
      if (window.matchMedia && window.matchMedia('(prefers-color-scheme: light)').matches) {
        return 'light';
      }
    } catch (_) {}
    return 'og';
  }

  function readStoredTheme() {
    try {
      var v = localStorage.getItem(THEME_LS_KEY);
      if (v && VALID_THEMES.indexOf(v) !== -1) return v;
    } catch (_) {}
    return null;
  }

  // setThemeCoreOnly: the safe pre-paint subset of theme application.
  //
  // ONLY sets data-theme on <html>, persists to localStorage, and updates
  // aria-pressed states on .theme-btn elements. Does NOT call syncMonacoTheme.
  //
  // Why the split: syncMonacoTheme() reads ideEditorWindow, which is declared
  // with `let` later in the IIFE. Calling it from the pre-paint init block
  // (which runs at IIFE top, before those `let` bindings are initialised)
  // triggers a TDZ ReferenceError that aborts the entire IIFE and produces a
  // black screen. The Monaco iframe does not exist at pre-paint time anyway;
  // theme sync to Monaco is handled correctly by:
  //   a) handleIdeEditorMessage {type:'ready'} — on first IDE tab open, and
  //   b) applyTheme() (the full version below) — on every picker-button click.
  function setThemeCoreOnly(theme) {
    if (VALID_THEMES.indexOf(theme) === -1) theme = 'og';
    document.documentElement.setAttribute('data-theme', theme);
    try { localStorage.setItem(THEME_LS_KEY, theme); } catch (_) {}
    // Update picker button states (may not exist yet at pre-paint time; safe).
    var btns = document.querySelectorAll('.theme-btn');
    for (var i = 0; i < btns.length; i++) {
      btns[i].setAttribute('aria-pressed', btns[i].getAttribute('data-theme-value') === theme ? 'true' : 'false');
    }
  }

  // applyTheme: the full theme-switch path used by the picker at runtime.
  // Calls setThemeCoreOnly first, then syncMonacoTheme. Safe to call only
  // AFTER the IIFE body has fully initialised (i.e. from picker click handlers
  // wired in buildPage, never from the pre-paint init block).
  function applyTheme(theme) {
    setThemeCoreOnly(theme);
    // syncMonacoTheme is defined later in the IIFE; ideEditorWindow is
    // initialised by that point whenever applyTheme is called at runtime.
    syncMonacoTheme(theme);
  }

  // Set theme before first paint — uses setThemeCoreOnly (NOT applyTheme)
  // to avoid the TDZ crash on ideEditorWindow described above.
  (function () {
    var stored = readStoredTheme();
    setThemeCoreOnly(stored || defaultTheme());

    // perf-low-end probe: gate FLUID aurora animations on low-end devices.
    try {
      var mem = navigator.deviceMemory;
      var cores = navigator.hardwareConcurrency;
      if ((mem && mem <= 2) || (cores && cores <= 2)) {
        document.documentElement.classList.add('perf-low-end');
      }
    } catch (_) {}
  }());

  // ---- 1. Token extraction + auth mode detection -----------------------------
  //
  // Two authentication modes:
  //
  //   bearer (loopback):
  //     The console URL is opened as http://127.0.0.1:7890/#token=<hex>.
  //     TOKEN is extracted from the URL fragment, stripped from history.
  //     All API calls use Authorization: Bearer <TOKEN>; the SW reconstructs
  //     requests with credentials:'omit' + injects the header.
  //
  //   session (networked human):
  //     No bearer token in the URL.  The browser holds an HttpOnly session
  //     cookie (yakos_session) set by POST /login.  The server also sets a
  //     non-HttpOnly CSRF cookie (yakos_csrf) that JS can read.  Mutating
  //     requests must carry X-CSRF-Token matching that cookie value.
  //     The SW is told not to reconstruct requests so the session cookie
  //     survives; the SW injects X-CSRF-Token on mutations.
  //     On any 401 response, we redirect the top window to /login.

  let TOKEN = '';
  let AUTH_MODE = 'bearer'; // 'bearer' | 'session'
  let CSRF_TOKEN = '';      // session mode: value of yakos_csrf cookie

  function extractAndStripToken() {
    const hash = location.hash;
    if (hash && hash.startsWith('#token=')) {
      TOKEN = hash.slice(7);
      history.replaceState(null, '', location.pathname + location.search);
    }
  }

  // readCookie reads a named cookie from document.cookie.
  // Returns '' if not found or if cookie access throws.
  function readCookie(name) {
    try {
      const prefix = name + '=';
      const parts = document.cookie.split(';');
      for (var i = 0; i < parts.length; i++) {
        var part = parts[i].trim();
        if (part.indexOf(prefix) === 0) {
          return decodeURIComponent(part.slice(prefix.length));
        }
      }
    } catch (_) {}
    return '';
  }

  extractAndStripToken();

  if (TOKEN) {
    // Bearer token present in the URL fragment — loopback mode.
    AUTH_MODE = 'bearer';
  } else {
    // No bearer token — networked session mode.
    AUTH_MODE = 'session';
    CSRF_TOKEN = readCookie('yakos_csrf');
  }

  // apiFetch: mode-aware fetch wrapper for all console API calls.
  //
  // bearer mode: sets Authorization: Bearer <TOKEN>.  The SW also injects
  //   this header for iframe/sub-resource requests, but apiFetch handles
  //   direct fetch() calls in the main window context.
  //
  // session mode: does NOT set Authorization.  The browser sends the HttpOnly
  //   session cookie automatically via credentials:'same-origin'.  For mutating
  //   requests (non-GET/HEAD), sets X-CSRF-Token from CSRF_TOKEN.
  //   If a 401 is received, redirects top to /login (session expired).
  //
  // This is the single top-scope definition used by ALL callers, including
  // the Flows section.  The Flows section previously redefined apiFetch locally
  // (which shadowed this one), creating a duplicate that was a regression
  // time-bomb on the CSRF path.  That duplicate has been removed; all callers
  // now bind to this definition.

  function apiFetch(method, path, body) {
    const opts = { method };
    const isBodyMethod = method !== 'GET' && method !== 'HEAD';
    if (AUTH_MODE === 'bearer') {
      opts.headers = { 'Authorization': 'Bearer ' + TOKEN };
    } else {
      // session mode: rely on browser cookie (credentials:'same-origin').
      opts.credentials = 'same-origin';
      opts.headers = {};
      if (isBodyMethod && CSRF_TOKEN) {
        opts.headers['X-CSRF-Token'] = CSRF_TOKEN;
      }
    }
    if (body !== undefined) {
      opts.headers['Content-Type'] = 'application/json';
      opts.body = JSON.stringify(body);
    }
    return fetch(path, opts).then(function(resp) {
      if (resp.status === 401 && AUTH_MODE === 'session') {
        // Session expired — redirect to login.
        window.top.location.href = '/login';
        // Return a never-resolving promise so callers don't handle stale data.
        // NOTE: callers using .finally() will NOT have their finally-callback run
        // because the page navigates before the promise settles (#198 / ideIsSaving
        // latent note: isSaving flags relying on .finally() stay true on 401).
        return new Promise(function() {});
      }
      return resp;
    });
  }

  // ---- 2. Service Worker registration + ready promise ------------------------
  // Gate iframe loading on navigator.serviceWorker.ready (not a boolean flag)
  // to fix the activation-race where the first tab click fires before the SW
  // activates and the iframe gets a 401.

  let swReadyPromise = Promise.resolve(false); // resolves to true when SW ready

  // postToSW delivers a message to the SW regardless of its current lifecycle
  // state (installing / waiting / active).  Used to deliver SET_TOKEN,
  // SET_AUTH_MODE, and SET_CSRF_TOKEN messages.
  function postToSW(msg) {
    if (!navigator.serviceWorker) return;
    if (navigator.serviceWorker.controller) {
      navigator.serviceWorker.controller.postMessage(msg);
    }
  }

  // deliverAuthToSW sends the appropriate auth messages to the SW based on
  // the current AUTH_MODE.  Called after registration and after each
  // controllerchange event so a freshly-activated SW gets the correct state.
  function deliverAuthToSW() {
    postToSW({ type: 'SET_AUTH_MODE', mode: AUTH_MODE });
    if (AUTH_MODE === 'bearer') {
      postToSW({ type: 'SET_TOKEN', token: TOKEN });
    } else {
      // session mode: deliver the CSRF token; no bearer token.
      postToSW({ type: 'SET_CSRF_TOKEN', token: CSRF_TOKEN });
    }
  }

  function registerServiceWorker() {
    // Register the SW in both modes.
    // bearer: SW needs the token for iframe auth injection.
    // session: SW needs CSRF_TOKEN for same-origin mutation injection.
    //
    // Guard: check navigator.serviceWorker is truthy, not just that the key
    // exists.  Some environments (Node stubs, old private-mode browsers) expose
    // navigator.serviceWorker as undefined even though 'serviceWorker' in
    // navigator evaluates to true (because the stub key exists).
    if (!navigator.serviceWorker) {
      if (AUTH_MODE === 'bearer') {
        console.warn('[console] Service Worker unavailable — iframe auth will fail');
      } else {
        console.warn('[console] Service Worker unavailable — CSRF injection unavailable');
      }
      return;
    }

    swReadyPromise = navigator.serviceWorker.register('/sw.js', { scope: '/' })
      .then((reg) => {
        // Deliver auth state to whatever state the SW is in.
        const target = reg.installing || reg.waiting || reg.active;
        if (target) {
          target.postMessage({ type: 'SET_AUTH_MODE', mode: AUTH_MODE });
          if (AUTH_MODE === 'bearer') {
            target.postMessage({ type: 'SET_TOKEN', token: TOKEN });
          } else {
            target.postMessage({ type: 'SET_CSRF_TOKEN', token: CSRF_TOKEN });
          }
        }
        navigator.serviceWorker.addEventListener('controllerchange', () => {
          deliverAuthToSW();
        });
        // Wait for the SW to actually control this page.
        return navigator.serviceWorker.ready;
      })
      .then(() => {
        // Deliver auth state to the now-active controller.
        deliverAuthToSW();
        return true;
      })
      .catch((err) => {
        console.warn('[console] SW registration failed:', err);
        return false;
      });
  }

  registerServiceWorker();

  // ---- 3. Tab management -----------------------------------------------------

  const TABS = [
    { id: 'overview',  label: 'Overview',    src: null,       phase: null },
    { id: 'repl',      label: 'REPL',        src: null,       phase: null },
    { id: 'kanban',    label: 'Kanban',       src: '/kanban/', phase: null },
    { id: 'cost',      label: 'Cost',         src: '/cost/',   phase: null },
    { id: 'perf',      label: 'Performance',  src: '/perf/',   phase: null },
    { id: 'chat',      label: 'Chat',         src: null,       phase: null },
    { id: 'ide',       label: 'IDE',          src: null,       phase: null },
    { id: 'flows',     label: 'Flows',        src: null,       phase: null },
    // users tab: only shown when account role === 'admin'; hidden by default
    // via adminOnly flag checked in renderTabs().  Server enforces RoleAdmin —
    // client-side hiding is convenience UX only, not a security gate.
    { id: 'users',     label: 'Users',        src: null,       phase: null, adminOnly: true },
  ];

  let activeTab = 'overview';
  const loadedTabs = new Set();

  // accountIdentity: populated from GET /api/account on boot.
  // Used to gate admin-only tabs (Users).  Null until the fetch resolves.
  // Shape: { operatorId: string, role: string, authMethod: string }
  let accountIdentity = null;

  function isAdmin() {
    return accountIdentity && accountIdentity.role === 'admin';
  }

  function renderTabs() {
    const bar = document.getElementById('tab-bar-tabs');
    bar.innerHTML = '';
    for (const tab of TABS) {
      // Admin-only tabs are omitted entirely from the DOM for non-admins.
      // They are only hidden here for UX; the server enforces role at the API layer.
      if (tab.adminOnly && !isAdmin()) continue;

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

    document.querySelectorAll('.tab').forEach((el) => {
      el.classList.toggle('active', el.getAttribute('data-tab') === id);
    });
    document.querySelectorAll('.tab-panel').forEach((el) => {
      el.classList.toggle('active', el.id === 'panel-' + id);
    });

    const tab = TABS.find((t) => t.id === id);
    if (tab && tab.src && !loadedTabs.has(id)) {
      loadedTabs.add(id);
      const panel = document.getElementById('panel-' + id);
      const iframe = panel && panel.querySelector('iframe');
      if (iframe) {
        // Gate on SW ready promise — fixes the activation-race (Phase 2.5).
        swReadyPromise.then((ready) => {
          if (!ready && AUTH_MODE === 'bearer') {
            // In bearer mode, SW is required for iframe auth injection.
            // In session mode, the kanban iframe self-injects X-CSRF-Token by
            // reading the yakos_csrf cookie directly (same-origin, non-HttpOnly),
            // so SW failure is fully recoverable for both reads and mutations.
            document.getElementById('auth-error').classList.add('visible');
            return;
          }
          if (AUTH_MODE === 'bearer') {
            // Bearer mode: append token as URL fragment so the dashboard's
            // client-side getToken() (metricsdash/perfdash read #token=<hex>)
            // authenticates without an extra round-trip.  Fragments are
            // NEVER sent to the server — they exist only in the browser and
            // cannot appear in server logs — so this is safe.  Kanban does
            // not use a fragment gate and ignores the extra fragment harmlessly.
            iframe.src = tab.src + '#token=' + TOKEN;
          } else {
            // Session mode: the session cookie rides same-origin automatically.
            // Do NOT append a bearer token (TOKEN is '' and the server does not
            // use it; appending it would produce a confusing #token= fragment).
            iframe.src = tab.src;
          }
        });
      }
    }

    // On first switch to REPL tab, seed the fleet panel from GET /api/fleet
    // and load kanban action items.
    if (id === 'repl') {
      initReplTab();
    }

    // On first switch to chat tab, ensure SSE is running.
    if (id === 'chat') {
      initChatTab();
    }

    // On first switch to IDE tab, initialize it.
    if (id === 'ide') {
      initIdeTab();
    }

    // On first switch to flows tab, initialize it.
    if (id === 'flows') {
      initFlowsTab();
    }

    // On every switch to users tab, refresh the table (data may have changed).
    if (id === 'users') {
      initUsersTab();
    }
  }

  // ---- 4. Overview state ------------------------------------------------------
  // in-flight dispatches: Map<agentKey, {agent, project, startedAt}>
  // presence:             Map<connId, PresenceRecord>
  // feed:                 Array of {ts, topic, summary, operatorId, color} (newest first)

  const FEED_CAP = 200;

  let inFlight = new Map();
  let presence = new Map();
  let feed = [];
  let myPresence = null;
  let feedFilterTopic = '';
  let feedFilterOp = '';

  // ---- 4b. REPL fleet state --------------------------------------------------
  //
  // fleetSessions: Map<sessionId, FleetSession> where FleetSession =
  //   { session_id, agent, runtime, status, started_at, task_preview, attachable }
  //
  // Seeded from GET /api/fleet on first REPL tab open, then patched live:
  //   fleet.started  → add entry (metadata only; no task text)
  //   fleet.finished → update status/exit_code on existing entry
  //
  // Security note: fleet.* WS events intentionally carry NO task text or tokens.
  // task_preview is only present in the REST seed response; it is stored here
  // from the initial fetch but is never populated from WS events.

  let fleetSessions = new Map(); // Map<sessionId, FleetSession>
  let fleetInitialized = false;  // true after first GET /api/fleet completes

  // ---- 5. WebSocket (Phase 2.5 subprotocol auth) ------------------------------

  let ws = null;
  let wsRetryMs = 1000;
  let wsLastSeq = 0; // last received event sequence number; used for ?since= replay

  function connectWS() {
    // In bearer mode, require a token.  In session mode, the browser sends
    // the session cookie on the WS upgrade request automatically — no token
    // is needed.
    if (AUTH_MODE === 'bearer' && !TOKEN) return;
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    // Append ?since=<seq> so the server replays missed events on reconnect,
    // preventing ghost in-flight dispatches after a disconnect.
    const since = wsLastSeq > 0 ? '?since=' + wsLastSeq : '';
    const url = proto + '//' + location.host + '/v1/events' + since;

    // Phase 2.5 / Phase 3e: Sec-WebSocket-Protocol bearer auth (bearer mode)
    //   and cookie auth (session mode).
    //
    // Bearer mode: The WS upgrade path is handled by consoleAuthSubprotocol in
    // cli-go/internal/consoleui/ws_handler.go.  That middleware requires
    // exactly two comma-separated protocol values — "yakos-bearer, <token>" —
    // and validates the token with constant-time comparison before the upgrade.
    // Browsers cannot set Authorization headers on WS upgrades, so the
    // subprotocol is the only delivery mechanism.
    //
    // Session mode (Phase 3e): In session mode, TOKEN is empty and we must NOT
    // send ['yakos-bearer', ''] — an empty token causes the server's
    // consoleAuthSubprotocol to reject the upgrade with 403 (the red "✗"
    // disconnected indicator).  Instead we open the WebSocket with NO
    // subprotocol so the browser sends the session cookie on the upgrade
    // request automatically (same-origin, credentials included by default for
    // WS).  The Phase 3e backend change to ws_handler.go / consoleAuth adds a
    // session-cookie check path that runs when no "yakos-bearer" subprotocol
    // is present.
    //
    // WS session contract (Phase 3e):
    //   - No Sec-WebSocket-Protocol header → server tries session cookie auth.
    //   - Sec-WebSocket-Protocol: yakos-bearer, <token> → server does bearer auth.
    //   The backend MUST NOT reject a no-subprotocol upgrade with 401/403 once
    //   Phase 3e lands; it should validate the session cookie instead.
    //
    // DO NOT change bearer mode to ['yakos-bearer'] (one part, no token) — the
    // server consoleAuthSubprotocol middleware does SplitN(protos, ",", 2) and
    // rejects any header with fewer than two parts.
    if (AUTH_MODE === 'session') {
      // Session mode: no subprotocol; browser sends session cookie automatically.
      // Requires Phase 3e backend: ws_handler.go must accept cookie auth when
      // no "yakos-bearer" subprotocol is present.
      ws = new WebSocket(url);
    } else {
      ws = new WebSocket(url, ['yakos-bearer', TOKEN]);
    }
    ws.addEventListener('open', () => {
      wsRetryMs = 1000; // reset backoff
      // Send hello frame with self-asserted identity (attribution-only).
      const hello = {
        type: 'hello',
        operatorId: myPresence ? myPresence.operatorId : '',
        displayName: myPresence ? myPresence.displayName : '',
        color: '',  // server derives color; client value ignored
      };
      ws.send(JSON.stringify(hello));
    });
    ws.addEventListener('message', (ev) => {
      let msg;
      try { msg = JSON.parse(ev.data); } catch { return; }
      handleWsMessage(msg);
    });
    ws.addEventListener('close', () => {
      ws = null;
      // Jitter: randomise delay to prevent thundering-herd reconnects when
      // many tabs reconnect simultaneously after a daemon restart.
      const jitteredDelay = wsRetryMs * (0.5 + Math.random() * 0.5);
      setTimeout(() => {
        connectWS();
        // S4: on reconnect, if an active run is being watched, poll once to
        // reconcile canvas state. The WS is the live path; poll is the
        // fallback — trigger it on reconnect so the canvas does not freeze
        // showing stale node states from before the disconnect.
        if (flowsState.activeRunId) {
          pollRunState(flowsState.activeRunId);
        }
      }, jitteredDelay);
      wsRetryMs = Math.min(wsRetryMs * 2, 30000);
    });
    ws.addEventListener('error', () => {
      // error fires before close; let close handler reconnect
    });
  }

  function handleWsMessage(msg) {
    // Welcome frame: server sends back our resolved presence record.
    if (msg.type === 'welcome') {
      myPresence = msg.presence || null;
      if (myPresence) {
        presence.set(msg.connId, myPresence);
      }
      renderPresence();
      return;
    }

    // Bus event envelope: {seq, topic, ts, payload}
    // Track the highest seq we've seen so reconnects can use ?since=<seq>.
    if (msg.seq && msg.seq > wsLastSeq) {
      wsLastSeq = msg.seq;
    }

    const topic = msg.topic;
    const payload = msg.payload || {};

    if (topic === 'dispatch.started') {
      const key = (payload.agent || '') + '|' + (payload.project || '');
      inFlight.set(key, {
        agent: payload.agent || '?',
        project: payload.project || '?',
        startedAt: payload.ts ? new Date(payload.ts) : new Date(),
      });
      pushFeed(msg.ts, topic, feedSummary(topic, payload), '');
      renderNow();
    } else if (topic === 'dispatch.finished') {
      const key = (payload.agent || '') + '|' + (payload.project || '');
      inFlight.delete(key);
      pushFeed(msg.ts, topic, feedSummary(topic, payload), '');
      renderNow();
    } else if (topic === 'fleet.started') {
      // fleet.started: metadata-only payload — no task text or token content.
      // Patch fleetSessions live; task_preview is absent (not on WS events by design).
      const sid = payload.session_id || '';
      if (sid) {
        fleetSessions.set(sid, {
          session_id: sid,
          agent:       payload.agent || '?',
          runtime:     '', // not in fleet.started WS payload; populated by REST seed
          status:      'running',
          started_at:  payload.ts || msg.ts || '',
          task_preview: '', // intentionally empty: not available on WS events
          attachable:   false,
        });
        renderReplFleet();
      }
      pushFeed(msg.ts, topic, feedSummary(topic, payload), '');
    } else if (topic === 'fleet.finished') {
      // fleet.finished: update status; keep task_preview from seed if present.
      const sid = payload.session_id || '';
      if (sid) {
        const existing = fleetSessions.get(sid);
        if (existing) {
          existing.status = payload.status || 'finished';
          fleetSessions.set(sid, existing);
        } else {
          // We may have missed the started event (reconnect race).
          // Add a stub entry so the panel shows the finished state.
          fleetSessions.set(sid, {
            session_id:   sid,
            agent:        payload.agent || '?',
            runtime:      '',
            status:       payload.status || 'finished',
            started_at:   '',
            task_preview: '',
            attachable:   false,
          });
        }
        renderReplFleet();
      }
      pushFeed(msg.ts, topic, feedSummary(topic, payload), '');
    } else if (topic === 'presence') {
      const opID = payload.operator_id || '';
      // Key on conn_id (present since B1 fix) for per-connection discrimination.
      // Fall back to opID only for legacy payloads that lack conn_id.
      const connId = payload.conn_id || opID;
      if (payload.status === 'offline') {
        // Remove by conn_id — precise even when multiple "anon" clients exist.
        presence.delete(connId);
      } else {
        presence.set(connId, payload);
      }
      pushFeed(msg.ts, topic, feedSummary(topic, payload), opID);
      renderPresence();
    } else if (topic === 'ping') {
      // heartbeat — no UI update needed
      return;
    } else if (topic && topic.startsWith('workflow.')) {
      // Route workflow lifecycle events to the Flows tab.
      handleFlowsWsEvent(topic, payload);
      pushFeed(msg.ts, topic, feedSummary(topic, payload), '');
    } else {
      pushFeed(msg.ts, topic, feedSummary(topic, payload), '');
    }

    renderFeed();
  }

  function pushFeed(ts, topic, summary, operatorId) {
    const color = operatorId
      ? (presence.get(operatorId) || {}).color || '#888888'
      : '#888888';
    feed.unshift({ ts: ts || new Date().toISOString(), topic, summary, operatorId, color });
    if (feed.length > FEED_CAP) feed.length = FEED_CAP;
  }

  // ---- 6. Overview rendering --------------------------------------------------

  function renderNow() {
    const nowEl = document.getElementById('now-dispatches');
    if (!nowEl) return;
    if (inFlight.size === 0) {
      nowEl.innerHTML = '<p class="empty-state">No active dispatches</p>';
    } else {
      nowEl.innerHTML = '';
      const now = Date.now();
      for (const [, d] of inFlight) {
        const elapsedS = Math.floor((now - d.startedAt.getTime()) / 1000);
        const item = document.createElement('div');
        item.className = 'dispatch-item';
        item.innerHTML =
          '<span class="dispatch-agent" aria-label="agent">' + esc(d.agent) + '</span>' +
          '<span class="dispatch-project" aria-label="project">' + esc(shortPath(d.project)) + '</span>' +
          '<span class="dispatch-elapsed" aria-label="elapsed">' + esc(String(elapsedS)) + 's</span>';
        nowEl.appendChild(item);
      }
    }
  }

  function renderPresence() {
    const presEl = document.getElementById('now-presence');
    if (!presEl) return;
    const online = [...presence.values()];
    if (online.length === 0) {
      presEl.innerHTML = '<p class="empty-state">No operators online</p>';
    } else {
      presEl.innerHTML = '';
      for (const rec of online) {
        const chip = document.createElement('div');
        chip.className = 'presence-chip';
        const color = rec.color || '#888888';
        const statusIcon = (rec.status === 'online') ? '●' : '○';
        const statusLabel = (rec.status === 'online') ? 'online' : 'offline';
        chip.innerHTML =
          '<span class="presence-icon" style="color:' + esc(color) + '" aria-hidden="true">' + statusIcon + '</span>' +
          '<span class="presence-status-label sr-only">' + esc(statusLabel) + '</span>' +
          '<span class="presence-name">' + esc(rec.display_name || rec.operator_id || 'anon') + '</span>';
        presEl.appendChild(chip);
      }
    }
  }

  function renderFeed() {
    const listEl = document.getElementById('feed-list');
    if (!listEl) return;

    const filtered = feed.filter((item) => {
      if (feedFilterTopic && !item.topic.includes(feedFilterTopic)) return false;
      if (feedFilterOp && item.operatorId !== feedFilterOp) return false;
      return true;
    });

    if (filtered.length === 0) {
      listEl.innerHTML = '<p class="empty-state">No events yet</p>';
    } else {
      listEl.innerHTML = '';
      for (const item of filtered) {
        const row = document.createElement('div');
        row.className = 'feed-item';
        const color = item.operatorId ? item.color : '#888888';
        row.innerHTML =
          '<span class="feed-dot" style="color:' + esc(color) + '" aria-hidden="true">●</span>' +
          '<span class="feed-time">' + esc(formatTime(item.ts)) + '</span>' +
          '<span class="feed-topic">' + esc(item.topic) + '</span>' +
          '<span class="feed-summary">' + esc(item.summary) + '</span>';
        listEl.appendChild(row);
      }
    }

    // Show cap notice if feed is at capacity.
    const capEl = document.getElementById('feed-cap-notice');
    if (capEl) {
      capEl.style.display = feed.length >= FEED_CAP ? '' : 'none';
    }
  }

  // ---- 7. Feed filter helpers -------------------------------------------------

  function applyFeedFilter() {
    const topicIn = document.getElementById('filter-topic');
    const opIn = document.getElementById('filter-op');
    feedFilterTopic = topicIn ? topicIn.value.trim() : '';
    feedFilterOp = opIn ? opIn.value.trim() : '';
    renderFeed();
  }

  // =========================================================================
  // ---- CHAT TAB (Phase 3c) ------------------------------------------------
  // =========================================================================
  //
  // Architecture:
  //   - ONE fetch-based SSE reader per operator (GET /api/chat/stream?operatorId=…)
  //     SW injects Authorization: Bearer; events arrive multiplexed by session_id.
  //   - Each pane owns a conversationId (persisted in localStorage; stable across
  //     tab refresh).  A fresh sessionId is generated per dispatch turn.
  //   - The SSE demux routes events to the correct pane by session_id.
  //
  // Security:
  //   - esc() on all server/operator-supplied strings before innerHTML.
  //   - operatorId sourced from localStorage (self-asserted, attribution only).
  //   - Token never in URL; always via SW header injection.

  // ---- Chat constants --------------------------------------------------------

  const RUNTIMES = ['claude', 'codex', 'agy', 'gemini'];

  // Model tiers valid for each runtime (derived from runtime.ValidateTier):
  //   haiku, sonnet, opus, fable.
  // All runtimes accept all tiers; codex/agy/gemini show "cost unavailable".
  const MODEL_TIERS = ['haiku', 'sonnet', 'opus', 'fable'];

  // Runtimes that stream incrementally (token events):
  const STREAMING_RUNTIMES = new Set(['claude']);

  const CHAT_PANEL_LS_KEY = 'yakos_chat_panes_v1'; // localStorage key for pane state
  const MAX_PANES = 6; // grid wraps after 3; up to 6 total

  // ---- Chat pane state -------------------------------------------------------
  //
  // panes: Map<paneId, PaneState>
  //
  // PaneState {
  //   id:             string (stable pane identifier)
  //   conversationId: string (generated once, persisted to localStorage)
  //   runtime:        string
  //   model:          string
  //   agent:          string
  //   activeSessionId: string | null  (in-flight dispatch session)
  //   status:         'idle' | 'streaming' | 'done' | 'error'
  //   costSoFar:      number | null   (null = unavailable)
  //   totalCost:      number | null
  //   startedAt:      Date | null
  //   elapsedTimer:   interval id | null
  //   autoScroll:     boolean
  //   messages:       Array<{role, text, ts, sessionId?, exitCode?, durationS?, costUSD?}>
  // }

  let chatPanes = new Map();   // paneId → PaneState
  let chatSSEAbort = null;     // AbortController for the SSE fetch

  // Map sessionId → paneId for fast event demux.
  let sessionToPaneId = new Map();

  // The self-asserted operator ID for this browser session.
  let chatOperatorId = '';

  function getChatOperatorId() {
    if (chatOperatorId) return chatOperatorId;
    let stored = '';
    try { stored = localStorage.getItem('yakos_operator_id') || ''; } catch { /* ignore */ }
    if (!stored || !/^[A-Za-z0-9][A-Za-z0-9._:\-]{0,127}$/.test(stored)) {
      // Mint a new random operator ID (alphanumeric, starts with a letter).
      stored = 'op-' + randomHex(12);
      try { localStorage.setItem('yakos_operator_id', stored); } catch { /* ignore */ }
    }
    chatOperatorId = stored;
    return stored;
  }

  function randomHex(n) {
    const arr = new Uint8Array(Math.ceil(n / 2));
    crypto.getRandomValues(arr);
    return Array.from(arr, b => b.toString(16).padStart(2, '0')).join('').slice(0, n);
  }

  function newPaneId() {
    return 'pane-' + randomHex(8);
  }

  function newConversationId() {
    return 'conv-' + randomHex(16);
  }

  function newSessionId() {
    return 'sess-' + randomHex(16);
  }

  // ---- Pane persistence (localStorage) ---------------------------------------

  function savePaneState() {
    const serializable = [];
    for (const [id, p] of chatPanes) {
      serializable.push({
        id,
        conversationId: p.conversationId,
        runtime: p.runtime,
        model: p.model,
        agent: p.agent,
      });
    }
    try { localStorage.setItem(CHAT_PANEL_LS_KEY, JSON.stringify(serializable)); } catch { /* ignore */ }
  }

  function loadPaneStateFromStorage() {
    let stored = null;
    try { stored = JSON.parse(localStorage.getItem(CHAT_PANEL_LS_KEY) || 'null'); } catch { /* ignore */ }
    if (!Array.isArray(stored) || stored.length === 0) return;
    for (const item of stored) {
      if (!item.id || !item.conversationId) continue;
      const p = makePane(item.id, item.conversationId);
      p.runtime = RUNTIMES.includes(item.runtime) ? item.runtime : 'claude';
      p.model = MODEL_TIERS.includes(item.model) ? item.model : 'sonnet';
      p.agent = item.agent || 'claude';
      chatPanes.set(item.id, p);
    }
  }

  function makePane(id, conversationId) {
    return {
      id,
      conversationId: conversationId || newConversationId(),
      runtime: 'claude',
      model: 'sonnet',
      agent: 'claude',
      activeSessionId: null,
      status: 'idle',
      costSoFar: null,
      totalCost: null,
      startedAt: null,
      elapsedTimer: null,
      autoScroll: true,
      messages: [],
      // Phase 3 session-attach fields.
      // attachedSessionId: non-null when this pane is watching/interjecting into
      //   an existing session (not a pane that originated the session itself).
      // watchOnly: true when the operator does not own the attached session
      //   (shared watcher — composer disabled, read-only affordance shown).
      attachedSessionId: null,
      watchOnly: false,
    };
  }

  // ---- Phase 3: openAttachPane ------------------------------------------------
  //
  // Opens a new pane bound to an existing session (attach / watch mode).
  //
  // sessionId   — the session to attach to (from /api/fleet row or /attach cmd).
  // conversationId — the conversation to backfill; defaults to sessionId when
  //                  not supplied (single-session pane convention).
  // watchOnly   — true when the caller does NOT own the session (shared watcher);
  //               false when the caller is the owner (can interject).
  //
  // The pane is registered in chatPanes and sessionToPaneId so that incoming SSE
  // frames for sessionId are routed to it by the existing SSE demux.
  // loadTranscriptForPane is called with the sessionId param so the transcript
  // handler's IsShared path applies for shared-session backfill.
  // _pushSystemMsg appends a system message into the active (focused) pane, or
  // the first pane if none is focused.  Used to surface client-side notices
  // (e.g. pane-cap errors) without a modal or alert().
  function _pushSystemMsg(text) {
    // Find the first pane in chatPanes (iteration order = insertion order).
    var firstPaneId = null;
    for (var pid of chatPanes.keys()) { firstPaneId = pid; break; }
    if (!firstPaneId) return;
    var pane = chatPanes.get(firstPaneId);
    if (!pane) return;
    pane.messages.push({ role: 'system', text: text, ts: new Date().toISOString(), sessionId: null });
    renderPaneMessages(firstPaneId);
    if (pane.autoScroll) scrollPaneToBottom(firstPaneId);
  }

  function openAttachPane(sessionId, conversationId, watchOnly) {
    if (!sessionId) return;
    // Normalise: conversationId defaults to sessionId (single-session convention).
    var convId = conversationId || sessionId;

    // Reuse existing pane if we are already watching this session.
    if (sessionToPaneId.has(sessionId)) {
      // Already attached — nothing to do; pane is already visible.
      return;
    }

    if (chatPanes.size >= MAX_PANES) {
      // S2: surface a visible notice rather than silently failing.
      _pushSystemMsg('Pane limit reached (' + MAX_PANES + ') — close a pane to attach to ' + sessionId + '.');
      return;
    }

    var p = makePane(newPaneId(), convId);
    p.attachedSessionId = sessionId;
    p.watchOnly = !!watchOnly;
    p.status = 'idle';

    chatPanes.set(p.id, p);
    sessionToPaneId.set(sessionId, p.id);

    // Rebuild the pane rail to include the new pane.
    var rail = document.getElementById('chat-pane-rail');
    if (rail) {
      rail.appendChild(buildPaneElement(p.id));
    }

    // Backfill transcript: pass sessionId so the server's IsShared check fires.
    loadTranscriptForPane(p.id);

    savePaneState();
  }

  // ---- SSE: single fetch-based reader for this operator ----------------------
  //
  // One long-lived GET /api/chat/stream?operatorId=… per browser tab.
  // The SW injects Authorization: Bearer so we never put the token in the URL.
  // Events are demuxed by session_id into the correct pane.

  let chatSSERetryMs = 1000;
  let chatSSERetryTimer = null;

  function startChatSSE() {
    // In bearer mode, require a token.  In session mode, the browser sends
    // the session cookie automatically — no bearer token needed.
    if (AUTH_MODE === 'bearer' && !TOKEN) return;
    if (chatSSEAbort) chatSSEAbort.abort();
    chatSSEAbort = new AbortController();
    const opId = getChatOperatorId();
    // operatorId goes in the query string (attribution, not auth).
    const url = '/api/chat/stream?operatorId=' + encodeURIComponent(opId);

    const fetchOpts = {
      method: 'GET',
      signal: chatSSEAbort.signal,
    };
    if (AUTH_MODE === 'session') {
      // Session mode: browser sends session cookie; SW injects CSRF on mutations.
      // SSE is a GET — no CSRF header needed.
      fetchOpts.credentials = 'same-origin';
    }
    // bearer mode: SW injects Authorization: Bearer automatically.

    fetch(url, fetchOpts).then((resp) => {
      if (!resp.ok) {
        console.warn('[chat SSE] connect failed:', resp.status);
        // 401 in session mode: session expired — redirect to login.
        if (resp.status === 401 && AUTH_MODE === 'session') {
          window.top.location.href = '/login';
          return;
        }
        // 401/403 in bearer mode: permanent auth failure — stop retrying.
        if (resp.status === 401 || resp.status === 403) {
          console.error('[chat SSE] auth failure (' + resp.status + ') — stopping SSE retry');
          return;
        }
        scheduleSSERetry();
        return;
      }
      chatSSERetryMs = 1000; // reset backoff on success
      const reader = resp.body.getReader();
      const decoder = new TextDecoder();
      let buf = '';

      function pump() {
        reader.read().then(({ done, value }) => {
          if (done) {
            scheduleSSERetry();
            return;
          }
          buf += decoder.decode(value, { stream: true });
          // SSE frames are delimited by a blank line (\n\n or \r\n\r\n).
          // Normalise \r\n to \n so a single split covers both wire variants.
          buf = buf.replace(/\r\n/g, '\n');
          const frames = buf.split('\n\n');
          buf = frames.pop(); // keep the incomplete last chunk
          for (const frame of frames) {
            if (!frame.trim()) continue;
            // Per RFC 8895: a frame may contain multiple lines; accumulate all
            // "data:" lines before parsing (handles multi-line data values).
            const lines = frame.split('\n');
            let dataParts = [];
            let isComment = false;
            for (const line of lines) {
              if (line.startsWith(':')) { isComment = true; continue; } // heartbeat / comment
              if (line.startsWith('data: ')) {
                dataParts.push(line.slice(6));
              } else if (line === 'data') {
                dataParts.push('');
              }
            }
            if (dataParts.length === 0) continue; // comment-only frame or no data
            const jsonStr = dataParts.join('\n');
            try {
              const ev = JSON.parse(jsonStr);
              handleSSEEvent(ev);
            } catch {
              // ignore malformed JSON
            }
          }
          pump();
        }).catch((err) => {
          if (err && err.name === 'AbortError') return; // deliberate disconnect
          scheduleSSERetry();
        });
      }
      pump();
    }).catch((err) => {
      if (err && err.name === 'AbortError') return;
      scheduleSSERetry();
    });
  }

  function scheduleSSERetry() {
    if (chatSSERetryTimer) clearTimeout(chatSSERetryTimer);
    // Jitter: randomise delay to prevent thundering-herd reconnects when many
    // tabs retry simultaneously after a daemon restart.
    const jitteredDelay = chatSSERetryMs * (0.5 + Math.random() * 0.5);
    chatSSERetryTimer = setTimeout(() => {
      chatSSERetryMs = Math.min(chatSSERetryMs * 2, 30000);
      startChatSSE();
    }, jitteredDelay);
  }

  // ---- SSE event demux -------------------------------------------------------

  function handleSSEEvent(ev) {
    // ev: {session_id, type, text?, exit_code?, duration_s?, total_cost_usd?,
    //       model_resolved?, tool_name?, tool_input?, tool_output?, is_error?, ts}
    const sessionId = ev.session_id;
    if (!sessionId) return;

    const paneId = sessionToPaneId.get(sessionId);
    if (!paneId) return; // unknown session — ignore (could belong to another tab)

    const pane = chatPanes.get(paneId);
    if (!pane) return;

    if (ev.type === 'token') {
      // Append token text to the last streaming message, or start a new one.
      const msgs = pane.messages;
      const last = msgs[msgs.length - 1];
      if (last && last.role === 'assistant' && last.sessionId === sessionId && last.streaming) {
        last.text += ev.text || '';
      } else {
        msgs.push({ role: 'assistant', text: ev.text || '', ts: ev.ts, sessionId, streaming: true });
      }
      renderPaneMessages(paneId);
      if (pane.autoScroll) scrollPaneToBottom(paneId);
    } else if (ev.type === 'tool_use') {
      // Phase 4: collapsible tool-invocation block.
      // XSS discipline: esc() applied to all server-supplied strings before DOM insertion.
      // ToolName and ToolInput arrive from the runtime (untrusted text).
      pane.messages.push({
        role: 'tool_use',
        toolName: ev.tool_name || '',
        toolInput: ev.tool_input || '',
        ts: ev.ts,
        sessionId,
      });
      renderPaneMessages(paneId);
      if (pane.autoScroll) scrollPaneToBottom(paneId);
    } else if (ev.type === 'tool_result') {
      // Phase 4: tool result block, rendered inside the tool_use collapsible.
      // Correlate with the most recent tool_use for the same session by appending
      // the output to that message.  If no prior tool_use is found, render standalone.
      // XSS discipline: esc() on toolName + toolOutput.
      const msgs = pane.messages;
      // Walk backwards to find the most recent tool_use for this session without output yet.
      let matched = false;
      for (let i = msgs.length - 1; i >= 0; i--) {
        const m = msgs[i];
        if (m.role === 'tool_use' && m.sessionId === sessionId && !m.hasResult) {
          m.toolOutput = ev.tool_output || '';
          m.isError = !!ev.is_error;
          m.hasResult = true;
          matched = true;
          break;
        }
      }
      if (!matched) {
        // No prior tool_use found — render as standalone tool_result.
        msgs.push({
          role: 'tool_result',
          toolName: ev.tool_name || '',
          toolOutput: ev.tool_output || '',
          isError: !!ev.is_error,
          ts: ev.ts,
          sessionId,
        });
      }
      renderPaneMessages(paneId);
      if (pane.autoScroll) scrollPaneToBottom(paneId);
    } else if (ev.type === 'summary') {
      // Mark the last streaming message as done.
      const msgs = pane.messages;
      const last = msgs[msgs.length - 1];
      if (last && last.streaming) {
        last.streaming = false;
      }

      // Cost update.
      const costUSD = ev.total_cost_usd != null ? ev.total_cost_usd : null;
      pane.totalCost = costUSD;
      pane.costSoFar = costUSD;

      // Status summary message.
      const exitCode = ev.exit_code != null ? ev.exit_code : null;
      const durationS = ev.duration_s != null ? ev.duration_s : null;
      msgs.push({
        role: 'summary',
        text: '',
        ts: ev.ts,
        sessionId,
        exitCode,
        durationS,
        costUSD,
      });

      // Done streaming.
      pane.status = exitCode === 0 ? 'done' : 'error';
      pane.activeSessionId = null;
      sessionToPaneId.delete(sessionId);
      stopElapsedTimer(pane);
      renderPaneHeader(paneId);
      renderPaneMessages(paneId);
      // Announce to screen reader (aria-live polite — not per-token).
      announcePaneStatus(paneId, pane.status === 'done' ? 'completed' : 'finished with error');
      if (pane.autoScroll) scrollPaneToBottom(paneId);
    }
  }

  // ---- Phase 4: tool block label helpers -------------------------------------

  // toolLabel returns a short human-readable label for a tool invocation,
  // suitable for the collapsible summary line.
  // XSS discipline: caller is responsible for esc()-wrapping the return value
  // before inserting into innerHTML.
  function toolLabel(toolName, toolInput) {
    // For Bash, extract the command string from JSON args for a concise label.
    // Falls back to the raw input if unparseable or empty.
    if (toolName === 'Bash' && toolInput) {
      try {
        var parsed = JSON.parse(toolInput);
        var cmd = parsed.command || parsed.cmd || '';
        if (cmd) return 'ran Bash: ' + cmd;
      } catch (_) { /* fall through */ }
    }
    if (toolName) return toolName + (toolInput ? ': ' + toolInput.slice(0, 60) : '');
    return toolInput ? toolInput.slice(0, 80) : '(tool)';
  }

  // ---- Chat tab init ---------------------------------------------------------

  let chatTabInitialized = false;  // true once chat infra (SSE, panes) is booted
  let chatLayoutRendered = false;  // true once the Chat tab's DOM layout is rendered

  // ---- Slash-command popover state -------------------------------------------
  //
  // skillsCache holds the last successful /api/skills response.
  // Fetched once per chat-tab init (no polling — the roster changes only on
  // restart; a manual refresh is not needed for Phase 1).
  //
  // skillsFetched guards the single fetch so bootChatInfrastructure re-entries
  // (IDE tab boot) do not issue duplicate requests.
  var skillsCache = { agents: [], commands: [] };
  var skillsFetched = false;

  // fetchSkills fetches GET /api/skills and caches the result in skillsCache.
  // Silent on failure: the popover simply shows the partial list.
  function fetchSkills() {
    if (skillsFetched) return;
    skillsFetched = true;
    apiFetch('GET', '/api/skills').then(function(resp) {
      if (!resp.ok) return;
      return resp.json();
    }).then(function(data) {
      if (data && Array.isArray(data.agents) && Array.isArray(data.commands)) {
        skillsCache = data;
      }
    }).catch(function() {
      // Silent: popover still shows static client commands from initial empty cache.
    });
  }

  // bootChatInfrastructure mints the operator ID, loads persisted pane state,
  // seeds a default pane if none exist, and starts the SSE reader.  It is
  // idempotent (no-op once chatTabInitialized is true) and called by BOTH
  // initChatTab and initIdeTab so the boot sequence is defined exactly once.
  function bootChatInfrastructure() {
    if (chatTabInitialized) return;
    getChatOperatorId();
    loadPaneStateFromStorage();
    if (chatPanes.size === 0) {
      const p = makePane(newPaneId(), newConversationId());
      chatPanes.set(p.id, p);
      savePaneState();
    }
    if (!chatSSEAbort) {
      startChatSSE();
    }
    // Prefetch the agent/command catalog for the "/" popover.
    fetchSkills();
    chatTabInitialized = true;
  }

  function initChatTab() {
    // Always boot infra (idempotent — safe if IDE tab already ran it).
    bootChatInfrastructure();

    // Render the Chat tab layout exactly once: on first open it builds the
    // DOM; on repeat tab switches it is a no-op so panes aren't duplicated.
    // chatLayoutRendered is separate from chatTabInitialized so that infra
    // pre-booted by initIdeTab does not prevent the layout from rendering.
    if (chatLayoutRendered) return;
    chatLayoutRendered = true;

    renderChatLayout();

    // Load transcripts for real chat panes only — skip ideEmbedded panes,
    // which are managed by mountIdeChatPane and must not appear in the Chat rail.
    for (const [paneId, pane] of chatPanes) {
      if (pane.ideEmbedded) continue;
      loadTranscriptForPane(paneId);
    }
  }

  // ---- Chat layout rendering -------------------------------------------------

  function renderChatLayout() {
    const panel = document.getElementById('panel-chat');
    if (!panel) return;

    // Top toolbar: operator badge + "New pane" button.
    panel.innerHTML =
      '<div class="chat-toolbar" role="toolbar" aria-label="Chat controls">' +
        '<span class="chat-op-badge" id="chat-op-badge" title="Your operator ID (self-asserted)">' +
          '<span class="presence-icon" aria-hidden="true">●</span>' +
          '<span id="chat-op-id">' + esc(getChatOperatorId()) + '</span>' +
        '</span>' +
        '<button class="chat-new-pane-btn" id="chat-new-pane" type="button" ' +
          'aria-label="Open a new chat pane">' +
          '&#xFF0B; New pane' +
        '</button>' +
        '<span id="chat-total-cost" class="chat-total-cost" aria-label="Total cost across all panes"></span>' +
      '</div>' +
      '<div class="chat-pane-rail" id="chat-pane-rail" role="region" aria-label="Chat panes"></div>';

    document.getElementById('chat-new-pane').addEventListener('click', addNewPane);

    renderAllPanes();
    updateTotalCostBadge();
  }

  function renderAllPanes() {
    const rail = document.getElementById('chat-pane-rail');
    if (!rail) return;
    rail.innerHTML = '';
    for (const [paneId, pane] of chatPanes) {
      if (pane.ideEmbedded) continue; // IDE-embedded pane lives in the IDE slot, not the Chat rail
      rail.appendChild(buildPaneElement(paneId));
    }
  }

  function addNewPane() {
    if (chatPanes.size >= MAX_PANES) {
      // Soft cap: inform user.
      const rail = document.getElementById('chat-pane-rail');
      if (rail) {
        const notice = document.createElement('div');
        notice.className = 'chat-cap-notice';
        notice.setAttribute('role', 'status');
        notice.textContent = 'Maximum ' + MAX_PANES + ' panes open. Close one first.';
        rail.insertBefore(notice, rail.firstChild);
        setTimeout(() => notice.remove(), 3000);
      }
      return;
    }
    const p = makePane(newPaneId(), newConversationId());
    chatPanes.set(p.id, p);
    savePaneState();

    const rail = document.getElementById('chat-pane-rail');
    if (rail) {
      rail.appendChild(buildPaneElement(p.id));
    }
  }

  function closePane(paneId) {
    const pane = chatPanes.get(paneId);
    if (!pane) return;
    // If streaming, cancel first.  cancelPaneSession nulls activeSessionId, so
    // any reference to pane.activeSessionId below reflects the post-cancel state.
    if (pane.activeSessionId) {
      cancelPaneSession(paneId);
    }
    stopElapsedTimer(pane);
    // Note: pane.activeSessionId is null here — cancelPaneSession (above) always
    // nulls it, and if there was no active session the guard above skipped it.
    // The sessionToPaneId entry was already removed inside cancelPaneSession.
    chatPanes.delete(paneId);
    savePaneState();
    const el = document.getElementById('pane-' + paneId);
    if (el) el.remove();
    updateTotalCostBadge();

    // If no panes remain, show the empty state.
    if (chatPanes.size === 0) {
      const rail = document.getElementById('chat-pane-rail');
      if (rail) {
        rail.innerHTML = '<div class="chat-empty-state"><p>No panes open. Click <b>+ New pane</b> to start.</p></div>';
      }
    }
  }

  // ---- Pane DOM construction -------------------------------------------------

  function buildPaneElement(paneId) {
    const pane = chatPanes.get(paneId);
    if (!pane) return document.createDocumentFragment();

    const el = document.createElement('div');
    el.className = 'chat-pane';
    el.id = 'pane-' + paneId;

    // Phase 3: watch-only attach panes disable the composer so the operator
    // cannot accidentally submit a task into a session they do not own.
    // The textarea is rendered with disabled + aria-disabled so screen readers
    // also announce the restriction.  Owner attach panes (watchOnly=false) keep
    // the composer enabled (interject path).
    const composerDisabled = pane.watchOnly ? ' disabled' : '';
    const composerAriaLabel = pane.watchOnly
      ? 'Read-only: watching another operator\'s session'
      : 'Task prompt for this pane';
    const composerPlaceholder = pane.watchOnly
      ? 'Watching — read only'
      : 'Task prompt…';

    el.innerHTML =
      // Header
      '<div class="chat-pane-header" id="pane-header-' + esc(paneId) + '">' +
        buildPaneHeaderHTML(paneId) +
      '</div>' +
      // Messages
      '<div class="chat-messages" id="pane-msgs-' + esc(paneId) + '" ' +
        'role="log" aria-label="Conversation" aria-live="polite"></div>' +
      // Status announcer (aria-live=polite, not per-token)
      '<div id="pane-announce-' + esc(paneId) + '" class="sr-only" aria-live="polite" aria-atomic="true"></div>' +
      // Input area
      '<div class="chat-input-area">' +
        '<textarea class="chat-input" id="pane-input-' + esc(paneId) + '" ' +
          'rows="3" placeholder="' + esc(composerPlaceholder) + '" ' +
          'aria-label="' + esc(composerAriaLabel) + '"' +
          composerDisabled + '></textarea>' +
        '<button class="chat-send-btn" id="pane-send-' + esc(paneId) + '" type="button" ' +
          'aria-label="Send task"' + composerDisabled + '>Send</button>' +
      '</div>';

    // Wire events after inserting.
    requestAnimationFrame(() => wirePaneEvents(paneId));

    return el;
  }

  function buildPaneHeaderHTML(paneId) {
    const pane = chatPanes.get(paneId);
    if (!pane) return '';

    // Phase 3: attached (watch/interject) panes show a simplified header.
    // Watch-only panes suppress the runtime/model/agent controls (read-only)
    // and display a "watching — read only" badge instead.
    if (pane.attachedSessionId) {
      const statusClass = 'pane-status-' + esc(pane.status);
      const statusLabel = paneStatusLabel(pane.status);
      const attachBadge = pane.watchOnly
        ? '<span class="pane-watch-badge" aria-label="Read-only watch mode">watching — read only</span>'
        : '<span class="pane-watch-badge pane-watch-badge-owner" aria-label="Interject mode">interjecting</span>';
      return '<div class="pane-header-row">' +
          attachBadge +
          '<span class="pane-attach-session" title="' + esc(pane.attachedSessionId) + '">' +
            esc(pane.attachedSessionId) +
          '</span>' +
          '<span class="pane-status ' + statusClass + '" id="pane-status-' + esc(paneId) + '" ' +
            'aria-label="Status: ' + esc(statusLabel) + '">' +
            paneStatusIcon(pane.status) + '<span class="sr-only">' + esc(statusLabel) + '</span>' +
          '</span>' +
          '<button class="pane-close-btn" id="pane-close-' + esc(paneId) + '" type="button" ' +
            'aria-label="Close this pane">&times;</button>' +
        '</div>';
    }

    const runtimeOpts = RUNTIMES.map((r) =>
      '<option value="' + esc(r) + '"' + (r === pane.runtime ? ' selected' : '') + '>' + esc(r) + '</option>'
    ).join('');

    const modelOpts = MODEL_TIERS.map((m) =>
      '<option value="' + esc(m) + '"' + (m === pane.model ? ' selected' : '') + '>' + esc(m) + '</option>'
    ).join('');

    const statusClass = 'pane-status-' + esc(pane.status);
    const statusLabel = paneStatusLabel(pane.status);
    const costStr = formatCost(pane.costSoFar, pane.runtime);

    const opId = getChatOperatorId();
    const opColor = operatorColor(opId);

    return '<div class="pane-header-row">' +
        '<span class="pane-op-badge" style="color:' + esc(opColor) + '" ' +
          'aria-label="Operator" title="' + esc(opId) + '">●</span>' +
        '<select class="pane-runtime-select" id="pane-runtime-' + esc(paneId) + '" ' +
          'aria-label="Runtime">' + runtimeOpts + '</select>' +
        '<select class="pane-model-select" id="pane-model-' + esc(paneId) + '" ' +
          'aria-label="Model tier">' + modelOpts + '</select>' +
        '<input class="pane-agent-input" id="pane-agent-' + esc(paneId) + '" ' +
          'type="text" value="' + esc(pane.agent) + '" ' +
          'placeholder="agent name" aria-label="Agent name" maxlength="80">' +
        '<span class="pane-cost" id="pane-cost-' + esc(paneId) + '">' + esc(costStr) + '</span>' +
        '<span class="pane-elapsed" id="pane-elapsed-' + esc(paneId) + '"></span>' +
        '<span class="pane-status ' + statusClass + '" id="pane-status-' + esc(paneId) + '" ' +
          'aria-label="Status: ' + esc(statusLabel) + '">' +
          paneStatusIcon(pane.status) + '<span class="sr-only">' + esc(statusLabel) + '</span>' +
        '</span>' +
        '<button class="pane-share-btn" id="pane-share-' + esc(paneId) + '" type="button" ' +
          'title="Share pane" aria-label="Share or unshare pane" aria-pressed="false">&#x1F517;</button>' +
        '<button class="pane-cancel-btn" id="pane-cancel-' + esc(paneId) + '" type="button" ' +
          'aria-label="Stop streaming" ' +
          (pane.status === 'streaming' ? '' : 'disabled ') + '>&#x23F9;</button>' +
        '<button class="pane-close-btn" id="pane-close-' + esc(paneId) + '" type="button" ' +
          'aria-label="Close this pane">&times;</button>' +
      '</div>';
  }

  function wirePaneEvents(paneId) {
    const pane = chatPanes.get(paneId);
    if (!pane) return;

    // Runtime → model dependency: when runtime changes, update model options.
    const runtimeSel = document.getElementById('pane-runtime-' + paneId);
    if (runtimeSel) {
      runtimeSel.addEventListener('change', () => {
        pane.runtime = runtimeSel.value;
        savePaneState();
      });
    }

    const modelSel = document.getElementById('pane-model-' + paneId);
    if (modelSel) {
      modelSel.addEventListener('change', () => {
        pane.model = modelSel.value;
        savePaneState();
      });
    }

    const agentIn = document.getElementById('pane-agent-' + paneId);
    if (agentIn) {
      agentIn.addEventListener('change', () => {
        pane.agent = agentIn.value.trim() || 'claude';
        savePaneState();
      });
    }

    // Stop button.
    const cancelBtn = document.getElementById('pane-cancel-' + paneId);
    if (cancelBtn) {
      cancelBtn.addEventListener('click', () => cancelPaneSession(paneId));
    }

    // Share toggle.
    const shareBtn = document.getElementById('pane-share-' + paneId);
    if (shareBtn) {
      shareBtn.addEventListener('click', () => togglePaneShare(paneId));
    }

    // Close button.
    const closeBtn = document.getElementById('pane-close-' + paneId);
    if (closeBtn) {
      closeBtn.addEventListener('click', () => closePane(paneId));
    }

    // Send button + Ctrl+Enter.
    const sendBtn = document.getElementById('pane-send-' + paneId);
    const textarea = document.getElementById('pane-input-' + paneId);
    if (sendBtn && textarea) {
      sendBtn.addEventListener('click', () => sendPaneMessage(paneId));
      textarea.addEventListener('keydown', (e) => {
        if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
          e.preventDefault();
          sendPaneMessage(paneId);
          return;
        }
        // Slash-command popover navigation keys.
        const pop = document.getElementById('slash-popover-' + paneId);
        if (pop) {
          if (e.key === 'Escape') {
            e.preventDefault();
            hideSlashPopover(paneId);
            return;
          }
          if (e.key === 'ArrowDown') {
            e.preventDefault();
            moveSlashPopoverCursor(paneId, 1);
            return;
          }
          if (e.key === 'ArrowUp') {
            e.preventDefault();
            moveSlashPopoverCursor(paneId, -1);
            return;
          }
          if (e.key === 'Enter' || e.key === 'Tab') {
            e.preventDefault();
            commitSlashPopoverSelection(paneId);
            return;
          }
        }
      });
      textarea.addEventListener('input', function() {
        const val = textarea.value;
        if (val.startsWith('/')) {
          showSlashPopover(paneId, val.slice(1));
        } else {
          hideSlashPopover(paneId);
        }
      });
    }

    // Auto-scroll: pause when user scrolls up; resume when back at bottom.
    const msgsEl = document.getElementById('pane-msgs-' + paneId);
    if (msgsEl) {
      msgsEl.addEventListener('scroll', () => {
        const atBottom = msgsEl.scrollHeight - msgsEl.scrollTop <= msgsEl.clientHeight + 40;
        pane.autoScroll = atBottom;
        const indicator = document.getElementById('pane-scroll-indicator-' + paneId);
        if (indicator) {
          indicator.style.display = pane.autoScroll ? 'none' : '';
        }
      });
    }

    // Render initial messages.
    renderPaneMessages(paneId);
  }

  // ---- Slash-command popover -------------------------------------------------
  //
  // showSlashPopover(paneId, filter) renders a filterable listbox above the
  // chat textarea showing matching agents (sets dispatch target) and client
  // commands (clear/help/attach).
  //
  // DOM structure (appended to the .chat-input-area of the pane):
  //   <div id="slash-popover-<paneId>" class="slash-popover" role="listbox"
  //        aria-label="Slash commands">
  //     <div class="slash-popover-section-label">Agents</div>
  //     <div class="slash-popover-item" role="option" aria-selected="false"
  //          data-slash-type="agent" data-slash-name="backend">
  //       <span class="slash-item-name">backend</span>
  //       <span class="slash-item-desc">…</span>
  //       <span class="slash-item-meta">claude</span>
  //     </div>
  //     … client command entries …
  //   </div>
  //
  // Navigation: ArrowUp/Down move cursor; Enter/Tab commit; Esc dismiss.
  // All three key handlers are registered in wirePaneEvents.
  //
  // Accessibility: role=listbox + role=option + aria-selected mirrors the
  // Users-tab table-row pattern and aligns with ARIA authoring practices for
  // combo-box widgets.

  // _slashCursor[paneId] tracks the 0-based index of the focused item.
  var _slashCursor = {};

  // _slashDocClickHandlers[paneId] holds the document 'click' handler registered
  // when the popover opens.  Storing the reference here lets hideSlashPopover
  // call removeEventListener unconditionally — whether the popover was dismissed
  // via Esc, Enter/Tab, mousedown, or an outside click — so no dead listener
  // accumulates across repeated open → close cycles.
  var _slashDocClickHandlers = {};

  function _slashItems(paneId) {
    var pop = document.getElementById('slash-popover-' + paneId);
    if (!pop) return [];
    return Array.prototype.slice.call(pop.querySelectorAll('[role="option"]'));
  }

  function showSlashPopover(paneId, filter) {
    var f = (filter || '').toLowerCase();

    // Build filtered lists.
    var agents = (skillsCache.agents || []).filter(function(a) {
      return !f || a.name.toLowerCase().indexOf(f) !== -1 ||
             (a.description || '').toLowerCase().indexOf(f) !== -1;
    });
    var commands = (skillsCache.commands || []).filter(function(c) {
      return !f || c.name.toLowerCase().indexOf(f) !== -1 ||
             (c.summary || '').toLowerCase().indexOf(f) !== -1;
    });

    // Nothing matches — hide and return.
    if (agents.length === 0 && commands.length === 0) {
      hideSlashPopover(paneId);
      return;
    }

    // Find or create the popover element.
    var inputArea = null;
    var textarea = document.getElementById('pane-input-' + paneId);
    if (textarea) inputArea = textarea.parentElement;
    if (!inputArea) return;

    var pop = document.getElementById('slash-popover-' + paneId);
    if (!pop) {
      pop = document.createElement('div');
      pop.id = 'slash-popover-' + paneId;
      pop.className = 'slash-popover';
      pop.setAttribute('role', 'listbox');
      pop.setAttribute('aria-label', 'Slash commands — agents and client actions');
      inputArea.appendChild(pop);

      // Dismiss on click outside.  Store the handler reference in
      // _slashDocClickHandlers so hideSlashPopover can remove it
      // unconditionally regardless of how the popover is dismissed
      // (Esc, Enter/Tab, mousedown, or outside click).  Without this
      // a dead listener accumulates on every open → non-click-dismiss cycle.
      var docClickHandler = function(e) {
        var p = document.getElementById('slash-popover-' + paneId);
        if (p && !p.contains(e.target) && e.target !== textarea) {
          hideSlashPopover(paneId);
        }
      };
      _slashDocClickHandlers[paneId] = docClickHandler;
      document.addEventListener('click', docClickHandler);
    }

    // Reset cursor on every re-render.
    _slashCursor[paneId] = 0;

    // Build inner HTML.
    var html = '';

    if (agents.length > 0) {
      html += '<div class="slash-popover-section-label" aria-hidden="true">Agents</div>';
      agents.forEach(function(a) {
        html +=
          '<div class="slash-popover-item" role="option" aria-selected="false" ' +
               'data-slash-type="agent" data-slash-name="' + esc(a.name) + '">' +
            '<span class="slash-item-name">' + esc(a.name) + '</span>' +
            '<span class="slash-item-desc">' + esc(a.description || '') + '</span>' +
            '<span class="slash-item-meta">' + esc(a.runtime || '') + '</span>' +
          '</div>';
      });
    }

    if (commands.length > 0) {
      html += '<div class="slash-popover-section-label" aria-hidden="true">Commands</div>';
      commands.forEach(function(c) {
        html +=
          '<div class="slash-popover-item" role="option" aria-selected="false" ' +
               'data-slash-type="command" data-slash-name="' + esc(c.name) + '">' +
            '<span class="slash-item-name">/' + esc(c.name) + '</span>' +
            '<span class="slash-item-desc">' + esc(c.summary || '') + '</span>' +
          '</div>';
      });
    }

    pop.innerHTML = html;

    // Wire click handlers on each option.
    Array.prototype.forEach.call(pop.querySelectorAll('[role="option"]'), function(item) {
      item.addEventListener('mousedown', function(e) {
        // mousedown fires before textarea blur; preventDefault keeps focus in textarea.
        e.preventDefault();
        _applySlashItem(paneId, item);
      });
    });

    // Highlight first item.
    _updateSlashCursor(paneId, 0);
  }

  function hideSlashPopover(paneId) {
    // Always remove the document click listener first — regardless of dismissal
    // path (Esc, Enter/Tab, mousedown, or outside click).  This is the fix for
    // the listener leak: without this, every Esc/Enter/Tab dismissal left one
    // dead listener behind and the next open registered another.
    var handler = _slashDocClickHandlers[paneId];
    if (handler) {
      document.removeEventListener('click', handler);
      delete _slashDocClickHandlers[paneId];
    }
    var pop = document.getElementById('slash-popover-' + paneId);
    if (pop && pop.parentNode) pop.parentNode.removeChild(pop);
    delete _slashCursor[paneId];
  }

  function _updateSlashCursor(paneId, idx) {
    var items = _slashItems(paneId);
    if (!items.length) return;
    // Clamp index.
    idx = Math.max(0, Math.min(idx, items.length - 1));
    _slashCursor[paneId] = idx;
    items.forEach(function(item, i) {
      var active = i === idx;
      item.setAttribute('aria-selected', active ? 'true' : 'false');
      if (active) {
        item.classList.add('slash-popover-item--active');
        // Scroll into view within the popover.
        if (item.scrollIntoView) item.scrollIntoView({ block: 'nearest' });
      } else {
        item.classList.remove('slash-popover-item--active');
      }
    });
  }

  function moveSlashPopoverCursor(paneId, delta) {
    var cur = _slashCursor[paneId] || 0;
    _updateSlashCursor(paneId, cur + delta);
  }

  function commitSlashPopoverSelection(paneId) {
    var items = _slashItems(paneId);
    var cur = _slashCursor[paneId] || 0;
    var item = items[cur];
    if (item) _applySlashItem(paneId, item);
  }

  function _applySlashItem(paneId, item) {
    var type = item.getAttribute('data-slash-type');
    var name = item.getAttribute('data-slash-name');
    hideSlashPopover(paneId);

    var textarea = document.getElementById('pane-input-' + paneId);
    var pane = chatPanes.get(paneId);

    if (type === 'agent') {
      // Set the dispatch target and clear the "/" prefix from the textarea.
      if (pane) {
        pane.agent = name;
        savePaneState();
        renderPaneHeader(paneId);
      }
      if (textarea) {
        // Replace the slash-filter text (everything from the "/" onward)
        // with an empty string so the user can type their task.
        textarea.value = '';
        textarea.focus();
      }
    } else if (type === 'command') {
      if (textarea) {
        textarea.value = '';
        textarea.focus();
      }
      if (name === 'clear') {
        // Clear the pane's messages.
        if (pane) {
          pane.messages = [];
          renderPaneMessages(paneId);
        }
      } else if (name === 'help') {
        // Show a brief help message as a system message in the pane.
        if (pane) {
          var helpLines = ['Available commands:'];
          (skillsCache.commands || []).forEach(function(c) {
            helpLines.push('  /' + c.name + ' — ' + (c.summary || ''));
          });
          helpLines.push('');
          helpLines.push('Available agents:');
          (skillsCache.agents || []).forEach(function(a) {
            helpLines.push('  ' + a.name + ' — ' + (a.description || ''));
          });
          pane.messages.push({
            role: 'system',
            text: helpLines.join('\n'),
            ts: new Date().toISOString(),
            sessionId: null,
          });
          renderPaneMessages(paneId);
          if (pane.autoScroll) scrollPaneToBottom(paneId);
        }
      }
      // Phase 3: /attach <sessionId> — open an attach pane for the given session.
      // The sessionId comes from the text the operator typed after "/attach ".
      // Ownership is determined by checking fleetSessions: if the session is in the
      // fleet map, the server-supplied "owned" field is authoritative.
      // If the sessionId is not in fleetSessions (typed manually), we open in
      // watchOnly mode as the conservative default.
      if (name === 'attach') {
        // Extract the sessionId from the textarea text following "/attach ".
        var rawText = textarea ? textarea.value.trim() : '';
        // rawText may be "/attach <sessionId>" or just the sessionId remainder.
        var attachSessionId = rawText.replace(/^\/attach\s*/i, '').trim();
        if (textarea) textarea.value = '';
        if (attachSessionId) {
          // Determine watchOnly via server-supplied owned field.
          var fleetRow = fleetSessions.get(attachSessionId);
          var isOwned = !!(fleetRow && fleetRow.owned);
          openAttachPane(attachSessionId, attachSessionId, !isOwned);
        }
      }
    }
  }

  // ---- Pane header re-render (cheaper than full rebuild) --------------------

  function renderPaneHeader(paneId) {
    const headerEl = document.getElementById('pane-header-' + paneId);
    if (!headerEl) return;
    headerEl.innerHTML = buildPaneHeaderHTML(paneId);
    // Re-wire header buttons.
    const cancelBtn = document.getElementById('pane-cancel-' + paneId);
    if (cancelBtn) cancelBtn.addEventListener('click', () => cancelPaneSession(paneId));
    const shareBtn = document.getElementById('pane-share-' + paneId);
    if (shareBtn) shareBtn.addEventListener('click', () => togglePaneShare(paneId));
    const closeBtn = document.getElementById('pane-close-' + paneId);
    if (closeBtn) closeBtn.addEventListener('click', () => closePane(paneId));
    const runtimeSel = document.getElementById('pane-runtime-' + paneId);
    const modelSel = document.getElementById('pane-model-' + paneId);
    const agentIn = document.getElementById('pane-agent-' + paneId);
    const pane = chatPanes.get(paneId);
    if (pane && runtimeSel) runtimeSel.addEventListener('change', () => { pane.runtime = runtimeSel.value; savePaneState(); });
    if (pane && modelSel) modelSel.addEventListener('change', () => { pane.model = modelSel.value; savePaneState(); });
    if (pane && agentIn) agentIn.addEventListener('change', () => { pane.agent = agentIn.value.trim() || 'claude'; savePaneState(); });
  }

  // ---- Messages rendering ---------------------------------------------------

  function renderPaneMessages(paneId) {
    const pane = chatPanes.get(paneId);
    const msgsEl = document.getElementById('pane-msgs-' + paneId);
    if (!pane || !msgsEl) return;

    msgsEl.innerHTML = '';

    // Scroll-pause indicator.
    const indicator = document.createElement('div');
    indicator.className = 'chat-scroll-indicator';
    indicator.id = 'pane-scroll-indicator-' + paneId;
    indicator.style.display = pane.autoScroll ? 'none' : '';
    indicator.textContent = 'New messages below — click to resume auto-scroll';
    indicator.setAttribute('role', 'status');
    indicator.addEventListener('click', () => {
      pane.autoScroll = true;
      indicator.style.display = 'none';
      scrollPaneToBottom(paneId);
    });
    msgsEl.appendChild(indicator);

    for (const msg of pane.messages) {
      msgsEl.appendChild(buildMessageElement(msg, pane));
    }

    // Streaming shimmer line (if last message is streaming).
    const last = pane.messages[pane.messages.length - 1];
    if (last && last.role === 'assistant' && last.streaming) {
      const shimmer = document.createElement('span');
      shimmer.className = 'chat-streaming-cursor';
      shimmer.setAttribute('aria-hidden', 'true');
      msgsEl.lastElementChild && msgsEl.lastElementChild.appendChild(shimmer);
    }
  }

  function buildMessageElement(msg, pane) {
    const el = document.createElement('div');

    if (msg.role === 'user') {
      el.className = 'chat-msg chat-msg-user';
      el.innerHTML =
        '<span class="chat-msg-role" aria-hidden="true">you</span>' +
        '<span class="chat-msg-time">' + esc(formatTime(msg.ts)) + '</span>' +
        '<div class="chat-msg-text">' + escLines(msg.text) + '</div>';
    } else if (msg.role === 'assistant') {
      el.className = 'chat-msg chat-msg-assistant' + (msg.streaming ? ' streaming' : '');
      el.innerHTML =
        '<span class="chat-msg-role" aria-hidden="true">agent</span>' +
        '<span class="chat-msg-time">' + esc(formatTime(msg.ts)) + '</span>' +
        '<div class="chat-msg-text">' + escLines(msg.text) + '</div>';
    } else if (msg.role === 'tool_use') {
      // Phase 4: collapsible tool-invocation block.
      // XSS discipline: all server-supplied strings go through esc() before innerHTML.
      // tool_name and tool_input arrive from the runtime — treat as untrusted.
      el.className = 'chat-msg chat-msg-tool';
      var label = toolLabel(msg.toolName, msg.toolInput);
      // Render as a <details> collapsible so the operator can expand to see args.
      var outputHtml = '';
      if (msg.hasResult) {
        // If this tool_use has its result merged in, render the output inside the block.
        var outputClass = msg.isError ? 'chat-tool-output chat-tool-output-error' : 'chat-tool-output';
        outputHtml = '<div class="' + esc(outputClass) + '">' +
          '<span class="chat-tool-output-label" aria-hidden="true">' + (msg.isError ? 'error' : 'output') + '</span>' +
          '<pre class="chat-tool-output-pre">' + escLines(msg.toolOutput || '') + '</pre>' +
          '</div>';
      }
      el.innerHTML =
        '<details class="chat-tool-details">' +
          '<summary class="chat-tool-summary">' +
            '<span class="chat-tool-icon" aria-hidden="true">⚙</span>' +
            '<span class="chat-tool-label">' + esc(label) + '</span>' +
            '<span class="chat-msg-time">' + esc(formatTime(msg.ts)) + '</span>' +
          '</summary>' +
          (msg.toolInput
            ? '<pre class="chat-tool-input">' + escLines(msg.toolInput) + '</pre>'
            : '') +
          outputHtml +
        '</details>';
    } else if (msg.role === 'tool_result') {
      // Phase 4: standalone tool_result (no prior tool_use matched in the pane).
      // XSS discipline: esc() on all server-supplied strings.
      el.className = 'chat-msg chat-msg-tool';
      var resLabel = msg.toolName ? msg.toolName + ' result' : 'tool result';
      var resOutputClass = msg.isError ? 'chat-tool-output chat-tool-output-error' : 'chat-tool-output';
      el.innerHTML =
        '<details class="chat-tool-details">' +
          '<summary class="chat-tool-summary">' +
            '<span class="chat-tool-icon" aria-hidden="true">⚙</span>' +
            '<span class="chat-tool-label">' + esc(resLabel) + '</span>' +
            '<span class="chat-msg-time">' + esc(formatTime(msg.ts)) + '</span>' +
          '</summary>' +
          '<div class="' + esc(resOutputClass) + '">' +
            '<pre class="chat-tool-output-pre">' + escLines(msg.toolOutput || '') + '</pre>' +
          '</div>' +
        '</details>';
    } else if (msg.role === 'summary') {
      el.className = 'chat-msg chat-msg-summary';
      const exitOk = msg.exitCode === 0;
      const costStr = msg.costUSD != null ? '$' + msg.costUSD.toFixed(4) : formatCostUnavailable(pane.runtime);
      const durationStr = msg.durationS != null ? msg.durationS.toFixed(1) + 's' : '';
      // errorText is server-supplied (resp.text() from a failed dispatch).
      // MUST go through esc() — never raw innerHTML.  Rendered here so the user
      // sees the dispatch error message rather than a silent failure.
      const errorHtml = msg.errorText
        ? '<span class="chat-summary-error">' + esc(msg.errorText) + '</span>'
        : '';
      el.innerHTML =
        '<span class="chat-summary-icon" aria-hidden="true">' + (exitOk ? '✓' : '✗') + '</span>' +
        '<span class="sr-only">' + esc(exitOk ? 'Completed' : 'Failed') + '</span>' +
        '<span class="chat-summary-cost">' + esc(costStr) + '</span>' +
        (durationStr ? '<span class="chat-summary-duration">' + esc(durationStr) + '</span>' : '') +
        errorHtml;
    } else if (msg.role === 'system') {
      // System messages are client-generated (e.g. /help output, attach notices).
      // esc() via escLines ensures no XSS from text content.
      el.className = 'chat-msg chat-msg-system';
      el.innerHTML =
        '<span class="chat-msg-role" aria-hidden="true">system</span>' +
        '<div class="chat-msg-text">' + escLines(msg.text) + '</div>';
    }

    return el;
  }

  // ---- Transcript restore ----------------------------------------------------

  function loadTranscriptForPane(paneId) {
    const pane = chatPanes.get(paneId);
    if (!pane || !pane.conversationId) return;
    const opId = getChatOperatorId();
    // Phase 3: when this pane is attached to another session (watch or interject),
    // include sessionId so the server's handleChatTranscript can call
    // hub.IsShared(sessionId) and grant shared-access transcript reads.
    // The server never allows the client to self-assert shared access — the hub
    // IsShared check is authoritative.
    var url = '/api/chat/transcript?conversationId=' + encodeURIComponent(pane.conversationId) +
              '&operatorId=' + encodeURIComponent(opId);
    if (pane.attachedSessionId) {
      url += '&sessionId=' + encodeURIComponent(pane.attachedSessionId);
    }

    const transcriptOpts = {};
    if (AUTH_MODE === 'session') {
      // Session mode: send cookie so the server can auth the request.
      transcriptOpts.credentials = 'same-origin';
    }
    fetch(url, transcriptOpts).then((resp) => {
      if (resp.status === 403) return []; // not owner; pane may belong to another op
      if (!resp.ok) return [];
      return resp.json();
    }).then((entries) => {
      if (!Array.isArray(entries) || entries.length === 0) return;
      const msgs = [];
      for (const e of entries) {
        if (e.role === 'user') {
          msgs.push({ role: 'user', text: e.text || '', ts: e.ts, sessionId: e.session_id });
        } else if (e.role === 'assistant') {
          msgs.push({ role: 'assistant', text: e.text || '', ts: e.ts, sessionId: e.session_id, streaming: false });
        } else if (e.role === 'summary') {
          msgs.push({
            role: 'summary',
            text: '',
            ts: e.ts,
            sessionId: e.session_id,
            exitCode: e.exit_code,
            durationS: e.duration_s,
            costUSD: e.total_cost_usd,
          });
          // Set pane cost from the most recent summary.
          if (e.total_cost_usd != null) pane.totalCost = e.total_cost_usd;
        }
      }
      pane.messages = msgs;
      renderPaneMessages(paneId);
      updateTotalCostBadge();
    }).catch(() => {
      // Silent: transcript unavailable (e.g. no WorkDir configured).
    });
  }

  // ---- Dispatch a message from a pane ----------------------------------------

  function sendPaneMessage(paneId) {
    const pane = chatPanes.get(paneId);
    if (!pane) return;
    if (pane.status === 'streaming') return; // already running

    const textarea = document.getElementById('pane-input-' + paneId);
    const task = textarea ? textarea.value.trim() : '';
    if (!task) return;

    // Mint a fresh sessionId per turn.
    const sessionId = newSessionId();
    pane.activeSessionId = sessionId;
    pane.status = 'streaming';
    pane.costSoFar = null; // reset for this turn
    pane.startedAt = new Date();

    // Register session → pane mapping for demux.
    sessionToPaneId.set(sessionId, paneId);

    // Append user message immediately (optimistic).
    pane.messages.push({ role: 'user', text: task, ts: new Date().toISOString(), sessionId });
    if (textarea) textarea.value = '';

    renderPaneHeader(paneId);
    renderPaneMessages(paneId);
    if (pane.autoScroll) scrollPaneToBottom(paneId);

    startElapsedTimer(pane, paneId);

    const dispatchBody = {
      runtime: pane.runtime,
      model: pane.model,
      agent: pane.agent,
      task: task,
      sessionId: sessionId,
      operatorId: getChatOperatorId(),
      conversationId: pane.conversationId,
    };

    // Phase 4: for non-claude runtimes (buffered path), tool events are never
    // emitted by the server.  Show a one-time static affordance so the operator
    // understands tool output is unavailable.  Do NOT fabricate tool events.
    if (!STREAMING_RUNTIMES.has(pane.runtime)) {
      pane.messages.push({
        role: 'tool_use',
        toolName: '',
        toolInput: '',
        toolOutput: 'Tool output is not available for runtime: ' + (pane.runtime || 'unknown') + '. Only claude emits incremental tool events.',
        hasResult: true,
        isError: false,
        ts: new Date().toISOString(),
        sessionId,
        _isRuntimeAffordance: true,
      });
      renderPaneMessages(paneId);
    }

    // Route through apiFetch so session mode sends X-CSRF-Token + credentials.
    apiFetch('POST', '/api/chat/dispatch', dispatchBody).then((resp) => {
      if (resp.ok) return; // 202 Accepted — streaming will arrive on SSE
      // Error path: clean up.
      return resp.text().then((errText) => {
        pane.status = 'error';
        pane.activeSessionId = null;
        sessionToPaneId.delete(sessionId);
        stopElapsedTimer(pane);
        pane.messages.push({
          role: 'summary',
          text: '',
          ts: new Date().toISOString(),
          sessionId,
          exitCode: 1,
          durationS: null,
          costUSD: null,
          errorText: errText,
        });
        renderPaneHeader(paneId);
        renderPaneMessages(paneId);
        // Announce for screen readers (aria-live polite, not per-token).
        announcePaneStatus(paneId, 'dispatch failed: ' + errText);
        updateTotalCostBadge();
      });
    }).catch((err) => {
      pane.status = 'error';
      pane.activeSessionId = null;
      sessionToPaneId.delete(sessionId);
      stopElapsedTimer(pane);
      renderPaneHeader(paneId);
      announcePaneStatus(paneId, 'network error');
      console.warn('[chat dispatch] error:', err);
    });
  }

  // ---- Cancel a pane session -------------------------------------------------

  function cancelPaneSession(paneId) {
    const pane = chatPanes.get(paneId);
    if (!pane || !pane.activeSessionId) return;
    const sessionId = pane.activeSessionId;
    const opId = getChatOperatorId();

    // Route through apiFetch so session mode sends X-CSRF-Token + credentials.
    apiFetch('POST', '/api/chat/cancel', { sessionId, operatorId: opId }).catch(() => {
      // Silent: cancel is best-effort.
    });

    pane.status = 'idle';
    pane.activeSessionId = null;
    sessionToPaneId.delete(sessionId);
    stopElapsedTimer(pane);
    renderPaneHeader(paneId);
    announcePaneStatus(paneId, 'stopped');
  }

  // ---- Share pane toggle -----------------------------------------------------

  function togglePaneShare(paneId) {
    const pane = chatPanes.get(paneId);
    if (!pane || !pane.activeSessionId) {
      // Can only share an active session (one with a session in the hub).
      return;
    }

    const shareBtn = document.getElementById('pane-share-' + paneId);
    const isShared = shareBtn && shareBtn.getAttribute('aria-pressed') === 'true';
    const newShared = !isShared;
    const opId = getChatOperatorId();

    // Route through apiFetch so session mode sends X-CSRF-Token + credentials.
    apiFetch('POST', '/api/chat/share', {
      sessionId: pane.activeSessionId,
      operatorId: opId,
      shared: newShared,
    }).then((resp) => {
      if (!resp.ok) return;
      if (shareBtn) {
        shareBtn.setAttribute('aria-pressed', String(newShared));
        shareBtn.title = newShared ? 'Stop sharing pane' : 'Share pane';
        shareBtn.classList.toggle('pane-share-active', newShared);
      }
      // Phase 4: surface the server-supplied tool-output visibility warning
      // when the session is promoted to shared.  This is a safety mechanic —
      // not optional.  The warning arrives in the JSON response body as
      // {"ok":true,"warning":"..."}.
      return resp.json().then(function(body) {
        if (newShared && body && body.warning) {
          // Show as an inline notification inside the pane so the operator
          // sees it in context without a disruptive alert().
          var paneEl = document.getElementById('pane-' + paneId);
          if (paneEl) {
            var warnEl = document.createElement('div');
            warnEl.className = 'chat-share-warning';
            warnEl.setAttribute('role', 'alert');
            warnEl.textContent = 'Share warning: ' + body.warning;
            paneEl.insertBefore(warnEl, paneEl.firstChild);
            // Auto-dismiss after 10 s.
            setTimeout(function() { if (warnEl.parentNode) warnEl.parentNode.removeChild(warnEl); }, 10000);
          }
        }
      }).catch(function() { /* ignore JSON parse failure */ });
    }).catch(() => { /* silent */ });
  }

  // ---- Elapsed timer ---------------------------------------------------------

  function startElapsedTimer(pane, paneId) {
    stopElapsedTimer(pane);
    pane.elapsedTimer = setInterval(() => {
      updateElapsedDisplay(pane, paneId);
    }, 1000);
    updateElapsedDisplay(pane, paneId);
  }

  function stopElapsedTimer(pane) {
    if (pane.elapsedTimer) {
      clearInterval(pane.elapsedTimer);
      pane.elapsedTimer = null;
    }
  }

  function updateElapsedDisplay(pane, paneId) {
    const el = document.getElementById('pane-elapsed-' + paneId);
    if (!el) return;
    if (!pane.startedAt || pane.status !== 'streaming') {
      el.textContent = '';
      return;
    }
    const elapsedS = Math.floor((Date.now() - pane.startedAt.getTime()) / 1000);
    el.textContent = elapsedS + 's';
  }

  // ---- Cost display ----------------------------------------------------------

  function updateTotalCostBadge() {
    const el = document.getElementById('chat-total-cost');
    if (!el) return;
    let total = 0;
    let anyAvailable = false;
    for (const [, pane] of chatPanes) {
      if (pane.totalCost != null) {
        total += pane.totalCost;
        anyAvailable = true;
      }
    }
    if (!anyAvailable) {
      el.textContent = '';
    } else {
      el.textContent = 'Total: $' + total.toFixed(4);
    }
  }

  function formatCost(cost, runtime) {
    // cost===null means no turn has completed yet (idle pane or non-cost runtime).
    // Show '–' for idle panes rather than '$0.0000' which implies a real $0 cost.
    // Only show a real cost string once a summary event has set it.
    if (cost == null) return formatCostUnavailable(runtime);
    return '$' + cost.toFixed(4);
  }

  function formatCostUnavailable(runtime) {
    // Non-streaming runtimes report "cost unavailable" (server never sends a cost).
    // Streaming runtimes (claude) will eventually report a cost; until then show
    // '–' to distinguish "not yet known" from "$0.0000" (which would be a real cost).
    if (STREAMING_RUNTIMES.has(runtime)) return '–'; // en-dash
    return 'cost unavailable';
  }

  // ---- Accessibility helpers -------------------------------------------------

  function announcePaneStatus(paneId, message) {
    const el = document.getElementById('pane-announce-' + paneId);
    if (!el) return;
    // Update text to trigger aria-live polite announcement.
    el.textContent = '';
    requestAnimationFrame(() => { el.textContent = message; });
  }

  function scrollPaneToBottom(paneId) {
    const msgsEl = document.getElementById('pane-msgs-' + paneId);
    if (msgsEl) msgsEl.scrollTop = msgsEl.scrollHeight;
  }

  // ---- Pane status helpers ---------------------------------------------------

  function paneStatusLabel(status) {
    switch (status) {
      case 'idle':      return 'idle';
      case 'streaming': return 'streaming';
      case 'done':      return 'done';
      case 'error':     return 'error';
      default:          return status;
    }
  }

  function paneStatusIcon(status) {
    // Icon + visually-hidden label; color-not-only per a11y requirement.
    switch (status) {
      case 'idle':      return '<span aria-hidden="true" class="pane-status-dot idle">○</span>';
      case 'streaming': return '<span aria-hidden="true" class="pane-status-dot streaming">◉</span>';
      case 'done':      return '<span aria-hidden="true" class="pane-status-dot done">●</span>';
      case 'error':     return '<span aria-hidden="true" class="pane-status-dot error">✗</span>';
      default:          return '';
    }
  }

  // ---- Operator color (stable per operator ID) --------------------------------
  // Simple deterministic hash → HSL color; same approach as server-side color
  // assignment (attribution only, not auth).

  function operatorColor(opId) {
    let h = 0;
    for (let i = 0; i < opId.length; i++) h = (h * 31 + opId.charCodeAt(i)) >>> 0;
    const hue = h % 360;
    return 'hsl(' + hue + ', 60%, 65%)';
  }

  // =========================================================================
  // ---- REPL TAB (Phase 2 fleet panel) -------------------------------------
  // =========================================================================
  //
  // The REPL tab contains two sections:
  //   (a) Action items — read-only kanban tasks fetched from /kanban/api/board.
  //       Shows TODO and IN PROGRESS columns.  Mirrors the existing Kanban tab
  //       data source without duplicating the parser.
  //   (b) Fleet panel — live in-flight dispatches seeded from GET /api/fleet,
  //       then patched live via fleet.started / fleet.finished WS events.
  //
  // Security notes:
  //   - fleet.* WS events carry NO task text/tokens (metadata-only invariant).
  //   - task_preview (<=120 chars) comes only from the REST seed; it is absent
  //     from WS events by design.
  //   - The panel shows only sessions the caller is allowed to see (server-side
  //     owner/shared scoping on /api/fleet; WS is global to the connection but
  //     the session_id alone reveals no private content).
  //   - All interpolated strings go through esc() before innerHTML insertion.

  function initReplTab() {
    // Fetch kanban action items (once per page load; changes are infrequent).
    loadReplKanban();

    // Seed fleet from REST; subsequent live updates arrive via fleet.* WS events.
    if (!fleetInitialized) {
      apiFetch('GET', '/api/fleet').then(function(resp) {
        if (!resp || !resp.ok) return;
        return resp.json();
      }).then(function(data) {
        if (!data || !Array.isArray(data.sessions)) return;
        fleetInitialized = true;
        fleetSessions = new Map();
        for (const s of data.sessions) {
          if (s.session_id) {
            fleetSessions.set(s.session_id, s);
          }
        }
        renderReplFleet();
      }).catch(function() {
        // /api/fleet unavailable — fleet panel shows empty state.
        fleetInitialized = true;
        renderReplFleet();
      });
    } else {
      // Already seeded; just re-render in case tab was re-opened.
      renderReplFleet();
    }
  }

  // loadReplKanban fetches /kanban/api/board and renders the action-items list.
  // Reuses the same endpoint the Kanban iframe uses — no new parser needed.
  function loadReplKanban() {
    const el = document.getElementById('repl-action-items');
    if (!el) return;
    el.innerHTML = '<p class="empty-state">Loading…</p>';

    apiFetch('GET', '/kanban/api/board').then(function(resp) {
      if (!resp || !resp.ok) {
        el.innerHTML = '<p class="empty-state">Kanban unavailable</p>';
        return null;
      }
      return resp.json();
    }).then(function(board) {
      if (!board) return;
      // Board shape: { "TODO": [{id, title, ...}], "IN PROGRESS": [...], "DONE": [...] }
      const cols = ['TODO', 'IN PROGRESS'];
      const items = [];
      for (const col of cols) {
        const tasks = board[col];
        if (Array.isArray(tasks)) {
          for (const task of tasks) {
            items.push({ col: col, title: task.title || task.id || '(untitled)' });
          }
        }
      }
      if (items.length === 0) {
        el.innerHTML = '<p class="empty-state">No action items</p>';
        return;
      }
      el.innerHTML = '';
      const ul = document.createElement('ul');
      ul.className = 'repl-action-list';
      ul.setAttribute('aria-label', 'Action items from kanban');
      for (const item of items) {
        const li = document.createElement('li');
        li.className = 'repl-action-item';
        li.innerHTML =
          '<span class="repl-action-col repl-action-col-' + esc(item.col.toLowerCase().replace(' ', '-')) + '" aria-label="column">' + esc(item.col) + '</span>' +
          '<span class="repl-action-title">' + esc(item.title) + '</span>';
        ul.appendChild(li);
      }
      el.appendChild(ul);
    }).catch(function() {
      el.innerHTML = '<p class="empty-state">Kanban unavailable</p>';
    });
  }

  // renderReplFleet updates the fleet panel DOM from the current fleetSessions map.
  // Called on initial seed (initReplTab) and on each fleet.* WS event.
  //
  // Phase 3: rows where attachable=true show an "attach" button.  Clicking the
  // row (or its attach button) calls openAttachPane with watchOnly derived from
  // the server-supplied owned field.  esc() is applied to all interpolated strings.
  function renderReplFleet() {
    const el = document.getElementById('repl-fleet-list');
    if (!el) return;

    if (fleetSessions.size === 0) {
      el.innerHTML = '<p class="empty-state">No active dispatches</p>';
      return;
    }

    el.innerHTML = '';
    const ul = document.createElement('ul');
    ul.className = 'repl-fleet-list';
    ul.setAttribute('aria-label', 'Active dispatch sessions');
    for (const [, s] of fleetSessions) {
      const li = document.createElement('li');
      const isAttachable = !!(s.attachable);
      const isOwned = !!(s.owned);
      li.className = 'repl-fleet-item repl-fleet-status-' + esc(s.status || 'running') +
        (isAttachable ? ' repl-fleet-attachable' : '');
      // Format started_at as a locale time string.
      let startedStr = '';
      if (s.started_at) {
        try { startedStr = new Date(s.started_at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' }); } catch (_) {}
      }
      const preview = s.task_preview ? esc(s.task_preview) : '';
      // Phase 3: attach button — only rendered when attachable.
      // The button text signals ownership: "watch" for non-owners, "attach" for owners.
      const attachBtnHtml = isAttachable
        ? '<button class="repl-fleet-attach-btn" type="button" ' +
            'aria-label="' + esc(isOwned ? 'Attach to session' : 'Watch session') + '">' +
            esc(isOwned ? 'attach' : 'watch') +
          '</button>'
        : '';
      li.innerHTML =
        '<span class="repl-fleet-agent" aria-label="agent">' + esc(s.agent || '?') + '</span>' +
        '<span class="repl-fleet-status" aria-label="status">' + esc(s.status || 'running') + '</span>' +
        (startedStr ? '<span class="repl-fleet-time" aria-label="started at">' + esc(startedStr) + '</span>' : '') +
        (preview ? '<span class="repl-fleet-preview" aria-label="task preview">' + preview + '</span>' : '') +
        attachBtnHtml;

      // Wire click: the native attach <button> is the primary keyboard target.
      // S3: do NOT set role="button" on the <li> — the nested <button> already
      // provides the correct ARIA semantics, and a redundant role on the <li>
      // would create a double-fire hazard on Enter (keydown fires on both).
      // The row click handler is kept as a pointer-only convenience (no tabindex).
      if (isAttachable) {
        // Capture loop variables for closure.
        (function(sessionId, watchOnly) {
          var attachBtn = li.querySelector('.repl-fleet-attach-btn');
          if (attachBtn) {
            // Native button handles both click and keyboard (Enter/Space natively).
            attachBtn.addEventListener('click', function(e) {
              e.stopPropagation();
              openAttachPane(sessionId, sessionId, watchOnly);
            });
          }
          // Row click: pointer-only convenience (no tabindex / role=button on li).
          li.addEventListener('click', function(e) {
            // Only fire when the click target is not the button itself
            // (the button's own handler fires in that case via stopPropagation).
            openAttachPane(sessionId, sessionId, watchOnly);
          });
        })(s.session_id, !isOwned);
      }

      ul.appendChild(li);
    }
    el.appendChild(ul);
  }

  // =========================================================================
  // ---- FLOWS TAB (Phase 5) -------------------------------------------------
  // =========================================================================
  //
  // Canvas: minimal vanilla-JS SVG DAG renderer (NOT Mermaid).
  //
  // Mermaid requires 'unsafe-eval' in script-src for its Chevrotain parser.
  // The console CSP is strict: script-src 'self' with no 'unsafe-eval'.
  // Rather than loosen the CSP, we render the DAG with a hand-rolled SVG
  // layout: Kahn topo-sort → layered columns → edge lines.
  //
  // Accessibility:
  //   - The YAML textarea is a first-class accessible alternate to the canvas.
  //   - aria-live regions announce run + node state changes (non-firehose).
  //   - Node status uses icon + label, NOT color alone.
  //   - prefers-reduced-motion: animation/transition is gated below.
  //   - All keyboard-operable controls: Run, Re-run, Save, node-click.
  //
  // XSS:
  //   - esc() is called on all server/workflow/node-output strings before
  //     DOM insertion.
  //   - Node IDs, run IDs, stdout, error messages, YAML content in the
  //     textarea are bound via textContent or textarea.value, never innerHTML
  //     without esc().
  //
  // Idempotency:
  //   POST /flows/api/run is NOT idempotent (always creates a new run).
  //   POST /flows/api/resume is idempotent for the same newRunId.

  // ---- Flows state ----------------------------------------------------------

  let flowsTabInitialized = false;

  // Currently loaded workflow state.
  let flowsState = {
    workflows: [],          // Array<string> — names from /flows/api/workflows
    selectedName: null,     // currently selected workflow name
    yaml: '',               // current YAML text (may be unsaved)
    version: '',            // last-loaded version stamp
    workflow: null,         // parsed workflow object (or null if parse failed)
    dirty: false,           // true if YAML has unsaved edits
    saveError: null,        // last save validation error string
    saveConflict: false,    // true if last save got 409
    // Run tracking: Map<runId, RunSnapshot>
    // RunSnapshot: {runId, workflowName, ts, status, nodes: {id: {status, ...}}, cost}
    runs: new Map(),
    activeRunId: null,      // run currently being watched
    selectedNodeId: null,   // node whose stdout is shown in the node panel
    nodeOutput: '',         // stdout text for selectedNodeId
    nodeOutputTruncated: false,
    viewMode: 'canvas',     // 'canvas' | 'yaml'
    _pendingCreate: null,   // non-null when a new workflow is staged but not yet saved
    // Drawflow canvas editor state
    drawflowEditor: null,   // Drawflow editor instance (initialized once per session)
    canvasEditMode: false,  // false = View (locked), true = Edit (drag/CRUD)
    // Cancel tracking: Set<runId> for runs where a cancel is in-flight (double-click guard).
    _cancellingRuns: new Set(),
    // Cancelled tracking: Set<runId> for runs where operator requested cancellation
    // (used to annotate the eventual "failed" status as "failed (cancelled)").
    _cancelledRuns: new Set(),
  };

  // ---- Flows init -----------------------------------------------------------

  function initFlowsTab() {
    if (flowsTabInitialized) return;
    flowsTabInitialized = true;
    // Load Drawflow assets dynamically (lazy: only when Flows tab is first opened).
    // Drawflow is vanilla JS with no eval/new Function; loads under script-src 'self'.
    _loadDrawflow(function() {
      renderFlowsLayout();
      loadFlowsWorkflowList();
    });
  }

  // _loadDrawflow injects Drawflow CSS + JS if not already present,
  // then calls onReady() once the script has executed.
  function _loadDrawflow(onReady) {
    if (typeof Drawflow !== 'undefined') {
      onReady();
      return;
    }
    // Inject CSS.
    if (!document.getElementById('drawflow-css')) {
      var link = document.createElement('link');
      link.id = 'drawflow-css';
      link.rel = 'stylesheet';
      link.href = '/vendor/drawflow/drawflow.min.css';
      document.head.appendChild(link);
    }
    // Inject JS.
    var script = document.createElement('script');
    script.src = '/vendor/drawflow/drawflow.min.js';
    script.onload = function() { onReady(); };
    script.onerror = function() {
      // Drawflow failed to load — render layout without editor (graceful degrade).
      renderFlowsLayout();
      loadFlowsWorkflowList();
      announceFlows('Canvas editor unavailable (script load failed) — use YAML view');
    };
    document.head.appendChild(script);
  }

  // ---- Flows WS event handler -----------------------------------------------

  function handleFlowsWsEvent(topic, payload) {
    if (!flowsTabInitialized) return;
    const runId = payload.run_id;
    if (!runId) return;

    if (topic === 'workflow.run.started') {
      ensureRunSnapshot(runId, payload.workflow || '');
      const snap = flowsState.runs.get(runId);
      if (snap) {
        snap.status = 'running';
        snap.ts = payload.ts || new Date().toISOString();
      }
      if (flowsState.activeRunId === runId) {
        renderFlowsRunPanel();
        announceFlows('Run ' + runId + ' started');
      }
      renderFlowsRunList();
    } else if (topic === 'workflow.run.finished') {
      ensureRunSnapshot(runId, payload.workflow || '');
      const snap = flowsState.runs.get(runId);
      if (snap) snap.status = payload.status || 'completed';
      // Run finished — clear any in-flight cancel guard.
      flowsState._cancellingRuns.delete(runId);
      if (flowsState.activeRunId === runId) {
        renderFlowsRunPanel();
        announceFlows('Run ' + runId + ' ' + runStatusLabel(payload.status || 'finished', runId));
      }
      renderFlowsRunList();
    } else if (topic === 'workflow.node.started') {
      ensureRunSnapshot(runId, payload.workflow || '');
      const snap = flowsState.runs.get(runId);
      if (snap && payload.node_id) {
        if (!snap.nodes) snap.nodes = {};
        snap.nodes[payload.node_id] = snap.nodes[payload.node_id] || {};
        snap.nodes[payload.node_id].status = 'running';
        snap.nodes[payload.node_id].agent = payload.agent || '';
      }
      if (flowsState.activeRunId === runId) {
        renderFlowsCanvas();
      }
    } else if (topic === 'workflow.node.finished') {
      ensureRunSnapshot(runId, payload.workflow || '');
      const snap = flowsState.runs.get(runId);
      if (snap && payload.node_id) {
        if (!snap.nodes) snap.nodes = {};
        snap.nodes[payload.node_id] = snap.nodes[payload.node_id] || {};
        snap.nodes[payload.node_id].status = payload.status || 'completed';
        snap.nodes[payload.node_id].exit_code = payload.exit_code;
        // Accumulate cost_usd from node-finished events.
        // cost_usd is absent for non-claude runtimes — treat absent as
        // "unavailable" (no $0 display). Only accumulate when present.
        if (typeof payload.cost_usd === 'number' && isFinite(payload.cost_usd)) {
          snap.cost = (snap.cost || 0) + payload.cost_usd;
        }
      }
      if (flowsState.activeRunId === runId) {
        renderFlowsCanvas();
        renderFlowsRunPanel();
        announceFlows('Node ' + payload.node_id + ' ' + (payload.status || 'finished'));
      }
    } else if (topic === 'workflow.node.truncated') {
      ensureRunSnapshot(runId, payload.workflow || '');
      const snap = flowsState.runs.get(runId);
      if (snap && payload.node_id) {
        if (!snap.nodes) snap.nodes = {};
        snap.nodes[payload.node_id] = snap.nodes[payload.node_id] || {};
        snap.nodes[payload.node_id].truncated = true;
      }
    }
  }

  function ensureRunSnapshot(runId, workflowName) {
    if (!flowsState.runs.has(runId)) {
      flowsState.runs.set(runId, {
        runId,
        workflowName,
        ts: new Date().toISOString(),
        status: 'running',
        nodes: {},
        cost: null,
      });
    }
  }

  // ---- Flows API calls -------------------------------------------------------
  // All Flows fetch calls go through the top-scope apiFetch defined in
  // section 1 (the auth-mode detection block).  That single definition handles
  // both bearer mode (Authorization: Bearer) and session mode (credentials:
  // 'same-origin' + X-CSRF-Token on mutations) with 401→/login redirect.
  // There is intentionally no local redefinition here.

  function loadFlowsWorkflowList() {
    apiFetch('GET', '/flows/api/workflows').then((r) => {
      if (!r.ok) return;
      return r.json();
    }).then((data) => {
      if (!data) return;
      flowsState.workflows = data.workflows || [];
      renderFlowsWorkflowList();
    }).catch(() => {});
  }

  function loadFlowsWorkflow(name) {
    apiFetch('GET', '/flows/api/workflow?name=' + encodeURIComponent(name)).then((r) => {
      if (r.status === 404) {
        showFlowsError('Workflow "' + esc(name) + '" not found');
        return null;
      }
      if (!r.ok) {
        showFlowsError('Failed to load workflow');
        return null;
      }
      return r.json();
    }).then((data) => {
      if (!data) return;
      flowsState.selectedName = data.name;
      flowsState.yaml = data.yaml || '';
      flowsState.version = data.version || '';
      flowsState.workflow = data.workflow || null;
      flowsState.dirty = false;
      flowsState.saveError = null;
      flowsState.saveConflict = false;
      flowsState._pendingCreate = null; // clear any pending-create state
      renderFlowsEditor();
      renderFlowsCanvas();
      renderFlowsRunList();
    }).catch(() => {
      showFlowsError('Network error loading workflow');
    });
  }

  function saveFlowsWorkflow() {
    const name = flowsState.selectedName;
    if (!name) return;
    const isPendingCreate = flowsState._pendingCreate === name;
    const body = {
      name,
      yaml: flowsState.yaml,
      version: flowsState.version,
    };
    apiFetch('POST', '/flows/api/workflow', body).then((r) => {
      if (r.status === 409) {
        return r.json().then((d) => {
          flowsState.saveConflict = true;
          if (isPendingCreate) {
            // Create conflict: a workflow with this name already exists on disk.
            flowsState._pendingCreate = null;
            flowsState.saveError = 'A workflow named "' + name + '" already exists. ' +
              'Choose a different name (+ New) or load and edit the existing one.';
          } else {
            // Edit conflict: concurrent save by another operator.
            flowsState.saveError = 'Conflict: another operator saved. Reloading…';
            // Auto-reload to truth after 2 seconds.
            setTimeout(() => loadFlowsWorkflow(name), 2000);
          }
          renderFlowsEditor();
        });
      }
      if (r.status === 400) {
        return r.json().then((d) => {
          flowsState.saveError = d.error || 'Validation error';
          flowsState.saveConflict = false;
          renderFlowsEditor();
        });
      }
      if (!r.ok) {
        flowsState.saveError = 'Save failed (server error)';
        renderFlowsEditor();
        return;
      }
      return r.json().then((d) => {
        const wasCreate = isPendingCreate;
        flowsState._pendingCreate = null;
        flowsState.version = d.version || flowsState.version;
        flowsState.dirty = false;
        flowsState.saveError = null;
        flowsState.saveConflict = false;
        renderFlowsEditor();
        if (wasCreate) {
          // Refresh list from server so the new workflow appears in canonical order.
          loadFlowsWorkflowList();
        }
        // Re-parse to update canvas.
        loadFlowsWorkflow(name);
      });
    }).catch(() => {
      flowsState.saveError = 'Network error saving workflow';
      renderFlowsEditor();
    });
  }

  function startFlowsRun(rerunFailed) {
    const name = flowsState.selectedName;
    if (!name) return;

    if (rerunFailed && flowsState.activeRunId) {
      // Resume: re-run failed/skipped nodes, reuse completed outputs.
      apiFetch('POST', '/flows/api/resume', {
        run_id: flowsState.activeRunId,
        operator_id: getChatOperatorId(),
      }).then((r) => {
        if (!r.ok) {
          showFlowsRunError('Resume failed');
          return null;
        }
        return r.json();
      }).then((d) => {
        if (!d) return;
        const newRunId = d.new_run_id;
        flowsState.activeRunId = newRunId;
        ensureRunSnapshot(newRunId, name);
        renderFlowsRunList();
        pollRunState(newRunId);
        announceFlows('Resuming as run ' + newRunId);
      }).catch(() => {
        showFlowsRunError('Network error on resume');
      });
      return;
    }

    apiFetch('POST', '/flows/api/run?name=' + encodeURIComponent(name), {
      operator_id: getChatOperatorId(),
    }).then((r) => {
      if (!r.ok) {
        return r.json().then((d) => {
          showFlowsRunError(d.error || 'Failed to start run');
        }).catch(() => {
          showFlowsRunError('Failed to start run');
        });
      }
      return r.json().then((d) => {
        const runId = d.run_id;
        flowsState.activeRunId = runId;
        ensureRunSnapshot(runId, name);
        renderFlowsRunList();
        // Poll for initial run state (WS events will update live).
        pollRunState(runId);
        announceFlows('Run ' + runId + ' started');
      });
    }).catch(() => {
      showFlowsRunError('Network error starting run');
    });
  }

  function cancelFlowsRun(runId) {
    if (!runId) return;
    // Double-click guard: if a cancel is already in-flight for this run, do nothing.
    if (flowsState._cancellingRuns.has(runId)) return;
    flowsState._cancellingRuns.add(runId);

    // Immediately reflect "Stopping…" state in the UI.
    updateFlowsToolbarState();
    renderFlowsRunList();

    apiFetch('POST', '/flows/api/cancel?id=' + encodeURIComponent(runId)).then((r) => {
      if (r.status === 202) {
        // Cancel signal sent. Mark this run as operator-cancelled so the eventual
        // "failed" status can be displayed as "failed (cancelled)".
        flowsState._cancelledRuns.add(runId);
        announceFlows('Cancelling run ' + runId);
        // Leave the button in "Stopping…" (disabled) state.
        // The existing poll loop will pick up the eventual status change.
        updateFlowsToolbarState();
        renderFlowsRunList();
        return;
      }
      if (r.status === 404) {
        // Run already finished by the time we sent the cancel — refresh state.
        flowsState._cancellingRuns.delete(runId);
        pollRunState(runId);
        return;
      }
      // 4xx / 5xx: surface error in the status area, clear the guard so retry is possible.
      return r.json().then((d) => {
        flowsState._cancellingRuns.delete(runId);
        showFlowsRunError(d.error || 'Cancel failed (' + r.status + ')');
        updateFlowsToolbarState();
        renderFlowsRunList();
      }).catch(() => {
        flowsState._cancellingRuns.delete(runId);
        showFlowsRunError('Cancel failed (' + r.status + ')');
        updateFlowsToolbarState();
        renderFlowsRunList();
      });
    }).catch(() => {
      flowsState._cancellingRuns.delete(runId);
      showFlowsRunError('Network error cancelling run');
      updateFlowsToolbarState();
      renderFlowsRunList();
    });
  }

  function pollRunState(runId) {
    apiFetch('GET', '/flows/api/run?id=' + encodeURIComponent(runId)).then((r) => {
      if (!r.ok) return;
      return r.json();
    }).then((data) => {
      if (!data) return;
      // Merge into our snapshot map.
      let snap = flowsState.runs.get(runId);
      if (!snap) {
        snap = { runId, workflowName: data.workflow_name || '', ts: data.started_at || '', status: data.status, nodes: {}, cost: null };
        flowsState.runs.set(runId, snap);
      }
      snap.status = data.status || snap.status;
      snap.workflowName = data.workflow_name || snap.workflowName;
      snap.ts = data.started_at || snap.ts;
      // Merge node states.
      if (data.nodes) {
        for (const [id, ns] of Object.entries(data.nodes)) {
          snap.nodes[id] = snap.nodes[id] || {};
          Object.assign(snap.nodes[id], ns);
        }
      }
      // If the run has reached a terminal status, clear the cancelling guard so
      // the Stop button is no longer shown in the "Stopping…" state.
      const terminalStatuses = ['completed', 'failed', 'interrupted'];
      if (terminalStatuses.indexOf(snap.status) !== -1) {
        flowsState._cancellingRuns.delete(runId);
      }
      if (flowsState.activeRunId === runId) {
        renderFlowsCanvas();
        renderFlowsRunPanel();
      }
      renderFlowsRunList();
    }).catch(() => {});
  }

  function loadNodeOutput(runId, nodeId) {
    apiFetch('GET', '/flows/api/run/node?id=' + encodeURIComponent(runId) + '&node=' + encodeURIComponent(nodeId))
      .then((r) => {
        if (r.status === 404) {
          flowsState.nodeOutput = '(no output yet)';
          flowsState.nodeOutputTruncated = false;
          renderFlowsNodePanel();
          return;
        }
        if (!r.ok) {
          flowsState.nodeOutput = '(failed to load output)';
          renderFlowsNodePanel();
          return;
        }
        return r.text().then((text) => {
          flowsState.nodeOutput = text;
          // Check if this node's snapshot says it was truncated.
          const snap = flowsState.runs.get(runId);
          flowsState.nodeOutputTruncated = !!(snap && snap.nodes[nodeId] && snap.nodes[nodeId].truncated);
          renderFlowsNodePanel();
        });
      }).catch(() => {
        flowsState.nodeOutput = '(network error)';
        renderFlowsNodePanel();
      });
  }

  // ---- Flows layout rendering ------------------------------------------------

  function renderFlowsLayout() {
    const panel = document.getElementById('panel-flows');
    if (!panel) return;

    panel.innerHTML =
      '<div class="flows-layout" role="main" aria-label="Flows DAG orchestration">' +
        // Left sidebar: workflow list + run list
        '<aside class="flows-sidebar" aria-label="Workflows and runs">' +
          '<div class="flows-section">' +
            '<div class="flows-section-header">' +
              '<h2 class="subsection-title">Workflows</h2>' +
              '<button id="flows-new-btn" class="flows-new-btn" type="button" ' +
                'aria-label="Create a new workflow" title="New workflow">' +
                '+ New' +
              '</button>' +
            '</div>' +
            '<div id="flows-new-error" class="flows-new-error" role="alert" style="display:none"></div>' +
            '<div id="flows-workflow-list" class="flows-workflow-list"></div>' +
          '</div>' +
          '<div class="flows-section" id="flows-run-list-section" style="display:none">' +
            '<h2 class="subsection-title">Runs</h2>' +
            '<div id="flows-run-list" class="flows-run-list" aria-label="Run history"></div>' +
          '</div>' +
        '</aside>' +
        // Main area: toolbar + canvas/yaml view + node panel
        '<div class="flows-main">' +
          '<div class="flows-toolbar" role="toolbar" aria-label="Workflow controls">' +
            '<span id="flows-workflow-name" class="flows-wf-name"></span>' +
            '<div class="flows-view-toggle" role="group" aria-label="View mode">' +
              '<button id="flows-view-canvas" class="flows-view-btn active" type="button" ' +
                'aria-pressed="true" title="DAG canvas view">Canvas</button>' +
              '<button id="flows-view-yaml" class="flows-view-btn" type="button" ' +
                'aria-pressed="false" title="YAML editor view">YAML</button>' +
            '</div>' +
            // Edit mode toggle — only shown in canvas view
            '<button id="flows-canvas-edit-btn" class="flows-btn" type="button" ' +
              'aria-pressed="false" title="Toggle canvas edit mode (drag nodes, add/delete/connect)">' +
              'Edit</button>' +
            // Add node button — only shown in Edit mode
            '<button id="flows-add-node-btn" class="flows-btn" type="button" ' +
              'style="display:none" aria-label="Add a new node to the canvas">+ Node</button>' +
            '<button id="flows-save-btn" class="flows-btn" type="button" ' +
              'aria-label="Save workflow YAML">Save</button>' +
            '<button id="flows-run-btn" class="flows-btn flows-run-btn" type="button" ' +
              'aria-label="Run this workflow">Run</button>' +
            '<button id="flows-rerun-btn" class="flows-btn" type="button" ' +
              'aria-label="Re-run failed and downstream nodes (reuse completed outputs)" ' +
              'title="Resume: re-run failed/skipped, reuse completed node outputs" ' +
              'style="display:none">Re-run failed</button>' +
            '<button id="flows-stop-btn" class="flows-btn flows-stop-btn" type="button" ' +
              'aria-label="Stop current run" ' +
              'style="display:none">Stop</button>' +
            '<span id="flows-run-status" class="flows-run-status" aria-live="polite"></span>' +
          '</div>' +
          '<div id="flows-save-error" class="flows-save-error" role="alert" style="display:none"></div>' +
          // Canvas + YAML side by side; only one shown at a time
          '<div class="flows-content">' +
            '<div id="flows-canvas-wrap" class="flows-canvas-wrap">' +
              // Canvas mode note: changes based on view/edit mode
              '<div id="flows-canvas-mode-note" class="flows-canvas-readonly-note" role="note" aria-label="Canvas note">' +
                'View mode — click a node to view output. Toggle Edit to add, connect, and edit nodes.' +
              '</div>' +
              // Drawflow container — Drawflow mounts inside this element
              '<div id="flows-canvas" class="flows-canvas drawflow-host" ' +
                'role="region" aria-label="Workflow DAG canvas" ' +
                'aria-describedby="flows-canvas-mode-note">' +
              '</div>' +
            '</div>' +
            '<div id="flows-yaml-wrap" class="flows-yaml-wrap" style="display:none">' +
              '<label for="flows-yaml-editor" class="sr-only">Workflow YAML editor</label>' +
              '<textarea id="flows-yaml-editor" class="flows-yaml-editor" ' +
                'spellcheck="false" autocorrect="off" autocapitalize="off" ' +
                'aria-label="Workflow YAML — accessible alternate to canvas. Edit here, then click Save"></textarea>' +
            '</div>' +
          '</div>' +
          // Node edit panel (shown when editing a node's properties)
          '<div id="flows-node-edit-panel" class="flows-node-edit-panel" style="display:none" ' +
            'role="dialog" aria-label="Edit node properties" aria-modal="false">' +
            '<div class="flows-node-edit-header">' +
              '<span class="flows-node-edit-title">Edit Node</span>' +
              '<button id="flows-node-edit-close" class="flows-node-close" type="button" ' +
                'aria-label="Close node editor">&times;</button>' +
            '</div>' +
            '<div class="flows-node-edit-body">' +
              '<div class="flows-node-edit-row">' +
                '<label class="flows-node-edit-label" for="fne-id">ID</label>' +
                '<input id="fne-id" class="flows-node-edit-input" type="text" ' +
                  'autocomplete="off" spellcheck="false" ' +
                  'placeholder="e.g. step1" aria-describedby="fne-id-hint">' +
                '<p id="fne-id-hint" class="flows-node-edit-hint">Lowercase letters, digits, hyphens. Max 64 chars.</p>' +
              '</div>' +
              '<div class="flows-node-edit-row">' +
                '<label class="flows-node-edit-label" for="fne-agent">Agent</label>' +
                '<input id="fne-agent" class="flows-node-edit-input" type="text" ' +
                  'autocomplete="off" spellcheck="false" placeholder="e.g. claude">' +
              '</div>' +
              '<div class="flows-node-edit-row">' +
                '<label class="flows-node-edit-label" for="fne-prompt">Prompt</label>' +
                '<textarea id="fne-prompt" class="flows-node-edit-textarea" ' +
                  'rows="4" placeholder="Describe the task…" spellcheck="false"></textarea>' +
              '</div>' +
              '<div class="flows-node-edit-row">' +
                '<label class="flows-node-edit-label" for="fne-output-limit">output_limit</label>' +
                '<input id="fne-output-limit" class="flows-node-edit-input" type="number" ' +
                  'min="1" placeholder="8000" aria-describedby="fne-ol-hint">' +
                '<p id="fne-ol-hint" class="flows-node-edit-hint">Required. Bytes budget for upstream output substitution.</p>' +
              '</div>' +
              '<div class="flows-node-edit-row">' +
                '<label class="flows-node-edit-label" for="fne-model">Model (optional)</label>' +
                '<input id="fne-model" class="flows-node-edit-input" type="text" ' +
                  'autocomplete="off" spellcheck="false" placeholder="haiku | sonnet | opus">' +
              '</div>' +
              '<div class="flows-node-edit-row">' +
                '<label class="flows-node-edit-label" for="fne-runtime">Runtime (optional)</label>' +
                '<input id="fne-runtime" class="flows-node-edit-input" type="text" ' +
                  'autocomplete="off" spellcheck="false" placeholder="claude | codex | gemini">' +
              '</div>' +
              '<div class="flows-node-edit-row">' +
                '<label class="flows-node-edit-label" for="fne-timeout">Timeout s (optional)</label>' +
                '<input id="fne-timeout" class="flows-node-edit-input" type="number" ' +
                  'min="0" placeholder="0 (default)">' +
              '</div>' +
              '<p id="fne-error" class="flows-node-edit-error" role="alert" style="display:none"></p>' +
            '</div>' +
            '<div class="flows-node-edit-footer">' +
              '<button id="fne-delete-btn" class="flows-btn flows-btn-danger" type="button" ' +
                'style="display:none" aria-label="Delete this node">Delete node</button>' +
              '<div class="flows-node-edit-actions">' +
                '<button id="fne-cancel-btn" class="flows-btn" type="button">Cancel</button>' +
                '<button id="fne-save-btn" class="flows-btn flows-btn-primary" type="button">Apply</button>' +
              '</div>' +
            '</div>' +
          '</div>' +
          // Node output panel (shown when a node is selected in View mode)
          '<div id="flows-node-panel" class="flows-node-panel" style="display:none">' +
            '<div class="flows-node-panel-header">' +
              '<span id="flows-node-title" class="flows-node-title"></span>' +
              '<button id="flows-node-close" class="flows-node-close" type="button" ' +
                'aria-label="Close node output panel">&times;</button>' +
            '</div>' +
            '<div id="flows-node-truncation-notice" class="flows-node-truncation" ' +
              'role="status" style="display:none">' +
              'Output was tail-truncated to the output_limit declared in the workflow.' +
            '</div>' +
            '<pre id="flows-node-output" class="flows-node-output" ' +
              'aria-label="Node output" tabindex="0"></pre>' +
          '</div>' +
        '</div>' +
        // Accessible status announcer
        '<div id="flows-announcer" class="sr-only" aria-live="polite" aria-atomic="true"></div>' +
        // Opus cost note (shown when opus nodes present)
        '<div id="flows-opus-note" class="flows-opus-note" role="note" style="display:none">' +
          'Note: This workflow contains opus-tier nodes. Costs may be significant ' +
          'and outputs are non-deterministic — runs may produce different results each time.' +
        '</div>' +
      '</div>';

    // Wire view toggle buttons.
    document.getElementById('flows-view-canvas').addEventListener('click', () => {
      switchFlowsView('canvas');
    });
    document.getElementById('flows-view-yaml').addEventListener('click', () => {
      switchFlowsView('yaml');
    });

    // Wire canvas Edit mode toggle.
    document.getElementById('flows-canvas-edit-btn').addEventListener('click', () => {
      toggleCanvasEditMode();
    });

    // Wire Add Node button.
    document.getElementById('flows-add-node-btn').addEventListener('click', () => {
      addNewNodeToCanvas();
    });

    // Wire YAML editor onChange.
    const yamlEditor = document.getElementById('flows-yaml-editor');
    if (yamlEditor) {
      yamlEditor.addEventListener('input', () => {
        flowsState.yaml = yamlEditor.value;
        flowsState.dirty = true;
        flowsState.saveError = null;
        flowsState.saveConflict = false;
        renderFlowsSaveErrorBanner();
        updateFlowsToolbarState();
      });
    }

    // Wire Save button.
    document.getElementById('flows-save-btn').addEventListener('click', () => {
      saveFlowsWorkflow();
    });

    // Wire Run button.
    document.getElementById('flows-run-btn').addEventListener('click', () => {
      startFlowsRun(false);
    });

    // Wire Re-run failed button.
    document.getElementById('flows-rerun-btn').addEventListener('click', () => {
      startFlowsRun(true);
    });

    // Wire Stop button.
    document.getElementById('flows-stop-btn').addEventListener('click', () => {
      if (flowsState.activeRunId) {
        cancelFlowsRun(flowsState.activeRunId);
      }
    });

    // Wire node output panel close button.
    document.getElementById('flows-node-close').addEventListener('click', () => {
      flowsState.selectedNodeId = null;
      document.getElementById('flows-node-panel').style.display = 'none';
    });

    // Wire node edit panel buttons.
    document.getElementById('flows-node-edit-close').addEventListener('click', () => {
      closeNodeEditPanel();
    });
    document.getElementById('fne-cancel-btn').addEventListener('click', () => {
      closeNodeEditPanel();
    });
    document.getElementById('fne-save-btn').addEventListener('click', () => {
      applyNodeEditPanel();
    });
    document.getElementById('fne-delete-btn').addEventListener('click', () => {
      deleteNodeFromEditPanel();
    });

    // Wire New workflow button.
    document.getElementById('flows-new-btn').addEventListener('click', () => {
      createNewFlowsWorkflow();
    });

    renderFlowsWorkflowList();
    // Initialize Drawflow after layout is mounted.
    initDrawflowEditor();
  }

  // idRe mirrors workflow.ValidateID — ^[a-z0-9][a-z0-9-]{0,63}$
  var WORKFLOW_ID_RE = /^[a-z0-9][a-z0-9-]{0,63}$/;

  function showFlowsNewError(msg) {
    var el = document.getElementById('flows-new-error');
    if (!el) return;
    if (msg) {
      el.textContent = msg;
      el.style.display = '';
    } else {
      el.textContent = '';
      el.style.display = 'none';
    }
  }

  // ── Themed modal system ───────────────────────────────────────────────────
  //
  // openModal(opts) shows an in-page modal styled with CSS-var tokens.
  // opts = {
  //   title:       string       — modal heading (escaped)
  //   body:        string       — inner HTML for the body area (caller-controlled)
  //   confirmText: string       — label for the primary action button
  //   cancelText:  string       — label for the cancel button (default 'Cancel')
  //   onConfirm:   fn(modal)    — called with the modal root on confirm click
  //   onCancel:    fn()         — optional; called on cancel/Esc/overlay-click
  //   dangerous:   bool         — if true, confirm button uses --color-error styling
  // }
  // Returns the modal DOM element (already in the document).
  // Call closeModal(el) to programmatically remove it.

  // Monotonic counter for unique modal heading IDs (B-1 fix).
  var _modalUidCounter = 0;

  function openModal(opts) {
    var title       = opts.title       || '';
    var confirmText = opts.confirmText || 'OK';
    var cancelText  = opts.cancelText  || 'Cancel';
    var dangerous   = !!opts.dangerous;

    // B-1 fix: give the heading a stable id and reference it with
    // aria-labelledby instead of aria-label.  This satisfies WCAG 4.1.2 by
    // deriving the accessible name from the visible <h2> element.
    var headingId = 'modal-title-' + (++_modalUidCounter);

    // Build modal DOM.
    var overlay = document.createElement('div');
    overlay.className = 'modal-overlay';
    overlay.setAttribute('role', 'dialog');
    overlay.setAttribute('aria-modal', 'true');
    overlay.setAttribute('aria-labelledby', headingId);

    var box = document.createElement('div');
    box.className = 'modal-box';

    var heading = document.createElement('h2');
    heading.className = 'modal-title';
    heading.id = headingId;
    heading.textContent = title;
    box.appendChild(heading);

    var bodyDiv = document.createElement('div');
    bodyDiv.className = 'modal-body';
    bodyDiv.innerHTML = opts.body || '';
    box.appendChild(bodyDiv);

    var footer = document.createElement('div');
    footer.className = 'modal-footer';

    var cancelBtn = document.createElement('button');
    cancelBtn.type = 'button';
    cancelBtn.className = 'modal-btn modal-btn-cancel';
    cancelBtn.textContent = cancelText;

    var confirmBtn = document.createElement('button');
    confirmBtn.type = 'button';
    confirmBtn.className = 'modal-btn modal-btn-confirm' + (dangerous ? ' modal-btn-danger' : '');
    confirmBtn.textContent = confirmText;

    footer.appendChild(cancelBtn);
    footer.appendChild(confirmBtn);
    box.appendChild(footer);
    overlay.appendChild(box);
    document.body.appendChild(overlay);

    // Save the element that had focus before the modal opened.
    var previousFocus = document.activeElement;

    // Focus trap: collect focusable elements inside the modal.
    function getFocusable() {
      return Array.prototype.slice.call(
        box.querySelectorAll(
          'button, [href], input, select, textarea, [tabindex]:not([tabindex="-1"])'
        )
      ).filter(function(el) { return !el.disabled; });
    }

    // Move focus into modal.
    function focusFirst() {
      var els = getFocusable();
      if (els.length) els[0].focus();
    }
    setTimeout(focusFirst, 0);

    function closeModal(el) {
      if (el && el.parentNode) el.parentNode.removeChild(el);
      // Restore focus to the element that had it before the modal.
      if (previousFocus && previousFocus.focus) {
        try { previousFocus.focus(); } catch (_) {}
      }
    }

    function onCancel() {
      closeModal(overlay);
      if (opts.onCancel) opts.onCancel();
    }

    cancelBtn.addEventListener('click', onCancel);
    confirmBtn.addEventListener('click', function() {
      if (opts.onConfirm) opts.onConfirm(overlay);
    });

    // Close on overlay background click (not on box itself).
    overlay.addEventListener('click', function(e) {
      if (e.target === overlay) onCancel();
    });

    // Keyboard: Esc → cancel; Tab/Shift-Tab → trap focus inside modal.
    overlay.addEventListener('keydown', function(e) {
      if (e.key === 'Escape') { onCancel(); return; }
      if (e.key === 'Tab') {
        var els = getFocusable();
        if (!els.length) { e.preventDefault(); return; }
        var first = els[0], last = els[els.length - 1];
        if (e.shiftKey) {
          if (document.activeElement === first) {
            e.preventDefault();
            last.focus();
          }
        } else {
          if (document.activeElement === last) {
            e.preventDefault();
            first.focus();
          }
        }
      }
    });

    overlay._closeModal = function() { closeModal(overlay); };
    return overlay;
  }

  function createNewFlowsWorkflow() {
    // Build modal body: name input + inline validation error.
    var bodyHTML =
      '<label for="modal-wf-name" class="modal-field-label">' +
        'Workflow name' +
      '</label>' +
      '<input id="modal-wf-name" class="modal-input" type="text" ' +
        'placeholder="e.g. my-flow" autocomplete="off" spellcheck="false" ' +
        'aria-describedby="modal-wf-name-hint modal-wf-name-err">' +
      '<p id="modal-wf-name-hint" class="modal-field-hint">' +
        'Lowercase letters, digits, hyphens. Starts with a letter or digit. Max 64 chars.' +
      '</p>' +
      '<p id="modal-wf-name-err" class="modal-field-error" role="alert" style="display:none"></p>';

    var modal = openModal({
      title:       'New workflow',
      body:        bodyHTML,
      confirmText: 'Create',
      cancelText:  'Cancel',
      onConfirm: function(overlay) {
        var input = overlay.querySelector('#modal-wf-name');
        var errEl = overlay.querySelector('#modal-wf-name-err');
        var name = (input ? input.value : '').trim();

        function showErr(msg) {
          if (errEl) { errEl.textContent = msg; errEl.style.display = ''; }
          if (input) input.focus();
        }

        if (!name) { showErr('Name is required.'); return; }
        if (!WORKFLOW_ID_RE.test(name)) {
          showErr(
            'Invalid name. Use lowercase letters, digits, and hyphens only; ' +
            'must start with a letter or digit; max 64 characters.'
          );
          return;
        }

        // Validation passed — close modal and load starter template.
        overlay._closeModal();
        showFlowsNewError(null);
        _applyNewWorkflow(name);
      },
    });

    // Focus the name input immediately (setTimeout in openModal handles initial
    // focus on first focusable; input is first, so this is already covered).
    // Also allow Enter key in the input to trigger Create.
    var input = modal.querySelector('#modal-wf-name');
    if (input) {
      input.addEventListener('keydown', function(e) {
        if (e.key === 'Enter') {
          e.preventDefault();
          modal.querySelector('.modal-btn-confirm').click();
        }
      });
    }
  }

  function _applyNewWorkflow(name) {
    // Minimal starter YAML that passes workflow.Validate:
    //   version: 1               — required schema version
    //   name: <name>             — must match ValidateID (pre-validated)
    //   nodes[].id               — must match ValidateID
    //   nodes[].agent            — required non-empty string
    //   nodes[].prompt           — required non-empty string
    //   nodes[].output_limit     — required > 0 (safety: prevents unbounded prompt inflation)
    var starterYAML = [
      'version: 1',
      'name: ' + name,
      'nodes:',
      '  - id: step1',
      '    agent: claude',
      '    prompt: "Describe the first step."',
      '    output_limit: 8000',
    ].join('\n') + '\n';

    // Load the new workflow into the editor state.
    // version: '' signals force-create (POST /flows/api/workflow with empty version
    // and a non-existent name creates the file; if the name already exists the
    // server returns 409 — handled by saveFlowsWorkflow's existing 409 path).
    flowsState.selectedName = name;
    flowsState.yaml = starterYAML;
    flowsState.version = '';
    flowsState.workflow = null;
    flowsState.dirty = true;
    flowsState.saveError = null;
    flowsState.saveConflict = false;
    flowsState._pendingCreate = name;

    // Add to workflows list for display (refreshed from server after first save).
    if (flowsState.workflows.indexOf(name) === -1) {
      flowsState.workflows = flowsState.workflows.concat([name]);
    }

    // Switch to Canvas view and render the starter node graph.
    // Set a minimal workflow object so renderFlowsCanvas can draw it.
    flowsState.workflow = {
      version: 1,
      name: name,
      nodes: [{ id: 'step1', agent: 'claude', prompt: 'Describe the first step.', output_limit: 8000 }],
    };

    switchFlowsView('canvas');

    var yamlEditor = document.getElementById('flows-yaml-editor');
    if (yamlEditor) yamlEditor.value = starterYAML;

    renderFlowsWorkflowList();
    renderFlowsEditor();
    updateFlowsToolbarState();
    announceFlows('New workflow "' + name + '" created — canvas showing starter node');
  }

  function deleteFlowsWorkflow(name) {
    openModal({
      title:       'Delete workflow',
      body:        '<p>Delete <strong>' + esc(name) + '</strong>?</p>' +
                   '<p class="modal-field-hint" style="margin-top:8px">' +
                     'This removes the workflow definition. ' +
                     'Existing run history is not deleted.' +
                   '</p>' +
                   '<p id="modal-del-err" class="modal-field-error" role="alert" style="display:none"></p>',
      confirmText: 'Delete',
      cancelText:  'Cancel',
      dangerous:   true,
      onConfirm: function(overlay) {
        var errEl = overlay.querySelector('#modal-del-err');
        function showErr(msg) {
          if (errEl) { errEl.textContent = msg; errEl.style.display = ''; }
        }
        apiFetch('DELETE', '/flows/api/workflow?name=' + encodeURIComponent(name)).then(function(r) {
          if (r.status === 404) {
            // 404: workflow was never saved (pending-create) or already deleted
            // externally. Clear local state if it was the selected workflow so
            // the canvas and editor don't show a stale/phantom workflow.
            overlay._closeModal();
            if (flowsState.selectedName === name) {
              flowsState.selectedName = null;
              flowsState.yaml = '';
              flowsState.version = '';
              flowsState.workflow = null;
              flowsState.dirty = false;
              flowsState.saveError = null;
              flowsState.saveConflict = false;
              flowsState._pendingCreate = null;
              closeNodeEditPanel();
              renderFlowsEditor();
              renderFlowsCanvas();
            }
            loadFlowsWorkflowList();
            announceFlows('Workflow "' + name + '" removed');
            return;
          }
          if (!r.ok) {
            return r.json().then(function(d) {
              showErr(d.error || 'Delete failed');
            }).catch(function() {
              showErr('Delete failed (server error)');
            });
          }
          overlay._closeModal();
          // Clear selection if we just deleted the selected workflow.
          if (flowsState.selectedName === name) {
            flowsState.selectedName = null;
            flowsState.yaml = '';
            flowsState.version = '';
            flowsState.workflow = null;
            flowsState.dirty = false;
            flowsState.saveError = null;
            flowsState.saveConflict = false;
            flowsState._pendingCreate = null;
            closeNodeEditPanel();
            renderFlowsEditor();
            renderFlowsCanvas();
          }
          loadFlowsWorkflowList();
          announceFlows('Workflow "' + name + '" deleted');
        }).catch(function() {
          showErr('Network error deleting workflow');
        });
      },
    });
  }

  function switchFlowsView(mode) {
    flowsState.viewMode = mode;
    const isCanvas = mode === 'canvas';

    document.getElementById('flows-canvas-wrap').style.display = isCanvas ? '' : 'none';
    document.getElementById('flows-yaml-wrap').style.display = isCanvas ? 'none' : '';

    const btnCanvas = document.getElementById('flows-view-canvas');
    const btnYaml = document.getElementById('flows-view-yaml');
    if (btnCanvas) {
      btnCanvas.classList.toggle('active', isCanvas);
      btnCanvas.setAttribute('aria-pressed', String(isCanvas));
    }
    if (btnYaml) {
      btnYaml.classList.toggle('active', !isCanvas);
      btnYaml.setAttribute('aria-pressed', String(!isCanvas));
    }

    // Show/hide canvas-only controls (Edit mode toggle, Add Node button).
    const editBtn = document.getElementById('flows-canvas-edit-btn');
    if (editBtn) editBtn.style.display = isCanvas ? '' : 'none';
    const addBtn = document.getElementById('flows-add-node-btn');
    if (addBtn) addBtn.style.display = (isCanvas && flowsState.canvasEditMode) ? '' : 'none';

    if (!isCanvas) {
      // Sync YAML editor with current state.
      const yamlEditor = document.getElementById('flows-yaml-editor');
      if (yamlEditor) yamlEditor.value = flowsState.yaml;
      // Force View mode on canvas when switching away (no accidental edits in background).
      if (flowsState.canvasEditMode) toggleCanvasEditMode();
    } else {
      renderFlowsCanvas();
    }
  }

  function renderFlowsWorkflowList() {
    const listEl = document.getElementById('flows-workflow-list');
    if (!listEl) return;
    listEl.innerHTML = '';
    if (flowsState.workflows.length === 0) {
      listEl.innerHTML = '<p class="empty-state">No workflows found</p>';
      return;
    }
    for (const name of flowsState.workflows) {
      // Wrap each workflow entry in a row: [name button] [delete button]
      const row = document.createElement('div');
      row.className = 'flows-wf-row';

      const btn = document.createElement('button');
      btn.className = 'flows-wf-item' + (name === flowsState.selectedName ? ' active' : '');
      btn.type = 'button';
      btn.setAttribute('aria-current', name === flowsState.selectedName ? 'true' : 'false');
      btn.textContent = name; // textContent is XSS-safe
      btn.addEventListener('click', () => {
        flowsState.selectedName = name;
        loadFlowsWorkflow(name);
        // Update active state immediately.
        listEl.querySelectorAll('.flows-wf-item').forEach((el) => {
          const isActive = el.textContent === name;
          el.classList.toggle('active', isActive);
          el.setAttribute('aria-current', String(isActive));
        });
      });

      const delBtn = document.createElement('button');
      delBtn.type = 'button';
      delBtn.className = 'flows-wf-delete';
      delBtn.setAttribute('aria-label', 'Delete workflow ' + name);
      delBtn.setAttribute('title', 'Delete');
      delBtn.textContent = '×';
      delBtn.addEventListener('click', (e) => {
        e.stopPropagation(); // don't also select the workflow
        deleteFlowsWorkflow(name);
      });

      row.appendChild(btn);
      row.appendChild(delBtn);
      listEl.appendChild(row);
    }
  }

  function renderFlowsEditor() {
    const nameEl = document.getElementById('flows-workflow-name');
    if (nameEl) nameEl.textContent = flowsState.selectedName || '(no workflow selected)';

    // Sync YAML editor if in yaml view.
    if (flowsState.viewMode === 'yaml') {
      const yamlEditor = document.getElementById('flows-yaml-editor');
      if (yamlEditor) yamlEditor.value = flowsState.yaml;
    }

    renderFlowsSaveErrorBanner();
    updateFlowsToolbarState();
    renderFlowsOpusNote();
  }

  function renderFlowsSaveErrorBanner() {
    const errEl = document.getElementById('flows-save-error');
    if (!errEl) return;
    if (flowsState.saveError) {
      errEl.style.display = '';
      errEl.textContent = flowsState.saveError; // textContent is XSS-safe
    } else {
      errEl.style.display = 'none';
      errEl.textContent = '';
    }
  }

  function updateFlowsToolbarState() {
    const saveBtn = document.getElementById('flows-save-btn');
    const runBtn = document.getElementById('flows-run-btn');
    const rerunBtn = document.getElementById('flows-rerun-btn');
    const stopBtn = document.getElementById('flows-stop-btn');
    const editBtn = document.getElementById('flows-canvas-edit-btn');

    const hasWorkflow = !!flowsState.selectedName;
    const activeSnap = flowsState.activeRunId ? flowsState.runs.get(flowsState.activeRunId) : null;
    const isRunning = activeSnap && (activeSnap.status === 'running' || activeSnap.status === 'pending');

    if (saveBtn) saveBtn.disabled = !hasWorkflow;
    if (runBtn) runBtn.disabled = !hasWorkflow;
    if (editBtn) editBtn.disabled = !hasWorkflow;

    // Show Re-run button only if there's an active run with failures.
    if (rerunBtn) {
      const hasFailed = activeSnap && activeSnap.status === 'failed';
      rerunBtn.style.display = (hasWorkflow && hasFailed) ? '' : 'none';
    }

    // Show Stop button only if the active run is in-progress (running or pending).
    // Disable it (and label it "Stopping…") if a cancel request is already in-flight.
    if (stopBtn) {
      if (hasWorkflow && isRunning) {
        stopBtn.style.display = '';
        const isCancelling = flowsState.activeRunId &&
          flowsState._cancellingRuns.has(flowsState.activeRunId);
        stopBtn.disabled = !!isCancelling;
        stopBtn.textContent = isCancelling ? 'Stopping…' : 'Stop';
        stopBtn.setAttribute('aria-label',
          isCancelling
            ? 'Cancellation requested for run ' + flowsState.activeRunId
            : 'Stop run ' + flowsState.activeRunId
        );
      } else {
        stopBtn.style.display = 'none';
      }
    }

    // Run status indicator.
    const statusEl = document.getElementById('flows-run-status');
    if (statusEl) {
      if (activeSnap) {
        statusEl.textContent = runStatusLabel(activeSnap.status, flowsState.activeRunId);
        statusEl.className = 'flows-run-status flows-run-status-' + activeSnap.status;
      } else {
        statusEl.textContent = '';
        statusEl.className = 'flows-run-status';
      }
    }
  }

  function renderFlowsOpusNote() {
    const noteEl = document.getElementById('flows-opus-note');
    if (!noteEl) return;
    const wf = flowsState.workflow;
    if (!wf || !wf.nodes) {
      noteEl.style.display = 'none';
      return;
    }
    const hasOpus = wf.nodes.some((n) => {
      const m = (n.model || '').toLowerCase();
      return m === 'opus';
    });
    noteEl.style.display = hasOpus ? '' : 'none';
  }

  // ---- Flows run list rendering -----------------------------------------------

  function renderFlowsRunList() {
    const section = document.getElementById('flows-run-list-section');
    const listEl = document.getElementById('flows-run-list');
    if (!section || !listEl) return;

    if (flowsState.runs.size === 0) {
      section.style.display = 'none';
      return;
    }
    section.style.display = '';
    listEl.innerHTML = '';

    // Show most recent first (Map iteration is insertion order; reverse it).
    const snaps = [...flowsState.runs.values()].reverse();
    for (const snap of snaps) {
      // Wrap each run in a row so we can place a stop button alongside it.
      const row = document.createElement('div');
      row.className = 'flows-run-row';

      const item = document.createElement('button');
      item.type = 'button';
      item.className = 'flows-run-item' + (snap.runId === flowsState.activeRunId ? ' active' : '');
      item.setAttribute('aria-current', snap.runId === flowsState.activeRunId ? 'true' : 'false');

      const icon = runStatusIcon(snap.status);
      const label = runStatusLabel(snap.status, snap.runId);
      const tsStr = snap.ts ? formatTime(snap.ts) : '';
      const costStr = snap.cost != null ? ' $' + snap.cost.toFixed(4) : '';

      // textContent for each child is XSS-safe; we use createElement.
      const iconSpan = document.createElement('span');
      iconSpan.className = 'flows-run-icon';
      iconSpan.setAttribute('aria-hidden', 'true');
      iconSpan.textContent = icon;

      const labelSpan = document.createElement('span');
      labelSpan.className = 'sr-only';
      labelSpan.textContent = label;

      const infoSpan = document.createElement('span');
      infoSpan.className = 'flows-run-info';
      infoSpan.textContent = snap.runId + (tsStr ? ' · ' + tsStr : '') + costStr;

      item.appendChild(iconSpan);
      item.appendChild(labelSpan);
      item.appendChild(infoSpan);

      item.addEventListener('click', () => {
        flowsState.activeRunId = snap.runId;
        pollRunState(snap.runId);
        renderFlowsCanvas();
        renderFlowsRunPanel();
        renderFlowsRunList();
        updateFlowsToolbarState();
      });
      row.appendChild(item);

      // Per-run Stop button: shown only for in-progress (running/pending) runs.
      const snapIsRunning = snap.status === 'running' || snap.status === 'pending';
      if (snapIsRunning) {
        const isCancelling = flowsState._cancellingRuns.has(snap.runId);
        const stopBtn = document.createElement('button');
        stopBtn.type = 'button';
        stopBtn.className = 'flows-run-stop-btn';
        stopBtn.disabled = isCancelling;
        stopBtn.textContent = isCancelling ? '…' : '■';
        stopBtn.setAttribute(
          'aria-label',
          isCancelling ? 'Cancellation requested for run ' + snap.runId : 'Stop run ' + snap.runId
        );
        stopBtn.setAttribute('title', isCancelling ? 'Stopping…' : 'Stop');
        stopBtn.addEventListener('click', (e) => {
          e.stopPropagation(); // don't also select the run
          cancelFlowsRun(snap.runId);
        });
        row.appendChild(stopBtn);
      }

      listEl.appendChild(row);
    }
  }

  function renderFlowsRunPanel() {
    updateFlowsToolbarState();
  }

  function showFlowsError(msg) {
    const errEl = document.getElementById('flows-save-error');
    if (errEl) {
      errEl.style.display = '';
      errEl.textContent = msg; // textContent
    }
  }

  function showFlowsRunError(msg) {
    const statusEl = document.getElementById('flows-run-status');
    if (statusEl) {
      statusEl.textContent = msg; // textContent
      statusEl.className = 'flows-run-status flows-run-status-failed';
    }
  }

  // ---- Flows node output panel -----------------------------------------------

  function renderFlowsNodePanel() {
    const panel = document.getElementById('flows-node-panel');
    const titleEl = document.getElementById('flows-node-title');
    const outputEl = document.getElementById('flows-node-output');
    const truncEl = document.getElementById('flows-node-truncation-notice');
    if (!panel) return;

    panel.style.display = flowsState.selectedNodeId ? '' : 'none';
    if (!flowsState.selectedNodeId) return;

    if (titleEl) titleEl.textContent = 'Node: ' + flowsState.selectedNodeId;
    if (outputEl) outputEl.textContent = flowsState.nodeOutput; // textContent is XSS-safe
    if (truncEl) {
      truncEl.style.display = flowsState.nodeOutputTruncated ? '' : 'none';
    }
  }

  // =========================================================================
  // ---- DAG canvas (Drawflow interactive editor) ----------------------------
  // =========================================================================
  //
  // Drawflow replaces the read-only SVG renderer with a full node editor.
  //
  // Architecture:
  //   - One Drawflow instance is created per Flows panel init (initDrawflowEditor).
  //   - View mode: editor_mode='fixed' (locked — no drag, no edit).
  //   - Edit mode: editor_mode='edit' (drag nodes, draw connections, CRUD).
  //   - YAML → canvas (load): parse nodes + needs[] → Drawflow import
  //     with layered auto-layout (Kahn topo-sort, same as old SVG renderer).
  //     Positions are ephemeral — NOT written back to YAML schema.
  //   - Canvas → YAML (edit): on any Drawflow change event, re-export the
  //     graph and regenerate engine-valid YAML (preserving name/version).
  //   - Double-click on canvas background → add node dialog.
  //   - Click on node (View mode) → show node run output panel.
  //   - Click on node (Edit mode) → open node edit panel.
  //   - Delete key (Edit mode) with selected node → remove node.
  //   - Connections are validated: self-loops blocked, client-side cycle warning.
  //
  // CSP: Drawflow is vanilla JS with no eval/new Function. Confirmed at vendor
  // time via grep. Loads under script-src 'self' with no relaxation.
  //
  // Accessibility:
  //   - YAML view is the accessible alternate (full keyboard editing).
  //   - Canvas has aria-label + aria-describedby pointing to the mode note.
  //   - Node edit panel is keyboard-operable (Tab/Shift-Tab, Enter).
  //   - aria-live status announcer for structural changes.

  // Auto-layout constants (same proportions as old SVG renderer).
  const DF_NODE_W = 160;
  const DF_NODE_H = 70;
  const DF_COL_GAP = 100;
  const DF_ROW_GAP = 30;
  const DF_PAD = 40;

  // --- Drawflow initialization -----------------------------------------------

  function initDrawflowEditor() {
    if (flowsState.drawflowEditor) return; // already initialized; guard against re-init
    const canvasEl = document.getElementById('flows-canvas');
    if (!canvasEl) return;
    if (typeof Drawflow === 'undefined') return; // Drawflow not loaded yet

    // Create the Drawflow editor instance.
    const editor = new Drawflow(canvasEl);
    editor.reroute = false;
    editor.editor_mode = 'fixed'; // start in View mode
    editor.start();

    flowsState.drawflowEditor = editor;
    flowsState.canvasEditMode = false;

    // ---- Drawflow event listeners ----

    // Node clicked: in View mode show output panel; in Edit mode open edit panel.
    editor.on('nodeSelected', function(id) {
      if (flowsState.canvasEditMode) {
        openNodeEditPanel(id);
      } else {
        const data = getDrawflowNodeData(id);
        if (!data) return;
        const nodeId = data.yakosId;
        flowsState.selectedNodeId = nodeId;
        const runId = flowsState.activeRunId;
        if (runId) loadNodeOutput(runId, nodeId);
        renderFlowsNodePanel();
      }
    });

    // Connection created: validate + regenerate YAML.
    editor.on('connectionCreated', function(info) {
      // Block self-loops.
      if (info.output_id === info.input_id) {
        editor.removeSingleConnection(
          info.output_id, info.input_id,
          info.output_class, info.input_class
        );
        announceFlows('Self-loop blocked: a node cannot connect to itself');
        return;
      }
      // Client-side cycle detection: warn (server validates definitively on save).
      if (dfGraphHasCycle()) {
        announceFlows('Warning: this connection may create a cycle — Save will validate');
      }
      syncCanvasToYaml();
    });

    // Connection removed: regenerate YAML.
    editor.on('connectionRemoved', function() {
      syncCanvasToYaml();
    });

    // Node moved (drag): no YAML schema change (positions are ephemeral).
    // We don't regenerate YAML here to avoid noisy dirty flags on pan/drag.

    // Double-click on canvas background → add node.
    canvasEl.addEventListener('dblclick', function(e) {
      if (!flowsState.canvasEditMode) return;
      // Only respond to dblclick on the canvas background (not on nodes).
      if (e.target.closest('.drawflow-node')) return;
      if (!flowsState.selectedName) return;
      addNewNodeToCanvas();
    });

    // Delete key → delete selected node (Edit mode only).
    document.addEventListener('keydown', function(e) {
      if (!flowsState.canvasEditMode) return;
      if (e.key !== 'Delete' && e.key !== 'Backspace') return;
      // Only fire when focus is on the canvas area (not in an input/textarea).
      const tag = document.activeElement ? document.activeElement.tagName : '';
      if (tag === 'INPUT' || tag === 'TEXTAREA') return;
      const canvasWrap = document.getElementById('flows-canvas-wrap');
      if (!canvasWrap || !canvasWrap.contains(document.activeElement)) return;
      if (editor.node_selected) {
        const selId = editor.node_selected.id
          ? editor.node_selected.id.replace('node-', '')
          : null;
        if (selId) {
          editor.removeNodeId('node-' + selId);
          syncCanvasToYaml();
          announceFlows('Node deleted');
        }
      }
    });
  }

  // --- Canvas ↔ YAML sync -------------------------------------------------------

  // renderFlowsCanvas: load flowsState.workflow into Drawflow with auto-layout.
  // Called whenever the workflow changes (load, after save, etc.).
  function renderFlowsCanvas() {
    if (!flowsState.drawflowEditor) return;
    const editor = flowsState.drawflowEditor;

    // Clear the editor.
    editor.clearModuleSelected();
    editor.import({ drawflow: { Home: { data: {} } } });

    const wf = flowsState.workflow;
    if (!wf || !wf.nodes || wf.nodes.length === 0) {
      return;
    }

    // 1. Compute layered auto-layout via Kahn topo-sort.
    const positions = computeLayeredPositions(wf.nodes);

    // 2. Build a map from yakosId → Drawflow internal id.
    const dfIdByYakos = {};

    // 3. Add nodes.
    for (const n of wf.nodes) {
      const pos = positions[n.id] || { x: DF_PAD, y: DF_PAD };
      const dfId = addDrawflowNode(editor, n, pos.x, pos.y);
      dfIdByYakos[n.id] = dfId;
    }

    // 4. Add connections from needs[].
    for (const n of wf.nodes) {
      for (const dep of (n.needs || [])) {
        if (dfIdByYakos[dep] && dfIdByYakos[n.id]) {
          try {
            editor.addConnection(
              dfIdByYakos[dep], dfIdByYakos[n.id],
              'output_1', 'input_1'
            );
          } catch (_) { /* defensive: skip bad connections */ }
        }
      }
    }

    // Update node status overlays (run state coloring).
    updateDrawflowNodeStatuses();
  }

  // syncCanvasToYaml: export Drawflow graph → engine-valid YAML → update state.
  function syncCanvasToYaml() {
    if (!flowsState.drawflowEditor) return;
    const exported = flowsState.drawflowEditor.export();
    const data = exported && exported.drawflow && exported.drawflow.Home
      ? exported.drawflow.Home.data
      : {};

    // Build id → yakosId mapping and collect nodes.
    const dfNodes = Object.values(data);
    const nodes = [];
    const idMap = {}; // dfId → yakosId

    for (const dfNode of dfNodes) {
      const d = dfNode.data || {};
      const yakosId = d.yakosId || '';
      if (yakosId) idMap[String(dfNode.id)] = yakosId;
    }

    for (const dfNode of dfNodes) {
      const d = dfNode.data || {};
      const yakosId = d.yakosId || '';
      if (!yakosId) continue;

      // Collect needs[] from input connections.
      const needs = [];
      const inputs = dfNode.inputs || {};
      for (const inputKey of Object.keys(inputs)) {
        const conns = (inputs[inputKey].connections || []);
        for (const conn of conns) {
          const depYakosId = idMap[String(conn.node)];
          if (depYakosId && needs.indexOf(depYakosId) === -1) {
            needs.push(depYakosId);
          }
        }
      }

      const node = {
        id: yakosId,
        agent: d.agent || 'claude',
        prompt: d.prompt || 'Describe the task.',
        output_limit: parseInt(d.output_limit, 10) || 8000,
      };
      if (needs.length > 0) node.needs = needs;
      if (d.model) node.model = d.model;
      if (d.runtime) node.runtime = d.runtime;
      if (d.timeout && parseInt(d.timeout, 10) > 0) node.timeout = parseInt(d.timeout, 10);

      nodes.push(node);
    }

    // Reconstruct YAML preserving version and name.
    const name = flowsState.selectedName || 'workflow';
    const version = (flowsState.workflow && flowsState.workflow.version) || 1;
    const yamlLines = ['version: ' + version, 'name: ' + name, 'nodes:'];
    for (const n of nodes) {
      yamlLines.push('  - id: ' + n.id);
      yamlLines.push('    agent: ' + yamlQuoteString(n.agent));
      yamlLines.push('    prompt: ' + yamlQuoteString(n.prompt));
      yamlLines.push('    output_limit: ' + n.output_limit);
      if (n.needs && n.needs.length > 0) {
        yamlLines.push('    needs:');
        for (const dep of n.needs) {
          yamlLines.push('      - ' + dep);
        }
      }
      if (n.model) yamlLines.push('    model: ' + yamlQuoteString(n.model));
      if (n.runtime) yamlLines.push('    runtime: ' + yamlQuoteString(n.runtime));
      if (n.timeout) yamlLines.push('    timeout: ' + n.timeout);
    }
    const newYaml = yamlLines.join('\n') + '\n';

    flowsState.yaml = newYaml;
    flowsState.dirty = true;
    flowsState.saveError = null;
    flowsState.saveConflict = false;

    // Keep YAML editor in sync (it may be hidden but stays valid).
    const yamlEditor = document.getElementById('flows-yaml-editor');
    if (yamlEditor) yamlEditor.value = newYaml;

    renderFlowsSaveErrorBanner();
    updateFlowsToolbarState();
  }

  // --- Drawflow node helpers ---------------------------------------------------

  function addDrawflowNode(editor, nodeData, x, y) {
    const html = buildNodeHTML(nodeData);
    // Capture editor.nodeId before the call; Drawflow assigns it as the new node's id
    // and then increments it. This is more reliable than scanning by name+position.
    const assignedId = String(editor.nodeId);
    // Drawflow addNode(name, inputs, outputs, posX, posY, class, data, html, typenode)
    editor.addNode(
      nodeData.id,    // name (used internally by Drawflow)
      1,              // inputs count
      1,              // outputs count
      x, y,
      'df-yakos-node',
      {
        yakosId:      nodeData.id,
        agent:        nodeData.agent || '',
        prompt:       nodeData.prompt || '',
        output_limit: String(nodeData.output_limit || 8000),
        model:        nodeData.model || '',
        runtime:      nodeData.runtime || '',
        timeout:      String(nodeData.timeout || 0),
      },
      html,
      false           // not a Vue component
    );
    return assignedId;
  }

  function buildNodeHTML(nodeData) {
    // XSS note: esc() is used for all user-controlled strings rendered into HTML.
    return '<div class="df-node-body">' +
      '<div class="df-node-id">' + esc(nodeData.id || '') + '</div>' +
      '<div class="df-node-agent">' + esc(nodeData.agent || '') + '</div>' +
    '</div>';
  }

  function getDrawflowNodeData(dfId) {
    if (!flowsState.drawflowEditor) return null;
    try {
      return flowsState.drawflowEditor.getNodeFromId(dfId);
    } catch (_) {
      return null;
    }
  }

  function updateDrawflowNodeStatuses() {
    if (!flowsState.drawflowEditor) return;
    const activeSnap = flowsState.activeRunId
      ? flowsState.runs.get(flowsState.activeRunId)
      : null;
    const exported = flowsState.drawflowEditor.export();
    const home = exported && exported.drawflow && exported.drawflow.Home
      ? exported.drawflow.Home.data
      : {};

    for (const dfId of Object.keys(home)) {
      const d = (home[dfId].data || {});
      const yakosId = d.yakosId;
      if (!yakosId) continue;
      const nodeState = activeSnap && activeSnap.nodes ? activeSnap.nodes[yakosId] : null;
      const status = nodeState ? (nodeState.status || 'pending') : 'pending';
      const el = document.querySelector('#node-' + dfId);
      if (!el) continue;
      // Remove old status classes and add new.
      el.classList.remove(
        'df-status-pending', 'df-status-running',
        'df-status-completed', 'df-status-failed', 'df-status-skipped'
      );
      el.classList.add('df-status-' + status);
    }
  }

  // --- Auto-layout (layered) --------------------------------------------------

  function computeLayeredPositions(nodes) {
    // Kahn topo-sort → assign layers.
    const inDegree = {};
    const successors = {};
    for (const n of nodes) {
      inDegree[n.id] = inDegree[n.id] || 0;
      successors[n.id] = successors[n.id] || [];
    }
    for (const n of nodes) {
      for (const dep of (n.needs || [])) {
        inDegree[n.id] = (inDegree[n.id] || 0) + 1;
        if (!successors[dep]) successors[dep] = [];
        successors[dep].push(n.id);
      }
    }

    const layer = {};
    const queue = [];
    for (const n of nodes) {
      if ((inDegree[n.id] || 0) === 0) {
        layer[n.id] = 0;
        queue.push(n.id);
      }
    }
    const remaining = Object.assign({}, inDegree);
    let qi = 0;
    while (qi < queue.length) {
      const cur = queue[qi++];
      for (const succ of (successors[cur] || [])) {
        const newLayer = (layer[cur] || 0) + 1;
        if (layer[succ] === undefined || layer[succ] < newLayer) layer[succ] = newLayer;
        remaining[succ] = (remaining[succ] || 1) - 1;
        if (remaining[succ] <= 0) queue.push(succ);
      }
    }

    // Bucket by layer.
    const maxLayer = Math.max(0, ...Object.values(layer));
    const columns = [];
    for (let i = 0; i <= maxLayer; i++) columns.push([]);
    for (const n of nodes) columns[layer[n.id] || 0].push(n.id);

    // Assign positions.
    const positions = {};
    const maxColSize = Math.max(1, ...columns.map((c) => c.length));
    const totalH = maxColSize * (DF_NODE_H + DF_ROW_GAP) - DF_ROW_GAP + DF_PAD * 2;
    let x = DF_PAD;
    for (let c = 0; c <= maxLayer; c++) {
      const col = columns[c];
      const colTotalH = col.length * (DF_NODE_H + DF_ROW_GAP) - DF_ROW_GAP;
      const startY = DF_PAD + (totalH - DF_PAD * 2 - colTotalH) / 2;
      for (let r = 0; r < col.length; r++) {
        positions[col[r]] = { x, y: startY + r * (DF_NODE_H + DF_ROW_GAP) };
      }
      x += DF_NODE_W + DF_COL_GAP;
    }
    return positions;
  }

  // --- Cycle detection (client-side) ------------------------------------------

  function dfGraphHasCycle() {
    if (!flowsState.drawflowEditor) return false;
    const exported = flowsState.drawflowEditor.export();
    const home = exported && exported.drawflow && exported.drawflow.Home
      ? exported.drawflow.Home.data
      : {};

    // Build adjacency list.
    const adj = {};
    for (const dfId of Object.keys(home)) {
      adj[dfId] = [];
      const outputs = home[dfId].outputs || {};
      for (const outKey of Object.keys(outputs)) {
        for (const conn of (outputs[outKey].connections || [])) {
          adj[dfId].push(String(conn.node));
        }
      }
    }

    // DFS cycle detection.
    const visited = {};
    const recStack = {};
    function hasCycleDFS(v) {
      visited[v] = true;
      recStack[v] = true;
      for (const w of (adj[v] || [])) {
        if (!visited[w] && hasCycleDFS(w)) return true;
        if (recStack[w]) return true;
      }
      recStack[v] = false;
      return false;
    }
    for (const v of Object.keys(adj)) {
      if (!visited[v] && hasCycleDFS(v)) return true;
    }
    return false;
  }

  // --- Canvas Edit mode -------------------------------------------------------

  function toggleCanvasEditMode() {
    flowsState.canvasEditMode = !flowsState.canvasEditMode;
    if (flowsState.drawflowEditor) {
      flowsState.drawflowEditor.editor_mode = flowsState.canvasEditMode ? 'edit' : 'fixed';
    }

    const editBtn = document.getElementById('flows-canvas-edit-btn');
    const addBtn = document.getElementById('flows-add-node-btn');
    const noteEl = document.getElementById('flows-canvas-mode-note');

    if (editBtn) {
      editBtn.classList.toggle('active', flowsState.canvasEditMode);
      editBtn.setAttribute('aria-pressed', String(flowsState.canvasEditMode));
      editBtn.textContent = flowsState.canvasEditMode ? 'View' : 'Edit';
    }
    if (addBtn) {
      addBtn.style.display = flowsState.canvasEditMode ? '' : 'none';
    }
    if (noteEl) {
      noteEl.textContent = flowsState.canvasEditMode
        ? 'Edit mode — drag nodes, draw connections. Double-click background to add a node. Select a node and press Delete to remove it.'
        : 'View mode — click a node to view output. Toggle Edit to add, connect, and edit nodes.';
    }

    // Close any open edit panel when leaving Edit mode.
    if (!flowsState.canvasEditMode) closeNodeEditPanel();

    announceFlows(flowsState.canvasEditMode ? 'Canvas edit mode enabled' : 'Canvas view mode enabled');
  }

  // --- Add new node -----------------------------------------------------------

  function addNewNodeToCanvas() {
    if (!flowsState.drawflowEditor || !flowsState.canvasEditMode) return;
    if (!flowsState.selectedName) return;

    // Generate a unique default ID.
    const existingIds = getExistingNodeIds();
    let candidate = 'node1';
    let counter = 1;
    while (existingIds.indexOf(candidate) !== -1) {
      counter++;
      candidate = 'node' + counter;
    }

    const newNodeData = {
      id: candidate,
      agent: 'claude',
      prompt: 'Describe the task.',
      output_limit: 8000,
      model: '',
      runtime: '',
      timeout: 0,
    };

    // Open edit panel for the new node (not yet added to graph).
    openNodeEditPanelForNew(newNodeData);
  }

  function getExistingNodeIds() {
    if (!flowsState.drawflowEditor) return [];
    const exported = flowsState.drawflowEditor.export();
    const home = exported && exported.drawflow && exported.drawflow.Home
      ? exported.drawflow.Home.data
      : {};
    return Object.values(home).map((n) => (n.data || {}).yakosId || '').filter(Boolean);
  }

  // --- Node edit panel (CRUD) -------------------------------------------------

  var _editingDfNodeId = null; // Drawflow internal node id currently being edited
  var _editingIsNew = false;   // true when adding a new node (not yet in graph)
  var _editingNewData = null;  // pending new node data

  function openNodeEditPanel(dfId) {
    const nodeInfo = getDrawflowNodeData(dfId);
    if (!nodeInfo) return;
    const d = nodeInfo.data || {};

    _editingDfNodeId = dfId;
    _editingIsNew = false;
    _editingNewData = null;

    _populateNodeEditPanel(d, true);
  }

  function openNodeEditPanelForNew(nodeData) {
    _editingDfNodeId = null;
    _editingIsNew = true;
    _editingNewData = nodeData;

    _populateNodeEditPanel({
      yakosId: nodeData.id,
      agent: nodeData.agent,
      prompt: nodeData.prompt,
      output_limit: String(nodeData.output_limit),
      model: '',
      runtime: '',
      timeout: '0',
    }, false);
  }

  function _populateNodeEditPanel(d, showDelete) {
    const panel = document.getElementById('flows-node-edit-panel');
    if (!panel) return;

    document.getElementById('fne-id').value = d.yakosId || '';
    document.getElementById('fne-agent').value = d.agent || '';
    document.getElementById('fne-prompt').value = d.prompt || '';
    document.getElementById('fne-output-limit').value = d.output_limit || '8000';
    document.getElementById('fne-model').value = d.model || '';
    document.getElementById('fne-runtime').value = d.runtime || '';
    document.getElementById('fne-timeout').value = d.timeout || '0';

    const errEl = document.getElementById('fne-error');
    if (errEl) { errEl.style.display = 'none'; errEl.textContent = ''; }

    const delBtn = document.getElementById('fne-delete-btn');
    if (delBtn) delBtn.style.display = showDelete ? '' : 'none';

    panel.style.display = '';
    // Focus the ID field.
    setTimeout(function() {
      var inp = document.getElementById('fne-id');
      if (inp) inp.focus();
    }, 0);
  }

  function closeNodeEditPanel() {
    const panel = document.getElementById('flows-node-edit-panel');
    if (panel) panel.style.display = 'none';
    _editingDfNodeId = null;
    _editingIsNew = false;
    _editingNewData = null;
  }

  function applyNodeEditPanel() {
    const newId = (document.getElementById('fne-id').value || '').trim();
    const agent = (document.getElementById('fne-agent').value || '').trim();
    const prompt = (document.getElementById('fne-prompt').value || '').trim();
    const outputLimit = parseInt(document.getElementById('fne-output-limit').value, 10);
    const model = (document.getElementById('fne-model').value || '').trim();
    const runtime = (document.getElementById('fne-runtime').value || '').trim();
    const timeout = parseInt(document.getElementById('fne-timeout').value, 10) || 0;

    const errEl = document.getElementById('fne-error');
    function showFneErr(msg) {
      if (errEl) { errEl.textContent = msg; errEl.style.display = ''; }
    }

    // Validate ID.
    if (!WORKFLOW_ID_RE.test(newId)) {
      showFneErr('ID must match ^[a-z0-9][a-z0-9-]{0,63}$ (lowercase, digits, hyphens, max 64)');
      return;
    }
    if (!agent) { showFneErr('Agent is required'); return; }
    if (!prompt) { showFneErr('Prompt is required'); return; }
    if (!outputLimit || outputLimit <= 0) { showFneErr('output_limit must be > 0'); return; }

    // Check uniqueness (skip if editing same node).
    const existingIds = getExistingNodeIds();
    const oldId = _editingIsNew ? null : (
      _editingDfNodeId
        ? ((getDrawflowNodeData(_editingDfNodeId) || {}).data || {}).yakosId
        : null
    );
    const isDupe = existingIds.filter((id) => id !== oldId).indexOf(newId) !== -1;
    if (isDupe) { showFneErr('Node ID "' + newId + '" is already used in this workflow'); return; }

    const newData = {
      yakosId: newId,
      agent: agent,
      prompt: prompt,
      output_limit: String(outputLimit),
      model: model,
      runtime: runtime,
      timeout: String(timeout),
    };

    if (_editingIsNew) {
      // Add new node to canvas at a sensible position.
      const canvasEl = document.getElementById('flows-canvas');
      const cx = canvasEl ? canvasEl.scrollLeft + 200 : 200;
      const cy = canvasEl ? canvasEl.scrollTop + 100 : 100;
      addDrawflowNode(flowsState.drawflowEditor, {
        id: newId,
        agent: agent,
        prompt: prompt,
        output_limit: outputLimit,
        model: model,
        runtime: runtime,
        timeout: timeout,
      }, cx, cy);
      announceFlows('Node "' + newId + '" added');
    } else if (_editingDfNodeId) {
      // Update existing node data.
      try {
        flowsState.drawflowEditor.updateNodeDataFromId(_editingDfNodeId, newData);
        // Update the node HTML label.
        const el = document.querySelector('#node-' + _editingDfNodeId + ' .df-node-id');
        if (el) el.textContent = newId;
        const agentEl = document.querySelector('#node-' + _editingDfNodeId + ' .df-node-agent');
        if (agentEl) agentEl.textContent = agent;
      } catch (_) { /* defensive */ }
      announceFlows('Node "' + newId + '" updated');
    }

    closeNodeEditPanel();
    syncCanvasToYaml();
  }

  function deleteNodeFromEditPanel() {
    if (!_editingDfNodeId || !flowsState.drawflowEditor) return;
    try {
      flowsState.drawflowEditor.removeNodeId('node-' + _editingDfNodeId);
      announceFlows('Node deleted');
    } catch (_) { /* defensive */ }
    closeNodeEditPanel();
    syncCanvasToYaml();
  }

  // --- YAML string quoting helper ---------------------------------------------

  function yamlQuoteString(s) {
    // Safely quote a string for YAML emission. Use double-quote form when needed.
    if (!s) return '""';
    // If string contains special chars that need quoting in YAML, double-quote it.
    if (/[:#\[\]{},|>&*!'"\\%@`\n\r\t]/.test(s) || /^\s|\s$/.test(s)) {
      return '"' + s.replace(/\\/g, '\\\\').replace(/"/g, '\\"').replace(/\n/g, '\\n').replace(/\r/g, '\\r') + '"';
    }
    return s;
  }

  // ---- Status helpers -----------------------------------------------------------

  function nodeStatusIcon(status) {
    switch (status) {
      case 'pending':   return '○';
      case 'running':   return '◉';
      case 'completed': return '●';
      case 'failed':    return '✗';
      case 'skipped':   return '–';
      default:          return '?';
    }
  }

  function nodeStatusLabel(status) {
    switch (status) {
      case 'pending':   return 'pending';
      case 'running':   return 'running';
      case 'completed': return 'completed';
      case 'failed':    return 'failed';
      case 'skipped':   return 'skipped';
      default:          return status || 'unknown';
    }
  }

  function runStatusIcon(status) {
    switch (status) {
      case 'pending':     return '○';
      case 'running':     return '◉';
      case 'completed':   return '●';
      case 'failed':      return '✗';
      case 'interrupted': return '!';
      default:            return '?';
    }
  }

  // runStatusLabel(status, runId) — runId is optional; when provided, a "failed"
  // status for a run where the operator requested cancellation is annotated as
  // "failed (cancelled)" so the UI reflects intent without inventing a backend status.
  function runStatusLabel(status, runId) {
    switch (status) {
      case 'pending':     return 'pending';
      case 'running':     return 'running';
      case 'completed':   return 'completed';
      case 'failed':
        if (runId && flowsState._cancelledRuns.has(runId)) return 'failed (cancelled)';
        return 'failed';
      case 'interrupted': return 'interrupted';
      default:            return status || 'unknown';
    }
  }

  function announceFlows(msg) {
    const el = document.getElementById('flows-announcer');
    if (!el) return;
    el.textContent = '';
    requestAnimationFrame(() => { el.textContent = msg; });
  }

  // =========================================================================
  // ---- IDE TAB (Phase 7 shell) ---------------------------------------------
  // =========================================================================
  //
  // Layout: 3-pane CSS grid:
  //   LEFT  — file tree (collapsible dirs; lazy-load via ?dir= on expand OR
  //            render the nested children returned by the API)
  //   CENTER — Monaco editor in an <iframe src="/ide/editor"> (read-only)
  //   RIGHT  — one chat pane (reuses the existing per-pane chat machinery)
  //
  // File-open flow:
  //   click file → GET /api/files/content?path= (SW injects token)
  //              → decode base64 if encoding==='base64'
  //              → postMessage({type:'openFile', path, content, language})
  //                to the Monaco iframe (origin-checked)
  //
  // Monaco handshake:
  //   - Hold a ref to the iframe's contentWindow.
  //   - Queue any openFile before {type:'ready'} arrives from the iframe.
  //   - On {type:'ready'}: flush the queued open (if any).
  //   - On {type:'error'}: surface a small inline error notice.
  //
  // Chat reuse:
  //   The IDE panel instantiates a single chat pane inside its layout by
  //   calling makePane() + buildPaneElement() — the same per-pane construction
  //   used by the Chat tab.  The SSE reader (startChatSSE) is shared; no second
  //   SSE connection is opened.  bootChatInfrastructure() is called to ensure
  //   the shared SSE + operator-ID are ready before mounting the pane.
  //
  //   The IDE's embedded pane is kept OUT of the persisted chatPanes map and
  //   savePaneState() so it does not consume from MAX_PANES (6) budget or
  //   reappear in the Chat tab's pane rail on reload.
  //
  // Phase 2 adds: Edit/View toggle, dirty tracking, ⌘S / Save button,
  // POST /api/files/write with optimistic concurrency (version echo).

  // =========================================================================
  // ── IDE TAB STATE INITIALISATION ─────────────────────────────────────────
  // =========================================================================
  //
  // These are var re-assignments (the vars were already hoisted at the top of
  // the IIFE for TDZ safety). Setting them here to their canonical initial
  // values right before the first IDE function definitions keeps the
  // authoritative defaults co-located with the code that uses them.

  ideTabInitialized = false;
  ideEditorWindow   = null;
  ideEditorReady    = false;
  ideQueuedOpen     = [];    // B2 fix: array queue so rapid pre-ready opens all land
  ideMessageHandlerRegistered = false;
  ideOpenFiles      = new Map(); // path → {version, dirty, editable, saving, saveStatusTimer}
  ideActiveTabPath  = '';
  ideCurrentPath    = '';
  ideCurrentVersion = '';
  ideEditable       = false;
  ideIsDirty        = false;
  ideIsSaving       = false;
  ideSaveStatusTimer = null;

  // ── Layout persistence helpers ────────────────────────────────────────────

  function ideLoadLayout() {
    var defaults = { treeW: 220, chatW: 280, treeCollapsed: false, chatCollapsed: false };
    try {
      var raw = localStorage.getItem(IDE_LAYOUT_LS_KEY);
      if (!raw) return defaults;
      var parsed = JSON.parse(raw);
      // Validate — tolerate corrupt/partial values gracefully.
      return {
        treeW:          (typeof parsed.treeW === 'number' && parsed.treeW > 0)  ? parsed.treeW  : defaults.treeW,
        chatW:          (typeof parsed.chatW === 'number' && parsed.chatW > 0)  ? parsed.chatW  : defaults.chatW,
        treeCollapsed:  !!parsed.treeCollapsed,
        chatCollapsed:  !!parsed.chatCollapsed,
      };
    } catch (_) { return defaults; }
  }

  function ideSaveLayout(layout) {
    try { localStorage.setItem(IDE_LAYOUT_LS_KEY, JSON.stringify(layout)); } catch (_) {}
  }

  // ── Tab strip helpers ─────────────────────────────────────────────────────
  //
  // One <button role="tab"> per open file; the close × is a nested <button>.
  // The entire strip is in a <div role="tablist">.

  function ideRenderTabStrip() {
    const strip = document.getElementById('ide-tab-strip');
    if (!strip) return;
    strip.innerHTML = '';
    var i = 0;
    for (const [path, fileState] of ideOpenFiles) {
      const basename = path.split('/').pop() || path;
      const isActive = path === ideActiveTabPath;
      const tab = document.createElement('button');
      tab.type = 'button';
      tab.className = 'ide-tab' + (isActive ? ' ide-tab-active' : '');
      tab.setAttribute('role', 'tab');
      tab.setAttribute('aria-selected', isActive ? 'true' : 'false');
      tab.setAttribute('title', path);
      tab.setAttribute('data-ide-tab-path', path);
      tab.setAttribute('tabindex', isActive ? '0' : '-1');
      tab.setAttribute('id', 'ide-tab-' + i);

      const dot = document.createElement('span');
      dot.className = 'ide-tab-dirty' + (fileState.dirty ? '' : ' ide-tab-dirty-hidden');
      dot.setAttribute('aria-hidden', 'true');
      dot.textContent = '●';

      const nameSpan = document.createElement('span');
      nameSpan.className = 'ide-tab-name';
      nameSpan.textContent = basename;

      const closeBtn = document.createElement('button');
      closeBtn.type = 'button';
      closeBtn.className = 'ide-tab-close';
      closeBtn.setAttribute('aria-label', 'Close ' + basename);
      // tabindex="-1": close button is reachable via Delete/Backspace on the tab
      // (S-1 / APG tab pattern) and by mouse click. Focus stays on the tab itself.
      closeBtn.setAttribute('tabindex', '-1');
      closeBtn.textContent = '×';

      tab.appendChild(dot);
      tab.appendChild(nameSpan);
      tab.appendChild(closeBtn);
      strip.appendChild(tab);

      // Capture path for closures.
      (function(p) {
        tab.addEventListener('click', function(e) {
          if (e.target === closeBtn || closeBtn.contains(e.target)) return;
          ideActivateTab(p);
        });
        closeBtn.addEventListener('click', function(e) {
          e.stopPropagation();
          ideCloseTab(p);
        });
      }(path));

      i++;
    }

    // Arrow-key navigation + Delete/Backspace close within the tablist (ARIA pattern).
    strip.addEventListener('keydown', function(e) {
      var tabs = strip.querySelectorAll('[role="tab"]');
      var arr = Array.prototype.slice.call(tabs);
      var idx = arr.indexOf(document.activeElement);
      if (e.key === 'ArrowRight') {
        e.preventDefault();
        var next = arr[(idx + 1) % arr.length];
        if (next) { next.focus(); next.click(); }
      } else if (e.key === 'ArrowLeft') {
        e.preventDefault();
        var prev = arr[(idx - 1 + arr.length) % arr.length];
        if (prev) { prev.focus(); prev.click(); }
      } else if (e.key === 'Home') {
        e.preventDefault();
        if (arr[0]) { arr[0].focus(); arr[0].click(); }
      } else if (e.key === 'End') {
        e.preventDefault();
        var last = arr[arr.length - 1];
        if (last) { last.focus(); last.click(); }
      } else if (e.key === 'Delete' || e.key === 'Backspace') {
        // S-1 (2.1.1): keyboard close of the focused tab.
        // APG tab pattern: Delete closes the focused tab; focus moves to
        // the right neighbor, or left if the closed tab was last.
        if (idx === -1) return;
        e.preventDefault();
        var closingPath = arr[idx] && arr[idx].getAttribute('data-ide-tab-path');
        if (closingPath) ideCloseTab(closingPath);
      }
    });
  }

  function ideActivateTab(path) {
    if (path === ideActiveTabPath) return;
    const fileState = ideOpenFiles.get(path);
    if (!fileState) return;

    ideActiveTabPath = path;
    // Sync legacy single-file vars to the newly active tab.
    ideCurrentPath   = path;
    ideCurrentVersion = fileState.version;
    ideEditable      = fileState.editable;
    ideIsDirty       = fileState.dirty;
    // Per-file saving state: treat switching away from a saving tab as cancelling
    // the UI lock (the in-flight fetch will still complete; its finally() resets
    // the per-file saving flag).
    ideIsSaving      = fileState.saving || false;

    // Tell Monaco to switch to this file's model.
    // The iframe already has the model from when the file was first opened —
    // we just send openFile with no content to switch model without re-loading.
    var payload = {
      type: 'openFile',
      path: path,
      content: null,   // null = reuse existing model; don't reset content
      language: null,
      editable: fileState.editable,
    };
    if (ideEditorReady && ideEditorWindow) {
      sendOpenFile(payload);
    } else {
      ideQueuedOpen.push(payload);
    }

    // Sync UI.
    ideRenderTabStrip();
    ideUpdateEditorHeader();
    // Clear any lingering editor notice from a previous tab.
    var notice = document.getElementById('ide-editor-notice');
    if (notice) notice.style.display = 'none';
    // M-2 (4.1.3): announce tab activation to screen readers via the
    // already-present aria-live="polite" path element.
    var pathEl = document.getElementById('ide-open-path');
    if (pathEl) {
      // Force a re-announcement even if the text didn't change (same path
      // re-activated after closing another): clear then set on next frame.
      var basename = path.split('/').pop() || path;
      pathEl.textContent = '';
      requestAnimationFrame(function() { pathEl.textContent = path; });
    }
  }

  function ideCloseTab(path) {
    const fileState = ideOpenFiles.get(path);
    if (!fileState) return;

    function doClose() {
      // B1 fix: determine neighbor BEFORE deleting from the map, and BEFORE
      // telling Monaco to switch away.  Activate the neighbor first so Monaco
      // has already set a different active model before closeFile arrives in
      // the iframe.  That way the iframe's `closeFile` guard
      //   if (editor.getModel() !== closeEntry.model) dispose()
      // evaluates correctly (the active model is already the neighbor's).
      var neighborPath = null;
      var neighborTabEl = null;
      if (ideActiveTabPath === path) {
        // Collect ordered keys from the map (insertion order).
        var keys = Array.from(ideOpenFiles.keys());
        var closingIdx = keys.indexOf(path);
        if (keys.length > 1) {
          // M-4: prefer the right neighbor; fall back to left if closing is last.
          neighborPath = (closingIdx < keys.length - 1)
            ? keys[closingIdx + 1]
            : keys[closingIdx - 1];
        }
      }

      // Switch editor to the neighbor now (before deleting or sending closeFile).
      if (neighborPath) {
        ideActivateTab(neighborPath);
        // M-4: restore keyboard focus to the newly active tab button.
        requestAnimationFrame(function() {
          var strip = document.getElementById('ide-tab-strip');
          if (strip) {
            var newActive = strip.querySelector('[data-ide-tab-path="' + neighborPath + '"]');
            if (newActive) newActive.focus();
          }
        });
      }

      // Now remove the closing path from the parent's map.
      ideOpenFiles.delete(path);

      // B1 fix: send closeFile AFTER switching the editor.  Monaco's iframe
      // handler can now unconditionally dispose the model (the active model has
      // already changed to the neighbor's model).
      if (ideEditorWindow) {
        try {
          ideEditorWindow.postMessage(
            { type: 'closeFile', path: path },
            window.location.origin
          );
        } catch (_) {}
      }

      if (!neighborPath) {
        if (ideActiveTabPath === path) {
          // All tabs closed — reset state.
          ideActiveTabPath = '';
          ideCurrentPath   = '';
          ideCurrentVersion = '';
          ideEditable       = false;
          ideIsDirty        = false;
          ideIsSaving       = false;
          ideRenderTabStrip();
          ideUpdateEditorHeader();
          // M-2 (4.1.3): announce "all closed" to screen readers.
          var pathEl = document.getElementById('ide-open-path');
          if (pathEl) pathEl.textContent = 'All files closed';
        } else {
          ideRenderTabStrip();
        }
      }
      // (If neighborPath was set, ideActivateTab already re-rendered the strip.)
    }

    if (fileState.dirty) {
      openModal({
        title:       'Unsaved changes',
        body:        '<p>Close <strong>' + esc(path.split('/').pop() || path) + '</strong>?</p>' +
                     '<p class="modal-field-hint" style="margin-top:8px">' +
                       'Your unsaved changes will be lost.' +
                     '</p>',
        confirmText: 'Close without saving',
        cancelText:  'Keep editing',
        dangerous:   true,
        onConfirm:   function(overlay) { overlay._closeModal(); doClose(); },
      });
    } else {
      doClose();
    }
  }

  // ideUpdateEditorHeader refreshes the path, dirty marker, Edit/Save buttons
  // in the editor header to reflect ideActiveTabPath's state.
  function ideUpdateEditorHeader() {
    const pathEl = document.getElementById('ide-open-path');
    if (pathEl) pathEl.textContent = ideActiveTabPath;

    const fileState = ideActiveTabPath ? ideOpenFiles.get(ideActiveTabPath) : null;

    const marker = document.getElementById('ide-dirty-marker');
    const dirty  = !!(fileState && fileState.dirty);
    if (marker) marker.style.display = dirty ? '' : 'none';

    const saveBtn = document.getElementById('ide-save-btn');
    if (saveBtn) {
      const canSave = dirty && !!(fileState && fileState.editable);
      saveBtn.disabled = !canSave;
      saveBtn.setAttribute('aria-disabled', canSave ? 'false' : 'true');
    }

    const editToggle = document.getElementById('ide-edit-toggle');
    if (editToggle) {
      const editable = !!(fileState && fileState.editable);
      editToggle.textContent = editable ? 'View' : 'Edit';
      editToggle.setAttribute('aria-pressed', editable ? 'true' : 'false');
      editToggle.classList.toggle('ide-header-btn-active', editable);
    }
  }

  // ── Tab-aware openFileInEditor ─────────────────────────────────────────────
  //
  // If the path is already open in a tab, activate it (no re-fetch).
  // Otherwise add it as a new tab and send the file to Monaco.
  function openFileInEditor(path, content, language, version) {
    if (ideOpenFiles.has(path)) {
      // Already open: just activate the tab. Update version in case it changed
      // (e.g. reloaded after conflict).
      var existing = ideOpenFiles.get(path);
      if (version !== undefined && version !== null) {
        existing.version = version || '';
      }
      ideActivateTab(path);
      // Close any stale editor notice.
      var notice = document.getElementById('ide-editor-notice');
      if (notice) notice.style.display = 'none';
      return;
    }

    // New tab.
    ideOpenFiles.set(path, {
      version:          version || '',
      dirty:            false,
      editable:         false,  // always start in View mode
      saving:           false,
      saveStatusTimer:  null,
    });

    ideActiveTabPath  = path;
    ideCurrentPath    = path;
    ideCurrentVersion = version || '';
    ideEditable       = false;
    ideIsDirty        = false;
    ideIsSaving       = false;

    // Clear any stale editor notice.
    var newTabNotice = document.getElementById('ide-editor-notice');
    if (newTabNotice) newTabNotice.style.display = 'none';

    // Clear save status.
    ideShowSaveStatus('', false, 0);

    var payload = {
      type:     'openFile',
      path:     path,
      content:  content,
      language: language || 'plaintext',
      editable: false,
    };

    if (ideEditorReady && ideEditorWindow) {
      sendOpenFile(payload);
    } else {
      ideQueuedOpen.push(payload);
    }

    ideRenderTabStrip();
    ideUpdateEditorHeader();
  }

  // ---- Users tab (Phase 5) -------------------------------------------------------
  //
  // initUsersTab() is called on every switch to the Users tab so the table
  // stays fresh.  All mutations (add, role, reset-password, disable/enable,
  // delete, change-my-password) go through apiFetch so they carry the CSRF
  // header automatically in session mode and the Bearer token in loopback mode.
  //
  // Self-protection: 409 "ErrLastAdmin" responses are surfaced inline so the
  // admin always knows why an action was blocked.

  function initUsersTab() {
    const panel = document.getElementById('panel-users');
    if (!panel) return;

    // Show loading state while fetching.
    const tableWrap = panel.querySelector('.users-table-wrap');
    if (tableWrap) {
      tableWrap.innerHTML = '<p class="users-loading">Loading users…</p>';
    }

    apiFetch('GET', '/api/users').then(function(resp) {
      if (!resp || !resp.ok) {
        if (tableWrap) {
          tableWrap.innerHTML =
            '<p class="users-empty">Failed to load users (status ' +
            (resp ? resp.status : 'unknown') + ').</p>';
        }
        return;
      }
      return resp.json();
    }).then(function(data) {
      if (!data) return;
      renderUsersTable(data.users || []);
    }).catch(function(err) {
      if (tableWrap) {
        tableWrap.innerHTML =
          '<p class="users-empty">Error loading users: ' + esc(String(err)) + '</p>';
      }
    });
  }

  // renderUsersTable builds the users table from the users array returned by
  // GET /api/users.  Each row has inline role select, reset-password, disable/
  // enable, and delete actions.
  function renderUsersTable(users) {
    const panel = document.getElementById('panel-users');
    if (!panel) return;
    const tableWrap = panel.querySelector('.users-table-wrap');
    if (!tableWrap) return;

    const statusEl = panel.querySelector('.users-status');

    if (!users.length) {
      tableWrap.innerHTML = '<p class="users-empty">No users found.</p>';
      return;
    }

    var html =
      '<table class="users-table" aria-label="User accounts">' +
        '<thead>' +
          '<tr>' +
            '<th scope="col">Username</th>' +
            '<th scope="col">Role</th>' +
            '<th scope="col">Status</th>' +
            '<th scope="col">Pw Reset</th>' +
            '<th scope="col">Created</th>' +
            '<th scope="col">Actions</th>' +
          '</tr>' +
        '</thead>' +
        '<tbody>';

    var ROLES = ['read', 'dispatch', 'flows-run', 'admin'];

    for (var i = 0; i < users.length; i++) {
      var u = users[i];
      var username = esc(u.username || '');
      var role = esc(u.role || 'read');
      var disabled = !!u.disabled;
      var pwReset = !!u.passwordResetReq;
      var created = u.createdAt ? new Date(u.createdAt).toLocaleDateString() : '';

      // Role select
      var roleSelectHtml = '<select class="users-role-select" aria-label="Role for ' + username + '" data-username="' + username + '">';
      for (var r = 0; r < ROLES.length; r++) {
        var rEsc = esc(ROLES[r]);
        roleSelectHtml += '<option value="' + rEsc + '"' + (ROLES[r] === u.role ? ' selected' : '') + '>' + rEsc + '</option>';
      }
      roleSelectHtml += '</select>';

      // Status badge
      var statusBadge = disabled
        ? '<span class="users-badge users-badge-disabled">Disabled</span>'
        : '<span class="users-badge users-badge-active">Active</span>';

      // Pw reset badge
      var pwBadge = pwReset
        ? '<span class="users-badge users-badge-reset">Required</span>'
        : '<span class="users-badge users-badge-active">No</span>';

      // Actions
      var toggleAction = disabled
        ? '<button class="users-action-btn" type="button" data-action="enable" data-username="' + username + '" aria-label="Enable ' + username + '">Enable</button>'
        : '<button class="users-action-btn" type="button" data-action="disable" data-username="' + username + '" aria-label="Disable ' + username + '">Disable</button>';

      html +=
        '<tr>' +
          '<td>' + username + '</td>' +
          '<td>' + roleSelectHtml + '</td>' +
          '<td>' + statusBadge + '</td>' +
          '<td>' + pwBadge + '</td>' +
          '<td style="white-space:nowrap;color:var(--text-dim)">' + esc(created) + '</td>' +
          '<td class="users-row-actions">' +
            '<button class="users-action-btn" type="button" data-action="reset-password" data-username="' + username + '" aria-label="Reset password for ' + username + '">Reset pw</button>' +
            toggleAction +
            '<button class="users-action-btn danger" type="button" data-action="delete" data-username="' + username + '" aria-label="Delete user ' + username + '">Delete</button>' +
          '</td>' +
        '</tr>';
    }

    html += '</tbody></table>';
    tableWrap.innerHTML = html;

    // Wire role select changes.
    var roleSelects = tableWrap.querySelectorAll('.users-role-select');
    for (var si = 0; si < roleSelects.length; si++) {
      (function(sel) {
        sel.addEventListener('change', function() {
          var uname = sel.getAttribute('data-username');
          var newRole = sel.value;
          usersApiAction('/api/users/role', { username: uname, role: newRole }, function(ok, msg) {
            showUsersStatus(statusEl, ok, ok ? ('Role updated for ' + uname) : msg);
            if (ok) initUsersTab();
          });
        });
      }(roleSelects[si]));
    }

    // Wire action buttons.
    var actionBtns = tableWrap.querySelectorAll('.users-action-btn');
    for (var bi = 0; bi < actionBtns.length; bi++) {
      (function(btn) {
        btn.addEventListener('click', function() {
          var action = btn.getAttribute('data-action');
          var uname = btn.getAttribute('data-username');
          if (action === 'reset-password') {
            openResetPasswordModal(uname);
          } else if (action === 'disable') {
            usersApiAction('/api/users/disable', { username: uname }, function(ok, msg) {
              showUsersStatus(statusEl, ok, ok ? (uname + ' disabled') : msg);
              if (ok) initUsersTab();
            });
          } else if (action === 'enable') {
            usersApiAction('/api/users/enable', { username: uname }, function(ok, msg) {
              showUsersStatus(statusEl, ok, ok ? (uname + ' enabled') : msg);
              if (ok) initUsersTab();
            });
          } else if (action === 'delete') {
            openDeleteUserModal(uname);
          }
        });
      }(actionBtns[bi]));
    }
  }

  // usersApiAction calls apiFetch POST to a /api/users/* endpoint and calls
  // cb(ok, message) with the result.  409 ErrLastAdmin is surfaced explicitly.
  function usersApiAction(endpoint, body, cb) {
    apiFetch('POST', endpoint, body)
      .then(function(resp) {
        if (!resp) { cb(false, 'No response from server'); return; }
        return resp.json().then(function(data) {
          if (resp.ok) {
            cb(true, (data && data.message) || 'OK');
          } else if (resp.status === 409) {
            // Last-admin guard — surface clearly.
            cb(false, (data && data.error) || 'Cannot remove the last admin');
          } else {
            cb(false, (data && data.error) || ('Error ' + resp.status));
          }
        });
      })
      .catch(function(err) {
        cb(false, 'Network error: ' + String(err));
      });
  }

  // showUsersStatus updates the status live region in the Users panel.
  // Clears after 4 seconds.
  var _usersStatusTimer = null;
  function showUsersStatus(el, ok, msg) {
    if (!el) return;
    el.textContent = msg || '';
    el.className = 'users-status' + (ok ? '' : ' error');
    // S-2: do NOT re-set aria-live here — the static markup already declares
    // role="status" aria-live="polite" on this element.  Redundant setAttribute
    // calls can interrupt live-region announcements in some AT implementations.
    clearTimeout(_usersStatusTimer);
    _usersStatusTimer = setTimeout(function() {
      if (el) el.textContent = '';
    }, 4000);
  }

  // openResetPasswordModal prompts for a new password and calls
  // POST /api/users/reset-password.
  function openResetPasswordModal(username) {
    var panel = document.getElementById('panel-users');
    var statusEl = panel ? panel.querySelector('.users-status') : null;

    var bodyHTML =
      '<label for="modal-reset-pw" class="modal-field-label">New temporary password for <strong>' + esc(username) + '</strong></label>' +
      '<input id="modal-reset-pw" class="modal-input" type="password" autocomplete="new-password" ' +
        'placeholder="Min 12 characters" aria-describedby="modal-reset-pw-err">' +
      '<p id="modal-reset-pw-err" class="modal-field-error" role="alert" style="display:none"></p>';

    openModal({
      title: 'Reset password',
      body: bodyHTML,
      confirmText: 'Reset',
      onConfirm: function(overlay) {
        var input = overlay.querySelector('#modal-reset-pw');
        var errEl = overlay.querySelector('#modal-reset-pw-err');
        var newPw = input ? input.value : '';
        if (!newPw) {
          if (errEl) { errEl.textContent = 'Password is required.'; errEl.style.display = ''; }
          return;
        }
        // cr S2: client-side length guard.
        if (newPw.length < 12) {
          if (errEl) { errEl.textContent = 'Password must be at least 12 characters.'; errEl.style.display = ''; }
          return;
        }
        usersApiAction('/api/users/reset-password', { username: username, newPassword: newPw }, function(ok, msg) {
          if (ok) {
            overlay._closeModal();
            showUsersStatus(statusEl, true, 'Password reset for ' + username);
            initUsersTab();
          } else {
            if (errEl) { errEl.textContent = msg; errEl.style.display = ''; }
          }
        });
      },
    });
  }

  // openDeleteUserModal asks for confirmation before calling
  // POST /api/users/delete.
  function openDeleteUserModal(username) {
    var panel = document.getElementById('panel-users');
    var statusEl = panel ? panel.querySelector('.users-status') : null;

    // S-3: pre-render the error element in the body string (same pattern as
    // the other three modals) instead of injecting it dynamically in the
    // confirm handler.  AT implementations see the live region in the initial
    // DOM and announce it immediately when textContent is populated.
    openModal({
      title: 'Delete user',
      body: '<p>Permanently delete <strong>' + esc(username) + '</strong>? This cannot be undone.</p>' +
            '<p id="modal-del-err" class="modal-field-error" role="alert" style="display:none"></p>',
      confirmText: 'Delete',
      cancelText: 'Cancel',
      dangerous: true,
      onConfirm: function(overlay) {
        var errEl = overlay.querySelector('#modal-del-err');
        usersApiAction('/api/users/delete', { username: username }, function(ok, msg) {
          if (ok) {
            overlay._closeModal();
            showUsersStatus(statusEl, true, username + ' deleted');
            initUsersTab();
          } else {
            // Surface 409 last-admin guard inline.
            if (errEl) { errEl.textContent = msg; errEl.style.display = ''; }
          }
        });
      },
    });
  }

  // openAddUserModal shows the "Add user" form → POST /api/users.
  function openAddUserModal() {
    var panel = document.getElementById('panel-users');
    var statusEl = panel ? panel.querySelector('.users-status') : null;

    var bodyHTML =
      '<label for="modal-add-username" class="modal-field-label">Username</label>' +
      '<input id="modal-add-username" class="modal-input" type="text" autocomplete="off" ' +
        'spellcheck="false" placeholder="e.g. alice" aria-describedby="modal-add-err">' +
      '<label for="modal-add-password" class="modal-field-label" style="margin-top:10px">Password</label>' +
      '<input id="modal-add-password" class="modal-input" type="password" autocomplete="new-password" ' +
        'placeholder="Min 12 characters" aria-describedby="modal-add-err">' +
      '<label for="modal-add-role" class="modal-field-label" style="margin-top:10px">Role</label>' +
      '<select id="modal-add-role" class="users-role-select" style="width:100%;font-size:13px;padding:5px 8px" aria-describedby="modal-add-err">' +
        '<option value="read">read</option>' +
        '<option value="dispatch">dispatch</option>' +
        '<option value="flows-run">flows-run</option>' +
        '<option value="admin">admin</option>' +
      '</select>' +
      '<p id="modal-add-err" class="modal-field-error" role="alert" style="display:none"></p>';

    openModal({
      title: 'Add user',
      body: bodyHTML,
      confirmText: 'Create',
      onConfirm: function(overlay) {
        var unameInput = overlay.querySelector('#modal-add-username');
        var pwInput    = overlay.querySelector('#modal-add-password');
        var roleInput  = overlay.querySelector('#modal-add-role');
        var errEl      = overlay.querySelector('#modal-add-err');

        var uname = (unameInput ? unameInput.value : '').trim();
        var pw    = pwInput ? pwInput.value : '';
        var role  = roleInput ? roleInput.value : 'read';

        function showErr(msg) {
          if (errEl) { errEl.textContent = msg; errEl.style.display = ''; }
        }

        if (!uname) { showErr('Username is required.'); return; }
        if (!pw)    { showErr('Password is required.'); return; }
        // cr S2: client-side length guard avoids an unnecessary round-trip.
        if (pw.length < 12) { showErr('Password must be at least 12 characters.'); return; }

        usersApiAction('/api/users', { username: uname, password: pw, role: role }, function(ok, msg) {
          if (ok) {
            overlay._closeModal();
            showUsersStatus(statusEl, true, 'User ' + uname + ' created');
            initUsersTab();
          } else {
            showErr(msg);
          }
        });
      },
    });
  }

  // openChangePasswordModal shows the self-service "Change my password" form
  // → POST /api/account/password.
  function openChangePasswordModal() {
    var panel = document.getElementById('panel-users');
    var statusEl = panel ? panel.querySelector('.users-status') : null;

    var bodyHTML =
      '<label for="modal-chpw-old" class="modal-field-label">Current password</label>' +
      '<input id="modal-chpw-old" class="modal-input" type="password" autocomplete="current-password" aria-describedby="modal-chpw-err">' +
      '<label for="modal-chpw-new" class="modal-field-label" style="margin-top:10px">New password</label>' +
      '<input id="modal-chpw-new" class="modal-input" type="password" autocomplete="new-password" ' +
        'placeholder="Min 12 characters" aria-describedby="modal-chpw-err">' +
      '<p id="modal-chpw-err" class="modal-field-error" role="alert" style="display:none"></p>';

    openModal({
      title: 'Change my password',
      body: bodyHTML,
      confirmText: 'Change',
      onConfirm: function(overlay) {
        var oldPwInput = overlay.querySelector('#modal-chpw-old');
        var newPwInput = overlay.querySelector('#modal-chpw-new');
        var errEl      = overlay.querySelector('#modal-chpw-err');

        var oldPw = oldPwInput ? oldPwInput.value : '';
        var newPw = newPwInput ? newPwInput.value : '';

        function showErr(msg) {
          if (errEl) { errEl.textContent = msg; errEl.style.display = ''; }
        }

        if (!oldPw) { showErr('Current password is required.'); return; }
        if (!newPw) { showErr('New password is required.'); return; }
        // cr S2: client-side length guard avoids an unnecessary round-trip.
        if (newPw.length < 12) { showErr('New password must be at least 12 characters.'); return; }

        apiFetch('POST', '/api/account/password', { oldPassword: oldPw, newPassword: newPw })
          .then(function(resp) {
            if (!resp) { showErr('No response from server.'); return; }
            return resp.json().then(function(data) {
              if (resp.ok) {
                overlay._closeModal();
                showUsersStatus(statusEl, true, 'Password changed. Please sign in again.');
                // Session invalidated server-side — redirect to login after short delay.
                setTimeout(function() { window.top.location.href = '/login'; }, 2000);
              } else if (resp.status === 401) {
                showErr('Current password is incorrect.');
              } else {
                showErr((data && data.error) || ('Error ' + resp.status));
              }
            });
          })
          .catch(function(err) {
            showErr('Network error: ' + String(err));
          });
      },
    });
  }

  function initIdeTab() {
    if (ideTabInitialized) return;
    ideTabInitialized = true;

    // Ensure chat infrastructure (SSE, operator ID) is running — we share it.
    bootChatInfrastructure();

    renderIdeLayout();
  }

  function renderIdeLayout() {
    const panel = document.getElementById('panel-ide');
    if (!panel) return;

    // Load persisted layout.
    var layout = ideLoadLayout();

    // Build column widths. Collapsed panes get a thin re-expand affordance (28px).
    var COLLAPSED_W = 28;
    var treeW = layout.treeCollapsed ? COLLAPSED_W : layout.treeW;
    var chatW = layout.chatCollapsed ? COLLAPSED_W : layout.chatW;

    panel.innerHTML =
      '<div class="ide-layout" id="ide-layout-root" role="main" aria-label="IDE" ' +
        'style="grid-template-columns:' + treeW + 'px 8px 1fr 8px ' + chatW + 'px">' +

        // ── Left: file tree pane ──────────────────────────────────────────
        '<aside class="ide-tree-pane' + (layout.treeCollapsed ? ' ide-pane-collapsed' : '') + '" ' +
          'id="ide-tree-col" aria-label="File tree" ' +
          'style="min-width:' + (layout.treeCollapsed ? COLLAPSED_W : 120) + 'px">' +
          '<div class="ide-tree-header">' +
            '<span class="subsection-title" id="ide-tree-title"' +
              (layout.treeCollapsed ? ' style="display:none"' : '') + '>Files</span>' +
            '<button class="ide-pane-toggle ide-tree-toggle" id="ide-tree-toggle" ' +
              'type="button" ' +
              'aria-expanded="' + (layout.treeCollapsed ? 'false' : 'true') + '" ' +
              'aria-controls="ide-tree-root" ' +
              'aria-label="' + (layout.treeCollapsed ? 'Expand file tree' : 'Collapse file tree') + '" ' +
              'title="' + (layout.treeCollapsed ? 'Expand file tree' : 'Collapse file tree') + '">' +
              (layout.treeCollapsed ? '&#x276F;' : '&#x276E;') +
            '</button>' +
          '</div>' +
          '<div id="ide-tree-root" class="ide-tree-root" role="tree" aria-label="Workspace files"' +
            (layout.treeCollapsed ? ' style="display:none"' : '') + '></div>' +
        '</aside>' +

        // ── Splitter: tree | editor ──────────────────────────────────────
        '<div class="ide-splitter" id="ide-splitter-left" ' +
          'role="separator" aria-orientation="vertical" ' +
          'aria-label="Resize file tree" tabindex="0" ' +
          'aria-valuemin="80" aria-valuemax="600" ' +
          'aria-valuenow="' + layout.treeW + '" ' +
          'aria-valuetext="' + layout.treeW + ' pixels" ' +
          'style="' + (layout.treeCollapsed ? 'display:none' : '') + '"></div>' +

        // ── Center: Monaco editor pane ────────────────────────────────────
        '<div class="ide-editor-pane" id="ide-editor-col">' +
          // Tab strip above editor (multi-file tabs).
          '<div class="ide-tab-strip" id="ide-tab-strip" ' +
            'role="tablist" aria-label="Open files">' +
          '</div>' +
          // Editor header: dirty marker, path, Edit, Save, status.
          '<div class="ide-editor-header" id="ide-editor-header">' +
            '<span id="ide-dirty-marker" class="ide-dirty-marker" aria-hidden="true" style="display:none">●</span>' +
            '<span id="ide-open-path" class="ide-open-path" aria-live="polite"></span>' +
            '<button id="ide-edit-toggle" type="button" class="ide-header-btn" ' +
              'aria-pressed="false" title="Toggle edit mode">Edit</button>' +
            '<button id="ide-save-btn" type="button" class="ide-header-btn ide-save-btn" ' +
              'disabled aria-disabled="true" title="Save file (⌘S / Ctrl-S)">Save</button>' +
            '<span id="ide-save-status" class="ide-save-status" aria-live="polite" role="status"></span>' +
          '</div>' +
          '<div class="ide-editor-wrap">' +
            '<div id="ide-editor-notice" class="ide-editor-notice" style="display:none" role="status"></div>' +
            // Transparent drag overlay — covers iframe during column resize so
            // pointer events don't get swallowed by the Monaco iframe.
            '<div id="ide-drag-overlay" class="ide-drag-overlay" style="display:none"></div>' +
            '<iframe id="ide-editor-frame" class="ide-editor-frame" ' +
              'src="/ide/editor" ' +
              'title="Code editor" ' +
              'aria-label="Monaco code editor">' +
            '</iframe>' +
          '</div>' +
        '</div>' +

        // ── Splitter: editor | chat ──────────────────────────────────────
        '<div class="ide-splitter" id="ide-splitter-right" ' +
          'role="separator" aria-orientation="vertical" ' +
          'aria-label="Resize chat panel" tabindex="0" ' +
          'aria-valuemin="80" aria-valuemax="600" ' +
          'aria-valuenow="' + layout.chatW + '" ' +
          'aria-valuetext="' + layout.chatW + ' pixels" ' +
          'style="' + (layout.chatCollapsed ? 'display:none' : '') + '"></div>' +

        // ── Right: embedded chat pane ────────────────────────────────────
        '<div class="ide-chat-pane' + (layout.chatCollapsed ? ' ide-pane-collapsed' : '') + '" ' +
          'aria-label="Chat" id="ide-chat-col" ' +
          'style="min-width:' + (layout.chatCollapsed ? COLLAPSED_W : 160) + 'px">' +
          '<div class="ide-chat-header">' +
            '<button class="ide-pane-toggle ide-chat-toggle" id="ide-chat-toggle" ' +
              'type="button" ' +
              'aria-expanded="' + (layout.chatCollapsed ? 'false' : 'true') + '" ' +
              'aria-controls="ide-chat-slot" ' +
              'aria-label="' + (layout.chatCollapsed ? 'Expand chat' : 'Collapse chat') + '" ' +
              'title="' + (layout.chatCollapsed ? 'Expand chat' : 'Collapse chat') + '">' +
              (layout.chatCollapsed ? '&#x276E;' : '&#x276F;') +
            '</button>' +
            '<span class="subsection-title"' +
              (layout.chatCollapsed ? ' style="display:none"' : '') + '>Chat</span>' +
          '</div>' +
          '<div id="ide-chat-slot"' +
            (layout.chatCollapsed ? ' style="display:none"' : '') + '></div>' +
        '</div>' +

      '</div>';

    // Wire Monaco iframe postMessage listener.
    if (!ideMessageHandlerRegistered) {
      window.addEventListener('message', handleIdeEditorMessage);
      ideMessageHandlerRegistered = true;
    }

    // Grab iframe contentWindow once it loads.
    const frame = document.getElementById('ide-editor-frame');
    if (frame) {
      frame.addEventListener('load', () => {
        ideEditorWindow = frame.contentWindow;
      });
    }

    // ── Edit/View toggle ──────────────────────────────────────────────────
    const editToggle = document.getElementById('ide-edit-toggle');
    if (editToggle) {
      editToggle.addEventListener('click', () => {
        if (!ideActiveTabPath) return;
        const fileState = ideOpenFiles.get(ideActiveTabPath);
        if (!fileState) return;
        fileState.editable = !fileState.editable;
        ideEditable = fileState.editable;
        if (ideEditorWindow) {
          ideEditorWindow.postMessage(
            { type: 'setEditable', editable: ideEditable },
            window.location.origin
          );
        }
        // When switching to View, clear dirty state.
        if (!ideEditable) {
          fileState.dirty = false;
          ideIsDirty = false;
          // Code-review S1: re-render tab strip so the ● dirty dot clears.
          ideRenderTabStrip();
        }
        ideUpdateEditorHeader();
      });
    }

    // ── Save button ───────────────────────────────────────────────────────
    const saveBtn = document.getElementById('ide-save-btn');
    if (saveBtn) {
      saveBtn.addEventListener('click', () => { triggerSave(); });
    }

    // ── Tree pane toggle ──────────────────────────────────────────────────
    const treeToggle = document.getElementById('ide-tree-toggle');
    if (treeToggle) {
      treeToggle.addEventListener('click', () => {
        ideTogglePane('tree');
      });
    }

    // ── Chat pane toggle ──────────────────────────────────────────────────
    const chatToggle = document.getElementById('ide-chat-toggle');
    if (chatToggle) {
      chatToggle.addEventListener('click', () => {
        ideTogglePane('chat');
      });
    }

    // ── Splitter drag (left: tree|editor) ────────────────────────────────
    ideWireSplitter('ide-splitter-left', 'left');

    // ── Splitter drag (right: editor|chat) ───────────────────────────────
    ideWireSplitter('ide-splitter-right', 'right');

    // ── Keyboard resize for splitters ────────────────────────────────────
    ideWireSplitterKeyboard('ide-splitter-left', 'left');
    ideWireSplitterKeyboard('ide-splitter-right', 'right');

    // ── Mount embedded chat pane ─────────────────────────────────────────
    mountIdeChatPane();

    // ── Load file tree ────────────────────────────────────────────────────
    loadIdeTree('');
  }

  // ── Pane collapse/expand ──────────────────────────────────────────────────

  function ideTogglePane(side) {
    const layout = ideLoadLayout();
    const COLLAPSED_W = 28;

    if (side === 'tree') {
      layout.treeCollapsed = !layout.treeCollapsed;
      ideSaveLayout(layout);
      var treeCol   = document.getElementById('ide-tree-col');
      var treeRoot  = document.getElementById('ide-tree-root');
      var treeTitle = document.getElementById('ide-tree-title');
      var toggle    = document.getElementById('ide-tree-toggle');
      var splitter  = document.getElementById('ide-splitter-left');
      var layoutEl  = document.getElementById('ide-layout-root');
      var collapsed = layout.treeCollapsed;

      if (treeCol)   treeCol.classList.toggle('ide-pane-collapsed', collapsed);
      if (treeRoot)  treeRoot.style.display = collapsed ? 'none' : '';
      if (treeTitle) treeTitle.style.display = collapsed ? 'none' : '';
      if (splitter)  splitter.style.display = collapsed ? 'none' : '';
      if (toggle) {
        toggle.innerHTML = collapsed ? '&#x276F;' : '&#x276E;';
        toggle.setAttribute('aria-expanded', collapsed ? 'false' : 'true');
        toggle.title = collapsed ? 'Expand file tree' : 'Collapse file tree';
        toggle.setAttribute('aria-label', collapsed ? 'Expand file tree' : 'Collapse file tree');
      }
      if (treeCol) treeCol.style.minWidth = (collapsed ? COLLAPSED_W : 120) + 'px';
      // Update grid.
      if (layoutEl) {
        var chatW = layout.chatCollapsed ? COLLAPSED_W : layout.chatW;
        var tW    = collapsed ? COLLAPSED_W : layout.treeW;
        layoutEl.style.gridTemplateColumns =
          tW + 'px ' + (collapsed ? '0' : '8') + 'px 1fr 8px ' + chatW + 'px';
      }

    } else { // chat
      layout.chatCollapsed = !layout.chatCollapsed;
      ideSaveLayout(layout);
      var chatCol   = document.getElementById('ide-chat-col');
      var chatSlot  = document.getElementById('ide-chat-slot');
      var chatTitle = chatCol ? chatCol.querySelector('.subsection-title') : null;
      var chatTog   = document.getElementById('ide-chat-toggle');
      var rSplitter = document.getElementById('ide-splitter-right');
      var layoutEl2 = document.getElementById('ide-layout-root');
      var collapsed2 = layout.chatCollapsed;

      if (chatCol)   chatCol.classList.toggle('ide-pane-collapsed', collapsed2);
      if (chatSlot)  chatSlot.style.display = collapsed2 ? 'none' : '';
      if (chatTitle) chatTitle.style.display = collapsed2 ? 'none' : '';
      if (rSplitter) rSplitter.style.display = collapsed2 ? 'none' : '';
      if (chatTog) {
        chatTog.innerHTML = collapsed2 ? '&#x276E;' : '&#x276F;';
        chatTog.setAttribute('aria-expanded', collapsed2 ? 'false' : 'true');
        chatTog.title = collapsed2 ? 'Expand chat' : 'Collapse chat';
        chatTog.setAttribute('aria-label', collapsed2 ? 'Expand chat' : 'Collapse chat');
      }
      if (chatCol) chatCol.style.minWidth = (collapsed2 ? COLLAPSED_W : 160) + 'px';
      if (layoutEl2) {
        var treeW2 = layout.treeCollapsed ? COLLAPSED_W : layout.treeW;
        var cW     = collapsed2 ? COLLAPSED_W : layout.chatW;
        layoutEl2.style.gridTemplateColumns =
          treeW2 + 'px 8px 1fr ' + (collapsed2 ? '0' : '8') + 'px ' + cW + 'px';
      }
    }
  }

  // ── Splitter drag ──────────────────────────────────────────────────────────

  function ideWireSplitter(splitterId, side) {
    var splitter = document.getElementById(splitterId);
    if (!splitter) return;

    var dragging  = false;
    var startX    = 0;
    var startSize = 0;

    splitter.addEventListener('pointerdown', function(e) {
      var layout = ideLoadLayout();
      var COLLAPSED_W = 28;
      if (side === 'left' && layout.treeCollapsed) return;
      if (side === 'right' && layout.chatCollapsed) return;

      dragging  = true;
      startX    = e.clientX;
      startSize = (side === 'left') ? layout.treeW : layout.chatW;
      splitter.setPointerCapture(e.pointerId);

      // Show the drag overlay to prevent Monaco iframe from swallowing events.
      var overlay = document.getElementById('ide-drag-overlay');
      if (overlay) overlay.style.display = 'block';

      e.preventDefault();
    });

    splitter.addEventListener('pointermove', function(e) {
      if (!dragging) return;
      var dx      = e.clientX - startX;
      var layout  = ideLoadLayout();
      var MIN     = 80;
      var MAX     = 600;
      var COLLAPSED_W = 28;
      var layoutEl = document.getElementById('ide-layout-root');
      if (!layoutEl) return;

      if (side === 'left') {
        var newW = Math.max(MIN, Math.min(MAX, startSize + dx));
        layout.treeW = newW;
        var chatW = layout.chatCollapsed ? COLLAPSED_W : layout.chatW;
        layoutEl.style.gridTemplateColumns = newW + 'px 8px 1fr 8px ' + chatW + 'px';
        var treeCol = document.getElementById('ide-tree-col');
        if (treeCol) treeCol.style.minWidth = MIN + 'px';
        splitter.setAttribute('aria-valuenow', newW);
        splitter.setAttribute('aria-valuetext', newW + ' pixels');
      } else {
        var newW2 = Math.max(MIN, Math.min(MAX, startSize - dx));
        layout.chatW = newW2;
        var treeW = layout.treeCollapsed ? COLLAPSED_W : layout.treeW;
        layoutEl.style.gridTemplateColumns = treeW + 'px 8px 1fr 8px ' + newW2 + 'px';
        var chatCol = document.getElementById('ide-chat-col');
        if (chatCol) chatCol.style.minWidth = MIN + 'px';
        splitter.setAttribute('aria-valuenow', newW2);
        splitter.setAttribute('aria-valuetext', newW2 + ' pixels');
      }
      ideSaveLayout(layout);
    });

    splitter.addEventListener('pointerup', function(e) {
      if (!dragging) return;
      dragging = false;
      splitter.releasePointerCapture(e.pointerId);
      var overlay = document.getElementById('ide-drag-overlay');
      if (overlay) overlay.style.display = 'none';
    });

    splitter.addEventListener('pointercancel', function(e) {
      dragging = false;
      splitter.releasePointerCapture(e.pointerId);
      var overlay = document.getElementById('ide-drag-overlay');
      if (overlay) overlay.style.display = 'none';
    });
  }

  function ideWireSplitterKeyboard(splitterId, side) {
    var splitter = document.getElementById(splitterId);
    if (!splitter) return;
    var STEP = 20;
    splitter.addEventListener('keydown', function(e) {
      var layout  = ideLoadLayout();
      var MIN     = 80;
      var MAX     = 600;
      var COLLAPSED_W = 28;
      var layoutEl = document.getElementById('ide-layout-root');
      if (!layoutEl) return;
      if (e.key !== 'ArrowLeft' && e.key !== 'ArrowRight') return;
      e.preventDefault();
      if (side === 'left') {
        if (layout.treeCollapsed) return;
        var delta = e.key === 'ArrowRight' ? STEP : -STEP;
        layout.treeW = Math.max(MIN, Math.min(MAX, layout.treeW + delta));
        var chatW = layout.chatCollapsed ? COLLAPSED_W : layout.chatW;
        layoutEl.style.gridTemplateColumns = layout.treeW + 'px 8px 1fr 8px ' + chatW + 'px';
        splitter.setAttribute('aria-valuenow', layout.treeW);
        splitter.setAttribute('aria-valuetext', layout.treeW + ' pixels');
      } else {
        if (layout.chatCollapsed) return;
        var delta2 = e.key === 'ArrowLeft' ? STEP : -STEP;
        layout.chatW = Math.max(MIN, Math.min(MAX, layout.chatW + delta2));
        var treeW = layout.treeCollapsed ? COLLAPSED_W : layout.treeW;
        layoutEl.style.gridTemplateColumns = treeW + 'px 8px 1fr 8px ' + layout.chatW + 'px';
        splitter.setAttribute('aria-valuenow', layout.chatW);
        splitter.setAttribute('aria-valuetext', layout.chatW + ' pixels');
      }
      ideSaveLayout(layout);
    });
  }

  // ── ideSetDirty — update dirty state for the active tab ──────────────────

  function ideSetDirty(dirty) {
    ideIsDirty = dirty;
    if (ideActiveTabPath) {
      var fileState = ideOpenFiles.get(ideActiveTabPath);
      if (fileState) fileState.dirty = dirty;
    }
    // Update the tab strip dot.
    ideRenderTabStrip();
    ideUpdateEditorHeader();
  }

  // ── ideShowSaveStatus — transient status message in editor header ─────────

  function ideShowSaveStatus(msg, isError, durationMs) {
    const el = document.getElementById('ide-save-status');
    if (!el) return;
    if (ideSaveStatusTimer !== null) {
      clearTimeout(ideSaveStatusTimer);
      ideSaveStatusTimer = null;
    }
    el.textContent = msg;
    el.className = 'ide-save-status' + (isError ? ' ide-save-status-error' : (msg ? ' ide-save-status-ok' : ''));
    if (durationMs) {
      ideSaveStatusTimer = setTimeout(() => {
        el.textContent = '';
        el.className = 'ide-save-status';
        ideSaveStatusTimer = null;
      }, durationMs);
    }
  }

  // ── triggerSave ───────────────────────────────────────────────────────────

  function triggerSave() {
    if (!ideEditable || !ideIsDirty || ideIsSaving) return;
    if (!ideEditorWindow || !ideCurrentPath) return;
    ideEditorWindow.postMessage({ type: 'requestContent' }, window.location.origin);
  }

  // ── performSave ───────────────────────────────────────────────────────────

  function performSave(path, content) {
    // Per-file saving state guard.
    var fileState = ideOpenFiles.get(path);
    if (fileState) fileState.saving = true;
    ideIsSaving = true;

    const saveBtn = document.getElementById('ide-save-btn');
    if (saveBtn) {
      saveBtn.disabled = true;
      saveBtn.setAttribute('aria-disabled', 'true');
      saveBtn.textContent = 'Saving…';
    }
    ideShowSaveStatus('', false, 0);

    var version = fileState ? fileState.version : ideCurrentVersion;

    // Route through apiFetch so session mode sends X-CSRF-Token + credentials.
    apiFetch('POST', '/api/files/write', { path: path, content: content, version: version })
      .then((r) => {
        if (r.status === 200) {
          return r.json().then((data) => {
            var newVersion = data.version || '';
            if (fileState) fileState.version = newVersion;
            if (path === ideActiveTabPath) ideCurrentVersion = newVersion;
            ideSetDirty(false);
            ideShowSaveStatus('Saved', false, 2500);
          });
        }
        if (r.status === 409) {
          ideShowSaveStatus('', false, 0);
          showIdeReloadConflictNotice(path);
          return;
        }
        if (r.status === 403) {
          ideShowSaveStatus('Permission denied (requires dispatch role)', true, 6000);
          return;
        }
        if (r.status === 413) {
          ideShowSaveStatus('File too large to save (max 5 MiB)', true, 6000);
          return;
        }
        return r.text().then(() => {
          ideShowSaveStatus('Save failed (' + String(r.status) + ') — your edits are preserved', true, 8000);
        });
      })
      .catch(() => {
        ideShowSaveStatus('Network error — your edits are preserved', true, 8000);
      })
      .finally(() => {
        if (fileState) fileState.saving = false;
        ideIsSaving = false;
        if (saveBtn) {
          saveBtn.textContent = 'Save';
          const canSave = ideIsDirty && ideEditable;
          saveBtn.disabled = !canSave;
          saveBtn.setAttribute('aria-disabled', canSave ? 'false' : 'true');
        }
      });
  }

  // ── showIdeReloadConflictNotice ───────────────────────────────────────────

  function showIdeReloadConflictNotice(path) {
    const notice = document.getElementById('ide-editor-notice');
    if (!notice) return;
    notice.textContent = '';
    notice.style.display = '';
    const msg = document.createTextNode('File changed on disk — ');
    notice.appendChild(msg);
    const reloadBtn = document.createElement('button');
    reloadBtn.type = 'button';
    reloadBtn.className = 'ide-notice-reload-btn';
    reloadBtn.textContent = 'Reload (discards local edits)';
    reloadBtn.addEventListener('click', () => {
      if (!window.confirm('Reload file from disk? Your unsaved edits will be lost.')) return;
      notice.style.display = 'none';
      // Remove from open-files map so it gets re-fetched and a new model is created.
      ideOpenFiles.delete(path);
      openIdeFile(path, path.split('/').pop(), null);
    });
    notice.appendChild(reloadBtn);
  }

  // ── handleIdeEditorMessage ────────────────────────────────────────────────

  function handleIdeEditorMessage(e) {
    if (e.origin !== window.location.origin) return;
    const msg = e.data;
    if (!msg || typeof msg !== 'object') return;

    if (msg.type === 'ready') {
      ideEditorReady = true;
      syncMonacoTheme(document.documentElement.getAttribute('data-theme') || 'og');
      // B2 fix: drain the array queue in order so rapid pre-ready opens all land.
      if (ideQueuedOpen.length > 0) {
        var queued = ideQueuedOpen.splice(0);
        for (var qi = 0; qi < queued.length; qi++) {
          sendOpenFile(queued[qi]);
        }
      }
    } else if (msg.type === 'error') {
      const notice = document.getElementById('ide-editor-notice');
      if (notice) {
        notice.textContent = 'Editor error: ' + (msg.message || 'unknown error');
        notice.style.display = '';
      }
    } else if (msg.type === 'dirty') {
      // msg.path is now included in the dirty message from ide-editor.js.
      // Only apply if it concerns the active tab (guard against stale messages).
      var dirtyPath = (typeof msg.path === 'string') ? msg.path : ideCurrentPath;
      if (dirtyPath === ideActiveTabPath) {
        var fileState = ideOpenFiles.get(dirtyPath);
        if (fileState && fileState.editable) {
          fileState.dirty = !!msg.dirty;
          ideIsDirty = !!msg.dirty;
          ideRenderTabStrip();
          ideUpdateEditorHeader();
        }
      }
    } else if (msg.type === 'save') {
      // ⌘S / Ctrl-S from Monaco.
      // B1: path echo guard — the echoed path must match the active tab path.
      // B2: !ideIsSaving — prevents double-POST when ⌘S fires twice quickly.
      if (ideEditable && ideIsDirty && !ideIsSaving &&
          typeof msg.content === 'string' && msg.path === ideCurrentPath) {
        performSave(ideCurrentPath, msg.content);
      }
    } else if (msg.type === 'content') {
      // Reply to requestContent from triggerSave().
      // B1: echoed path must match the current active path.
      if (typeof msg.content === 'string' && msg.path === ideCurrentPath &&
          ideEditable && ideIsDirty && !ideIsSaving) {
        performSave(ideCurrentPath, msg.content);
      }
    }
  }

  function sendOpenFile(payload) {
    if (!ideEditorWindow) return;
    ideEditorWindow.postMessage(payload, window.location.origin);
  }

  // syncMonacoTheme sends a {type:'setTheme', theme} postMessage to the Monaco
  // iframe. The iframe maps yakOS theme names to Monaco theme strings:
  //   ops, fluid, og → 'vs-dark'  (all dark themes use Monaco dark)
  //   light          → 'vs'       (Monaco's built-in light theme)
  // This is called by applyTheme() on every theme switch, and by
  // handleIdeEditorMessage on {type:'ready'} to sync the initial theme.
  // CSP-safe: same-origin postMessage requires no CSP changes.
  function syncMonacoTheme(theme) {
    if (!ideEditorWindow) return;
    try {
      ideEditorWindow.postMessage({ type: 'setTheme', theme: theme }, window.location.origin);
    } catch (_) {}
  }

  // ---- File tree ---------------------------------------------------------------

  // loadIdeTree fetches the root workspace listing at depth=1 (shallow) and
  // renders a collapsed tree. Individual dirs are lazy-loaded on expand.
  function loadIdeTree(reldir) {
    const treeEl = document.getElementById('ide-tree-root');
    if (!treeEl) return;

    const dir = reldir || '.';
    const url = '/api/files/tree?dir=' + encodeURIComponent(dir) + '&depth=1';

    // In session mode, send the session cookie (SW does not run inside
    // the IDE iframe context for these direct fetch calls).
    const treeRootOpts = AUTH_MODE === 'session' ? { credentials: 'same-origin' } : {};
    fetch(url, treeRootOpts).then((r) => {
      if (r.status === 403) {
        const p = document.createElement('p');
        p.className = 'ide-tree-error';
        p.textContent = 'Access denied (403). Check your token.';
        treeEl.innerHTML = '';
        treeEl.appendChild(p);
        return null;
      }
      if (r.status === 503) {
        const p = document.createElement('p');
        p.className = 'ide-tree-error';
        p.textContent = 'Workspace not configured.';
        treeEl.innerHTML = '';
        treeEl.appendChild(p);
        return null;
      }
      if (!r.ok) {
        const p = document.createElement('p');
        p.className = 'ide-tree-error';
        p.textContent = 'Failed to load tree (' + String(r.status) + ').';
        treeEl.innerHTML = '';
        treeEl.appendChild(p);
        return null;
      }
      return r.json();
    }).then((data) => {
      if (!data) return;
      treeEl.innerHTML = '';

      if (!data.entries || data.entries.length === 0) {
        const p = document.createElement('p');
        p.className = 'empty-state';
        p.style.padding = '8px';
        p.textContent = 'Workspace is empty';
        treeEl.appendChild(p);
        return;
      }

      const ul = buildTreeList(data.entries, 0);
      treeEl.appendChild(ul);

      if (data.truncated) {
        const note = document.createElement('p');
        note.className = 'ide-tree-truncated';
        note.textContent = 'Root directory truncated — too many entries.';
        treeEl.appendChild(note);
      }
    }).catch(() => {
      if (treeEl) {
        treeEl.innerHTML = '';
        const p = document.createElement('p');
        p.className = 'ide-tree-error';
        p.textContent = 'Network error loading file tree.';
        treeEl.appendChild(p);
      }
    });
  }

  // buildTreeList renders a list of file/dir entries at the given indent depth.
  // Dirs start COLLAPSED (aria-expanded='false'). Expanding a dir that has no
  // pre-fetched children fires a depth=1 lazy-load fetch and caches the result.
  // A per-dir truncation notice is appended when data.truncated is true.
  function buildTreeList(entries, depth) {
    const ul = document.createElement('ul');
    ul.className = 'ide-tree-list';
    ul.setAttribute('role', 'group');

    for (const entry of entries) {
      const li = document.createElement('li');
      li.className = 'ide-tree-item';
      li.setAttribute('role', 'treeitem');
      li.style.paddingLeft = (depth * 12) + 'px';

      if (entry.type === 'dir') {
        // Start collapsed.
        li.setAttribute('aria-expanded', 'false');
        const toggle = document.createElement('button');
        toggle.type = 'button';
        toggle.className = 'ide-tree-dir';
        // B2: esc() on server-supplied entry.name in aria-label.
        toggle.setAttribute('aria-label', 'Directory ' + esc(entry.name));
        toggle.title = entry.name; // tooltip for truncated names

        const iconSpan = document.createElement('span');
        iconSpan.className = 'ide-tree-icon ide-tree-dir-icon';
        iconSpan.setAttribute('aria-hidden', 'true');
        iconSpan.textContent = '▶'; // ▶ right-pointing (collapsed)

        const nameSpan = document.createElement('span');
        nameSpan.className = 'ide-tree-name';
        nameSpan.textContent = entry.name;

        toggle.appendChild(iconSpan);
        toggle.appendChild(nameSpan);
        li.appendChild(toggle);

        // childUl is null until the first expand (lazy sentinel).
        // If the server already returned children (depth > 1 request), pre-build.
        let childUl = null;
        if (entry.children && entry.children.length > 0) {
          childUl = buildTreeList(entry.children, depth + 1);
          childUl.style.display = 'none'; // still start collapsed
          li.appendChild(childUl);
        }
        // entry.children === [] means explicitly empty dir (pre-loaded but empty).
        // entry.children == null means depth cap — lazy-load on expand.

        // Track whether a fetch is in-flight to prevent double-clicks.
        let loading = false;
        let expanded = false;

        toggle.addEventListener('click', () => {
          if (loading) return; // ignore clicks while fetching
          expanded = !expanded;
          li.setAttribute('aria-expanded', String(expanded));
          iconSpan.textContent = expanded ? '▼' : '▶'; // ▼ / ▶

          if (expanded) {
            if (childUl !== null) {
              // Already fetched — just show.
              childUl.style.display = '';
            } else if (entry.children && entry.children.length === 0) {
              // Explicitly empty dir — nothing to show.
            } else {
              // Lazy-load: depth=1 for this subdirectory.
              loading = true;
              const spinner = document.createElement('div');
              spinner.className = 'ide-tree-loading';
              spinner.setAttribute('aria-label', 'Loading');
              spinner.textContent = 'Loading…';
              li.appendChild(spinner);

              // Session mode: send cookie; SW handles bearer mode automatically.
              const lazyTreeOpts = AUTH_MODE === 'session' ? { credentials: 'same-origin' } : {};
              fetch('/api/files/tree?dir=' + encodeURIComponent(entry.path) + '&depth=1', lazyTreeOpts)
                .then((r) => r.ok ? r.json() : null)
                .then((data) => {
                  li.removeChild(spinner);
                  loading = false;
                  if (!data) {
                    // S3: surface fetch error — revert toggle and show inline note.
                    expanded = false;
                    li.setAttribute('aria-expanded', 'false');
                    iconSpan.textContent = '▶';
                    const errNote = document.createElement('div');
                    errNote.className = 'ide-tree-error';
                    errNote.textContent = 'Failed to load ' + entry.name;
                    li.appendChild(errNote);
                    return;
                  }
                  childUl = buildTreeList(data.entries || [], depth + 1);
                  li.appendChild(childUl);
                  if (data.truncated) {
                    const truncNote = document.createElement('div');
                    truncNote.className = 'ide-tree-truncated';
                    truncNote.style.paddingLeft = ((depth + 1) * 12) + 'px';
                    truncNote.textContent = '… (truncated)';
                    li.appendChild(truncNote);
                  }
                })
                .catch(() => {
                  if (li.contains(spinner)) li.removeChild(spinner);
                  loading = false;
                  // S3: network error — revert toggle and show inline note.
                  expanded = false;
                  li.setAttribute('aria-expanded', 'false');
                  iconSpan.textContent = '▶';
                  const errNote = document.createElement('div');
                  errNote.className = 'ide-tree-error';
                  errNote.textContent = 'Network error loading ' + entry.name;
                  li.appendChild(errNote);
                });
            }
          } else {
            if (childUl) childUl.style.display = 'none';
          }
        });
      } else {
        // File entry.
        const btn = document.createElement('button');
        btn.type = 'button';
        btn.className = 'ide-tree-file';
        // B2: esc() on server-supplied entry.name in aria-label.
        btn.setAttribute('aria-label', 'File ' + esc(entry.name));
        btn.title = entry.name; // tooltip for truncated names

        const iconSpan = document.createElement('span');
        iconSpan.className = 'ide-tree-icon';
        iconSpan.setAttribute('aria-hidden', 'true');
        iconSpan.textContent = '📄'; // 📄

        const nameSpan = document.createElement('span');
        nameSpan.className = 'ide-tree-name';
        nameSpan.textContent = entry.name;

        btn.appendChild(iconSpan);
        btn.appendChild(nameSpan);

        btn.addEventListener('click', () => {
          // Tentatively mark selected; openIdeFile will revert on error.
          const treeEl = document.getElementById('ide-tree-root');
          if (treeEl) {
            treeEl.querySelectorAll('.ide-tree-file.selected').forEach((el) => el.classList.remove('selected'));
          }
          btn.classList.add('selected');

          openIdeFile(entry.path, entry.name, btn);
        });

        li.appendChild(btn);
      }

      ul.appendChild(li);
    }

    return ul;
  }

  // openIdeFile fetches a file's content and either opens it in Monaco or
  // shows an inline editor notice on any error condition.
  //
  // selectedBtn (optional) is the tree button that triggered this open.  On any
  // error path the .selected class is removed so the tree selection does not
  // falsely imply the file is open in the editor.
  //
  // Error cases handled:
  //   413 — file exceeds the 5 MiB content cap
  //   403 — secret / access-denied
  //   404 — file disappeared between tree render and click
  //   5xx / other — generic server error with status code
  //   network  — fetch() rejection (offline, CORS, etc.)
  //   base64   — binary file; preview not available
  // All cases call showIdeEditorNotice (never throw, never blank the pane).
  function openIdeFile(relpath, name, selectedBtn) {
    function onError(msg) {
      showIdeEditorNotice(msg);
      // Revert the tentative tree selection so it doesn't misrepresent state.
      if (selectedBtn) selectedBtn.classList.remove('selected');
    }

    // Session mode: send cookie so the server can authenticate the request.
    const contentOpts = AUTH_MODE === 'session' ? { credentials: 'same-origin' } : {};
    fetch('/api/files/content?path=' + encodeURIComponent(relpath), contentOpts)
      .then((r) => {
        if (r.status === 413) {
          onError('File too large to preview (max 5 MiB): ' + name);
          return null;
        }
        if (r.status === 403) {
          onError('Access denied for: ' + name);
          return null;
        }
        if (r.status === 404) {
          onError('File not found: ' + name);
          return null;
        }
        if (!r.ok) {
          onError('Failed to load file (' + String(r.status) + '): ' + name);
          return null;
        }
        return r.json();
      })
      .then((data) => {
        if (!data) return;

        if (data.encoding === 'base64') {
          // Binary file: show notice but do not clear the editor content.
          onError('Binary file — preview not available: ' + (data.path || name));
          return;
        }

        openFileInEditor(data.path || relpath, data.content || '', data.language || 'plaintext', data.version || '');
      })
      .catch(() => {
        onError('Network error loading: ' + name);
      });
  }

  // B1: showIdeEditorNotice uses textContent — no innerHTML, no caller-escape
  // convention.  msg is plain text; all strings at call sites are either static
  // literals or field values that do NOT contain markup (path, name, status).
  function showIdeEditorNotice(msg) {
    const notice = document.getElementById('ide-editor-notice');
    if (!notice) return;
    notice.textContent = msg;
    notice.style.display = '';
    // Clear the open path header to avoid a stale filename beside the error.
    const pathEl = document.getElementById('ide-open-path');
    if (pathEl) pathEl.textContent = '';
  }

  // ---- Embedded chat pane for IDE --------------------------------------------
  //
  // Reuses buildPaneElement() + wirePaneEvents() exactly as the Chat tab does.
  // No second SSE reader: the shared startChatSSE() instance handles demux.
  //
  // The IDE pane is intentionally kept OUT of the shared chatPanes map and
  // savePaneState():
  //   - It does not consume from the MAX_PANES (6) budget.
  //   - It does not persist to localStorage, so it never reappears as a
  //     ghost pane in the Chat tab's pane rail on next page load.
  //   - Event wiring (send, cancel, close, share) is still fully functional;
  //     the SSE demux routes by sessionId, not by chatPanes membership.

  function mountIdeChatPane() {
    const slot = document.getElementById('ide-chat-slot');
    if (!slot) return;

    // Create one pane — NOT added to chatPanes or savePaneState.
    const p = makePane(newPaneId(), newConversationId());

    // Mark as IDE-embedded so Chat-tab rendering skips this pane.  It stays
    // in chatPanes for the duration of this page load so the SSE demux
    // (sessionToPaneId → chatPanes.get()) can route events to it correctly.
    // We do NOT call savePaneState() — the pane is session-only.
    p.ideEmbedded = true;
    chatPanes.set(p.id, p);

    const paneEl = buildPaneElement(p.id);
    slot.appendChild(paneEl);

    // Load transcript for this pane.
    loadTranscriptForPane(p.id);
  }

  // =========================================================================
  // ---- 8. Page rendering ------------------------------------------------------

  // ---- Logout (session mode) -------------------------------------------------
  //
  // Sends POST /logout with the CSRF token, then redirects to /login.
  // No-op in bearer mode (loopback sessions don't use cookie-based logout).

  function doLogout() {
    if (AUTH_MODE !== 'session') return;
    const btn = document.getElementById('console-logout-btn');
    if (btn) {
      btn.disabled = true;
      btn.textContent = 'Signing out…';
    }
    const headers = { 'Content-Type': 'application/json' };
    if (CSRF_TOKEN) headers['X-CSRF-Token'] = CSRF_TOKEN;
    fetch('/logout', {
      method: 'POST',
      credentials: 'same-origin',
      headers: headers,
      body: '{}',
    }).finally(function() {
      // Redirect regardless of response status.
      window.location.href = '/login';
    });
  }

  // wireUsersPanelButtons connects the "Add user" and "Change my password"
  // toolbar buttons in the Users panel.  Called after the DOM is built and
  // after /api/account resolves (so accountIdentity is populated).
  function wireUsersPanelButtons() {
    const addBtn = document.getElementById('users-add-btn');
    if (addBtn) addBtn.addEventListener('click', openAddUserModal);
    const chPwBtn = document.getElementById('users-change-pw-btn');
    if (chPwBtn) chPwBtn.addEventListener('click', openChangePasswordModal);
  }

  function buildPage() {
    const app = document.getElementById('app');
    app.innerHTML = `
      <div class="tab-bar">
        <span class="tab-bar-brand">yakOS</span>
        <div id="tab-bar-tabs"></div>
        <nav class="theme-picker" aria-label="Console theme">
          <span class="theme-picker-label" aria-hidden="true">Theme</span>
          <button class="theme-btn" type="button" data-theme-value="ops"
            aria-pressed="false" aria-label="OPS theme — terminal HUD">OPS</button>
          <button class="theme-btn" type="button" data-theme-value="fluid"
            aria-pressed="false" aria-label="FLUID theme — glass interface">FLUID</button>
          <button class="theme-btn" type="button" data-theme-value="og"
            aria-pressed="false" aria-label="OG theme — raw brand">OG</button>
          <button class="theme-btn" type="button" data-theme-value="light"
            aria-pressed="false" aria-label="LIGHT theme — light mode">LIGHT</button>
        </nav>
        <div id="console-operator-chrome" class="console-operator-chrome" aria-label="Signed-in operator"></div>
      </div>
      <div class="tab-content">
        <div id="panel-overview" class="tab-panel active">
          <div class="overview-layout">
            <aside class="now-panel" aria-label="Current activity">
              <div class="now-section">
                <h2 class="subsection-title">In-flight dispatches</h2>
                <div id="now-dispatches" aria-live="polite">
                  <p class="empty-state">No active dispatches</p>
                </div>
              </div>
              <div class="now-section">
                <h2 class="subsection-title">Operators online</h2>
                <div id="now-presence" class="presence-list" aria-live="polite">
                  <p class="empty-state">No operators online</p>
                </div>
              </div>
            </aside>
            <section class="feed-panel" aria-label="Activity feed">
              <h2 class="subsection-title">Activity feed</h2>
              <div class="feed-filters" role="search" aria-label="Filter activity feed">
                <input id="filter-topic" class="filter-input" type="text"
                  placeholder="Filter by topic…" aria-label="Filter by topic"
                  autocomplete="off" />
                <input id="filter-op" class="filter-input" type="text"
                  placeholder="Filter by operator…" aria-label="Filter by operator ID"
                  autocomplete="off" />
                <button id="filter-clear-btn" class="filter-btn" type="button"
                  aria-label="Clear filters">Clear</button>
              </div>
              <p id="feed-cap-notice" class="feed-cap-notice" style="display:none"
                 aria-live="polite">
                Feed capped at ${FEED_CAP} events. Oldest events dropped.
              </p>
              <div id="feed-list" class="feed-list" aria-live="polite" aria-label="Activity events">
                <p class="empty-state">No events yet</p>
              </div>
            </section>
          </div>
        </div>
        <div id="panel-repl" class="tab-panel">
          <div class="repl-layout">
            <aside class="repl-sidebar" aria-label="Action items">
              <h2 class="subsection-title">Action items</h2>
              <p class="repl-sidebar-hint">Read-only mirror of TODO + IN PROGRESS from Kanban</p>
              <div id="repl-action-items" aria-live="polite">
                <p class="empty-state">Switch to this tab to load</p>
              </div>
            </aside>
            <section class="repl-main" aria-label="Fleet — active dispatches">
              <h2 class="subsection-title">In-flight dispatches</h2>
              <div id="repl-fleet-list" aria-live="polite">
                <p class="empty-state">No active dispatches</p>
              </div>
            </section>
          </div>
        </div>
        <div id="panel-kanban" class="tab-panel">
          <!-- First-party same-origin content; isolation boundary is the auth edge, not an escapable sandbox. -->
          <iframe title="Kanban board"></iframe>
        </div>
        <div id="panel-cost" class="tab-panel">
          <!-- First-party same-origin content; isolation boundary is the auth edge, not an escapable sandbox. -->
          <iframe title="Cost dashboard"></iframe>
        </div>
        <div id="panel-perf" class="tab-panel">
          <!-- First-party same-origin content; isolation boundary is the auth edge, not an escapable sandbox. -->
          <iframe title="Performance dashboard"></iframe>
        </div>
        <div id="panel-chat" class="tab-panel">
          <div class="chat-loading">
            <p class="empty-state">Initializing Chat…</p>
          </div>
        </div>
        <div id="panel-ide" class="tab-panel">
          <div class="ide-loading">
            <p class="empty-state">Initializing IDE…</p>
          </div>
        </div>
        <div id="panel-flows" class="tab-panel">
          <div class="flows-loading">
            <p class="empty-state">Initializing Flows…</p>
          </div>
        </div>
        <div id="panel-users" class="tab-panel">
          <div class="users-toolbar">
            <h2 class="users-toolbar-title">Users</h2>
            <span class="users-status" role="status" aria-live="polite"></span>
            <button class="users-change-pw-btn" type="button" id="users-change-pw-btn"
              aria-label="Change my password">Change my password</button>
            <button class="users-add-btn" type="button" id="users-add-btn">+ Add user</button>
          </div>
          <div class="users-table-wrap" role="region" aria-label="User accounts table">
            <p class="users-loading">Loading users…</p>
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

    // Wire theme picker buttons.
    // applyTheme() updates aria-pressed states and persists to localStorage.
    // We also immediately sync aria-pressed to reflect current theme.
    (function () {
      var currentTheme = document.documentElement.getAttribute('data-theme') || 'og';
      var btns = document.querySelectorAll('.theme-btn');
      for (var i = 0; i < btns.length; i++) {
        (function (btn) {
          var v = btn.getAttribute('data-theme-value');
          btn.setAttribute('aria-pressed', v === currentTheme ? 'true' : 'false');
          btn.addEventListener('click', function () { applyTheme(v); });
        }(btns[i]));
      }
    }());

    // In bearer mode, TOKEN is required; show auth-error if missing.
    // In session mode, we always proceed (auth is via cookie — if it expired
    // the first API call will return 401 and redirect to /login).
    if (AUTH_MODE === 'bearer' && !TOKEN) {
      document.getElementById('auth-error').classList.add('visible');
      return;
    }

    // Fetch /api/account on boot to populate accountIdentity.
    // Used for:
    //   1. Operator chrome display (session mode: username + logout button)
    //   2. Admin-only tab gating: Users tab is only rendered when role==='admin'
    //   3. Loopback bearer users are also RoleAdmin → Users tab shows there too
    //
    // apiFetch handles auth mode transparently (Bearer in loopback, cookie in
    // session).  On failure we fall back gracefully: no Users tab shown.
    apiFetch('GET', '/api/account').then(function(r) {
      return (r && r.ok) ? r.json() : null;
    }).then(function(data) {
      // Populate accountIdentity — used by isAdmin() / renderTabs().
      if (data) {
        accountIdentity = {
          operatorId: data.operatorId || '',
          role:       data.role       || '',
          authMethod: data.authMethod || '',
        };
      }

      // Session-mode operator chrome: show display name + logout button.
      if (AUTH_MODE === 'session') {
        const chromeEl = document.getElementById('console-operator-chrome');
        if (chromeEl) {
          const displayName = (data && data.operatorId) || '';
          const labelText = displayName ? esc(displayName) : 'Signed in';
          chromeEl.innerHTML =
            '<span class="console-op-label" aria-label="Signed in as ' + esc(displayName || 'operator') + '">' +
              labelText +
            '</span>' +
            '<button id="console-logout-btn" class="console-logout-btn" type="button" ' +
              'aria-label="Sign out">Sign out</button>';
          const logoutBtn = document.getElementById('console-logout-btn');
          if (logoutBtn) logoutBtn.addEventListener('click', doLogout);
        }
      }

      // Re-render tabs now that accountIdentity is known — this makes the
      // Users tab appear for admins without requiring a page reload.
      renderTabs();

      // Wire Users panel action buttons now that the panel is in the DOM.
      wireUsersPanelButtons();
    }).catch(function() {
      // /api/account unavailable (e.g. daemon not started with user store).
      // Session mode: show a minimal logout button.
      if (AUTH_MODE === 'session') {
        const chromeEl = document.getElementById('console-operator-chrome');
        if (chromeEl) {
          chromeEl.innerHTML =
            '<button id="console-logout-btn" class="console-logout-btn" type="button" ' +
              'aria-label="Sign out">Sign out</button>';
          const logoutBtn = document.getElementById('console-logout-btn');
          if (logoutBtn) logoutBtn.addEventListener('click', doLogout);
        }
      }
      // Tab bar already rendered without Users tab (accountIdentity is null).
    });

    // Wire filter inputs.
    const topicIn = document.getElementById('filter-topic');
    const opIn = document.getElementById('filter-op');
    const clearBtn = document.getElementById('filter-clear-btn');
    if (topicIn) topicIn.addEventListener('input', applyFeedFilter);
    if (opIn) opIn.addEventListener('input', applyFeedFilter);
    if (clearBtn) {
      clearBtn.addEventListener('click', () => {
        if (topicIn) topicIn.value = '';
        if (opIn) opIn.value = '';
        applyFeedFilter();
      });
    }

    // Elapsed timer: re-render in-flight dispatches every 5s.
    setInterval(renderNow, 5000);

    connectWS();
  }

  // ---- 9. Utility helpers ------------------------------------------------------

  function esc(s) {
    return String(s)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;');
  }

  // escLines escapes a multi-line string and converts newlines to <br>.
  // Safe for use in innerHTML — every character is escaped first.
  function escLines(s) {
    return esc(s).replace(/\n/g, '<br>');
  }

  function formatTime(ts) {
    if (!ts) return '';
    try {
      const d = new Date(ts);
      return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
    } catch { return String(ts); }
  }

  // feedSummary returns a plain-text summary string for a bus event.
  // IMPORTANT: output MUST be escaped by the caller before inserting into HTML.
  // Current sinks (renderFeed) escape via esc(); do not add an unescaped sink.
  function feedSummary(topic, payload) {
    switch (topic) {
      case 'dispatch.started':
        return (payload.agent || '?') + ' started on ' + shortPath(payload.project || '?');
      case 'dispatch.finished':
        return (payload.agent || '?') + ' finished (exit=' + (payload.exit_code !== undefined ? payload.exit_code : '?') + ')';
      case 'fleet.started':
        return 'fleet: ' + (payload.agent || '?') + ' started [' + (payload.session_id || '?').slice(0, 8) + ']';
      case 'fleet.finished':
        return 'fleet: ' + (payload.agent || '?') + ' ' + (payload.status || 'finished') + ' (exit=' + (payload.exit_code !== undefined ? payload.exit_code : '?') + ')';
      case 'presence':
        return (payload.display_name || payload.operator_id || 'anon') + ' is ' + (payload.status || 'unknown');
      case 'workflow.run.started':
        return 'run ' + (payload.run_id || '?') + ' started (' + (payload.workflow || '?') + ')';
      case 'workflow.run.finished':
        return 'run ' + (payload.run_id || '?') + ' ' + (payload.status || '?') + ' (' + (payload.workflow || '?') + ')';
      case 'workflow.node.started':
        return 'node ' + (payload.node_id || '?') + ' started [' + (payload.workflow || '?') + ']';
      case 'workflow.node.finished':
        return 'node ' + (payload.node_id || '?') + ' ' + (payload.status || '?') + ' [' + (payload.workflow || '?') + ']';
      case 'workflow.node.truncated':
        return 'node ' + (payload.node_id || '?') + ' output truncated';
      default:
        return topic;
    }
  }

  function shortPath(p) {
    if (!p) return '?';
    const parts = p.replace(/\\/g, '/').split('/');
    return parts[parts.length - 1] || p;
  }

  // ---- 10. Init ----------------------------------------------------------------

  document.addEventListener('DOMContentLoaded', buildPage);

})();
