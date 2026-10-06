// render_check.js - behaviour test for dist/app.js, the Performance dashboard.
//
// It runs the real dist/app.js in a Node vm sandbox against a small fake DOM and
// canned API payloads, then inspects the HTML the page rendered. The fake DOM is
// built from dist/index.html (element ids, initial classes, the selected chart
// metric), so an id that app.js uses and the page lacks fails here just as it
// would in a browser.
//
// Run by TestDashboardRendering (render_check_test.go), which skips when node is
// not on PATH:
//
//   node render_check.js <dist/app.js> <dist/index.html>
//
// Exit 0 when every check passes; exit 1 and a list of failures otherwise.

'use strict';

const fs = require('fs');
const vm = require('vm');

const appSource = fs.readFileSync(process.argv[2], 'utf8');
const indexHtml = fs.readFileSync(process.argv[3], 'utf8');

const failures = [];
let checks = 0;
function check(cond, msg) {
  checks++;
  if (!cond) failures.push(msg);
}

// ---- fake DOM built from index.html ----------------------------------------

// attrFlags returns the bare (boolean) attributes of a tag's attribute text.
function attrFlags(attrs) {
  const bare = attrs.replace(/"[^"]*"/g, '""');
  const flags = {};
  ['hidden', 'disabled', 'selected'].forEach(f => {
    flags[f] = new RegExp('(^|\\s)' + f + '(\\s|$)').test(bare);
  });
  return flags;
}

function parseIndex(html) {
  const specs = {};
  const tag = /<([a-zA-Z][a-zA-Z0-9]*)\b([^>]*)>/g;
  let m;
  while ((m = tag.exec(html)) !== null) {
    const idm = /\bid="([^"]+)"/.exec(m[2]);
    if (!idm) continue;
    const cls = /\bclass="([^"]*)"/.exec(m[2]);
    const f = attrFlags(m[2]);
    specs[idm[1]] = {
      tag: m[1],
      classes: cls ? cls[1].split(/\s+/).filter(Boolean) : [],
      hidden: f.hidden,
      disabled: f.disabled,
      value: '',
    };
  }
  // A select's initial value is its selected option, else its first.
  const sel = /<select\b[^>]*\bid="([^"]+)"[^>]*>([\s\S]*?)<\/select>/g;
  while ((m = sel.exec(html)) !== null) {
    const opts = [];
    const opt = /<option\b([^>]*)>/g;
    let o;
    while ((o = opt.exec(m[2])) !== null) {
      const v = /\bvalue="([^"]*)"/.exec(o[1]);
      opts.push({ value: v ? v[1] : '', selected: attrFlags(o[1]).selected });
    }
    const chosen = opts.find(x => x.selected) || opts[0];
    if (chosen && specs[m[1]]) specs[m[1]].value = chosen.value;
  }
  return specs;
}

function makeElement(id, spec) {
  const classes = new Set(spec ? spec.classes : []);
  let html = '';
  const e = {
    id: id,
    tagName: spec ? spec.tag.toUpperCase() : 'DIV',
    children: [],
    attributes: {},
    listeners: {},
    style: {},
    textContent: '',
    value: spec ? spec.value : '',
    hidden: spec ? spec.hidden : false,
    disabled: spec ? spec.disabled : false,
    clientWidth: 800,
    classes: classes,
    classList: {
      add: c => { classes.add(c); },
      remove: c => { classes.delete(c); },
      contains: c => classes.has(c),
    },
    setAttribute(k, v) { e.attributes[k] = String(v); },
    getAttribute(k) { return e.attributes[k]; },
    appendChild(c) { e.children.push(c); return c; },
    addEventListener(ev, fn) { (e.listeners[ev] = e.listeners[ev] || []).push(fn); },
  };
  // Assigning innerHTML replaces the children, as in a browser.
  Object.defineProperty(e, 'innerHTML', {
    get() { return html; },
    set(v) { html = String(v); e.children = []; },
  });
  return e;
}

function makeDocument(specs) {
  const byId = {};
  Object.keys(specs).forEach(id => { byId[id] = makeElement(id, specs[id]); });
  const doc = {
    byId: byId,
    domReady: [],
    getElementById: id => byId[id] || null,
    createElement: tag => makeElement(null, { tag: tag, classes: [], value: '' }),
    createElementNS: (ns, tag) => makeElement(null, { tag: tag, classes: [], value: '' }),
    addEventListener(ev, fn) { if (ev === 'DOMContentLoaded') doc.domReady.push(fn); },
  };
  return doc;
}

// ---- running one scenario ---------------------------------------------------

async function runScenario(payloads, preset) {
  const doc = makeDocument(parseIndex(indexHtml));
  if (preset) preset(doc);

  const fetched = [];
  const location = { hash: '', pathname: '/', search: '' };
  const sandbox = {
    document: doc,
    window: { location: location },
    location: location,
    history: { replaceState() {} },
    sessionStorage: { getItem: () => null, setItem() {} },
    setTimeout: () => 1,
    clearTimeout: () => {},
    console: console,
    fetch: async (url) => {
      fetched.push(url);
      const name = String(url).split('?')[0];
      if (!(name in payloads)) throw new Error('unexpected fetch ' + url);
      const body = JSON.stringify(payloads[name]);
      return { ok: true, status: 200, json: async () => JSON.parse(body), text: async () => body };
    },
  };
  const ctx = vm.createContext(sandbox);
  vm.runInContext(appSource, ctx, { filename: 'app.js' });
  check(doc.domReady.length === 1, 'app.js registers one DOMContentLoaded listener');
  doc.domReady.forEach(fn => fn());

  // Wait for loadAll to finish: all I/O is promise-based, so a few macrotask
  // turns drain it.
  for (let i = 0; i < 200 && !doc.byId['last-updated'].textContent; i++) {
    await new Promise(r => setImmediate(r));
  }
  const status = doc.byId['last-updated'].textContent;
  check(/^Updated /.test(status), 'dashboard finished loading, status was: ' + JSON.stringify(status));
  return { doc: doc, fetched: fetched };
}

// ---- helpers over the rendered output ---------------------------------------

const TABLES = [
  ['top-agents-head', 'top-agents-body'],
  ['top-runtimes-head', 'top-runtimes-body'],
  ['breakdown-head', 'breakdown-body'],
  ['recent-head', 'recent-body'],
];

function count(s, re) { return (s.match(re) || []).length; }

function rowsOf(doc, bodyId) { return doc.byId[bodyId].children.map(c => c.innerHTML); }

// everything the page handed to the HTML parser (innerHTML), for hostile-value
// scans. textContent is not scanned: assigning it never parses markup.
function allRendered(doc) {
  let out = '';
  Object.keys(doc.byId).forEach(id => {
    const e = doc.byId[id];
    out += '\n' + e.innerHTML;
    e.children.forEach(c => { out += '\n' + c.innerHTML; });
  });
  return out;
}

// header and cells always agree: every row has as many cells as the header has
// columns.
function checkTablesConsistent(name, doc) {
  TABLES.forEach(([h, b]) => {
    const head = doc.byId[h].innerHTML;
    const cols = count(head, /<th[ >]/g);
    check(cols > 0, name + ': ' + h + ' has columns');
    rowsOf(doc, b).forEach((row, i) => {
      if (/colspan=/.test(row)) return; // "No data" placeholder
      check(count(row, /<td[ >]/g) === cols,
        name + ': ' + b + ' row ' + i + ' has ' + count(row, /<td[ >]/g) + ' cells for ' + cols + ' columns');
    });
  });
}

const detail = (i, o, cr, cc) => ({ input: i, output: o, cache_read: cr, cache_creation: cc });

// ---- scenario A: subscription-only (tokens, no dollars) ---------------------

async function subscriptionOnly() {
  const codexD = detail(7707, 421, 7424, 0);
  const agyD = detail(12863, 1, 0, 0);
  const payloads = {
    'api/perf/summary': {
      total_dispatches: 2, total_cost_usd: 0, avg_latency_ms: 2000, p50_latency_ms: 2000, p95_latency_ms: 2000,
      total_tokens: 28416, token_detail: detail(20570, 422, 7424, 0),
      top_agents: [
        { key: 'w-codex', dispatches: 1, cost_usd: 0, tokens: 15552, token_detail: codexD },
        { key: 'w-agy', dispatches: 1, cost_usd: 0, tokens: 12864, token_detail: agyD },
      ],
      top_runtimes: [
        { key: 'codex', dispatches: 1, cost_usd: 0, tokens: 15552, token_detail: codexD },
        { key: 'agy', dispatches: 1, cost_usd: 0, tokens: 12864, token_detail: agyD },
      ],
    },
    'api/perf/timeseries': [
      { ts: '2026-10-06T09:00:00Z', value: 0 },
      { ts: '2026-10-06T10:00:00Z', value: 28416 },
    ],
    'api/perf/by_axis': [
      { key: 'codex', dispatches: 1, cost_usd: 0, avg_latency_ms: 2000, p95_latency_ms: 2000, tokens: 15552, token_detail: codexD },
      { key: 'agy', dispatches: 1, cost_usd: 0, avg_latency_ms: 2000, p95_latency_ms: 2000, tokens: 12864, token_detail: agyD },
    ],
    'api/perf/recent': [
      { ts: '2026-10-06T10:00:00Z', agent: 'w-codex', runtime: 'codex', project: 'p', exit_code: 0, duration_s: 2,
        cost_usd: 0, latency_ms: 2000, tokens: 15552, token_detail: codexD, billing: 'subscription' },
      { ts: '2026-10-06T10:01:00Z', agent: 'w-agy', runtime: 'agy', project: 'p', exit_code: 0, duration_s: 2,
        cost_usd: 0, latency_ms: 2000, tokens: 12864, token_detail: agyD, billing: 'subscription' },
    ],
  };
  const { doc, fetched } = await runScenario(payloads);
  const n = 'subscription-only';

  check(doc.byId['card-tokens'].textContent === '28,416', n + ': Tokens card is ' + doc.byId['card-tokens'].textContent);
  check(doc.byId['card-tokens-detail'].textContent === 'in 20,570 · out 422 · cache 7,424',
    n + ': token detail line is ' + doc.byId['card-tokens-detail'].textContent);
  check(doc.byId['card-dispatches'].textContent === '2', n + ': dispatches card');
  check(doc.byId['card-cost-wrap'].classes.has('hidden'), n + ': no Cost (USD) card when there is no API spend');
  check(doc.byId['card-apieq'].classes.has('hidden') && doc.byId['card-apieq'].textContent === '',
    n + ': no API-equivalent note without an api_equivalent_usd');

  TABLES.forEach(([h, b]) => {
    const head = doc.byId[h].innerHTML;
    check(/>Tokens</.test(head), n + ': ' + h + ' has a Tokens column');
    check(!/Cost \(USD\)/.test(head), n + ': ' + h + ' has no Cost (USD) column');
    check(!/API equiv/.test(head), n + ': ' + h + ' has no API equiv. column');
  });
  check(/>Billing</.test(doc.byId['recent-head'].innerHTML), n + ': Recent has a Billing column');
  check(rowsOf(doc, 'recent-body').every(r => />subscription</.test(r)), n + ': Recent rows show their billing');
  check(rowsOf(doc, 'top-runtimes-body')[0].includes('>15,552<'), n + ': codex tokens are shown in full');
  check(rowsOf(doc, 'top-runtimes-body').every(r => !/\$/.test(r)), n + ': no dollar figure in the runtime rows');
  check(/title="input 7,707 · output 421 · cache read 7,424 · cache creation 0 \(as logged\)"/
    .test(rowsOf(doc, 'top-runtimes-body')[0]), n + ': the tokens cell carries the per-kind tooltip');
  checkTablesConsistent(n, doc);

  const cost = doc.byId['metric-opt-cost'];
  check(cost.hidden === true && cost.disabled === true, n + ': the Cost (USD) chart series is hidden and disabled');
  check(doc.byId['metric-select'].value === 'tokens', n + ': tokens is the chart metric');
  check(fetched.some(u => /api\/perf\/timeseries\?.*metric=tokens/.test(u)), n + ': the chart asks for the tokens series');
  const labels = doc.byId['timeseries-svg'].children.map(c => c.textContent).filter(Boolean);
  check(labels.some(l => /^\d+(\.\d)?[kMB]$/.test(l)), n + ': chart axis labels are compact token counts, got ' + JSON.stringify(labels));
  check(!labels.some(l => /\$/.test(l)), n + ': no dollar labels on the tokens chart');
}

// ---- scenario B: some API spend (USD column appears, header and cells) ------

async function withSpend(metric) {
  const d = detail(900, 100, 0, 0);
  const subD = detail(1200, 340, 50000, 2000);
  const payloads = {
    'api/perf/summary': {
      total_dispatches: 3, total_cost_usd: 0.3, api_equivalent_usd: 0.42, avg_latency_ms: 2000, p50_latency_ms: 2000, p95_latency_ms: 2000,
      total_tokens: 54540, token_detail: detail(2100, 440, 50000, 2000),
      top_agents: [
        { key: 'w-api', dispatches: 1, cost_usd: 0.3, tokens: 1000, token_detail: d },
        { key: 'w-sub', dispatches: 1, cost_usd: 0, tokens: 53540, token_detail: subD },
      ],
      top_runtimes: [
        { key: 'claude', dispatches: 2, cost_usd: 0.3, tokens: 54540, token_detail: detail(2100, 440, 50000, 2000) },
        { key: 'codex', dispatches: 1, cost_usd: 0, tokens: 0, token_detail: detail(0, 0, 0, 0) },
      ],
    },
    'api/perf/timeseries': [{ ts: '2026-10-06T10:00:00Z', value: 0.3 }],
    'api/perf/by_axis': [
      { key: 'w-api', dispatches: 1, cost_usd: 0.3, avg_latency_ms: 2000, p95_latency_ms: 2000, tokens: 1000, token_detail: d },
      { key: 'w-sub', dispatches: 1, cost_usd: 0, avg_latency_ms: 2000, p95_latency_ms: 2000, tokens: 53540, token_detail: subD, api_equivalent_usd: 0.42 },
    ],
    'api/perf/recent': [
      { ts: '2026-10-06T10:00:00Z', agent: 'w-api', runtime: 'claude', project: 'p', exit_code: 0, duration_s: 2,
        cost_usd: 0.3, latency_ms: 2000, tokens: 1000, token_detail: d, billing: 'api' },
      { ts: '2026-10-06T10:01:00Z', agent: 'w-sub', runtime: 'claude', project: 'p', exit_code: 0, duration_s: 2,
        cost_usd: 0, latency_ms: 2000, tokens: 53540, token_detail: subD, billing: 'subscription' },
    ],
  };
  const { doc, fetched } = await runScenario(payloads, metric ? d0 => { d0.byId['metric-select'].value = metric; } : null);
  const n = 'with-spend' + (metric ? '/' + metric : '');

  check(!doc.byId['card-cost-wrap'].classes.has('hidden'), n + ': the Cost (USD) card is shown');
  check(doc.byId['card-cost'].textContent === '$0.3000', n + ': cost card value is ' + doc.byId['card-cost'].textContent);
  check(!doc.byId['card-apieq'].classes.has('hidden') &&
    doc.byId['card-apieq'].textContent === '≈ $0.4200 at API rates (not spend)',
    n + ': API-equivalent note is ' + JSON.stringify(doc.byId['card-apieq'].textContent));

  ['top-agents-head', 'top-runtimes-head', 'breakdown-head', 'recent-head'].forEach(h => {
    check(/>Cost \(USD\)</.test(doc.byId[h].innerHTML), n + ': ' + h + ' shows the Cost (USD) column');
    check(/>Tokens</.test(doc.byId[h].innerHTML), n + ': ' + h + ' still shows Tokens');
  });
  checkTablesConsistent(n, doc);

  // The cost cell of the api row, and a $0.00 (not blank) for the row without.
  check(rowsOf(doc, 'top-agents-body')[0].includes('>$0.3000<'), n + ': the api row shows its dollars');
  check(rowsOf(doc, 'top-agents-body')[1].endsWith('<td>\u2014</td>'), n + ': a row without spend shows an em dash in the Cost column');

  // API-equivalent: muted column in the breakdown only, em dash where none.
  const bh = doc.byId['breakdown-head'].innerHTML;
  check(/<th class="muted">API equiv\. \(USD\)<\/th>/.test(bh), n + ': breakdown has the muted API equiv. column: ' + bh);
  ['top-agents-head', 'top-runtimes-head', 'recent-head'].forEach(h => {
    check(!/API equiv/.test(doc.byId[h].innerHTML), n + ': ' + h + ' has no API equiv. column');
  });
  const br = rowsOf(doc, 'breakdown-body');
  check(br[1].includes('<td class="muted">$0.4200</td>'), n + ': the subscription row shows its API-equivalent muted');
  check(br[0].includes('<td class="muted">—</td>'), n + ': the api row shows an em dash for API-equivalent');

  const opt = doc.byId['metric-opt-cost'];
  check(opt.hidden === false && opt.disabled === false, n + ': the Cost (USD) chart series is offered');
  const want = metric || 'tokens';
  check(doc.byId['metric-select'].value === want, n + ': chart metric stays ' + want + ', is ' + doc.byId['metric-select'].value);
  check(fetched.some(u => u.indexOf('metric=' + want) >= 0), n + ': the chart asks for ' + want);
}

// ---- scenario C: Cost chosen, window has no spend -> falls back to tokens ----

async function costFallsBack() {
  const empty = {
    'api/perf/summary': { total_dispatches: 0, total_cost_usd: 0, avg_latency_ms: 0, p50_latency_ms: 0, p95_latency_ms: 0,
      total_tokens: 0, token_detail: detail(0, 0, 0, 0), top_agents: [], top_runtimes: [] },
    'api/perf/timeseries': [],
    'api/perf/by_axis': [],
    'api/perf/recent': [],
  };
  const { doc, fetched } = await runScenario(empty, d0 => { d0.byId['metric-select'].value = 'cost'; });
  const n = 'cost-falls-back';
  check(doc.byId['metric-select'].value === 'tokens', n + ': metric select fell back to tokens, is ' + doc.byId['metric-select'].value);
  check(fetched.every(u => !/metric=cost/.test(u)), n + ': the cost series was not requested');
  check(fetched.some(u => /metric=tokens/.test(u)), n + ': the tokens series was requested');
  check(doc.byId['card-tokens'].textContent === '0', n + ': Tokens card shows 0');

  // Empty tables: the placeholder spans exactly the visible columns.
  const span = (b) => (/colspan="(\d+)"/.exec(rowsOf(doc, b)[0] || '') || [])[1];
  check(span('top-agents-body') === '3', n + ': top agents placeholder spans 3 columns, got ' + span('top-agents-body'));
  check(span('breakdown-body') === '5', n + ': breakdown placeholder spans 5 columns, got ' + span('breakdown-body'));
  check(span('recent-body') === '8', n + ': recent placeholder spans 8 columns, got ' + span('recent-body'));
}

// ---- scenario D: hostile strings are escaped everywhere ----------------------

async function hostile() {
  const H = '<img src=x onerror=alert(1)>"\'&';
  const ESC = '&lt;img src=x onerror=alert(1)&gt;&quot;&#39;&amp;';
  const d = detail(1, 2, 3, 4);
  const payloads = {
    'api/perf/summary': {
      total_dispatches: 1, total_cost_usd: 1, api_equivalent_usd: 1, avg_latency_ms: H, p50_latency_ms: 1, p95_latency_ms: H,
      total_tokens: 10, token_detail: d,
      top_agents: [{ key: H, dispatches: 1, cost_usd: 1, tokens: 10, token_detail: d }],
      top_runtimes: [{ key: H, dispatches: 1, cost_usd: 1, tokens: 10, token_detail: d }],
    },
    'api/perf/timeseries': [{ ts: H, value: 1 }],
    'api/perf/by_axis': [
      { key: H, dispatches: 1, cost_usd: 1, avg_latency_ms: 1, p95_latency_ms: 1, tokens: 10, token_detail: d, api_equivalent_usd: 1 },
    ],
    'api/perf/recent': [
      { ts: H, agent: H, runtime: H, project: H, exit_code: H, duration_s: 1, cost_usd: 1, latency_ms: 1,
        tokens: 10, token_detail: d, billing: H },
    ],
  };
  const { doc } = await runScenario(payloads);
  const n = 'hostile';
  const rendered = allRendered(doc);

  check(!rendered.includes('<img'), n + ': a raw <img tag reached the page');
  check(!rendered.includes(H) && !rendered.includes('alert(1)>'), n + ': the raw hostile string survived');
  // Each hostile field is present, escaped.
  ['top-agents-body', 'top-runtimes-body', 'breakdown-body'].forEach(b => {
    check(rowsOf(doc, b)[0].includes('<td>' + ESC + '</td>'), n + ': ' + b + ' key cell is escaped');
  });
  const recent = rowsOf(doc, 'recent-body')[0];
  check(count(recent, new RegExp(ESC.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'), 'g')) >= 5,
    n + ': recent row escapes time, agent, runtime, project, exit and billing, found ' +
    count(recent, new RegExp(ESC.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'), 'g')));
  // Attributes cannot be broken out of: titles hold only escaped text.
  Object.keys(doc.byId).forEach(id => {
    doc.byId[id].children.forEach(c => {
      (c.innerHTML.match(/title="[^"]*"/g) || []).forEach(t => {
        check(!/[<>']/.test(t.slice(7, -1)), n + ': title attribute is not clean: ' + t);
      });
    });
  });
}

// ---- main ---------------------------------------------------------------------

(async function main() {
  try {
    await subscriptionOnly();
    await withSpend(null);
    await withSpend('cost');
    await costFallsBack();
    await hostile();
  } catch (e) {
    failures.push('scenario threw: ' + (e && e.stack || e));
  }
  if (failures.length) {
    console.error('FAIL: ' + failures.length + ' of ' + checks + ' checks failed');
    failures.forEach(f => console.error(' - ' + f));
    process.exit(1);
  }
  console.log('OK: ' + checks + ' checks passed');
})();
