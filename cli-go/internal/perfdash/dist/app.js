/* yakOS Performance Dashboard — vanilla JS SPA
 *
 * Auth: token read from URL fragment (#token=<hex>) → sessionStorage.
 * All API calls attach "Authorization: Bearer <token>" header.
 * Auto-refresh every 30 seconds.
 * SVG timeseries chart implemented inline (no CDN dependency).
 */

'use strict';

// ---- auth -------------------------------------------------------------------

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

function authHeader(tok) {
  return tok ? { 'Authorization': 'Bearer ' + tok } : {};
}

// ---- fetch helpers ----------------------------------------------------------

async function apiFetch(path, tok) {
  const resp = await fetch(path, { headers: authHeader(tok) });
  if (!resp.ok) {
    const text = await resp.text();
    throw new Error('API ' + resp.status + ': ' + text);
  }
  return resp.json();
}

// ---- DOM helpers ------------------------------------------------------------

function el(id) { return document.getElementById(id); }
function setText(id, v) { const e = el(id); if (e) e.textContent = v; }

function buildRows(tbody, rows) {
  tbody.innerHTML = '';
  rows.forEach(r => {
    const tr = document.createElement('tr');
    tr.innerHTML = r;
    tbody.appendChild(tr);
  });
  if (rows.length === 0) {
    const tr = document.createElement('tr');
    tr.innerHTML = '<td colspan="10" style="text-align:center;color:#6b7280;padding:20px">No data</td>';
    tbody.appendChild(tr);
  }
}

function td(v, cls) {
  return '<td' + (cls ? ' class="' + cls + '"' : '') + '>' + escapeHTML(String(v)) + '</td>';
}

function escapeHTML(s) {
  return s.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;');
}

function fmtMs(ms) {
  if (ms < 1000) return ms + ' ms';
  return (ms / 1000).toFixed(1) + ' s';
}

function fmtCost(c) {
  if (c === 0) return '$0.00';
  if (c < 0.0001) return '<$0.0001';
  return '$' + c.toFixed(4);
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

async function loadAll() {
  const tok = currentToken;
  const win = el('window-select').value;
  const metric = el('metric-select').value;
  const bucket = el('bucket-select').value;
  const axis = el('axis-select').value;

  try {
    // Summary
    const summary = await apiFetch('api/perf/summary?window=' + win, tok);
    setText('card-dispatches', summary.total_dispatches.toLocaleString());
    setText('card-cost', fmtCost(summary.total_cost_usd));
    setText('card-avg-latency', fmtMs(summary.avg_latency_ms));
    setText('card-p95-latency', fmtMs(summary.p95_latency_ms));

    // Top agents
    buildRows(el('top-agents-body'), (summary.top_agents || []).map(a =>
      td(a.key) + td(a.dispatches) + td(fmtCost(a.cost_usd))
    ));

    // Top runtimes
    buildRows(el('top-runtimes-body'), (summary.top_runtimes || []).map(r =>
      td(r.key) + td(r.dispatches) + td(fmtCost(r.cost_usd))
    ));

    // Timeseries
    const ts = await apiFetch(
      'api/perf/timeseries?window=' + win + '&bucket=' + bucket + '&metric=' + metric, tok
    );
    renderChart(el('timeseries-svg'), ts, metric);

    // By axis
    const byAxis = await apiFetch('api/perf/by_axis?axis=' + axis + '&window=' + win, tok);
    buildRows(el('breakdown-body'), (byAxis || []).map(row =>
      td(row.key) +
      td(row.dispatches) +
      td(fmtCost(row.cost_usd)) +
      td(fmtMs(row.avg_latency_ms)) +
      td(fmtMs(row.p95_latency_ms))
    ));

    // Recent
    const recent = await apiFetch('api/perf/recent?limit=50', tok);
    const rows = (recent || []).slice().reverse(); // most recent first
    buildRows(el('recent-body'), rows.map(r =>
      td(fmtTs(r.ts)) +
      td(r.agent || '—') +
      td(r.runtime || '—') +
      td(r.project || '—') +
      td(r.exit_code, r.exit_code === 0 ? 'exit-ok' : 'exit-err') +
      td(fmtMs(r.latency_ms)) +
      td(fmtCost(r.cost_usd))
    ));

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

  if (!tok) {
    el('auth-error').classList.remove('hidden');
    return;
  }

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
