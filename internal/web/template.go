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
.build-chip{position:fixed;bottom:8px;right:12px;z-index:50;display:inline-flex;align-items:center;gap:6px;padding:3px 8px;border:1px solid var(--border);border-radius:3px;font-family:ui-monospace,monospace;font-size:10px;color:var(--text3);background:var(--bg2);flex-shrink:0;cursor:help;opacity:.75}
.build-chip:hover{opacity:1;color:var(--text2);border-color:var(--border2)}
.build-chip-commit{color:var(--text2);letter-spacing:.04em}
.build-chip-when{color:var(--text3)}
.build-chip-when:empty::before{content:"local"}
.build-chip.stale{opacity:1;color:var(--accent);border-color:var(--accent);cursor:pointer}
.build-chip.stale:hover{background:color-mix(in srgb,var(--accent) 14%,var(--bg2));color:var(--white);border-color:var(--accent)}
.build-chip.stale .build-chip-commit,.build-chip.stale .build-chip-when{color:inherit}
.build-chip-reload{background:none;border:none;color:inherit;cursor:pointer;padding:0;margin-left:2px;font:inherit;font-size:11px;line-height:1;display:none;pointer-events:none}
.build-chip.stale .build-chip-reload{display:inline-block}
.logo span{color:var(--text3);font-weight:normal}
.view-tabs{display:flex;gap:2px;flex-shrink:0}
.view-tab{background:transparent;border:1px solid transparent;color:var(--text2);padding:3px 10px;border-radius:4px;cursor:pointer;font-family:inherit;font-size:11px;transition:all .12s}
.view-tab:hover{border-color:var(--border2);color:var(--text)}
.view-tab.active{background:var(--bg4);border-color:var(--border2);color:var(--white)}
.divider{width:1px;background:var(--border);align-self:stretch;margin:0 4px}
.tab-scroll-wrap{display:flex;align-items:center;flex:1;min-width:0;gap:4px}
.tab-scroll-btn{background:var(--bg3);border:1px solid var(--border2);color:var(--text2);padding:2px 7px;border-radius:3px;cursor:pointer;font-size:12px;line-height:1.4;flex-shrink:0;font-family:inherit;transition:all .12s}
.tab-scroll-btn:hover{border-color:var(--accent);color:var(--accent);background:var(--bg4)}
.topbar-btn{background:transparent;border:1px solid var(--border2);color:var(--text2);padding:2px 8px;border-radius:3px;cursor:pointer;font-size:13px;line-height:1.3;flex-shrink:0;font-family:inherit;transition:all .12s;height:22px;display:inline-flex;align-items:center;justify-content:center;min-width:26px}
.topbar-btn:hover{border-color:var(--accent);color:var(--accent);background:var(--bg3)}
.topbar-btn.spinning{animation:spin .7s linear infinite;color:var(--accent);border-color:var(--accent)}
.topbar-btn:disabled{opacity:.5;cursor:wait}
.source-tabs{display:flex;gap:4px;flex:1;overflow-x:auto;scrollbar-width:none;padding:0 4px}
.source-tabs::-webkit-scrollbar{display:none}
.tab{background:transparent;border:1px solid transparent;color:var(--text2);padding:3px 10px;border-radius:4px;cursor:pointer;font-family:inherit;font-size:11px;white-space:nowrap;transition:all .12s}
.tab:hover{border-color:var(--border2);color:var(--text)}
.tab.active{background:var(--bg4);border-color:var(--border2);color:var(--white)}
.tab .scope-badge{font-size:9px;margin-left:5px;padding:1px 5px;border-radius:3px}
.tab .scope-badge.global{background:color-mix(in srgb,var(--level-global) 12%,transparent);color:var(--level-global)}
.tab .scope-badge.project{background:color-mix(in srgb,var(--level-project) 12%,transparent);color:var(--level-project)}
.tab .scope-badge.devcontainer{background:color-mix(in srgb,var(--level-devcontainer) 12%,transparent);color:var(--level-devcontainer)}
.search-wrap{position:relative;display:inline-flex;align-items:center;flex-shrink:0;background:var(--bg3);border:1px solid var(--border2);border-radius:4px;width:26px;height:24px;transition:width .18s ease,border-color .12s,background .12s;overflow:hidden;cursor:text;margin-left:auto}
.search-wrap:hover{background:var(--bg4);border-color:var(--border2)}
.search-wrap:focus-within,.search-wrap.has-value{width:200px;background:var(--bg3)}
.search-wrap:focus-within{border-color:#444}
.search-icon{position:absolute;left:7px;top:50%;transform:translateY(-50%);width:12px;height:12px;color:var(--text3);pointer-events:none;transition:color .12s}
.search-wrap:hover .search-icon,.search-wrap:focus-within .search-icon,.search-wrap.has-value .search-icon{color:var(--text2)}
#search{background:transparent;border:none;color:var(--text);padding:4px 8px 4px 24px;border-radius:4px;font-family:inherit;font-size:11px;width:100%;outline:none;min-width:0}
#search::placeholder{color:var(--text3)}

/* ── kind filter bar ── */
.kind-bar{display:flex;align-items:center;gap:4px;padding:8px 16px;border-bottom:1px solid var(--border);flex-shrink:0}
.kind-bar-refresh{margin-left:auto;height:20px;min-width:24px;padding:0 6px;font-size:12px}
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
.entity-tool-tag{display:inline-flex;align-items:center;justify-content:center;flex-shrink:0;padding:0 4px;border-radius:3px;height:16px;font-size:9px;letter-spacing:.02em;border:1px solid transparent;gap:3px}
.entity-tool-tag svg{display:block}
.entity-tool-tag.claude{background:color-mix(in srgb,#D97757 12%,transparent);border-color:color-mix(in srgb,#D97757 25%,transparent)}
.entity-tool-tag.opencode{background:color-mix(in srgb,#cccccc 10%,transparent);color:#d8d8d8;border-color:color-mix(in srgb,#cccccc 18%,transparent)}
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
#view-llama{flex:1;overflow-y:auto;padding:16px 24px;display:none;flex-direction:column}
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
.svc-offline .svc-boot-btn{margin-left:0;margin-top:4px}
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
.process-table code{font-size:11px;color:var(--text2)}
.process-table .col-name{width:220px;max-width:220px}
.process-table .col-name .proc-name{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.process-table .col-state{width:96px}
.process-table .col-pid{width:64px}
.process-table .col-actions{width:1%;white-space:nowrap;text-align:right}
.svc-icon{display:inline-block;width:16px;text-align:center;color:var(--text2);font-size:13px;line-height:1;flex-shrink:0}
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
.bridge-composite{cursor:pointer}
.bridge-composite:hover{background:var(--bg2)}
.bridge-composite.expanded .bridge-disclosure{transform:rotate(90deg)}
.bridge-disclosure{display:inline-block;color:var(--text3);font-size:9px;width:12px;transition:transform .12s ease}
.bridge-composite-summary{color:var(--text3);font-style:italic;font-size:11px}
.bridge-member > td{background:var(--bg1);border-top:1px dashed var(--border)}
.bridge-member-name{padding-left:6px !important}
.bridge-member-indent{color:var(--text3);font-family:monospace;margin-right:6px}
/* Server renders bridge-namespace processes always; CSS hides them by default
   so the toggle is purely client-side. body.show-internals flips the rule. */
tr.proc-internal{display:none}
body.show-internals tr.proc-internal{display:table-row}
.proc-ns{color:var(--text3);font-size:9px;letter-spacing:.04em;text-transform:uppercase;margin-top:1px}
.proc-actions{display:inline-flex;gap:4px}
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
/* ── llama view ── */
.llama-page-empty{color:var(--text3);font-style:italic;padding:20px}
.llama-page-empty code{background:var(--bg3);color:var(--text2);padding:1px 5px;border-radius:3px;font-size:11px}
.llama-grid{display:flex;flex-direction:column;gap:14px}
.llama-card{background:var(--bg2);border:1px solid var(--border);border-radius:6px;padding:14px 18px;display:flex;flex-direction:column;gap:10px;font-size:11px;color:var(--text)}
.llama-head{display:flex;align-items:center;gap:8px;flex-wrap:wrap}
.llama-card-name{color:var(--white);font-weight:600;letter-spacing:.02em}
.llama-endpoint{color:var(--text2);font-family:ui-monospace,monospace;font-size:10px}
.llama-pill{padding:1px 6px;border-radius:10px;font-size:10px;display:inline-flex;align-items:center;gap:4px}
.llama-pill.good{background:#1a3a1a;color:var(--green)}
.llama-pill.warn{background:#2a2a1a;color:var(--yellow)}
.llama-pill.bad{background:#3a1a1a;color:var(--red)}
.llama-pill.mode{background:color-mix(in srgb,var(--accent) 12%,transparent);color:var(--accent)}
.llama-build{color:var(--text3);font-family:ui-monospace,monospace;font-size:10px;margin-left:auto}
.llama-identity{display:flex;flex-wrap:wrap;gap:4px 18px;color:var(--text2);font-size:10px}
.llama-identity .lbl{color:var(--text3);margin-right:4px;text-transform:uppercase;letter-spacing:.04em}
.llama-identity code{color:var(--text);background:var(--bg3);padding:1px 5px;border-radius:3px;font-size:10px}
.llama-stats{display:grid;grid-template-columns:repeat(6,minmax(0,1fr));gap:6px 16px;align-items:center}
.llama-stat{display:flex;flex-direction:column;gap:2px}
.llama-stat .lbl{color:var(--text3);font-size:9px;text-transform:uppercase;letter-spacing:.04em}
.llama-stat .val{color:var(--text);font-family:ui-monospace,monospace}
.llama-stat .val.warn{color:var(--yellow)}
.llama-kv{display:flex;flex-direction:column;gap:3px}
.llama-kv-label{display:flex;justify-content:space-between;font-size:10px}
.llama-kv-label .lbl{color:var(--text3);text-transform:uppercase;letter-spacing:.04em}
.llama-kv-label .val{color:var(--text2);font-family:ui-monospace,monospace}
.llama-kv .usage-bar-track{width:100%;height:6px}
.llama-kv .usage-bar-fill.low{background:var(--green)}
.llama-kv .usage-bar-fill.med{background:var(--yellow)}
.llama-kv .usage-bar-fill.high{background:var(--orange)}
.llama-row-err{color:var(--red);font-size:10px}
.llama-row-hint{color:var(--text3);font-style:italic;font-size:10px}
.llama-row-hint code{background:var(--bg3);color:var(--text2);padding:1px 4px;border-radius:3px;font-size:10px}
.llama-models{display:flex;flex-direction:column;gap:6px;margin-top:4px}
.llama-model{padding:8px 10px;border:1px solid var(--border);border-left:3px solid var(--text3);border-radius:4px;background:var(--bg);display:flex;flex-direction:column;gap:6px}
.llama-model-loaded{border-left-color:var(--green)}
.llama-model-sleeping{border-left-color:var(--yellow)}
.llama-model-loading{border-left-color:var(--accent)}
.llama-model-unloaded{border-left-color:var(--text3);opacity:.6}
.llama-model.failed{border-left-color:var(--red);opacity:.7}
.llama-model.synthetic{border-left-color:var(--accent);opacity:.55;border-style:dashed}
.llama-model-path{display:flex;align-items:baseline;gap:8px;font-size:10px;color:var(--text2)}
.llama-model-path .lbl{color:var(--text3);text-transform:uppercase;letter-spacing:.04em;font-size:9px}
.llama-model-path code{color:var(--text);background:var(--bg3);padding:1px 5px;border-radius:3px;font-size:10px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;max-width:520px;display:inline-block;vertical-align:bottom}
.llama-model-head{display:flex;align-items:center;gap:8px;flex-wrap:wrap}
.llama-model-status{font-size:9px;text-transform:uppercase;letter-spacing:.06em;padding:1px 6px;border-radius:3px;color:var(--text3);background:var(--bg3)}
.llama-model-status.status-loaded{color:var(--green);background:#1a3a1a}
.llama-model-status.status-sleeping{color:var(--yellow);background:#2a2a1a}
.llama-model-status.status-loading{color:var(--accent);background:color-mix(in srgb,var(--accent) 12%,transparent)}
.llama-model-id{color:var(--text);font-family:ui-monospace,monospace;font-size:10px;background:var(--bg3);padding:1px 5px;border-radius:3px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;max-width:560px}
.llama-slots{display:grid;grid-template-columns:repeat(auto-fill,minmax(280px,1fr));gap:8px}
.llama-slot{padding:9px 12px;border:1px solid var(--border);border-radius:5px;background:var(--bg2);display:flex;flex-direction:column;gap:8px;font-size:11px}
.llama-slot.busy{border-color:var(--kind-claude_md);background:color-mix(in srgb,var(--kind-claude_md) 4%,var(--bg2))}
.llama-slot-head{display:flex;align-items:center;gap:6px;flex-wrap:wrap}
.llama-slot-id{color:var(--text2);font-family:ui-monospace,monospace;font-weight:600;letter-spacing:.04em}
.llama-slot.busy .llama-slot-id{color:var(--kind-claude_md)}
.llama-slot-state{font-size:9px;text-transform:uppercase;letter-spacing:.06em;padding:1px 6px;border-radius:3px}
.llama-slot-state.busy{color:var(--kind-claude_md);background:color-mix(in srgb,var(--kind-claude_md) 14%,transparent)}
.llama-slot-state.idle{color:var(--text3);background:var(--bg3)}
.llama-slot-task{color:var(--text3);font-family:ui-monospace,monospace;font-size:10px;margin-left:auto}
.llama-slot-tag{font-size:9px;padding:1px 5px;border-radius:3px;font-family:ui-monospace,monospace;color:var(--text3);background:var(--bg3)}
.llama-slot-tag.spec{color:var(--accent);background:color-mix(in srgb,var(--accent) 14%,transparent)}
.llama-slot-progress{display:flex;flex-direction:column;gap:3px}
.llama-slot-bar{height:5px;border-radius:3px;background:var(--bg4);overflow:hidden}
.llama-slot-fill{height:100%;background:var(--kind-claude_md);transition:width .3s}
.llama-slot-progress-label{display:flex;justify-content:space-between;color:var(--text2);font-family:ui-monospace,monospace;font-size:10px}
.llama-slot-detail{display:grid;grid-template-columns:auto 1fr;gap:2px 12px;margin:0;font-size:10px}
.llama-slot-detail dt{color:var(--text3);text-transform:uppercase;letter-spacing:.04em;font-size:9px;align-self:center}
.llama-slot-detail dd{margin:0;color:var(--text);font-family:ui-monospace,monospace}
.llama-slot-detail dd .muted{color:var(--text3)}
.llama-logs-section{margin-top:4px;border-top:1px solid var(--border);padding-top:8px}
.llama-logs-section summary{cursor:pointer;color:var(--text2);font-size:10px;display:flex;align-items:center;gap:8px;padding:2px 0;list-style:none;outline:none}
.llama-logs-section summary::-webkit-details-marker{display:none}
.llama-logs-section summary::before{content:"▸";color:var(--text3);font-size:9px;width:9px;display:inline-block;transition:transform .12s}
.llama-logs-section[open] summary::before{transform:rotate(90deg)}
.llama-logs-section summary:hover{color:var(--text)}
.llama-logs-section .lbl{color:var(--text3);text-transform:uppercase;letter-spacing:.04em;font-size:9px}
.llama-logs-count{color:var(--text3);font-family:ui-monospace,monospace;font-size:10px;margin-left:auto}
.llama-logs-body{margin-top:4px;background:var(--bg);border:1px solid var(--border);border-radius:4px;padding:6px 8px;max-height:200px;overflow-y:auto;font-size:10px;line-height:1.55;font-family:ui-monospace,monospace}
.llama-log-line{color:var(--text2);white-space:pre-wrap;word-break:break-all}
.llama-log-time{color:var(--text3);margin-right:8px}
.llama-logs-empty{color:var(--text3);font-style:italic;font-size:10px;padding:4px 0}

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
.preview-toolbar{display:flex;justify-content:flex-end;margin-top:8px;gap:6px}
.promote-btn{background:transparent;border:1px solid var(--border2);color:var(--text2);padding:2px 9px;border-radius:3px;cursor:pointer;font-family:inherit;font-size:10px;transition:all .1s;position:relative}
.promote-btn:hover{border-color:var(--accent);color:var(--accent)}
.promote-btn:disabled{opacity:.5;cursor:wait}
.promote-btn.htmx-request::after{content:"";display:inline-block;width:8px;height:8px;margin-left:5px;border:1px solid currentColor;border-top-color:transparent;border-radius:50%;animation:spin .6s linear infinite;vertical-align:-1px}
.promote-slot:empty{display:none}
/* ── promote modal ── */
.promote-modal{position:fixed;inset:0;background:rgba(0,0,0,.55);display:flex;align-items:flex-start;justify-content:center;z-index:9999;padding-top:8vh;backdrop-filter:blur(2px);-webkit-backdrop-filter:blur(2px);animation:promoteFadeIn .12s ease-out}
@keyframes promoteFadeIn{from{opacity:0}to{opacity:1}}
.promote-modal-card{background:var(--bg2);border:1px solid var(--border2);border-radius:8px;width:min(560px,calc(100vw - 32px));max-height:80vh;display:flex;flex-direction:column;box-shadow:0 20px 60px rgba(0,0,0,.5);animation:promoteSlideIn .14s ease-out}
@keyframes promoteSlideIn{from{transform:translateY(-8px);opacity:0}to{transform:translateY(0);opacity:1}}
.promote-modal-head{display:flex;justify-content:space-between;align-items:flex-start;gap:12px;padding:14px 16px;border-bottom:1px solid var(--border)}
.promote-modal-titles{flex:1;min-width:0}
.promote-modal-title{color:var(--white);font-size:13px;font-weight:500;margin-bottom:3px}
.promote-modal-title strong{color:var(--accent);font-weight:600}
.promote-modal-title .promote-kind{color:var(--text3);font-size:11px;text-transform:uppercase;letter-spacing:.04em;margin-right:2px}
.promote-modal-sub{color:var(--text3);font-size:11px;font-family:ui-monospace,monospace;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.promote-modal-sub span{color:var(--text2)}
.promote-modal-body{padding:14px 16px;overflow-y:auto;display:flex;flex-direction:column;gap:14px}
.promote-rename{display:flex;flex-direction:column;gap:4px}
.promote-rename-label{color:var(--text3);font-size:10px;text-transform:uppercase;letter-spacing:.06em}
.promote-rename-input{background:var(--bg3);border:1px solid var(--border2);color:var(--text);padding:7px 10px;border-radius:4px;font-family:inherit;font-size:12px;outline:none;transition:border-color .1s}
.promote-rename-input:focus{border-color:var(--accent)}
.promote-mirror-row{display:flex;flex-direction:column;gap:4px}
.promote-mirror{display:flex;align-items:center;gap:8px;color:var(--text);font-size:11px;cursor:pointer;user-select:none}
.promote-mirror input{accent-color:var(--accent);cursor:pointer}
.promote-mirror-info{position:relative;display:inline-flex;align-items:center;justify-content:center;width:14px;height:14px;border:1px solid var(--border2);border-radius:50%;color:var(--text3);font-size:9px;font-weight:600;line-height:1;cursor:help;background:var(--bg3);transition:all .1s}
.promote-mirror-info:hover,.promote-mirror-info:focus{color:var(--text);border-color:var(--text2);outline:none}
.promote-mirror-info::after{content:attr(data-tooltip);position:absolute;left:0;top:calc(100% + 6px);background:var(--bg);border:1px solid var(--border2);color:var(--text2);padding:8px 10px;border-radius:4px;font-size:10px;font-weight:400;line-height:1.5;width:280px;white-space:normal;display:none;z-index:10;box-shadow:0 4px 12px rgba(0,0,0,.5);text-align:left;cursor:default;letter-spacing:0;text-transform:none}
.promote-mirror-info:hover::after,.promote-mirror-info:focus::after{display:block}
.promote-mirror-hint{color:var(--text3);font-size:10px;padding-left:24px;line-height:1.4}
.promote-empty{color:var(--text3);font-size:11px;padding:14px;text-align:center;background:var(--bg3);border:1px dashed var(--border2);border-radius:4px}
.promote-group{display:flex;flex-direction:column;gap:6px}
.promote-group-title{display:flex;align-items:center;gap:8px;color:var(--text2);font-size:10px;font-weight:600;text-transform:uppercase;letter-spacing:.08em;margin:0}
.promote-group-title::after{content:"";flex:1;height:1px;background:var(--border)}
.promote-group-count{background:var(--bg3);color:var(--text3);font-size:9px;padding:1px 6px;border-radius:8px;font-weight:500;letter-spacing:0;text-transform:none}
.promote-targets{list-style:none;display:flex;flex-direction:column;gap:4px}
.promote-target{display:flex;justify-content:space-between;align-items:center;gap:10px;width:100%;background:var(--bg3);border:1px solid var(--border);color:var(--text);padding:9px 12px;border-radius:5px;cursor:pointer;font-family:inherit;font-size:12px;text-align:left;transition:all .1s}
.promote-target:not(:disabled):hover{border-color:var(--accent);background:var(--bg2);transform:translateX(2px)}
.promote-target.exists{border-color:#553f1a}
.promote-target.exists:not(:disabled):hover{border-color:#dca97d}
.promote-target.readonly,.promote-target:disabled{cursor:not-allowed;opacity:.42;background:var(--bg)}
.promote-target-main{display:flex;flex-direction:column;gap:2px;min-width:0;flex:1}
.promote-target-label{color:var(--white);font-weight:500;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.promote-target-path{color:var(--text3);font-size:10px;font-family:ui-monospace,monospace;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.promote-target-flags{display:flex;gap:5px;align-items:center;flex-shrink:0}
.promote-flag{font-size:9px;text-transform:uppercase;letter-spacing:.06em;padding:2px 6px;border-radius:3px;font-weight:600}
.promote-flag.new{background:#0e1f17;color:#5cb088;border:1px solid #1d4d3c}
.promote-flag.exists{background:#241c10;color:#dca97d;border:1px solid #553f1a}
.promote-flag.readonly{background:#1c1c1c;color:var(--text3);border:1px solid var(--border2)}
.promote-flag.cross-tool{background:#1c1228;color:#b89cda;border:1px solid #3a285a}
/* result variants */
.promote-result-card.ok .promote-result-icon{color:#7ddca7}
.promote-result-card.warn .promote-result-icon{color:#dca97d}
.promote-result-card.error .promote-result-icon{color:var(--red)}
.promote-result-icon{margin-right:6px}
.promote-result-prompt{color:var(--text2);font-size:12px;margin:0}
.promote-result-actions{display:flex;justify-content:flex-end;gap:6px;margin-top:8px}
.promote-confirm{background:#dca97d;border:1px solid #dca97d;color:#1a0e02;padding:6px 14px;border-radius:4px;cursor:pointer;font-family:inherit;font-size:11px;font-weight:600;transition:all .1s}
.promote-confirm:hover{filter:brightness(1.1)}
.promote-cancel{background:transparent;border:1px solid var(--border2);color:var(--text2);padding:6px 14px;border-radius:4px;cursor:pointer;font-family:inherit;font-size:11px;transition:all .1s}
.promote-cancel:hover{border-color:var(--text);color:var(--text)}
.promote-close{background:transparent;border:none;color:var(--text3);font-size:20px;line-height:1;cursor:pointer;padding:0 6px;border-radius:3px;transition:all .1s}
.promote-close:hover{color:var(--white);background:var(--bg3)}
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
.ov-ctr-dot{width:7px;height:7px;border-radius:50%;flex-shrink:0;transition:background .2s}
.ov-ctr-dot.running{background:#3a3}
.ov-ctr-dot.exited,.ov-ctr-dot.stopped{background:var(--text3)}
.ov-ctr-dot.starting{background:#3a3;animation:ctrPulse 1.2s ease-in-out infinite}
@keyframes ctrPulse{0%,100%{opacity:.35;transform:scale(1)}50%{opacity:1;transform:scale(1.5)}}
.ov-ctr-label{font-size:10px;color:var(--text2)}
.ov-ctr-btn{background:var(--bg3);border:1px solid var(--border2);color:var(--text);font-size:10px;padding:1px 7px;border-radius:3px;cursor:pointer;font-family:inherit;text-decoration:none;display:inline-flex;align-items:center}
.ov-ctr-btn:hover{border-color:var(--accent);color:var(--accent)}
.ov-ctr-btn:disabled{opacity:.4;cursor:default}
/* ── container log panel ── */
.ctr-log-wrap{border-top:1px solid var(--border)}
.ctr-log-bar{display:flex;justify-content:space-between;align-items:center;padding:4px 16px;font-size:10px;color:var(--text3)}
.ctr-log-close{background:none;border:none;color:var(--text3);cursor:pointer;font-size:14px;padding:0;line-height:1}
.ctr-log-close:hover{color:var(--text)}
.ctr-log-pre{margin:0 8px 8px;padding:8px;font-size:10px;line-height:1.4;color:var(--text);background:var(--bg);border:1px solid var(--border);border-radius:3px;height:240px;overflow-y:auto;white-space:pre-wrap;word-break:break-all}
.ctr-event-list{margin:0 8px 8px;padding:6px 8px;font-size:10px;background:var(--bg);border:1px solid var(--border);border-radius:3px;height:240px;overflow-y:auto;display:flex;flex-direction:column;gap:2px}
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
    <button class="view-tab" id="vtab-llama" onclick="showView('llama')">llama</button>
    <button class="view-tab" id="vtab-services" onclick="showView('services')">services</button>
  </div>

  <div class="divider"></div>

  <div class="tab-scroll-wrap" id="tab-scroll-wrap">
    <button class="tab-scroll-btn" onclick="document.getElementById('source-tabs-bar').scrollBy({left:-140,behavior:'smooth'})">‹</button>
    <div class="source-tabs" id="source-tabs-bar">
      <button class="tab active" data-project="__all__">all</button>
      {{range .Sources}}
      <button class="tab" data-project="{{.Key}}">
        {{.Label}}{{range .Levels}}<span class="scope-badge {{.}}" title="{{.}}">{{levelShort .}}</span>{{end}}
      </button>
      {{end}}
    </div>
    <button class="tab-scroll-btn" onclick="document.getElementById('source-tabs-bar').scrollBy({left:140,behavior:'smooth'})">›</button>
  </div>

  <button class="topbar-btn" id="ov-rescan" onclick="doRescan(event)" title="Rescan sources — discover new projects / devcontainers">⤺</button>

  <label class="search-wrap" for="search" title="Search (⌘K)">
    <svg class="search-icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" aria-hidden="true">
      <circle cx="7" cy="7" r="4.5"></circle>
      <path d="M10.5 10.5l3 3"></path>
    </svg>
    <input id="search" type="text" placeholder="⌘K search" autocomplete="off">
  </label>
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
  <button class="topbar-btn kind-bar-refresh" id="ov-refresh" onclick="doRefresh(event)" title="Refresh entities — re-read all known sources">↻</button>
</div>

<div id="project-overview" style="display:none">
  <div class="ov-row" id="ov-row">
    <span class="ov-name" id="ov-name" style="display:none"></span>
    <span class="ov-scopes" id="ov-scopes" style="display:none"></span>
    <span class="ov-sep" id="ov-sep" style="display:none">·</span>
    <span class="ov-counts" id="ov-counts" style="display:none"></span>
    <span class="ov-spacer"></span>
    <div class="ov-ctr" id="ov-ctr"></div>
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

<div id="view-llama"
     hx-get="/partials/llama"
     hx-trigger="revealed, every 10s"
     hx-swap="innerHTML">
  <p style="color:var(--text3);font-style:italic;padding:20px">loading llama servers…</p>
</div>

<script>
// ── view switching ──
function showView(v) {
  document.getElementById('view-config').style.display   = v === 'config'   ? 'flex' : 'none';
  document.getElementById('view-services').style.display = v === 'services' ? 'flex' : 'none';
  document.getElementById('view-llama').style.display    = v === 'llama'    ? 'flex' : 'none';
  document.getElementById('kind-bar').style.display      = v === 'config'   ? 'flex' : 'none';
  document.getElementById('tab-scroll-wrap').style.display = v === 'config' ? 'flex' : 'none';
  document.getElementById('vtab-config').classList.toggle('active',   v === 'config');
  document.getElementById('vtab-services').classList.toggle('active', v === 'services');
  document.getElementById('vtab-llama').classList.toggle('active',    v === 'llama');
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
// Initial load: activeProject is '__all__', so apply any persisted collapse
// state from a previous session. (Project views always start expanded.)
// Defined later in this script — run on next tick so the function is in scope.
queueMicrotask(() => applyCollapsedStateForAllView());

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

// ── kind-group collapse/expand persistence ──
// Persisted only for the 'all' view (activeProject === '__all__'). Project
// tabs always start fully expanded — collapsing while on a project is
// ephemeral and gets reset on the next tab switch.
const COLLAPSED_KEY = 'infra-mngmt:groups-collapsed';

function readCollapsedSet() {
  try {
    const raw = localStorage.getItem(COLLAPSED_KEY);
    return new Set(raw ? JSON.parse(raw) : []);
  } catch (_) { return new Set(); }
}

function writeCollapsedSet(set) {
  try { localStorage.setItem(COLLAPSED_KEY, JSON.stringify([...set])); } catch (_) {}
}

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
  const set = new Set(JSON.parse(localStorage.getItem(key) || '[]'));
  if (expanded) set.add(name); else set.delete(name);
  localStorage.setItem(key, JSON.stringify([...set]));
}
function reapplyCompositeExpansion() {
  const key = 'im_composite_expanded';
  const set = new Set(JSON.parse(localStorage.getItem(key) || '[]'));
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
  let set;
  try { set = new Set(JSON.parse(localStorage.getItem(LLAMA_LOGS_OPEN_KEY) || '[]')); }
  catch (_) { set = new Set(); }
  if (el.open) set.add(key); else set.delete(key);
  try { localStorage.setItem(LLAMA_LOGS_OPEN_KEY, JSON.stringify([...set])); } catch (_) {}
}
function applyLlamaLogsOpenFromStorage() {
  let set;
  try { set = new Set(JSON.parse(localStorage.getItem(LLAMA_LOGS_OPEN_KEY) || '[]')); }
  catch (_) { return; }
  document.querySelectorAll('details.llama-logs-section[data-llama-logs-key]').forEach(d => {
    if (set.has(d.getAttribute('data-llama-logs-key'))) d.open = true;
  });
}

// Re-apply both states whenever htmx swaps in fresh services HTML.
document.addEventListener('htmx:afterSwap', (e) => {
  if (!e.target) return;
  if (e.target.id === 'services-inner' || e.target.id === 'view-services') {
    applyShowInternalsFromStorage();
    reapplyCompositeExpansion();
  }
  if (e.target.id === 'view-llama') {
    applyLlamaLogsOpenFromStorage();
  }
});
// And on initial load.
document.addEventListener('DOMContentLoaded', () => {
  applyShowInternalsFromStorage();
  reapplyCompositeExpansion();
  applyLlamaLogsOpenFromStorage();
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

async function doRescan(e) {
  e.stopPropagation();
  const btn = document.getElementById('ov-rescan');
  btn.classList.add('spinning');
  btn.disabled = true;
  try {
    const r = await fetch('/api/sources/rescan', {method:'POST'});
    if (!r.ok) {
      showRescanToast('rescan failed: HTTP ' + r.status, 'error');
      return;
    }
    const body = await r.json();
    const added = (body.added || []).length;
    const discovered = (body.discovered || []).length;
    if (added > 0) {
      // Refresh the entity list so the new sources actually render. Pass a
      // synthetic event because doRefresh() calls e.stopPropagation().
      await doRefresh({stopPropagation:()=>{}});
      showRescanToast('added ' + added + ' new source' + (added===1?'':'s') + ': ' + body.added.join(', '), 'ok');
    } else {
      // Surface what was actually scanned so the user can debug "why didn't
      // my new project show up?". The list is the universe of sources
      // discovery currently sees; if their new project isn't there, the
      // problem is upstream (no .claude/, no devcontainer label, not in a
      // scanned workspace dir, etc.).
      showRescanToast(
        'no new sources (scanned ' + discovered + '): ' + (body.discovered || []).join(', '),
        'info'
      );
    }
  } catch (err) {
    showRescanToast('rescan error: ' + err, 'error');
  } finally {
    btn.classList.remove('spinning');
    btn.disabled = false;
  }
}

function showRescanToast(msg, kind) {
  let host = document.getElementById('rescan-toast-host');
  if (!host) {
    host = document.createElement('div');
    host.id = 'rescan-toast-host';
    host.style.cssText = 'position:fixed;top:54px;right:14px;z-index:9999;display:flex;flex-direction:column;gap:6px;max-width:520px';
    document.body.appendChild(host);
  }
  const t = document.createElement('div');
  const palette = kind === 'ok'    ? 'background:#11201a;border:1px solid #1d4d3c;color:#7ddca7'
                : kind === 'error' ? 'background:#2a1212;border:1px solid #5a1818;color:#e06c6c'
                :                    'background:#16161b;border:1px solid #2a2a2a;color:#c9c9c9';
  t.style.cssText = palette + ';padding:8px 12px;border-radius:4px;font-size:11px;font-family:ui-monospace,monospace;line-height:1.5;word-break:break-all;cursor:pointer;box-shadow:0 6px 20px rgba(0,0,0,.4);transition:opacity .2s';
  t.textContent = msg;
  t.onclick = () => t.remove();
  host.appendChild(t);
  setTimeout(() => { t.style.opacity = '0'; setTimeout(() => t.remove(), 250); }, 8000);
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
    // Re-apply collapse state since the swap rebuilt the .kind-group elements.
    if (activeProject === '__all__') applyCollapsedStateForAllView();
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
  t.classList.add('active');
  activeProject = t.dataset.project;
  selectedEntityProject = null;
  closeLogStream();
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
// IDs of containers whose start/devup is in flight. Survives tab switches
// (closeLogStream resets _logContainerId but leaves this alone) so the
// pulsing-dot indicator stays visible while devcontainer up grinds away.
let _pendingStarts = new Set();

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
  // devcontainer up creates a NEW container on devcontainer.json hash drift
  // and orphans the old one (also: VS Code/Dev Containers does the same on
  // rebuild). Multiple containers can therefore share one projectRoot.
  // Pick the running one if any, else the most relevant: starting > exited.
  const matches = containers.filter(c => c.projectRoot === displayProject);
  if (!matches.length) { el.innerHTML = ''; return; }
  const ctr = matches.find(c => c.state === 'running')
           || matches.find(c => _pendingStarts.has(c.id))
           || matches[0];
  const running = ctr.state === 'running';
  // _pendingStarts survives tab switches and re-renders, so the pulsing
  // dot keeps animating while devcontainer up runs in the background.
  const isStarting = _pendingStarts.has(ctr.id) && !running;
  const stateClass = running ? 'running' : (isStarting ? 'starting' : (ctr.state || 'exited'));
  const stateLabel = isStarting ? 'starting'
    : (ctr.health && ctr.health !== 'healthy' && ctr.health !== '' ? ctr.state+' ('+ctr.health+')' : ctr.state);
  const ready = isContainerReady(ctr);
  // When the container has a known project root, "start" runs devcontainer
  // up \u2014 it creates if missing, starts if stopped, AND runs lifecycle
  // scripts (postCreate / postStart / postAttach). Plain docker start
  // skips all hooks. Label flips to "\u2191 up" so the user sees which path
  // will fire.
  // Architectural decision: devcontainers (containers with projectRoot)
  // are exclusively brought up via VS Code's "Reopen in Container" \u2014 it's
  // the only path that gets full lifecycle + the user-space port forwarding
  // VS Code owns. infra-mngmt offers stop + attach. Standalone services
  // should be managed via raw docker / docker compose / process-compose.
  const hasDev = !!ctr.projectRoot;
  const showActBtn = running || !hasDev;
  const actLabel = running ? '\u25a0 stop' : '\u25b6 start';
  const actFn   = running ? 'stop' : 'start';
  const actTitle = running ? 'Stop container'
    : 'docker start \u2014 daemon-level resume only; no devcontainer.json lifecycle';
  const actBtn = showActBtn
    ? '<button class="ov-ctr-btn" data-cid="' + ctr.id + '" data-act="' + actFn + '" title="' + actTitle + '" onclick="containerAction(this.dataset.cid,this.dataset.act)">' + actLabel + '</button>'
    : '';
  // VS Code button always-on for devcontainers. Backend dispatches:
  //   running   → attach to container (existing URI form)
  //   stopped   → "code --new-window <projectRoot>" so VS Code's Dev
  //               Containers extension prompts "Reopen in Container",
  //               which is the supported full-lifecycle start path.
  const vsClickable = ready || hasDev;
  const vsRef       = ctr.name || ctr.id;
  const vsAt        = ctr.workspaceFolder ? ' at ' + ctr.workspaceFolder : '';
  const vsTitle     = ready
    ? 'Attach VS Code (new window) to ' + vsRef + vsAt
    : (hasDev ? 'Open in VS Code — directly opens as a dev container (full lifecycle + port forwarding). No "Reopen in Container" prompt.' : 'waiting for container to be ready');
  const vsBtn = vsClickable
    ? '<button type="button" class="ov-ctr-btn" data-vsid="' + ctr.id + '" onclick="openVSCodeAttach(this.dataset.vsid)" title="' + vsTitle + '">VS Code</button>'
    : '';
  el.innerHTML =
    '<span class="ov-ctr-dot ' + stateClass + '"></span>' +
    '<span class="ov-ctr-label">' + stateLabel + '</span>' +
    actBtn +
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
    // Plain docker start path — only reached for non-devcontainer
    // auto-discovered containers (rare). Devcontainers are routed through
    // VS Code's "Reopen in Container" instead.
    const ctr = containers.find(c => c.id === id);
    if (ctr) ctr.health = null;
    _pendingStarts.add(id);
    openLogStream(id);
    renderContainerControls(selectedEntityProject || activeProject);
    try {
      const r = await fetch('/api/container/start?id='+encodeURIComponent(id), {method:'POST'});
      if (!r.ok) {
        const txt = (await r.text()).trim();
        showToast({title: 'start failed', body: txt || ('HTTP '+r.status)});
      }
    } catch(e) {
      showToast({title: 'start failed', body: e.message || String(e)});
    } finally {
      _pendingStarts.delete(id);
      await fetchContainers();
      renderContainerControls(selectedEntityProject || activeProject);
    }
  }
}

async function openVSCodeAttach(id) {
  try {
    const r = await fetch('/api/container/open-vscode?id='+encodeURIComponent(id), {method:'POST'});
    if (r.ok) return;
    const txt = await r.text();
    showToast({title:'VS Code launch failed', body: txt.trim() || ('HTTP '+r.status)});
  } catch (e) {
    showToast({title:'VS Code launch failed', body: e.message || String(e)});
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

{{with .Build}}
{{if or .BuildEpoch .Commit}}
<span class="build-chip" id="build-chip"
      title="commit {{if .Commit}}{{.Commit}}{{else}}(unknown){{end}}{{if .Dirty}}-dirty{{end}}{{if .VCSTime}} · committed {{.VCSTime}}{{end}}{{if .BuildEpoch}} · built {{.BuildEpoch}}{{end}}{{if .GoVersion}} · {{.GoVersion}}{{end}}"
      data-epoch="{{.BuildEpoch}}"
      data-commit="{{.Commit}}">
  <span class="build-chip-commit">{{if .Commit}}{{.Commit}}{{else}}local{{end}}{{if .Dirty}}+{{end}}</span>
  <span class="build-chip-when" id="build-chip-when">{{.BuildEpoch}}</span>
  <span class="build-chip-reload" id="build-chip-reload" aria-hidden="true">↻</span>
</span>
<script>
(function(){
  const chip = document.getElementById('build-chip');
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
})();
</script>
{{end}}
{{end}}
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
         data-scope="{{.Scope.Label}}"
         data-tool="{{entityTool .}}">
      <span class="kind-icon {{.Kind}}">{{kindIcon .Kind}}</span>
      <span class="entity-name">{{.Name}}</span>
      {{if $status}}<span class="runtime-badge {{$status.State}}" title="{{$status.State}}{{if $status.Process}} · {{$status.Instance}}/{{$status.Process}}{{end}}"></span>{{end}}
      {{$tool := entityTool .}}{{if $tool}}<span class="entity-tool-tag {{$tool}}" title="tool: {{$tool}}">{{toolIcon $tool}}</span>{{end}}
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
    {{with entityTool $e}}<span>tool: <span class="entity-tool-tag {{.}}">{{toolIcon .}} {{.}}</span></span>{{end}}
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
  <div class="preview-toolbar">
    <button class="promote-btn"
            hx-get="/partials/promote-picker?id={{$e.ID}}"
            hx-target="#promote-slot"
            hx-swap="innerHTML"
            hx-disabled-elt="this">promote →</button>
  </div>
  <div id="promote-slot" class="promote-slot"></div>
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
    <span class="svc-icon" title="network bridges">⇆</span>
    <span class="svc-name">network bridges</span>
    <span class="svc-endpoint">portproxy + firewall (persistent state)</span>
    <span class="svc-running-count">{{activeBridgeCount .Bridges}}/{{len .Bridges}} active</span>
    <button class="svc-boot-btn"
            hx-post="/bridges/apply"
            hx-target="#services-inner"
            hx-swap="outerHTML"
            title="re-apply every Windows-tier bridge in one UAC prompt">▶ apply all</button>
    <button class="svc-boot-btn"
            hx-post="/bridges/refresh"
            hx-target="#services-inner"
            hx-swap="outerHTML"
            title="reload bridges.yaml from disk and re-snapshot state">⟳ refresh</button>
  </div>
  <table class="process-table">
    <thead><tr>
      <th class="col-name">bridge</th><th>tier</th><th class="col-state">state</th><th>listen</th><th>connect</th><th class="col-actions"></th>
    </tr></thead>
    <tbody>
    {{range .Bridges}}
    {{if eq .Kind "composite"}}
    <tr class="bridge-composite" data-composite="{{.Name}}" onclick="toggleCompositeMembers('{{.Name}}', event)">
      <td class="col-name">
        <span class="bridge-disclosure">▸</span>
        <span class="proc-name">{{.Name}}</span>
        {{if .Description}}<div class="proc-ns">{{.Description}}</div>{{end}}
      </td>
      <td>composite</td>
      <td class="col-state"><span class="status-pill {{.StateClass}}"><span class="dot"></span>{{.State}}</span></td>
      <td class="bridge-composite-summary" colspan="2">{{len .Members}} member{{if ne (len .Members) 1}}s{{end}}</td>
      <td class="col-actions">
        <div class="proc-actions">
          <button class="proc-btn start"
                  hx-post="/bridge/apply?name={{.Name}}"
                  hx-target="#services-inner" hx-swap="outerHTML"
                  onclick="event.stopPropagation()"
                  title="bring composite to active state; skips UAC if Windows portproxy is already in place">apply</button>
          <button class="proc-btn restart"
                  hx-post="/bridge/pause?name={{.Name}}"
                  hx-target="#services-inner" hx-swap="outerHTML"
                  onclick="event.stopPropagation()"
                  title="pause: stop WSL relay + remove Windows portproxy. Smart-skips elevation if Windows side is already inactive; otherwise UAC."
                  hx-confirm="Pause composite {{.Name}}? Removing the Windows portproxy will request elevation if the rule is currently in place.">pause</button>
          <button class="proc-btn stop"
                  hx-post="/bridge/reset?name={{.Name}}"
                  hx-target="#services-inner" hx-swap="outerHTML"
                  onclick="event.stopPropagation()"
                  hx-confirm="Reset composite {{.Name}}? This removes the netsh + firewall rules and will request elevation.">reset</button>
        </div>
      </td>
    </tr>
    {{$parentName := .Name}}
    {{range .Members}}
    <tr class="bridge-member" data-member-of="{{$parentName}}" hidden>
      <td class="col-name bridge-member-name">
        <span class="bridge-member-indent">└─</span>
        <span class="proc-name">{{.Name}}</span>
        {{if .DisplayName}}<div class="proc-ns">{{.DisplayName}}</div>{{end}}
      </td>
      <td>{{.Tier}}</td>
      <td class="col-state"><span class="status-pill {{.StateClass}}"><span class="dot"></span>{{.State}}</span></td>
      <td><code>{{.Listen}}</code></td>
      <td><code>{{.Connect}}</code></td>
      <td class="col-actions"></td>
    </tr>
    {{end}}
    {{else}}
    <tr>
      <td class="col-name">
        <div class="proc-name">{{.Name}}</div>
        {{if .DisplayName}}<div class="proc-ns">{{.DisplayName}}</div>{{end}}
      </td>
      <td>{{.Tier}}</td>
      <td class="col-state"><span class="status-pill {{.StateClass}}"><span class="dot"></span>{{.State}}</span></td>
      <td><code>{{.Listen}}</code></td>
      <td><code>{{.Connect}}</code></td>
      <td class="col-actions">
        <div class="proc-actions">
          <button class="proc-btn start"
                  hx-post="/bridge/apply?name={{.Name}}"
                  hx-target="#services-inner" hx-swap="outerHTML">apply</button>
          {{if eq .Tier "wsl"}}
          <button class="proc-btn restart"
                  hx-post="/bridge/pause?name={{.Name}}"
                  hx-target="#services-inner" hx-swap="outerHTML"
                  title="pause: stop the relay process; reset to fully remove">pause</button>
          {{end}}
          <button class="proc-btn stop"
                  hx-post="/bridge/reset?name={{.Name}}"
                  hx-target="#services-inner" hx-swap="outerHTML"
                  hx-confirm="Remove bridge {{.Name}}?">reset</button>
        </div>
      </td>
    </tr>
    {{end}}
    {{end}}
    </tbody>
  </table>
</div>
{{end}}
{{if .Containers}}
<div class="svc-instance containers">
  <div class="svc-header">
    <span class="svc-icon" title="docker containers">⬢</span>
    {{if .Docker.Configured}}
    <span class="online-dot {{if .Docker.Online}}online{{else}}offline{{end}}"
          title="docker daemon {{if .Docker.Online}}reachable{{else}}unreachable{{if .Docker.Error}} — {{.Docker.Error}}{{end}}{{end}}"></span>
    {{end}}
    <span class="svc-name">containers</span>
    <span class="svc-endpoint">{{if .Docker.Endpoint}}docker: {{.Docker.Endpoint}}{{else}}docker: matched by name{{end}}</span>
    <span class="svc-running-count">{{runningContainerCount .Containers}}/{{len .Containers}} running</span>
    <button class="svc-boot-btn"
            hx-post="/containers/refresh"
            hx-target="#services-inner"
            hx-swap="outerHTML"
            title="reload containers.yaml from disk and re-snapshot state">⟳ refresh</button>
  </div>
  <table class="process-table">
    <thead><tr>
      <th class="col-name">container</th><th class="col-state">state</th><th>status</th><th>cpu</th><th>mem</th><th>id</th><th class="col-actions"></th>
    </tr></thead>
    <tbody>
    {{range .Containers}}
    <tr>
      <td class="col-name">
        <div class="proc-name">{{.Name}}</div>
        {{if .Description}}<div class="proc-ns">{{.Description}}</div>{{end}}
      </td>
      <td class="col-state"><span class="status-pill {{.StateClass}}"><span class="dot"></span>{{.State}}</span></td>
      <td>{{if .Status}}<code>{{.Status}}</code>{{else}}—{{end}}</td>
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
      <td>{{if .ID}}<code>{{.ID}}</code>{{else}}—{{end}}</td>
      <td class="col-actions">
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
    <span class="svc-icon" title="process-compose instance">⚙</span>
    <span class="online-dot {{if $iv.Online}}online{{else}}offline{{end}}"></span>
    <span class="svc-name">{{$iv.Name}}</span>
    <span class="svc-endpoint">{{$iv.Endpoint}}</span>
    {{if $iv.Online}}
    <span class="svc-running-count">{{runningCount $iv.Processes}}/{{len $iv.Processes}} running</span>
    <button class="svc-boot-btn"
            data-toggle="show-internals"
            onclick="toggleShowInternals()"
            title="show internal bridge-* processes for debugging">show internals</button>
    <button class="svc-boot-btn"
            hx-post="/compose/reload?instance={{$iv.Name}}"
            hx-target="#services-inner"
            hx-swap="outerHTML"
            title="re-read compose YAML and reconcile (drops removed entries on recent process-compose versions)">⟳ refresh</button>
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
      <th class="col-name">process</th><th class="col-state">status</th><th class="col-pid">pid</th><th>restarts</th><th>cpu</th><th>mem</th><th>age</th><th class="col-actions"></th>
    </tr></thead>
    <tbody>
    {{range $iv.Processes}}
    <tr{{if isInternalProcess .}} class="proc-internal"{{end}}>
      <td class="col-name">
        <div class="proc-name">{{.Name}}</div>
        {{if and .Namespace (ne .Namespace "default")}}<div class="proc-ns">{{.Namespace}}</div>{{end}}
      </td>
      <td class="col-state">
        <span class="status-pill {{statusClass .Status}}"><span class="dot"></span>{{.Status}}</span>
        {{/* Health pill is redundant when status=Running + health=Ready
             (the common happy path). Show it only when it adds info:
             probe configured AND (not ready OR not running). */}}
        {{if and .HasHealthProbe (or (ne .Health "Ready") (not .IsRunning))}}<span class="health-pill {{healthClass .Health}}" title="readiness: {{.Health}}">{{.Health}}</span>{{end}}
        {{if and (not .IsRunning) (ne .ExitCode 0)}}<span class="exit-code" title="exit code">{{.ExitCode}}</span>{{end}}
      </td>
      <td class="col-pid">{{if .Pid}}{{.Pid}}{{else}}—{{end}}</td>
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
      <td class="col-actions">
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
    <button class="svc-boot-btn"
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

// llamaPageHTML is the full body of the standalone "llama" view. Each
// configured llama-server gets its own card; in router mode the card holds a
// list of model tiles, each with its own per-model slot grid + metrics.
//
// The whole tree re-renders every 10s — see the wrapper hx-get on #view-llama.
// 10s is the safe cadence for router-mode probes against models actively
// decoding (per-call latency can spike to several seconds while waiting for
// the inference mutex between decode steps).
// Re-rendering the entire view is fine: the data is small (one HTTP call per
// server, plus one per loaded model) and avoids the partial-state bookkeeping
// of granular swaps.
const llamaPageHTML = `
{{if not .Servers}}
<div class="llama-page-empty">
  no <code>llama_servers</code> declared in config.yaml — add at least one entry to populate this view
</div>
{{else}}
<div class="llama-grid">
{{range .Servers}}
<div class="llama-card">
  <div class="llama-head">
    <span class="llama-card-name" title="process-compose instance / process — matches the row in the services view">{{.Instance}} / {{.Process}}</span>
    <span class="llama-endpoint" title="base URL probed every 10s; /health, /props, /v1/models, /metrics, /slots">{{.Endpoint}}</span>
    {{if .HealthErr}}
      <span class="llama-pill bad" title="GET /health failed: {{.HealthErr}}">unreachable</span>
    {{else if .Health.OK}}
      <span class="llama-pill good" title="GET /health → 200 {status: ok}">healthy</span>
    {{else}}
      <span class="llama-pill warn" title="GET /health returned a non-200 status — server is alive but not ready">{{.Health.Status}}</span>
    {{end}}
    {{if .HasProps}}
      {{if .Props.IsSleeping}}<span class="llama-pill warn" title="--sleep-idle-seconds elapsed; model unloaded from VRAM. KV-cache metrics will be 0 until next request reloads it.">sleeping</span>{{end}}
      {{if .Router}}<span class="llama-pill mode" title="multi-model router detected via /v1/models[].status — /metrics and /slots are proxied to a child process selected by ?model=">router</span>{{end}}
      {{if .Props.BuildInfo}}<span class="llama-build" title="llama.cpp build identifier from /props.build_info — format b<num>-<commit>; useful for spotting drift across tiers">{{.Props.BuildInfo}}</span>{{end}}
    {{end}}
  </div>

  {{if .HasProps}}
  <div class="llama-identity">
    {{if .Props.ModelPath}}{{if ne .Props.ModelPath "none"}}<div title="from /props.model_path — full path to the loaded GGUF on the server's filesystem"><span class="lbl">model</span> <code>{{.Props.ModelPath}}</code></div>{{end}}{{end}}
    {{if .Props.TotalSlots}}<div title="from /props.total_slots — concurrent generation slots, set at startup via -np/--parallel and fixed for process lifetime"><span class="lbl">slots</span> {{.Props.TotalSlots}}</div>{{end}}
    {{if .Props.NCtx}}<div title="from /props.default_generation_settings.n_ctx — per-slot context budget; total_ctx ≈ n_ctx × total_slots"><span class="lbl">n_ctx</span> {{.Props.NCtx}}</div>{{end}}
  </div>
  {{else if .PropsErr}}
  <div class="llama-row-err">props: {{.PropsErr}}</div>
  {{end}}

  {{if .Router}}
    {{if .ModelsErr}}
    <div class="llama-row-err">models: {{.ModelsErr}}</div>
    {{else if not .Models}}
    <div class="llama-row-hint">no models declared in router config</div>
    {{else}}
    <div class="llama-models">
      {{range .Models}}
      <div class="llama-model llama-model-{{or .Status "unknown"}}{{if .Failed}} failed{{end}}{{if .Synthetic}} synthetic{{end}}">
        <div class="llama-model-head">
          <span class="llama-model-status status-{{or .Status "unknown"}}" title="{{statusHelp .Status}}">{{or .Status "unknown"}}</span>
          <code class="llama-model-id" title="model id from /v1/models[].id — pass as ?model=&lt;urlencoded&gt; on /metrics and /slots&#10;{{.ID}}">{{.ID}}</code>
          {{if .Synthetic}}
            <span class="llama-pill mode" title="auto-injected by the router as a fallback when no [default] is defined in your preset INI. Has no --model arg so it can never launch — this is benign upstream behavior, not a config bug.">router default (synthetic)</span>
          {{else if .Failed}}
            <span class="llama-pill bad" title="upstream /v1/models reports status.failed=true with exit_code={{.ExitCode}}. NOTE: the router uses the same flag for 'preset failed to launch' AND 'LRU-evicted child didn't shut down cleanly' — the exit code is the only discriminator available in the JSON.&#10;&#10;Code {{.ExitCode}}: {{exitCodeHelp .ExitCode}}">failed (exit {{.ExitCode}})</span>
          {{end}}
        </div>
        {{if and .ModelPath (or .Failed (eq .Status "loaded") (eq .Status "sleeping"))}}
        <div class="llama-model-path" title="value of --model from /v1/models[].status.args — verify the file exists at this path on the llama-server host">
          <span class="lbl">model file</span>
          <code>{{.ModelPath}}</code>
        </div>
        {{end}}
        {{if or (eq .Status "loaded") (eq .Status "sleeping")}}
          {{if .Metrics.Available}}
          {{template "llama-metrics-row" .Metrics}}
          {{else if .MetricsErr}}
          <div class="llama-row-err">metrics: {{.MetricsErr}}</div>
          {{else}}
          <div class="llama-row-hint">metrics endpoint disabled — start llama-server with <code>--metrics</code> to enable</div>
          {{end}}

          {{if .SlotsDisabled}}
          <div class="llama-row-hint">slots endpoint disabled — start without <code>--no-slots</code> to enable</div>
          {{else if .SlotsErr}}
          <div class="llama-row-err">slots: {{.SlotsErr}}</div>
          {{else if .Slots}}
          {{template "llama-slot-grid" .Slots}}
          {{end}}
        {{end}}
      </div>
      {{end}}
    </div>
    {{end}}
  {{else}}
    {{if .Metrics.Available}}
    {{template "llama-metrics-row" .Metrics}}
    {{else if .MetricsErr}}
    <div class="llama-row-err">metrics: {{.MetricsErr}}</div>
    {{else}}
    <div class="llama-row-hint">metrics endpoint disabled — start llama-server with <code>--metrics</code> to enable</div>
    {{end}}

    {{if .SlotsDisabled}}
    <div class="llama-row-hint">slots endpoint disabled — start without <code>--no-slots</code> to enable</div>
    {{else if .SlotsErr}}
    <div class="llama-row-err">slots: {{.SlotsErr}}</div>
    {{else if .Slots}}
    {{template "llama-slot-grid" .Slots}}
    {{end}}
  {{end}}

  <details class="llama-logs-section" data-llama-logs-key="{{.Instance}}|{{.Process}}" ontoggle="onLlamaLogsToggle(this)">
    <summary title="last 150 lines from the process-compose log buffer for {{.Instance}}/{{.Process}} — refreshes with the panel every 10s. Open/closed state is persisted across panel refreshes via localStorage.">
      <span class="lbl">logs</span>
      <span class="llama-logs-count">{{len .Logs}}{{if .LogsErr}} · err{{end}}</span>
    </summary>
    {{if .LogsErr}}
    <div class="llama-row-err">logs: {{.LogsErr}}</div>
    {{else if .Logs}}
    <div class="llama-logs-body">
      {{range .Logs}}<div class="llama-log-line">{{if .Time}}<span class="llama-log-time">{{.Time}}</span>{{end}}{{.Message}}</div>{{end}}
    </div>
    {{else}}
    <div class="llama-logs-empty">no log lines yet</div>
    {{end}}
  </details>
</div>
{{end}}
</div>
{{end}}

{{define "llama-metrics-row"}}
<div class="llama-stats">
  <div class="llama-stat" title="token-weighted prompt-eval throughput averaged over ALL completions since process start — NOT a rolling window. Stale-when-idle: keeps showing the historical mean indefinitely. (llamacpp:prompt_tokens_seconds gauge = Σtokens / Σms)">
    <span class="lbl">prompt</span>
    <span class="val">{{formatTokensPerSec .PromptTokensPerSec}}</span>
  </div>
  <div class="llama-stat" title="token-weighted generation throughput averaged over ALL completions since process start — NOT a rolling window. Updates only when a generation finishes (slot::release). For current speed, derive via PromQL rate() on the *_total counters. (llamacpp:predicted_tokens_seconds gauge = Σtokens / Σms)">
    <span class="lbl">predict</span>
    <span class="val">{{formatTokensPerSec .PredictedPerSec}}</span>
  </div>
  <div class="llama-stat" title="active requests (llamacpp:requests_processing gauge)">
    <span class="lbl">in flight</span>
    <span class="val">{{printf "%.0f" .RequestsProcessing}}</span>
  </div>
  <div class="llama-stat" title="queued waiting for a free slot (llamacpp:requests_deferred gauge)">
    <span class="lbl">deferred</span>
    <span class="val {{if gt .RequestsDeferred 0.0}}warn{{end}}">{{printf "%.0f" .RequestsDeferred}}</span>
  </div>
  <div class="llama-stat" title="avg busy slots per llama_decode() call (llamacpp:n_busy_slots_per_decode counter — closer to total_slots = better batch packing)">
    <span class="lbl">batch util</span>
    <span class="val">{{printf "%.2f" .NBusySlotsPerDecode}}</span>
  </div>
  <div class="llama-stat" title="cumulative since process start (llamacpp:tokens_predicted_total counter — resets on restart)">
    <span class="lbl">decoded</span>
    <span class="val">{{formatCount .TokensPredictedTotal}}</span>
  </div>
</div>
{{end}}

{{define "llama-slot-grid"}}
<div class="llama-slots">
  {{range .}}
  <div class="llama-slot {{if .IsProcessing}}busy{{else}}idle{{end}}">
    <div class="llama-slot-head">
      <span class="llama-slot-id" title="slot index from /slots[].id">slot #{{.ID}}</span>
      {{if .IsProcessing}}
        <span class="llama-slot-state busy" title="slot is actively decoding">busy</span>
        <span class="llama-slot-task" title="server-assigned task id from /slots[].id_task — useful for correlating with logs">task {{.IDTask}}</span>
      {{else}}
        <span class="llama-slot-state idle" title="slot is free; next request will land here">idle</span>
      {{end}}
      {{if .Speculative}}<span class="llama-slot-tag spec" title="speculative decoding active for this slot — draft model is generating candidate tokens that the main model verifies in parallel">spec</span>{{end}}
    </div>

    {{if .IsProcessing}}
    <div class="llama-slot-progress" title="decoded / total — total = n_decoded + n_remain. Progress within the per-task n_predict budget, NOT the slot's n_ctx.">
      <div class="llama-slot-bar">
        <div class="llama-slot-fill" style="width:{{slotProgress .Progress.NDecoded .Progress.NRemain}}%"></div>
      </div>
      <div class="llama-slot-progress-label">
        <span>{{.Progress.NDecoded}} / {{add .Progress.NDecoded .Progress.NRemain}} tok</span>
        <span>{{slotProgress .Progress.NDecoded .Progress.NRemain}}%</span>
      </div>
    </div>
    {{end}}

    <dl class="llama-slot-detail">
      {{if .IsProcessing}}
      <dt title="sampling temperature — higher = more diverse, 0 = greedy">temperature</dt>
      <dd>{{printf "%.2f" .Params.Temperature}}</dd>

      {{if gt .Params.TopK 0}}
      <dt title="top_k — keep only the K most-likely candidates before sampling">top_k</dt>
      <dd>{{.Params.TopK}}</dd>
      {{end}}

      {{if and (gt .Params.TopP 0.0) (lt .Params.TopP 1.0)}}
      <dt title="top_p (nucleus) — keep candidates whose cumulative probability ≤ p">top_p</dt>
      <dd>{{printf "%.2f" .Params.TopP}}</dd>
      {{end}}

      {{if gt .Params.MinP 0.0}}
      <dt title="min_p — drop candidates whose probability < p × max_prob">min_p</dt>
      <dd>{{printf "%.2f" .Params.MinP}}</dd>
      {{end}}

      {{if gt .Params.NPredict 0}}
      <dt title="n_predict — max generation budget for this task (params.n_predict)">budget</dt>
      <dd>{{.Params.NPredict}} tok{{if gt .Params.NKeep 0}} · keep {{.Params.NKeep}}{{end}}</dd>
      {{end}}

      {{if .Params.ChatFormat}}
      <dt title="chat_format — prompt template applied by the server">format</dt>
      <dd>{{.Params.ChatFormat}}{{if .Params.ReasoningFormat}} <span class="muted">· reasoning={{.Params.ReasoningFormat}}</span>{{end}}</dd>
      {{end}}

      {{if and .Params.SpeculativeType (ne .Params.SpeculativeType "none")}}
      <dt title="speculative-decoding strategy">spec</dt>
      <dd>{{.Params.SpeculativeType}}</dd>
      {{end}}
      {{end}}

      <dt title="per-slot context budget from /slots[].n_ctx — total context across all slots ≈ n_ctx × total_slots">n_ctx</dt>
      <dd>{{.NCtx}}</dd>
    </dl>
  </div>
  {{end}}
</div>
{{end}}
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

// promotePickerHTML renders the target-picker modal. The whole modal lives
// inside #promote-slot but uses position:fixed so it escapes the preview
// pane's box and overlays the viewport. Clicking the backdrop or pressing
// Esc closes it (the inline script wires those up).
const promotePickerHTML = `
{{$e := .Entity}}
<div class="promote-modal" id="promote-modal"
     onclick="if(event.target===this){document.getElementById('promote-close-btn').click();}">
  <div class="promote-modal-card" role="dialog" aria-label="Promote entity">
    <header class="promote-modal-head">
      <div class="promote-modal-titles">
        <div class="promote-modal-title">copy <span class="promote-kind">{{$e.Kind}}</span> <strong>{{$e.Name}}</strong></div>
        <div class="promote-modal-sub">from <span>{{$e.Source}}</span></div>
      </div>
      <button id="promote-close-btn" class="promote-close"
              hx-get="/partials/promote-clear"
              hx-target="#promote-slot"
              hx-swap="innerHTML"
              aria-label="close">×</button>
    </header>

    <div class="promote-modal-body">
      <label class="promote-rename">
        <span class="promote-rename-label">name at target</span>
        <input id="promote-rename-input" type="text" name="name"
               value="{{.SuggestedName}}"
               class="promote-rename-input" autocomplete="off" spellcheck="false">
      </label>
      <div class="promote-mirror-row">
        <label class="promote-mirror">
          <input id="promote-mirror-input" type="checkbox" name="mirror" value="true">
          <span>mirror copy</span>
          <span class="promote-mirror-info" tabindex="0"
                aria-label="What is mirror copy?"
                data-tooltip="Without mirror, copying overwrites overlapping files but leaves anything else at the target untouched. With mirror, the target entity is wiped first so it ends up as an exact reflection of the source — useful when a skill's source no longer contains a script or template that the target still has. Single-file kinds (command, agent, memory, claude_md, mcp_server, hook) are already fully replaced on copy, so the flag is a no-op for them.">i</span>
        </label>
        <div class="promote-mirror-hint">
          replace the target with an exact copy of the source — files at the target that aren't in the source get deleted.
          only meaningful for skills.
        </div>
      </div>

      {{if not .AnyTargets}}
      <div class="promote-empty">no other sources are configured — promote needs at least one second source as a target</div>
      {{else}}
      {{range .Groups}}
      {{if .Targets}}
      <section class="promote-group">
        <h3 class="promote-group-title">
          <span>{{.Title}}</span>
          <span class="promote-group-count">{{len .Targets}}</span>
        </h3>
        <ul class="promote-targets">
          {{range .Targets}}
          <li>
            <button class="promote-target {{if .ReadOnly}}readonly{{end}} {{if .Exists}}exists{{end}}"
                    {{if .ReadOnly}}disabled aria-disabled="true" title="this source is read-only"{{else}}
                    hx-post="/api/promote?from={{$e.ID}}&to={{.SourceID}}"
                    hx-include="#promote-rename-input, #promote-mirror-input"
                    hx-target="#promote-slot"
                    hx-swap="innerHTML"{{end}}>
              <div class="promote-target-main">
                <div class="promote-target-label">{{.Label}}</div>
                {{if .ProjectPath}}<div class="promote-target-path">{{.ProjectPath}}</div>{{end}}
              </div>
              <div class="promote-target-flags">
                {{with .Tool}}<span class="entity-tool-tag {{.}}" title="tool: {{.}}">{{toolIcon .}} {{.}}</span>{{end}}
                {{if .CrossTool}}<span class="promote-flag cross-tool" title="source and target are different tools">cross-tool</span>{{end}}
                {{if .Exists}}<span class="promote-flag exists">replaces</span>{{end}}
                {{if .ReadOnly}}<span class="promote-flag readonly">read-only</span>{{end}}
                {{if and (not .Exists) (not .ReadOnly)}}<span class="promote-flag new">new</span>{{end}}
              </div>
            </button>
          </li>
          {{end}}
        </ul>
      </section>
      {{end}}
      {{end}}
      {{end}}
    </div>
  </div>
</div>
<script>
(function(){
  const close = () => document.getElementById('promote-close-btn')?.click();
  const onKey = (e) => {
    if(e.key === 'Escape'){
      e.preventDefault();
      document.removeEventListener('keydown', onKey);
      close();
    }
  };
  document.addEventListener('keydown', onKey);
  // Auto-focus the rename input so power users can edit immediately.
  setTimeout(()=>{
    const i = document.getElementById('promote-rename-input');
    if(i){ i.focus(); i.select(); }
  }, 0);
})();
</script>
`

// promoteResultHTML renders success / conflict / read-only / generic-error
// fragments inside the modal shell so the user stays in context after the
// action. Conflict adds a confirm-overwrite button that re-POSTs with the
// preserved rename + overwrite=true.
const promoteResultHTML = `
<div class="promote-modal" id="promote-modal"
     onclick="if(event.target===this){document.getElementById('promote-close-btn').click();}">
  <div class="promote-modal-card promote-result-card {{if .OK}}ok{{else if .Conflict}}warn{{else}}error{{end}}"
       role="dialog" aria-label="Promote result">
    <header class="promote-modal-head">
      <div class="promote-modal-titles">
        <div class="promote-modal-title">
          {{if .OK}}<span class="promote-result-icon">✓</span> done{{end}}
          {{if .Conflict}}<span class="promote-result-icon">⚠</span> already exists{{end}}
          {{if .ReadOnly}}<span class="promote-result-icon">✖</span> read-only target{{end}}
          {{if and (not .OK) (not .Conflict) (not .ReadOnly)}}<span class="promote-result-icon">✖</span> error{{end}}
        </div>
        <div class="promote-modal-sub">{{.Message}}</div>
      </div>
      <button id="promote-close-btn" class="promote-close"
              hx-get="/partials/promote-clear"
              hx-target="#promote-slot"
              hx-swap="innerHTML"
              aria-label="close">×</button>
    </header>
    <div class="promote-modal-body promote-result-body">
      {{if .Conflict}}
      <p class="promote-result-prompt">overwrite the existing entity at the target?</p>
      <div class="promote-result-actions">
        <button class="promote-confirm"
                hx-post="/api/promote?from={{.From}}&to={{.To}}&overwrite=true&name={{.NewName}}{{if .Mirror}}&mirror=true{{end}}"
                hx-target="#promote-slot"
                hx-swap="innerHTML">overwrite</button>
        <button class="promote-cancel"
                hx-get="/partials/promote-clear"
                hx-target="#promote-slot"
                hx-swap="innerHTML">cancel</button>
      </div>
      {{else}}
      <div class="promote-result-actions">
        <button class="promote-cancel"
                hx-get="/partials/promote-clear"
                hx-target="#promote-slot"
                hx-swap="innerHTML">close</button>
      </div>
      {{end}}
    </div>
  </div>
</div>
<script>
(function(){
  const onKey = (e) => {
    if(e.key === 'Escape'){
      e.preventDefault();
      document.removeEventListener('keydown', onKey);
      document.getElementById('promote-close-btn')?.click();
    }
  };
  document.addEventListener('keydown', onKey);
})();
</script>
`
