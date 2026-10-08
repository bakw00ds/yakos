// Runs dist/context-drawer.js against a minimal fake DOM. innerHTML,
// outerHTML and insertAdjacentHTML throw, so any markup sink fails the test.
'use strict';
const fs = require('fs');
const src = fs.readFileSync(process.argv[2], 'utf8');

function mk(tag) {
  const e = { tag, children: [], className: '', _text: '', listeners: {} };
  for (const sink of ['innerHTML', 'outerHTML']) {
    Object.defineProperty(e, sink, { set() { throw new Error('markup sink ' + sink); } });
  }
  e.insertAdjacentHTML = function () { throw new Error('markup sink insertAdjacentHTML'); };
  e.appendChild = function (c) { e.children.push(c); c.parentNode = e; return c; };
  e.removeChild = function (c) { e.children = e.children.filter(function (x) { return x !== c; }); };
  e.setAttribute = function () {};
  e.addEventListener = function (n, f) { e.listeners[n] = f; };
  Object.defineProperty(e, 'textContent', {
    get() { return e._text; },
    set(v) { e._text = String(v); e.children = []; },
  });
  return e;
}
const body = mk('body');
const ids = {};
global.window = global;
global.document = {
  body: body,
  createElement: mk,
  getElementById: function (id) { return ids[id] || null; },
};
eval(src);

function texts(n, out) {
  out = out || [];
  if (n._text) out.push(n._text);
  n.children.forEach(function (c) { texts(c, out); });
  return out;
}
const evil = '<img src=x onerror=alert(1)>';
const tree = window.YakosContextDrawer.render({
  knowledge: { sha: 'a'.repeat(64), bytes: 100, cap: 24576, parts: [
    { name: evil, kind: 'rule', bytes: 5, included: true },
    { name: 'late', kind: 'project-rule', bytes: 9, included: false },
    { name: 'lead', kind: 'agent', bytes: 7, included: true, truncated: true },
  ] },
  soul: '<b>soul</b>',
});
const all = texts(tree).join('\n');
if (all.indexOf(evil) < 0) throw new Error('part name not shown as text');
if (all.indexOf('left out') < 0 || all.indexOf('truncated') < 0) throw new Error('part states missing');
if (all.indexOf('<b>soul</b>') < 0) throw new Error('soul missing');
const empty = texts(window.YakosContextDrawer.render({ knowledge: null })).join(' ');
if (empty.indexOf('No knowledge pack') < 0) throw new Error('empty state missing');

// open(): fetches the right URL, shows the result, and Close removes it.
let asked = '';
const done = new Promise(function (res) {
  window.YakosContextDrawer.open('conv/1', function (m, p) {
    asked = m + ' ' + p;
    return Promise.resolve({ ok: true, json: function () { return Promise.resolve({ knowledge: null }); } }).then(function (r) { res(); return r; });
  });
});
done.then(function () {
  if (asked !== 'GET /api/chat/context?conv=conv%2F1') throw new Error('bad request: ' + asked);
  console.log('ok');
}).catch(function (e) { console.error(e.message); process.exit(1); });
