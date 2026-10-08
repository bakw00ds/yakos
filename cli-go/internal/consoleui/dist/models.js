// models.js: the Models & Providers tab (K-153).
//
// Loaded before app.js (a plain script, no module system); app.js reaches it
// through window.YakModels. It renders GET /api/models/overview (providers,
// catalog, tier aliases, pins and router rules, budgets, eval results, the
// sensitive policy), the 30-day token use per model from the performance
// dashboard (axis=model), and an explain playground over GET /api/models/explain.
//
// This build is READ-ONLY: the policy files are edited with `yakos models ...` and
// `yakos router policy set`, which go through the one trusted, audited writer. The
// tab says so and shows the commands; it has no form that writes.
//
// XSS discipline: every server string reaches the DOM through textContent. The
// file has no markup sink at all (a Go test greps for them and a node smoke test
// makes the DOM's throw).
(function (root) {
  'use strict';

  var doc = root.document;
  var ID_RE = /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/;

  function el(tag, cls, text) {
    var e = doc.createElement(tag);
    if (cls) e.className = cls;
    if (text !== undefined && text !== null) e.textContent = String(text);
    return e;
  }

  function yesNo(b) { return b ? 'yes' : 'no'; }

  function num(n) {
    return typeof n === 'number' && isFinite(n) ? n.toLocaleString('en-US') : '-';
  }

  function usd(n) {
    return typeof n === 'number' && isFinite(n) ? '$' + n.toFixed(2) : '-';
  }

  // table builds a table from header names and rows of cell strings.
  function table(headers, rows, emptyText) {
    if (!rows.length) return el('p', 'models-empty', emptyText || 'None.');
    var t = el('table', 'models-table');
    var head = el('tr');
    headers.forEach(function (h) { head.appendChild(el('th', null, h)); });
    t.appendChild(head);
    rows.forEach(function (r) {
      var tr = el('tr');
      r.forEach(function (c) { tr.appendChild(el('td', null, c)); });
      t.appendChild(tr);
    });
    return t;
  }

  function section(title, note) {
    var s = el('section', 'models-section');
    s.appendChild(el('h3', null, title));
    if (note) s.appendChild(el('p', 'models-note', note));
    return s;
  }

  function price(e) {
    if (!e.cost) return e.billing === 'api' ? 'not set' : '-';
    var c = e.cost;
    return '$' + c.input + ' in / $' + c.output + ' out per M tokens';
  }

  function ruleText(o) {
    var parts = [];
    Object.keys(o || {}).sort().forEach(function (k) {
      var v = o[k];
      if (v === '' || v === 0 || v === false || v === null || (Array.isArray(v) && !v.length)) return;
      parts.push(k + '=' + (Array.isArray(v) ? v.join(',') : v));
    });
    return parts.join(' ') || '-';
  }

  // render builds the whole tab body. tokens maps model id to 30-day tokens, or is
  // null when the performance dashboard could not be read.
  function render(data, tokens) {
    var body = el('div', 'models-body');
    var d = data || {};

    var ro = el('p', 'models-banner', 'Read-only view. Change policy from a terminal: ' +
      'yakos models enable|disable <id>, alias <alias> <codex|agy> <id>, pin <agent> <id>, ' +
      'pricing <id> --input N --output N; yakos router policy get|set. Each change is written ' +
      'atomically, owner-only, and recorded in the dispatch log.');
    body.appendChild(ro);

    var prov = section('Providers');
    prov.appendChild(table(['Harness', 'Provider', 'Installed', 'Signed in', 'Cooldown', 'Next step'],
      (d.providers || []).map(function (p) {
        return [p.harness, p.provider, yesNo(p.installed), yesNo(p.signed_in),
          p.cooling ? p.cooldown_seconds + 's left' : 'no', p.hint || '-'];
      })));
    body.appendChild(prov);

    var cat = section('Catalog', 'Tokens are the unit everywhere; only models billed per API call carry a price. ' +
      '30-day tokens come from the dispatch log (Performance, by model).');
    cat.appendChild(table(['Model', 'Harness', 'Billing', 'Enabled', 'Seen', 'Price', 'Tokens (30d)', 'Aliases'],
      (d.models || []).map(function (e) {
        var seen = e.availability ? e.availability.state : 'unknown';
        return [e.id, e.harness, e.billing + ' (' + e.billing_by + ')', yesNo(e.enabled) + ' (' + e.enabled_by + ')',
          seen, price(e), tokens ? num(tokens[e.id] || 0) : '-', (e.aliases || []).join(', ') || '-'];
      })));
    body.appendChild(cat);

    var al = section('Tier aliases', 'Which model each alias resolves to per harness (claude takes tier names).');
    al.appendChild(table(['Alias', 'claude', 'codex', 'agy'], (d.aliases || []).map(function (a) {
      var by = a.by || {};
      function cell(h) { return h in by ? (by[h] || '(default)') : '-'; }
      return [a.alias, cell('claude'), cell('codex'), cell('agy')];
    })));
    body.appendChild(al);

    var r = d.router || {};
    var pins = section('Per-agent pins');
    pins.appendChild(table(['Agent', 'Runtime', 'Model'], (r.pins || []).map(function (p) {
      return [p.agent, p.runtime, p.model];
    }), 'No pins.'));
    body.appendChild(pins);

    var rules = section('Router rules', 'policy sha ' + (r.sha || '(no policy file)') + '. Rules apply in order; R0, the default chain, is last.');
    rules.appendChild(table(['Rule', 'Match', 'Action', 'Overrides pins'], (r.rules || []).map(function (x) {
      return [x.id, ruleText(x.match), ruleText(x.action), yesNo(x.override_pins)];
    }), 'No rules: the default chain decides.'));
    rules.appendChild(el('p', 'models-note', 'Unsandboxed runtimes allowed: ' +
      ((r.allow_unsandboxed_runtimes || []).join(', ') || 'none') + '. Hooks endpoint: ' + yesNo(r.hooks_endpoint) +
      '. OpenAI endpoint: ' + yesNo(r.openai_endpoint) + '. Edit these in the policy file by hand.'));
    (r.warnings || []).forEach(function (w) { rules.appendChild(el('p', 'models-warn', w)); });
    body.appendChild(rules);

    if ((r.gateway_classes || []).length) {
      var gc = section('Gateway classes', 'Variable NAMES that `yakos start` sets for Claude Code; values are never shown here.');
      gc.appendChild(table(['Class', 'Variable', 'Model'], r.gateway_classes.map(function (g) {
        return [g.class, g.env, g.model];
      })));
      body.appendChild(gc);
    }

    var bud = section('Budgets', 'Tokens first, then dollars (dollars count only for per-call billing).');
    bud.appendChild(table(['Agent', 'State', 'Window', 'Tokens', 'Token limit', 'Spent', 'Limit', 'Used'],
      (d.budgets || []).map(function (b) {
        return [b.agent, b.read_failed ? 'unreadable' : b.state, b.window, num(b.spent_tokens),
          b.limit_tokens ? num(b.limit_tokens) : 'off', usd(b.spent_usd), b.limit_usd ? usd(b.limit_usd) : 'off',
          b.limit_usd || b.limit_tokens ? Math.round(b.pct) + '%' : '-'];
      }), 'No budgets.'));
    body.appendChild(bud);

    var ev = section('Eval results');
    ev.appendChild(table(['When', 'Agent', 'Run', 'Pass rate by tier', 'Candidate'], (d.evals || []).map(function (e) {
      var rates = Object.keys(e.tier_pass_rates || {}).sort().map(function (k) {
        return k + ' ' + Math.round(e.tier_pass_rates[k] * 100) + '%';
      }).join(', ');
      return [e.ts, e.agent, e.run_id || '-', rates || '-', e.candidate_tier || (e.partial ? 'partial run' : 'none')];
    }), 'No eval runs in the log.'));
    body.appendChild(ev);

    var s = d.sensitive || {};
    var sen = section('Sensitive policy');
    sen.appendChild(el('p', null, 'Class "' + (s.class || 'sensitive') + '": ' + (s.behavior || '') +
      ' (' + num(s.secret_patterns) + ' secret patterns, ' + num(s.never_path_patterns) + ' credential-file patterns; a project may add more, never remove).'));
    body.appendChild(sen);

    return body;
  }

  // renderExplain builds the playground result for one explain response.
  function renderExplain(x) {
    var out = el('div', 'models-explain-result');
    var rows = [
      ['Route', (x.runtime || '-') + (x.model ? '/' + x.model : ' (harness default)')],
      ['Provider', x.provider || '-'], ['Rule', x.rule || '-'],
      ['Chain', '[' + (x.chain || []).join(' ') + ']'], ['Reason', x.reason || '-'],
      ['Fell back from', x.fallback_from || '-'], ['Route class', x.route_class || '-'],
      ['Policy sha', x.policy_sha || '-']
    ];
    (x.skipped || []).forEach(function (sk) {
      rows.push(['Skipped', sk.runtime + ' (' + (sk.cooling ? 'cooling' : sk.reason) + ')']);
    });
    out.appendChild(table(['', ''], rows));
    return out;
  }

  function playground(fetchFn) {
    var s = section('Explain playground', 'A dry run of the router: nothing starts, no ledger row is written.');
    var form = el('div', 'models-explain-form');
    var agent = el('input', 'models-explain-agent');
    agent.type = 'text';
    agent.placeholder = 'agent name';
    agent.setAttribute('aria-label', 'Agent name');
    var cls = el('input', 'models-explain-class');
    cls.type = 'text';
    cls.placeholder = 'class (optional)';
    cls.setAttribute('aria-label', 'Route class');
    var go = el('button', 'models-explain-go', 'Explain');
    go.type = 'button';
    var result = el('div', 'models-explain-out');
    go.addEventListener('click', function () {
      var a = String(agent.value || '').trim();
      var c = String(cls.value || '').trim();
      result.textContent = '';
      if (!ID_RE.test(a) || (c && !ID_RE.test(c))) {
        result.appendChild(el('p', 'models-warn', 'Enter an agent name (letters, digits, . _ : -).'));
        return;
      }
      var url = '/api/models/explain?agent=' + encodeURIComponent(a) + (c ? '&class=' + encodeURIComponent(c) : '');
      fetchFn('GET', url).then(function (resp) {
        return resp.json().then(function (body) { return { ok: resp.ok, body: body }; });
      }).then(function (r) {
        result.textContent = '';
        if (!r.ok) result.appendChild(el('p', 'models-warn', (r.body && r.body.error) || 'Could not explain.'));
        else result.appendChild(renderExplain(r.body));
      }).catch(function () {
        result.textContent = '';
        result.appendChild(el('p', 'models-warn', 'Could not explain.'));
      });
    });
    form.appendChild(agent);
    form.appendChild(cls);
    form.appendChild(go);
    s.appendChild(form);
    s.appendChild(result);
    return s;
  }

  // tokenMap turns the by_axis rows into {model id: tokens}.
  function tokenMap(rows) {
    if (!Array.isArray(rows)) return null;
    var m = {};
    rows.forEach(function (r) {
      if (r && typeof r.key === 'string' && typeof r.tokens === 'number') m[r.key] = r.tokens;
    });
    return m;
  }

  // open fills holder with the tab. fetchFn(method, path) resolves to a fetch
  // Response (the console's apiFetch).
  function open(holder, fetchFn) {
    holder.textContent = 'Loading...';
    var overview = fetchFn('GET', '/api/models/overview').then(function (resp) {
      if (!resp.ok) throw new Error('status ' + resp.status);
      return resp.json();
    });
    var tokens = fetchFn('GET', '/perf/api/perf/by_axis?axis=model&window=30d').then(function (resp) {
      return resp.ok ? resp.json() : null;
    }).then(tokenMap).catch(function () { return null; });
    return Promise.all([overview, tokens]).then(function (r) {
      holder.textContent = '';
      holder.appendChild(render(r[0], r[1]));
      holder.appendChild(playground(fetchFn));
    }).catch(function () {
      holder.textContent = 'Could not load the models overview.';
    });
  }

  root.YakModels = { open: open, render: render, renderExplain: renderExplain, tokenMap: tokenMap };
})(typeof window !== 'undefined' ? window : globalThis);
