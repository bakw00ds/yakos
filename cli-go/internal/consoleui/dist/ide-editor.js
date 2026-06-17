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

// ── Editor state ──────────────────────────────────────────────────────────
var editor = null;

// Multi-model support: Map<path, {model, isDirty, dirtyDebounceTimer}>
// Each open file gets its own Monaco ITextModel so switching files does not
// reload the iframe — editor.setModel(entry.model) is instant.
var modelMap = {}; // plain object: path → {model, isDirty, dirtyTimer}

// The workspace-relative path currently displayed in the editor.
var currentPath = '';

// Dirty tracking: true when the active model has unsaved changes.
// Kept as a module-level var for backward compatibility with the single-file
// save/⌘S path; the per-model isDirty inside modelMap is the authoritative
// per-file state.
var isDirty = false;

// Debounce timer for dirty postMessage to parent.
var dirtyDebounceTimer = null;

function postDirty(path, dirty) {
  // Debounced: coalesce rapid keystrokes into a single message every 300 ms.
  // Always include path so the parent can associate the event to the right tab.
  var entry = modelMap[path];
  if (entry) {
    if (entry.dirtyTimer !== null) {
      clearTimeout(entry.dirtyTimer);
    }
    entry.dirtyTimer = setTimeout(function() {
      entry.dirtyTimer = null;
      try {
        window.parent.postMessage(
          { type: 'dirty', path: path, dirty: dirty },
          window.location.origin
        );
      } catch (_) {}
    }, 300);
  } else {
    // Fallback: no model entry yet (should not happen), send immediately.
    if (dirtyDebounceTimer !== null) {
      clearTimeout(dirtyDebounceTimer);
    }
    dirtyDebounceTimer = setTimeout(function() {
      dirtyDebounceTimer = null;
      try {
        window.parent.postMessage({ type: 'dirty', path: path, dirty: dirty }, window.location.origin);
      } catch (_) {}
    }, 300);
  }
}

// getOrCreateModel returns (and caches) a Monaco ITextModel for the given path.
// If content/language are supplied (on first open), the model is created with
// that initial content.  On subsequent calls with the same path (tab switch),
// content/language may be null — the existing model is reused as-is.
function getOrCreateModel(path, content, language) {
  if (modelMap[path]) {
    return modelMap[path];
  }
  var uri = monaco.Uri.file(path);
  // Check if Monaco already has a model for this URI (defensive — should not
  // happen given we track by path, but guards against double-init races).
  var existing = monaco.editor.getModel(uri);
  var model;
  if (existing) {
    model = existing;
  } else {
    model = monaco.editor.createModel(
      typeof content === 'string' ? content : '',
      language || 'plaintext',
      uri
    );
  }
  var entry = { model: model, isDirty: false, dirtyTimer: null };

  // Wire per-model dirty tracking.
  model.onDidChangeContent(function() {
    if (!editor) return;
    // Only mark dirty when this model is currently active and editor is editable.
    if (editor.getModel() === model && !editor.getRawOptions().readOnly) {
      entry.isDirty = true;
      isDirty = true;
      postDirty(path, true);
    }
  });

  modelMap[path] = entry;
  return entry;
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

  // ⌘S / Ctrl-S inside Monaco: prevent browser save dialog, post save request.
  // The save message now includes path so the parent can guard against stale
  // path/content pairs (B1 parallel from the multi-tab context).
  editor.addCommand(
    monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS,
    function() {
      if (!editor.getRawOptions().readOnly && currentPath) {
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

  // ── "Ask agent about selection" editor action ─────────────────────────
  //
  // Appears in the Monaco context menu (right-click) and can be triggered
  // via the keyboard shortcut Alt+A.  Posts an askAgent message to the parent
  // so the parent can inject context into the IDE chat input.
  editor.addAction({
    id:    'yakos.ask-agent-about-selection',
    label: 'Ask agent about this selection',
    keybindings: [monaco.KeyMod.Alt | monaco.KeyCode.KeyA],
    contextMenuGroupId: 'navigation',
    contextMenuOrder: 1.5,
    run: function(ed) {
      var sel = ed.getSelection();
      if (!sel) return;
      var text = ed.getModel() ? ed.getModel().getValueInRange(sel) : '';
      if (!text) return;
      try {
        window.parent.postMessage({
          type:      'askAgent',
          path:      currentPath,
          startLine: sel.startLineNumber,
          endLine:   sel.endLineNumber,
          text:      text,
        }, window.location.origin);
      } catch (_) {}
    },
  });

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

  var demoPath = '/demo/main.go';
  var entry = getOrCreateModel(demoPath, demoContent, 'go');
  currentPath = demoPath;
  editor.setModel(entry.model);
}

// ── Decaying "changed on disk" decoration ─────────────────────────────────
//
// applyExternalContent uses Monaco's decorations API to highlight the lines
// that changed when an external file modification is applied.  The decoration
// class fades over ~2 s via a CSS keyframe animation; it is removed entirely
// after 2.5 s so it cannot accumulate across rapid successive file-writes.
//
// Decoration state is stored per-model so concurrent models don't share it.
var externalChangeDecorIds = {}; // path → decorationCollection or []
var externalChangeDecorTimers = {}; // path → clearTimeout handle

// ── postMessage API ───────────────────────────────────────────────────────
//
// Messages received from parent:
//   { type: 'openFile', path, content, language, version, editable }
//     — Load content into a per-path model; switch editor to that model.
//       Re-opening an already-open path reuses the cached model (no content
//       reset) — the parent sends content only on first open for a given path.
//       When `content` is explicitly provided (non-null string), the model
//       content is replaced (used on reload after conflict or explicit re-read).
//   { type: 'setTheme', theme }
//     — Switch Monaco theme (dark/light).
//   { type: 'setEditable', editable }
//     — Toggle readOnly on the editor.  When switching to read-only, resets dirty.
//   { type: 'requestContent' }
//     — Reply immediately with { type: 'content', path, content }.
//   { type: 'closeFile', path }
//     — Dispose the model for the given path (called when a tab is closed).
//   { type: 'applyExternalContent', path, content, version }
//     — Live-apply external file change into the model for `path`.
//       Saves cursor/scroll view-state, replaces model content, restores
//       view-state, and applies a decaying highlight decoration on changed lines.
//       Only applies when the target path's model exists; no-op otherwise.
//   { type: 'revealLine', path, line }
//     — Scroll the editor to `line` (1-based) and place the cursor there.
//       No-op when path does not match the currently active model.
//
// Messages posted to parent:
//   { type: 'ready' }            — editor mounted.
//   { type: 'error', message }   — fatal init error.
//   { type: 'dirty', path, dirty } — dirty state changed (debounced 300 ms).
//   { type: 'save', path, content } — ⌘S / Ctrl-S triggered (parent does the fetch).
//   { type: 'content', path, content } — reply to 'requestContent'.
//   { type: 'askAgent', path, startLine, endLine, text }
//     — Operator invoked "Ask agent about selection"; text is the raw selected
//       text; parent injects a context-prefixed message into the IDE chat input.
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
    var path = typeof msg.path === 'string' ? msg.path : '';
    var language = msg.language || 'plaintext';

    // Look up or create the model for this path.
    var entry = modelMap[path];
    var isFirstOpen = !entry;

    if (isFirstOpen) {
      // First time opening this file: create model with provided content.
      var content = typeof msg.content === 'string' ? msg.content : '';
      entry = getOrCreateModel(path, content, language);
    } else if (typeof msg.content === 'string') {
      // Re-open with explicit content (e.g. reload after conflict):
      // update the existing model content without triggering dirty.
      var wasReadOnly = editor.getRawOptions().readOnly;
      editor.updateOptions({ readOnly: true });
      entry.model.setValue(msg.content);
      editor.updateOptions({ readOnly: wasReadOnly });
      // Reset dirty for this model after an explicit reload.
      entry.isDirty = false;
      if (entry.dirtyTimer !== null) {
        clearTimeout(entry.dirtyTimer);
        entry.dirtyTimer = null;
      }
      // Update language if provided.
      monaco.editor.setModelLanguage(entry.model, language);
      try {
        window.parent.postMessage({ type: 'dirty', path: path, dirty: false }, window.location.origin);
      } catch (_) {}
    }

    // Switch editor to this model.
    currentPath = path;
    editor.setModel(entry.model);
    isDirty = entry.isDirty;

    // Apply editable state if explicitly set in the message.
    if (typeof msg.editable === 'boolean') {
      editor.updateOptions({ readOnly: !msg.editable });
    }

    // Clear dirty state on first open (model was just created clean).
    if (isFirstOpen) {
      entry.isDirty = false;
      isDirty = false;
      if (entry.dirtyTimer !== null) {
        clearTimeout(entry.dirtyTimer);
        entry.dirtyTimer = null;
      }
      try {
        window.parent.postMessage({ type: 'dirty', path: path, dirty: false }, window.location.origin);
      } catch (_) {}
    }

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
    // Switching to read-only resets dirty for the active model.
    if (!editable) {
      var activeEntry = currentPath ? modelMap[currentPath] : null;
      if (activeEntry && activeEntry.isDirty) {
        activeEntry.isDirty = false;
        isDirty = false;
        if (activeEntry.dirtyTimer !== null) {
          clearTimeout(activeEntry.dirtyTimer);
          activeEntry.dirtyTimer = null;
        }
        try {
          window.parent.postMessage(
            { type: 'dirty', path: currentPath, dirty: false },
            window.location.origin
          );
        } catch (_) {}
      }
    }
    if (editable && editor) {
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

  } else if (msg.type === 'closeFile') {
    // Dispose the model for the given path when its tab is closed.
    var closePath = typeof msg.path === 'string' ? msg.path : '';
    if (closePath && modelMap[closePath]) {
      var closeEntry = modelMap[closePath];
      // Cancel any pending dirty timer.
      if (closeEntry.dirtyTimer !== null) {
        clearTimeout(closeEntry.dirtyTimer);
        closeEntry.dirtyTimer = null;
      }
      // Cancel any pending decor timer.
      if (externalChangeDecorTimers[closePath]) {
        clearTimeout(externalChangeDecorTimers[closePath]);
        delete externalChangeDecorTimers[closePath];
      }
      delete externalChangeDecorIds[closePath];
      // Parent always activates a neighbor tab before sending closeFile, so
      // the model being closed is never the currently displayed one. Dispose
      // unconditionally so we don't leak Monaco models.
      closeEntry.model.dispose();
      delete modelMap[closePath];
    }

  } else if (msg.type === 'applyExternalContent') {
    // Live-apply an external file change into the cached Monaco model.
    //
    // Strategy:
    //   1. Save the editor view-state (cursor + scroll) if this model is active.
    //   2. Temporarily mark model read-only, replace content, restore read-only.
    //      Using setValue on the model directly avoids triggering the dirty
    //      listener (the listener is guarded by editor.getRawOptions().readOnly).
    //   3. Restore the view-state.
    //   4. Apply a decaying line-highlight decoration on all changed lines.
    //
    // The dirty guard (readOnly wrapper) is identical to the 'openFile' reload
    // path already in this file — no new patterns introduced.
    if (!editor) return;
    var applyPath = typeof msg.path === 'string' ? msg.path : '';
    var applyContent = typeof msg.content === 'string' ? msg.content : '';
    if (!applyPath) return;
    var applyEntry = modelMap[applyPath];
    if (!applyEntry) return; // model not open; nothing to do

    var isActive = (currentPath === applyPath) && (editor.getModel() === applyEntry.model);

    // 1. Save view-state (cursor + scroll) for the active model.
    var savedViewState = isActive ? editor.saveViewState() : null;

    // 2. Compute changed-line ranges for decoration BEFORE replacing content.
    //    We do a simple line-by-line diff between old and new content.
    var oldLines = applyEntry.model.getValue().split('\n');
    var newLines = applyContent.split('\n');
    var changedRanges = [];
    var maxLines = Math.max(oldLines.length, newLines.length);
    var inChanged = false;
    var changedStart = -1;
    for (var li = 0; li < maxLines; li++) {
      var same = (li < oldLines.length && li < newLines.length && oldLines[li] === newLines[li]);
      if (!same && !inChanged) {
        inChanged = true;
        changedStart = li + 1; // Monaco lines are 1-based
      } else if (same && inChanged) {
        inChanged = false;
        changedRanges.push({ startLineNumber: changedStart, endLineNumber: li, startColumn: 1, endColumn: 1 });
      }
    }
    if (inChanged) {
      changedRanges.push({ startLineNumber: changedStart, endLineNumber: maxLines, startColumn: 1, endColumn: 1 });
    }

    // 3. Replace model content without triggering dirty.
    var wasReadOnly = editor.getRawOptions().readOnly;
    editor.updateOptions({ readOnly: true });
    applyEntry.model.setValue(applyContent);
    editor.updateOptions({ readOnly: wasReadOnly });
    // External content is by definition clean (not a user edit).
    applyEntry.isDirty = false;
    if (applyEntry.dirtyTimer !== null) {
      clearTimeout(applyEntry.dirtyTimer);
      applyEntry.dirtyTimer = null;
    }
    // Tell parent: this path is now clean (matches disk).
    try {
      window.parent.postMessage({ type: 'dirty', path: applyPath, dirty: false }, window.location.origin);
    } catch (_) {}

    // 4. Restore view-state for the active model.
    if (isActive && savedViewState) {
      editor.restoreViewState(savedViewState);
    }

    // 5. Apply decaying decoration on changed lines (active model only).
    if (isActive && changedRanges.length > 0) {
      // Clear any previous decor for this path.
      if (externalChangeDecorTimers[applyPath]) {
        clearTimeout(externalChangeDecorTimers[applyPath]);
        delete externalChangeDecorTimers[applyPath];
      }
      var decorations = changedRanges.map(function(r) {
        return {
          range: new monaco.Range(r.startLineNumber, 1, r.endLineNumber, 1),
          options: {
            isWholeLine: true,
            className: 'ide-external-change-line',
            overviewRuler: { color: 'rgba(255, 200, 0, 0.6)', position: monaco.editor.OverviewRulerLane.Right },
          },
        };
      });
      // Remove previous decoration set before adding new one.
      if (externalChangeDecorIds[applyPath] &&
          typeof externalChangeDecorIds[applyPath].clear === 'function') {
        externalChangeDecorIds[applyPath].clear();
      }
      externalChangeDecorIds[applyPath] = editor.createDecorationsCollection(decorations);
      // Auto-clear after 2.5 s (the CSS animation runs for 2 s).
      externalChangeDecorTimers[applyPath] = setTimeout(function() {
        if (externalChangeDecorIds[applyPath] &&
            typeof externalChangeDecorIds[applyPath].clear === 'function') {
          externalChangeDecorIds[applyPath].clear();
        }
        delete externalChangeDecorIds[applyPath];
        delete externalChangeDecorTimers[applyPath];
      }, 2500);
    }

  } else if (msg.type === 'revealLine') {
    // Scroll the editor to the requested line and place the cursor there.
    // Only applies when the message path matches the currently active model.
    if (!editor) return;
    var revPath = typeof msg.path === 'string' ? msg.path : '';
    var revLine = typeof msg.line === 'number' ? msg.line : parseInt(msg.line, 10);
    if (!revPath || isNaN(revLine) || revLine < 1) return;
    if (currentPath !== revPath) return;
    // Clamp line to model line count.
    var revModel = editor.getModel();
    if (!revModel) return;
    var lineCount = revModel.getLineCount();
    var targetLine = Math.min(revLine, lineCount);
    editor.revealLineInCenter(targetLine);
    editor.setPosition({ lineNumber: targetLine, column: 1 });
    editor.focus();
  }
});
