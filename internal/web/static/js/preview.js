// ── CodeMirror preamble ──
// Imports are static module imports — resolved before the rest of this file
// executes. The old `cmReady` Promise + `window._cm` shim existed when the
// preamble lived in a separate <script type="module"> than the main script;
// now that everything is in one module file, the bindings are usable directly.
//
// KNOWN ISSUE: CodeMirror's esm.sh module graph can hang on slow / partially
// reachable networks (the redirect target /codemirror@<v>/es2022/codemirror.mjs
// recursively imports dozens of sub-modules). Vendoring CodeMirror as a
// bundled artifact under static/vendor/ is scheduled as a follow-up; see
// docs/frontend-architecture.md §16 M5 / §10. Until then, the entity edit
// flow may fail to open the editor in environments where esm.sh is flaky.
import {EditorView,basicSetup} from "https://esm.sh/codemirror@6";
import {markdown} from "https://esm.sh/@codemirror/lang-markdown@6";
import {EditorState} from "https://esm.sh/@codemirror/state@6";

let _activeEditor = null;

function renderPreviewMarkdown() {
  const rawEl = document.querySelector('#preview .raw-src');
  const mdEl  = document.querySelector('#preview .rendered-md');
  if (rawEl && mdEl) mdEl.innerHTML = marked.parse(rawEl.textContent || '');
}
document.body.addEventListener('htmx:afterSwap', e => {
  if (e.detail.target.id === 'preview') renderPreviewMarkdown();
});

function startEdit(btn) {
  const body = btn.closest('.preview-body');
  const raw  = body.querySelector('.raw-src').textContent;
  body.querySelector('.rendered-md').style.display = 'none';
  btn.style.display = 'none';
  const wrap = body.querySelector('.editor-wrap');
  wrap.style.display = 'block';
  wrap.innerHTML = '';
  const theme = EditorView.theme({
    '&':                   {background:'var(--bg2)', color:'var(--text)'},
    '.cm-content':         {caretColor:'var(--accent)', padding:'12px 14px'},
    '.cm-gutters':         {background:'var(--bg3)', color:'var(--text3)', border:'none', borderRight:'1px solid var(--border)'},
    '.cm-activeLine':      {background:'rgba(255,255,255,.03)'},
    '.cm-activeLineGutter':{background:'rgba(255,255,255,.03)'},
    '.cm-selectionBackground, &.cm-focused .cm-selectionBackground': {background:'#264f78 !important'},
    '.cm-cursor':          {borderLeftColor:'var(--accent)'},
  }, {dark:true});
  _activeEditor = new EditorView({
    state: EditorState.create({
      doc: raw,
      extensions: [basicSetup, markdown(), theme, EditorView.lineWrapping],
    }),
    parent: wrap,
  });
  body.querySelector('.edit-toolbar').style.display = 'flex';
}

async function saveEdit(btn) {
  if (!_activeEditor) return;
  const body     = btn.closest('.preview-body');
  const entityId = body.dataset.entityId;
  const content  = _activeEditor.state.doc.toString();
  const status   = body.querySelector('.save-status');
  status.textContent = 'saving…';
  try {
    const resp = await fetch('/api/entity?id=' + encodeURIComponent(entityId), {
      method: 'POST', body: content,
      headers: {'Content-Type': 'text/plain; charset=utf-8'},
    });
    if (resp.ok) {
      status.textContent = '✓ saved';
      body.querySelector('.raw-src').textContent = content;
      setTimeout(() => { status.textContent = ''; cancelEdit(btn); renderPreviewMarkdown(); }, 1200);
    } else {
      status.textContent = '✗ ' + (await resp.text()).trim();
    }
  } catch { status.textContent = '✗ network error'; }
}

function cancelEdit(btn) {
  const body = btn.closest('.preview-body');
  if (_activeEditor) { _activeEditor.destroy(); _activeEditor = null; }
  body.querySelector('.editor-wrap').style.display = 'none';
  body.querySelector('.editor-wrap').innerHTML = '';
  body.querySelector('.edit-toolbar').style.display = 'none';
  body.querySelector('.rendered-md').style.display = '';
  body.querySelector('.edit-btn').style.display = '';
  body.querySelector('.save-status').textContent = '';
}

// ── window exports for onclick= attributes ──
window.startEdit = startEdit;
window.saveEdit = saveEdit;
window.cancelEdit = cancelEdit;
