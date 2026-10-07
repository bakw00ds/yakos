// context-drawer.js: the conversation context drawer (K-149).
//
// Shows what GET /api/chat/context says a non-claude conversation was given:
// the knowledge block's hash and size, the parts it was built from (name, kind,
// bytes, whether it fit) and, for the owner of a lead conversation, the soul
// text. Every value is written with textContent; no markup is ever built from
// server data.
(function (root) {
  'use strict';

  var doc = root.document;

  function el(tag, cls, text) {
    var e = doc.createElement(tag);
    if (cls) e.className = cls;
    if (text !== undefined) e.textContent = String(text);
    return e;
  }

  // render builds the drawer body for one /api/chat/context response.
  function render(data) {
    var body = el('div', 'context-drawer-body');
    var k = data && data.knowledge;
    if (!k) {
      body.appendChild(el('p', 'context-drawer-empty',
        'No knowledge pack for this conversation. claude loads the rules natively.'));
      return body;
    }
    body.appendChild(el('p', 'context-drawer-hash', 'sha256 ' + k.sha));
    body.appendChild(el('p', 'context-drawer-size', k.bytes + ' of ' + k.cap + ' bytes'));
    var list = el('ul', 'context-drawer-parts');
    (k.parts || []).forEach(function (p) {
      var line = p.kind + ': ' + p.name + ' (' + p.bytes + ' bytes)';
      if (!p.included) line += ' - left out (over the cap)';
      else if (p.truncated) line += ' - truncated';
      list.appendChild(el('li', p.included ? 'included' : 'dropped', line));
    });
    body.appendChild(list);
    if (typeof data.soul === 'string' && data.soul) {
      body.appendChild(el('h4', 'context-drawer-soul-title', 'Soul'));
      body.appendChild(el('pre', 'context-drawer-soul', data.soul));
    }
    return body;
  }

  // open fetches and shows the drawer. fetchFn(method, path) resolves to a
  // fetch Response (the console's apiFetch).
  function open(conversationId, fetchFn) {
    var existing = doc.getElementById('context-drawer');
    if (existing && existing.parentNode) existing.parentNode.removeChild(existing);
    var drawer = el('aside', 'context-drawer');
    drawer.id = 'context-drawer';
    drawer.setAttribute('role', 'dialog');
    drawer.setAttribute('aria-label', 'Conversation context');
    var head = el('div', 'context-drawer-head');
    head.appendChild(el('strong', null, 'Context'));
    var close = el('button', 'context-drawer-close', 'Close');
    close.type = 'button';
    close.addEventListener('click', function () {
      if (drawer.parentNode) drawer.parentNode.removeChild(drawer);
    });
    head.appendChild(close);
    drawer.appendChild(head);
    var holder = el('div', 'context-drawer-holder', 'Loading...');
    drawer.appendChild(holder);
    doc.body.appendChild(drawer);
    fetchFn('GET', '/api/chat/context?conv=' + encodeURIComponent(conversationId))
      .then(function (resp) {
        if (!resp.ok) throw new Error('status ' + resp.status);
        return resp.json();
      })
      .then(function (data) {
        holder.textContent = '';
        holder.appendChild(render(data));
      })
      .catch(function () {
        holder.textContent = 'Could not load the context.';
      });
    return drawer;
  }

  root.YakosContextDrawer = { open: open, render: render };
})(typeof window !== 'undefined' ? window : globalThis);
