package web

const indexHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>infra-mngmt</title>
<link rel="icon" href="data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 32 32'><rect width='32' height='32' rx='6' fill='%230d0d0d'/><text x='50%25' y='54%25' dominant-baseline='middle' text-anchor='middle' font-size='20' fill='%234a9eff'>⬡</text></svg>">
<script src="https://unpkg.com/htmx.org@2.0.4/dist/htmx.min.js"></script>
<script src="https://cdn.jsdelivr.net/npm/fuse.js@7.0.0/dist/fuse.min.js"></script>
<script src="https://cdn.jsdelivr.net/npm/marked@12/marked.min.js"></script>
<script type="module">
import {EditorView,basicSetup} from "https://esm.sh/codemirror@6";
import {markdown} from "https://esm.sh/@codemirror/lang-markdown@6";
import {EditorState} from "https://esm.sh/@codemirror/state@6";
window._cm = {EditorView, EditorState, basicSetup, markdown};
window._cmResolve?.();
</script>
<style>
*,*::before,*::after{box-sizing:border-box;margin:0;padding:0}
:root{
  --bg:#0d0d0d;--bg2:#111;--bg3:#181818;--bg4:#1e1e1e;
  --border:#222;--border2:#2a2a2a;
  --text:#c9c9c9;--text2:#888;--text3:#444;--white:#f0f0f0;
  --accent:#4a9eff;--accent2:#b899ff;
  --green:#6bcf7f;--red:#e06c6c;--yellow:#ffcc5c;--orange:#f0a04a;
  --kind-mcp:#4a9eff;--kind-command:#6bcf7f;--kind-agent:#f0a04a;
  --kind-skill:#e06c8a;--kind-memory:#9b9b9b;--kind-hook:#ffcc5c;--kind-claude_md:#aaa;
}
html,body{height:100%;overflow:hidden}
body{font-family:ui-monospace,monospace;font-size:12px;background:var(--bg);color:var(--text);display:flex;flex-direction:column}

/* ── header ── */
header{display:flex;align-items:center;gap:12px;padding:8px 16px;border-bottom:1px solid var(--border);flex-shrink:0;min-height:40px}
.logo{color:var(--white);font-size:13px;font-weight:600;letter-spacing:.04em;flex-shrink:0;margin-right:4px;display:flex;align-items:center;gap:6px}
.logo span{color:var(--text3);font-weight:normal}
.view-tabs{display:flex;gap:2px;flex-shrink:0}
.view-tab{background:transparent;border:1px solid transparent;color:var(--text2);padding:3px 10px;border-radius:4px;cursor:pointer;font-family:inherit;font-size:11px;transition:all .12s}
.view-tab:hover{border-color:var(--border2);color:var(--text)}
.view-tab.active{background:var(--bg4);border-color:var(--border2);color:var(--white)}
.divider{width:1px;background:var(--border);align-self:stretch;margin:0 4px}
.tab-scroll-wrap{display:flex;align-items:center;flex:1;min-width:0;gap:4px}
.tab-scroll-btn{background:var(--bg3);border:1px solid var(--border2);color:var(--text2);padding:2px 7px;border-radius:3px;cursor:pointer;font-size:12px;line-height:1.4;flex-shrink:0;font-family:inherit;transition:all .12s}
.tab-scroll-btn:hover{border-color:var(--accent);color:var(--accent);background:var(--bg4)}
.source-tabs{display:flex;gap:4px;flex:1;overflow-x:auto;scrollbar-width:none;padding:0 4px}
.source-tabs::-webkit-scrollbar{display:none}
.tab{background:transparent;border:1px solid transparent;color:var(--text2);padding:3px 10px;border-radius:4px;cursor:pointer;font-family:inherit;font-size:11px;white-space:nowrap;transition:all .12s}
.tab:hover{border-color:var(--border2);color:var(--text)}
.tab.active{background:var(--bg4);border-color:var(--border2);color:var(--white)}
.tab .scope-badge{font-size:9px;margin-left:5px;padding:1px 5px;border-radius:3px}
.tab .scope-badge.global{background:#1e3a5f;color:var(--accent)}
.tab .scope-badge.project{background:#2d1f3d;color:var(--accent2)}
.tab .scope-badge.devcontainer{background:#1a2d1a;color:#6bcf7f}
#search{background:var(--bg3);border:1px solid var(--border2);color:var(--text);padding:4px 8px;border-radius:4px;font-family:inherit;font-size:11px;width:180px;outline:none;flex-shrink:0}
#search:focus{border-color:#444}
#search::placeholder{color:var(--text3)}

/* ── kind filter bar ── */
.kind-bar{display:flex;gap:4px;padding:8px 16px;border-bottom:1px solid var(--border);flex-shrink:0}
.pill{background:transparent;border:1px solid var(--border);color:var(--text2);padding:2px 8px;border-radius:12px;cursor:pointer;font-family:inherit;font-size:10px;transition:all .12s}
.pill:hover{border-color:var(--border2);color:var(--text)}
.pill.active{color:var(--white)}
.pill-icon{display:inline-block}
.pill[data-kind="mcp_server"] .pill-icon{color:var(--kind-mcp)}
.pill[data-kind="command"] .pill-icon{color:var(--kind-command)}
.pill[data-kind="agent"] .pill-icon{color:var(--kind-agent)}
.pill[data-kind="skill"] .pill-icon{color:var(--kind-skill)}
.pill[data-kind="memory"] .pill-icon{color:var(--kind-memory)}
.pill[data-kind="hook"] .pill-icon{color:var(--kind-hook)}
.pill[data-kind="claude_md"] .pill-icon{color:var(--kind-claude_md)}
.pill[data-kind="mcp_server"].active{border-color:var(--kind-mcp)}
.pill[data-kind="command"].active{border-color:var(--kind-command)}
.pill[data-kind="agent"].active{border-color:var(--kind-agent)}
.pill[data-kind="skill"].active{border-color:var(--kind-skill)}
.pill[data-kind="memory"].active{border-color:var(--kind-memory)}
.pill[data-kind="hook"].active{border-color:var(--kind-hook)}
.pill[data-kind="claude_md"].active{border-color:var(--kind-claude_md)}

/* ── entity panels ── */
.panels{display:flex;flex:1;overflow:hidden}
.entity-list{width:320px;flex-shrink:0;overflow-y:auto;border-right:1px solid var(--border);padding:8px 0}
.entity-card{display:flex;align-items:baseline;gap:8px;padding:5px 16px;cursor:pointer;border-left:2px solid transparent;transition:background .1s}
.entity-card:hover{background:var(--bg2)}
.entity-card.selected{background:var(--bg3);border-left-color:var(--accent)}
.kind-icon{font-size:10px;flex-shrink:0;width:10px;text-align:center}
.kind-icon.mcp_server{color:var(--kind-mcp)}.kind-icon.command{color:var(--kind-command)}
.kind-icon.agent{color:var(--kind-agent)}.kind-icon.skill{color:var(--kind-skill)}
.kind-icon.memory{color:var(--kind-memory)}.kind-icon.hook{color:var(--kind-hook)}
.kind-icon.claude_md{color:var(--kind-claude_md)}
.entity-name{flex:1;color:var(--text);overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.entity-scope-dot{font-size:8px;flex-shrink:0}
.entity-scope-dot.global{color:var(--accent)}
.entity-scope-dot.project{color:var(--accent2)}
.entity-scope-dot.devcontainer{color:#6bcf7f}
.empty-list{color:var(--text3);padding:24px 16px;font-style:italic}

/* ── preview pane ── */
.preview{flex:1;overflow-y:auto;padding:20px 24px;min-width:0}
.preview-hint{color:var(--text3);font-style:italic;margin-top:40px}
@keyframes spin{to{transform:rotate(360deg)}}
.preview-loading{color:var(--text2);display:flex;flex-direction:column;align-items:center;padding-top:80px;gap:12px}
.spinner{width:20px;height:20px;border:2px solid var(--border2);border-top-color:var(--accent);border-radius:50%;animation:spin .7s linear infinite}
.preview-header{margin-bottom:16px;padding-bottom:12px;border-bottom:1px solid var(--border)}
.preview-name{color:var(--white);font-size:14px;font-weight:600;margin-bottom:4px}
.preview-meta{display:flex;gap:16px;color:var(--text2);font-size:11px}
.preview-path{color:var(--text3);font-size:10px;margin-top:6px;word-break:break-all}
.preview-content{background:var(--bg2);border:1px solid var(--border);border-radius:4px;padding:16px;overflow-x:auto;line-height:1.6}
.preview-content pre{white-space:pre-wrap;word-break:break-word;color:var(--text)}
.preview-no-content{color:var(--text3);font-style:italic;margin-top:12px}

/* ── services view ── */
#view-services{flex:1;overflow-y:auto;padding:16px 24px;display:none}
.svc-grid{display:flex;flex-direction:column;gap:20px}
.svc-instance{background:var(--bg2);border:1px solid var(--border);border-radius:6px;overflow:hidden}
.svc-header{display:flex;align-items:center;gap:10px;padding:10px 14px;border-bottom:1px solid var(--border);background:var(--bg3)}
.svc-name{color:var(--white);font-weight:600;font-size:13px}
.svc-endpoint{color:var(--text3);font-size:10px}
.online-dot{width:7px;height:7px;border-radius:50%;flex-shrink:0}
.online-dot.online{background:var(--green)}.online-dot.offline{background:var(--red)}
.svc-boot-btn{margin-left:auto;background:var(--bg4);border:1px solid var(--border2);color:var(--accent);padding:3px 10px;border-radius:4px;cursor:pointer;font-family:inherit;font-size:10px}
.svc-offline{padding:12px 14px;color:var(--text3);font-style:italic}
.process-table{width:100%;border-collapse:collapse}
.process-table th{text-align:left;padding:5px 14px;font-size:10px;color:var(--text3);border-bottom:1px solid var(--border);font-weight:normal;text-transform:uppercase;letter-spacing:.06em}
.process-table td{padding:6px 14px;border-bottom:1px solid var(--border);font-size:11px;vertical-align:middle}
.process-table tr:last-child td{border-bottom:none}
.process-table tr:hover td{background:var(--bg3)}
.status-pill{display:inline-flex;align-items:center;gap:4px;padding:1px 7px;border-radius:10px;font-size:10px}
.status-pill .dot{width:5px;height:5px;border-radius:50%;flex-shrink:0}
.status-pill.running{background:#1a3a1a;color:var(--green)}.status-pill.running .dot{background:var(--green)}
.status-pill.stopped{background:#2a2a2a;color:var(--text2)}.status-pill.stopped .dot{background:var(--text3)}
.status-pill.error{background:#3a1a1a;color:var(--red)}.status-pill.error .dot{background:var(--red)}
.status-pill.starting{background:#2a2a1a;color:var(--yellow)}.status-pill.starting .dot{background:var(--yellow)}
.status-pill.disabled{background:#1e1e1e;color:var(--text3)}.status-pill.disabled .dot{background:var(--text3)}
.status-pill.unknown{background:#1e1e1e;color:var(--text3)}.status-pill.unknown .dot{background:var(--text3)}
.proc-actions{display:flex;gap:4px}
.proc-btn{background:transparent;border:1px solid var(--border);color:var(--text2);padding:2px 7px;border-radius:3px;cursor:pointer;font-family:inherit;font-size:10px;transition:all .1s}
.proc-btn:hover{border-color:var(--border2);color:var(--text)}
.proc-btn.start:hover{border-color:var(--green);color:var(--green)}
.proc-btn.stop:hover{border-color:var(--red);color:var(--red)}
.proc-btn.restart:hover{border-color:var(--yellow);color:var(--yellow)}
.proc-btn.logs:hover{border-color:var(--accent);color:var(--accent)}
.logs-panel{background:var(--bg);border-top:1px solid var(--border);padding:10px 14px;max-height:200px;overflow-y:auto;font-size:10px;line-height:1.6}
.logs-panel .log-line{color:var(--text2)}
.logs-panel .log-time{color:var(--text3);margin-right:8px}
.logs-panel .no-logs{color:var(--text3);font-style:italic}

/* ── runtime status dots on entity cards ── */
.runtime-dot{width:6px;height:6px;border-radius:50%;flex-shrink:0;display:inline-block}
.runtime-dot.running{background:var(--green)}
.runtime-dot.stopped,.runtime-dot.disabled{background:var(--text3)}
.runtime-dot.error{background:var(--red)}
.runtime-dot.starting{background:var(--yellow)}
.runtime-dot.unresolved{background:var(--orange)}
.runtime-dot.offline{background:var(--text3);opacity:.45}
.runtime-dot.unknown{background:var(--text3);opacity:.35}

/* ── preview MCP status badge ── */
.mcp-status{display:inline-flex;align-items:center;gap:5px;padding:2px 8px;border-radius:10px;font-size:10px;margin-top:8px}
.mcp-status .dot{width:5px;height:5px;border-radius:50%;flex-shrink:0}
.mcp-status.running{background:#1a3a1a;color:var(--green)}.mcp-status.running .dot{background:var(--green)}
.mcp-status.stopped,.mcp-status.disabled{background:#2a2a2a;color:var(--text2)}.mcp-status.stopped .dot,.mcp-status.disabled .dot{background:var(--text3)}
.mcp-status.error{background:#3a1a1a;color:var(--red)}.mcp-status.error .dot{background:var(--red)}
.mcp-status.starting{background:#2a2a1a;color:var(--yellow)}.mcp-status.starting .dot{background:var(--yellow)}
.mcp-status.unresolved{background:#2a1e12;color:var(--orange)}.mcp-status.unresolved .dot{background:var(--orange)}
.mcp-status.offline{background:#1e1e1e;color:var(--text2)}.mcp-status.offline .dot{background:var(--text3)}
.mcp-status.unknown{background:#1e1e1e;color:var(--text3)}.mcp-status.unknown .dot{background:var(--text3);opacity:.4}

/* ── broken-ref indicator in preview ── */
.broken-ref-banner{background:#2a1e12;border:1px solid #5a3a18;border-radius:4px;color:var(--orange);padding:8px 12px;margin-bottom:12px;font-size:11px}

/* ── edit toolbar ── */
.preview-actions{display:flex;justify-content:flex-end;padding:6px 0 2px;gap:6px;align-items:center}
.edit-btn,.save-btn,.cancel-btn{background:transparent;border:1px solid var(--border2);color:var(--text2);padding:2px 9px;border-radius:3px;cursor:pointer;font-family:inherit;font-size:10px;transition:all .1s}
.edit-btn:hover{border-color:var(--accent);color:var(--accent)}
.save-btn:hover{border-color:var(--green);color:var(--green)}
.cancel-btn:hover{border-color:var(--red);color:var(--red)}
.edit-toolbar{gap:6px;align-items:center}
.save-status{font-size:10px;color:var(--text2)}
.raw-src{display:none}

/* ── rendered markdown ── */
.rendered-md{line-height:1.7;color:var(--text);font-size:12px}
.rendered-md h1,.rendered-md h2,.rendered-md h3,.rendered-md h4{color:var(--white);font-weight:600;margin:16px 0 6px;line-height:1.3}
.rendered-md h1{font-size:16px}.rendered-md h2{font-size:14px}.rendered-md h3,.rendered-md h4{font-size:12px}
.rendered-md p{margin:6px 0}
.rendered-md code{background:var(--bg3);padding:1px 5px;border-radius:3px;font-size:11px}
.rendered-md pre{background:var(--bg2);border:1px solid var(--border);border-radius:4px;padding:14px;overflow-x:auto;margin:10px 0}
.rendered-md pre code{background:none;padding:0;font-size:11px}
.rendered-md ul,.rendered-md ol{padding-left:20px;margin:6px 0}
.rendered-md li{margin:2px 0}
.rendered-md strong{color:var(--white)}
.rendered-md a{color:var(--accent)}
.rendered-md blockquote{border-left:3px solid var(--border2);padding-left:12px;color:var(--text2);margin:8px 0}
.rendered-md hr{border:none;border-top:1px solid var(--border);margin:12px 0}
.rendered-md table{border-collapse:collapse;width:100%;margin:10px 0}
.rendered-md th,.rendered-md td{border:1px solid var(--border);padding:4px 10px;font-size:11px}
.rendered-md th{background:var(--bg3);color:var(--white)}

/* ── project overview ── */
#project-overview{border-bottom:1px solid var(--border);background:var(--bg2);flex-shrink:0;user-select:none}
.ov-row{display:flex;align-items:center;gap:8px;padding:5px 16px;font-size:11px}
.ov-name{color:var(--white);font-weight:600;flex-shrink:0}
.ov-scopes{display:flex;gap:3px;flex-shrink:0}
.ov-sep{color:var(--text3);flex-shrink:0}
.ov-counts{display:flex;gap:8px;align-items:center;color:var(--text2)}
.ov-count{display:inline-flex;align-items:center;gap:2px}
.ov-spacer{flex:1}
.ov-refresh{background:none;border:none;color:var(--text3);font-size:15px;cursor:pointer;padding:0;line-height:1;border-radius:3px;transition:color .15s;display:inline-flex;align-items:center;justify-content:center;width:20px;height:20px;flex-shrink:0}
.ov-refresh:hover{color:var(--accent)}
.ov-refresh.spinning{animation:spin .7s linear infinite}
.ov-toggle{color:var(--text2);font-size:13px;flex-shrink:0;transition:transform .2s;cursor:pointer;line-height:1;display:inline-flex;align-items:center}
.ov-toggle.open{transform:rotate(180deg)}
.ov-detail{padding:2px 16px 7px;font-size:10px;color:var(--text3)}
/* ── container controls in overview ── */
.ov-ctr{display:flex;align-items:center;gap:4px;flex-shrink:0}
.ov-ctr-dot{width:7px;height:7px;border-radius:50%;flex-shrink:0}
.ov-ctr-dot.running{background:#3a3}
.ov-ctr-dot.exited,.ov-ctr-dot.stopped{background:var(--text3)}
.ov-ctr-label{font-size:10px;color:var(--text2)}
.ov-ctr-btn{background:var(--bg3);border:1px solid var(--border2);color:var(--text);font-size:10px;padding:1px 7px;border-radius:3px;cursor:pointer;font-family:inherit;text-decoration:none;display:inline-flex;align-items:center}
.ov-ctr-btn:hover{border-color:var(--accent);color:var(--accent)}
.ov-ctr-btn:disabled{opacity:.4;cursor:default}
/* ── container log panel ── */
.ctr-log-wrap{border-top:1px solid var(--border)}
.ctr-log-bar{display:flex;justify-content:space-between;align-items:center;padding:4px 16px;font-size:10px;color:var(--text3)}
.ctr-log-close{background:none;border:none;color:var(--text3);cursor:pointer;font-size:14px;padding:0;line-height:1}
.ctr-log-close:hover{color:var(--text)}
.ctr-log-pre{margin:0 8px 8px;padding:8px;font-size:10px;line-height:1.4;color:var(--text);background:var(--bg);border:1px solid var(--border);border-radius:3px;max-height:180px;overflow-y:auto;white-space:pre-wrap;word-break:break-all}

/* ── CodeMirror container ── */
.editor-wrap{margin-top:4px;border:1px solid var(--border2);border-radius:4px;overflow:hidden;min-height:360px}
.editor-wrap .cm-editor{min-height:360px;font-size:12px}
.editor-wrap .cm-editor.cm-focused{outline:none}
.editor-wrap .cm-scroller{font-family:ui-monospace,monospace!important}
</style>
</head>
<body>

<header>
  <span class="logo"><svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 20" width="18" height="18"><rect width="20" height="20" rx="4" fill="#111"/><text x="50%" y="54%" dominant-baseline="middle" text-anchor="middle" font-size="13" fill="#4a9eff">⬡</text></svg>infra-mngmt</span>

  <div class="view-tabs">
    <button class="view-tab active" id="vtab-config" onclick="showView('config')">config</button>
    <button class="view-tab" id="vtab-services" onclick="showView('services')">services</button>
  </div>

  <div class="divider"></div>

  <div class="tab-scroll-wrap" id="tab-scroll-wrap">
    <button class="tab-scroll-btn" onclick="document.getElementById('source-tabs-bar').scrollBy({left:-140,behavior:'smooth'})">‹</button>
    <div class="source-tabs" id="source-tabs-bar">
      <button class="tab active" data-project="__all__">all</button>
      {{range .Sources}}
      <button class="tab" data-project="{{.Key}}">
        {{.Label}}{{range .Levels}}<span class="scope-badge {{.}}">{{.}}</span>{{end}}
      </button>
      {{end}}
    </div>
    <button class="tab-scroll-btn" onclick="document.getElementById('source-tabs-bar').scrollBy({left:140,behavior:'smooth'})">›</button>
  </div>

  <input id="search" type="text" placeholder="⌘K search" autocomplete="off">
</header>

<div class="kind-bar" id="kind-bar">
  <button class="pill active" data-kind="all">all</button>
  <button class="pill" data-kind="mcp_server"><span class="pill-icon">⬡</span> MCP servers</button>
  <button class="pill" data-kind="command"><span class="pill-icon">/</span> commands</button>
  <button class="pill" data-kind="agent"><span class="pill-icon">◈</span> agents</button>
  <button class="pill" data-kind="skill"><span class="pill-icon">◆</span> skills</button>
  <button class="pill" data-kind="hook"><span class="pill-icon">⚡</span> hooks</button>
  <button class="pill" data-kind="memory"><span class="pill-icon">◎</span> memory</button>
  <button class="pill" data-kind="claude_md"><span class="pill-icon">≡</span> CLAUDE.md</button>
</div>

<div id="project-overview" style="display:none">
  <div class="ov-row" id="ov-row">
    <span class="ov-name" id="ov-name" style="display:none"></span>
    <span class="ov-scopes" id="ov-scopes" style="display:none"></span>
    <span class="ov-sep" id="ov-sep" style="display:none">·</span>
    <span class="ov-counts" id="ov-counts" style="display:none"></span>
    <span class="ov-spacer"></span>
    <div class="ov-ctr" id="ov-ctr"></div>
    <button class="ov-refresh" id="ov-refresh" onclick="doRefresh(event)" title="Refresh entities">↻</button>
    <span class="ov-toggle" id="ov-toggle" style="display:none" onclick="toggleOverview()">▾</span>
  </div>
  <div class="ov-detail" id="ov-detail" style="display:none"></div>
  <div id="ov-log-panel" style="display:none"></div>
</div>

<div id="view-config" style="display:flex;flex:1;overflow:hidden">
  <div class="entity-list" id="entity-list">
    {{range .Entities}}
    {{$status := index $.MCPStatuses .ID}}
    <div class="entity-card"
         data-id="{{.ID}}"
         data-project="{{.Scope.Project}}"
         data-kind="{{.Kind}}"
         data-name="{{.Name}}"
         data-scope="{{.Scope.Label}}">
      <span class="kind-icon {{.Kind}}">{{kindIcon .Kind}}</span>
      <span class="entity-name">{{.Name}}</span>
      {{if $status}}<span class="runtime-dot {{$status.State}}" title="{{$status.State}}{{if $status.Process}} · {{$status.Instance}}/{{$status.Process}}{{end}}"></span>{{end}}
      <span class="entity-scope-dot {{entityLevel .}}" title="{{entityLevel .}}">●</span>
    </div>
    {{end}}
    <div class="empty-list" id="empty-list" style="{{if .Entities}}display:none{{end}}">no entities found</div>
  </div>
  <div class="preview" id="preview">
    <p class="preview-hint">← select an entity to preview</p>
  </div>
</div>

<div id="view-services"
     hx-get="/partials/services"
     hx-trigger="revealed, every 8s"
     hx-swap="innerHTML">
  <p style="color:var(--text3);font-style:italic;padding:20px">loading services…</p>
</div>

<script>
// ── view switching ──
function showView(v) {
  document.getElementById('view-config').style.display   = v === 'config'   ? 'flex' : 'none';
  document.getElementById('view-services').style.display = v === 'services' ? 'flex' : 'none';
  document.getElementById('kind-bar').style.display      = v === 'config'   ? 'flex' : 'none';
  document.getElementById('tab-scroll-wrap').style.display = v === 'config' ? 'flex' : 'none';
  document.getElementById('vtab-config').classList.toggle('active',   v === 'config');
  document.getElementById('vtab-services').classList.toggle('active', v === 'services');
  if (v === 'config') updateProjectOverview(); else document.getElementById('project-overview').style.display = 'none';
}

// ── entity filtering ──
let activeProject = '__all__', activeKind = 'all', fuseResults = null;
const cards = () => [...document.querySelectorAll('.entity-card')];
const fuseData = cards().map(c => ({id:c.dataset.id,name:c.dataset.name,kind:c.dataset.kind}));
const fuse = new Fuse(fuseData, {keys:['name','kind'],threshold:0.35});

function applyFilters() {
  const ids = fuseResults ? new Set(fuseResults.map(r=>r.item.id)) : null;
  let any = false;
  cards().forEach(c => {
    const ok = (activeProject==='__all__'||c.dataset.project===activeProject)
             && (activeKind==='all'||c.dataset.kind===activeKind)
             && (!ids||ids.has(c.dataset.id));
    c.style.display = ok ? '' : 'none';
    if (ok) any = true;
  });
  document.getElementById('empty-list').style.display = any ? 'none' : '';
}

// ── project overview ──
const kindIcons = {mcp_server:'⬡',command:'/',agent:'◈',skill:'◆',memory:'◎',hook:'⚡',claude_md:'≡'};
let ovExpanded = false, selectedEntityProject = null;

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
    const countsEl = document.getElementById('ov-counts');
    countsEl.innerHTML = '';
    ['mcp_server','command','agent','skill','hook','memory','claude_md'].filter(k => counts[k]).forEach(k => {
      const s = document.createElement('span'); s.className = 'ov-count';
      const ic = document.createElement('span'); ic.className = 'kind-icon '+k; ic.textContent = kindIcons[k]||'·';
      s.appendChild(ic); s.appendChild(document.createTextNode(' '+counts[k]));
      countsEl.appendChild(s);
    });
    document.getElementById('ov-detail').textContent = displayProject;
    document.getElementById('ov-detail').style.display = ovExpanded ? '' : 'none';
    document.getElementById('ov-toggle').className = 'ov-toggle' + (ovExpanded ? ' open' : '');
  } else {
    document.getElementById('ov-detail').style.display = 'none';
  }

  ov.style.display = 'block';
  renderContainerControls(displayProject);
}

function toggleOverview() {
  const displayProject = selectedEntityProject || activeProject;
  if (!displayProject || displayProject === '__all__') return;
  ovExpanded = !ovExpanded;
  document.getElementById('ov-detail').style.display = ovExpanded ? '' : 'none';
  document.getElementById('ov-toggle').className = 'ov-toggle' + (ovExpanded ? ' open' : '');
}

async function doRefresh(e) {
  e.stopPropagation();
  const btn = document.getElementById('ov-refresh');
  btn.classList.add('spinning');
  btn.disabled = true;
  try {
    await fetch('/api/refresh', {method:'POST'});
    window.location.reload();
  } catch(_) {
    btn.classList.remove('spinning');
    btn.disabled = false;
  }
}

document.querySelectorAll('.tab').forEach(t => t.addEventListener('click', () => {
  document.querySelectorAll('.tab').forEach(x=>x.classList.remove('active'));
  t.classList.add('active'); activeProject = t.dataset.project; selectedEntityProject = null; applyFilters(); updateProjectOverview();
}));
document.querySelectorAll('.pill').forEach(p => p.addEventListener('click', () => {
  document.querySelectorAll('.pill').forEach(x=>x.classList.remove('active'));
  p.classList.add('active'); activeKind = p.dataset.kind; applyFilters();
}));

const search = document.getElementById('search');
search.addEventListener('input', () => {
  const q = search.value.trim();
  fuseResults = q ? fuse.search(q) : null;
  applyFilters();
});
document.addEventListener('keydown', e => {
  if ((e.metaKey||e.ctrlKey) && e.key==='k') { e.preventDefault(); search.focus(); search.select(); }
  if (e.key==='Escape' && document.activeElement===search) { search.value=''; fuseResults=null; applyFilters(); search.blur(); }
});

// ── entity preview ──
document.getElementById('entity-list').addEventListener('click', e => {
  const card = e.target.closest('.entity-card');
  if (!card) return;
  document.querySelectorAll('.entity-card.selected').forEach(c=>c.classList.remove('selected'));
  card.classList.add('selected');
  selectedEntityProject = card.dataset.project || null;
  updateProjectOverview();
  document.getElementById('preview').innerHTML = '<div class="preview-loading"><span class="spinner"></span>loading…</div>';
  htmx.ajax('GET', '/partials/entity?id='+encodeURIComponent(card.dataset.id), {target:'#preview',swap:'innerHTML'});
});

// ── markdown + editor ──
let _cmResolve;
const cmReady = new Promise(r => { _cmResolve = r; });
window._cmResolve = _cmResolve;
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
  await cmReady;
  const body = btn.closest('.preview-body');
  const raw  = body.querySelector('.raw-src').textContent;
  body.querySelector('.rendered-md').style.display = 'none';
  btn.style.display = 'none';
  const wrap = body.querySelector('.editor-wrap');
  wrap.style.display = 'block';
  wrap.innerHTML = '';
  const {EditorView, EditorState, basicSetup, markdown} = window._cm;
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
// ── container controls ──
let containers = [];
let _logEventSource = null;
let _logContainerId = null;

async function fetchContainers() {
  try {
    const r = await fetch('/api/containers');
    if (r.ok) containers = await r.json();
  } catch(_) {}
}

function isContainerReady(ctr) {
  if (ctr.state !== 'running') return false;
  if (_logContainerId === ctr.id) {
    // During active startup: wait for health (null = unknown, '' = no check)
    if (ctr.health === null || ctr.health === undefined) return false;
    return ctr.health === '' || ctr.health === 'healthy';
  }
  return true;
}

function renderContainerControls(displayProject) {
  const el = document.getElementById('ov-ctr');
  if (!el) return;
  const ctr = containers.find(c => c.projectRoot === displayProject);
  if (!ctr) { el.innerHTML = ''; return; }
  const running = ctr.state === 'running';
  const starting = _logContainerId === ctr.id && !running;
  const stateClass = running ? 'running' : (ctr.state || 'exited');
  const stateLabel = starting ? 'starting…'
    : (ctr.health && ctr.health !== 'healthy' && ctr.health !== '' ? ctr.state+' ('+ctr.health+')' : ctr.state);
  const ready = isContainerReady(ctr);
  const actLabel = running ? '\u25a0 stop' : '\u25b6 start';
  const actFn   = running ? 'stop' : 'start';
  const vsHref  = ready ? 'href="vscode://ms-vscode-remote.remote-containers/openFolder?containerId=' + ctr.shortId + '"' : '';
  const vsCls   = 'ov-ctr-btn' + (ready ? '' : ' disabled');
  const vsTitle = ready ? 'Open in VS Code' : 'waiting for container to be ready';
  const vsBtn   = running ? '<a class="' + vsCls + '" ' + vsHref + ' title="' + vsTitle + '">VS Code</a>' : '';
  el.innerHTML =
    '<span class="ov-ctr-dot ' + stateClass + '"></span>' +
    '<span class="ov-ctr-label">' + stateLabel + '</span>' +
    '<button class="ov-ctr-btn" data-cid="' + ctr.id + '" data-act="' + actFn + '" onclick="containerAction(this.dataset.cid,this.dataset.act)">' + actLabel + '</button>' +
    vsBtn;
}

async function containerAction(id, action) {
  if (action === 'stop') {
    closeLogStream();
    const btns = document.querySelectorAll('#ov-ctr .ov-ctr-btn');
    btns.forEach(b => { if (b.tagName==='BUTTON') { b.disabled=true; b.textContent='stopping…'; } });
    try { await fetch('/api/container/stop?id='+encodeURIComponent(id), {method:'POST'}); } catch(_) {}
    await fetchContainers();
    const dp = selectedEntityProject || activeProject;
    renderContainerControls(dp);
  } else {
    const ctr = containers.find(c => c.id === id);
    if (ctr) ctr.health = null; // unknown until first state event
    try { await fetch('/api/container/start?id='+encodeURIComponent(id), {method:'POST'}); } catch(_) {}
    openLogStream(id);
  }
}

function openLogStream(id) {
  if (_logEventSource) _logEventSource.close();
  _logContainerId = id;
  const panel = document.getElementById('ov-log-panel');
  panel.innerHTML = '<div class="ctr-log-wrap"><div class="ctr-log-bar">startup log<button class="ctr-log-close" onclick="closeLogStream()">×</button></div><pre class="ctr-log-pre" id="ctr-log-pre"></pre></div>';
  panel.style.display = '';
  const dp = selectedEntityProject || activeProject;
  renderContainerControls(dp);

  _logEventSource = new EventSource('/api/container/logs-stream?id='+encodeURIComponent(id));
  _logEventSource.addEventListener('log', e => {
    const pre = document.getElementById('ctr-log-pre');
    if (pre) { pre.textContent += e.data + '\n'; pre.scrollTop = pre.scrollHeight; }
  });
  _logEventSource.addEventListener('state', e => {
    try {
      const s = JSON.parse(e.data);
      const ctr = containers.find(c => c.id === id);
      if (ctr) { ctr.state = s.Status; ctr.health = s.Health ? s.Health.Status : ''; }
      renderContainerControls(selectedEntityProject || activeProject);
    } catch(_) {}
  });
  function onStreamEnd() {
    _logEventSource && _logEventSource.close();
    _logEventSource = null; _logContainerId = null;
    fetchContainers().then(() => renderContainerControls(selectedEntityProject || activeProject));
  }
  _logEventSource.addEventListener('done', onStreamEnd);
  _logEventSource.onerror = onStreamEnd;
}

function closeLogStream() {
  if (_logEventSource) { _logEventSource.close(); _logEventSource = null; }
  _logContainerId = null;
  const panel = document.getElementById('ov-log-panel');
  if (panel) { panel.style.display = 'none'; panel.innerHTML = ''; }
  renderContainerControls(selectedEntityProject || activeProject);
}

updateProjectOverview();
fetchContainers().then(() => {
  const displayProject = selectedEntityProject || activeProject;
  renderContainerControls(displayProject);
});
</script>
</body>
</html>
`

const previewHTML = `
{{$e := .Entity}}
{{$c := .Content}}
{{$s := .MCPStatus}}
{{if $s}}
  {{if eq $s.State "unresolved"}}
  <div class="broken-ref-banner">⚠ no matching process-compose process found — this MCP server may have no backing service running</div>
  {{end}}
  {{if eq $s.State "stopped"}}
  <div class="broken-ref-banner" style="background:#2a1212;border-color:#5a1818;color:var(--red)">⚠ backing process <strong>{{$s.Instance}}/{{$s.Process}}</strong> is not running</div>
  {{end}}
  {{if eq $s.State "error"}}
  <div class="broken-ref-banner" style="background:#2a1212;border-color:#5a1818;color:var(--red)">✖ backing process <strong>{{$s.Instance}}/{{$s.Process}}</strong> is in error state</div>
  {{end}}
{{end}}
<div class="preview-header">
  <div class="preview-name">{{$e.Name}}</div>
  <div class="preview-meta">
    <span>kind: {{$e.Kind}}</span>
    <span>level: {{entityLevel $e}}</span>
    <span>source: {{$e.Source}}</span>
  </div>
  {{if $s}}
  <div style="margin-top:8px">
    <span class="mcp-status {{$s.State}}">
      <span class="dot"></span>{{$s.State}}{{if $s.Process}} &nbsp;·&nbsp; {{$s.Instance}}/{{$s.Process}}{{end}}
    </span>
  </div>
  {{end}}
  <div class="preview-path">{{$e.Path}}</div>
</div>
{{if $c}}
<div class="preview-body" data-entity-id="{{$e.ID}}">
  <div class="preview-actions">
    <button class="edit-btn" onclick="startEdit(this)">edit</button>
    <div class="edit-toolbar" style="display:none">
      <button class="save-btn" onclick="saveEdit(this)">save</button>
      <button class="cancel-btn" onclick="cancelEdit(this)">cancel</button>
      <span class="save-status"></span>
    </div>
  </div>
  <div class="raw-src">{{$c}}</div>
  <div class="rendered-md"></div>
  <div class="editor-wrap" style="display:none"></div>
</div>
{{else}}
<div class="preview-no-content">content not available for this entity type</div>
{{end}}
`

// servicesHTML is the HTMX partial for the services view (auto-refreshed every 8s).
const servicesHTML = `
<div class="svc-grid"
     id="services-inner"
     hx-get="/partials/services"
     hx-trigger="every 8s"
     hx-swap="outerHTML">
{{if not .}}
  <p style="color:var(--text3);font-style:italic">no process-compose instances configured — add them to config.json</p>
{{else}}
{{range $iv := .}}
<div class="svc-instance">
  <div class="svc-header">
    <span class="online-dot {{if $iv.Online}}online{{else}}offline{{end}}"></span>
    <span class="svc-name">{{$iv.Name}}</span>
    <span class="svc-endpoint">{{$iv.Endpoint}}</span>
    {{if and (not $iv.Online) $iv.CanBoot}}
    <button class="svc-boot-btn"
            hx-post="/compose/start?instance={{$iv.Name}}"
            hx-target="#services-inner"
            hx-swap="outerHTML">▶ start process-compose</button>
    {{end}}
  </div>
  {{if $iv.Online}}
  <table class="process-table">
    <thead><tr>
      <th>process</th><th>status</th><th>pid</th><th>restarts</th><th>cpu</th><th>mem</th><th>age</th><th></th>
    </tr></thead>
    <tbody>
    {{range $iv.Processes}}
    <tr>
      <td>{{.Name}}</td>
      <td><span class="status-pill {{statusClass .Status}}"><span class="dot"></span>{{.Status}}</span></td>
      <td>{{if .Pid}}{{.Pid}}{{else}}—{{end}}</td>
      <td>{{.Restarts}}</td>
      <td>{{printf "%.1f" .CPU}}%</td>
      <td>{{formatMem .Mem}}</td>
      <td>{{formatAge .Age}}</td>
      <td>
        <div class="proc-actions">
          {{if .IsRunning}}
          <button class="proc-btn stop"
                  hx-post="/process/stop?instance={{$iv.Name}}&process={{.Name}}"
                  hx-target="#services-inner" hx-swap="outerHTML">stop</button>
          <button class="proc-btn restart"
                  hx-post="/process/restart?instance={{$iv.Name}}&process={{.Name}}"
                  hx-target="#services-inner" hx-swap="outerHTML">restart</button>
          {{else}}
          <button class="proc-btn start"
                  hx-post="/process/start?instance={{$iv.Name}}&process={{.Name}}"
                  hx-target="#services-inner" hx-swap="outerHTML">start</button>
          {{end}}
          <button class="proc-btn logs"
                  hx-get="/partials/logs?instance={{$iv.Name}}&process={{.Name}}"
                  hx-target="#logs-{{$iv.Name}}-{{.Name}}"
                  hx-swap="innerHTML">logs</button>
        </div>
        <div id="logs-{{$iv.Name}}-{{.Name}}"></div>
      </td>
    </tr>
    {{end}}
    </tbody>
  </table>
  {{else}}
  <div class="svc-offline">
    offline — endpoint unreachable ({{$iv.Endpoint}})
    {{if not $iv.CanBoot}}· no binary configured for bootstrap{{end}}
  </div>
  {{end}}
</div>
{{end}}
{{end}}
</div>
`

const logsHTML = `
<div class="logs-panel">
  {{if .Err}}<div class="no-logs">error: {{.Err}}</div>
  {{else if not .Lines}}<div class="no-logs">no logs</div>
  {{else}}
  {{range .Lines}}
  <div class="log-line">{{if .Time}}<span class="log-time">{{.Time}}</span>{{end}}{{.Message}}</div>
  {{end}}
  {{end}}
</div>
`

const loginHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>infra-mngmt · login</title>
<link rel="icon" href="data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 32 32'><rect width='32' height='32' rx='6' fill='%230d0d0d'/><text x='50%25' y='54%25' dominant-baseline='middle' text-anchor='middle' font-size='20' fill='%234a9eff'>⬡</text></svg>">
<style>
*,*::before,*::after{box-sizing:border-box;margin:0;padding:0}
:root{--bg:#0d0d0d;--bg2:#111;--bg3:#181818;--border:#222;--border2:#2a2a2a;--text:#c9c9c9;--text2:#888;--text3:#444;--white:#f0f0f0;--accent:#4a9eff;--red:#e06c6c}
html,body{height:100%;background:var(--bg);color:var(--text);font-family:ui-monospace,monospace;font-size:12px;display:flex;align-items:center;justify-content:center}
.card{background:var(--bg2);border:1px solid var(--border2);border-radius:6px;padding:32px 36px;width:320px}
.logo{color:var(--white);font-size:14px;font-weight:600;letter-spacing:.04em;margin-bottom:24px}
.logo span{color:var(--text3);font-weight:normal}
label{display:block;color:var(--text2);margin-bottom:6px;font-size:11px}
input[type=password]{width:100%;background:var(--bg3);border:1px solid var(--border2);color:var(--text);padding:7px 10px;border-radius:4px;font-family:inherit;font-size:12px;outline:none;margin-bottom:16px}
input[type=password]:focus{border-color:#444}
button{width:100%;background:var(--accent);border:none;color:#000;padding:8px;border-radius:4px;font-family:inherit;font-size:12px;font-weight:600;cursor:pointer}
button:hover{opacity:.88}
.error{background:#2a1212;border:1px solid #5a1818;color:var(--red);padding:8px 10px;border-radius:4px;margin-bottom:14px;font-size:11px}
</style>
</head>
<body>
<form class="card" method="POST" action="/login">
  <div class="logo">infra-mngmt <span>/ login</span></div>
  {{if .Error}}<div class="error">{{.Error}}</div>{{end}}
  <label for="token">bearer token</label>
  <input id="token" type="password" name="token" autofocus placeholder="paste your token">
  <input type="hidden" name="next" value="{{.Next}}">
  <button type="submit">sign in</button>
</form>
</body>
</html>
`
