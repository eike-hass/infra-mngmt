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

## Tests / verification

- [ ] **Empirical confirmation of User-scope MCP key path in `~/.claude.json`** — docs only say "stored in `~/.claude.json`" without specifying the JSON key. We assume top-level `mcpServers`; should be confirmed against a real Claude Code install.
- [ ] **Encoded-path round-trip** — capture real `~/.claude/projects/` directory names from a few projects with hyphens, dots, and unusual characters in their paths, and confirm our decoder produces the right project root for each.
