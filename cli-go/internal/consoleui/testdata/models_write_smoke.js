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
const ovNew = function () {
  const b = overview(true);
  b.models.push({ id: 'brand-new-model', harness: 'codex', billing: 'api', billing_by: 'overlay', enabled: true, enabled_by: 'catalog', aliases: [] });
  return { status: 200, body: b };
};
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
    'GET /api/models/overview': [ov(), ovNew()], 'GET /perf/api/perf/by_axis?axis=model&window=30d': perf,
    'GET /api/models/write-session': { status: 200, body: { csrf_token: 'TOK1', step_up_method: 'password', policy_sha: 'SHA1', rules_yaml: '- {match: {agent: a}}\n' } },
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

  // The rules editor starts from the current rules, not blank.
  await tick();
  assert(find(panel, 'models-write-rules').value === '- {match: {agent: a}}\n', 'rules editor not prefilled: ' + find(panel, 'models-write-rules').value);
  // Disable the first model: mint, then PUT with the CSRF header; step-up is required.
  buttons(panel, 'Disable')[0].listeners.click();
  await tick();
  const put1 = calls.filter(function (c) { return c.method === 'PUT'; });
  assert(put1.length === 1 && put1[0].path === '/api/models/disable' && put1[0].body.id === 'gpt-5.5', 'first PUT: ' + JSON.stringify(put1));
  assert(put1[0].headers['X-CSRF-Token'] === 'TOK1' && put1[0].keep401, 'PUT lacks the CSRF header or keep401');
  assert(calls.filter(function (c) { return c.path === '/api/models/write-session'; }).length === 1, 'write-session fetched more than once before the first write');
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

  // A saved change rebuilds the selects from the reloaded overview.
  await tick();
  const sel = [];
  walk(panel, function (x) { if (x.tag === 'select') sel.push(x); });
  const offered = [];
  sel[0].children.forEach(function (o) { offered.push(o.value); });
  assert(offered.indexOf('brand-new-model') >= 0, 'selects stale after a save: ' + offered);

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
  script['GET /api/models/write-session'] = { status: 200, body: { csrf_token: 'TOK2', step_up_method: 'password', policy_sha: 'SHA1', rules_yaml: '- {match: {agent: a}}\n' } };
  buttons(panel, 'Enable')[0].listeners.click();
  await tick();
  assert(texts(panel).join(' ').indexOf(evil) >= 0, 'server error not shown as text');
  script['PUT /api/models/enable'] = { status: 200, body: { ok: true, changed: false, changes: [] } };
  buttons(panel, 'Enable')[0].listeners.click();
  await tick();
  const last = calls.filter(function (c) { return c.method === 'PUT'; }).pop();
  assert(last.headers['X-CSRF-Token'] === 'TOK2', 'token not re-minted after a 403: ' + last.headers['X-CSRF-Token']);
  assert(texts(panel).join(' ').indexOf('No change') >= 0, 'unchanged result not shown');

  // 4b. A same-tab change (or a re-mint) must not move the sha under the text:
  // the box still holds the rules loaded at SHA1, so a save cites SHA1 (the server
  // answers 409 once the policy has moved), never the fresher sha.
  script['GET /api/models/write-session'] = { status: 200, body: { csrf_token: 'TOK2', step_up_method: 'password', policy_sha: 'SHA9', rules_yaml: '- {newer: 1}\n' } };
  script['PUT /api/models/disable'] = { status: 200, body: { ok: true, changed: true, changes: [] } };
  const boxText = find(panel, 'models-write-rules').value;
  buttons(panel, 'Disable')[0].listeners.click();
  await tick();
  await tick();
  assert(find(panel, 'models-write-rules').value === boxText, 'a refresh replaced the editor text');
  inputs.find(function (i) { return i.type === 'checkbox'; }).checked = true;
  script['PUT /api/router/policy'] = { status: 409, body: { error: 'changed', sha: 'SHA9' } };
  buttons(panel, 'Save rules')[0].listeners.click();
  await tick();
  assert(calls.filter(function (c) { return c.path === '/api/router/policy'; }).pop().body.base_sha === 'SHA1', 'save cited a sha newer than its text');
  assert(find(panel, 'models-write-rules').value === boxText, '409 replaced the editor text');
  script['GET /api/models/write-session'] = { status: 200, body: { csrf_token: 'TOK2', step_up_method: 'password', policy_sha: 'SHA1', rules_yaml: '- {match: {agent: a}}\n' } };

  // 5. Rules: ticked box sends the YAML text and the sha it was loaded from.
  inputs.find(function (i) { return i.type === 'checkbox'; }).checked = true;
  script['PUT /api/router/policy'] = { status: 200, body: { ok: true, changed: true, changes: [] } };
  buttons(panel, 'Save rules')[0].listeners.click();
  await tick();
  const pol = calls.filter(function (c) { return c.path === '/api/router/policy'; }).pop();
  assert(pol && pol.method === 'PUT' && Object.keys(pol.body).sort().join() === 'base_sha,rules_yaml' && pol.body.rules_yaml.indexOf('runtime: codex') >= 0 && pol.body.base_sha === 'SHA1', 'rules call: ' + JSON.stringify(pol));
  // A 409 says so and leaves the user's text alone until they ask for a reload.
  script['PUT /api/router/policy'] = { status: 409, body: { error: 'changed', sha: 'SHA2' } };
  ta.value = 'my edit';
  buttons(panel, 'Save rules')[0].listeners.click();
  await tick();
  assert(texts(panel).join(' ').indexOf('policy changed since you loaded it') >= 0 && ta.value === 'my edit', '409 handling');
  script['GET /api/models/write-session'] = { status: 200, body: { csrf_token: 'TOK3', policy_sha: 'SHA2', rules_yaml: '- {fresh: 1}\n' } };
  buttons(panel, 'Reload current rules')[0].listeners.click();
  await tick();
  assert(ta.value === '- {fresh: 1}\n', 'reload did not replace the editor text');
  script['PUT /api/router/policy'] = { status: 200, body: { ok: true, changed: true, changes: [] } };
  buttons(panel, 'Save rules')[0].listeners.click();
  await tick();
  assert(calls.filter(function (c) { return c.path === '/api/router/policy'; }).pop().body.base_sha === 'SHA2', 'save after reload cites the old sha');

  // 6. Methods and paths the panel may use, and no others.
  const allowed = { 'GET /api/models/write-session': 1, 'GET /api/models/overview': 1, 'GET /perf/api/perf/by_axis?axis=model&window=30d': 1,
    'POST /api/models/step-up': 1, 'PUT /api/models/enable': 1, 'PUT /api/models/disable': 1, 'PUT /api/router/policy': 1 };
  calls.forEach(function (c) { assert(allowed[c.method + ' ' + c.path], 'unexpected call ' + c.method + ' ' + c.path); });
  console.log('ok');
}
main().catch(function (e) { console.error(e.stack || e.message); process.exit(1); });
