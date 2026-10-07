// chat-routing.js — the console chat's routing UI (K-148).
//
// Loaded before app.js (a plain script, no module system); app.js reaches it
// through window.YakChatRouting and works without it. It holds:
//   - the model registry cache behind the runtime/model selects (GET /api/models),
//   - the "@codex:gpt-5 ..." message prefix parser,
//   - the pane routing mode label (auto | runtime | pinned),
//   - the "why this model" route chip and the "context reset (cache)" banner.
//
// XSS discipline: server text (reason, runtime, model, ids) reaches the DOM only
// through textContent / setAttribute in buildRouteElement and buildHandoffElement,
// or through esc() in modeBadgeHTML. There is no other innerHTML in this file.
(function () {
  'use strict';

  var HARNESSES = ['claude', 'codex', 'agy'];
  var ID_RE = /^[a-z0-9][a-z0-9._:-]{0,63}$/;
  var LS_KEY = 'yakos_models_v1';
  var MAX_MODELS = 600;

  function esc(s) {
    return String(s == null ? '' : s)
      .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;').replace(/'/g, '&#39;');
  }

  // ---- registry cache -------------------------------------------------------

  var models = null; // [{id, harness, usable}] or null before the first load

  // clean keeps only entries a dispatch would accept: a known harness and a
  // well-formed id. Whatever the server (or localStorage) says, nothing else
  // reaches an <option> or a request body.
  function clean(list) {
    if (!Array.isArray(list)) return null;
    var out = [];
    for (var i = 0; i < list.length && out.length < MAX_MODELS; i++) {
      var m = list[i];
      if (!m || typeof m.id !== 'string' || !ID_RE.test(m.id)) continue;
      if (HARNESSES.indexOf(m.harness) < 0) continue;
      out.push({ id: m.id, harness: m.harness, usable: m.usable !== false });
    }
    return out;
  }

  try {
    var saved = clean(JSON.parse(localStorage.getItem(LS_KEY) || 'null'));
    if (saved && saved.length) models = saved;
  } catch (e) { /* no cache */ }

  // ensureLoaded fetches the registry once (apiFetch is app.js's authenticated
  // fetch wrapper) and calls onChange when the lists changed.
  var loading = false;
  function ensureLoaded(apiFetch, onChange) {
    if (loading || typeof apiFetch !== 'function') return;
    loading = true;
    apiFetch('GET', '/api/models').then(function (resp) {
      if (!resp || !resp.ok) return null;
      return resp.json();
    }).then(function (body) {
      var c = clean(body && body.models);
      if (!c || !c.length) return;
      var changed = JSON.stringify(c) !== JSON.stringify(models);
      models = c;
      try { localStorage.setItem(LS_KEY, JSON.stringify(c)); } catch (e) { /* quota */ }
      if (changed && typeof onChange === 'function') onChange();
    }).catch(function () { /* the static lists keep working */ }).then(function () { loading = false; });
  }

  // modelOptions returns the model values for a runtime from the registry:
  // '' (default) first, the harness's usable ids, then the tier aliases that are
  // valid everywhere. null when there is no registry or the runtime is 'auto'
  // (its real runtime is unknown until the router decides), so the caller keeps
  // its static list.
  function modelOptions(runtime, aliases) {
    if (!models || HARNESSES.indexOf(runtime) < 0) return null;
    var out = [''];
    models.forEach(function (m) {
      if (m.harness === runtime && m.usable && out.indexOf(m.id) < 0) out.push(m.id);
    });
    (aliases || []).forEach(function (a) { if (out.indexOf(a) < 0) out.push(a); });
    return out.length > 1 ? out : null;
  }

  // ---- @prefix --------------------------------------------------------------

  // parseOverride reads "@codex[:gpt-5] task". It returns {runtime, model, task}
  // with the prefix removed, or null when the text has no valid prefix (it is
  // then an ordinary message, even if it starts with an @). A task is required.
  var PREFIX_RE = /^@(claude|codex|agy)(?::([a-z0-9][a-z0-9._:-]{0,63}))?[ \t]+(\S[\s\S]*)$/;
  function parseOverride(text) {
    var m = PREFIX_RE.exec(String(text == null ? '' : text).trim());
    if (!m) return null;
    return { runtime: m[1], model: m[2] || '', task: m[3] };
  }

  // decorateBody adds the override fields of a parsed prefix to a dispatch body.
  // The server checks them again; they beat the pane's selects for this turn.
  function decorateBody(body, override) {
    if (override) {
      body.overrideRuntime = override.runtime;
      if (override.model) body.overrideModel = override.model;
    }
    return body;
  }

  // ---- pane routing mode ----------------------------------------------------

  // routeMode names what the pane's selects ask for: the router decides (auto),
  // the runtime is fixed (runtime), or runtime and model are (pinned).
  function routeMode(pane) {
    if (!pane || !pane.runtime || pane.runtime === 'auto') return 'auto';
    return pane.model ? 'pinned' : 'runtime';
  }

  var MODE_TEXT = {
    auto: 'router chooses',
    runtime: 'runtime pinned',
    pinned: 'runtime and model pinned'
  };

  function modeBadgeHTML(pane) {
    var mode = routeMode(pane);
    return '<span class="pane-route-mode pane-route-mode-' + esc(mode) + '" title="' + esc(MODE_TEXT[mode]) + '">' +
      esc(mode) + '</span>';
  }

  // ---- route chip and handoff banner ----------------------------------------

  var PINNED_TEXT = { override: 'set by @prefix', pane: 'set by the pane', router: 'chosen by the router' };

  function addSpan(doc, parent, cls, text) {
    var el = doc.createElement('span');
    el.className = cls;
    el.textContent = text;
    parent.appendChild(el);
  }

  // buildRouteElement renders a route (SSE `route` event or transcript turn) as
  // the "why this model" chip. Every server string goes in through textContent.
  function buildRouteElement(route, doc) {
    var el = doc.createElement('div');
    el.className = 'chat-msg chat-route-chip';
    el.setAttribute('role', 'note');
    addSpan(doc, el, 'chat-route-where', String(route.runtime || '?') + (route.model ? ' / ' + route.model : ''));
    addSpan(doc, el, 'chat-route-how', ' ' + (PINNED_TEXT[route.pinned] || PINNED_TEXT.router));
    if (route.fallback_from) addSpan(doc, el, 'chat-route-fallback', ' (fell back from ' + route.fallback_from + ')');
    if (route.reason) {
      addSpan(doc, el, 'chat-route-why', ' - ' + route.reason);
      el.setAttribute('title', route.reason);
    }
    if (route.rule_id) addSpan(doc, el, 'chat-route-rule', ' [' + route.rule_id + ']');
    return el;
  }

  // buildHandoffElement renders the "context reset (cache)" banner.
  function buildHandoffElement(h, doc) {
    var el = doc.createElement('div');
    el.className = 'chat-msg chat-handoff-banner';
    el.setAttribute('role', 'status');
    var n = Number(h.turns) || 0, b = Number(h.digest_bytes) || 0, r = Number(h.redactions) || 0;
    el.textContent = 'Context reset (cache): moved from ' + String(h.from || '?') + ' to ' + String(h.to || '?') +
      '. The new runtime starts without the earlier prompt cache; a digest of ' + n + ' earlier turn' + (n === 1 ? '' : 's') +
      ' (' + b + ' bytes' + (r ? ', ' + r + ' secret-like value' + (r === 1 ? '' : 's') + ' redacted' : '') +
      ') was added to this message.';
    return el;
  }

  // buildElement is app.js's hook for the two message roles this module owns.
  // It returns null for any other role.
  function buildElement(msg, doc) {
    if (!msg) return null;
    if (msg.role === 'route' && msg.route) return buildRouteElement(msg.route, doc);
    if (msg.role === 'handoff' && msg.handoff) return buildHandoffElement(msg.handoff, doc);
    return null;
  }

  // fromEvent turns an SSE event into a pane message, or null.
  function fromEvent(ev, sessionId) {
    if (!ev) return null;
    if (ev.type === 'route' && ev.route) return { role: 'route', route: ev.route, ts: ev.ts, sessionId: sessionId };
    if (ev.type === 'handoff' && ev.handoff) return { role: 'handoff', handoff: ev.handoff, ts: ev.ts, sessionId: sessionId };
    return null;
  }

  // fromTranscript turns a persisted transcript turn into a pane message, or null.
  function fromTranscript(e) {
    if (!e || e.role !== 'route') return null;
    return {
      role: 'route', ts: e.ts, sessionId: e.session_id,
      route: { runtime: e.runtime, model: e.model, reason: e.text, rule_id: e.rule_id,
        fallback_from: e.fallback_from, pinned: e.pinned }
    };
  }

  window.YakChatRouting = {
    parseOverride: parseOverride,
    decorateBody: decorateBody,
    routeMode: routeMode,
    modeBadgeHTML: modeBadgeHTML,
    modelOptions: modelOptions,
    ensureLoaded: ensureLoaded,
    buildElement: buildElement,
    fromEvent: fromEvent,
    fromTranscript: fromTranscript,
    _setModels: function (list) { models = clean(list); }, // test hook
    _esc: esc
  };
})();
