# TODO — Claude Code coverage gaps

Items below were identified by cross-checking our entity discovery against the
official [Claude Code docs](https://docs.claude.com/en/docs/claude-code/) (see
`settings`, `mcp`, `skills`, `sub-agents`, `memory`). Fixed bugs are documented
in commit history; this file lists what we still don't surface.

## Missing entity kinds

- [ ] **Rules** — `~/.claude/rules/*.md` (recursive) and `<project>/.claude/rules/*.md` (recursive). Different from Memory; supports `paths:` frontmatter to scope rules to specific paths in the repo. Likely a new `entity.KindRule`.
- [ ] **Subagent auto-memory** — `~/.claude/agent-memory/<agent-name>/`. Per-agent persistent state, distinct from project auto-memory.
- [ ] **Plugin-provided** skills, agents, commands, MCP servers — scan `~/.claude/plugins/<plugin>/{skills,agents,commands}/` and the plugin's bundled `.mcp.json` / inline `mcpServers` in `plugin.json`.

## Missing scopes

- [ ] **Managed scope** (enterprise-deployed) — system dirs:
  - macOS: `/Library/Application Support/ClaudeCode/`
  - Linux/WSL: `/etc/claude-code/`
  - Windows: `C:\Program Files\ClaudeCode\`
  Files: `managed-settings.json`, `managed-settings.d/*.json` (drop-in merged), `managed-mcp.json`, `CLAUDE.md`. Highest-precedence scope in Claude Code's hierarchy.
- [ ] **Nested `.claude/` discovery** — Claude Code walks up from cwd and also probes nested `<subdir>/.claude/skills/` for monorepo support. Our scanner only discovers the top-level `.claude/` per project.
- [ ] **Nested `CLAUDE.md`** — Claude Code loads `CLAUDE.md` files in subdirectories on demand; we only check the project root.

## Read-path quirks

- [ ] **Local-scope MCP name collision across projects** — `readSettingsBlock`'s fallback into `~/.claude.json`'s `projects.*.mcpServers` returns the first match by name, regardless of which project's MCPs the entity actually came from. Two projects with a same-named Local-scope MCP would conflate. The right fix is to extend the `Source` interface to pass an entity ID or scope to `Read()` so the source can disambiguate.
- [ ] **`<project>/.claude/CLAUDE.md` Write** — `filePath()` for `KindClaudeMD` always writes to `<projectRoot>/CLAUDE.md`. If the entity was discovered at `<projectRoot>/.claude/CLAUDE.md`, a Write will create a competing file at the alternate location instead of updating the original.
- [ ] **`CLAUDE.local.md` Write** — same issue: `filePath()` doesn't know about the local-override sibling.

## Project-path encoding

- [ ] **Lossy encoding decode** — Claude Code encodes project paths as `/` → `-` for `~/.claude.json`'s `projects` map keys and the `~/.claude/projects/<encoded>/` dir name. Our `decodeProjectPath()` reverses with naive `strings.ReplaceAll(s, "-", "/")`, which produces the wrong path for repos like `/home/u/my-project`. Match against known project roots (from `discoverDockerSources` / config) before falling back to the naive decode.
- [ ] **`autoMemoryDirectory` setting** — users can override Claude Code's auto-memory location via this user/local setting. We always assume `~/.claude/projects/`. Honor the override if present.

## Plugin/MCP feature support

- [ ] **`${VAR}` / `${VAR:-default}` expansion** — supported in `.mcp.json` for `command`, `args`, `env`, `url`, `headers`. We display literal values; consider expanding them in the UI (or showing both the raw and expanded form).
- [ ] **`oauth`, `headersHelper`, `alwaysLoad`, `authServerMetadataUrl`** — additional MCP server fields per docs. We don't surface them in the structured card.
- [ ] **`anthropic/maxResultSizeChars` annotation** on tools (an MCP server-side concern, not infra-mngmt's, but worth flagging if we ever add an MCP probe).

## UX adjacent

- [ ] **Skills/Commands name-collision badge** — when a skill and command share a name, skills win and commands are effectively dead. Surface this as a warning on the affected command card.
- [ ] **Project tab inclusion of nested-project memories** — currently the global source emits auto-memory entities scoped to a decoded project path. If that project doesn't already have a tab (no `.claude/` directory etc.), the entities surface but are mis-grouped. Consider auto-creating tabs for projects that exist only via auto-memory.

## Promote / cross-source copy

The `POST /api/promote` flow ([`internal/web/promote.go`](internal/web/promote.go)) and the `ReadFiles` / `WriteFiles` / `Has` additions to `Source` cover host→host copies for every kind. Remaining gaps:

- [ ] **Volume / container as a write target** — `DockerVolumeSource.WriteFiles` returns `ErrReadOnly`. Vol→host promote works (the source's `ReadFiles` handles skill enumeration via `docker.ListVolume`), but host→vol does not. The picker shows volume sources as candidates; the user only learns it's unsupported after picking. Either implement volume writes via a sidecar (`docker run --rm -v <vol>:/data alpine sh -c 'mkdir -p ... && cat > ...'`) or pre-filter read-only sources out of the picker.
- [ ] **`ContainerAgentSource`** doesn't exist yet ([CLAUDE.md](CLAUDE.md) lists it as future). Once it lands, both `ReadFiles` and `WriteFiles` need first-class implementations — the agent should expose tar streaming so skill multi-file payloads round-trip without N round-trips per file.
- [ ] **Settings-backed targets only write to `<base>/settings.json`** — `HostFSSource.spliceSettings` doesn't know about User-scope `~/.claude.json` (top-level `mcpServers`) or Project-scope `<repo>/.mcp.json`. Promoting an MCP from a project's `.mcp.json` into the global host source ends up in `~/.claude/settings.json`, which Claude Code does pick up but isn't where Claude Code itself writes User-scope MCPs. Decide whether to mirror Claude Code's preferred file per scope, or stay with one canonical destination and document it.
- [x] **Skill copy is additive, not a mirror** — `writeSkillFiles` overwrites overlapping files but leaves stale files at the destination (e.g. removed scripts from the source skill). Add an `?mirror=true` opt-in or a separate "sync" verb that wipes the destination dir first. _Done: `Source.Clear` primitive + `?mirror=true` query + "mirror copy" checkbox in the picker. Skills get the cleanup; single-file kinds are no-ops._
- [ ] **CLAUDE.md alternate-location write** — `claudeMDPathForName` only knows about `<repoRoot>/CLAUDE.md` and `<repoRoot>/CLAUDE.local.md`. If the entity was discovered at `<repoRoot>/.claude/CLAUDE.md`, promote will write to `<repoRoot>/CLAUDE.md` instead of the original location. Same root cause as the existing read-path TODO, but now it bites the promote flow too — fixing both probably means propagating the entity's `Path` (or a stable per-entity write target) through the source layer.
- [ ] **Read-only target feedback in the picker** — currently every non-self source is rendered as a button; only after clicking does the user learn the destination rejects writes. Either filter the picker to writable sources, or annotate read-only rows with a badge and a tooltip ("Docker volume — write support pending").
- [ ] **No move semantics** — current scope is copy-only by user request. If "promote" should also delete the source entry on success (true "promote" rather than "copy"), needs a delete path on `Source` that doesn't exist yet.
- [ ] **No undo** — overwrite is destructive once confirmed. Consider keeping a one-deep `.bak` of the previous payload for skills + settings blocks, surfaced via an "undo last promote" affordance for the active session.
- [ ] **Bulk promote** — UI is one-entity-at-a-time. A common workflow is "lift all of project A's skills + commands to global"; a multi-select + batch endpoint would save a lot of clicking once people start using this.

## Volume IO performance — beyond the current cache layer

Three optimizations landed (singleflight, TTL cache, `docker exec` when a container with the volume is running). The remaining big swing is **long-lived sidecars / bind-mounted volumes / push-based invalidation**, captured here so we can come back to it once we hit the wall.

- [ ] **Long-lived sidecar with a JSON-RPC protocol** — Replace the per-call `docker run` with a single `docker run -d` per volume that runs a tiny stdin/stdout server (or a Unix socket inside the volume). infra-mngmt sends `{"op":"cat","path":"foo"}` and reads framed responses. Pros: amortizes container creation across requests; first-request latency drops to ~0 once the container is up. Cons:
    - Lifecycle: leaks on infra-mngmt crash unless we use `docker run --rm` and a shutdown hook (and even then, host reboots can orphan containers).
    - Concurrency: stdin/stdout is a single stream; concurrent reads must serialize or open multiple exec streams. Singleflight masks most of this.
    - Protocol fragility: shell-based `eval` over stdin breaks on weird inputs; a real protocol means shipping our own image.
    - Recovery: if the sidecar exits (OOM, host kill), every IO has to detect EOF and respawn.
    - Marginal value: option 3 (exec into running container) already reaches near-native speed in the common case (active devcontainer); the long-lived sidecar only helps when *no* container is running and the cache is cold. The current 30s `volumeCacheTTL` makes that a narrow window.
  Recommended path forward when needed: package a small Go agent (`infra-mngmt-volagent`) as a baked image, use a length-prefixed JSON protocol over `docker attach`, and add a per-volume mutex.

- [ ] **Bind-mount the volumes into infra-mngmt's own container** — At startup, list managed containers, discover their `.claude` volumes, and request the same volume mounts on the infra-mngmt service itself. Then `DockerVolumeSource` collapses into `HostFSSource` reading from `/mounted-volumes/<name>/`. Pros: zero IPC, no exec, no sidecar; reads are filesystem-fast and writable for free. Cons:
    - Volumes must be known at infra-mngmt startup (or service must restart when new ones appear).
    - Requires the supervisor (process-compose) to support remounting on reload.
    - `/var/lib/docker/volumes/<name>/_data` is the moral equivalent and is sometimes accessible directly with the right permissions, but that's host-OS-specific.
  Net: structurally simpler than long-lived sidecars and probably worth the architectural shift before adding more sidecar machinery.

- [ ] **inotify-driven push invalidation** — Once we own the agent (either the long-lived sidecar or the bind-mount path), have it watch `/data/` with inotify and emit events to infra-mngmt. Lets `Source.Watch()` finally return useful data, drop the 30s `volumeCacheTTL`, and serve always-fresh reads. Major UX win for the entity list (shows external edits immediately) but requires the bind-mount or long-lived-sidecar foundation first.

- [ ] **`Source.ReadMany([]Spec)`** — One sidecar/exec call pulls many files at once. Big win when the picker / preview navigates a skill with N files; eliminated by the bind-mount path entirely. Worth doing iff we stay on the sidecar/exec architecture.

## Tests / verification

- [ ] **Empirical confirmation of User-scope MCP key path in `~/.claude.json`** — docs only say "stored in `~/.claude.json`" without specifying the JSON key. We assume top-level `mcpServers`; should be confirmed against a real Claude Code install.
- [ ] **Encoded-path round-trip** — capture real `~/.claude/projects/` directory names from a few projects with hyphens, dots, and unusual characters in their paths, and confirm our decoder produces the right project root for each.
