// ── container controls ──
let _logEventSource = null;
let _logContainerId = null;

function refreshControls() {
  const project = window.selectedEntityProject || window.activeProject;
  if (!project || !window.htmx) return;
  window.htmx.ajax('GET', '/partials/container-controls?project=' + encodeURIComponent(project),
                   {target: '#ov-ctr', swap: 'innerHTML'});
}

async function openVSCodeAttach(id) {
  try {
    const r = await fetch('/api/container/open-vscode?id='+encodeURIComponent(id), {method:'POST'});
    if (r.ok) return;
    const txt = await r.text();
    window.showToast({title:'VS Code launch failed', body: txt.trim() || ('HTTP '+r.status)});
  } catch (e) {
    window.showToast({title:'VS Code launch failed', body: e.message || String(e)});
  }
}

// Action → glyph + color class for the event log.
const eventStyle = (action) => {
  if (action === 'start' || action === 'health_status: healthy') return ['✓','event-ok'];
  if (action === 'die' || action === 'kill' || action === 'health_status: unhealthy' || action === 'oom') return ['✖','event-err'];
  if (action === 'restart' || action === 'health_status: starting') return ['↺','event-warn'];
  if (action === 'exec_create' || action === 'exec_start' || action === 'attach') return ['⚙','event-info'];
  return ['·','event-info'];
};

function fmtEventTime(unixSecs) {
  if (!unixSecs) return '';
  const d = new Date(unixSecs * 1000);
  return d.toTimeString().slice(0,8); // HH:MM:SS
}

function openLogStream(id) {
  if (_logEventSource) _logEventSource.close();
  _logContainerId = id;
  const panel = document.getElementById('ov-log-panel');
  // No inline onclick — CSP is script-src 'self' (no unsafe-inline), so an
  // onclick attribute would silently no-op. The close button is wired via the
  // delegated .ctr-log-close listener below.
  panel.innerHTML = '<div class="ctr-log-wrap"><div class="ctr-log-bar">container events<button class="ctr-log-close" aria-label="close">×</button></div><div class="ctr-event-list" id="ctr-event-list"></div></div>';
  panel.style.display = '';

  _logEventSource = new EventSource('/api/container/events-stream?id='+encodeURIComponent(id));
  _logEventSource.addEventListener('docker_event', e => {
    const list = document.getElementById('ctr-event-list');
    if (!list) return;
    try {
      const ev = JSON.parse(e.data);
      const [glyph, cls] = eventStyle(ev.action);
      const row = document.createElement('div');
      row.className = 'ctr-event ' + cls;
      row.innerHTML =
        '<span class="ctr-event-time">' + fmtEventTime(ev.time) + '</span>' +
        '<span class="ctr-event-glyph">' + glyph + '</span>' +
        '<span class="ctr-event-action">' + (ev.action || '?') + '</span>';
      list.appendChild(row);
      list.scrollTop = list.scrollHeight;
    } catch(_) {}
  });
  _logEventSource.addEventListener('state', () => {
    refreshControls();
  });
  function onStreamEnd() {
    _logEventSource && _logEventSource.close();
    _logEventSource = null; _logContainerId = null;
    refreshControls();
  }
  _logEventSource.addEventListener('done', onStreamEnd);
  _logEventSource.onerror = onStreamEnd;
}

function closeLogStream() {
  if (_logEventSource) { _logEventSource.close(); _logEventSource = null; }
  _logContainerId = null;
  const panel = document.getElementById('ov-log-panel');
  if (panel) { panel.style.display = 'none'; panel.innerHTML = ''; }
  refreshControls();
}

// Note: the htmx:responseError toast for /api/container/* failures lives in
// app.js's single consolidated listener (avoids double-toasting). Close button
// for the event-log panel is wired by delegation since its markup is injected
// via innerHTML and CSP forbids inline onclick handlers.
document.body.addEventListener('click', e => {
  if (e.target.closest('.ctr-log-close')) closeLogStream();
});

// Initial controls render — depends on app.js having set window.activeProject.
// Use queueMicrotask so we run after app.js's initial bootstrap.
queueMicrotask(() => refreshControls());

// ── event delegation: VS Code attach button (HTMX-swapped region) ──
document.body.addEventListener('click', e => {
  const btn = e.target.closest('#ov-ctr [data-vsid]');
  if (btn) openVSCodeAttach(btn.dataset.vsid);
});

// ── window exports for cross-module access ──
window.openVSCodeAttach = openVSCodeAttach;
window.closeLogStream = closeLogStream;
window.openLogStream = openLogStream;
window.refreshControls = refreshControls;
