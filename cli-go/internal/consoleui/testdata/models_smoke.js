// Runs dist/models.js against a minimal fake DOM. innerHTML, outerHTML and
// insertAdjacentHTML throw, so any markup sink fails the test. The fetch stub
// records every call: the tab may only GET.
'use strict';
const fs = require('fs');
const src = fs.readFileSync(process.argv[2], 'utf8');

function mk(tag) {
  const e = { tag, children: [], className: '', _text: '', listeners: {}, value: '' };
  for (const sink of ['innerHTML', 'outerHTML']) {
    Object.defineProperty(e, sink, { set() { throw new Error('markup sink ' + sink); } });
  }
  e.insertAdjacentHTML = function () { throw new Error('markup sink insertAdjacentHTML'); };
  e.appendChild = function (c) { e.children.push(c); c.parentNode = e; return c; };
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
eval(src);

function walk(n, f) { f(n); n.children.forEach(function (c) { walk(c, f); }); }
function texts(n) { const out = []; walk(n, function (x) { if (x._text) out.push(x._text); }); return out; }
function find(n, cls) { let hit = null; walk(n, function (x) { if (!hit && x.className === cls) hit = x; }); return hit; }
function assert(c, m) { if (!c) throw new Error(m); }

const evil = '<img src=x onerror=alert(1)>';
const overview = {
  writes_enabled: false,
  providers: [
    { harness: 'claude', provider: 'anthropic', installed: true, signed_in: true, cooling: false, cooldown_seconds: 0 },
    { harness: 'codex', provider: 'openai', installed: true, signed_in: false, cooling: true, cooldown_seconds: 42, hint: 'set OPENAI_API_KEY ' + evil },
  ],
  models: [
    { id: 'gpt-5.5', harness: 'codex', billing: 'api', billing_by: 'overlay', enabled: true, enabled_by: 'catalog',
      availability: { state: 'yes' }, cost: { input: 5, output: 25 }, aliases: ['best'] },
    { id: evil, harness: 'agy', billing: 'subscription', billing_by: 'catalog', enabled: false, enabled_by: 'overlay', aliases: [] },
  ],
  aliases: [{ alias: 'best', by: { claude: 'opus', codex: 'gpt-5.5', agy: '' } }],
  router: {
    sha: 'a'.repeat(64), pins: [{ agent: evil, runtime: 'codex', model: 'gpt-5.5' }],
    rules: [{ id: 'R1', match: { class: 'chat' }, action: { runtime: 'claude', model: 'sonnet' }, override_pins: false }],
    allow_unsandboxed_runtimes: ['codex'], hooks_endpoint: false, openai_endpoint: true,
    gateway_classes: [{ class: 'opus', env: 'ANTHROPIC_DEFAULT_OPUS_MODEL', model: 'claude-opus-4' }],
    warnings: ['router policy: ' + evil],
  },
  budgets: [{ agent: 'supervisor', state: 'ok', window: 'monthly', limit_usd: 100, spent_usd: 1.5, limit_tokens: 33000000, spent_tokens: 1500, pct: 3 }],
  evals: [{ ts: '2026-10-01T10:00:00Z', agent: 'backend', run_id: 'r1', tier_pass_rates: { sonnet: 0.9 }, candidate_tier: 'sonnet' }],
  sensitive: { class: 'sensitive', behavior: 'routes narrowly', secret_patterns: 8, never_path_patterns: 40 },
};

const body = window.YakModels.render(overview, { 'gpt-5.5': 1234567 });
const all = texts(body).join('\n');
for (const want of [evil, 'set OPENAI_API_KEY', '42s left', '$5 in / $25 out per M tokens', '1,234,567', 'ANTHROPIC_DEFAULT_OPUS_MODEL',
  'Read-only view', 'R1', 'sonnet 90%', '3%', '33,000,000', 'hooks endpoint: no'.replace('hooks endpoint', 'Hooks endpoint')]) {
  assert(all.indexOf(want) >= 0, 'missing in render: ' + want);
}
assert(all.indexOf('-') >= 0, 'dash placeholders missing');
// A null token map (perf dashboard unreadable) still renders.
assert(texts(window.YakModels.render(overview, null)).length > 0, 'render without tokens failed');
assert(texts(window.YakModels.render({}, null)).join(' ').indexOf('No budgets.') >= 0, 'empty overview not handled');
assert(JSON.stringify(window.YakModels.tokenMap([{ key: 'a', tokens: 5 }, { key: 7 }, null])) === '{"a":5}', 'tokenMap');
assert(window.YakModels.tokenMap({}) === null, 'tokenMap of a non-list');

// open(): GETs only, in the expected places, then the playground.
const calls = [];
function fetchFn(method, path) {
  calls.push(method + ' ' + path);
  let payload;
  if (path === '/api/models/overview') payload = overview;
  else if (path.indexOf('/perf/api/perf/by_axis') === 0) payload = [{ key: 'gpt-5.5', tokens: 99 }];
  else if (path.indexOf('/api/models/explain') === 0) payload = { runtime: 'codex', model: 'gpt-5.5', rule: 'R1', chain: ['codex'], skipped: [{ runtime: 'agy', cooling: true }] };
  else return Promise.reject(new Error('unexpected ' + path));
  return Promise.resolve({ ok: true, status: 200, json: function () { return Promise.resolve(payload); } });
}
const holder = mk('div');
window.YakModels.open(holder, fetchFn).then(function () {
  assert(calls[0] === 'GET /api/models/overview' && calls[1] === 'GET /perf/api/perf/by_axis?axis=model&window=30d', 'calls: ' + calls);
  assert(texts(holder).join('\n').indexOf('99') >= 0, 'token column not filled from the perf rows');
  const agent = find(holder, 'models-explain-agent');
  const cls = find(holder, 'models-explain-class');
  const go = find(holder, 'models-explain-go');
  const out = find(holder, 'models-explain-out');
  assert(agent && cls && go && out, 'playground missing');
  // Invalid input never reaches the network.
  const before = calls.length;
  agent.value = '../etc/passwd';
  go.listeners.click();
  assert(calls.length === before, 'an invalid agent was sent');
  assert(texts(out).join(' ').indexOf('Enter an agent name') >= 0, 'no validation message');
  agent.value = 'backend';
  cls.value = 'chat';
  go.listeners.click();
  return new Promise(function (r) { setTimeout(r, 20); }).then(function () {
    assert(calls[calls.length - 1] === 'GET /api/models/explain?agent=backend&class=chat', 'explain url: ' + calls[calls.length - 1]);
    const t = texts(out).join('\n');
    assert(t.indexOf('codex/gpt-5.5') >= 0 && t.indexOf('agy (cooling)') >= 0, 'explain result: ' + t);
    assert(calls.every(function (c) { return c.indexOf('GET ') === 0; }), 'the tab issued a non-GET: ' + calls);
  });
}).then(function () {
  // A failed overview leaves a message, not a stack.
  const h2 = mk('div');
  return window.YakModels.open(h2, function () { return Promise.resolve({ ok: false, status: 500 }); }).then(function () {
    assert(h2._text === 'Could not load the models overview.', 'failure text: ' + h2._text);
  });
}).then(function () { console.log('ok'); }).catch(function (e) { console.error(e.message); process.exit(1); });
