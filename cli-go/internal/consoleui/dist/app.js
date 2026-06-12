// yakOS Unified Console — Phase 2.5 SPA
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
// Phase 2.5 features:
//   - Overview tab: "Now" panel (in-flight dispatches, online operators)
//   - Activity feed (newest-first, filterable, capped at 200 events)
//   - Operator presence via hello frame on WS connect
//   - SW ready promise gates iframe loading (activation-race fix)

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
      setTimeout(connectWS, wsRetryMs);
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
