# Frontend architecture & guidelines

Status: **in effect — M1–M5 + M7 of §16 have shipped; M6 deferred**. Sections marked **(now)** describe the present codebase; **(target)** describes where we are headed (only M6 remains); **(rule)** is binding for new code regardless of where the surrounding files sit today.

This is the canonical reference for anyone touching [internal/web/](../internal/web/). It's referenced from [CLAUDE.md](../CLAUDE.md) and [README.md](../README.md); changes to the frontend layout, asset pipeline, interaction model, or visual language must update this doc in the same change. It exists because the frontend crossed the line where ad-hoc decisions compound, and because we explicitly chose *not* to adopt a SPA framework — a choice that only pays off if the discipline below is followed.

---

## 1. Goals and non-goals

### 1.1 Goals

- **Server-rendered, HTML-over-the-wire.** The Go backend is the source of truth. Pages are projections of server state; the browser is a thin viewport with localized interactivity.
- **Single static binary.** No Node toolchain, no build step, no postinstall. `make build` continues to produce `dist/infra-mngmt` and that file is the entire deliverable.
- **Offline-capable.** Every JS, CSS, and font asset ships inside the binary via `embed.FS`. The tool must work on a host with no public-internet egress.
- **Editable by a backend engineer.** Anyone who can write Go must be able to modify any page in the UI without learning a frontend framework.
- **Auditable.** A reader should be able to look at one page's HTML, one CSS file, and one JS module and know exactly what runs.

### 1.2 Non-goals

- Mobile-first or responsive design beyond "doesn't break at laptop widths."
- SEO, social previews, accessibility certification (WCAG AA is aspirational, not gated).
- Multi-user theming, i18n, RTL layouts.
- Replacing HTMX with React, Vue, or Svelte. See §13 for the narrow circumstances under which a JS island is permitted.

### 1.3 Design philosophy

The product surface is dark, monospace, terminal-adjacent. Information density is high — the user is babysitting infrastructure and wants to see everything at once. No chrome: no rounded cards with generous padding, no hero sections, no progressive disclosure of essential state. When a row needs to expand into a deeper view (composite bridges, log streams, vault tree), it expands **in place** rather than navigating away. Loading is invisible by default; the UI animates only to confirm an action was received (spinning rotation on rescan, pulsing dot during devcontainer up). Color reinforces, never substitutes — every status surface pairs a colored dot or pill with a text label, so a red-green color-blind user can still tell `running` from `error` from the label alone.

The visual tokens (§5.1), component vocabulary (§5.4), and UX patterns (§7.6–§7.9) enforce this philosophy. Don't introduce a new color, glyph, button shape, or interaction surface without checking whether an existing one fits.

---

## 2. Stack

| Layer | Choice | Notes |
|---|---|---|
| Templating | Go [`html/template`](https://pkg.go.dev/html/template) | Auto-escaping is mandatory; never bypass with `template.HTML` except for vetted SVG icons. |
| Partial swaps | [HTMX 2.x](https://htmx.org) | Vendored, served from `/static/vendor/htmx.min.js`. |
| Live streams | Server-Sent Events | Plain `text/event-stream` over the existing chi router. WebSockets are not used. |
| Markdown | [marked](https://marked.js.org) | Vendored. Used only for entity preview. |
| Search | [Fuse.js](https://www.fusejs.io) | Vendored. Client-side only because the entity set is small (< 1k items). |
| Editor | [CodeMirror 6](https://codemirror.net) | Vendored. Loaded lazily; no other page may depend on it. |
| Icons | Inline SVG | No icon font, no sprite sheet, no external set. |
| Styling | Hand-written CSS with custom properties | No Tailwind, no CSS-in-JS, no PostCSS. |

The server is `net/http` + chi, configured in [internal/web/server.go](../internal/web/server.go). The router applies `middleware.Compress(5)` to gzip responses; chi compresses only its `text/*` + `application/{json,javascript,…}` allowlist, so `text/event-stream` (the container-events SSE) is left untouched and keeps streaming (biggest win is the CodeMirror bundle on the preview route). Static assets are served from a single `embed.FS` rooted at `internal/web/static/`.

---

## 3. File layout

### 3.1 Now

The §3.2 layout is reality: templates in `templates/*.html.tmpl` (parsed via `embed.FS`), CSS in `static/css/app.css`, JS as ES modules under `static/js/`, vendored assets in `static/vendor/`. There is no `internal/web/template.go` — the old raw-string constants were removed in M4. CDN references are gone everywhere except `cmd/vendor-codemirror/` (a build-time bundler, not in the production binary).

### 3.2 Current layout (achieved by M1–M5 + M7)

```
internal/web/
  server.go               // router, middleware, asset mount
  handlers.go             // request handlers, FuncMap definition
  bridges.go              // feature-specific handlers
  containers.go
  llama.go
  vault.go
  promote.go
  status.go
  templates/              // *.html.tmpl, embed.FS rooted here
    index.html.tmpl       // full page shell; composed with entity_list at render time
    entity_list.html.tmpl // partial, returned from /partials/entity-list
    preview.html.tmpl     // entity preview partial
    services.html.tmpl    // services view; several {{define}} section shells (see §7.10)
    logs.html.tmpl
    llama.html.tmpl       // llama view; per-card {{define}} shells (see §7.10)
    login.html.tmpl
    promote_picker.html.tmpl
    promote_result.html.tmpl
    container_controls.html.tmpl
    vault_panel.html.tmpl
    open_design_token_stats.html.tmpl
    open_design_version.html.tmpl
  static/
    css/
      app.css             // single shared stylesheet — tokens, layout, components
    js/
      app.js              // bootstrap: htmx config, view switch, search, toasts
      preview.js          // markdown rendering, edit-in-place (CodeMirror)
      containers.js       // container panel: SSE events, controls
      promote.js          // promote modal
      wake.js             // WSL wake-on-idle island (gated on wake_url)
    vendor/
      htmx.min.js
      fuse.min.js
      marked.min.js
      codemirror.bundle.js
      LICENSES.md         // attribution for every vendored file
```

### 3.3 Rules

- **(rule)** No HTML, CSS, or JS may live inside a `.go` file. The only exception is small SVG icon constants used by `template.HTML` FuncMap helpers (see [handlers.go:351](../internal/web/handlers.go#L351)).
- **(rule)** Every template file ends in `.html.tmpl` and is parsed via `embed.FS` + `template.ParseFS`. Globbing is fine; explicit lists are clearer for partials that are returned standalone.
- **(rule)** Every static asset is served from a single chi `Handle("/static/*", http.FileServer(http.FS(staticFS)))` mount. Never construct asset URLs by hand in templates — define `{{static "app.css"}}` in the FuncMap so cache-busting can be added in one place.
- **(rule)** No CDN references in production code. Vendored copies only. A vendored asset's version is recorded in `static/vendor/LICENSES.md` along with the upstream source URL.

---

## 4. Templates

### 4.1 Composition model

We use Go template composition, not concatenation:

- There is **no separate layout file**. `index.html.tmpl` is the full page shell (`<html>`/`<head>`/header/view container/footer); handlers compose it with `entity_list.html.tmpl` at render time — `parseTemplate("index", "templates/index.html.tmpl", "templates/entity_list.html.tmpl")`.
- HTMX partials returned to the client are *whole files* under `templates/` (`preview`, `promote_picker`, `promote_result`, `container_controls`, `open_design_*`) — the response shape is obvious from the filename.
- **Deliberate exception — the polling views:** `services.html.tmpl` and `llama.html.tmpl` each hold several `{{define}}` section shells, one per self-polling endpoint (see §7.10). Co-locating a view's sections in one file is intentional; the rule is only *don't scatter `{{define}}` blocks across **unrelated** files*.
- **Parsed templates are cached.** `parseTemplate()` memoizes the parsed `*template.Template` by file-set key in `tmplCache` ([templates.go](../internal/web/templates.go)). The templates live in an immutable `embed.FS` and the FuncMap is stateless, so the parsed set is safe to reuse across concurrent requests — the self-polling section endpoints don't re-parse the full set (incl. the ~550-line services template) on every tick.

### 4.2 FuncMap

The `tmplFuncs` map ([handlers.go:317](../internal/web/handlers.go#L317)) is a curated surface. Keep it small and documented. Security-relevant helpers worth knowing: `static` (cache-busted asset URLs — never hand-build them), `qesc` (`url.QueryEscape` for user-controlled values in `hx-get`/`hx-post` query strings), and the `template.HTML` icon helpers (whose inputs must be trusted constants).

- **(rule)** A function added to `tmplFuncs` must have a unit test in [template_test.go](../internal/web/template_test.go) covering its happy path and the empty/zero input.
- **(rule)** A FuncMap helper that returns `template.HTML` must escape any caller-supplied content explicitly. Never feed user-mutable data through `template.HTML`.
- **(rule)** Formatters (`formatMem`, `formatCount`, `formatTokensPerSec`, …) are pure functions and must be unit-tested without spinning up a template.
- **(rule)** Handlers execute templates via `renderTmpl(w, tmpl, data)` ([templates.go:53](../internal/web/templates.go#L53)), never `tmpl.Execute` directly. It logs execution errors instead of silently swallowing them — HTMX partials have already written their status by Execute time, so a late error can't change the response but must still leave a trace in the log.

### 4.3 Escaping

- **(rule)** Auto-escaping stays on. Untrusted input (entity names, source IDs, file paths, log lines) is rendered with `{{.}}` only.
- **(rule)** The only places `template.HTML` is acceptable: vendored SVG icons defined as Go constants, or output produced by `marked` *on the client*. Never hand the client pre-rendered HTML produced from arbitrary file contents.

### 4.4 Data shape

- One handler → one view struct → one template. Don't pass `map[string]any` except when the data is truly heterogeneous (login form, ad-hoc errors).
- View structs live next to the handler that produces them, named `<page>View`. Use exported field names and explicit JSON tags only if the same struct also crosses an API boundary.

---

## 5. CSS

### 5.1 Tokens

`:root` in [`static/css/app.css`](../internal/web/static/css/app.css) is the design system. **Always reuse a token**; never introduce a new hex literal or inline color when one applies. Adding a new value means extending `:root` first.

**Background / text / border layers** — three depths of each:

| Token | Hex | Where |
|---|---|---|
| `--bg`   | `#0c0c0c` | Page background |
| `--bg2`  | `#141414` | Cards / panels |
| `--bg3`  | `#1c1c1c` | Buttons / inputs / preview body |
| `--bg4`  | `#222`    | Active tabs, hovered buttons |
| `--text`  | `#c9c9c9` | Primary content |
| `--text2` | `#989898` | Labels, secondary content, placeholder |
| `--text3` | `#5e5e5e` | De-emphasized (file paths, build chip when fresh) |
| `--white` | `#f0f0f0` | Logo, active emphasis |
| `--border`  | `#252525` | Default rule |
| `--border2` | `#2e2e2e` | Hover state, pill borders |

**Status colors** carry semantic weight; never repurpose:

| Token | Hex | Means |
|---|---|---|
| `--green`  | `#6bcf7f` | Running / active / OK |
| `--red`    | `#e06c6c` | Error / failed / stopped-with-error |
| `--yellow` | `#ffcc5c` | Warning / drifted / health "Not Ready" |
| `--orange` | `#f0a04a` | Attention / starting / degraded |
| `--accent` | `oklch(68% 0.18 200)` (cyan) | Primary action / focus / build chip stale |
| `--accent2`| `oklch(68% 0.18 302)` (magenta) | Project-scope badge contrast |

**Pill tints** — status-pill / badge backgrounds, the single source of truth for those surfaces: `--pill-running` / `--pill-stopped` / `--pill-error` / `--pill-warning` / `--pill-starting` / `--pill-muted` / `--pill-amber`. Reused by `.status-pill[state]`, `.mcp-status`, `.health-pill`, `.exit-code`, `.broken-ref-banner` — build a new status surface from these, never a fresh tint.

**Accent fills** — primary-button backgrounds: `--accent-fill` (6% accent tint, default) and `--accent-fill-hover` (14% on hover). Used by llama load, vault allow, and promote buttons.

**Kind colors** (entity-kind glyph + group pill outline) and **scope colors** (the `global`/`project`/`devcontainer` badge on each entity) live in `:root` as `--kind-{mcp,command,agent,skill,hook,memory,claude_md}` and `--level-{global,project,devcontainer}`. The hues are deliberately distinct so the eye can scan a mixed list by either dimension at a glance.

**Motion tokens** drive every transition + animation in the app:

| Token | Value | Where |
|---|---|---|
| `--dur-tap`  | `80ms`  | Button press feedback (`transform: scale(.96)` on `:active`) |
| `--dur-fast` | `120ms` | Hover, chevron rotate, expand-enter, `.row-hover` background fade |
| `--dur-base` | `180ms` | slideIn for entity cards, modal/promote enter |
| `--dur-slow` | `320ms` | Wide layout transitions (search input expand) |
| `--ease-out`    | `cubic-bezier(.2,.8,.2,1)`  | Default ease for hover + transition-out |
| `--ease-spring` | `cubic-bezier(.4,1.4,.4,1)` | Chevron rotate, "pop" emphasis on toggles |

**(rule)** New transitions and `@keyframes` reuse these tokens — never hand-type a duration or ease curve.

### 5.2 Naming

- Components are flat, BEM-ish: `.entity-card`, `.entity-card-header`, `.entity-card.is-collapsed`. No nesting selectors more than two levels deep.
- State classes are prefixed `is-` (`is-active`, `is-collapsed`, `is-loading`) — never set state via `style="display:none"` from JS for anything more than transient toggling.
- All component and layout CSS lives in the single `static/css/app.css`, loaded once on every page. There is no per-page stylesheet split today; if `app.css` grows unwieldy, split it by *component* into additional embedded files rather than a per-page-loaded scheme.

### 5.3 Rules

- **(rule)** No `!important` except in vendor overrides, with a comment naming the offending rule.
- **(rule)** No `style="..."` attributes for layout. Inline styles are acceptable only for genuinely dynamic values (CPU bar widths from server data, e.g. [handlers.go cpuBarWidth](../internal/web/handlers.go) — render via `{{cpuBarWidth . | printf "width:%s"}}` once, not piecemeal).
- **(rule)** Every new color, spacing, or radius value must reuse a token or extend `:root`. No hex literals scattered through component CSS.

### 5.4 Component vocabulary

When designing a new surface, **build it from these**. If you find yourself wanting something not listed, look harder — there's probably a component that fits, or a small extension of one. Adding a new component means updating this section in the same change.

| Component | Class(es) | Purpose |
|---|---|---|
| Status pill | `.status-pill` + state (`running` / `error` / `starting` / `stopped` / `degraded` / `disabled` / `unknown` / `ready` / `not-ready`) | Color-coded label, ~10 px text. Process status, container/bridge/vault state, llama health. State class comes from the `statusClass` FuncMap helper. |
| Status dot | `.dot` (inside `.status-pill`) / `.online-dot` (daemon-reachable) / `.ov-ctr-dot` (devcontainer) | 6 px circle, compact "is-it-up?" indicator paired with a surrounding label. |
| Kind icon | Inline glyph in `--kind-*` color | Entity-kind row prefix + services-section icon (`⇆` bridges, `⬢` containers, `▣` vaults, `◇` open-design, `⚙` process-compose instance). The vocabulary is intentionally small. |
| Pill button (kind filter) | `.pill` + active state | Outlined when inactive, tinted background when active (`color-mix(in srgb, var(--kind-X) 12%, transparent)`). Kind-filter bar above the entity list. |
| View tab | `.view-tab` + `.active` | Top-of-page tabs (`entities` / `services` / `llama`). Active gets `--bg4` + `--border2`. |
| Source tab | `.tab` + scope-badge children | Per-project tab strip; carries one or more `glb`/`prj`/`ctr` badges. |
| Two-line row | `.svc-row` / `.bridge-row` / `.svc-process` / `.llama-card` / `.vault-card-header` / `tr.project-header` + `tr.project-member` | Title or name on top (`--white`), description or endpoint below (`--text2`), pills/buttons fixed-width on the right. |
| Section header | `.svc-instance > .svc-header` with `.svc-name` + `.svc-endpoint` + `.svc-actions` | Each services-panel section follows: icon, name, endpoint/subtitle, then a right-aligned `.svc-actions` container with running-count + lifecycle buttons (`apply all` / `reload` / `start` / `restart` / `stop`). |
| Action button | `.svc-boot-btn` (section level) / `.proc-btn` (row level) + intent class (`start` / `restart` / `stop`) | Bordered, no background until hover. Intent class colors the hover state — green for start, yellow for restart, red for stop. The label is a verb (`apply`, `pause`, `reset`, `reload`, `start`, `stop`); intent color carries the signal so the glyph (▶ / ⟳ / ↻ / ■) is dropped. Mutating actions sit left of safe `reload`. |
| Card | `.svc-instance` + per-feature subclass (`.vaults` / `.open-design` / `.containers` / `.processes`) | `--bg2` rectangle, `--border` outline, 4 px radius, vertical density. Header on top, content below. Never use shadow — borders only. |
| Model chip | `.model-chip` (shared) / `.ods-chip` (OD-specific, has inner `name + out + cost` spans) | Pill with a colored dot + tinted background + tinted border, all driven by a single `--m: <oklch>` inline style. Used by the OD card chip strip and the llama view's per-model row so the same model id renders with the same hue across views. The dot uses `box-shadow: 0 0 4px var(--m)` for a subtle glow. Color is derived deterministically from the model id (`odColorFor` / `modelColor` FuncMap) via SHA-256 → hue; stays inside the 68% L / 0.18 C OKLCH plane so every chip lives in the same visual family. |
| Unreachable card | `.svc-unreachable` (alias: `.ods-down`) + `-warn` / `-warn-icon` / `-name` / `-explain` / `-strong` / `-action` children | Centered red-wash explainer (`background: rgba(224,108,108,.04)`) used when a section's backend is unreachable: OD compose project stopped, vault sidecar down, llama-server `/health` failing. Always carries: `⚠` icon + `<section-name> isn't reachable` line + small explainer pointing at where lifecycle lives. No retry button — section auto-refreshes on its parent poll (services 8s, llama 10s). |
| Modal | `#promote-slot` (today, the only one) | Position-fixed overlay with darkened backdrop. Backdrop click + Escape dismiss; first input auto-focuses on open. |
| Toast | `.toast` via `window.showToast({title, body, kind, timeout})` where `kind` is `error` (default) / `info` / `ok` | Bottom-right stack, slides in, auto-dismisses after ~8 s. Used for rescan results, container action failures (auto-toasted by the `htmx:responseError` listener), VS Code launch failures. |
| Build chip | `.build-chip` / `.build-chip.stale` | Bottom-right, always present, monospace 10 px. `<short-sha>+` (`+` if dirty) + relative time. Turns `--accent` with a reload affordance when the server's commit differs from the page's. |

### 5.5 Typography, spacing, radius

- **Typography**: `--font: ui-monospace, 'Cascadia Code', 'SF Mono', monospace;`, base 12 px. Everything is monospace; there is **no sans-serif** anywhere on the product surface (the `<title>` aside). Pills and small labels go 10–11 px; the logo and view tabs go 13 px. Don't introduce a new font.
- **Spacing**: padding values cluster around `2px`, `4px`, `6px`, `8px`, `12px`, `16px`. Vertical density is high — most rows are 22–28 px tall. If a section needs breathing room, use a 1 px border or a slightly different background (`--bg2` next to `--bg3`) before reaching for whitespace.
- **Radius**: `3px` on pills, `4px` on cards/inputs, `6px` on the login card. Nothing is fully rounded; nothing is sharp. Don't introduce new radii.

---

## 6. JavaScript

### 6.1 Module model

- **(target)** Every page-level script is an ES module. `app.js` is the only globally loaded module (bootstraps HTMX configuration, ⌘K search, view switching, build-chip polling). Page modules are loaded by the page templates with `<script type="module" src="/static/js/<page>.js">`.
- Modules export named functions; no implicit globals. The HTMX `htmx.config` and the `marked` global may be touched once, in `app.js`, to fix configuration.
- Communication between modules goes through the DOM (custom events on `document`) or HTMX (`htmx:afterSwap` listeners), never through ad-hoc shared globals like `window._foo`.

### 6.2 Rules

- **(rule)** No `onclick="..."` attributes in templates. Bind in the page module via `addEventListener`. Exception: HTMX `hx-on:click` is permitted because it's namespaced and audit-friendly.
- **(rule)** No `eval`, no `new Function()`, no string-templated HTML injected via `innerHTML` from user-mutable data. Build DOM nodes with `document.createElement` or render server-side and swap.
- **(rule)** Browser APIs only — no `npm` packages, no `import` from URLs in production code. CodeMirror is the one allowed exception and is loaded as a single pre-built bundle file.
- **(rule)** Any `EventSource` opened by a module must close itself on `pagehide` / view switch. The pattern in [containers.js closeLogStream](../internal/web/static/js/containers.js#L77) is correct; copy it.

### 6.3 What goes in app.js vs a page module

| In `app.js` (loaded everywhere) | In a page module (loaded only on its page) |
|---|---|
| HTMX config, error toast | Page-specific fetches |
| ⌘K search, fuse setup | Streaming consumers (SSE) |
| View tab switching, route sync | Editor instantiation (CodeMirror) |
| Build-chip stale-version polling | Anything that touches a chart or visualization |
| Toast notifications | Form-validation logic |

If something would only run for 5% of sessions, it does not belong in `app.js`.

---

## 7. Interaction model

There are exactly three interaction patterns. Picking the right one is the most important design choice on every new feature.

### 7.1 HTMX partial swap (default)

Use when: the user clicks/submits and the result is HTML the server can render. This is 80% of cases.

```html
<button hx-post="/api/sources/rescan"
        hx-target="#entity-list"
        hx-swap="innerHTML">rescan</button>
```

Server returns the rendered partial template. No JSON, no client-side state.

### 7.2 SSE stream

Use when: the data updates faster than 5 s, or the update is event-driven (Docker engine events, process-compose log tail). Implementation reference: [handlers.go handleContainerEventsStream](../internal/web/handlers.go#L988).

```js
const es = new EventSource('/api/container/events-stream?id=' + id);
es.addEventListener('docker_event', e => { /* update DOM */ });
es.addEventListener('done', () => es.close());
```

**(rule)** Every SSE endpoint sends a terminal `event: done` so clients can close cleanly. Server flushes after every event. Reconnection is the browser's default.

### 7.3 JSON fetch + manual DOM patch

Use when: HTMX swap would require a contortion (cross-frame DOM updates, optimistic UI), and SSE is overkill. Examples: container start/stop buttons that need to update three different parts of the page.

```js
const r = await fetch('/api/container/start?id=' + id, { method: 'POST' });
if (!r.ok) showToast({ kind: 'error', body: await r.text() });
```

**(rule)** A new JSON endpoint must be justified in the PR description. Default to HTMX. Reach for fetch() only when the answer to "could this be HTMX with a target?" is unambiguously no.

### 7.4 Polling

`hx-trigger="every 8s"` on a `<div>` whose content is the partial is acceptable for slow-changing aggregates. **(rule)** Polling intervals: 5 s minimum, 30 s preferred for non-critical surfaces. Anything faster must use SSE.

For composite panels (multiple sections, mixed update frequencies), don't poll the whole panel — use the per-section pattern in §7.10.

### 7.5 Forbidden

- Mixing HTMX swap with manual DOM patching of the swapped region. Pick one per region.
- Building HTML strings on the client and `innerHTML`-ing them. Either swap a server partial, or `createElement`.
- Long-poll loops (`while(true) { await fetch... await sleep }`). Use SSE or HTMX polling.

### 7.6 State indicators

- **Dot-and-label pairing.** Every status surface is `[colored dot or pill] [label text]`. The label always says the state in words. Color reinforces, never substitutes (see §1.3).
- **Counts as state.** Section headers carry "N/M" framing (`2/3 active`, `1/3 running`). When N is below M, the count adopts `--yellow` or `--orange` styling so it reads as needing attention at a glance.
- **The build chip** (`.build-chip`) is the only mid-session notification of a backend change. When the running binary's commit differs from the page's, the chip flips to `--accent` with a reload affordance. Don't add other persistent notification surfaces — this one is enough.
- **The reload button** (`.svc-boot-btn.restart` with label `reload`) spins via the `spin .7s linear infinite` animation while its request is in flight, stopping when the response lands. Same pattern for any in-flight indicator on a single button — animate the button itself, never a separate spinner.

### 7.7 Information density

Default to showing more, not less. The user came because their terminal/htop/`docker ps` output was too fragmented; the value here is consolidation. Specific patterns:

- **(rule) Collapse, don't navigate.** When there's too much to show at once, expand in place — composite bridges, log streams, vault `<details>`. Never push the user to a new page for "see more."
- **(rule) Persist collapse state.** A collapsable section's open/closed state survives HTMX swaps and page reloads via `localStorage.setItem('infra-mngmt:<feature>-state', …)`. Kind-group collapse, composite-row expand, llama-logs `<details>` all do this.
- **(rule) Hide zero-info widgets.** A row element that adds no information in some state must hide in that state. The `default` process-compose namespace is hidden; a `Running + Ready` process suppresses the redundant `Ready` pill (only `Running + Not Ready` keeps it); a devcontainer-only "start via VS Code" button hides on non-devcontainer rows.
- **(rule) Tooltips carry the depth.** Anywhere a value is compact (status pill, number, glyph), `title="…"` carries the longer explanation. The visible text is the *what*; the tooltip is the *why* (e.g. `■ stop` button → `title="halt the process and stop the restart loop"`).

### 7.8 Empty / loading / error surfaces

- **Empty states get a sentence**, not `(empty)`. The sentence names what the user could do next or explains the consequence: `no llama_servers declared in config.yaml — add at least one entry to populate this view`, `No paths allowed yet — agents can't read anything from this vault`.
- **Loading states for HTMX-loaded panels** use a one-line italic `--text3` placeholder: `loading vault…`, `loading services…`. No spinners for partial loads — partials are fast. The exception: a button whose action takes 1+ seconds animates its own glyph (see §7.6 reload button).
- **Three error surfaces, picked by user-flow context**:
  - **Inline error** (red banner at the top of a content region) for content-endpoint failures: `⚠ no matching process-compose process found`.
  - **Toast** for action failures: the `htmx:responseError` listener auto-toasts when an action endpoint (`/api/container/*`, etc.) returns ≥ 400. Same surface for VS Code launch failures, rescan errors.
  - **Modal** for action errors that need the user's continued context (promote conflict / read-only / generic error — all rendered in the same `#promote-slot` so the user sees the result in the same flow they took the action).
  - **(rule)** Don't introduce a fourth error surface; pick the one whose context matches the action.

### 7.9 No optimistic UI

We don't show optimistic state. The button greys out via `hx-disabled-elt="this"` during the round-trip, then the server's response renders the actual new state. This trades a brief frozen UI for a flicker-back if the action fails — worth the latency on a local-network tool. Don't add optimistic patches without a strong reason; the full state always lives on the server, the client never holds "this is starting" in JS.

### 7.10 Polling without flicker

The naive way to keep a composite panel live is to poll the whole thing every Ns with an `outerHTML` swap. This flickers visibly: every cycle the whole DOM is replaced, lazy-loaded children briefly disappear, CSS-animated cells re-animate.

The right pattern is **don't poll the composite**. Render a static shell once on view-reveal, and let each section self-poll its own endpoint. The services panel and llama view both follow this — see `handleServicesPartial` / `handleServicesContainers` / `handleServicesBridges` / etc. in [handlers.go](../internal/web/handlers.go) and `handleLlamaAll` / `handleLlamaCard` in [llama.go](../internal/web/llama.go).

**The pattern in five rules:**

**1. The composite endpoint renders a static shell.** No `hx-trigger="every Ns"` on the outer wrapper. Each section inside is a `<div>` placeholder with its own `hx-get="/partials/X" hx-trigger="load, every 8s" hx-swap="outerHTML"`.

```html
<!-- /partials/services renders this once on view-reveal -->
<div class="svc-grid" id="services-inner">
  {{if .HasContainers}}
  <div id="containers-section" class="svc-instance svc-section-loading"
       hx-get="/partials/services/containers"
       hx-trigger="load, every 8s"
       hx-swap="outerHTML">
    <p class="svc-section-placeholder">loading containers…</p>
  </div>
  {{end}}
  <!-- … more section placeholders … -->
</div>
```

**2. Each section endpoint returns its own wrapper with the same `hx-trigger`.** That keeps the polling alive across self-swaps. Don't put the trigger only on the shell placeholder — after the first swap, the new content replaces the trigger element with its own, so the wrapper must re-declare it.

```html
<!-- /partials/services/containers response -->
<div id="containers-section" class="svc-instance containers"
     hx-get="/partials/services/containers"
     hx-trigger="every 8s"
     hx-swap="outerHTML">
  …content…
</div>
```

**3. Lazy-loaded inner slots inside a section use `hx-preserve`.** Sub-elements that fetch their own data (OD usage body, version chip, snapshot timestamp, vault row body) carry `hx-preserve="true"` + a stable id so they survive the section's outerHTML swap. The section may re-render every 8 s, but its preserved children keep their loaded state — no flicker back to "loading…" twice a minute.

```html
<!-- inside open-design-card response -->
<div class="ods-body" id="ods-body-{{.Name}}"
     hx-preserve="true"
     hx-get="/partials/open-design/token-stats?name={{.Name}}"
     hx-trigger="load, every 8s"
     hx-swap="outerHTML">
  <div class="ods-body-loading">loading usage…</div>
</div>
```

**4. Reserve space + inline known states.** Set `min-height` on lazy-loaded elements that matches the loaded content (e.g., `.vault-row[open] > .vault-row-body { min-height: 120px }`) so the initial fetch doesn't grow the row. When the server already knows the final state at render time, render the final HTML inline instead of a loading placeholder — e.g., when the vault is unreachable on F5, the unreachable panel renders inline so the user never sees "loading vault…" flash.

**(rule)** An inline pre-render MUST be byte-identical to what the partial endpoint later returns (including cosmetic classes like `expand-enter`). When the first inner self-poll replaces the inline content, identical HTML produces an invisible swap. Add a comment cross-referencing the partial so they stay in sync.

**5. Action buttons target the right scope.** A button that only affects one section (`/bridge/apply?name=X` updating bridges only) can target `#bridges-section` directly. Buttons whose effect crosses sections (`/containers/refresh` re-reading containers.yaml, which can affect both the containers table and any composite cards) target `#services-inner` and the handler returns the shell — each section's placeholder then re-fetches. Brief "loading…" placeholders during the action are acceptable because actions are user-initiated and rare.

### 7.11 Per-section polling gotchas

- **Don't reach for ETag + 304 to "fix flicker on a polled composite."** It looks tempting (304 = skip the swap = no DOM churn) but is a misuse of HTTP caching that fights the browser. The browser auto-revalidates ETagged responses with `If-None-Match` on F5; htmx with `responseHandling[304] = swap:false` will then leave the page blank because the initial `revealed once` trigger gets 304 instead of body. Working around this requires `Cache-Control: no-store` + a JS-side ETag tracker — at which point you've reinvented the polling architecture in JS instead of just splitting the endpoint. The correct fix is per-section polling.
- **`hx-trigger="every Ns"` fires the first tick at T = N, not T = 0.** If you want immediate-then-periodic refresh, use `load, every Ns`. If the server pre-renders the content (inline known state), use just `every Ns` to suppress the redundant immediate fetch.
- **`hx-preserve` moves elements through a hidden "preserve-pantry" during swaps** (htmx 2.x). Modern Chrome uses native `moveBefore()` so the move is seamless; older browsers may detach and reattach, which fires `connectedCallback`, restarts CSS animations, and can briefly collapse height. The `min-height` reservation absorbs this. Firefox uses the same fallback path as older Chrome, so test there explicitly.
- **OOB swaps still update preserved elements.** `hx-preserve="true"` keeps an element across regular swaps but does NOT block `hx-swap-oob="true"` updates targeting the same id. Useful when an inner partial needs to push fresh data into a chip in the otherwise-stable outer chrome (e.g., the OD snapshot timestamp updated from the token-stats partial response).
- **Default htmx swap is `innerHTML`, not `outerHTML`.** CSS selectors like `.parent > .child` only match when the child is a *direct* child. If your CSS was written assuming an outerHTML swap (where the wrapper gets replaced) but the actual swap is innerHTML (where the response is nested inside the wrapper), the selectors won't match. Extend with `:has()` to cover both DOM shapes — see the `.vault-row > .vault-panel.vault-panel-down, .vault-row > .vault-row-body:has(> .vault-panel.vault-panel-down)` rule in [app.css](../internal/web/static/css/app.css).
- **The browser's `<details>` open/closed state is reset by an outerHTML swap** that recreates the element. Sort the polled output so a stable `<details>` element keeps its open state across polls.

### 7.12 When polling is NOT the right answer

Per-section polling works well for surfaces where:
- The data is cheap to compute server-side (single docker call, single yaml read).
- The natural update interval is 5–30 s.
- The user doesn't need real-time latency.

When those break down, reach for SSE instead (see §7.2). Specifically:
- Updates faster than 5 s → SSE.
- Updates that are event-driven (docker events, log lines) → SSE.
- Surfaces where "stale by Ns" is a real correctness issue → SSE.

---

## 8. Routing

### 8.1 Now

The three top-level views (`entities`, `services`, `llama`) are sibling `<div>`s toggled via `display:` in `showView()` ([static/js/app.js](../internal/web/static/js/app.js)). The URL never changes, so reload, back/forward, and link-sharing don't work.

### 8.2 Target

- Each view has a real URL: `/`, `/services`, `/llama`. Server renders the layout with the requested view active.
- View tabs are anchor tags with HTMX `hx-boost` for SPA-like navigation: `<a href="/services" hx-boost="true" hx-target="#main" hx-swap="innerHTML">`.
- The `popstate` event is handled by HTMX automatically with `hx-push-url="true"`.
- Per-tab state inside a view (selected source tab, kind filter, expanded composites) lives in `localStorage` keyed by view name, not in the URL — these are personal preferences, not shared state.

### 8.3 Rule

- **(rule)** A new top-level view gets its own URL. Don't extend the `display:none` pattern.
- **(rule)** Sub-state that a user might want to share (e.g., a deep link to a specific entity preview) goes in the query string and is applied on page load.

---

## 9. Live data

| Surface | Mechanism | Cadence |
|---|---|---|
| Entity list | Manual refresh + HTMX swap on demand | n/a |
| Container panel header counts | Polling | 8 s |
| Container engine events / state | SSE | event-driven |
| Services panel — shell | HTMX once-only (`revealed once`) | n/a |
| Services panel — containers section | HTMX per-section polling | 8 s |
| Services panel — open-design card (per project) | HTMX per-section polling | 8 s |
| Services panel — vaults section | HTMX per-section polling | 8 s |
| Services panel — instance card (per compose instance) | HTMX per-section polling | 8 s |
| Services panel — bridges section | HTMX action-triggered only | n/a |
| Services panel — OD token-stats body (per project) | HTMX self-poll with `hx-preserve` | 8 s |
| Services panel — OD version chip (per project) | HTMX self-poll with `hx-preserve` | 60 s |
| Services panel — vault row body (per vault) | HTMX self-poll with `hx-preserve` | 8 s |
| Llama panel — shell | HTMX once-only (`revealed once`) | n/a |
| Llama panel — server card (per server) | HTMX per-card polling | 10 s |
| Build chip (server version vs. page version) | Polling | 30 s |
| Process logs | SSE | event-driven |

**(rule)** A new "live" surface must declare in the PR description which mechanism it uses and why. If you're adding a third polling cadence, you should probably use SSE.

---

## 10. Vendored dependencies

- Each vendored file is committed to `internal/web/static/vendor/` with its license recorded in [LICENSES.md](../internal/web/static/vendor/LICENSES.md) alongside the upstream source URL and version.
- Today vendored from `https://registry.npmjs.org/<pkg>/-/<pkg>-<ver>.tgz`: htmx 2.0.4, fuse.js 7.0.0, marked 12.0.2. The devcontainer firewall allowlists `registry.npmjs.org` — no proxy needed.
- Upgrades happen in a dedicated PR with a single line of justification (CVE, needed feature, etc.). No drive-by version bumps.
- We do not minify our own code. Vendor files are committed in their published distribution form.
- Subresource Integrity (SRI) is unnecessary because we serve the assets ourselves from the binary.

### CodeMirror is now vendored too

CodeMirror 6 ships as a single pre-bundled file at [static/vendor/codemirror.bundle.js](../internal/web/static/vendor/codemirror.bundle.js) (~1.1 MB). [cmd/vendor-codemirror/main.go](../cmd/vendor-codemirror/main.go) fetches the `@codemirror/*` + `@lezer/*` package tree from `registry.npmjs.org`, populates a temp `node_modules/`, and bundles via the [esbuild Go API](https://pkg.go.dev/github.com/evanw/esbuild) per the §13 island playbook (no Node toolchain involved).

Regenerate with `make vendor-codemirror`. The tool prints a manifest of pinned versions; paste into [LICENSES.md](../internal/web/static/vendor/LICENSES.md) when bumping. The tool is a build-time dependency only — `make build` (which builds `./cmd/infra-mngmt` explicitly) does not link esbuild into the production binary.

Result: one HTTP fetch for the CodeMirror runtime instead of the ~50-request module graph esm.sh walked. CSP's `script-src` and `connect-src` are now `'self'` only (no external host allowance anywhere).

---

## 11. Accessibility & semantics

Aspirational, not gated, but required for new code:

- **(rule)** Every interactive element is a `<button>` or `<a>`, never a `<div onclick>`. (The current code mostly complies; new code must.)
- **(rule)** Form inputs have `<label for>` (or `aria-label` if visually hidden).
- **(rule)** Color is never the sole signal of state. Status pills already pair color with text — keep that pattern.
- **(rule)** Focus states are visible. Use `:focus-visible`, not `outline:none`.
- Keyboard shortcuts have a visible affordance.

### 11.1 Keyboard support

Minimal but specific. Anything that can't be done with mouse-only is also keyboard-accessible:

- `⌘K` / `Ctrl+K` — focus the search box (visible affordance: the placeholder text says `⌘K search`).
- `Tab` — through every interactive element in DOM order.
- `Esc` — dismiss the promote modal (any future modal must honor the same key).
- `Enter` — submits forms (login, save edit, target buttons in promote).
- `Space` / `Enter` on a focused button — activates it (browser default; never override).

**(rule)** There is **no global hotkey scheme** beyond `⌘K`. A feature that genuinely needs a hotkey adds one through a discoverable affordance (visible kbd badge or tooltip); ad-hoc shortcut bindings without one are not allowed.

---

## 12. Performance budgets

Soft targets, not enforced in CI:

- Initial HTML response: < 50 KB after gzip for the `/` route.
- Total static asset weight on first load (HTML + CSS + JS, vendored deps included): < 250 KB after gzip.
- Time to interactive on a fresh load (localhost): < 200 ms.

If a feature would push the bundle past 250 KB, that's the trigger to split the page module behind a route, not to add it to `app.js`.

---

## 13. When to introduce a JS island

A JS framework (Svelte preferred, Solid acceptable) is permitted in *exactly one* page region when **all** of the following hold:

1. The interaction is genuinely reactive — multiple values change in response to one event, with cross-references between them. Examples: a dependency graph with hover-highlight, a multi-pane diff editor.
2. HTMX swap would round-trip the server > 1× per second.
3. The state lives only on the client and the server doesn't need it.
4. The island can be mounted into a single `<div id="...">` and the rest of the page is unaffected if the island fails to load.

If introduced:

- The framework runtime ships as a vendored bundle in `static/vendor/`.
- The island has its own folder under `static/js/islands/<name>/` containing the source.
- A pre-build script (Go-based, `go run` invoked from `make build`) compiles the island. **No Node toolchain is added to the contributor flow.** If we need esbuild, we vendor the esbuild Go API and shell it from the Makefile.

The first candidate for an island is the dependency-graph + blast-radius UI from [CLAUDE.md](../CLAUDE.md) build-order step 7. That is the *only* feature on the roadmap that warrants an island today.

---

## 14. Testing

Reference: [TESTING.md](../TESTING.md).

### 14.1 What gets tested where

| Layer | Test type | File |
|---|---|---|
| FuncMap helpers | Unit, table-driven | [internal/web/template_test.go](../internal/web/template_test.go) |
| Handler routing, status codes, partial shape | HTTP test | [internal/web/handlers_http_test.go](../internal/web/handlers_http_test.go) |
| Multi-step flows (login, redirect, cookie) | E2E with `e2eEnv` | [internal/web/e2e_test.go](../internal/web/e2e_test.go) |
| Templates parse & render with realistic data | Unit | [internal/web/template_test.go](../internal/web/template_test.go) + [template_render_test.go](../internal/web/template_render_test.go) |
| Frontend guideline enforcement (this doc) | Source-tree scan | [internal/web/frontend_guidelines_test.go](../internal/web/frontend_guidelines_test.go) |
| JS modules | (none) | We accept this gap; see §14.2 |

### 14.1a Mechanical guideline enforcement

The rules in this document that can be checked by walking the source tree
are enforced by [frontend_guidelines_test.go](../internal/web/frontend_guidelines_test.go).
Failures emit `<file>:<line>: <reason> — see §X` so the violator can find
the affected file and the relevant section of this doc. The current checks:

| Test | Enforces | Doc section |
|---|---|---|
| `TestGuideline_NoTemplateStringConstantsInGo` | No HTML template content in `internal/web/*.go` raw strings (SVG icon constants exempted by name suffix) | §3.3, §17 |
| `TestGuideline_TemplatesHaveNoInlineCSSOrJS` | No `<style>` or inline `<script>` body in `templates/*.html.tmpl` (login + promote modals are documented exceptions) | §3.3, §17 |
| `TestGuideline_NoCDNURLsInServedAssets` | No `unpkg.com` / `cdn.jsdelivr.net` / `esm.sh` URLs in templates or static assets — every external dep must be vendored | §10 |
| `TestGuideline_NoURLImportsInJS` | ES module `import` statements only use same-origin paths (CodeMirror exception in `preview.js`) | §6.2 |
| `TestGuideline_VendoredFilesHaveLicenseEntry` | Every file in `static/vendor/` is recorded in `LICENSES.md` | §10 |
| `TestGuideline_HTMXPollingCadenceFloor` | `hx-trigger="...every Ns..."` has N ≥ 5 (anything tighter must use SSE) | §7.4, §9 |
| `TestGuideline_NoEvalOrFunctionConstructor` | No `eval(...)` or `new Function(...)` in `static/js/*` | §6.2 |
| `TestGuideline_TemplateHTMLOnlyForIcons` | `template.HTML(x)` only wraps identifiers ending in `SVG` / `Icon` | §4.3 |

**To add a new exception**: update both the test (so the suite stays green)
*and* the corresponding section of this doc with the documented reason.
Adding only the allowlist entry leaves the exception undocumented; adding
only the doc text means the test breaks on the next change. Both, always.

**Not (yet) enforced mechanically** — these still live in code review:
no `onclick=` in *new* templates (delta detection requires git context),
BEM-ish CSS naming, focus-visible vs. outline:none, `<label for>` on
inputs, gzip size budgets. Add a test when one of these regresses, not
prophylactically.

### 14.2 JS testing

We do not have a JS test framework, and we will not add one until a page module exceeds ~300 lines or has branching logic that has bitten us in production. The mitigation is:

- Keep JS modules small, single-purpose, and pure where possible.
- Render-test the *templates* — if HTMX swaps and rendered partials are correct, most "frontend" bugs are caught on the Go side.
- Manual verification in `ident-browser` is part of every UI-touching PR (see project memory `feedback_verify_in_browser`).

### 14.3 Rules

- **(rule)** A new template gets a render test that constructs the view struct and exercises `template.Execute` (or `ExecuteTemplate` for named `{{define}}` blocks) with both a populated and an empty case.
- **(rule)** A new partial endpoint gets a handler test that asserts on at least one stable selector in the response (`strings.Contains(body, \`id="entity-list"\`)`).
- **(rule)** Multi-section panels (composite shells per §7.10) get one test per section template plus a cross-cutting `renderServicesSections`-style helper for assertions that span sections.
- **(rule)** A new SSE endpoint gets an e2e test that opens the stream, asserts on one event, and asserts the connection closes on `done`.

---

## 15. Adding a new feature: checklist

For any change that adds or modifies UI:

1. **Pick the interaction model.** §7 — HTMX, SSE, or fetch. Justify if not HTMX.
2. **Pick the route.** §8 — new top-level view = new URL.
3. **Decide what's a partial.** Anything HTMX touches must be its own template file under `templates/`.
4. **Wire the data.** Define a view struct next to the handler; never `map[string]any`.
5. **Write the template.** New file under `templates/`. If the view needs self-polling sub-sections, co-locate their `{{define}}` shells in that file (see §7.10).
6. **Style it.** Extend `static/css/app.css` and reuse `:root` tokens — no hex literals, no per-page stylesheet.
7. **Script it.** New module under `static/js/<page>.js` only if interaction goes beyond HTMX. Bootstrap from `app.js` only if every page needs it.
8. **Test it.**
   - Unit-test new FuncMap helpers and view-struct mappers.
   - HTTP-test the handler.
   - Render-test the template.
   - For a multi-step flow, add an `e2eEnv` test.
9. **Verify in `ident-browser`** at `localhost:7842` — `make check` does not catch runtime JS errors.
10. **Run `make check`**. Pre-commit gate.

---

## 16. Migration plan from the current state

This is a sequenced refactor, not a big-bang rewrite. Each step is independently mergeable, leaves the binary working, and unblocks the next.

| Step | Status | Scope | Risk |
|---|---|---|---|
| **M1** | ✅ shipped | Created [internal/web/static/](../internal/web/static/) and [internal/web/templates/](../internal/web/templates/), both `embed.FS`-mounted via [static.go](../internal/web/static.go) and [templates.go](../internal/web/templates.go). Vendored htmx, fuse.js, marked, and (in a follow-up) CodeMirror 6 + its `@lezer/*` deps from `registry.npmjs.org` into [static/vendor/](../internal/web/static/vendor/) with [LICENSES.md](../internal/web/static/vendor/LICENSES.md). | Low. Pure asset move. |
| **M2** | ✅ shipped | Extracted the CSS block from `indexHTML` into [static/css/app.css](../internal/web/static/css/app.css) (~525 lines). Layout references it via `<link rel="stylesheet" href="{{static "css/app.css"}}">`. | Low. CSS-only, easy rollback. |
| **M3** | ✅ shipped | Inline `<script>` block moved into [static/js/app.js](../internal/web/static/js/app.js) as an ES module. Window exports added for every function called from an `onclick=` attr. | Medium — module scoping shifts globals; verified in `ident-browser`. |
| **M4** | ✅ shipped | Every HTML constant moved into [templates/*.html.tmpl](../internal/web/templates/) and parsed via `embed.FS`. `internal/web/template.go` deleted entirely (2309 lines gone). | Medium — pure mechanical move; render tests catch most regressions. |
| **M5** | ✅ shipped | `app.js` (797→516 lines) split into shared bootstrap + [preview.js](../internal/web/static/js/preview.js) (entity preview + CodeMirror) + [containers.js](../internal/web/static/js/containers.js) (container panel + SSE). Cross-module communication via `window.X` (transitional). | Low–medium. |
| **M6** | ⏸ deferred | Replace `display:none` view switching with real URL routes (`/services`, `/llama`) using `hx-boost`. Migrate per-view local state to `localStorage` keyed by view. **Deferred per project decision** — view switching still uses display toggle and works. Re-open when deep links or back-button behavior become friction. | Medium — touches every test that asserts on landing page state. |
| **M7** | ✅ shipped | Container panel converted from `fetch()` + manual `innerHTML` to HTMX partial swaps. New [`/partials/container-controls`](../internal/web/handlers.go) endpoint and [container_controls.html.tmpl](../internal/web/templates/container_controls.html.tmpl); `containers.js` shrunk 208→98 lines. SSE for engine events unchanged. | Medium — touches the most complex client logic. |
| **M8** | not triggered | (Optional, blocked on roadmap step 7) Introduce the dependency-graph island per §13 if and only if the UX cannot be done with HTMX. | High — first time we ship a framework runtime. |

### Browser verification log

Verified in `ident-browser` against the deployed binary (commit `e7bf9edf+`, build epoch `1778439765`):

- ✅ Page renders; build chip relative time formats correctly (confirms `app.js` executes)
- ✅ View tabs switch: `entities` ↔ `services` ↔ `llama`; HTMX-polled partials load on reveal
- ✅ HTMX partial swap on entity-card click loads `/partials/entity` into `#preview`
- ✅ M7 container panel: clicking the `infra-mngmt` source tab loads `/partials/container-controls?project=...` showing the dot, label, `■ stop`, and `VS Code` buttons via the server-rendered partial (not the old JS `renderContainerControls`)
- ✅ Entity **edit** flow now works reliably (verified live with the vendored CodeMirror bundle — editor opens, accepts input, save round-trips). The save attempt on a Docker-volume-backed entity returns 403 "read-only" as expected.

M1–M5 + M7 are pure structural wins. M6 is the only step explicitly skipped.

---

## 17. Anti-patterns (do not do these)

- Adding a new HTML constant to `template.go` "for now."
- Adding a CDN URL "until we vendor it."
- A new top-level view that's a fourth `<div>` toggled by `display`.
- A `fetch()` that builds an HTML string and `innerHTML`s it. (If you need server-rendered HTML, use HTMX. If you need a JSON response, render a server partial instead.)
- A `window.<name>` global to share state between two scripts.
- A polling loop tighter than 5 s.
- Bypassing `html/template` auto-escape with `template.HTML(s)` for any `s` derived from user/file input.
- Adding a JS dependency from npm or a CDN.
- Writing a CSS selector that assumes the htmx swap target is the response root (`.parent > .child`) without verifying the actual `hx-swap` style. Default is `innerHTML`, which nests the response inside the target. See §7.11.
- Polling a composite panel with one outer endpoint, then trying to fix the resulting flicker with ETag/304/JS workarounds. The correct fix is per-section polling — see §7.10.
- A new polled surface that re-renders multiple unrelated sections in one response. Split it.
- Relying on process-compose (or any upstream API) for a stable row order. Sort server-side before rendering, or the open/closed state of any `<details>` in the section will jump around.

---

## 18. Open questions

1. ✅ **Cache busting for static assets.** Resolved: [internal/web/static.go](../internal/web/static.go) `staticAssetURL` appends `?v=<token>` derived from `BuildInfo.Commit` (falling back to `BuildEpoch` for unstamped builds). Wired via `SetBuildInfo`; covered by `TestStaticAssetURL_CacheBuster` and `TestSetBuildInfo_WiresCacheBuster`.
2. ✅ **HTMX error toast surface.** Resolved during M7: [containers.js](../internal/web/static/js/containers.js) subscribes to `htmx:responseError` and toasts when any `/api/container/*` action fails. Same hook is the place to extend if other API endpoints need similar treatment — the listener gates on path prefix so it's safe to broaden.
3. ✅ **CSP header.** Resolved: [securityHeadersMiddleware](../internal/web/static.go) ships `Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; …` plus `X-Content-Type-Options: nosniff` and `Referrer-Policy: same-origin` (switched from `no-referrer`, which nulled the cross-origin `Origin` header in Chromium and broke the wake.js POST). `script-src` and `connect-src` are now `'self'`-only after the modal-script extraction, the `onclick=` removal, and the CodeMirror vendoring. `'unsafe-inline'` remains in `style-src` for inline `style="display:none"` toggles and dynamic widths — dropping it requires migrating those to class-based toggles; tracked as a follow-up.
4. ✅ **Lazy-loading CodeMirror.** Resolved: [preview.js](../internal/web/static/js/preview.js) `loadCodeMirror()` wraps a dynamic `import("/static/vendor/codemirror.bundle.js")` and caches the promise, so the ~1.1 MB bundle is fetched at most once and only when the user clicks "edit". Initial page render no longer pays the cost. While the bundle is in flight the editor slot shows a `"loading editor…"` placeholder; a cancel during that window bails before instantiating the editor.
