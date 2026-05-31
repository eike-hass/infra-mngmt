# Proposal: Dependency graph — diagnosis & blast radius

Status: accepted (personal-cockpit scope)
Scope: a dependency graph over entities + the services/bridges/containers/tiers
that back them, surfaced two ways — **backward** (root-cause diagnosis: "why is
this broken?") and **forward** (blast radius: "what breaks if I touch this?").

## Why

Today the tool lets you *act* (stop/restart, promote, bridge-reset) and *flags*
broken refs, but both are half-blind:

- **Diagnosis is flat.** A broken MCP shows a red badge. You then hop tiers by
  hand to find the culprit ("is the service down? the bridge? is windows
  process-compose even reachable?"). This is the #1 daily friction.
- **Acting is blind.** Stopping a process or resetting a bridge gives no preview
  of what depends on it.

Both are the **same dependency graph read in opposite directions.** Build it
once; traverse it backward for diagnosis and forward for blast radius.

Out of scope (explicitly): reversibility/undo. Future (not in this proposal):
drift detection (declared vs. running) and a visual graph view.

## Current state

`internal/graph/refs.go` (`Resolve`) is entity-centric and one-hop: for each
entity it matches `deps` rules (or the legacy MCP substring fallback) to a list
of `Ref`s (service / bridge / container) and rolls them up to a worst-of
`RefState` for the badge. `internal/web/status.go` feeds it `ProcInfo`
(per-process, with `Instance` + CSS state), `BridgeInfo`, container infos, and a
single `anyOnline` bool, producing the `MCPStatus` map the UI badges read.

Gaps for this feature:

1. **No reverse index** — given a service/bridge, which entities depend on it?
   (needed for blast radius).
2. **No causal chain** — `Ref`s are peers, not a walkable path; there's no
   "service runs on tier X; tier X is unreachable" structure.
3. **Reachability is collapsed** to `anyOnline` — we can't say *which* instance
   is down, which is the most common cross-tier root cause.

## The model

A new layer in `internal/graph` (additive — `Resolve`/`EntityRefs` stay for the
existing badges; the graph reuses the same inputs):

**Nodes** (typed, each carrying an observed `Health`: ok / degraded / down /
missing / offline / unknown):

- `entity` — id, kind, name, scope
- `service` — (instance, name)
- `bridge` — name
- `container` — name
- `instance` — a process-compose tier (`wsl`, `windows`); health = reachable?

**Edges** (directed, consumer → supplier), derived from existing inputs:

| edge | from → to | derived from |
|---|---|---|
| `needs` | entity → service/bridge/container | `deps` rules + legacy MCP fallback (already computed by `Resolve`) |
| `runs-on` | service → instance | `ProcInfo.Instance` |
| `backed-by` | service → bridge | port match: a `socat` bridge whose listen port equals the service's endpoint port (heuristic; a `deps` rule can override) |

Edge derivation is reused from the resolver where possible; `runs-on` and the
per-instance health are the new cheap, high-value signals. `backed-by` (the
cross-tier socat link) is the richer hop — built on a documented port-match
heuristic, refinable later via explicit rules.

**Health** is computed once per node from the same observed state the badges use
(process CSS state, bridge state, container state, **per-instance reachability**
— see below), then propagated: a node is `offline` if the instance it runs on is
unreachable; `down`/`missing` from its own observed state.

### Per-instance reachability (prerequisite)

Refine `resolveMCPStatuses`: replace the single `anyOnline` bool with a
`map[instance]bool` (which instances pinged OK). `Resolve` already takes
`anyOnline`; it gains per-instance awareness so `RefOffline` can be attributed
to a specific tier. This is a small, self-contained first step and unblocks the
"windows process-compose unreachable" root cause.

## Two traversals

**Backward — root-cause diagnosis.** From a broken node, walk *up* the `needs` /
`runs-on` / `backed-by` edges to the first node whose own health is bad and whose
own dependencies are healthy (or a leaf infra node that's down). Render the path:

> `mcp:foo` ✗ → needs `service:llama@windows` (down) → runs-on `instance:windows`
> (**unreachable**) ← root cause

**Forward — blast radius.** From any node, walk the *reverse* edges to enumerate
dependents, grouped by kind: "stopping `instance:windows` affects: 1 llama card,
2 MCP refs, the OD token-stats probe."

Both are pure functions over the built graph (cheap; no new probing).

## UI surfaces

Server-rendered HTMX partials, consistent with the per-section model
(`docs/frontend-architecture.md` §7.10):

- **Diagnosis** — a "why?" disclosure on any broken/red status badge and on the
  preview's `broken-ref-banner`. Expands (lazy `GET /partials/diagnose?id=…`) to
  the root-cause trace. *Phase 1.*
- **Blast radius** — a confirm step on the mutating POSTs (`/process/stop`,
  `/process/restart`, `/bridge/reset`, `/decl-container/stop`, `/api/promote`
  overwrite, future delete): `GET /partials/blast-radius?node=…` renders
  "affects N — proceed?" before the action fires. *Phase 2.*

## Phasing

1. **Graph + diagnosis.** Per-instance reachability; `internal/graph` graph
   model + builder + backward root-cause traversal (pure, unit-tested); the
   "why?" disclosure partial on broken badges + the broken-ref banner.
2. **Blast radius.** Reverse-edge traversal; the "affects N — proceed?" confirm
   partial wired into the mutating POSTs.

Each phase: `make check` green, committed, deployed, verified live.

## Non-goals / risks

- **Not** a general graph database or a live-updating viz — inline traces beat a
  canvas for a solo cockpit.
- The `backed-by` port-match is a **heuristic**; document it and let an explicit
  `deps` rule override. A wrong/absent edge degrades gracefully (diagnosis stops
  one hop short; it never lies — it only ever reports observed health).
- Keep the graph build cheap (it runs on the same cadence as status resolution);
  it consumes already-probed state, adds no new round-trips.
