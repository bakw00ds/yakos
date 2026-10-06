// render_check.js - behaviour harness for dist/index.html, the page the console
// iframes as its Cost tab (K-136: tokens first, dollars only for API spend).
//
//   node render_check.js <path-to-index.html> <scenario>
//
// It reads the page's single inline <script> from the file (the source is not
// duplicated here), runs it in a vm sandbox with a tiny fake DOM and a canned
// fetch, waits for the page to finish loading, and asserts on what it rendered.
// Exit 0 means the scenario passed; any failure (including a syntax error in
// the script, or the page falling into its "Failed to load" path) exits 1.

'use strict';

const fs = require('fs');
const vm = require('vm');

const failures = [];
function check(cond, msg) { if (!cond) failures.push(msg); }
function die(msg) { process.stderr.write('render_check: ' + msg + '\n'); process.exit(1); }

const htmlPath = process.argv[2];
const scenarioName = process.argv[3];
if (!htmlPath || !scenarioName) die('usage: node render_check.js <index.html> <scenario>');

const html = fs.readFileSync(htmlPath, 'utf8');
const scriptMatch = html.match(/<script>([\s\S]*?)<\/script>/);
if (!scriptMatch) die('no inline <script> in ' + htmlPath);

// ---- fake DOM ----------------------------------------------------------------
// Elements are created on demand. The initial hidden / display:none state is
// read from the page's own markup so "the page must call show()" is meaningful.
const initial = new Map(); // id -> { hidden: bool, displayNone: bool }
for (const tag of html.match(/<[a-zA-Z][^>]*>/g) || []) {
  const id = (tag.match(/\bid="([^"]+)"/) || [])[1];
  if (!id) continue;
  const cls = (tag.match(/\bclass="([^"]*)"/) || [])[1] || '';
  initial.set(id, {
    classes: cls.split(/\s+/).filter(Boolean),
    displayNone: /style="[^"]*display:\s*none/.test(tag),
  });
}

const els = new Map();
function getEl(id) {
  if (els.has(id)) return els.get(id);
  const init = initial.get(id) || { classes: [], displayNone: false };
  const classes = new Set(init.classes);
  const e = {
    id,
    innerHTML: '',
    textContent: '',
    value: '',
    style: init.displayNone ? { display: 'none' } : {},
    _classes: classes,
    classList: {
      add: c => { classes.add(c); },
      remove: c => { classes.delete(c); },
      contains: c => classes.has(c),
    },
    setAttribute() {},
    getAttribute() { return null; },
    addEventListener() {},
    appendChild() {},
  };
  els.set(id, e);
  return e;
}
const isHidden = id => getEl(id)._classes.has('hidden');

const document = { getElementById: getEl, body: { innerHTML: '' } };

// ---- scenarios ---------------------------------------------------------------
const NO_SNAPSHOT = { message: 'no snapshots yet - run: yakos metrics collect' };
const HOSTILE = '<img src=x onerror=alert(1)>"\'&';

function liveOf(runtimes, extra) {
  const total = runtimes.reduce((n, r) => n + r.tokens, 0);
  return Object.assign({
    total_cost_usd: runtimes.reduce((n, r) => n + r.usd, 0),
    event_count: runtimes.reduce((n, r) => n + r.dispatches, 0),
    tokens: { input: total, output: 0, cache_read: 0, cache_creation: 0 },
    total_tokens: total,
    api_equivalent_usd: runtimes.reduce((n, r) => n + r.api_equivalent_usd, 0),
    runtimes,
  }, extra || {});
}

const scenarios = {
  // Subscription runs only: tokens everywhere, not one dollar of spend. The
  // claude row has an API-equivalent figure, which is shown muted and labelled
  // as not spend; there is NO USD column.
  subscription_only: {
    live: {
      total_cost_usd: 0, event_count: 3,
      tokens: { input: 1500, output: 700, cache_read: 8000, cache_creation: 100 },
      total_tokens: 10300, api_equivalent_usd: 0.42,
      runtimes: [
        { runtime: 'codex', dispatches: 2, tokens: 7000, usd: 0, api_equivalent_usd: 0 },
        { runtime: 'claude', dispatches: 1, tokens: 3300, usd: 0, api_equivalent_usd: 0.42 },
      ],
    },
    verify(r) {
      check(r.tokensCard === '10,300', 'tokens card = ' + JSON.stringify(r.tokensCard) + ', want "10,300"');
      check(r.tokensSub.includes('in 1,500') && r.tokensSub.includes('out 700') && r.tokensSub.includes('cache 8,100'),
        'tokens sub-line lacks the in/out/cache split: ' + r.tokensSub);
      check(r.tokensSub.includes('(as logged)'), 'split is not labelled "as logged": ' + r.tokensSub);
      check(r.tokensSub.includes('~$0.4200 at API rates (not spend)'),
        'api-equivalent note missing or not labelled as not spend: ' + r.tokensSub);
      check(!isHidden('runtime-section'), 'runtime section is still hidden');
      check(count(r.head, /<th\b/g) === 4, 'header has ' + count(r.head, /<th\b/g) + ' columns, want 4 (Runtime, Dispatches, Tokens, API equiv.)');
      check(!/>USD</.test(r.head), 'a USD column appears although no runtime has API spend: ' + r.head);
      check(r.head.includes('>Tokens<'), 'no Tokens column');
      check(r.head.includes('API equiv. (not spend)'), 'the API-equivalent column is not labelled as not spend');
      for (const row of r.rows) check(count(row, /<td\b/g) === 4, 'row has ' + count(row, /<td\b/g) + ' cells, want 4: ' + row);
      const codex = r.rows.find(x => x.includes('>codex<'));
      check(codex && codex.includes('>7,000<') && !codex.includes('$'), 'codex row must show tokens and no dollars: ' + codex);
    },
  },

  // codex only: nothing to show but tokens. No dollar sign anywhere in the table.
  codex_only: {
    live: liveOf([{ runtime: 'codex', dispatches: 4, tokens: 20000, usd: 0, api_equivalent_usd: 0 }]),
    verify(r) {
      check(r.tokensCard === '20,000', 'tokens card = ' + JSON.stringify(r.tokensCard));
      check(!r.tokensSub.includes('$'), 'a dollar figure appears with no API spend and no API-equivalent: ' + r.tokensSub);
      check(count(r.head, /<th\b/g) === 3, 'header has ' + count(r.head, /<th\b/g) + ' columns, want 3');
      check(!r.head.includes('USD') && !r.head.includes('API equiv'), 'dollar columns appear for a tokens-only log: ' + r.head);
      check(!r.body.includes('$'), 'a dollar figure is rendered for a codex-only log: ' + r.body);
    },
  },

  // An API-billed runtime: the USD header and its cells appear together, and a
  // subscription runtime beside it shows a dash, not a dollar figure.
  api_billed: {
    live: liveOf([
      { runtime: 'claude', dispatches: 2, tokens: 5000, usd: 0.3012, api_equivalent_usd: 0 },
      { runtime: 'codex', dispatches: 1, tokens: 900, usd: 0, api_equivalent_usd: 0 },
    ]),
    verify(r) {
      const ths = count(r.head, /<th\b/g);
      check(ths === 4 && />USD</.test(r.head), 'header must be Runtime, Dispatches, Tokens, USD: ' + r.head);
      for (const row of r.rows) check(count(row, /<td\b/g) === ths, 'row cells (' + count(row, /<td\b/g) + ') do not match header columns (' + ths + '): ' + row);
      const claude = r.rows.find(x => x.includes('>claude<'));
      const codex = r.rows.find(x => x.includes('>codex<'));
      check(claude && claude.includes('>$0.3012<'), 'claude row lacks its API spend: ' + claude);
      check(codex && !codex.includes('$') && codex.includes('>—<'), 'codex row must show a dash, not dollars: ' + codex);
      check(!r.head.includes('API equiv'), 'API-equivalent column appears with nothing to show');
    },
  },

  // A runtime name from the log is hostile markup: it must come out escaped in
  // the table, never raw, and non-numeric "numbers" must not inject either.
  hostile_runtime: {
    live: {
      total_cost_usd: 1, event_count: 2,
      tokens: { input: 10, output: 0, cache_read: 0, cache_creation: 0 },
      total_tokens: 10, api_equivalent_usd: 0.5,
      runtimes: [
        { runtime: HOSTILE, dispatches: '<b>1</b>', tokens: '<i>9</i>', usd: 1, api_equivalent_usd: 0.5 },
      ],
    },
    verify(r) {
      check(!/<img/i.test(r.body), 'raw <img appears in the runtime table: ' + r.body);
      check(!/<b>|<i>/i.test(r.body), 'raw <b>/<i> appears in the runtime table: ' + r.body);
      check(r.body.includes('&lt;img src=x onerror=alert(1)&gt;&quot;\'&amp;'),
        'the runtime name is not shown escaped: ' + r.body);
      check(r.body.includes('&lt;b&gt;1&lt;/b&gt;'), 'a non-numeric dispatch count is not escaped: ' + r.body);
      check(!/<img/i.test(r.head) && !/<img/i.test(r.tokensSub), 'raw markup in head or tokens sub-line');
    },
  },

  // No log directory configured ({message}): the page still loads, with dashes.
  log_dir_not_configured: {
    live: { message: 'dispatch log directory not configured' },
    verify(r) {
      check(r.tokensCard === '—', 'tokens card = ' + JSON.stringify(r.tokensCard) + ', want an em dash');
      check(r.tokensSub === '', 'tokens sub-line should be empty: ' + r.tokensSub);
      check(isHidden('runtime-section'), 'runtime section should be hidden');
    },
  },

  // live_cost answers 500: same, the dashboard keeps working.
  live_cost_fails: {
    liveStatus: 500,
    live: { error: 'live cost: boom' },
    verify(r) {
      check(r.tokensCard === '—', 'tokens card = ' + JSON.stringify(r.tokensCard));
      check(isHidden('runtime-section'), 'runtime section should be hidden');
    },
  },

  // An older server (no token fields) is treated as "not available".
  older_server: {
    live: { total_cost_usd: 1.25, event_count: 7 },
    verify(r) {
      check(r.tokensCard === '—', 'tokens card = ' + JSON.stringify(r.tokensCard));
      check(isHidden('runtime-section'), 'runtime section should be hidden');
    },
  },

  // A malformed runtimes list never takes the page down: junk rows are skipped
  // and a non-array is treated as "no rows".
  malformed_runtimes: {
    live: {
      total_cost_usd: 0, event_count: 1,
      tokens: { input: 10, output: 0, cache_read: 0, cache_creation: 0 },
      total_tokens: 10, api_equivalent_usd: 0,
      runtimes: [null, 5, 'x', { runtime: 'codex', dispatches: 1, tokens: 10, usd: 0, api_equivalent_usd: 0 }],
    },
    verify(r) {
      check(r.rows.length === 1 && r.rows[0].includes('>codex<'), 'only the well-formed row should render: ' + r.body);
    },
  },
  runtimes_not_an_array: {
    live: {
      total_cost_usd: 0, event_count: 1,
      tokens: { input: 10, output: 0, cache_read: 0, cache_creation: 0 },
      total_tokens: 10, api_equivalent_usd: 0,
      runtimes: 'nope',
    },
    verify(r) {
      check(r.tokensCard === '10', 'tokens card = ' + JSON.stringify(r.tokensCard));
      check(r.rows.length === 0, 'no rows should render for a non-array runtimes value: ' + r.body);
      check(getEl('runtime-msg').textContent === 'No dispatches yet.', 'runtime message = ' + getEl('runtime-msg').textContent);
    },
  },

  // The pre-existing snapshot cards still render from the snapshot.
  existing_cards: {
    snapshot: {
      commit: 'abcdef1234567890', branch: 'main',
      metrics: {
        efficiency: { total_cost_usd: 1.5, task_count: 7 },
        dispatch: { first_try_success_rate: 0.5 },
        test: { coverage_pct: 80 },
        security: { secret_scan_hits: 2 },
        code_quality: { lint_findings: 3 },
      },
    },
    live: liveOf([{ runtime: 'claude', dispatches: 7, tokens: 1234567, usd: 1.5, api_equivalent_usd: 0 }]),
    verify(r) {
      check(getEl('card-cost').textContent === '1.50', 'cost card = ' + getEl('card-cost').textContent);
      check(getEl('card-tasks').textContent === '7', 'tasks card = ' + getEl('card-tasks').textContent);
      check(getEl('card-success').textContent === '50.0%', 'success card = ' + getEl('card-success').textContent);
      check(getEl('card-coverage').textContent === '80.0%', 'coverage card = ' + getEl('card-coverage').textContent);
      check(getEl('card-secrets').textContent === '2', 'secrets card = ' + getEl('card-secrets').textContent);
      check(getEl('card-lint').textContent === '3', 'lint card = ' + getEl('card-lint').textContent);
      check(r.tokensCard === '1,234,567', 'tokens card = ' + JSON.stringify(r.tokensCard));
      check(getEl('project-label').textContent.includes('abcdef12'), 'project label = ' + getEl('project-label').textContent);
    },
  },
};

function count(s, re) { return (s.match(re) || []).length; }

const sc = scenarios[scenarioName];
if (!sc) die('unknown scenario ' + scenarioName + '; have ' + Object.keys(scenarios).join(', '));

// ---- canned fetch -------------------------------------------------------------
function response(status, body) {
  return {
    ok: status >= 200 && status < 300,
    status,
    statusText: 'canned',
    json: async () => JSON.parse(JSON.stringify(body)),
    text: async () => JSON.stringify(body),
  };
}
const routes = {
  'api/metrics/snapshot': () => response(200, sc.snapshot || NO_SNAPSHOT),
  'api/metrics/history': () => response(200, []),
  'api/metrics/trend': () => response(200, []),
  'api/metrics/live_cost': () => response(sc.liveStatus || 200, sc.live),
  'api/metrics/projects': () => response(404, { error: 'not found' }),
};
const fetched = [];
async function fakeFetch(url) {
  const path = String(url).split('?')[0];
  fetched.push(path);
  const route = routes[path];
  return route ? route() : response(404, { error: 'no route ' + path });
}

getEl('trend-metric').value = 'efficiency.total_cost_usd';
getEl('trend-last').value = '10';

const sandbox = {
  document,
  window: { location: { hash: '', pathname: '/', search: '' } },
  sessionStorage: { getItem: () => null, setItem() {} },
  history: { replaceState() {} },
  fetch: fakeFetch,
  setInterval: () => 0, // auto-refresh never fires in the harness
  console,
};
vm.createContext(sandbox);

(async () => {
  // A syntax error in the page throws here and fails the scenario.
  vm.runInContext(scriptMatch[1], sandbox, { filename: 'index.html:<script>' });

  // Wait for loadAll() to reach its last statement.
  let loaded = false;
  for (let i = 0; i < 400 && !loaded; i++) {
    await new Promise(r => setImmediate(r));
    loaded = getEl('last-updated').textContent.startsWith('Updated');
  }
  await new Promise(r => setImmediate(r)); // let boot() finish wiring after loadAll

  check(loaded, 'the page never finished loading (last-updated was not set)');
  check(document.body.innerHTML === '', 'the page fell into its "Failed to load" path: ' + document.body.innerHTML);
  check(fetched.includes('api/metrics/live_cost'), 'the page never asked for api/metrics/live_cost');
  check(!isHidden('dashboard'), 'the dashboard was not shown');

  const body = getEl('runtime-body').innerHTML;
  const r = {
    tokensCard: getEl('card-tokens').textContent,
    tokensSub: getEl('card-tokens-sub').textContent,
    head: getEl('runtime-head').innerHTML,
    body,
    rows: body.match(/<tr>[\s\S]*?<\/tr>/g) || [],
  };
  sc.verify(r);

  if (failures.length) {
    process.stderr.write('scenario ' + scenarioName + ' FAILED:\n  - ' + failures.join('\n  - ') + '\n');
    process.exit(1);
  }
  process.stdout.write('scenario ' + scenarioName + ' ok\n');
})().catch(e => { process.stderr.write('render_check: ' + (e && e.stack || e) + '\n'); process.exit(1); });
