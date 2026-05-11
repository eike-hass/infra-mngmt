# Frontend architecture & guidelines

Status: **in effect — M1–M5 + M7 of §16 have shipped; M6 deferred**. Sections marked **(now)** describe the present codebase; **(target)** describes where we are headed; **(rule)** is binding for new code regardless of where the surrounding files sit today.

This document is the canonical reference for anyone touching anything under [internal/web/](../internal/web/). It is referenced from [CLAUDE.md](../CLAUDE.md) and [README.md](../README.md); changes to the frontend layout, asset pipeline, or interaction model must update this doc in the same change. **For the orientation guide ("how is the codebase laid out, what UX patterns exist, where do I look when X breaks"), see the companion [frontend-handover.md](frontend-handover.md).**

It exists because the frontend has crossed the line where ad-hoc decisions start to compound, and because we have explicitly chosen *not* to migrate to a SPA framework — that choice only pays off if the discipline below is followed.

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

The server is `net/http` + chi, configured in [internal/web/server.go](../internal/web/server.go). Static assets are served from a single `embed.FS` rooted at `internal/web/static/`.

---

## 3. File layout

### 3.1 Now

Templates live as Go raw-string constants inside [internal/web/template.go](../internal/web/template.go) (~2280 lines) and a few siblings ([vault.go:263](../internal/web/vault.go#L263)). CSS and JS are inlined inside `indexHTML`. CDN URLs are referenced directly from `<script>` and `<link>` tags. There is no static asset directory.

### 3.2 Target

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
    layout.html.tmpl      // <html>, <head>, header, view container, footer
    index.html.tmpl       // entity-list + preview, extends layout
    entity_list.html.tmpl // partial, returned from /partials/entity-list
    preview.html.tmpl     // partial
    services.html.tmpl
    logs.html.tmpl
    llama.html.tmpl
    login.html.tmpl
    promote_picker.html.tmpl
    promote_result.html.tmpl
    vault.html.tmpl
    components/           // sub-templates included by name
      entity_card.html.tmpl
      service_row.html.tmpl
      build_chip.html.tmpl
      kind_pill.html.tmpl
  static/
    app.css               // shared tokens, layout, components
    pages/
      services.css        // page-scoped CSS, only loaded by that page
      llama.css
    js/
      app.js              // bootstrap: htmx config, view switch, search
      preview.js          // markdown rendering, edit-in-place
      containers.js       // container panel: SSE, controls
      llama.js            // llama page module
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

- `layout.html.tmpl` defines `{{define "page"}}` blocks: `head`, `body`, `scripts`. Pages override the blocks they care about and inherit the rest.
- Reusable fragments live under `components/` and are included by `{{template "entity_card" .}}` with an explicit data argument.
- Partials returned to HTMX are *whole files* under `templates/`, not nested defines — this keeps the response shape obvious from the filename.

Avoid `{{define}}` blocks scattered across unrelated files; the rule is *one named template per file* unless the file is explicitly the layout.

### 4.2 FuncMap

The `tmplFuncs` map ([handlers.go:293](../internal/web/handlers.go#L293)) is a curated surface. Keep it small and documented.

- **(rule)** A function added to `tmplFuncs` must have a unit test in [template_test.go](../internal/web/template_test.go) covering its happy path and the empty/zero input.
- **(rule)** A FuncMap helper that returns `template.HTML` must escape any caller-supplied content explicitly. Never feed user-mutable data through `template.HTML`.
- **(rule)** Formatters (`formatMem`, `formatCount`, `formatTokensPerSec`, …) are pure functions and must be unit-tested without spinning up a template.

### 4.3 Escaping

- **(rule)** Auto-escaping stays on. Untrusted input (entity names, source IDs, file paths, log lines) is rendered with `{{.}}` only.
- **(rule)** The only places `template.HTML` is acceptable: vendored SVG icons defined as Go constants, or output produced by `marked` *on the client*. Never hand the client pre-rendered HTML produced from arbitrary file contents.

### 4.4 Data shape

- One handler → one view struct → one template. Don't pass `map[string]any` except when the data is truly heterogeneous (login form, ad-hoc errors).
- View structs live next to the handler that produces them, named `<page>View`. Use exported field names and explicit JSON tags only if the same struct also crosses an API boundary.

---

## 5. CSS

### 5.1 Tokens

The variables already in `indexHTML` (`--bg`, `--text`, `--accent`, `--kind-*`, `--level-*`) are the design system. Move them to `:root` in `app.css` and never inline-override them in component CSS.

```css
:root {
  --bg:  #0c0c0c;
  --bg2: #141414;
  --bg3: #1c1c1c;
  --text:  #c9c9c9;
  --text2: #888;
  --accent:  oklch(68% 0.18 200);
  /* …kind, level, status colors… */
}
```

### 5.2 Naming

- Components are flat, BEM-ish: `.entity-card`, `.entity-card-header`, `.entity-card.is-collapsed`. No nesting selectors more than two levels deep.
- State classes are prefixed `is-` (`is-active`, `is-collapsed`, `is-loading`) — never set state via `style="display:none"` from JS for anything more than transient toggling.
- Page-scoped styles live in `static/pages/<page>.css` and are loaded only on that page via a `{{block "head" .}}` override.

### 5.3 Rules

- **(rule)** No `!important` except in vendor overrides, with a comment naming the offending rule.
- **(rule)** No `style="..."` attributes for layout. Inline styles are acceptable only for genuinely dynamic values (CPU bar widths from server data, e.g. [handlers.go cpuBarWidth](../internal/web/handlers.go) — render via `{{cpuBarWidth . | printf "width:%s"}}` once, not piecemeal).
- **(rule)** Every new color, spacing, or radius value must reuse a token or extend `:root`. No hex literals scattered through component CSS.

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
- **(rule)** Any `EventSource` opened by a module must close itself on `pagehide` / view switch. The pattern in [template.go closeLogStream](../internal/web/template.go) is correct; copy it.

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

`hx-trigger="every 8s"` on a `<div>` whose content is the partial is acceptable for slow-changing aggregates (services panel, llama page). **(rule)** Polling intervals: 5 s minimum, 30 s preferred for non-critical surfaces. Anything faster must use SSE.

### 7.5 Forbidden

- Mixing HTMX swap with manual DOM patching of the swapped region. Pick one per region.
- Building HTML strings on the client and `innerHTML`-ing them. Either swap a server partial, or `createElement`.
- Long-poll loops (`while(true) { await fetch... await sleep }`). Use SSE or HTMX polling.

---

## 8. Routing

### 8.1 Now

The three top-level views (`config`, `services`, `llama`) are sibling `<div>`s toggled via `display:` in `showView()` ([template.go:641](../internal/web/template.go#L641)). The URL never changes, so reload, back/forward, and link-sharing don't work.

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
| Services panel | HTMX polling on the panel partial | 8 s |
| Llama panel | HTMX polling | 10 s |
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
- Keyboard shortcuts (⌘K) have a visible affordance.

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

- **(rule)** A new template gets a render test that constructs the view struct and exercises `template.Execute` with both a populated and an empty case.
- **(rule)** A new partial endpoint gets a handler test that asserts on at least one stable selector in the response (`strings.Contains(body, \`id="entity-list"\`)`).
- **(rule)** A new SSE endpoint gets an e2e test that opens the stream, asserts on one event, and asserts the connection closes on `done`.

---

## 15. Adding a new feature: checklist

For any change that adds or modifies UI:

1. **Pick the interaction model.** §7 — HTMX, SSE, or fetch. Justify if not HTMX.
2. **Pick the route.** §8 — new top-level view = new URL.
3. **Decide what's a partial.** Anything HTMX touches must be its own template file under `templates/`.
4. **Wire the data.** Define a view struct next to the handler; never `map[string]any`.
5. **Write the template.** New file under `templates/`. Reuse a `components/` partial if one fits.
6. **Style it.** Page-scoped CSS in `static/pages/<page>.css` if > 20 lines; otherwise extend `app.css` and reuse tokens.
7. **Script it.** New module under `static/js/<page>.js` only if interaction goes beyond HTMX. Bootstrap from `app.js` only if every page needs it.
8. **Test it.**
   - Unit-test new FuncMap helpers and view-struct mappers.
   - HTTP-test the handler.
   - Render-test the template.
   - For a multi-step flow, add an `e2eEnv` test.
9. **Verify in `ident-browser`** at `172.17.0.1:7842` — `make check` does not catch runtime JS errors.
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
- ✅ View tabs switch: `config` ↔ `services` ↔ `llama`; HTMX-polled partials load on reveal
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

---

## 18. Open questions

1. ✅ **Cache busting for static assets.** Resolved: [internal/web/static.go](../internal/web/static.go) `staticAssetURL` appends `?v=<token>` derived from `BuildInfo.Commit` (falling back to `BuildEpoch` for unstamped builds). Wired via `SetBuildInfo`; covered by `TestStaticAssetURL_CacheBuster` and `TestSetBuildInfo_WiresCacheBuster`.
2. ✅ **HTMX error toast surface.** Resolved during M7: [containers.js](../internal/web/static/js/containers.js) subscribes to `htmx:responseError` and toasts when any `/api/container/*` action fails. Same hook is the place to extend if other API endpoints need similar treatment — the listener gates on path prefix so it's safe to broaden.
3. ✅ **CSP header.** Resolved: [securityHeadersMiddleware](../internal/web/static.go) ships `Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; …` plus `X-Content-Type-Options: nosniff` and `Referrer-Policy: no-referrer`. `script-src` and `connect-src` are now `'self'`-only after the modal-script extraction, the `onclick=` removal, and the CodeMirror vendoring. `'unsafe-inline'` remains in `style-src` for inline `style="display:none"` toggles and dynamic widths — dropping it requires migrating those to class-based toggles; tracked as a follow-up.
4. ✅ **Lazy-loading CodeMirror.** Resolved: [preview.js](../internal/web/static/js/preview.js) `loadCodeMirror()` wraps a dynamic `import("/static/vendor/codemirror.bundle.js")` and caches the promise, so the ~1.1 MB bundle is fetched at most once and only when the user clicks "edit". Initial page render no longer pays the cost. While the bundle is in flight the editor slot shows a `"loading editor…"` placeholder; a cancel during that window bails before instantiating the editor.
