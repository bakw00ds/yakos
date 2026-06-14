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
  var ideQueuedOpen = null;
  var ideMessageHandlerRegistered = false;
  // Edit/save state (hoisted for TDZ safety).
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

  // ---- 1. Token extraction ---------------------------------------------------

  let TOKEN = '';

  function extractAndStripToken() {
    const hash = location.hash;
    if (hash && hash.startsWith('#token=')) {
      TOKEN = hash.slice(7);
      history.replaceState(null, '', location.pathname + location.search);
    }
  }

  extractAndStripToken();

  // ---- 2. Service Worker registration + ready promise ------------------------
  // Gate iframe loading on navigator.serviceWorker.ready (not a boolean flag)
  // to fix the activation-race where the first tab click fires before the SW
  // activates and the iframe gets a 401.

  let swReadyPromise = Promise.resolve(false); // resolves to true when SW ready

  function registerServiceWorker() {
    if (!TOKEN) return;
    if (!('serviceWorker' in navigator)) {
      console.warn('[console] Service Worker unavailable — iframe auth will fail');
      return;
    }

    swReadyPromise = navigator.serviceWorker.register('/sw.js', { scope: '/' })
      .then((reg) => {
        // Deliver token to whatever state the SW is in.
        const target = reg.installing || reg.waiting || reg.active;
        if (target) {
          target.postMessage({ type: 'SET_TOKEN', token: TOKEN });
        }
        navigator.serviceWorker.addEventListener('controllerchange', () => {
          if (navigator.serviceWorker.controller) {
            navigator.serviceWorker.controller.postMessage({ type: 'SET_TOKEN', token: TOKEN });
          }
        });
        // Wait for the SW to actually control this page.
        return navigator.serviceWorker.ready;
      })
      .then(() => {
        // Deliver token to the now-active controller.
        if (navigator.serviceWorker.controller) {
          navigator.serviceWorker.controller.postMessage({ type: 'SET_TOKEN', token: TOKEN });
        }
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
    { id: 'kanban',    label: 'Kanban',       src: '/kanban/', phase: null },
    { id: 'cost',      label: 'Cost',         src: '/cost/',   phase: null },
    { id: 'perf',      label: 'Performance',  src: '/perf/',   phase: null },
    { id: 'chat',      label: 'Chat',         src: null,       phase: null },
    { id: 'ide',       label: 'IDE',          src: null,       phase: null },
    { id: 'flows',     label: 'Flows',        src: null,       phase: null },
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
          if (!ready) {
            document.getElementById('auth-error').classList.add('visible');
            return;
          }
          // Append the bearer token as a URL fragment so the dashboard's
          // client-side getToken() (metricsdash/perfdash read #token=<hex>)
          // authenticates without an extra round-trip.  Fragments are
          // NEVER sent to the server — they exist only in the browser and
          // cannot appear in server logs — so this is safe.  Kanban does
          // not use a fragment gate and ignores the extra fragment harmlessly.
          iframe.src = tab.src + '#token=' + TOKEN;
        });
      }
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

  // ---- 5. WebSocket (Phase 2.5 subprotocol auth) ------------------------------

  let ws = null;
  let wsRetryMs = 1000;
  let wsLastSeq = 0; // last received event sequence number; used for ?since= replay

  function connectWS() {
    if (!TOKEN) return;
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    // Append ?since=<seq> so the server replays missed events on reconnect,
    // preventing ghost in-flight dispatches after a disconnect.
    const since = wsLastSeq > 0 ? '?since=' + wsLastSeq : '';
    const url = proto + '//' + location.host + '/v1/events' + since;

    // Phase 2.5: Sec-WebSocket-Protocol bearer auth.
    // The token is sent as the second protocol value; the server echoes
    // "yakos-bearer" as the accepted protocol.
    ws = new WebSocket(url, ['yakos-bearer', TOKEN]);
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
    };
  }

  // ---- SSE: single fetch-based reader for this operator ----------------------
  //
  // One long-lived GET /api/chat/stream?operatorId=… per browser tab.
  // The SW injects Authorization: Bearer so we never put the token in the URL.
  // Events are demuxed by session_id into the correct pane.

  let chatSSERetryMs = 1000;
  let chatSSERetryTimer = null;

  function startChatSSE() {
    if (!TOKEN) return;
    if (chatSSEAbort) chatSSEAbort.abort();
    chatSSEAbort = new AbortController();
    const opId = getChatOperatorId();
    // operatorId goes in the query string (attribution, not auth).
    const url = '/api/chat/stream?operatorId=' + encodeURIComponent(opId);

    fetch(url, {
      method: 'GET',
      signal: chatSSEAbort.signal,
      // The SW will inject Authorization: Bearer <token> automatically.
      // No credentials: 'include' — we rely on the SW, not cookies.
    }).then((resp) => {
      if (!resp.ok) {
        console.warn('[chat SSE] connect failed:', resp.status);
        // 401/403 are permanent auth failures (misconfigured SW or expired
        // token); retrying every 30s would hammer the server pointlessly.
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
    // ev: {session_id, type, text?, exit_code?, duration_s?, total_cost_usd?, model_resolved?, ts}
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

  // ---- Chat tab init ---------------------------------------------------------

  let chatTabInitialized = false;

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
    chatTabInitialized = true;
  }

  function initChatTab() {
    if (chatTabInitialized) return;

    // S4: use the shared boot helper so the sequence is defined once and both
    // initChatTab + initIdeTab stay in sync.  bootChatInfrastructure() is
    // idempotent; calling it here sets chatTabInitialized = true.
    bootChatInfrastructure();

    renderChatLayout();

    // Load transcripts for each pane asynchronously.
    for (const [paneId] of chatPanes) {
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
    for (const [paneId] of chatPanes) {
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
          'rows="3" placeholder="Task prompt…" ' +
          'aria-label="Task prompt for this pane"></textarea>' +
        '<button class="chat-send-btn" id="pane-send-' + esc(paneId) + '" type="button" ' +
          'aria-label="Send task">Send</button>' +
      '</div>';

    // Wire events after inserting.
    requestAnimationFrame(() => wirePaneEvents(paneId));

    return el;
  }

  function buildPaneHeaderHTML(paneId) {
    const pane = chatPanes.get(paneId);
    if (!pane) return '';

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
    }

    return el;
  }

  // ---- Transcript restore ----------------------------------------------------

  function loadTranscriptForPane(paneId) {
    const pane = chatPanes.get(paneId);
    if (!pane || !pane.conversationId) return;
    const opId = getChatOperatorId();
    const url = '/api/chat/transcript?conversationId=' + encodeURIComponent(pane.conversationId) +
                '&operatorId=' + encodeURIComponent(opId);

    fetch(url).then((resp) => {
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

    const body = JSON.stringify({
      runtime: pane.runtime,
      model: pane.model,
      agent: pane.agent,
      task: task,
      sessionId: sessionId,
      operatorId: getChatOperatorId(),
      conversationId: pane.conversationId,
    });

    fetch('/api/chat/dispatch', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: body,
    }).then((resp) => {
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

    fetch('/api/chat/cancel', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ sessionId, operatorId: opId }),
    }).catch(() => {
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

    fetch('/api/chat/share', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        sessionId: pane.activeSessionId,
        operatorId: opId,
        shared: newShared,
      }),
    }).then((resp) => {
      if (!resp.ok) return;
      if (shareBtn) {
        shareBtn.setAttribute('aria-pressed', String(newShared));
        shareBtn.title = newShared ? 'Stop sharing pane' : 'Share pane';
        shareBtn.classList.toggle('pane-share-active', newShared);
      }
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
  };

  // ---- Flows init -----------------------------------------------------------

  function initFlowsTab() {
    if (flowsTabInitialized) return;
    flowsTabInitialized = true;
    renderFlowsLayout();
    loadFlowsWorkflowList();
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
      if (flowsState.activeRunId === runId) {
        renderFlowsRunPanel();
        announceFlows('Run ' + runId + ' ' + (payload.status || 'finished'));
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

  function apiFetch(method, path, body) {
    const opts = {
      method,
      headers: { 'Authorization': 'Bearer ' + TOKEN },
    };
    if (body !== undefined) {
      opts.headers['Content-Type'] = 'application/json';
      opts.body = JSON.stringify(body);
    }
    return fetch(path, opts);
  }

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
            '<button id="flows-save-btn" class="flows-btn" type="button" ' +
              'aria-label="Save workflow YAML">Save</button>' +
            '<button id="flows-run-btn" class="flows-btn flows-run-btn" type="button" ' +
              'aria-label="Run this workflow">Run</button>' +
            '<button id="flows-rerun-btn" class="flows-btn" type="button" ' +
              'aria-label="Re-run failed and downstream nodes (reuse completed outputs)" ' +
              'title="Resume: re-run failed/skipped, reuse completed node outputs" ' +
              'style="display:none">Re-run failed</button>' +
            '<span id="flows-run-status" class="flows-run-status" aria-live="polite"></span>' +
          '</div>' +
          '<div id="flows-save-error" class="flows-save-error" role="alert" style="display:none"></div>' +
          // Canvas + YAML side by side; only one shown at a time
          '<div class="flows-content">' +
            '<div id="flows-canvas-wrap" class="flows-canvas-wrap">' +
              '<div class="flows-canvas-readonly-note" role="note" aria-label="Canvas note">' +
                'Canvas is read-only — click a node to view output. ' +
                'To author or edit a workflow, switch to YAML view.' +
              '</div>' +
              '<div id="flows-canvas" class="flows-canvas" role="img" aria-label="Workflow DAG canvas"></div>' +
            '</div>' +
            '<div id="flows-yaml-wrap" class="flows-yaml-wrap" style="display:none">' +
              '<label for="flows-yaml-editor" class="sr-only">Workflow YAML editor</label>' +
              '<textarea id="flows-yaml-editor" class="flows-yaml-editor" ' +
                'spellcheck="false" autocorrect="off" autocapitalize="off" ' +
                'aria-label="Workflow YAML — edit here, then click Save"></textarea>' +
            '</div>' +
          '</div>' +
          // Node output panel (shown when a node is selected)
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

    // Wire node panel close button.
    document.getElementById('flows-node-close').addEventListener('click', () => {
      flowsState.selectedNodeId = null;
      document.getElementById('flows-node-panel').style.display = 'none';
    });

    // Wire New workflow button.
    document.getElementById('flows-new-btn').addEventListener('click', () => {
      createNewFlowsWorkflow();
    });

    renderFlowsWorkflowList();
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

  function openModal(opts) {
    var title       = opts.title       || '';
    var confirmText = opts.confirmText || 'OK';
    var cancelText  = opts.cancelText  || 'Cancel';
    var dangerous   = !!opts.dangerous;

    // Build modal DOM.
    var overlay = document.createElement('div');
    overlay.className = 'modal-overlay';
    overlay.setAttribute('role', 'dialog');
    overlay.setAttribute('aria-modal', 'true');
    overlay.setAttribute('aria-label', title);

    var box = document.createElement('div');
    box.className = 'modal-box';

    var heading = document.createElement('h2');
    heading.className = 'modal-title';
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
          'button, [href], input, select, textarea, [tabindex]:not([tabindex=”-1”])'
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
      '<label for=”modal-wf-name” class=”modal-field-label”>' +
        'Workflow name' +
      '</label>' +
      '<input id=”modal-wf-name” class=”modal-input” type=”text” ' +
        'placeholder=”e.g. my-flow” autocomplete=”off” spellcheck=”false” ' +
        'aria-describedby=”modal-wf-name-hint modal-wf-name-err”>' +
      '<p id=”modal-wf-name-hint” class=”modal-field-hint”>' +
        'Lowercase letters, digits, hyphens. Starts with a letter or digit. Max 64 chars.' +
      '</p>' +
      '<p id=”modal-wf-name-err” class=”modal-field-error” role=”alert” style=”display:none”></p>';

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
      '    prompt: “Describe the first step.”',
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

    // Switch to YAML view so the operator can review/edit before saving.
    switchFlowsView('yaml');

    var yamlEditor = document.getElementById('flows-yaml-editor');
    if (yamlEditor) yamlEditor.value = starterYAML;

    renderFlowsWorkflowList();
    renderFlowsEditor();
    updateFlowsToolbarState();
  }

  function deleteFlowsWorkflow(name) {
    openModal({
      title:       'Delete workflow',
      body:        '<p>Delete <strong>' + esc(name) + '</strong>?</p>' +
                   '<p class=”modal-field-hint” style=”margin-top:8px”>' +
                     'This removes the workflow definition. ' +
                     'Existing run history is not deleted.' +
                   '</p>' +
                   '<p id=”modal-del-err” class=”modal-field-error” role=”alert” style=”display:none”></p>',
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
            overlay._closeModal();
            loadFlowsWorkflowList();
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
            renderFlowsEditor();
            renderFlowsCanvas();
          }
          loadFlowsWorkflowList();
          announceFlows('Workflow “' + name + '” deleted');
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

    if (!isCanvas) {
      // Sync YAML editor with current state.
      const yamlEditor = document.getElementById('flows-yaml-editor');
      if (yamlEditor) yamlEditor.value = flowsState.yaml;
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

    const hasWorkflow = !!flowsState.selectedName;

    if (saveBtn) saveBtn.disabled = !hasWorkflow;
    if (runBtn) runBtn.disabled = !hasWorkflow;

    // Show Re-run button only if there's an active run with failures.
    if (rerunBtn) {
      const activeSnap = flowsState.activeRunId ? flowsState.runs.get(flowsState.activeRunId) : null;
      const hasFailed = activeSnap && activeSnap.status === 'failed';
      rerunBtn.style.display = (hasWorkflow && hasFailed) ? '' : 'none';
    }

    // Run status indicator.
    const statusEl = document.getElementById('flows-run-status');
    if (statusEl) {
      const activeSnap = flowsState.activeRunId ? flowsState.runs.get(flowsState.activeRunId) : null;
      if (activeSnap) {
        statusEl.textContent = runStatusLabel(activeSnap.status);
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
      const item = document.createElement('button');
      item.type = 'button';
      item.className = 'flows-run-item' + (snap.runId === flowsState.activeRunId ? ' active' : '');
      item.setAttribute('aria-current', snap.runId === flowsState.activeRunId ? 'true' : 'false');

      const icon = runStatusIcon(snap.status);
      const label = runStatusLabel(snap.status);
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
      listEl.appendChild(item);
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

  // ---- DAG canvas (minimal SVG) -----------------------------------------------
  //
  // Layout algorithm:
  //   1. Kahn topo-sort → assign each node to a column (layer) = max(layer of needs) + 1
  //   2. Nodes in the same column are stacked vertically.
  //   3. Edges are drawn as SVG lines from the right edge of the source node to
  //      the left edge of the target node.
  //
  // Status rendering: icon + text label in each node box. NOT color alone.
  // Node boxes are <button> elements (keyboard-operable, focusable).
  // Accessibility: aria-label per node includes status + agent.

  const SVG_NODE_W = 140;
  const SVG_NODE_H = 56;
  const SVG_COL_GAP = 80;
  const SVG_ROW_GAP = 20;
  const SVG_PAD = 24;

  function renderFlowsCanvas() {
    const canvasEl = document.getElementById('flows-canvas');
    if (!canvasEl) return;
    canvasEl.innerHTML = '';

    const wf = flowsState.workflow;
    if (!wf || !wf.nodes || wf.nodes.length === 0) {
      canvasEl.innerHTML = '<p class="empty-state" style="padding:24px">Select a workflow to view its DAG</p>';
      return;
    }

    const activeSnap = flowsState.activeRunId ? flowsState.runs.get(flowsState.activeRunId) : null;

    // 1. Build adjacency and in-degree for Kahn.
    const nodeById = {};
    const inDegree = {};
    const successors = {}; // id → [successor ids]
    for (const n of wf.nodes) {
      nodeById[n.id] = n;
      inDegree[n.id] = (inDegree[n.id] || 0);
      successors[n.id] = successors[n.id] || [];
    }
    for (const n of wf.nodes) {
      for (const dep of (n.needs || [])) {
        inDegree[n.id] = (inDegree[n.id] || 0) + 1;
        successors[dep] = successors[dep] || [];
        successors[dep].push(n.id);
      }
    }

    // 2. Assign layers via Kahn BFS: layer[id] = max(layer of needs) + 1, min 0.
    const layer = {};
    const queue = [];
    for (const n of wf.nodes) {
      if ((inDegree[n.id] || 0) === 0) {
        layer[n.id] = 0;
        queue.push(n.id);
      }
    }
    // BFS to assign layers.
    const remaining = Object.assign({}, inDegree);
    let qi = 0;
    while (qi < queue.length) {
      const cur = queue[qi++];
      for (const succ of (successors[cur] || [])) {
        const newLayer = (layer[cur] || 0) + 1;
        if (layer[succ] === undefined || layer[succ] < newLayer) {
          layer[succ] = newLayer;
        }
        remaining[succ] = (remaining[succ] || 1) - 1;
        if (remaining[succ] <= 0) {
          queue.push(succ);
        }
      }
    }

    // 3. Bucket nodes by layer.
    const maxLayer = Math.max(...Object.values(layer));
    const columns = [];
    for (let i = 0; i <= maxLayer; i++) columns.push([]);
    for (const n of wf.nodes) {
      const l = layer[n.id] || 0;
      columns[l].push(n.id);
    }

    // 4. Compute node positions.
    const pos = {}; // id → {x, y, cx, cy} (top-left + center)
    const colX = [];
    let xCursor = SVG_PAD;
    for (let c = 0; c <= maxLayer; c++) {
      colX[c] = xCursor;
      xCursor += SVG_NODE_W + SVG_COL_GAP;
    }
    const maxColSize = Math.max(...columns.map((c) => c.length));
    const totalH = maxColSize * (SVG_NODE_H + SVG_ROW_GAP) - SVG_ROW_GAP + SVG_PAD * 2;
    const totalW = xCursor - SVG_COL_GAP + SVG_PAD;

    // S2: guard against non-finite dimensions (defensive — cycles are rejected
    // upstream by Validate, but guard here so a malformed snapshot does not
    // produce a broken SVG with width="-Infinity" or NaN attributes.
    if (!isFinite(totalW) || !isFinite(totalH) || totalW <= 0 || totalH <= 0) {
      canvasEl.innerHTML = '<p class="empty-state" style="padding:24px">Unable to render canvas (invalid graph dimensions)</p>';
      return;
    }

    for (let c = 0; c <= maxLayer; c++) {
      const colNodes = columns[c];
      const colTotalH = colNodes.length * (SVG_NODE_H + SVG_ROW_GAP) - SVG_ROW_GAP;
      const startY = SVG_PAD + (totalH - SVG_PAD * 2 - colTotalH) / 2;
      for (let r = 0; r < colNodes.length; r++) {
        const id = colNodes[r];
        const x = colX[c];
        const y = startY + r * (SVG_NODE_H + SVG_ROW_GAP);
        pos[id] = { x, y, cx: x + SVG_NODE_W / 2, cy: y + SVG_NODE_H / 2 };
      }
    }

    // 5. Build SVG.
    const svgNS = 'http://www.w3.org/2000/svg';
    const svg = document.createElementNS(svgNS, 'svg');
    svg.setAttribute('width', String(totalW));
    svg.setAttribute('height', String(totalH));
    svg.setAttribute('role', 'presentation');
    svg.setAttribute('aria-hidden', 'true'); // canvas is decorative; nodes have their own buttons

    // Draw edges first (below nodes).
    for (const n of wf.nodes) {
      for (const dep of (n.needs || [])) {
        if (!pos[dep] || !pos[n.id]) continue;
        const x1 = pos[dep].x + SVG_NODE_W;
        const y1 = pos[dep].cy;
        const x2 = pos[n.id].x;
        const y2 = pos[n.id].cy;
        const mx = (x1 + x2) / 2;
        const line = document.createElementNS(svgNS, 'path');
        const d = 'M ' + x1 + ' ' + y1 + ' C ' + mx + ' ' + y1 + ' ' + mx + ' ' + y2 + ' ' + x2 + ' ' + y2;
        line.setAttribute('d', d);
        line.setAttribute('class', 'dag-edge');
        svg.appendChild(line);
      }
    }

    canvasEl.appendChild(svg);

    // Draw node boxes as positioned <button> elements on top of SVG
    // (using a relative container so buttons can be absolutely positioned).
    canvasEl.style.position = 'relative';
    svg.style.position = 'absolute';
    svg.style.top = '0';
    svg.style.left = '0';
    // Set the canvas to be at least svg size.
    canvasEl.style.minWidth = totalW + 'px';
    canvasEl.style.minHeight = totalH + 'px';

    for (const n of wf.nodes) {
      const p = pos[n.id];
      if (!p) continue;
      const nodeState = activeSnap && activeSnap.nodes ? activeSnap.nodes[n.id] : null;
      const status = nodeState ? (nodeState.status || 'pending') : 'pending';
      const icon = nodeStatusIcon(status);
      const statusLabel = nodeStatusLabel(status);

      const btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'dag-node dag-node-' + status + (n.id === flowsState.selectedNodeId ? ' dag-node-selected' : '');
      btn.style.position = 'absolute';
      btn.style.left = p.x + 'px';
      btn.style.top = p.y + 'px';
      btn.style.width = SVG_NODE_W + 'px';
      btn.style.height = SVG_NODE_H + 'px';
      btn.setAttribute('aria-label', 'Node ' + n.id + ': ' + n.agent + ', status: ' + statusLabel);

      const iconEl = document.createElement('span');
      iconEl.className = 'dag-node-icon';
      iconEl.setAttribute('aria-hidden', 'true');
      iconEl.textContent = icon;

      const labelEl = document.createElement('span');
      labelEl.className = 'dag-node-label';
      labelEl.textContent = n.id; // textContent is safe

      const agentEl = document.createElement('span');
      agentEl.className = 'dag-node-agent';
      agentEl.textContent = n.agent; // textContent is safe

      const statusEl = document.createElement('span');
      statusEl.className = 'sr-only';
      statusEl.textContent = statusLabel;

      btn.appendChild(iconEl);
      btn.appendChild(labelEl);
      btn.appendChild(agentEl);
      btn.appendChild(statusEl);

      // Click: show node output.
      const nodeId = n.id;
      const runId = flowsState.activeRunId;
      btn.addEventListener('click', () => {
        flowsState.selectedNodeId = nodeId;
        if (runId) {
          loadNodeOutput(runId, nodeId);
        }
        // Re-render canvas to update selected highlight.
        renderFlowsCanvas();
        // Show node panel.
        renderFlowsNodePanel();
      });

      canvasEl.appendChild(btn);
    }
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

  function runStatusLabel(status) {
    switch (status) {
      case 'pending':     return 'pending';
      case 'running':     return 'running';
      case 'completed':   return 'completed';
      case 'failed':      return 'failed';
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

  // IDE editor state variables.
  // Declared as var (not let) so they are hoisted to the top of the IIFE and
  // initialised to undefined/false/null before any code runs — including the
  // pre-paint theme init block. The matching var declarations at the top of the
  // IIFE (in the TDZ-guard block) ensure no TDZ window exists for these names.
  // Using var here avoids a duplicate-declaration SyntaxError; var re-declarations
  // of var are a no-op in sloppy mode and silently ignored in strict mode.
  ideTabInitialized = false; // already var-declared above; reset to canonical initial value

  // Ref to Monaco iframe's contentWindow (set once the iframe loads).
  ideEditorWindow = null;

  // True once the Monaco {type:'ready'} postMessage arrives.
  ideEditorReady = false;

  // Queued openFile to send once the editor is ready.
  ideQueuedOpen = null;

  // Stored handler ref so we can removeEventListener if needed in the future.
  // Guard prevents double-registration if renderIdeLayout were ever called twice.
  ideMessageHandlerRegistered = false;

  // The workspace-relative path of the file currently open in Monaco.
  // Updated on every openFileInEditor call.
  ideCurrentPath = '';

  // The version stamp (SHA-256 hex) returned by GET /api/files/content and
  // updated after every successful POST /api/files/write.  Sent back on the
  // next write for optimistic concurrency.  Empty string = force-write.
  ideCurrentVersion = '';

  // Whether the editor is in editable mode (Edit toggle is pressed).
  // Default: false (view-only, readOnly:true).
  ideEditable = false;

  // Whether the editor's model has unsaved changes since the last openFile
  // or successful save.
  ideIsDirty = false;

  // True while a POST /api/files/write fetch is in-flight.  Prevents double-POST
  // when the operator presses ⌘S twice before the first response arrives.
  ideIsSaving = false;

  // Timer for the transient save-status message.  Reset to canonical initial value.
  ideSaveStatusTimer = null;

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

    panel.innerHTML =
      '<div class="ide-layout" role="main" aria-label="IDE">' +
        // Left: file tree
        '<aside class="ide-tree-pane" aria-label="File tree">' +
          '<div class="ide-tree-header">' +
            '<span class="subsection-title" id="ide-tree-title">Files</span>' +
          '</div>' +
          '<div id="ide-tree-root" class="ide-tree-root" role="tree" aria-label="Workspace files"></div>' +
        '</aside>' +
        // Center: editor
        '<div class="ide-editor-pane">' +
          '<div class="ide-editor-header" id="ide-editor-header">' +
            // Dirty marker (● before path) shown when editor has unsaved changes.
            '<span id="ide-dirty-marker" class="ide-dirty-marker" aria-hidden="true" style="display:none">●</span>' +
            '<span id="ide-open-path" class="ide-open-path" aria-live="polite"></span>' +
            // Edit/View toggle: pressing Edit enables Monaco editing.
            '<button id="ide-edit-toggle" type="button" class="ide-header-btn" ' +
              'aria-pressed="false" title="Toggle edit mode">' +
              'Edit' +
            '</button>' +
            // Save button: enabled when dirty + editable; triggers POST /api/files/write.
            '<button id="ide-save-btn" type="button" class="ide-header-btn ide-save-btn" ' +
              'disabled aria-disabled="true" title="Save file (⌘S / Ctrl-S)">' +
              'Save' +
            '</button>' +
            // Save status: transient "Saved" / error text (aria-live polite).
            '<span id="ide-save-status" class="ide-save-status" aria-live="polite" role="status"></span>' +
          '</div>' +
          '<div class="ide-editor-wrap">' +
            '<div id="ide-editor-notice" class="ide-editor-notice" style="display:none" role="status"></div>' +
            // First-party same-origin content; isolation boundary is the
            // scoped per-route CSP on /ide/editor, not an escapable sandbox.
            '<iframe id="ide-editor-frame" class="ide-editor-frame" ' +
              'src="/ide/editor" ' +
              'title="Code editor" ' +
              'aria-label="Monaco code editor">' +
            '</iframe>' +
          '</div>' +
        '</div>' +
        // Right: embedded chat
        '<div class="ide-chat-pane" aria-label="Chat" id="ide-chat-col">' +
          '<div class="ide-chat-header">' +
            '<span class="subsection-title">Chat</span>' +
          '</div>' +
          '<div id="ide-chat-slot"></div>' +
        '</div>' +
      '</div>';

    // Wire Monaco iframe postMessage listener — guard prevents double-registration
    // if this function is ever re-entered (e.g. a future teardown+reinit path).
    if (!ideMessageHandlerRegistered) {
      window.addEventListener('message', handleIdeEditorMessage);
      ideMessageHandlerRegistered = true;
    }

    // Wire the iframe load event to grab its contentWindow.
    const frame = document.getElementById('ide-editor-frame');
    if (frame) {
      frame.addEventListener('load', () => {
        ideEditorWindow = frame.contentWindow;
        // The 'ready' message comes from the iframe after Monaco mounts.
      });
    }

    // Wire Edit/View toggle button.
    const editToggle = document.getElementById('ide-edit-toggle');
    if (editToggle) {
      editToggle.addEventListener('click', () => {
        ideEditable = !ideEditable;
        editToggle.textContent = ideEditable ? 'View' : 'Edit';
        editToggle.setAttribute('aria-pressed', ideEditable ? 'true' : 'false');
        editToggle.classList.toggle('ide-header-btn-active', ideEditable);
        // Inform Monaco iframe.
        if (ideEditorWindow) {
          ideEditorWindow.postMessage(
            { type: 'setEditable', editable: ideEditable },
            window.location.origin
          );
        }
        // When switching to View, clear dirty state in the parent too.
        if (!ideEditable) {
          ideSetDirty(false);
        }
      });
    }

    // Wire Save button.
    const saveBtn = document.getElementById('ide-save-btn');
    if (saveBtn) {
      saveBtn.addEventListener('click', () => {
        triggerSave();
      });
    }

    // Mount the embedded chat pane.
    mountIdeChatPane();

    // Load file tree.
    loadIdeTree('');
  }

  // ideSetDirty updates the dirty state + UI (marker, Save button enabled/disabled).
  function ideSetDirty(dirty) {
    ideIsDirty = dirty;
    const marker = document.getElementById('ide-dirty-marker');
    if (marker) marker.style.display = dirty ? '' : 'none';
    const saveBtn = document.getElementById('ide-save-btn');
    if (saveBtn) {
      const canSave = dirty && ideEditable;
      saveBtn.disabled = !canSave;
      saveBtn.setAttribute('aria-disabled', canSave ? 'false' : 'true');
    }
  }

  // ideShowSaveStatus shows a transient status message (success or error) in
  // the editor header's aria-live region.  It auto-clears after `durationMs`.
  // Clears immediately if called again before the previous timeout fires.
  // ideSaveStatusTimer is var-declared in the hoisted TDZ-guard block above.
  function ideShowSaveStatus(msg, isError, durationMs) {
    const el = document.getElementById('ide-save-status');
    if (!el) return;
    if (ideSaveStatusTimer !== null) {
      clearTimeout(ideSaveStatusTimer);
      ideSaveStatusTimer = null;
    }
    el.textContent = msg;
    el.className = 'ide-save-status' + (isError ? ' ide-save-status-error' : ' ide-save-status-ok');
    if (durationMs) {
      ideSaveStatusTimer = setTimeout(() => {
        el.textContent = '';
        el.className = 'ide-save-status';
        ideSaveStatusTimer = null;
      }, durationMs);
    }
  }

  // triggerSave asks the Monaco iframe for its current content, then POSTs it
  // to /api/files/write.  Content is obtained by posting {type:'save'} — Monaco
  // replies with {type:'content', path, content} in handleIdeEditorMessage.
  //
  // Called from: Save button click, and from handleIdeEditorMessage when Monaco
  // signals {type:'save'} (⌘S / Ctrl-S).
  //
  // The actual fetch runs in performSave(path, content) once content arrives.
  function triggerSave() {
    if (!ideEditable || !ideIsDirty || ideIsSaving) return;
    if (!ideEditorWindow || !ideCurrentPath) return;
    // Ask the iframe for the current content.  The reply fires handleIdeEditorMessage
    // with {type:'content', path, content}, which calls performSave().
    ideEditorWindow.postMessage({ type: 'requestContent' }, window.location.origin);
  }

  // performSave POSTs content to /api/files/write with the stored version stamp.
  // Handles 200, 409, 403, 413, and generic errors inline.
  // ideIsSaving is set true at entry and reset in .finally() to prevent a
  // double-POST race when ⌘S fires twice before the first response arrives.
  function performSave(path, content) {
    ideIsSaving = true;
    const saveBtn = document.getElementById('ide-save-btn');
    if (saveBtn) {
      saveBtn.disabled = true;
      saveBtn.setAttribute('aria-disabled', 'true');
      saveBtn.textContent = 'Saving…';
    }
    ideShowSaveStatus('', false, 0);

    fetch('/api/files/write', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path: path, content: content, version: ideCurrentVersion }),
    })
      .then((r) => {
        if (r.status === 200) {
          return r.json().then((data) => {
            // Update stored version to the new hash from the response.
            ideCurrentVersion = data.version || '';
            ideSetDirty(false);
            ideShowSaveStatus('Saved', false, 2500);
          });
        }
        if (r.status === 409) {
          // File changed on disk since we read it.  The inline conflict notice
          // (showIdeReloadConflictNotice) carries the full message + Reload action.
          // Clear the status bar so the same message isn't duplicated in two places.
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
        // Generic error: keep editor content intact, show status.
        return r.text().then((body) => {
          ideShowSaveStatus('Save failed (' + String(r.status) + ') — your edits are preserved', true, 8000);
        });
      })
      .catch(() => {
        ideShowSaveStatus('Network error — your edits are preserved', true, 8000);
      })
      .finally(() => {
        // Always reset the in-flight guard, regardless of outcome.
        ideIsSaving = false;
        if (saveBtn) {
          saveBtn.textContent = 'Save';
          // Re-enable only if still dirty + editable (may have been cleared on success).
          const canSave = ideIsDirty && ideEditable;
          saveBtn.disabled = !canSave;
          saveBtn.setAttribute('aria-disabled', canSave ? 'false' : 'true');
        }
      });
  }

  // showIdeReloadConflictNotice shows a 409-conflict inline notice with a
  // Reload button.  The editor's existing content is preserved; the operator
  // must explicitly confirm before local edits are discarded.
  function showIdeReloadConflictNotice(path) {
    const notice = document.getElementById('ide-editor-notice');
    if (!notice) return;

    // Clear any previous content.
    notice.textContent = '';
    notice.style.display = '';

    const msg = document.createTextNode('File changed on disk — ');
    notice.appendChild(msg);

    const reloadBtn = document.createElement('button');
    reloadBtn.type = 'button';
    reloadBtn.className = 'ide-notice-reload-btn';
    reloadBtn.textContent = 'Reload (discards local edits)';
    reloadBtn.addEventListener('click', () => {
      // Confirm before discarding unsaved local edits.
      if (!window.confirm('Reload file from disk? Your unsaved edits will be lost.')) return;
      notice.style.display = 'none';
      openIdeFile(path, path.split('/').pop(), null);
    });
    notice.appendChild(reloadBtn);
  }

  function handleIdeEditorMessage(e) {
    // Only accept messages from the same origin.
    if (e.origin !== window.location.origin) return;
    const msg = e.data;
    if (!msg || typeof msg !== 'object') return;

    if (msg.type === 'ready') {
      ideEditorReady = true;
      // Sync Monaco theme to the current console theme.
      syncMonacoTheme(document.documentElement.getAttribute('data-theme') || 'og');
      // Flush any queued openFile.
      if (ideQueuedOpen) {
        sendOpenFile(ideQueuedOpen);
        ideQueuedOpen = null;
      }
    } else if (msg.type === 'error') {
      const notice = document.getElementById('ide-editor-notice');
      if (notice) {
        notice.textContent = 'Editor error: ' + (msg.message || 'unknown error');
        notice.style.display = '';
      }
    } else if (msg.type === 'dirty') {
      // Monaco reports dirty state change (debounced).
      if (ideEditable) {
        ideSetDirty(!!msg.dirty);
      }
    } else if (msg.type === 'save') {
      // Monaco signals ⌘S / Ctrl-S: content is provided directly.
      // Guard on !ideIsSaving to prevent a double-POST when two ⌘S fire before
      // the first response arrives (B2).  msg.path echo is also checked for
      // stale-path safety (B1 parallel: if the file switched mid-keypress).
      if (ideEditable && ideIsDirty && !ideIsSaving &&
          typeof msg.content === 'string' && msg.path === ideCurrentPath) {
        performSave(ideCurrentPath, msg.content);
      }
    } else if (msg.type === 'content') {
      // Reply to {type:'requestContent'} — triggered by triggerSave().
      // B1: guard on echoed msg.path matching ideCurrentPath.  If the operator
      // switched files between triggerSave() posting requestContent and this reply
      // arriving, the echoed path will differ from ideCurrentPath — drop the reply
      // rather than writing old content to the new file's path/version.
      // Also gate on ideEditable + ideIsDirty + !ideIsSaving so a stale reply
      // from a previous session cannot trigger an unexpected write.
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

  // openFileInEditor sends the file content to Monaco and updates IDE state.
  // `version` is the SHA-256 hex stamp returned by GET /api/files/content;
  // it is stored for optimistic concurrency on the next write.
  function openFileInEditor(path, content, language, version) {
    // Update the path header.
    const pathEl = document.getElementById('ide-open-path');
    if (pathEl) pathEl.textContent = path;

    // Clear any previous error notice.
    const notice = document.getElementById('ide-editor-notice');
    if (notice) notice.style.display = 'none';

    // Store current-file state for save operations.
    ideCurrentPath = path;
    ideCurrentVersion = version || '';

    // Opening a new file resets dirty state and the Edit toggle back to View.
    ideEditable = false;
    ideSetDirty(false);
    ideShowSaveStatus('', false, 0);

    const editToggle = document.getElementById('ide-edit-toggle');
    if (editToggle) {
      editToggle.textContent = 'Edit';
      editToggle.setAttribute('aria-pressed', 'false');
      editToggle.classList.remove('ide-header-btn-active');
    }

    // Tell Monaco to switch back to read-only when opening a new file.
    if (ideEditorWindow) {
      ideEditorWindow.postMessage(
        { type: 'setEditable', editable: false },
        window.location.origin
      );
    }

    const payload = { type: 'openFile', path, content, language: language || 'plaintext' };

    if (ideEditorReady && ideEditorWindow) {
      sendOpenFile(payload);
    } else {
      // Queue: will be sent once {type:'ready'} arrives.
      ideQueuedOpen = payload;
    }
  }

  // ---- File tree ---------------------------------------------------------------

  // loadIdeTree fetches the root workspace listing at depth=1 (shallow) and
  // renders a collapsed tree. Individual dirs are lazy-loaded on expand.
  function loadIdeTree(reldir) {
    const treeEl = document.getElementById('ide-tree-root');
    if (!treeEl) return;

    const dir = reldir || '.';
    const url = '/api/files/tree?dir=' + encodeURIComponent(dir) + '&depth=1';

    fetch(url).then((r) => {
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

              fetch('/api/files/tree?dir=' + encodeURIComponent(entry.path) + '&depth=1')
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

    fetch('/api/files/content?path=' + encodeURIComponent(relpath))
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

    // Register in chatPanes only for the duration of this page load so the
    // SSE demux (sessionToPaneId → chatPanes.get()) can find the pane.
    // We do NOT call savePaneState() — the pane is session-only.
    chatPanes.set(p.id, p);

    const paneEl = buildPaneElement(p.id);
    slot.appendChild(paneEl);

    // Load transcript for this pane.
    loadTranscriptForPane(p.id);
  }

  // =========================================================================
  // ---- 8. Page rendering ------------------------------------------------------

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

    if (!TOKEN) {
      document.getElementById('auth-error').classList.add('visible');
      return;
    }

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
