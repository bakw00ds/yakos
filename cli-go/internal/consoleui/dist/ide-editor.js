'use strict';

// ── Utilities ────────────────────────────────────────────────────────────

function showError(msg) {
  var banner = document.getElementById('error-banner');
  if (banner) {
    banner.textContent = 'Editor error: ' + msg;
    banner.style.display = 'block';
  }
  document.getElementById('loading').style.display = 'none';
  try {
    window.parent.postMessage({ type: 'error', message: msg }, window.location.origin);
  } catch (_) {}
  console.error('[ide-editor] fatal:', msg);
}

// ── CSP-safe worker factory (blob-wrapper pattern) ────────────────────────
//
// Monaco resolves workerMain.js relative to vs/base/worker/workerMain.js.
// We intercept getWorkerUrl and return a blob: URL whose content was fetched
// from same-origin — satisfying "worker-src blob: 'self'" in the CSP.
//
// Synchronous XHR is used intentionally: MonacoEnvironment.getWorkerUrl is
// called synchronously by the AMD loader; async alternatives (fetch + await)
// cannot be used here without significant architectural complexity.  The XHR
// is to a local same-origin URL so latency is negligible.
//
// Blob URL lifetime: the blob: URL is revoked after the Worker is constructed
// to avoid a memory leak (the Worker holds a reference to the script content
// independently of the blob URL once it starts).
function makeBlobWorker(workerUrl) {
  var xhr = new XMLHttpRequest();
  xhr.open('GET', workerUrl, false); // synchronous
  xhr.send();
  if (xhr.status !== 200) {
    throw new Error('Failed to fetch worker script: ' + workerUrl + ' (HTTP ' + xhr.status + ')');
  }
  var blob = new Blob([xhr.responseText], { type: 'application/javascript' });
  var blobUrl = URL.createObjectURL(blob);
  var worker = new Worker(blobUrl);
  URL.revokeObjectURL(blobUrl);
  return worker;
}

// ── MonacoEnvironment: must be set BEFORE require() is called ─────────────
window.MonacoEnvironment = {
  getWorkerUrl: function(_moduleId, _label) {
    // Return a blob: URL wrapping the same-origin workerMain.js.
    // The actual Worker construction happens inside makeBlobWorker.
    // We return the same-origin path; the AMD loader passes it here
    // but we override by building the worker ourselves in getWorker.
    return '/vendor/monaco/min/vs/base/worker/workerMain.js';
  },
  getWorker: function(_moduleId, _label) {
    return makeBlobWorker('/vendor/monaco/min/vs/base/worker/workerMain.js');
  }
};

// ── AMD loader bootstrap ──────────────────────────────────────────────────
var loaderScript = document.createElement('script');
loaderScript.src = '/vendor/monaco/min/vs/loader.js';
loaderScript.onerror = function() {
  showError('Failed to load Monaco AMD loader from /vendor/monaco/min/vs/loader.js');
};
loaderScript.onload = function() {
  // Configure require.js base path so Monaco can locate its modules.
  require.config({
    paths: { vs: '/vendor/monaco/min/vs' }
  });

  require(['vs/editor/editor.main'], function() {
    try {
      initEditor();
    } catch (e) {
      showError(e && e.message ? e.message : String(e));
    }
  }, function(err) {
    showError('Monaco module load failed: ' + (err && err.message ? err.message : String(err)));
  });
};
document.head.appendChild(loaderScript);

// ── Editor initialisation ─────────────────────────────────────────────────
var editor = null;

// Track the path currently open so we can include it in save/dirty messages.
var currentPath = '';

// Dirty tracking: true when the model has been changed since the last openFile
// or successful save.  The parent uses this to decide whether to enable Save.
var isDirty = false;

// Debounce timer for dirty postMessage to parent.
var dirtyDebounceTimer = null;

function postDirty(dirty) {
  // Debounced: coalesce rapid keystrokes into a single message every 300 ms.
  if (dirtyDebounceTimer !== null) {
    clearTimeout(dirtyDebounceTimer);
  }
  dirtyDebounceTimer = setTimeout(function() {
    dirtyDebounceTimer = null;
    try {
      window.parent.postMessage({ type: 'dirty', dirty: dirty }, window.location.origin);
    } catch (_) {}
  }, 300);
}

function initEditor() {
  var container = document.getElementById('container');
  editor = monaco.editor.create(container, {
    value: '',
    language: 'plaintext',
    theme: 'vs-dark',
    readOnly: true,
    automaticLayout: true, // resizes with container
    scrollBeyondLastLine: false,
    minimap: { enabled: false },
    fontSize: 13,
    fontFamily: '"Cascadia Code", "Fira Code", "Consolas", monospace',
    renderLineHighlight: 'all',
    renderWhitespace: 'selection',
    // Accessibility: show line numbers, cursor blinking so it's clearly
    // a read-only view (not a dead text block).
    lineNumbers: 'on',
    cursorBlinking: 'smooth',
    accessibilitySupport: 'auto',
  });

  // Dirty-tracking: fire when editable and model content changes.
  editor.onDidChangeModelContent(function() {
    if (!editor.getRawOptions().readOnly) {
      isDirty = true;
      postDirty(true);
    }
  });

  // ⌘S / Ctrl-S inside Monaco: prevent browser save dialog, post save request.
  editor.addCommand(
    monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS,
    function() {
      if (!editor.getRawOptions().readOnly) {
        var content = editor.getValue();
        try {
          window.parent.postMessage(
            { type: 'save', path: currentPath, content: content },
            window.location.origin
          );
        } catch (_) {}
      }
    }
  );

  // Hide loading overlay.
  document.getElementById('loading').style.display = 'none';

  // Notify parent that editor is ready.
  try {
    window.parent.postMessage({ type: 'ready' }, window.location.origin);
  } catch (_) {}

  // Demo mode: load a hardcoded sample when ?demo=1 is in the URL.
  var params = new URLSearchParams(window.location.search);
  if (params.get('demo') === '1') {
    loadDemo();
  }
}

// ── Demo payload ──────────────────────────────────────────────────────────
function loadDemo() {
  if (!editor) return;
  var demoContent = [
    '// yakOS IDE spike — Monaco editor demo',
    '//',
    '// This file is loaded automatically when ?demo=1 is present.',
    '// It proves Monaco renders, syntax-highlighting is active, and',
    '// no CSP violations appear in the browser console.',
    '',
    'package main',
    '',
    'import (',
    '\t"context"',
    '\t"fmt"',
    '\t"log/slog"',
    '\t"net/http"',
    ')',
    '',
    '// Server is the unified console HTTP server.',
    'type Server struct {',
    '\tcfg    Config',
    '\thttpSrv *http.Server',
    '}',
    '',
    '// Serve starts the HTTP server and blocks until ctx is cancelled.',
    'func (s *Server) Serve(ctx context.Context) error {',
    '\tslog.Info("consoleui: starting", "addr", s.cfg.Addr)',
    '\terrCh := make(chan error, 1)',
    '\tgo func() {',
    '\t\terrCh <- s.httpSrv.ListenAndServe()',
    '\t}()',
    '\tselect {',
    '\tcase <-ctx.Done():',
    '\t\treturn s.httpSrv.Shutdown(context.Background())',
    '\tcase err := <-errCh:',
    '\t\tif err == http.ErrServerClosed {',
    '\t\t\treturn nil',
    '\t\t}',
    '\t\treturn fmt.Errorf("consoleui: serve: %w", err)',
    '\t}',
    '}',
  ].join('\n');

  monaco.editor.setModelLanguage(editor.getModel(), 'go');
  editor.setValue(demoContent);
}

// ── postMessage API ───────────────────────────────────────────────────────
//
// Messages received from parent:
//   { type: 'openFile', path, content, language }
//     — Load content into editor; reset dirty state; keep current readOnly setting.
//   { type: 'setTheme', theme }
//     — Switch Monaco theme (dark/light).
//   { type: 'setEditable', editable }
//     — Toggle readOnly on the editor.  When switching to read-only, resets dirty.
//   { type: 'requestContent' }
//     — Reply immediately with { type: 'content', path, content }.
//
// Messages posted to parent:
//   { type: 'ready' }        — editor mounted.
//   { type: 'error', message } — fatal init error.
//   { type: 'dirty', dirty }  — dirty state changed (debounced 300 ms).
//   { type: 'save', path, content } — ⌘S / Ctrl-S triggered (parent does the fetch).
//   { type: 'content', path, content } — reply to 'requestContent'.
window.addEventListener('message', function(e) {
  // Only accept messages from the same origin (parent console frame).
  if (e.origin !== window.location.origin) return;
  var msg = e.data;
  if (!msg || typeof msg !== 'object') return;

  if (msg.type === 'openFile') {
    if (!editor) {
      showError('Received openFile before editor was ready');
      return;
    }
    var lang = msg.language || 'plaintext';
    var content = typeof msg.content === 'string' ? msg.content : '';
    currentPath = typeof msg.path === 'string' ? msg.path : '';
    monaco.editor.setModelLanguage(editor.getModel(), lang);
    // Suppress dirty events while loading: temporarily mark as not editable
    // so the onDidChangeModelContent guard does not fire during setValue.
    var wasReadOnly = editor.getRawOptions().readOnly;
    editor.updateOptions({ readOnly: true });
    editor.setValue(content);
    // Restore original readOnly setting.
    editor.updateOptions({ readOnly: wasReadOnly });
    // Clear dirty state — opening a file always starts clean.
    isDirty = false;
    if (dirtyDebounceTimer !== null) {
      clearTimeout(dirtyDebounceTimer);
      dirtyDebounceTimer = null;
    }
    try {
      window.parent.postMessage({ type: 'dirty', dirty: false }, window.location.origin);
    } catch (_) {}
  } else if (msg.type === 'setTheme') {
    // Map yakOS console theme names to Monaco built-in theme identifiers.
    // Dark themes (ops, fluid, og) → 'vs-dark'; light → 'vs'.
    // CSP-safe: monaco.editor.setTheme is a runtime API call, not an
    // inline script — no CSP changes required.
    var monacoTheme = (msg.theme === 'light') ? 'vs' : 'vs-dark';
    monaco.editor.setTheme(monacoTheme);
  } else if (msg.type === 'setEditable') {
    if (!editor) return;
    var editable = !!msg.editable;
    editor.updateOptions({ readOnly: !editable });
    // Switching to read-only resets dirty (user cancelled edit).
    if (!editable && isDirty) {
      isDirty = false;
      if (dirtyDebounceTimer !== null) {
        clearTimeout(dirtyDebounceTimer);
        dirtyDebounceTimer = null;
      }
      try {
        window.parent.postMessage({ type: 'dirty', dirty: false }, window.location.origin);
      } catch (_) {}
    }
    if (editable) {
      // Focus the editor so the operator can type immediately.
      editor.focus();
    }
  } else if (msg.type === 'requestContent') {
    // Parent is about to save; reply with current content.
    if (!editor) return;
    try {
      window.parent.postMessage(
        { type: 'content', path: currentPath, content: editor.getValue() },
        window.location.origin
      );
    } catch (_) {}
  }
});
