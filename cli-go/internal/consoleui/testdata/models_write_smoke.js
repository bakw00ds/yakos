// Runs dist/models.js and dist/models_write.js against a minimal fake DOM whose
// markup sinks throw (K-175). The fetch stub records every call and plays the
// server: write-session, step_up_required, step-up, CSRF refusal, success.
'use strict';
const fs = require('fs');

function mk(tag) {
  const e = { tag, children: [], className: '', _text: '', listeners: {}, value: '', checked: false };
  for (const sink of ['innerHTML', 'outerHTML']) {
    Object.defineProperty(e, sink, { set() { throw new Error('markup sink ' + sink); } });
  }
  e.insertAdjacentHTML = function () { throw new Error('markup sink insertAdjacentHTML'); };
  e.appendChild = function (c) {
    e.children.push(c); c.parentNode = e;
    if (tag === 'select' && e._picked !== true) { e.value = c.value; e._picked = true; } // a real select starts on its first option
    return c;
  };
  e.setAttribute = function () {};
  e.addEventListener = function (n, f) { e.listeners[n] = f; };
  Object.defineProperty(e, 'textContent', {
    get() { return e._text; },
    set(v) { e._text = String(v); e.children = []; },
  });
  return e;
}
global.window = global;
global.document = { createElement: mk };
for (const f of process.argv.slice(2)) eval(fs.readFileSync(f, 'utf8'));

function walk(n, f) { f(n); n.children.forEach(function (c) { walk(c, f); }); }
function texts(n) { const out = []; walk(n, function (x) { if (x._text) out.push(x._text); }); return out; }
function find(n, cls) { let hit = null; walk(n, function (x) { if (!hit && x.className === cls) hit = x; }); return hit; }
function buttons(n, label) { const out = []; walk(n, function (x) { if (x.tag === 'button' && x._text === label) out.push(x); }); return out; }
function assert(c, m) { if (!c) throw new Error(m); }
const tick = function () { return new Promise(function (r) { setTimeout(r, 15); }); };

const evil = '<img src=x onerror=alert(1)>';
function overview(canWrite) {
  return {
    writes_enabled: canWrite, can_write: canWrite, privileged_hidden: false,
    providers: [], aliases: [{ alias: 'best', by: {} }, { alias: 'fast', by: {} }],
    models: [
      { id: 'gpt-5.5', harness: 'codex', billing: 'api', billing_by: 'overlay', enabled: true, enabled_by: 'catalog', aliases: [] },
      { id: evil, harness: 'agy', billing: 'subscription', billing_by: 'catalog', enabled: true, enabled_by: 'catalog', aliases: [] },
    ],
    router: { sha: '', pins: [], rules: [], allow_unsandboxed_runtimes: [], gateway_classes: [], warnings: [] },
    budgets: [], evals: [], sensitive: {},
  };
}

// The server stub: a script of replies per "METHOD path".
let script = {};
const calls = [];
function fetchFn(method, path, body, extra) {
  calls.push({ method, path, body, headers: (extra && extra.headers) || {}, keep401: !!(extra && extra.keep401) });
  const key = method + ' ' + path;
  let reply = script[key];
  if (Array.isArray(reply)) reply = reply.length > 1 ? reply.shift() : reply[0];
  if (!reply) return Promise.reject(new Error('unexpected call ' + key));
  return Promise.resolve({ ok: reply.status < 400, status: reply.status, json: function () { return Promise.resolve(reply.body); } });
}
const ov = function () { return { status: 200, body: overview(true) }; };
const perf = { status: 200, body: [] };

async function main() {
  // 1. can_write false: no panel, nothing but GETs.
  script = { 'GET /api/models/overview': { status: 200, body: overview(false) }, 'GET /perf/api/perf/by_axis?axis=model&window=30d': perf };
  let holder = mk('div');
  await window.YakModels.open(holder, fetchFn);
  assert(!find(holder, 'models-section models-write'), 'write panel drawn without can_write');
  assert(texts(holder).join('\n').indexOf('--console-model-writes') >= 0, 'no hint how to turn writes on');
  assert(calls.every(function (c) { return c.method === 'GET'; }), 'a non-GET without can_write');

  // 2. can_write: panel drawn, evil strings only through textContent.
  calls.length = 0;
  script = {
    'GET /api/models/overview': ov(), 'GET /perf/api/perf/by_axis?axis=model&window=30d': perf,
    'GET /api/models/write-session': { status: 200, body: { csrf_token: 'TOK1', step_up_method: 'password' } },
    'PUT /api/models/disable': [
      { status: 401, body: { error: 'step_up_required', method: 'password' } },
      { status: 200, body: { ok: true, changed: true, changes: [] } },
    ],
    'POST /api/models/step-up': [{ status: 401, body: { error: 'step_up_failed' } }, { status: 200, body: { ok: true, expires_in: 300 } }],
  };
  holder = mk('div');
  await window.YakModels.open(holder, fetchFn);
  const panel = find(holder, 'models-section models-write');
  assert(panel, 'write panel missing with can_write');
  assert(texts(panel).join('\n').indexOf('router-policy pins only') >= 0, 'pin-scope note missing');
  assert(texts(holder).join('\n').indexOf('Browser writes are on') >= 0, 'banner does not say writes are on');

  // Disable the first model: mint, then PUT with the CSRF header; step-up is required.
  buttons(panel, 'Disable')[0].listeners.click();
  await tick();
  const put1 = calls.filter(function (c) { return c.method === 'PUT'; });
  assert(put1.length === 1 && put1[0].path === '/api/models/disable' && put1[0].body.id === 'gpt-5.5', 'first PUT: ' + JSON.stringify(put1));
  assert(put1[0].headers['X-CSRF-Token'] === 'TOK1' && put1[0].keep401, 'PUT lacks the CSRF header or keep401');
  assert(calls.filter(function (c) { return c.path === '/api/models/write-session'; }).length === 1, 'minted not once');
  const step = find(panel, 'models-write-step');
  const secretInput = find(step, 'models-write-input');
  assert(secretInput && secretInput.type === 'password', 'no password prompt after step_up_required');

  // A wrong password: refused, input cleared, nothing stored.
  secretInput.value = 'hunter2';
  buttons(step, 'Re-authenticate')[0].listeners.click();
  await tick();
  assert(secretInput.value === '', 'the secret stays in the input');
  const su1 = calls.filter(function (c) { return c.path === '/api/models/step-up'; });
  assert(su1.length === 1 && su1[0].method === 'POST' && su1[0].body.password === 'hunter2', 'step-up call: ' + JSON.stringify(su1));
  assert(texts(find(panel, 'models-warn models-write-status') || panel).join(' ').indexOf('failed') >= 0, 'failure not shown');
  // The right one: then the retry succeeds and the read side reloads.
  secretInput.value = 'right';
  buttons(step, 'Re-authenticate')[0].listeners.click();
  await tick();
  const overviewsBefore = calls.filter(function (c) { return c.path === '/api/models/overview'; }).length;
  buttons(panel, 'Disable')[0].listeners.click();
  await tick();
  assert(texts(panel).join(' ').indexOf('Saved.') >= 0, 'success not shown: ' + texts(panel).join('|'));
  assert(calls.filter(function (c) { return c.path === '/api/models/overview'; }).length === overviewsBefore + 1, 'no reload after a change');

  // 3. Client-side validation never reaches the network.
  const n0 = calls.length;
  const inputs = [];
  walk(panel, function (x) { if (x.tag === 'input') inputs.push(x); });
  const agentIn = inputs.find(function (i) { return i.placeholder === 'agent name'; });
  agentIn.value = '../etc/passwd';
  buttons(panel, 'Pin')[0].listeners.click();
  buttons(panel, 'Clear pin')[0].listeners.click();
  const priceIn = inputs.find(function (i) { return i.placeholder === 'input'; });
  priceIn.value = 'abc';
  buttons(panel, 'Set price')[0].listeners.click();
  priceIn.value = '-3';
  buttons(panel, 'Set price')[0].listeners.click();
  const aliasIn = inputs.find(function (i) { return i.placeholder === 'model id, or default'; });
  aliasIn.value = '../x';
  buttons(panel, 'Set alias')[0].listeners.click();
  const ta = find(panel, 'models-write-rules');
  ta.value = '- {match: {}, action: {runtime: codex}}';
  buttons(panel, 'Save rules')[0].listeners.click(); // checkbox not ticked
  await tick();
  assert(calls.length === n0, 'invalid input was sent: ' + JSON.stringify(calls.slice(n0)));

  // 4. A server error string is shown as text; a CSRF refusal clears the token.
  script['PUT /api/models/enable'] = { status: 403, body: { error: evil } };
  script['GET /api/models/write-session'] = { status: 200, body: { csrf_token: 'TOK2', step_up_method: 'password' } };
  buttons(panel, 'Enable')[0].listeners.click();
  await tick();
  assert(texts(panel).join(' ').indexOf(evil) >= 0, 'server error not shown as text');
  script['PUT /api/models/enable'] = { status: 200, body: { ok: true, changed: false, changes: [] } };
  buttons(panel, 'Enable')[0].listeners.click();
  await tick();
  const last = calls.filter(function (c) { return c.method === 'PUT'; }).pop();
  assert(last.headers['X-CSRF-Token'] === 'TOK2', 'token not re-minted after a 403: ' + last.headers['X-CSRF-Token']);
  assert(texts(panel).join(' ').indexOf('No change') >= 0, 'unchanged result not shown');

  // 5. Rules: ticked box sends the YAML text as rules_yaml, nothing else.
  inputs.find(function (i) { return i.type === 'checkbox'; }).checked = true;
  script['PUT /api/router/policy'] = { status: 200, body: { ok: true, changed: true, changes: [] } };
  buttons(panel, 'Save rules')[0].listeners.click();
  await tick();
  const pol = calls.filter(function (c) { return c.path === '/api/router/policy'; }).pop();
  assert(pol && pol.method === 'PUT' && Object.keys(pol.body).join() === 'rules_yaml' && pol.body.rules_yaml.indexOf('runtime: codex') >= 0, 'rules call: ' + JSON.stringify(pol));

  // 6. Methods and paths the panel may use, and no others.
  const allowed = { 'GET /api/models/write-session': 1, 'GET /api/models/overview': 1, 'GET /perf/api/perf/by_axis?axis=model&window=30d': 1,
    'POST /api/models/step-up': 1, 'PUT /api/models/enable': 1, 'PUT /api/models/disable': 1, 'PUT /api/router/policy': 1 };
  calls.forEach(function (c) { assert(allowed[c.method + ' ' + c.path], 'unexpected call ' + c.method + ' ' + c.path); });
  console.log('ok');
}
main().catch(function (e) { console.error(e.stack || e.message); process.exit(1); });
