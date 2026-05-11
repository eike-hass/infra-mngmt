// CodeMirror lives in /static/vendor/codemirror.bundle.js (~1.1 MB ESM,
// produced by `make vendor-codemirror`). It's loaded lazily via dynamic
// import inside startEdit() — the bundle is only fetched when the user
// actually clicks "edit", not on initial page render. Resolves
// docs/frontend-architecture.md §18.4.
//
// _cm caches the resolved module promise so concurrent clicks (or a
// second edit in the same session) reuse the same fetch; the browser's
// module cache means the underlying network request happens at most once.
let _cm = null;
function loadCodeMirror() {
  if (!_cm) _cm = import("/static/vendor/codemirror.bundle.js");
  return _cm;
}

let _activeEditor = null;

function renderPreviewMarkdown() {
  const rawEl = document.querySelector('#preview .raw-src');
  const mdEl  = document.querySelector('#preview .rendered-md');
  if (rawEl && mdEl) mdEl.innerHTML = marked.parse(rawEl.textContent || '');
}
document.body.addEventListener('htmx:afterSwap', e => {
  if (e.detail.target.id === 'preview') renderPreviewMarkdown();
});

async function startEdit(btn) {
  const body = btn.closest('.preview-body');
  const raw  = body.querySelector('.raw-src').textContent;
  body.querySelector('.rendered-md').style.display = 'none';
  btn.style.display = 'none';
  const wrap = body.querySelector('.editor-wrap');
  wrap.style.display = 'block';
  wrap.innerHTML = '<div class="editor-loading" style="padding:12px;color:var(--text2);font-style:italic">loading editor…</div>';
  const {EditorView, basicSetup, EditorState, markdown} = await loadCodeMirror();
  // The user could have cancelled (or clicked a different entity) while
  // we were awaiting the bundle. Bail if the slot was reset.
  if (wrap.style.display === 'none') return;
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

// ── event delegation: preview edit/save/cancel buttons (HTMX-swapped region) ──
document.body.addEventListener('click', e => {
  const editBtn = e.target.closest('#preview .edit-btn');
  if (editBtn) { startEdit(editBtn); return; }
  const saveBtn = e.target.closest('#preview .save-btn');
  if (saveBtn) { saveEdit(saveBtn); return; }
  const cancelBtn = e.target.closest('#preview .cancel-btn');
  if (cancelBtn) { cancelEdit(cancelBtn); return; }
});

// ── window exports for cross-module access ──
window.startEdit = startEdit;
window.saveEdit = saveEdit;
window.cancelEdit = cancelEdit;
