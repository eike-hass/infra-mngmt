// ── view switching ──
function showView(v) {
  document.getElementById('view-entities').style.display = v === 'entities' ? 'flex' : 'none';
  document.getElementById('view-services').style.display = v === 'services' ? 'flex' : 'none';
  document.getElementById('view-llama').style.display    = v === 'llama'    ? 'flex' : 'none';
  document.getElementById('kind-bar').style.display      = v === 'entities' ? 'flex' : 'none';
  document.getElementById('tab-scroll-wrap').style.display = v === 'entities' ? 'flex' : 'none';
  document.getElementById('vtab-entities').classList.toggle('active', v === 'entities');
  document.getElementById('vtab-services').classList.toggle('active', v === 'services');
  document.getElementById('vtab-llama').classList.toggle('active',    v === 'llama');
  if (v === 'entities') updateProjectOverview(); else document.getElementById('project-overview').style.display = 'none';
}

// ── entity filtering ──
let activeProject = '__all__', activeKind = 'all', fuseResults = null;
const cards = () => [...document.querySelectorAll('.entity-card')];
let fuse;
function rebuildFuse() {
  const fuseData = cards().map(c => ({id:c.dataset.id,name:c.dataset.name,kind:c.dataset.kind}));
  fuse = new Fuse(fuseData, {keys:['name','kind'],threshold:0.35});
}
rebuildFuse();
// Initial load: activeProject is '__all__', so apply any persisted collapse
// state from a previous session. (Project views always start expanded.)
// Defined later in this script — run on next tick so the function is in scope.
queueMicrotask(() => {
  applyCollapsedStateForAllView();
  // activeProject is '__all__' on load, so reveal each row's project label
  // (C1). applyFilters() toggles this on subsequent tab switches.
  document.getElementById('entity-list')?.classList.add('show-project');
});

function applyFilters() {
  const ids = fuseResults ? new Set(fuseResults.map(r=>r.item.id)) : null;
  const list = document.getElementById('entity-list');
  // 'flat' class hides group headers when a single-kind filter is active.
  list.classList.toggle('flat', activeKind !== 'all');
  // 'show-project' reveals each row's owning-project label, but only in the
  // cross-scope "all" view — on a project tab it'd be redundant (C1).
  list.classList.toggle('show-project', activeProject === '__all__');

  let any = false;
  cards().forEach(c => {
    // Inclusive filter: a project depends on its own MCPs *and* all globals.
    // When MCP kind is selected on a project tab, also show global-scoped MCPs
    // (they apply to every project at runtime).
    const projOK = activeProject === '__all__'
                || c.dataset.project === activeProject
                || (activeKind === 'mcp_server' && c.dataset.scope === 'global');
    const ok = projOK
             && (activeKind==='all'||c.dataset.kind===activeKind)
             && (!ids||ids.has(c.dataset.id));
    c.style.display = ok ? '' : 'none';
    if (ok) any = true;
  });
  // Hide whole kind-group sections that have no visible cards (and update counts).
  document.querySelectorAll('.kind-group').forEach(g => {
    const visible = g.querySelectorAll('.entity-card:not([style*="display: none"])').length;
    g.style.display = visible === 0 ? 'none' : '';
    const cnt = g.querySelector('.kind-group-count');
    if (cnt) cnt.textContent = visible;
  });
  document.getElementById('empty-list').style.display = any ? 'none' : '';
}

// ── kind-group collapse/expand persistence ──
// Persisted only for the 'all' view (activeProject === '__all__'). Project
// tabs always start fully expanded — collapsing while on a project is
// ephemeral and gets reset on the next tab switch.
const COLLAPSED_KEY = 'infra-mngmt:groups-collapsed';

// loadSet/saveSet — shared localStorage<->Set persistence used by the
// kind-group, bridge-composite, llama-logs, and llama-collapsed features.
// Both swallow storage/parse errors so a corrupt value never breaks the UI.
function loadSet(key) {
  try { return new Set(JSON.parse(localStorage.getItem(key) || '[]')); }
  catch (_) { return new Set(); }
}
function saveSet(key, set) {
  try { localStorage.setItem(key, JSON.stringify([...set])); } catch (_) {}
}

function readCollapsedSet() { return loadSet(COLLAPSED_KEY); }
function writeCollapsedSet(set) { saveSet(COLLAPSED_KEY, set); }

function applyCollapsedStateForAllView() {
  const collapsed = readCollapsedSet();
  document.querySelectorAll('.kind-group').forEach(g => {
    g.classList.toggle('collapsed', collapsed.has(g.dataset.kind));
  });
}

function expandAllKindGroups() {
  document.querySelectorAll('.kind-group.collapsed').forEach(g => g.classList.remove('collapsed'));
}

// ── composite bridge expand/collapse ──
// Click on a composite row toggles visibility of its member rows. State is
// kept in localStorage so it survives the every-8s panel refresh (htmx
// re-renders the partial; we re-apply expand state from storage on each
// htmx:afterSwap below).
function toggleCompositeMembers(name, ev) {
  // Action buttons on the composite row stop propagation themselves; this
  // guard catches clicks that reached us via bubbling from non-button areas.
  if (ev && ev.target && ev.target.closest('.proc-actions')) return;
  const expanded = !document.querySelector('tr.bridge-composite[data-composite="'+name+'"]')?.classList.contains('expanded');
  setCompositeExpanded(name, expanded);
}
function setCompositeExpanded(name, expanded) {
  const row = document.querySelector('tr.bridge-composite[data-composite="'+name+'"]');
  if (!row) return;
  row.classList.toggle('expanded', expanded);
  document.querySelectorAll('tr.bridge-member[data-member-of="'+name+'"]').forEach(m => {
    if (expanded) m.removeAttribute('hidden');
    else m.setAttribute('hidden', '');
  });
  // Persist preference per-composite.
  const key = 'im_composite_expanded';
  const set = loadSet(key);
  if (expanded) set.add(name); else set.delete(name);
  saveSet(key, set);
}
function reapplyCompositeExpansion() {
  const key = 'im_composite_expanded';
  const set = loadSet(key);
  document.querySelectorAll('tr.bridge-composite[data-composite]').forEach(row => {
    const name = row.getAttribute('data-composite');
    if (set.has(name)) setCompositeExpanded(name, true);
  });
}

// ── show internals toggle ──
// Body class gates CSS visibility of bridge-* PC processes; localStorage
// persists. Faster than a server round-trip — toggle is instant.
function toggleShowInternals() {
  const want = !document.body.classList.contains('show-internals');
  document.body.classList.toggle('show-internals', want);
  localStorage.setItem('im_show_internals', want ? '1' : '0');
  // Also flip any in-DOM toggle button labels so they reflect the new state.
  document.querySelectorAll('[data-toggle="show-internals"]').forEach(b => {
    b.textContent = want ? 'hide internals' : 'show internals';
    b.title = want ? 'hide internal bridge-* processes (already shown in bridges panel)'
                   : 'show internal bridge-* processes for debugging';
  });
}
function applyShowInternalsFromStorage() {
  if (localStorage.getItem('im_show_internals') === '1') {
    document.body.classList.add('show-internals');
    document.querySelectorAll('[data-toggle="show-internals"]').forEach(b => {
      b.textContent = 'hide internals';
      b.title = 'hide internal bridge-* processes (already shown in bridges panel)';
    });
  }
}

// ── llama logs <details> persistence ──
// The /partials/llama view re-renders every 10s; <details open> attribute
// resets to closed each time. Persist per-card open state in localStorage
// keyed by "instance|process" so user-opened panels stay open across the
// poll cycle. Same pattern as bridge-composite expansion + show-internals.
const LLAMA_LOGS_OPEN_KEY = 'im_llama_logs_open';
function onLlamaLogsToggle(el) {
  const key = el.getAttribute('data-llama-logs-key');
  if (!key) return;
  const set = loadSet(LLAMA_LOGS_OPEN_KEY);
  if (el.open) set.add(key); else set.delete(key);
  saveSet(LLAMA_LOGS_OPEN_KEY, set);
}
function applyLlamaLogsOpenFromStorage() {
  const set = loadSet(LLAMA_LOGS_OPEN_KEY);
  document.querySelectorAll('details.llama-logs-section[data-llama-logs-key]').forEach(d => {
    if (set.has(d.getAttribute('data-llama-logs-key'))) d.open = true;
  });
}

// ── llama header disclosure (collapse/expand card body) ──
// Persisted per card key (instance|process) in localStorage.
const LLAMA_COLLAPSED_KEY = 'im_llama_collapsed';

function readLlamaCollapsedSet() { return loadSet(LLAMA_COLLAPSED_KEY); }
function writeLlamaCollapsedSet(set) { saveSet(LLAMA_COLLAPSED_KEY, set); }

function applyLlamaCollapsedFromStorage() {
  const collapsed = readLlamaCollapsedSet();
  document.querySelectorAll('.llama-head[role="button"]').forEach(head => {
    const card = head.closest('.llama-card');
    const key = (head.getAttribute('aria-controls') || '').replace('llama-body-', '').replace(/-/g, '|');
    if (card && collapsed.has(key)) {
      card.classList.add('collapsed');
      head.setAttribute('aria-expanded', 'false');
    }
  });
}

function toggleLlamaCard(head) {
  const card = head.closest('.llama-card');
  if (!card) return;
  const nowCollapsed = card.classList.toggle('collapsed');
  head.setAttribute('aria-expanded', nowCollapsed ? 'false' : 'true');
  // Derive key from aria-controls id: "llama-body-{inst}-{proc}" → "inst|proc"
  // Must match applyLlamaCollapsedFromStorage which also uses replace(/-/g,'|').
  const bodyId = head.getAttribute('aria-controls') || '';
  const key = bodyId.replace(/^llama-body-/, '').replace(/-/g, '|');
  const set = readLlamaCollapsedSet();
  if (nowCollapsed) set.add(key); else set.delete(key);
  writeLlamaCollapsedSet(set);
}

function bindProjectHeaders() {
  document.querySelectorAll('tr.project-header[data-project]').forEach(hdr => {
    hdr.addEventListener('click', (e) => {
      // Don't collapse when clicking action buttons inside the header.
      if (e.target.closest('button')) return;
      const proj = hdr.dataset.project;
      const collapsed = hdr.classList.toggle('collapsed');
      document.querySelectorAll('tr.project-member[data-project="'+CSS.escape(proj)+'"]')
        .forEach(m => m.classList.toggle('project-collapsed', collapsed));
    });
  });
}

function bindLlamaDisclosures() {
  document.querySelectorAll('.llama-head[role="button"]').forEach(head => {
    head.addEventListener('click', (e) => {
      // Don't collapse when clicking action buttons inside the header.
      if (e.target.closest('button')) return;
      toggleLlamaCard(head);
    });
    head.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault();
        toggleLlamaCard(head);
      }
    });
  });
}

// Re-apply both states whenever htmx swaps in fresh services HTML.
document.addEventListener('htmx:afterSwap', (e) => {
  if (!e.target) return;
  if (e.target.id === 'services-inner' || e.target.id === 'view-services') {
    applyShowInternalsFromStorage();
    reapplyCompositeExpansion();
    bindProjectHeaders();
  }
  if (e.target.id === 'view-llama') {
    applyLlamaLogsOpenFromStorage();
    applyLlamaCollapsedFromStorage();
    bindLlamaDisclosures();
    // <details> fires 'toggle' on open/close. The event doesn't bubble,
    // so document-level delegation doesn't work — re-bind per element on
    // each HTMX swap. Each swap creates fresh DOM nodes, so prior
    // listeners are GC'd with the old nodes.
    document.querySelectorAll('details.llama-logs-section[data-llama-logs-key]').forEach(d => {
      d.addEventListener('toggle', () => onLlamaLogsToggle(d));
    });
  }
});
// And on initial load.
document.addEventListener('DOMContentLoaded', () => {
  applyShowInternalsFromStorage();
  reapplyCompositeExpansion();
  applyLlamaLogsOpenFromStorage();
  applyLlamaCollapsedFromStorage();
  bindLlamaDisclosures();
  bindProjectHeaders();
});

// ── In-app confirm modal (B7) ───────────────────────────────────────────────
// htmx's hx-confirm defaults to the native window.confirm() — unstyleable, off
// the app's theme, and auto-dismissed by headless automation (so a UI-driven
// load/unload/drain/pause silently no-ops). Intercept htmx:confirm and render a
// styled modal that matches the blast-radius/promote chrome instead. Built in a
// page-level overlay so a section poll can't clobber it.
function showConfirmModal(question, onProceed) {
  document.getElementById('confirm-slot')?.remove();
  // Split "Title? trailing detail…" into a heading + body, mirroring the
  // blast-radius modal's title/sub split.
  const m = question.match(/^(.*?\?)\s*([\s\S]*)$/);
  const title = m ? m[1] : 'Confirm action';
  const detail = m ? m[2] : question;
  const wrap = document.createElement('div');
  wrap.id = 'confirm-slot';
  wrap.innerHTML =
    '<div class="promote-modal"><div class="promote-modal-card">' +
    '<div class="promote-modal-head"><div class="promote-modal-titles">' +
    '<div class="promote-modal-title"></div></div>' +
    '<button class="promote-close" type="button" aria-label="cancel">×</button></div>' +
    '<div class="promote-modal-body"><p class="promote-result-prompt"></p>' +
    '<div class="promote-result-actions">' +
    '<button class="promote-cancel" type="button">cancel</button>' +
    '<button class="promote-confirm" type="button">proceed</button>' +
    '</div></div></div></div>';
  // textContent (not innerHTML) for the question — never inject it as markup.
  wrap.querySelector('.promote-modal-title').textContent = title;
  const prompt = wrap.querySelector('.promote-result-prompt');
  if (detail) { prompt.textContent = detail; } else { prompt.remove(); }
  const close = () => { wrap.remove(); document.removeEventListener('keydown', onKey); };
  const onKey = (ev) => { if (ev.key === 'Escape') close(); };
  wrap.querySelector('.promote-cancel').addEventListener('click', close);
  wrap.querySelector('.promote-close').addEventListener('click', close);
  wrap.querySelector('.promote-modal').addEventListener('click', (ev) => {
    if (ev.target === ev.currentTarget) close(); // backdrop click cancels
  });
  wrap.querySelector('.promote-confirm').addEventListener('click', () => { close(); onProceed(); });
  document.body.appendChild(wrap);
  document.addEventListener('keydown', onKey);
  wrap.querySelector('.promote-confirm').focus();
}

document.addEventListener('htmx:confirm', (e) => {
  const q = e.detail && e.detail.question; // hx-confirm text; null when unset
  if (!q) return;            // no hx-confirm on this element → let htmx proceed
  e.preventDefault();        // suppress the native confirm + the immediate request
  showConfirmModal(q, () => e.detail.issueRequest(true)); // true = skip htmx's own confirm
});

function toggleKindGroup(headerEl) {
  const group = headerEl.parentElement;
  group.classList.toggle('collapsed');
  // Only persist while on the all view; per-project collapse is intentionally
  // session-only so each project tab opens with everything visible.
  if (activeProject === '__all__') {
    const collapsed = readCollapsedSet();
    const kind = group.dataset.kind;
    if (group.classList.contains('collapsed')) collapsed.add(kind);
    else collapsed.delete(kind);
    writeCollapsedSet(collapsed);
  }
}

// ── project overview ──
const kindIcons = {mcp_server:'⬡',command:'$',agent:'◉',skill:'✦',memory:'▤',hook:'↪',claude_md:'#'};
const kindLabels = {mcp_server:'mcp servers',command:'commands',agent:'agents',skill:'skills',memory:'memory',hook:'hooks',claude_md:'claude.md'};
let ovExpanded = false;
let selectedEntityProject = null;

function updateProjectOverview() {
  const ov = document.getElementById('project-overview');
  const displayProject = selectedEntityProject || activeProject;
  const isAll = displayProject === '__all__' || !displayProject;

  ['ov-name','ov-scopes','ov-sep','ov-counts','ov-toggle'].forEach(id => {
    document.getElementById(id).style.display = isAll ? 'none' : '';
  });
  document.getElementById('ov-row').style.cursor = isAll ? 'default' : 'pointer';

  if (!isAll) {
    const tab = [...document.querySelectorAll('.tab')].find(t => t.dataset.project === displayProject);
    let name = displayProject.split('/').pop() || displayProject;
    if (tab) {
      const clone = tab.cloneNode(true);
      clone.querySelectorAll('.scope-badge').forEach(n => n.remove());
      name = clone.textContent.trim();
    }
    const counts = {};
    document.querySelectorAll('.entity-card').forEach(c => {
      if (c.dataset.project === displayProject) counts[c.dataset.kind] = (counts[c.dataset.kind]||0)+1;
    });
    document.getElementById('ov-name').textContent = name;
    const scopesEl = document.getElementById('ov-scopes');
    scopesEl.innerHTML = '';
    if (tab) tab.querySelectorAll('.scope-badge').forEach(b => scopesEl.appendChild(b.cloneNode(true)));
    // Prototype: single total "N entities" / "1 entity" text in the header
    // row; per-kind breakdown lives only in the expanded panel below.
    const countsEl = document.getElementById('ov-counts');
    const total = Object.values(counts).reduce((a, b) => a + b, 0);
    countsEl.textContent = total + ' ' + (total === 1 ? 'entity' : 'entities');
    const detailEl = document.getElementById('ov-detail');
    detailEl.innerHTML = '';
    // Path line
    const pathLine = document.createElement('div');
    pathLine.className = 'ov-detail-path';
    pathLine.textContent = displayProject;
    detailEl.appendChild(pathLine);
    // Kind-breakdown chips row
    if (Object.keys(counts).length > 0) {
      const chipsRow = document.createElement('div');
      chipsRow.className = 'ov-detail-chips';
      ['mcp_server','command','agent','skill','hook','memory','claude_md'].filter(k => counts[k]).forEach(k => {
        const chip = document.createElement('span');
        chip.className = 'ov-detail-chip';
        const ic = document.createElement('span');
        ic.className = 'kind-icon ' + k;
        ic.textContent = kindIcons[k] || '·';
        const lbl = document.createElement('span');
        lbl.className = 'ov-detail-chip-label';
        lbl.textContent = kindLabels[k] || k.replace('_', ' ');
        const cnt = document.createElement('span');
        cnt.className = 'ov-detail-chip-count';
        cnt.textContent = counts[k];
        chip.appendChild(ic);
        chip.appendChild(lbl);
        chip.appendChild(cnt);
        chipsRow.appendChild(chip);
      });
      detailEl.appendChild(chipsRow);
    }
    detailEl.style.display = ovExpanded ? '' : 'none';
    document.getElementById('ov-toggle').className = 'ov-toggle' + (ovExpanded ? ' open' : '');
  } else {
    document.getElementById('ov-detail').style.display = 'none';
  }

  ov.style.display = isAll ? 'none' : 'block';
  // Delegate container rendering to containers.js via window global.
  if (window.refreshControls) window.refreshControls();
}

function toggleOverview() {
  const displayProject = selectedEntityProject || activeProject;
  if (!displayProject || displayProject === '__all__') return;
  ovExpanded = !ovExpanded;
  document.getElementById('ov-detail').style.display = ovExpanded ? '' : 'none';
  document.getElementById('ov-toggle').className = 'ov-toggle' + (ovExpanded ? ' open' : '');
}

async function doRescan(e) {
  e.stopPropagation();
  const btn = document.getElementById('ov-rescan');
  btn.classList.add('spinning');
  btn.disabled = true;
  try {
    const r = await fetch('/api/sources/rescan', {method:'POST'});
    if (!r.ok) {
      showToast({title: 'rescan failed', body: 'HTTP ' + r.status});
      return;
    }
    const body = await r.json();
    const added = (body.added || []).length;
    const discovered = (body.discovered || []).length;
    if (added > 0) {
      // Refresh the entity list so the new sources actually render. Pass a
      // synthetic event because doRefresh() calls e.stopPropagation().
      await doRefresh({stopPropagation:()=>{}});
      showToast({title: 'rescan complete', body: 'added ' + added + ' new source' + (added===1?'':'s') + ': ' + body.added.join(', '), kind: 'ok'});
    } else {
      // Surface what was actually scanned so the user can debug "why didn't
      // my new project show up?". The list is the universe of sources
      // discovery currently sees; if their new project isn't there, the
      // problem is upstream (no .claude/, no devcontainer label, not in a
      // scanned workspace dir, etc.).
      showToast({
        title: 'rescan complete',
        body: 'no new sources (scanned ' + discovered + '): ' + (body.discovered || []).join(', '),
        kind: 'info',
      });
    }
  } catch (err) {
    showToast({title: 'rescan error', body: String(err)});
  } finally {
    btn.classList.remove('spinning');
    btn.disabled = false;
  }
}

async function doRefresh(e) {
  e.stopPropagation();
  const btn = document.getElementById('ov-refresh');
  if (btn) { btn.classList.add('spinning'); btn.disabled = true; }
  // Remember which entity was selected so we can re-mark it after the swap.
  const prevSelectedId = document.querySelector('.entity-card.selected')?.dataset.id || null;
  try {
    const refreshResp = await fetch('/api/refresh', {method:'POST'});
    if (!refreshResp.ok) {
      showToast({title: 'refresh failed', body: 'HTTP ' + refreshResp.status});
      return;
    }
    const listResp = await fetch('/partials/entity-list');
    if (!listResp.ok) {
      showToast({title: 'refresh failed', body: 'HTTP ' + listResp.status});
      return;
    }
    const html = await listResp.text();
    document.getElementById('entity-list').innerHTML = html;
    rebuildFuse();
    // Re-apply collapse state since the swap rebuilt the .kind-group elements.
    if (activeProject === '__all__') applyCollapsedStateForAllView();
    if (prevSelectedId) {
      const card = document.querySelector('.entity-card[data-id="'+prevSelectedId.replace(/"/g,'\\"')+'"]');
      if (card) card.classList.add('selected');
    }
    if (window.refreshControls) window.refreshControls();
    applyFilters();
    updateProjectOverview();
  } finally {
    if (btn) { btn.classList.remove('spinning'); btn.disabled = false; }
  }
}

document.querySelectorAll('.tab').forEach(t => t.addEventListener('click', () => {
  document.querySelectorAll('.tab').forEach(x=>x.classList.remove('active'));
  t.classList.add('active');
  activeProject = t.dataset.project;
  window.activeProject = activeProject;
  selectedEntityProject = null;
  window.selectedEntityProject = null;
  if (window.closeLogStream) window.closeLogStream();
  // Switching to a project tab expands everything (project views start fresh);
  // switching back to all re-applies the persisted collapse state.
  if (activeProject === '__all__') applyCollapsedStateForAllView();
  else expandAllKindGroups();
  applyFilters();
  updateProjectOverview();
}));
document.querySelectorAll('.pill').forEach(p => p.addEventListener('click', () => {
  document.querySelectorAll('.pill').forEach(x=>x.classList.remove('active'));
  p.classList.add('active'); activeKind = p.dataset.kind; applyFilters();
}));

const search = document.getElementById('search');
const searchWrap = search.closest('.search-wrap');
function setSearchHasValue() {
  // Keep the wrap expanded while there's content even after the input blurs;
  // the collapse animation only fires when blurring an empty input.
  searchWrap.classList.toggle('has-value', search.value.trim() !== '');
}
search.addEventListener('input', () => {
  const q = search.value.trim();
  fuseResults = q ? fuse.search(q) : null;
  setSearchHasValue();
  applyFilters();
});
document.addEventListener('keydown', e => {
  if ((e.metaKey||e.ctrlKey) && e.key==='k') { e.preventDefault(); search.focus(); search.select(); }
  if (e.key==='Escape' && document.activeElement===search) {
    search.value=''; fuseResults=null; setSearchHasValue(); applyFilters(); search.blur();
  }
});

// ── entity preview ──
function activateEntityCard(card) {
  document.querySelectorAll('.entity-card.selected').forEach(c=>c.classList.remove('selected'));
  card.classList.add('selected');
  card.setAttribute('aria-current', 'true');
  document.querySelectorAll('.entity-card:not(.selected)').forEach(c=>c.removeAttribute('aria-current'));
  selectedEntityProject = card.dataset.project || null;
  window.selectedEntityProject = selectedEntityProject;
  updateProjectOverview();
  document.getElementById('preview').innerHTML = '<div class="preview-loading"><span class="spinner"></span>loading…</div>';
  htmx.ajax('GET', '/partials/entity?id='+encodeURIComponent(card.dataset.id), {target:'#preview',swap:'innerHTML'});
}
document.getElementById('entity-list').addEventListener('click', e => {
  const card = e.target.closest('.entity-card');
  if (!card) return;
  activateEntityCard(card);
});
// Cards are real <button>s — Space/Enter activate natively, no extra keydown needed.

// ── toast notifications ─────────────────────────────────────────
// HTMX swallows 4xx/5xx by default (no swap), so without this the user sees
// nothing on failure. Surface server-side errors as bottom-right toasts.
function showToast({title, body, kind='error', timeout=8000}) {
  const stack = document.getElementById('toast-stack');
  if (!stack) return;
  const t = document.createElement('div');
  t.className = 'toast' + (kind === 'info' ? ' info' : kind === 'ok' ? ' ok' : '');
  const close = document.createElement('button');
  close.className = 'toast-close';
  close.setAttribute('aria-label', 'dismiss');
  close.textContent = '×';
  close.onclick = () => removeToast(t);
  if (title) {
    const h = document.createElement('div');
    h.className = 'toast-title';
    h.textContent = title;
    t.appendChild(h);
  }
  if (body) {
    const b = document.createElement('div');
    b.className = 'toast-body';
    b.textContent = body;
    t.appendChild(b);
  }
  t.appendChild(close);
  stack.appendChild(t);
  // Force reflow so the transition fires.
  // eslint-disable-next-line no-unused-expressions
  t.offsetWidth;
  t.classList.add('show');
  if (timeout > 0) setTimeout(() => removeToast(t), timeout);
}
function removeToast(t) {
  if (!t || !t.parentNode) return;
  t.classList.remove('show');
  setTimeout(() => t.remove(), 200);
}

// Vault buttons (allow/disallow/tree drill) workaround.
//
// The vault panel loads via htmx into #vault-X-mount with hx-trigger="load",
// and the response contains hx-post buttons targeting that same mount with
// hx-swap="innerHTML". htmx 2.0.4 fails to wire the click handlers on those
// buttons — verified empirically: htmx:configRequest / beforeRequest never
// fire when the user clicks "remove", even though the button shows
// `htmx-internal-data` is set (i.e., htmx looked at the button at some
// point). Removing the hx-preserve on #vaults-section does not help; the
// failure is independent of preservation. The exact mechanism inside htmx
// is unclear — suspected interaction between hx-trigger="load"-loaded
// content and the swap target pointing back at the same mount element.
//
// Pragmatic fix: replicate the htmx swap logic for `/api/vault/*` URLs
// using vanilla fetch + innerHTML/outerHTML assignment. Scope is narrow
// (vault endpoints only); other partials continue to use htmx normally.
// If we later figure out the root cause, this can be removed.
document.body.addEventListener('click', e => {
  const btn = e.target.closest('button[hx-post], button[hx-get]');
  if (!btn) return;
  const method = btn.hasAttribute('hx-post') ? 'POST' : 'GET';
  const url = btn.getAttribute(method === 'POST' ? 'hx-post' : 'hx-get');
  if (!url || !url.startsWith('/api/vault/') && !url.startsWith('/partials/vault/')) return;
  const targetSel = btn.getAttribute('hx-target');
  const target = targetSel ? document.querySelector(targetSel) : null;
  const swap = btn.getAttribute('hx-swap') || 'innerHTML';
  e.preventDefault();
  e.stopImmediatePropagation();
  fetch(url, {method}).then(r => {
    if (!r.ok) throw new Error('HTTP ' + r.status);
    return r.text();
  }).then(html => {
    if (!target) return;
    if (swap === 'outerHTML') target.outerHTML = html;
    else target.innerHTML = html;
    // After manual swap, re-process for nested htmx attrs (e.g., the tree
    // drill buttons inside the new panel content). They'll hit this same
    // delegation handler on click thanks to the URL prefix filter above.
    if (window.htmx && window.htmx.process) window.htmx.process(target);
  }).catch(err => {
    if (window.showToast) window.showToast({title: 'vault action failed', body: err.message || String(err)});
  });
});
// Dismiss the blast-radius confirm modal once its "proceed" action succeeds
// (the action itself swaps #services-inner; this clears the modal in #blast-slot).
document.body.addEventListener('htmx:afterRequest', e => {
  if (!e.detail.successful) return;
  const verb = (e.detail.requestConfig && e.detail.requestConfig.verb || '').toLowerCase();
  if (verb === 'post' && e.target.closest && e.target.closest('#blast-slot')) {
    const slot = document.getElementById('blast-slot');
    if (slot) slot.innerHTML = '';
  }
});
document.body.addEventListener('htmx:responseError', e => {
  const xhr = e.detail.xhr;
  const verb = (e.detail.requestConfig && e.detail.requestConfig.verb || '').toUpperCase();
  // pathInfo.requestPath is set for hx-* requests; requestConfig.path is the
  // fallback. Compute from both so routing works regardless of htmx version.
  const path = (e.detail.pathInfo && e.detail.pathInfo.requestPath) ||
               (e.detail.requestConfig && e.detail.requestConfig.path) || '';
  const text = (xhr.responseText || xhr.statusText || 'request failed').trim();
  // Trim absurdly long bodies; full detail is in the server journal.
  const body = text.length > 400 ? text.slice(0, 400) + '…' : text;
  // Container actions get a friendlier title. This branch was formerly a
  // separate listener in containers.js; consolidated here so a /api/container/*
  // failure toasts once, not twice.
  if (path.startsWith('/api/container/')) {
    showToast({title: 'container action failed', body: body || ('HTTP ' + xhr.status)});
    return;
  }
  showToast({
    title: xhr.status + ' ' + (verb ? verb + ' ' : '') + path,
    body: body,
  });
});
document.body.addEventListener('htmx:sendError', e => {
  showToast({
    title: 'network error',
    body: 'could not reach the server — is infra-mngmt still running?',
  });
});

// ── build chip ──
function initBuildChip() {
  const chip = document.getElementById('build-chip');
  if (!chip) return;
  const el = document.getElementById('build-chip-when');
  const epoch = parseInt(chip.dataset.epoch, 10);
  if (epoch) {
    const fmt = () => {
      const diff = Date.now()/1000 - epoch;
      if (diff < 60) return Math.floor(diff)+'s ago';
      if (diff < 3600) return Math.floor(diff/60)+'m ago';
      if (diff < 86400) return Math.floor(diff/3600)+'h ago';
      return Math.floor(diff/86400)+'d ago';
    };
    el.textContent = fmt();
    setInterval(() => { el.textContent = fmt(); }, 30000);
  }
  const curEpoch = chip.dataset.epoch || '';
  const curCommit = chip.dataset.commit || '';
  let stale = false;
  async function checkVersion(){
    if (stale) return;
    try {
      const r = await fetch('/api/version', {cache:'no-store'});
      if (!r.ok) return;
      const v = await r.json();
      const epochDiff  = v.build_epoch && v.build_epoch !== curEpoch;
      const commitDiff = v.commit      && v.commit      !== curCommit;
      if (epochDiff || commitDiff) {
        stale = true;
        chip.classList.add('stale');
        chip.title = 'New version available — click to reload';
        chip.addEventListener('click', () => location.reload());
      }
    } catch(_) { /* server may be restarting; retry on next tick */ }
  }
  setInterval(checkVersion, 30000);
  setTimeout(checkVersion, 5000);
}

if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', initBuildChip);
} else {
  initBuildChip();
}

updateProjectOverview();

// Expose globals for cross-module access (containers.js, preview.js read these).
window.activeProject = activeProject;
window.selectedEntityProject = selectedEntityProject;

// ── index shell: direct addEventListener for static (non-HTMX-swapped) buttons ──
document.getElementById('vtab-entities').addEventListener('click', () => showView('entities'));
document.getElementById('vtab-llama').addEventListener('click', () => showView('llama'));
document.getElementById('vtab-services').addEventListener('click', () => showView('services'));
document.getElementById('ov-rescan').addEventListener('click', e => doRescan(e));
document.getElementById('ov-refresh')?.addEventListener('click', e => doRefresh(e));
document.getElementById('ov-toggle').addEventListener('click', () => toggleOverview());
for (const btn of document.querySelectorAll('.tab-scroll-btn')) {
  btn.addEventListener('click', () => {
    const dir = btn.dataset.dir === 'right' ? 140 : -140;
    document.getElementById('source-tabs-bar').scrollBy({left: dir, behavior: 'smooth'});
  });
}

// ── event delegation: entity-list kind-group headers (HTMX-swapped region) ──
document.body.addEventListener('click', e => {
  const header = e.target.closest('.kind-group-header');
  if (header) toggleKindGroup(header);
});

// ── event delegation: services composite-row toggle + inner action buttons ──
// Clicks on HTMX action buttons (hx-post/hx-get) must not toggle the row.
document.body.addEventListener('click', e => {
  const compositeRow = e.target.closest('.bridge-composite');
  if (compositeRow && !e.target.closest('button[hx-post], button[hx-get]')) {
    toggleCompositeMembers(compositeRow.dataset.composite, e);
  }
});

// ── event delegation: show-internals toggle button (HTMX-polled region) ──
document.body.addEventListener('click', e => {
  if (e.target.closest('.show-internals-btn')) toggleShowInternals();
});

// ── window exports for cross-module access ──
window.showView = showView;
window.doRescan = doRescan;
window.doRefresh = doRefresh;
window.toggleKindGroup = toggleKindGroup;
window.toggleOverview = toggleOverview;
window.toggleCompositeMembers = toggleCompositeMembers;
window.toggleShowInternals = toggleShowInternals;
window.onLlamaLogsToggle = onLlamaLogsToggle;
window.showToast = showToast;
