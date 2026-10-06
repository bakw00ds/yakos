/* yakOS Performance Dashboard — vanilla JS SPA
 *
 * Auth: token read from URL fragment (#token=<hex>) → sessionStorage.
 * All API calls attach "Authorization: Bearer <token>" header.
 * Auto-refresh every 30 seconds.
 * SVG timeseries chart implemented inline (no CDN dependency).
 *
 * Tokens are the primary measure (K-136). Dollars are API spend only, so the
 * Cost (USD) card, table columns and chart series exist only while some
 * displayed row has spend; subscription and local runs show tokens and no
 * dollars. API-equivalent dollars are informational, muted, never spend.
 */

'use strict';

// ---- auth -------------------------------------------------------------------
// Returns the bearer token when one is available (loopback / standalone mode),
// or null when running embedded in the session console (networked mode).
// In session mode the browser's session cookie authenticates API calls instead.

function getToken() {
  // Try sessionStorage first (so fragment isn't needed on every page load).
  let tok = sessionStorage.getItem('perf_token');
  if (tok) return tok;

  // Read from URL fragment (#token=<hex>).
  const frag = window.location.hash;
  const match = frag.match(/[#&]token=([0-9a-f]{64})/);
  if (match) {
    tok = match[1];
    sessionStorage.setItem('perf_token', tok);
    // Remove fragment from URL bar so it's not in browser history.
    history.replaceState(null, '', window.location.pathname + window.location.search);
    return tok;
  }
  return null;
}

// buildFetchOpts returns fetch init options for an API call.
// Bearer mode (token present):  Authorization header, default credentials.
// Session mode (no token):      credentials:'same-origin', no Authorization
//   header — the browser sends the session cookie so the console auth edge
//   authenticates the request.
function buildFetchOpts(tok) {
  if (tok) {
    return { headers: { 'Authorization': 'Bearer ' + tok } };
  }
  return { credentials: 'same-origin' };
}

// ---- fetch helpers ----------------------------------------------------------

async function apiFetch(path, tok) {
  const resp = await fetch(path, buildFetchOpts(tok));
  if (!resp.ok) {
    const text = await resp.text();
    throw new Error('API ' + resp.status + ': ' + text);
  }
  return resp.json();
}

// ---- DOM helpers ------------------------------------------------------------

function el(id) { return document.getElementById(id); }
function setText(id, v) { const e = el(id); if (e) e.textContent = v; }

// buildRows fills tbody with one <tr> per entry of rows (each entry is the
// cell HTML of a row, built with td()). ncols sizes the placeholder row.
function buildRows(tbody, rows, ncols) {
  tbody.innerHTML = '';
  rows.forEach(r => {
    const tr = document.createElement('tr');
    tr.innerHTML = r;
    tbody.appendChild(tr);
  });
  if (rows.length === 0) {
    const tr = document.createElement('tr');
    tr.innerHTML = '<td colspan="' + (Number(ncols) || 10) + '" style="text-align:center;color:#6b7280;padding:20px">No data</td>';
    tbody.appendChild(tr);
  }
}

// td and th are the only places a value becomes HTML, and both escape it.
// cls must be a literal from this file (never data). title is an optional
// tooltip, escaped for use inside an attribute.
function td(v, cls, title) {
  return '<td' + (cls ? ' class="' + cls + '"' : '') +
    (title ? ' title="' + escapeHTML(title) + '"' : '') + '>' + escapeHTML(v) + '</td>';
}

function th(label, cls) {
  return '<th' + (cls ? ' class="' + cls + '"' : '') + '>' + escapeHTML(label) + '</th>';
}

// escapeHTML is safe for text content and for double- or single-quoted
// attribute values.
function escapeHTML(s) {
  return String(s)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

// num coerces an API number to a finite number (0 when absent or malformed).
function num(v) {
  const n = Number(v);
  return isFinite(n) ? n : 0;
}

function fmtInt(v) {
  return num(v).toLocaleString('en-US');
}

function fmtMs(ms) {
  if (ms < 1000) return ms + ' ms';
  return (ms / 1000).toFixed(1) + ' s';
}

function fmtCost(c) {
  c = num(c);
  if (c === 0) return '$0.00';
  if (c < 0.0001) return '<$0.0001';
  return '$' + c.toFixed(4);
}

// fmtTokensCompact abbreviates a token count for chart axis labels: 1.2k, 3M.
function fmtTokensCompact(v) {
  v = num(v);
  const units = [[1e9, 'B'], [1e6, 'M'], [1e3, 'k']];
  for (let i = 0; i < units.length; i++) {
    if (v >= units[i][0]) return (v / units[i][0]).toFixed(1).replace(/\.0$/, '') + units[i][1];
  }
  return String(Math.round(v));
}

// tokenTitle is the tooltip of a tokens cell: the per-kind split as logged.
// Only the total is comparable across rows from different writers.
function tokenTitle(d) {
  if (!d) return '';
  return 'input ' + fmtInt(d.input) + ' \u00b7 output ' + fmtInt(d.output) +
    ' \u00b7 cache read ' + fmtInt(d.cache_read) + ' \u00b7 cache creation ' +
    fmtInt(d.cache_creation) + ' (as logged)';
}

function toggle(id, visible) {
  const e = el(id);
  if (!e) return;
  if (visible) e.classList.remove('hidden'); else e.classList.add('hidden');
}

// ---- tables -----------------------------------------------------------------
// Each table is described by column specs { head, cell(row), cls, show }. A
// column with show === false is left out of the header and of every row
// together, so the two can never disagree.

function renderTable(headId, bodyId, cols, rows) {
  const visible = cols.filter(c => c.show !== false);
  el(headId).innerHTML = '<tr>' + visible.map(c => th(c.head, c.cls)).join('') + '</tr>';
  buildRows(el(bodyId), rows.map(r => visible.map(c => c.cell(r)).join('')), visible.length);
}

function anyPositive(rows, key) {
  return rows.some(r => num(r[key]) > 0);
}

function tokensCol() {
  return { head: 'Tokens', cell: r => td(fmtInt(r.tokens), '', tokenTitle(r.token_detail)) };
}

// usdCol is the Cost (USD) column: API spend only, so it appears only when some
// row in the table has any. A row with none (a subscription or local run) shows
// an em dash, not $0.00: it was not billed per call, it is not a free API call.
function usdCol(rows) {
  return {
    head: 'Cost (USD)', show: anyPositive(rows, 'cost_usd'),
    cell: r => td(num(r.cost_usd) > 0 ? fmtCost(r.cost_usd) : '\u2014'),
  };
}

// apiEquivCol is the muted API-equivalent column: what subscription runs would
// have cost at API rates. Informational, never spend.
function apiEquivCol(rows) {
  return {
    head: 'API equiv. (USD)', cls: 'muted', show: anyPositive(rows, 'api_equivalent_usd'),
    cell: r => td(num(r.api_equivalent_usd) > 0 ? fmtCost(r.api_equivalent_usd) : '\u2014', 'muted'),
  };
}

function fmtTs(ts) {
  if (!ts) return '—';
  // Show only date + time (drop timezone for brevity).
  return ts.replace('T', ' ').replace(/\.\d+Z$/, '').replace('Z', '');
}

// ---- SVG chart --------------------------------------------------------------

function renderChart(svg, points, metric) {
  svg.innerHTML = '';

  const W = svg.clientWidth || 800;
  const H = 220;
  const PAD = { top: 10, right: 20, bottom: 36, left: 56 };
  const cW = W - PAD.left - PAD.right;
  const cH = H - PAD.top - PAD.bottom;

  svg.setAttribute('viewBox', '0 0 ' + W + ' ' + H);
  svg.setAttribute('height', H);

  if (!points || points.length === 0) {
    const t = document.createElementNS('http://www.w3.org/2000/svg', 'text');
    t.setAttribute('x', W / 2);
    t.setAttribute('y', H / 2);
    t.setAttribute('text-anchor', 'middle');
    t.setAttribute('fill', '#6b7280');
    t.setAttribute('font-size', '13');
    t.textContent = 'No data for this window';
    svg.appendChild(t);
    return;
  }

  const values = points.map(p => p.value);
  const maxVal = Math.max(...values, 0.001);
  const minVal = 0; // always start from 0

  function xPos(i) { return PAD.left + (i / Math.max(points.length - 1, 1)) * cW; }
  function yPos(v) { return PAD.top + cH - ((v - minVal) / (maxVal - minVal)) * cH; }

  // Gradient definition.
  const defs = document.createElementNS('http://www.w3.org/2000/svg', 'defs');
  const grad = document.createElementNS('http://www.w3.org/2000/svg', 'linearGradient');
  grad.setAttribute('id', 'area-gradient');
  grad.setAttribute('x1', '0'); grad.setAttribute('y1', '0');
  grad.setAttribute('x2', '0'); grad.setAttribute('y2', '1');
  const stop1 = document.createElementNS('http://www.w3.org/2000/svg', 'stop');
  stop1.setAttribute('offset', '0%'); stop1.setAttribute('stop-color', '#3b82f6');
  stop1.setAttribute('stop-opacity', '0.25');
  const stop2 = document.createElementNS('http://www.w3.org/2000/svg', 'stop');
  stop2.setAttribute('offset', '100%'); stop2.setAttribute('stop-color', '#3b82f6');
  stop2.setAttribute('stop-opacity', '0.03');
  grad.appendChild(stop1); grad.appendChild(stop2);
  defs.appendChild(grad);
  svg.appendChild(defs);

  // Horizontal grid lines (5 ticks).
  const ticks = 5;
  for (let i = 0; i <= ticks; i++) {
    const v = minVal + (maxVal - minVal) * (i / ticks);
    const y = yPos(v);
    // Grid line.
    const line = document.createElementNS('http://www.w3.org/2000/svg', 'line');
    line.setAttribute('x1', PAD.left); line.setAttribute('x2', PAD.left + cW);
    line.setAttribute('y1', y); line.setAttribute('y2', y);
    line.setAttribute('class', 'chart-grid');
    svg.appendChild(line);
    // Label.
    const label = document.createElementNS('http://www.w3.org/2000/svg', 'text');
    label.setAttribute('x', PAD.left - 4);
    label.setAttribute('y', y + 4);
    label.setAttribute('text-anchor', 'end');
    label.setAttribute('class', 'chart-label');
    label.textContent = fmtAxisVal(v, metric);
    svg.appendChild(label);
  }

  // Area path.
  let areaD = 'M ' + xPos(0) + ' ' + (PAD.top + cH);
  points.forEach((p, i) => { areaD += ' L ' + xPos(i) + ' ' + yPos(p.value); });
  areaD += ' L ' + xPos(points.length - 1) + ' ' + (PAD.top + cH) + ' Z';
  const area = document.createElementNS('http://www.w3.org/2000/svg', 'path');
  area.setAttribute('d', areaD);
  area.setAttribute('class', 'chart-area');
  svg.appendChild(area);

  // Line path.
  let lineD = '';
  points.forEach((p, i) => {
    lineD += (i === 0 ? 'M ' : ' L ') + xPos(i) + ' ' + yPos(p.value);
  });
  const linePath = document.createElementNS('http://www.w3.org/2000/svg', 'path');
  linePath.setAttribute('d', lineD);
  linePath.setAttribute('class', 'chart-line');
  svg.appendChild(linePath);

  // X-axis labels (up to 8 evenly spaced).
  const maxLabels = Math.min(8, points.length);
  const step = Math.max(1, Math.floor(points.length / maxLabels));
  for (let i = 0; i < points.length; i += step) {
    const x = xPos(i);
    const label = document.createElementNS('http://www.w3.org/2000/svg', 'text');
    label.setAttribute('x', x);
    label.setAttribute('y', PAD.top + cH + 18);
    label.setAttribute('text-anchor', 'middle');
    label.setAttribute('class', 'chart-label');
    label.textContent = formatBucketLabel(points[i].ts);
    svg.appendChild(label);
  }

  // Dots on data points (only if few enough).
  if (points.length <= 48) {
    points.forEach((p, i) => {
      const dot = document.createElementNS('http://www.w3.org/2000/svg', 'circle');
      dot.setAttribute('cx', xPos(i));
      dot.setAttribute('cy', yPos(p.value));
      dot.setAttribute('r', '3');
      dot.setAttribute('class', 'chart-dot');
      svg.appendChild(dot);
    });
  }
}

function fmtAxisVal(v, metric) {
  if (metric === 'cost') return '$' + v.toFixed(3);
  if (metric === 'latency') return Math.round(v) + 'ms';
  if (metric === 'tokens') return fmtTokensCompact(v);
  return Math.round(v).toString();
}

function formatBucketLabel(ts) {
  if (!ts) return '';
  // ts is RFC3339 e.g. "2026-06-03T14:00:00Z"
  const d = new Date(ts);
  if (isNaN(d.getTime())) return ts.slice(0, 10);
  const h = d.getUTCHours();
  const m = d.getUTCMinutes();
  // If midnight, show date; otherwise show hour.
  if (h === 0 && m === 0) {
    return (d.getUTCMonth() + 1) + '/' + d.getUTCDate();
  }
  return String(h).padStart(2, '0') + ':' + String(m).padStart(2, '0');
}

// ---- data loading -----------------------------------------------------------

let currentToken = null;
let refreshTimer = null;

// renderCards fills the summary cards. Tokens lead; the cost card exists only
// while the window has API spend, and the API-equivalent note only when some
// subscription run reported one.
function renderCards(summary, hasSpend) {
  const d = summary.token_detail || {};
  setText('card-tokens', fmtInt(summary.total_tokens));
  setText('card-tokens-detail', 'in ' + fmtInt(d.input) + ' \u00b7 out ' + fmtInt(d.output) +
    ' \u00b7 cache ' + fmtInt(num(d.cache_read) + num(d.cache_creation)));
  setText('card-dispatches', fmtInt(summary.total_dispatches));
  setText('card-cost', fmtCost(summary.total_cost_usd));
  toggle('card-cost-wrap', hasSpend);
  const eq = num(summary.api_equivalent_usd);
  setText('card-apieq', eq > 0 ? '\u2248 ' + fmtCost(eq) + ' at API rates (not spend)' : '');
  toggle('card-apieq', eq > 0);
  setText('card-avg-latency', fmtMs(summary.avg_latency_ms));
  setText('card-p95-latency', fmtMs(summary.p95_latency_ms));
}

// syncMetricSelect offers the Cost (USD) series only while the window has API
// spend. With none it is hidden, and if it was selected the chart falls back
// to tokens.
function syncMetricSelect(hasSpend) {
  const opt = el('metric-opt-cost');
  if (opt) { opt.hidden = !hasSpend; opt.disabled = !hasSpend; }
  const sel = el('metric-select');
  if (!hasSpend && sel.value === 'cost') sel.value = 'tokens';
}

async function loadAll() {
  const tok = currentToken;
  const win = el('window-select').value;
  const bucket = el('bucket-select').value;
  const axis = el('axis-select').value;

  try {
    // Summary
    const summary = await apiFetch('api/perf/summary?window=' + win, tok);
    // total_cost_usd is API spend only (subscription and local runs add none).
    const hasSpend = num(summary.total_cost_usd) > 0;
    renderCards(summary, hasSpend);
    syncMetricSelect(hasSpend);
    const metric = el('metric-select').value;

    // Top agents
    const topAgents = summary.top_agents || [];
    renderTable('top-agents-head', 'top-agents-body', [
      { head: 'Agent', cell: a => td(a.key) },
      { head: 'Dispatches', cell: a => td(a.dispatches) },
      tokensCol(),
      usdCol(topAgents),
    ], topAgents);

    // Top runtimes
    const topRuntimes = summary.top_runtimes || [];
    renderTable('top-runtimes-head', 'top-runtimes-body', [
      { head: 'Runtime', cell: r => td(r.key) },
      { head: 'Dispatches', cell: r => td(r.dispatches) },
      tokensCol(),
      usdCol(topRuntimes),
    ], topRuntimes);

    // Timeseries
    const ts = await apiFetch(
      'api/perf/timeseries?window=' + win + '&bucket=' + bucket + '&metric=' + metric, tok
    );
    renderChart(el('timeseries-svg'), ts, metric);

    // By axis
    const byAxis = (await apiFetch('api/perf/by_axis?axis=' + axis + '&window=' + win, tok)) || [];
    renderTable('breakdown-head', 'breakdown-body', [
      { head: 'Key', cell: row => td(row.key) },
      { head: 'Dispatches', cell: row => td(row.dispatches) },
      tokensCol(),
      usdCol(byAxis),
      apiEquivCol(byAxis),
      { head: 'Avg Latency', cell: row => td(fmtMs(row.avg_latency_ms)) },
      { head: 'p95 Latency', cell: row => td(fmtMs(row.p95_latency_ms)) },
    ], byAxis);

    // Recent
    const recent = (await apiFetch('api/perf/recent?limit=50', tok)) || [];
    const rows = recent.slice().reverse(); // most recent first
    renderTable('recent-head', 'recent-body', [
      { head: 'Time', cell: r => td(fmtTs(r.ts)) },
      { head: 'Agent', cell: r => td(r.agent || '\u2014') },
      { head: 'Runtime', cell: r => td(r.runtime || '\u2014') },
      { head: 'Project', cell: r => td(r.project || '\u2014') },
      { head: 'Exit', cell: r => td(r.exit_code, r.exit_code === 0 ? 'exit-ok' : 'exit-err') },
      { head: 'Duration', cell: r => td(fmtMs(r.latency_ms)) },
      tokensCol(),
      { head: 'Billing', cell: r => td(r.billing || '\u2014') },
      usdCol(rows),
    ], rows);

    setText('last-updated', 'Updated ' + new Date().toLocaleTimeString());

  } catch (err) {
    console.error('perfdash load error:', err);
    // Don't hide the dashboard on refresh errors; show stale data.
    setText('last-updated', 'Error: ' + err.message);
  }
}

// ---- init -------------------------------------------------------------------

function init() {
  const tok = getToken();
  currentToken = tok;

  // Session mode: no token in fragment or sessionStorage.  The browser sends
  // the session cookie and the console auth edge authenticates the request.
  // We proceed without a token; if the session is invalid the loadAll() error
  // handler will surface it via the last-updated status text.
  // Bearer mode: token present — existing standalone / loopback behaviour.

  el('dashboard').classList.remove('hidden');

  // Wire controls.
  el('refresh-btn').addEventListener('click', () => {
    clearTimeout(refreshTimer);
    loadAll().then(scheduleRefresh);
  });

  ['window-select', 'metric-select', 'bucket-select', 'axis-select'].forEach(id => {
    el(id).addEventListener('change', () => {
      clearTimeout(refreshTimer);
      loadAll().then(scheduleRefresh);
    });
  });

  function scheduleRefresh() {
    refreshTimer = setTimeout(() => loadAll().then(scheduleRefresh), 30000);
  }

  loadAll().then(scheduleRefresh);
}

document.addEventListener('DOMContentLoaded', init);
