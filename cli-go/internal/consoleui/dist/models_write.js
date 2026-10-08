// models_write.js: the write controls of the Models & Providers tab (K-175).
//
// Loaded after models.js (a plain script); models.js calls window.YakModelsWrite.panel
// only when GET /api/models/overview says can_write: the operator started the
// daemon with --console-model-writes AND the caller is an admin. Nothing here is
// drawn otherwise, and the server refuses every write path (405) while the flag is
// off, so hiding the controls is a convenience, not the protection.
//
// What a write needs, and what this file does about each:
//   - a CSRF token bound to the credential: GET /api/models/write-session mints it
//     and sets the double-submit cookie; it is sent as X-CSRF-Token on each write.
//   - a re-authentication within 5 minutes: a refusal of {"error":"step_up_required",
//     "method":...} shows the step-up form (password, token or certificate). The
//     secret is sent once and the input is cleared; it is never kept or stored.
//   - Content-Type: application/json (apiFetch sets it with a body).
//
// XSS discipline: every server string reaches the DOM through textContent. The file
// has no markup sink and no storage (a Go test greps for both; a node smoke test makes
// the DOM's markup sinks throw).
(function (root) {
  'use strict';

  var doc = root.document;
  var ID_RE = /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/;
  var PATHS = {
    enable: '/api/models/enable', disable: '/api/models/disable', alias: '/api/models/alias',
    pin: '/api/models/pin', pricing: '/api/models/pricing', policy: '/api/router/policy'
  };

  function el(tag, cls, text) {
    var e = doc.createElement(tag);
    if (cls) e.className = cls;
    if (text !== undefined && text !== null) e.textContent = String(text);
    return e;
  }

  function input(type, label, placeholder) {
    var i = el('input', 'models-write-input');
    i.type = type;
    if (placeholder) i.placeholder = placeholder;
    i.setAttribute('aria-label', label);
    return i;
  }

  function select(label, values) {
    var s = el('select', 'models-write-select');
    s.setAttribute('aria-label', label);
    values.forEach(function (v) {
      var o = el('option', null, v);
      o.value = v;
      s.appendChild(o);
    });
    return s;
  }

  function button(text, onClick) {
    var b = el('button', 'models-write-go', text);
    b.type = 'button';
    b.addEventListener('click', onClick);
    return b;
  }

  function uniq(list) {
    var seen = {};
    return list.filter(function (v) { if (seen[v]) return false; seen[v] = true; return true; });
  }

  // number parses a price field: '' is null (not sent), anything else must be a
  // finite non-negative number.
  function number(s) {
    s = String(s || '').trim();
    if (s === '') return { ok: true, v: null };
    var n = Number(s);
    return isFinite(n) && n >= 0 ? { ok: true, v: n } : { ok: false };
  }

  // panel builds the write controls. fetchFn(method, path, body, extra) is the
  // console's apiFetch (extra.headers are added, extra.keep401 keeps a 401 from
  // being taken for an expired login). reload() refreshes the tab after a write.
  function panel(overview, fetchFn, reload) {
    var o = overview || {};
    var csrf = null;
    var sec = el('section', 'models-section models-write');
    sec.appendChild(el('h3', null, 'Edit policy'));
    sec.appendChild(el('p', 'models-note', 'Browser writes are on for this daemon. Each change goes through the ' +
      'same writer as the CLI (atomic, owner-only), is recorded in the dispatch log as operator-browser, and needs ' +
      'a re-authentication within the last 5 minutes. The tab lists router-policy pins only; a pin in an ' +
      'agent\'s own file is not shown here.'));
    var status = el('p', 'models-write-status', '');
    var stepBox = el('div', 'models-write-step');
    sec.appendChild(status);
    sec.appendChild(stepBox);

    function say(msg, warn) {
      status.className = warn ? 'models-warn models-write-status' : 'models-write-status';
      status.textContent = msg;
    }

    function call(method, path, body) {
      var extra = { keep401: true, headers: csrf ? { 'X-CSRF-Token': csrf } : {} };
      return fetchFn(method, path, body, extra).then(function (resp) {
        return resp.json().then(function (b) { return { status: resp.status, ok: resp.ok, body: b || {} }; },
          function () { return { status: resp.status, ok: resp.ok, body: {} }; });
      });
    }

    // mint fetches the CSRF token (and the double-submit cookie) once.
    function mint() {
      if (csrf) return Promise.resolve(true);
      return call('GET', '/api/models/write-session').then(function (r) {
        if (!r.ok || typeof r.body.csrf_token !== 'string') { say('Could not start a write session.', true); return false; }
        csrf = r.body.csrf_token;
        return true;
      });
    }

    function showStepUp(method) {
      stepBox.textContent = '';
      var label = method === 'password' ? 'Password' : method === 'token' ? 'Console token' : 'Client certificate';
      var secret = null;
      if (method !== 'certificate') {
        secret = input('password', label, label);
        secret.autocomplete = 'off';
        stepBox.appendChild(secret);
      }
      stepBox.appendChild(button('Re-authenticate', function () {
        var body = {};
        if (method === 'password') body.password = String(secret.value || '');
        else if (method === 'token') body.token = String(secret.value || '');
        if (secret) secret.value = '';
        mint().then(function (ok) {
          if (!ok) return null;
          return call('POST', '/api/models/step-up', body);
        }).then(function (r) {
          if (!r) return;
          if (r.ok) { stepBox.textContent = ''; say('Re-authenticated. You can save changes for the next 5 minutes.'); }
          else if (r.status === 429) say('Too many attempts. Wait a minute and try again.', true);
          else say('Re-authentication failed.', true);
        }).catch(function () { say('Re-authentication failed.', true); });
      }));
    }

    // submit sends one write and reports the outcome.
    function submit(op, body) {
      say('Saving...');
      return mint().then(function (ok) {
        if (!ok) return null;
        return call('PUT', PATHS[op], body);
      }).then(function (r) {
        if (!r) return;
        if (r.ok) {
          say(r.body.changed ? 'Saved.' : 'No change: already set.');
          if (r.body.changed && typeof reload === 'function') reload();
        } else if (r.status === 401 && r.body.error === 'step_up_required') {
          say('Re-authenticate to save this change.', true);
          showStepUp(String(r.body.method || 'password'));
        } else if (r.status === 403) {
          csrf = null; // the token may be stale (daemon restarted); the next try mints again
          say('The change was refused (' + (r.body.error || 'forbidden') + '). Try again.', true);
        } else if (r.status === 405) {
          say('Browser writes are off for this daemon.', true);
        } else {
          say('Refused: ' + (r.body.error || 'status ' + r.status), true);
        }
      }).catch(function () { say('Could not save the change.', true); });
    }

    function row(title) {
      var r = el('div', 'models-write-row');
      r.appendChild(el('strong', null, title));
      return r;
    }

    var models = (o.models || []).filter(function (m) { return m && ID_RE.test(String(m.id)); });
    var ids = uniq(models.map(function (m) { return m.id; }));
    var aliases = (o.aliases || []).map(function (a) { return a.alias; });

    // enable / disable
    var r1 = row('Model on or off');
    var m1 = select('Model', ids);
    r1.appendChild(m1);
    r1.appendChild(button('Enable', function () { submit('enable', { id: m1.value }); }));
    r1.appendChild(button('Disable', function () { submit('disable', { id: m1.value }); }));
    sec.appendChild(r1);

    // alias
    var r2 = row('Tier alias');
    var a2 = select('Alias', aliases), h2 = select('Harness', ['codex', 'agy']);
    var m2 = input('text', 'Model id or default', 'model id, or default');
    r2.appendChild(a2); r2.appendChild(h2); r2.appendChild(m2);
    r2.appendChild(button('Set alias', function () {
      var id = String(m2.value || '').trim() || 'default';
      if (id !== 'default' && !ID_RE.test(id)) { say('Enter a model id or "default".', true); return; }
      submit('alias', { alias: a2.value, harness: h2.value, id: id });
    }));
    sec.appendChild(r2);

    // pin
    var r3 = row('Per-agent pin');
    var ag3 = input('text', 'Agent name', 'agent name');
    var m3 = select('Model', ids), rt3 = select('Runtime', ['', 'claude', 'codex', 'agy']);
    r3.appendChild(ag3); r3.appendChild(m3); r3.appendChild(rt3);
    r3.appendChild(button('Pin', function () {
      var a = String(ag3.value || '').trim();
      if (!ID_RE.test(a)) { say('Enter an agent name (letters, digits, . _ : -).', true); return; }
      submit('pin', { agent: a, id: m3.value, runtime: rt3.value });
    }));
    r3.appendChild(button('Clear pin', function () {
      var a = String(ag3.value || '').trim();
      if (!ID_RE.test(a)) { say('Enter an agent name (letters, digits, . _ : -).', true); return; }
      submit('pin', { agent: a, clear: true });
    }));
    sec.appendChild(r3);

    // pricing
    var r4 = row('Price (dollars per million tokens)');
    var m4 = select('Model', ids), b4 = select('Billing', ['', 'api', 'subscription', 'local']);
    var in4 = input('text', 'Input price', 'input'), out4 = input('text', 'Output price', 'output');
    var cr4 = input('text', 'Cache read price', 'cache read'), cw4 = input('text', 'Cache write price', 'cache write');
    [m4, b4, in4, out4, cr4, cw4].forEach(function (c) { r4.appendChild(c); });
    r4.appendChild(button('Set price', function () {
      var f = { input: number(in4.value), output: number(out4.value), cache_read: number(cr4.value), cache_write: number(cw4.value) };
      if (!f.input.ok || !f.output.ok || !f.cache_read.ok || !f.cache_write.ok) { say('Prices must be numbers of 0 or more.', true); return; }
      var body = { id: m4.value };
      if (b4.value) body.billing = b4.value;
      Object.keys(f).forEach(function (k) { if (f[k].v !== null) body[k] = f[k].v; });
      submit('pricing', body);
    }));
    r4.appendChild(button('Clear price', function () { submit('pricing', { id: m4.value, clear: true }); }));
    sec.appendChild(r4);

    // router rules
    var r5 = row('Router rules (YAML list)');
    var ta = el('textarea', 'models-write-rules');
    ta.setAttribute('aria-label', 'Router rules as a YAML list');
    ta.placeholder = '- match: {agent: backend}\n  action: {runtime: codex, model: gpt-5.6-terra}';
    var ok5 = input('checkbox', 'I understand this replaces every rule');
    r5.appendChild(ta);
    r5.appendChild(el('p', 'models-note', 'Saving REPLACES the whole rules list, including the pins above (they are rules). ' +
      'Paste the full list you want. Anchors, aliases and merge keys are refused; the privileged keys in the file are never changed here.'));
    r5.appendChild(ok5);
    r5.appendChild(el('span', null, ' I understand this replaces every rule. '));
    r5.appendChild(button('Save rules', function () {
      if (!ok5.checked) { say('Tick the box to confirm the replacement.', true); return; }
      submit('policy', { rules_yaml: String(ta.value || '') });
    }));
    sec.appendChild(r5);
    return sec;
  }

  root.YakModelsWrite = { panel: panel, number: number };
})(typeof window !== 'undefined' ? window : globalThis);
