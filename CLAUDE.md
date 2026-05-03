# infra-mngmt

Unified Claude Code config viewer + process manager for Windows + WSL2 + devcontainer environments. See [README.md](README.md) for setup instructions.

## What it does

- Surfaces all Claude Code entities (skills, hooks, MCP servers, rules, agents, memory) across scopes: host `~/.claude/`, every project's `.claude/`, and inside devcontainers (bind-mounted or Docker volumes)
- Shows runtime status of services those entities depend on (llama.cpp on Windows, socat bridges in WSL, MCP servers anywhere)
- Detects cross-boundary broken references (MCP config pointing at a stopped Windows service, skill invoking a missing script)
- Lets you start/stop services and promote/copy entities between scopes

## Architecture

The central abstraction is `EntitySource` — every source of `.claude/` state implements one interface regardless of where data lives:

```go
type EntitySource interface {
    ID() string   // e.g. "host:/home/user/.claude", "vol:claude-code-config", "ctr:abc123"
    Scope() Scope // Global | Project(repoPath)
    Entities(ctx context.Context) ([]Entity, error)
    Read(ctx context.Context, kind Kind, name string) ([]byte, error)
    Write(ctx context.Context, kind Kind, name string, data []byte) error // ErrReadOnly if not writable
    Watch(ctx context.Context) (<-chan ChangeEvent, error) // nil = no live updates
}
```

Three implementations:
- `HostFSSource` — direct filesystem read/write
- `DockerVolumeSource` — throwaway sidecar container for read/write; no Watch
- `ContainerAgentSource` — HTTP to optional in-container agent; full Watch support (future)

Process management is delegated to [process-compose](https://github.com/F1bonacc1/process-compose) instances running on each tier (Windows host, WSL, optionally inside containers). The backend holds HTTP clients to their REST APIs and can start/stop process-compose itself when an endpoint is unreachable.

## Tech stack

| Layer | Choice | Reason |
|---|---|---|
| Backend | Go | Goroutines for concurrent source watching; single static binary |
| Docker API | stdlib `net/http` over Unix socket | No external deps; targets exactly the 6 Docker API calls needed |
| HTTP server | `net/http` + [chi](https://github.com/go-chi/chi) | Lightweight, idiomatic |
| Frontend | Go templates + HTMX | No JS build pipeline; server-side rendering |
| Search | fuse.js (CDN) | ⌘K across entities without a build step |
| Config | `encoding/json` | No firewall-blocked deps; JSON config at `~/.config/infra-mngmt/config.json` |

## Project structure

```
cmd/
  infra-mngmt/
    main.go              # CLI flags, config load, server start
internal/
  source/
    source.go            # Source interface + shared types (ErrReadOnly, ErrNotFound, ChangeEvent)
    hostfs.go            # HostFSSource — direct filesystem
    dockervol.go         # DockerVolumeSource — sidecar pattern
  entity/
    types.go             # Entity model: Kind, Scope, Entity (with Attrs map)
  docker/
    client.go            # Docker socket REST client (pure stdlib)
    labels.go            # devcontainer + claude.managed label constants
  compose/
    client.go            # process-compose REST API client
    bootstrap.go         # start/stop process-compose itself
  graph/
    refs.go              # Cross-reference resolution: MCP → process matching, broken-ref detection
  web/
    server.go            # HTTP router, middleware
    handlers.go          # Route handlers
    status.go            # MCP runtime status resolver (uses graph package)
    template.go          # Inline Go HTML templates (index, preview, services, logs)
config/
  config.go              # App config schema + Load/Save
  wsl.go                 # WSL2 NAT helper: resolves "wsl-windows" hostname from /etc/resolv.conf
go.mod
go.sum
```

## Development commands

```bash
go run ./cmd/infra-mngmt           # Run (re-run manually for changes)
go build -o dist/infra-mngmt ./cmd/infra-mngmt  # Build binary
go test ./...                      # Run tests
go vet ./...                       # Vet
```

The devcontainer firewall blocks the Go module proxy. Use `GOPROXY=direct GONOSUMDB='*'` when running `go mod tidy` or adding dependencies.

## App config

Stored at `~/.config/infra-mngmt/config.json`. Auto-created on first run with defaults.

```json
{
  "bind": "127.0.0.1:7842",
  "process_compose": [
    {
      "name": "wsl",
      "endpoint": "http://localhost:9998",
      "binary": "/usr/local/bin/process-compose",
      "compose_file": "/home/user/.config/infra-mngmt/process-compose.yaml"
    },
    {
      "name": "windows",
      "endpoint": "http://wsl-windows:9999",
      "binary": "/mnt/c/tools/process-compose/process-compose.exe",
      "compose_file": "/mnt/c/Users/user/.config/infra-mngmt/process-compose.yaml"
    }
  ],
  "extra_paths": []
}
```

`wsl-windows` in an endpoint is a sentinel resolved at startup to the Windows host IP from `/etc/resolv.conf` (see `config/wsl.go`). Necessary for WSL2 NAT mode where the gateway IP changes on each restart.

process-compose YAML files are **infrastructure config**, not Claude Code config — they do not belong in `.claude/` directories. Use `~/.config/infra-mngmt/` or any path the `compose_file` field points to.

## Key design decisions to preserve

1. **EntitySource is the only seam** — route handlers never touch the filesystem or Docker directly; they call sources. This keeps adding new source types cheap.

2. **process-compose bootstrap** — the app must be able to *start* process-compose, not just query it. Check endpoint reachability on startup; surface start button in UI if unreachable.

3. **Scope mirrors Claude Code** — global (`~/.claude/`) and per-project (`.claude/` in repo root) scopes only. Don't invent new ones.

4. **Devcontainer auto-discovery** — primary discovery uses the standard `devcontainer.local_folder` label set by VS Code/devcontainer CLI. `claude.managed=true` is the explicit opt-in fallback for non-devcontainer containers. Never use container naming conventions.

5. **Web-first, Tauri later** — keep the UI purely server-rendered + HTMX. Tauri is an upgrade path, not a constraint.

6. **No external deps beyond chi** — the Docker SDK (`github.com/docker/docker`) pulls in OpenTelemetry which is blocked by the devcontainer firewall. Use pure stdlib HTTP over the Unix socket instead.

## Build order (MVP = steps 1–3)

1. ✅ Backend skeleton: config load, `HostFSSource`, chi router, entity list endpoint
2. ✅ Read-only entity viewer: scope tabs, entity cards, ⌘K search
3. ✅ Docker source: devcontainer label discovery, bind-mount detection, volume sidecar
   > **MVP** — useful as a cross-scope viewer right here
4. ✅ process-compose integration: connect, start/stop, log tail, bootstrap
5. ✅ Runtime status in entity views: badges on MCP cards, services panel
6. ✅ Cross-reference resolution: parse MCP host refs, match to known services, broken-ref detection
7. Dependency graph + broken-reference UI (blast radius before rename/move/delete)

## References

- [CCM](https://github.com/dustinlacewell/claude-config-manager) — UX and scope model reference (TypeScript, MIT)
- [process-compose](https://github.com/F1bonacc1/process-compose) — runtime supervisor
- [trailofbits/claude-code-devcontainer](https://github.com/trailofbits/claude-code-devcontainer) — Docker label patterns and volume sync
