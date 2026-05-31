# infra-mngmt

Unified Claude Code config viewer + process manager for Windows + WSL2 + devcontainer environments. See [README.md](README.md) for setup instructions.

## What it does

- Surfaces all Claude Code entities (skills, hooks, MCP servers, rules, agents, memory) across scopes: host `~/.claude/`, every project's `.claude/`, and inside devcontainers (bind-mounted or Docker volumes)
- Shows runtime status of services those entities depend on (llama.cpp on Windows, socat bridges in WSL, MCP servers anywhere)
- Detects cross-boundary broken references (MCP config pointing at a stopped Windows service, skill invoking a missing script)
- Lets you start/stop services and promote/copy entities between scopes

## Architecture

The central abstraction is `Source` — every source of `.claude/` state implements one interface regardless of where data lives:

```go
type Source interface {
    ID() string   // e.g. "host:~/.claude", "vol:claude-code-config", "ctr:abc123"
    Scope() Scope // Global | Project(repoPath)
    Entities(ctx context.Context) ([]Entity, error)
    Read(ctx context.Context, kind Kind, name string) ([]byte, error)
    Write(ctx context.Context, kind Kind, name string, data []byte) error // ErrReadOnly if not writable
    Watch(ctx context.Context) (<-chan ChangeEvent, error) // nil = no live updates
}
```

Two implementations:

- `HostFSSource` — direct filesystem read/write
- `DockerVolumeSource` — throwaway sidecar container for read/write; no Watch

Planned: `ContainerAgentSource` — HTTP to an optional in-container agent with full Watch support (not yet implemented).

Process management is delegated to [process-compose](https://github.com/F1bonacc1/process-compose) instances running on each tier (Windows host, WSL, optionally inside containers). The backend holds HTTP clients to their REST APIs and can start/stop process-compose itself when an endpoint is unreachable.

## Tech stack

| Layer       | Choice                                            | Reason                                                                                                                                                                             |
| ----------- | ------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Backend     | Go                                                | Goroutines for concurrent source watching; single static binary                                                                                                                    |
| Docker API  | `github.com/docker/docker/client` (official SDK)  | Strong types, API version negotiation, `stdcopy.StdCopy` for log demux, native support for events/exec/cp                                                                          |
| HTTP server | `net/http` + [chi](https://github.com/go-chi/chi) | Lightweight, idiomatic                                                                                                                                                             |
| Frontend    | Go templates + HTMX                               | No JS build pipeline; server-side rendering. See [docs/frontend-architecture.md](docs/frontend-architecture.md).                                                                  |
| Search      | fuse.js (vendored, `internal/web/static/vendor/`) | ⌘K across entities without a build step                                                                                                                                            |
| Config      | `gopkg.in/yaml.v3`                                | YAML config at `~/.config/infra-mngmt/config.yaml` (loader still falls back to legacy `config.json` with a deprecation log)                                                        |

## Project structure

```
cmd/
  infra-mngmt/
    main.go              # CLI: server, addr, bridges, version subcommands
internal/
  source/                # Source implementations (hostfs, dockervol)
  entity/                # Entity model: Kind, Scope, Entity (with Attrs map)
  docker/                # Docker client: managed-container discovery, sidecar IO, ContainerStats
  compose/               # process-compose REST client (incl. bootstrap)
  bridge/                # bridges.yaml: types, load, state, apply (PowerShell for windows tier;
                         # composegen.go renders a process-compose fragment for wsl/socat tier)
  containers/            # containers.yaml: declared-container declarations + load
  deps/                  # dependencies.yaml: rules, scope-pattern matching
  rates/                 # model-rates.yaml: per-model token pricing for the OD card cost rollup
  llama/                 # llama.cpp REST client (health, props, models, metrics, slots)
  vault/                 # mcp-fs vault client (allowlist + tree browse + grant/revoke)
  graph/
    refs.go              # Resolver: deps + legacy substring fallback; bridge/process/container state rollup
  web/                   # HTTP server + frontend assets — see docs/frontend-architecture.md
    server.go            # HTTP router, middleware, auth
    handlers.go          # Entity + process routes; container stats fan-out; FuncMap
    bridges.go           # bridge views + apply/reset/refresh handlers
    containers.go        # declared-container views + OD card aggregation (per-model rollup + cost)
    vault.go             # mcp-fs vault panel handlers
    status.go            # MCP runtime status resolver (uses graph package)
    static.go            # embed.FS mount for /static/*; static FuncMap helper
    templates.go         # embed.FS + parseTemplate helper for templates/*.html.tmpl
    templates/           # one .html.tmpl per page/partial (no Go-string templates)
    static/
      css/app.css        # shared design tokens + component CSS
      js/                # ES modules: app.js (shared), preview.js, containers.js, promote.js, wake.js
      vendor/            # htmx, fuse, marked, codemirror.bundle.js + LICENSES.md
config/
  config.go              # App config: YAML primary, JSON fallback for legacy installs
  wsl.go                 # `wsl-windows` sentinel → Windows host IP via /proc/net/route
go.mod
go.sum
```

## Development commands

```bash
make build                         # Compile to dist/infra-mngmt
make run                           # Build + start the server
make test                          # Full test suite
make test-cover                    # With per-package coverage summary
make fmt                           # Apply gofmt -w to the tree
make fmt-check                     # Fail if anything needs reformatting
make vet                           # `go vet ./...`
make lint                          # golangci-lint with .golangci.yml
make check                         # fmt-check + vet + lint + test (pre-commit gate)
make tidy                          # `go mod tidy`
```

**Always run `make check` before declaring work done.** It enforces formatting, the `go vet` baseline, the `golangci-lint` set in `.golangci.yml` (errcheck, govet, ineffassign, staticcheck, unused, bodyclose, misspell, unconvert), and the full test suite. Bare `go test ./...` is not enough — the lint gate catches unclosed bodies, misspellings, and dead code that tests won't.

The devcontainer firewall allowlists the Go module proxy (`proxy.golang.org`, `sum.golang.org`, `dl.google.com`, etc.) — see `.devcontainer/init-firewall.sh`. Standard `go mod tidy` and toolchain auto-upgrade work without env overrides.

## Testing

See [TESTING.md](TESTING.md) for the test layout, mock patterns, coverage baseline, and the full process for keeping the suite in sync with the code. Highlights to enforce on every change:

- **`go test ./...` must be green before declaring work done.** Never commit a failing test or a skipped one without a comment explaining why.
- **Tests are part of every change**, not a follow-up:
  - New pure function (parser, formatter, mapper) → unit test in the same package, happy path + at least one edge case.
  - New HTTP handler → test in `internal/web/handlers_http_test.go` using the in-memory `mockSource` — at least the success path and the relevant failure cases (missing id, not-found, permission).
  - New multi-step user flow (auth, redirects, cookies, cache invalidation across requests) → test in `internal/web/e2e_test.go` using the `e2eEnv` harness; single-request handler tests can't catch session/redirect/cache bugs.
  - New source-kind or path-mapping logic → test the path mapping plus read/write roundtrip if writable.
  - New entity kind → update `kindIcon` and extend `TestKindIcon`.
  - Bug fix → add a regression test that fails before the fix and passes after.
  - Refactor → existing tests pass without modification; if they don't, observable behavior changed and that needs its own test.
- **Don't test pure logic through HTTP.** Unit-test it directly; reserve handler tests for routing, status codes, auth, and contracts.
- **Mock `source.Source`, fake `httptest.Server`, real `t.TempDir()`** — established patterns in the existing tests; use them, don't invent new ones.

## App config

Stored at `~/.config/infra-mngmt/config.yaml`. Auto-created on first run with defaults. The loader still accepts `config.json` for legacy installs and logs a deprecation warning.

```yaml
bind: 127.0.0.1:7842
token_file: ~/.config/infra-mngmt/token
process_compose:
  - name: wsl
    endpoint: http://localhost:9998
    binary: /usr/local/bin/process-compose
    compose_file: ~/.config/infra-mngmt/process-compose.yaml
    token_file: ~/.config/infra-mngmt/process-compose.token
  - name: windows
    endpoint: http://wsl-windows:9999
    binary: /c/Users/<user>/AppData/Local/Programs/process-compose/process-compose.exe
    compose_file: /c/Users/<user>/.config/infra-mngmt/process-compose.yaml
    token_file: /c/Users/<user>/.config/infra-mngmt/process-compose.token
trusted_networks: [127.0.0.0/8, ::1/128] # loopback only — NOT the Docker bridge; headless clients (devcontainer deploy) send Authorization: Bearer instead
extra_paths: []
```

Full annotated field reference: README §3.

`wsl-windows` in an endpoint is a sentinel resolved at startup to the Windows host IP (the WSL guest's default-route gateway), needed because WSL2 NAT mode changes that IP on each restart — see README §4 and `config/wsl.go`.

`bridges.yaml`, `dependencies.yaml`, `containers.yaml`, and `model-rates.yaml` live alongside `config.yaml` and are auto-discovered (or pointed at via `bridges_file`/`dependencies_file`/`containers_file`/`model_rates_file` in the main config). `model-rates.yaml` is optional — when absent, the OD card's `≈ cost` slot renders `—` for every model (no built-in fallback rates).

For `tier: wsl, type: socat` bridges, infra-mngmt also generates `process-compose.bridges.yaml` (path configurable via `bridges_compose_file`) — a fragment the user's main `process-compose.yaml` includes via `extends:`, with entries under namespace `bridges`. See README §4 and `.claude/skills/infra-mngmt-config/SKILL.md` for the schema and runtime model.

process-compose YAML files are **infrastructure config**, not Claude Code config — they do not belong in `.claude/` directories. Use `~/.config/infra-mngmt/` or any path the `compose_file` field points to.

## Key design decisions to preserve

1. **Source is the only seam** — route handlers never touch the filesystem or Docker directly; they call sources. This keeps adding new source types cheap.

2. **process-compose bootstrap** — the app must be able to _start_ process-compose, not just query it. Check endpoint reachability on startup; surface start button in UI if unreachable.

3. **Scope mirrors Claude Code** — global (`~/.claude/`) and per-project (`.claude/` in repo root) scopes only. Don't invent new ones.

4. **Devcontainer auto-discovery** — primary discovery uses the standard `devcontainer.local_folder` label set by VS Code/devcontainer CLI. `claude.managed=true` is the explicit opt-in fallback for non-devcontainer containers. Never use container naming conventions.

5. **Web-first, Tauri later** — keep the UI purely server-rendered + HTMX. Tauri is an upgrade path, not a constraint.

6. **Docker SDK pinning** — `github.com/docker/docker v27.5.1+incompatible` with explicit `github.com/docker/go-connections v0.5.0` (newer versions remove `sockets.DialPipe` which v27 still references) and `github.com/pkg/errors v0.9.1+` (earlier versions lack `errors.As`/`Is`). The SDK pulls in OpenTelemetry as a transitive dep; the firewall now allows the Go infra domains so this is fine, but resist upgrading to v28+ until those breaking changes settle.

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

- [docs/frontend-architecture.md](docs/frontend-architecture.md) — frontend stack, file layout, interaction model, JS/CSS/template conventions, when to introduce a JS island. Binding for changes under [internal/web/](internal/web/).
- [TESTING.md](TESTING.md) — test layout, mock patterns, coverage baseline
- [docs/proposals/](docs/proposals/) — design proposals for in-flight work
- [CCM](https://github.com/dustinlacewell/claude-config-manager) — UX and scope model reference (TypeScript, MIT)
- [process-compose](https://github.com/F1bonacc1/process-compose) — runtime supervisor
- [trailofbits/claude-code-devcontainer](https://github.com/trailofbits/claude-code-devcontainer) — Docker label patterns and volume sync
