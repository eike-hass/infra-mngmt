package web

const indexHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>infra-mngmt</title>
<link rel="icon" type="image/svg+xml" href="/favicon.svg">
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
  --bg:#0c0c0c;--bg2:#141414;--bg3:#1c1c1c;--bg4:#222;
  --border:#252525;--border2:#2e2e2e;
  --text:#c9c9c9;--text2:#888;--text3:#444;--white:#f0f0f0;
  --accent:oklch(68% 0.18 200);--accent2:oklch(68% 0.18 302);
  --green:#6bcf7f;--red:#e06c6c;--yellow:#ffcc5c;--orange:#f0a04a;
  --kind-mcp:oklch(68% 0.18 200);--kind-command:oklch(68% 0.18 251);
  --kind-agent:oklch(68% 0.18 302);--kind-skill:oklch(68% 0.18 353);
  --kind-hook:oklch(68% 0.18 44);--kind-memory:oklch(68% 0.18 95);
  --kind-claude_md:oklch(68% 0.18 146);
  --level-global:oklch(68% 0.18 200);--level-project:oklch(68% 0.18 302);--level-devcontainer:oklch(68% 0.18 146);
  --font:ui-monospace,'Cascadia Code','SF Mono',monospace;
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
.tab .scope-badge.global{background:color-mix(in srgb,var(--level-global) 12%,transparent);color:var(--level-global)}
.tab .scope-badge.project{background:color-mix(in srgb,var(--level-project) 12%,transparent);color:var(--level-project)}
.tab .scope-badge.devcontainer{background:color-mix(in srgb,var(--level-devcontainer) 12%,transparent);color:var(--level-devcontainer)}
#search{background:var(--bg3);border:1px solid var(--border2);color:var(--text);padding:4px 8px;border-radius:4px;font-family:inherit;font-size:11px;width:180px;outline:none;flex-shrink:0}
#search:focus{border-color:#444}
#search::placeholder{color:var(--text3)}

/* ── kind filter bar ── */
.kind-bar{display:flex;gap:4px;padding:8px 16px;border-bottom:1px solid var(--border);flex-shrink:0}
.pill{background:transparent;border:1px solid var(--border);color:var(--text2);padding:2px 8px;border-radius:12px;cursor:pointer;font-family:inherit;font-size:10px;transition:all .12s}
.pill:hover{border-color:var(--border2);color:var(--text)}
.pill.active{color:var(--white)}
.pill[data-kind="mcp_server"].active{background:color-mix(in srgb,var(--kind-mcp) 12%,transparent)}
.pill[data-kind="command"].active{background:color-mix(in srgb,var(--kind-command) 12%,transparent)}
.pill[data-kind="agent"].active{background:color-mix(in srgb,var(--kind-agent) 12%,transparent)}
.pill[data-kind="skill"].active{background:color-mix(in srgb,var(--kind-skill) 12%,transparent)}
.pill[data-kind="memory"].active{background:color-mix(in srgb,var(--kind-memory) 12%,transparent)}
.pill[data-kind="hook"].active{background:color-mix(in srgb,var(--kind-hook) 12%,transparent)}
.pill[data-kind="claude_md"].active{background:color-mix(in srgb,var(--kind-claude_md) 12%,transparent)}
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
.entity-card{display:flex;align-items:center;gap:8px;padding:5px 16px;cursor:pointer;border-left:2px solid transparent;transition:background .1s}
.entity-card:hover{background:var(--bg2)}
.entity-card.selected{background:var(--bg3);border-left-color:var(--accent)}
.kind-icon{font-size:12px;flex-shrink:0;width:12px;text-align:center}
.kind-icon.mcp_server{color:var(--kind-mcp)}.kind-icon.command{color:var(--kind-command)}
.kind-icon.agent{color:var(--kind-agent)}.kind-icon.skill{color:var(--kind-skill)}
.kind-icon.memory{color:var(--kind-memory)}.kind-icon.hook{color:var(--kind-hook)}
.kind-icon.claude_md{color:var(--kind-claude_md)}
.entity-name{flex:1;color:var(--text);overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.entity-scope-tag{font-size:9px;flex-shrink:0;padding:0 4px;border-radius:3px;letter-spacing:.02em;line-height:16px}
.entity-scope-tag.global{background:color-mix(in srgb,var(--level-global) 12%,transparent);color:var(--level-global)}
.entity-scope-tag.project{background:color-mix(in srgb,var(--level-project) 12%,transparent);color:var(--level-project)}
.entity-scope-tag.devcontainer{background:color-mix(in srgb,var(--level-devcontainer) 12%,transparent);color:var(--level-devcontainer)}
.empty-list{color:var(--text3);padding:24px 16px;font-style:italic}
/* ── kind groups in entity list ── */
.kind-group-header{display:flex;align-items:center;gap:6px;padding:8px 16px 4px;cursor:pointer;user-select:none}
.kind-group-label{font-size:10px;color:var(--text3);text-transform:uppercase;letter-spacing:.08em;font-weight:600;flex:1}
.kind-group-count{font-size:10px;color:var(--text3);background:var(--bg3);padding:0 5px;border-radius:8px;min-width:18px;text-align:center}
.kind-group-chev{font-size:9px;color:var(--text3);transition:transform .15s;line-height:1;margin-left:2px}
.kind-group.collapsed .kind-group-chev{transform:rotate(-90deg)}
.kind-group.collapsed .kind-group-items{display:none}
/* When a specific kind filter is active, hide group headers and rely on per-kind filtering. */
#entity-list.flat .kind-group-header{display:none}

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
#view-services{flex:1;overflow-y:auto;padding:16px 24px;display:none;flex-direction:column}
.svc-grid{display:flex;flex-direction:column;gap:20px;max-width:1400px;width:100%;margin:0 auto}
.svc-instance{background:var(--bg2);border:1px solid var(--border);border-radius:6px;overflow:hidden}
.svc-header{display:flex;align-items:center;gap:10px;padding:10px 14px;border-bottom:1px solid var(--border);background:var(--bg3)}
.svc-name{color:var(--white);font-weight:600;font-size:13px}
.svc-endpoint{color:var(--text3);font-size:10px}
.online-dot{width:7px;height:7px;border-radius:50%;flex-shrink:0}
.online-dot.online{background:var(--green);box-shadow:0 0 5px var(--green)}.online-dot.offline{background:var(--red)}
.svc-running-count{margin-left:auto;font-size:10px;color:var(--text3)}
.svc-boot-btn{margin-left:auto;background:rgba(74,158,255,.1);border:1px solid rgba(74,158,255,.3);color:var(--accent);padding:4px 12px;border-radius:4px;cursor:pointer;font-family:inherit;font-size:11px;font-weight:600}
.svc-boot-btn:hover{background:rgba(74,158,255,.18)}
.svc-offline{padding:32px 24px;display:flex;flex-direction:column;align-items:center;gap:12px;text-align:center}
.svc-offline-icon{font-size:28px;opacity:.2}
.svc-offline-title{color:var(--text2);font-size:12px}
.svc-offline-endpoint{color:var(--text3);font-size:10px}
.svc-offline-hint{color:var(--text3);font-size:10px;margin-top:2px}
/* CPU/mem bars in services table */
.usage-bar{display:flex;align-items:center;gap:5px}
.usage-bar-track{width:36px;height:4px;border-radius:2px;background:var(--bg4);overflow:hidden;flex-shrink:0}
.usage-bar-fill{height:100%;transition:width .3s}
.usage-bar-fill.cpu-low{background:var(--green)}
.usage-bar-fill.cpu-mid{background:var(--yellow)}
.usage-bar-fill.cpu-high{background:var(--orange)}
.usage-bar-fill.mem{background:var(--accent);opacity:.6}
.usage-val{color:var(--text2);font-size:11px}
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
.health-pill{display:inline-block;margin-left:4px;padding:1px 6px;border-radius:10px;font-size:9px;letter-spacing:.02em;border:1px solid transparent;vertical-align:middle}
.health-pill.ready{background:#15311a;color:var(--green);border-color:#1a3a1a}
.health-pill.not-ready{background:#3a1a1a;color:var(--red);border-color:#4a1818}
.health-pill.unknown{background:#1e1e1e;color:var(--text3);border-color:var(--border)}
.exit-code{display:inline-block;margin-left:4px;padding:1px 6px;border-radius:10px;font-size:9px;background:#3a1a1a;color:var(--red);vertical-align:middle}
.proc-name{color:var(--text)}
.proc-ns{color:var(--text3);font-size:9px;letter-spacing:.04em;text-transform:uppercase;margin-top:1px}
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
/* MCP runtime badge — 14×14 tinted square with a 5px dot inside */
.runtime-badge{display:inline-flex;align-items:center;justify-content:center;width:14px;height:14px;border-radius:3px;flex-shrink:0;border:1px solid var(--border2);background:rgba(255,255,255,.04)}
.runtime-badge::before{content:"";width:5px;height:5px;border-radius:50%;background:var(--text3);display:block}
.runtime-badge.running{background:color-mix(in srgb,var(--kind-claude_md) 15%,transparent);border-color:var(--kind-claude_md)}
.runtime-badge.running::before{background:var(--kind-claude_md)}
.runtime-badge.error{background:color-mix(in srgb,var(--red) 12%,transparent);border-color:var(--red)}
.runtime-badge.error::before{background:var(--red)}
.runtime-badge.starting{background:color-mix(in srgb,var(--yellow) 12%,transparent);border-color:var(--yellow)}
.runtime-badge.starting::before{background:var(--yellow)}
.runtime-badge.unresolved{background:color-mix(in srgb,var(--orange) 12%,transparent);border-color:var(--orange)}
.runtime-badge.unresolved::before{background:var(--orange)}
.runtime-badge.offline,.runtime-badge.unknown{opacity:.5}

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
/* ── preview header colored meta ── */
.meta-kind.mcp_server{color:var(--kind-mcp)}.meta-kind.command{color:var(--kind-command)}
.meta-kind.agent{color:var(--kind-agent)}.meta-kind.skill{color:var(--kind-skill)}
.meta-kind.memory{color:var(--kind-memory)}.meta-kind.hook{color:var(--kind-hook)}
.meta-kind.claude_md{color:var(--kind-claude_md)}
.meta-level{padding:1px 5px;border-radius:3px}
.meta-level.global{background:color-mix(in srgb,var(--level-global) 10%,transparent);color:var(--level-global)}
.meta-level.project{background:color-mix(in srgb,var(--level-project) 10%,transparent);color:var(--level-project)}
.meta-level.devcontainer{background:color-mix(in srgb,var(--level-devcontainer) 10%,transparent);color:var(--level-devcontainer)}
.mcp-attrs{margin-top:8px;display:flex;flex-wrap:wrap;gap:4px 12px;font-size:10px;color:var(--text2)}
.mcp-attrs-k{color:var(--text3)}
.mcp-attrs-v{color:var(--text)}
/* ── MCP structured card (when no markdown content) ── */
.mcp-card{margin-top:4px}
.mcp-transport-row{display:flex;align-items:center;gap:8px;margin-bottom:14px}
.mcp-transport{font-size:10px;padding:2px 8px;border-radius:10px;background:var(--bg3);border:1px solid var(--border2);color:var(--kind-mcp)}
.mcp-transport.sse,.mcp-transport.http{color:var(--accent2)}
.mcp-table{background:var(--bg2);border:1px solid var(--border);border-radius:4px;overflow:hidden;margin-bottom:14px}
.mcp-row{display:flex;border-bottom:1px solid var(--border)}
.mcp-row:last-child{border-bottom:none}
.mcp-row-k{width:70px;flex-shrink:0;padding:7px 12px;font-size:10px;color:var(--text3);background:var(--bg3);border-right:1px solid var(--border);letter-spacing:.03em}
.mcp-row-v{flex:1;padding:7px 12px;font-size:11px;color:var(--text);font-family:var(--font);word-break:break-all;line-height:1.5}
.mcp-detail{display:flex;flex-direction:column;gap:5px}
.mcp-detail-row{display:flex;gap:8px;font-size:10px}
.mcp-detail-k{color:var(--text3);width:50px;flex-shrink:0}
.mcp-detail-v{color:var(--text2);word-break:break-all}

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
.ctr-event-list{margin:0 8px 8px;padding:6px 8px;font-size:10px;background:var(--bg);border:1px solid var(--border);border-radius:3px;max-height:180px;overflow-y:auto;display:flex;flex-direction:column;gap:2px}
.ctr-event{display:flex;align-items:center;gap:8px;line-height:1.5}
.ctr-event-time{color:var(--text3);flex-shrink:0;font-variant-numeric:tabular-nums}
.ctr-event-glyph{flex-shrink:0;width:10px;text-align:center}
.ctr-event-action{color:var(--text)}
.ctr-event.event-ok .ctr-event-glyph{color:var(--green)}
.ctr-event.event-err .ctr-event-glyph{color:var(--red)}
.ctr-event.event-warn .ctr-event-glyph{color:var(--yellow)}
.ctr-event.event-info .ctr-event-glyph{color:var(--text2)}

/* ── toast notifications ── */
.toast-stack{position:fixed;bottom:16px;right:16px;display:flex;flex-direction:column-reverse;gap:8px;z-index:9999;max-width:480px;pointer-events:none}
.toast{background:var(--bg2);border:1px solid var(--red);border-left:3px solid var(--red);color:var(--text);padding:10px 36px 10px 12px;border-radius:4px;font-size:11px;line-height:1.5;box-shadow:0 6px 20px rgba(0,0,0,.4);position:relative;pointer-events:auto;opacity:0;transform:translateY(8px);transition:opacity .18s,transform .18s;word-break:break-word;max-width:100%}
.toast.show{opacity:1;transform:translateY(0)}
.toast.info{border-color:var(--accent);border-left-color:var(--accent)}
.toast .toast-title{color:var(--white);font-weight:600;margin-bottom:2px;font-size:11px}
.toast .toast-body{color:var(--text2);white-space:pre-wrap}
.toast .toast-close{position:absolute;top:6px;right:8px;background:none;border:none;color:var(--text3);cursor:pointer;font-size:16px;line-height:1;padding:0;font-family:inherit}
.toast .toast-close:hover{color:var(--text)}

/* ── CodeMirror container ── */
.editor-wrap{margin-top:4px;border:1px solid var(--border2);border-radius:4px;overflow:hidden;min-height:360px}
.editor-wrap .cm-editor{min-height:360px;font-size:12px}
.editor-wrap .cm-editor.cm-focused{outline:none}
.editor-wrap .cm-scroller{font-family:ui-monospace,monospace!important}
</style>
</head>
<body>

<div id="toast-stack" class="toast-stack" aria-live="polite"></div>

<header>
  <span class="logo"><svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 28 20" width="28" height="20"><polygon points="8,2 14,2 17,7 14,12 8,12 5,7" fill="var(--accent)" opacity="0.95"/><polygon points="13,8 19,8 22,13 19,18 13,18 10,13" fill="none" stroke="var(--accent)" stroke-width="1.2" opacity="0.55"/></svg>infra-mngmt</span>

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
  <button class="pill" data-kind="command"><span class="pill-icon">$</span> commands</button>
  <button class="pill" data-kind="agent"><span class="pill-icon">◉</span> agents</button>
  <button class="pill" data-kind="skill"><span class="pill-icon">✦</span> skills</button>
  <button class="pill" data-kind="hook"><span class="pill-icon">↪</span> hooks</button>
  <button class="pill" data-kind="memory"><span class="pill-icon">▤</span> memory</button>
  <button class="pill" data-kind="claude_md"><span class="pill-icon">#</span> CLAUDE.md</button>
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
    {{template "entity-list-inner" .}}
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
let fuse;
function rebuildFuse() {
  const fuseData = cards().map(c => ({id:c.dataset.id,name:c.dataset.name,kind:c.dataset.kind}));
  fuse = new Fuse(fuseData, {keys:['name','kind'],threshold:0.35});
}
rebuildFuse();

function applyFilters() {
  const ids = fuseResults ? new Set(fuseResults.map(r=>r.item.id)) : null;
  const list = document.getElementById('entity-list');
  // 'flat' class hides group headers when a single-kind filter is active.
  list.classList.toggle('flat', activeKind !== 'all');

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

function toggleKindGroup(headerEl) {
  headerEl.parentElement.classList.toggle('collapsed');
}

// ── project overview ──
const kindIcons = {mcp_server:'⬡',command:'$',agent:'◉',skill:'✦',memory:'▤',hook:'↪',claude_md:'#'};
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
  // Remember which entity was selected so we can re-mark it after the swap.
  const prevSelectedId = document.querySelector('.entity-card.selected')?.dataset.id || null;
  try {
    await fetch('/api/refresh', {method:'POST'});
    const html = await fetch('/partials/entity-list').then(r => r.text());
    document.getElementById('entity-list').innerHTML = html;
    rebuildFuse();
    if (prevSelectedId) {
      const card = document.querySelector('.entity-card[data-id="'+prevSelectedId.replace(/"/g,'\\"')+'"]');
      if (card) card.classList.add('selected');
    }
    await fetchContainers();
    const dp = selectedEntityProject || activeProject;
    renderContainerControls(dp);
    applyFilters();
    updateProjectOverview();
  } finally {
    btn.classList.remove('spinning');
    btn.disabled = false;
  }
}

document.querySelectorAll('.tab').forEach(t => t.addEventListener('click', () => {
  document.querySelectorAll('.tab').forEach(x=>x.classList.remove('active'));
  t.classList.add('active'); activeProject = t.dataset.project; selectedEntityProject = null; closeLogStream(); applyFilters(); updateProjectOverview();
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

// ── toast notifications ─────────────────────────────────────────
// HTMX swallows 4xx/5xx by default (no swap), so without this the user sees
// nothing on failure. Surface server-side errors as bottom-right toasts.
function showToast({title, body, kind='error', timeout=8000}) {
  const stack = document.getElementById('toast-stack');
  if (!stack) return;
  const t = document.createElement('div');
  t.className = 'toast' + (kind === 'info' ? ' info' : '');
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

document.body.addEventListener('htmx:responseError', e => {
  const xhr = e.detail.xhr;
  const verb = (e.detail.requestConfig && e.detail.requestConfig.verb || '').toUpperCase();
  const path = (e.detail.requestConfig && e.detail.requestConfig.path) || '';
  const text = (xhr.responseText || xhr.statusText || 'request failed').trim();
  // Trim absurdly long bodies; full detail is in the server journal.
  const body = text.length > 400 ? text.slice(0, 400) + '…' : text;
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
  panel.innerHTML = '<div class="ctr-log-wrap"><div class="ctr-log-bar">container events<button class="ctr-log-close" onclick="closeLogStream()">×</button></div><div class="ctr-event-list" id="ctr-event-list"></div></div>';
  panel.style.display = '';
  const dp = selectedEntityProject || activeProject;
  renderContainerControls(dp);

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

// entityListInnerHTML is a Go template fragment that defines the inner content
// of #entity-list. Used by indexHTML and exposed as a separate partial endpoint
// so the refresh button can swap the entity list without a full page reload.
const entityListInnerHTML = `{{define "entity-list-inner"}}
{{range $group := groupEntitiesByKind .Entities}}
<div class="kind-group" data-kind="{{$group.Kind}}">
  <div class="kind-group-header" onclick="toggleKindGroup(this)">
    <span class="kind-icon {{$group.Kind}}">{{kindIcon $group.Kind}}</span>
    <span class="kind-group-label">{{$group.Label}}</span>
    <span class="kind-group-count">{{len $group.Entities}}</span>
    <span class="kind-group-chev">▾</span>
  </div>
  <div class="kind-group-items">
    {{range $group.Entities}}
    {{$status := index $.MCPStatuses .ID}}
    <div class="entity-card"
         data-id="{{.ID}}"
         data-project="{{.Scope.Project}}"
         data-kind="{{.Kind}}"
         data-name="{{.Name}}"
         data-scope="{{.Scope.Label}}">
      <span class="kind-icon {{.Kind}}">{{kindIcon .Kind}}</span>
      <span class="entity-name">{{.Name}}</span>
      {{if $status}}<span class="runtime-badge {{$status.State}}" title="{{$status.State}}{{if $status.Process}} · {{$status.Instance}}/{{$status.Process}}{{end}}"></span>{{end}}
      <span class="entity-scope-tag {{entityLevel .}}" title="{{entityLevel .}}">{{entityLevelShort .}}</span>
    </div>
    {{end}}
  </div>
</div>
{{end}}
<div class="empty-list" id="empty-list" style="{{if .Entities}}display:none{{end}}">no entities found</div>
{{end}}`

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
    <span>kind: <span class="meta-kind {{$e.Kind}}">{{$e.Kind}}</span></span>
    <span>level: <span class="meta-level {{entityLevel $e}}">{{entityLevel $e}}</span></span>
    <span>source: <span style="color:var(--text)">{{$e.Source}}</span></span>
  </div>
  {{if $s}}
  <div style="margin-top:8px">
    <span class="mcp-status {{$s.State}}">
      <span class="dot"></span>{{$s.State}}{{if $s.Process}} &nbsp;·&nbsp; {{$s.Instance}}/{{$s.Process}}{{end}}
    </span>
  </div>
  {{end}}
  {{if and (eq (printf "%s" $e.Kind) "mcp_server") $e.Attrs}}
  <div class="mcp-attrs">
    {{range $k, $v := $e.Attrs}}<span><span class="mcp-attrs-k">{{$k}}:</span> <span class="mcp-attrs-v">{{$v}}</span></span>{{end}}
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
{{else if eq (printf "%s" $e.Kind) "mcp_server"}}
<div class="mcp-card">
  {{$transport := index $e.Attrs "type"}}{{if not $transport}}{{$transport = "stdio"}}{{end}}
  <div class="mcp-transport-row">
    <span class="mcp-transport {{$transport}}">{{$transport}}</span>
    {{if and $s (eq $s.State "running")}}<span style="font-size:10px;color:var(--text3)">↑ connected</span>{{end}}
  </div>
  {{if $e.Attrs}}
  <div class="mcp-table">
    {{range $k, $v := $e.Attrs}}{{if ne $k "type"}}
    <div class="mcp-row">
      <div class="mcp-row-k">{{$k}}</div>
      <div class="mcp-row-v">{{$v}}</div>
    </div>
    {{end}}{{end}}
  </div>
  {{end}}
  <div class="mcp-detail">
    <div class="mcp-detail-row"><span class="mcp-detail-k">scope</span><span class="mcp-detail-v">{{if $e.Scope.Global}}global (~/.claude){{else}}project ({{$e.Scope.Project}}){{end}}</span></div>
    <div class="mcp-detail-row"><span class="mcp-detail-k">source</span><span class="mcp-detail-v">{{$e.Source}}</span></div>
    <div class="mcp-detail-row"><span class="mcp-detail-k">config</span><span class="mcp-detail-v" style="color:var(--text3)">{{$e.Path}}</span></div>
  </div>
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
{{if .Bridges}}
<div class="svc-instance bridges">
  <div class="svc-header">
    <span class="svc-name">network bridges</span>
    <span class="svc-endpoint">portproxy + firewall (persistent state)</span>
    <button class="svc-boot-btn"
            hx-post="/bridges/refresh"
            hx-target="#services-inner"
            hx-swap="outerHTML"
            title="reload bridges.yaml from disk and re-snapshot state">⟳ refresh</button>
    <button class="svc-boot-btn"
            hx-post="/bridges/apply"
            hx-target="#services-inner"
            hx-swap="outerHTML"
            title="re-apply every Windows-tier bridge in one UAC prompt">▶ apply all</button>
  </div>
  <table class="process-table">
    <thead><tr>
      <th>bridge</th><th>tier</th><th>state</th><th>listen</th><th>connect</th><th></th>
    </tr></thead>
    <tbody>
    {{range .Bridges}}
    <tr>
      <td>
        <div class="proc-name">{{.Name}}</div>
        {{if .DisplayName}}<div class="proc-ns">{{.DisplayName}}</div>{{end}}
      </td>
      <td>{{.Tier}}</td>
      <td><span class="status-pill {{.StateClass}}"><span class="dot"></span>{{.State}}</span></td>
      <td><code style="font-size:11px">{{.Listen}}</code></td>
      <td><code style="font-size:11px">{{.Connect}}</code></td>
      <td>
        <div class="proc-actions">
          <button class="proc-btn start"
                  hx-post="/bridge/apply?name={{.Name}}"
                  hx-target="#services-inner" hx-swap="outerHTML">apply</button>
          <button class="proc-btn stop"
                  hx-post="/bridge/reset?name={{.Name}}"
                  hx-target="#services-inner" hx-swap="outerHTML"
                  hx-confirm="Remove bridge {{.Name}}?">reset</button>
        </div>
      </td>
    </tr>
    {{end}}
    </tbody>
  </table>
</div>
{{end}}
{{if .Containers}}
<div class="svc-instance containers">
  <div class="svc-header">
    <span class="svc-name">containers</span>
    <span class="svc-endpoint">docker: matched by name</span>
    <button class="svc-boot-btn"
            hx-post="/containers/refresh"
            hx-target="#services-inner"
            hx-swap="outerHTML"
            title="reload containers.yaml from disk and re-snapshot state">⟳ refresh</button>
  </div>
  <table class="process-table">
    <thead><tr>
      <th>container</th><th>state</th><th>status</th><th>cpu</th><th>mem</th><th>id</th><th></th>
    </tr></thead>
    <tbody>
    {{range .Containers}}
    <tr>
      <td>
        <div class="proc-name">{{.Name}}</div>
        {{if .Description}}<div class="proc-ns">{{.Description}}</div>{{end}}
      </td>
      <td><span class="status-pill {{.StateClass}}"><span class="dot"></span>{{.State}}</span></td>
      <td>{{if .Status}}<code style="font-size:11px">{{.Status}}</code>{{else}}—{{end}}</td>
      <td>
        {{if .HasStats}}
        <div class="usage-bar">
          <div class="usage-bar-track"><div class="usage-bar-fill {{cpuBarClass .CPU}}" style="width:{{cpuBarWidth .CPU}}%"></div></div>
          <span class="usage-val">{{printf "%.1f" .CPU}}%</span>
        </div>
        {{else}}—{{end}}
      </td>
      <td>
        {{if .HasStats}}
        <div class="usage-bar">
          <div class="usage-bar-track"><div class="usage-bar-fill mem" style="width:{{memBarWidth .Mem}}%"></div></div>
          <span class="usage-val">{{formatMem .Mem}}</span>
        </div>
        {{else}}—{{end}}
      </td>
      <td>{{if .ID}}<code style="font-size:11px">{{.ID}}</code>{{else}}—{{end}}</td>
      <td>
        <div class="proc-actions">
          {{if eq .StateClass "running"}}
          <button class="proc-btn stop"
                  hx-post="/decl-container/stop?name={{.Name}}"
                  hx-target="#services-inner" hx-swap="outerHTML">stop</button>
          {{else if .ID}}
          <button class="proc-btn start"
                  hx-post="/decl-container/start?name={{.Name}}"
                  hx-target="#services-inner" hx-swap="outerHTML">start</button>
          {{end}}
        </div>
      </td>
    </tr>
    {{end}}
    </tbody>
  </table>
</div>
{{end}}
{{if not .Instances}}
  <p style="color:var(--text3);font-style:italic">no process-compose instances configured — add them to config.yaml</p>
{{else}}
{{range $iv := .Instances}}
<div class="svc-instance">
  <div class="svc-header">
    <span class="online-dot {{if $iv.Online}}online{{else}}offline{{end}}"></span>
    <span class="svc-name">{{$iv.Name}}</span>
    <span class="svc-endpoint">{{$iv.Endpoint}}</span>
    {{if $iv.Online}}
    <span class="svc-running-count">{{runningCount $iv.Processes}}/{{len $iv.Processes}} running</span>
    <button class="svc-boot-btn"
            hx-post="/compose/reload?instance={{$iv.Name}}"
            hx-target="#services-inner"
            hx-swap="outerHTML"
            title="re-read compose YAML and reconcile (drops removed entries on recent process-compose versions)">↻ reload</button>
    {{else if $iv.CanBoot}}
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
      <td>
        <div class="proc-name">{{.Name}}</div>
        {{if and .Namespace (ne .Namespace "default")}}<div class="proc-ns">{{.Namespace}}</div>{{end}}
      </td>
      <td>
        <span class="status-pill {{statusClass .Status}}"><span class="dot"></span>{{.Status}}</span>
        {{if .HasHealthProbe}}<span class="health-pill {{healthClass .Health}}" title="readiness: {{.Health}}">{{.Health}}</span>{{end}}
        {{if and (not .IsRunning) (ne .ExitCode 0)}}<span class="exit-code" title="exit code">{{.ExitCode}}</span>{{end}}
      </td>
      <td>{{if .Pid}}{{.Pid}}{{else}}—{{end}}</td>
      <td>{{.Restarts}}</td>
      <td>
        <div class="usage-bar">
          <div class="usage-bar-track"><div class="usage-bar-fill {{cpuBarClass .CPU}}" style="width:{{cpuBarWidth .CPU}}%"></div></div>
          <span class="usage-val">{{printf "%.1f" .CPU}}%</span>
        </div>
      </td>
      <td>
        <div class="usage-bar">
          <div class="usage-bar-track"><div class="usage-bar-fill mem" style="width:{{memBarWidth .Mem}}%"></div></div>
          <span class="usage-val">{{formatMem .Mem}}</span>
        </div>
      </td>
      <td>{{if .SystemTime}}{{.SystemTime}}{{else}}—{{end}}</td>
      <td>
        <div class="proc-actions">
          {{if canStop .Status}}
          <button class="proc-btn stop"
                  hx-post="/process/stop?instance={{$iv.Name}}&process={{.Name}}"
                  hx-target="#services-inner" hx-swap="outerHTML"
                  title="halt the process and stop the restart loop">stop</button>
          {{end}}
          {{if canStart .Status}}
          <button class="proc-btn start"
                  hx-post="/process/start?instance={{$iv.Name}}&process={{.Name}}"
                  hx-target="#services-inner" hx-swap="outerHTML">start</button>
          {{end}}
          {{if .IsRunning}}
          <button class="proc-btn restart"
                  hx-post="/process/restart?instance={{$iv.Name}}&process={{.Name}}"
                  hx-target="#services-inner" hx-swap="outerHTML">restart</button>
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
    <span class="svc-offline-icon">⬡</span>
    <div class="svc-offline-title">process-compose unreachable</div>
    <div class="svc-offline-endpoint">{{$iv.Endpoint}}</div>
    {{if $iv.CanBoot}}
    <button class="svc-boot-btn" style="margin-left:0;margin-top:4px"
            hx-post="/compose/start?instance={{$iv.Name}}"
            hx-target="#services-inner"
            hx-swap="outerHTML">▶ start process-compose</button>
    {{else}}
    <div class="svc-offline-hint">no binary configured — add <code style="background:var(--bg3);padding:1px 4px;border-radius:3px">binary</code> to config.json to enable bootstrap</div>
    {{end}}
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
<link rel="icon" type="image/svg+xml" href="/favicon.svg">
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
  <div class="logo"><svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 28 20" width="28" height="20" style="vertical-align:middle;margin-right:4px"><polygon points="8,2 14,2 17,7 14,12 8,12 5,7" fill="var(--accent)" opacity="0.95"/><polygon points="13,8 19,8 22,13 19,18 13,18 10,13" fill="none" stroke="var(--accent)" stroke-width="1.2" opacity="0.55"/></svg>infra-mngmt <span>/ login</span></div>
  {{if .Error}}<div class="error">{{.Error}}</div>{{end}}
  <label for="token">bearer token</label>
  <input id="token" type="password" name="token" autofocus placeholder="paste your token">
  <input type="hidden" name="next" value="{{.Next}}">
  <button type="submit">sign in</button>
</form>
</body>
</html>
`
