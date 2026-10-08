// flows-gallery.js — workflow template gallery for the Flows tab (K-152).
//
// Loaded lazily by app.js the first time the "Templates" button is pressed.
// Under script-src 'self': no inline script, no eval, and every string from the
// server is set with textContent, never as markup.
//
//   window.YakosFlowsGallery.open({ apiFetch, onSaved })
//
// apiFetch(method, path, body) is app.js's authenticated fetch; onSaved(name)
// runs after a template was saved as a workflow. Templates are read-only
// framework files: "Use" saves a COPY through the normal POST
// /flows/api/workflow path, which validates it, so a template never runs
// anything until the operator runs the copy.
(function () {
  'use strict';

  var ID_RE = /^[a-z0-9][a-z0-9-]{0,63}$/;

  function el(tag, cls, text) {
    var n = document.createElement(tag);
    if (cls) n.className = cls;
    if (text !== undefined) n.textContent = text;
    return n;
  }

  function open(ctx) {
    var overlay = el('div', 'flows-gallery-overlay');
    overlay.setAttribute('role', 'dialog');
    overlay.setAttribute('aria-modal', 'true');
    overlay.setAttribute('aria-label', 'Workflow templates');
    var box = el('div', 'flows-gallery-box');
    var title = el('h2', 'subsection-title', 'Workflow templates');
    var note = el('p', 'flows-gallery-note',
      'Pick a template to save a copy as a workflow. Nothing runs until you run it.');
    var status = el('div', 'flows-gallery-status');
    status.setAttribute('role', 'status');
    var list = el('ul', 'flows-gallery-list');
    var close = el('button', 'flows-btn', 'Close');
    close.type = 'button';
    close.addEventListener('click', function () { overlay.remove(); });
    box.appendChild(title);
    box.appendChild(note);
    box.appendChild(status);
    box.appendChild(list);
    box.appendChild(close);
    overlay.appendChild(box);
    document.body.appendChild(overlay);

    function say(msg) { status.textContent = msg || ''; }

    function use(tpl) {
      var name = tpl.name;
      if (!ID_RE.test(name)) { say('Template name is not a valid workflow name.'); return; }
      say('Saving...');
      ctx.apiFetch('GET', '/flows/api/templates?name=' + encodeURIComponent(name))
        .then(function (r) { return r.ok ? r.json() : null; })
        .then(function (data) {
          if (!data || typeof data.yaml !== 'string') { say('Could not load the template.'); return null; }
          return ctx.apiFetch('POST', '/flows/api/workflow', { name: name, yaml: data.yaml, version: '' });
        })
        .then(function (r) {
          if (!r) return;
          if (r.status === 409) { say('A workflow named "' + name + '" already exists.'); return; }
          if (!r.ok) { say('Save failed (' + r.status + ').'); return; }
          overlay.remove();
          if (ctx.onSaved) ctx.onSaved(name);
        })
        .catch(function () { say('Save failed.'); });
    }

    function render(templates) {
      list.textContent = '';
      if (!templates.length) { say('No templates are installed.'); return; }
      templates.forEach(function (tpl) {
        var li = el('li', 'flows-gallery-item');
        li.appendChild(el('strong', 'flows-gallery-name', tpl.name));
        li.appendChild(el('span', 'flows-gallery-desc', tpl.description || ''));
        var btn = el('button', 'flows-btn', 'Use');
        btn.type = 'button';
        btn.setAttribute('aria-label', 'Use template ' + tpl.name);
        btn.addEventListener('click', function () { use(tpl); });
        li.appendChild(btn);
        list.appendChild(li);
      });
    }

    ctx.apiFetch('GET', '/flows/api/templates')
      .then(function (r) { return r.ok ? r.json() : null; })
      .then(function (data) {
        if (!data) { say('Could not load templates.'); return; }
        render(data.templates || []);
      })
      .catch(function () { say('Could not load templates.'); });
    return overlay;
  }

  window.YakosFlowsGallery = { open: open };
})();
