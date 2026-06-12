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
          iframe.src = tab.src;
        });
      }
    }

    // On first switch to chat tab, ensure SSE is running.
    if (id === 'chat') {
      initChatTab();
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
      setTimeout(connectWS, jitteredDelay);
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

  function initChatTab() {
    if (chatTabInitialized) return;
    chatTabInitialized = true;

    getChatOperatorId(); // ensure operator ID is minted
    loadPaneStateFromStorage();

    // If no panes, add a default one.
    if (chatPanes.size === 0) {
      const p = makePane(newPaneId(), newConversationId());
      chatPanes.set(p.id, p);
      savePaneState();
    }

    renderChatLayout();

    // Load transcripts for each pane asynchronously.
    for (const [paneId] of chatPanes) {
      loadTranscriptForPane(paneId);
    }

    // Start SSE reader.
    startChatSSE();
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
  // ---- 8. Page rendering ------------------------------------------------------

  function buildPage() {
    const app = document.getElementById('app');
    app.innerHTML = `
      <div class="tab-bar">
        <span class="tab-bar-brand">yakOS</span>
        <div id="tab-bar-tabs" style="display:flex;"></div>
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
          <iframe title="Kanban board" allow="same-origin"></iframe>
        </div>
        <div id="panel-cost" class="tab-panel">
          <iframe title="Cost dashboard" allow="same-origin"></iframe>
        </div>
        <div id="panel-perf" class="tab-panel">
          <iframe title="Performance dashboard" allow="same-origin"></iframe>
        </div>
        <div id="panel-chat" class="tab-panel">
          <div class="chat-loading">
            <p class="empty-state">Initializing Chat…</p>
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
