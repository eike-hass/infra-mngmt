# `kind: open-design` + generic compose-project grouping

**Status:** shipped. Document reflects the final shape, not the original
proposal — see "Divergence from the initial plan" at the bottom for the
substantive design changes made during implementation.

**Goal:** let `containers.yaml` declare a compose project as one entry with
the services it owns, so the services panel can render it as a project
group (header + per-service rows + project-scoped lifecycle controls)
instead of one flat row per service with a duplicated `compose_file:`.
`kind: open-design` is the first specialization, layering a privileged card
on top with a web-URL link and HTMX-loaded token-stats summary.

## Why

The two entries that originally registered OD were:

```yaml
- name: open-design          # web + daemon
  compose_file: .../compose.yaml
- name: od-token-stats       # sidecar
  compose_file: .../compose.yaml      # ← duplicated path
```

Two issues:

1. **Schema smell** — the same `compose_file:` repeated because there's no
   way to say "these two share a project."
2. **Display smell** — the two services render as unrelated rows. No
   visual sign that stopping one means the other goes with it, no rolled-up
   status, no link to the actual app, no surface for the per-session token
   stats the sidecar already exposes.

## Schema

`Container` gains an optional top-level `services []ServiceEntry` field.
When non-empty, the entry represents a compose project (ComposeFile
required), and the services panel renders it as a project group. `kind:`
is independent — present, it activates kind-specific rendering on top.

```yaml
# Plain compose-project grouping (no kind):
- name: my-stack
  compose_file: /abs/compose.yaml
  services:
    - container: web
    - container: api
      role: api          # role is free-form; just a display label here
    - container: cache
      url: http://localhost:6379

# Privileged kind: open-design:
- name: open-design
  kind: open-design
  compose_file: /home/eike/Workspace/open-design/compose.yaml
  services:
    - container: open-design
      role: web
      url: http://localhost:7456
    - container: od-token-stats
      role: token-stats
      url: http://localhost:7460
```

**Field meanings:**

- **`services[].container`** — docker container name (FindByName-matched). Required.
- **`services[].role`** — optional free-form label. Kind-specific renderers
  attach behavior to known roles (e.g. `kind: open-design` reads
  `role: web` for the card's link, `role: token-stats` for the HTMX slot).
- **`services[].url`** — optional. For `role: web` it's the clickable link
  on the card; for `role: token-stats` it's the base URL whose `/usage`
  path the HTMX slot fetches.

**Validation:**

- `services[]` non-empty requires `compose_file:` (compose drives lifecycle).
- Each `services[].container` matches the docker-container name regex.
- `kind: open-design` requires services with `role: web` (with `url`)
  **and** `role: token-stats` (with `url`). Both back the card.

## What changed in code

| Layer | Change |
|---|---|
| [`internal/containers/types.go`](../../internal/containers/types.go) | `ServiceEntry` type; `Services []ServiceEntry` field on `Container`; `"open-design"` in `knownKinds`; validation per the rules above. |
| [`internal/containers/types_test.go`](../../internal/containers/types_test.go) | Tests for plain-compose grouping, OD kind, and the kind-specific role + URL requirements. |
| [`internal/web/containers.go`](../../internal/web/containers.go) | New `containerProjectView` (one per compose project, regardless of kind) and `buildContainerProjectGroups`. `snapshotContainers` skips entries with `services[]` (project groups handle their own children). `handleOpenDesignTokenStats` resolves the usage URL from `Container.Services` (role=token-stats's URL). |
| [`internal/web/handlers.go`](../../internal/web/handlers.go) | `handleServicesPartial` populates both `ContainerProjects` (all compose projects) and `OpenDesignProjects` (pre-filtered subset for the card section). |
| [`internal/web/server.go`](../../internal/web/server.go) | Route `GET /partials/open-design/token-stats`. |
| [`internal/web/templates/services.html.tmpl`](../../internal/web/templates/services.html.tmpl) | Project-group rows (`tr.project-header` + `tr.project-member`) inside the containers table; slimmed OD card section (web link + status + HTMX token-stats slot — no lifecycle, no per-service pills). |
| [`internal/web/templates/open_design_token_stats.html.tmpl`](../../internal/web/templates/open_design_token_stats.html.tmpl) | HTMX-swapped totals row partial. |
| [`internal/web/static/css/app.css`](../../internal/web/static/css/app.css) | `.status-pill.degraded`, project-group row styles, slimmed OD card styles. |
| [`internal/web/open_design_test.go`](../../internal/web/open_design_test.go) | Handler tests (5), builder tests (3), template render test. |

## UI shape

Two surfaces:

**Containers section — project group (lifecycle lives here):**
```
┌─ containers ────────────────────────────────────────────────┐
│ <flat container rows>                                       │
│ open-design [project]      ● running   compose.yaml  [stop] │
│   open-design              ● running   Up 5m   abc123       │
│   od-token-stats           ● running   Up 5m   def456       │
└─────────────────────────────────────────────────────────────┘
```

**Open Design card (privileged display, no controls):**
```
┌─ Open Design ─────────────────────── http://localhost:7456 →┐
│  ● running                                                   │
│  usage: 149 sessions · 1441 msgs · in 8.9M · cache_read 84.6M│
│         hit 90.5%                                       [↻]  │
└──────────────────────────────────────────────────────────────┘
```

- Rolled-up status pill: worst-state-wins across services
  (`running` / `stopped` / `degraded` / `unknown`).
- Project lifecycle is project-scoped (`docker compose -f <file> up -d` /
  `down`) — uses the existing per-`compose_file:` machinery in
  `handleContainerStartByName` / `handleContainerStopByName`.
- The OD card is read-only. No buttons, no per-service pills — the project
  group above carries those.
- Token-stats slot loads via HTMX from
  `GET /partials/open-design/token-stats?name=<container>`; the partial
  has its own refresh button. Section is `hx-preserve`'d so the 8s services
  poll doesn't refetch the summary on every tick; the rolled-up pill
  updates via OOB swap.

## Lifecycle

Reuses the existing per-`compose_file:` machinery
([`internal/web/containers.go:268-...`](../../internal/web/containers.go)).
The compose project name resolves to `name:` in the compose YAML (set as
`name: open-design` at the top of OD's `compose.yaml`). One start call
brings up both services; one stop call takes them both down.

The SDK fallback in `composeAction` continues to apply for the case where
the compose project name doesn't match (e.g. a manual `docker run`).

## Migration

The two flat entries become one grouped entry:

```yaml
# before
- name: open-design
  description: "Open Design — web UI + daemon"
  compose_file: /home/eike/Workspace/open-design/compose.yaml
- name: od-token-stats
  description: "OD opencode token usage"
  compose_file: /home/eike/Workspace/open-design/compose.yaml

# after
- name: open-design
  description: "Open Design — compose project (web + token-stats)"
  kind: open-design
  compose_file: /home/eike/Workspace/open-design/compose.yaml
  services:
    - container: open-design
      role: web
      url: http://localhost:7456
    - container: od-token-stats
      role: token-stats
      url: http://localhost:7460
```

## Out of scope

- **Per-service restart buttons.** Compose's natural granularity is the
  project; per-service restarts are a rare ask. The project header owns
  lifecycle.
- **Compose-yaml viewer / diff between declared and running.** Worth a
  separate proposal if/when we generalize further.
- **Pricing/cost in the token-stats panel.** The sidecar doesn't compute
  cost (per its own design). The card shows raw usage.

## Divergence from the initial plan

The implementation diverged from the original proposal in three substantive
ways. Reasons captured here so future readers can see the trail:

1. **Services moved from `open_design.services[]` to top-level `Container.Services`.**
   Reason: the user asked for the project grouping to work for any compose
   project (not just OD). Schema generalized; `kind: open-design` is now a
   pure discriminator that activates the card section.

2. **Two surfaces instead of one card.** The original card had everything:
   web URL, per-service pills, lifecycle buttons, token-stats. The user
   asked for lifecycle controls in a project group inside the containers
   table. So the card was slimmed to web URL + rolled-up pill + token-stats
   (the OD-specific value-add); per-service pills + lifecycle live in the
   project group.

3. **`open_design:` typed block dropped entirely.** No `OpenDesignConfig`
   struct in `containers/types.go`. Top-level `Services[]` + `kind:` carry
   everything that was in the block. Plain compose-project grouping
   (no kind, no card) is a free byproduct.

## Open questions

- **Token-stats summary refresh cadence.** Loaded once on card render plus
  manual refresh button. Auto-poll feels noisy for a panel that's not the
  primary content. Worth revisiting if users ask.
- **Number formatting.** Token counts currently render as raw integers
  (e.g. `8918029` rather than `8.9M`). Easy `formatTokens` FuncMap helper
  if it becomes an irritation.
