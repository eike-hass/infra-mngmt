# Testing

The suite covers parsers, source implementations, the cross-reference resolver,
the compose REST client, the auth flow, and HTTP handler contracts — without
requiring Docker, process-compose, or a network.

## Running

```bash
make test                      # all tests
make test-cover                # with coverage by package
make check                     # fmt + vet + lint + test (pre-commit gate)

go test -run TestFoo ./pkg     # single test
go test -v ./internal/web      # verbose, single package
```

`make check` must pass before any change is considered done. It enforces:

- `gofmt -l .` produces empty output (no unformatted files)
- `go vet ./...` passes
- `golangci-lint run` passes against `.golangci.yml` (errcheck, govet, ineffassign, staticcheck, unused, bodyclose, misspell, unconvert)
- `go test ./...` passes

Bare `go test ./...` will not catch lint regressions — always use `make check`.

## Layout

Tests live next to the code they cover:

| Package | Test files | Covers |
|---|---|---|
| `cmd/infra-mngmt/` | `main_test.go` | Subcommand dispatch, addr/version/bridges CLI surface |
| `config/` | `config_test.go` | YAML primary + JSON fallback Load/Save, token gen, `wsl-windows` endpoint resolution |
| `internal/entity/` | `types_test.go` | `Scope`, `Kind` helpers |
| `internal/source/` | `hostfs_test.go`, `dockervol_test.go` | Source implementations and path↔entity mapping |
| `internal/docker/` | `client_test.go` | Path normalization, frame parsing, `toManaged` label/mount logic, `cpuPercent`/`memUsage` for stats |
| `internal/compose/` | `client_test.go` | REST client driven by `httptest.Server` (start uses POST, stop uses PATCH; reload posts to `/project/configuration`) |
| `internal/bridge/` | `apply_test.go`, `load_test.go`, `state_test.go`, `runner_test.go`, `types_test.go` | bridges.yaml schema, PowerShell payload generation, portproxy state parsing |
| `internal/containers/` | `load_test.go`, `types_test.go` | containers.yaml schema + load |
| `internal/deps/` | `load_test.go`, `match_test.go`, `types_test.go` | dependencies.yaml schema, scope-pattern rule matching |
| `internal/graph/` | `refs_test.go` | dependencies-driven resolution + legacy substring fallback, bridge/container state rollup |
| `internal/rates/` | `rates_test.go`, `testutil_test.go` | model-rates.yaml load (missing file → empty map, valid YAML, bad YAML) |
| `internal/llama/` | `client_test.go` | llama.cpp REST client (health, props, /v1/models, /metrics, /slots probe + parse) |
| `internal/vault/` | `client_test.go` | mcp-fs vault control-plane client (allowlist, tree, grant/revoke) |
| `internal/web/` | `helpers_test.go`, `handlers_http_test.go`, `template_test.go`, `template_render_test.go`, `e2e_test.go`, `status_test.go`, `open_design_test.go`, `frontend_guidelines_test.go` | Helper funcs, HTTP routing/auth, template render, end-to-end flows, OD card aggregation + rendering, frontend-rules enforcement |

## Patterns

**Filesystem tests** — use `t.TempDir()`. See `hostfs_test.go:buildClaudeDir`
for a reusable `.claude/` fixture builder.

**HTTP handler tests** — use the in-memory `mockSource` defined at the top of
`handlers_http_test.go`. Don't touch the real filesystem when testing handlers.

```go
m := newMockSource("host:/x", entity.GlobalScope())
m.addEntity(entity.KindCommand, "run", []byte("body"))
srv := newServerWithSource(m)
rr := httptest.NewRecorder()
srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/entities", nil))
```

**Outbound HTTP clients** — drive against `httptest.NewServer`. See
`internal/compose/client_test.go`.

**End-to-end flows** — `internal/web/e2e_test.go` wraps the real `Server` in
`httptest.NewServer` and uses an `http.Client` with a cookie jar (no auto-redirect
follow) to drive multi-step user flows: login → list → preview → edit → logout.
The harness lives in the same file (`e2eEnv`, `newE2E`, `login`, `get`, `post`,
`getJSON`); add new flows alongside the existing ones rather than duplicating
the setup. These are the only tests that exercise middleware + cookies + actual
network round-trips end-to-end.

**Don't test internals through HTTP** — if a function is pure logic, unit-test
it directly. HTTP tests should cover routing, status codes, and auth — not
parsing details that already have unit coverage.

## Coverage baseline

Refreshed via `make test-cover` (statement coverage).

| Package | Coverage |
|---|---|
| `internal/entity` | 100% |
| `internal/containers` | 96% |
| `internal/rates` | 92% |
| `internal/deps` | 86% |
| `internal/vault` | 86% |
| `internal/llama` | 81% |
| `cmd/wake-proxy` | 81% |
| `internal/bridge` | 79% |
| `internal/graph` | 75% |
| `config` | 73% |
| `internal/source` | 66% |
| `internal/web` | 64% |
| `internal/compose` | 60% |
| `internal/docker` | 34% (pure helpers + volume cache; the live HTTP/exec path needs a Docker socket) |
| `cmd/infra-mngmt` | 7% (mostly `main()` wiring — not unit-testable; logic lives in tested packages) |

A regression below these numbers warrants a note in the PR explaining why.

## Intentionally not covered

- Docker `Client` HTTP methods (`ListManaged`, `StreamLogs`, `InspectContainer`,
  sidecar volume IO) — would need a fake Docker socket fixture
- SSE log streaming end-to-end
- Frontend JavaScript

These are validated by manual integration testing today. Closing them is
welcome — start with a `httptest.Server` that mimics the Docker daemon's
multiplexed log frames and `/containers/{id}/json` inspect responses.

## Process: tests are part of every change

When adding a feature, fixing a bug, or changing observable behavior:

1. **New pure function** (parser, formatter, mapper) → unit test in the same
   package covering happy path + at least one edge case.
2. **New HTTP handler** → add to `handlers_http_test.go` with `mockSource` —
   minimum: success path, missing-id 400 (if applicable), not-found 404,
   permission errors (read-only sources, missing docker, etc.).
3. **New source kind or path mapping** → test the path mapping plus a
   read/write roundtrip if writable.
4. **New entity kind** → update `kindIcon` and extend `TestKindIcon`; if it
   changes scoping rules also extend `TestSourceLevel`.
5. **New Docker label or container-discovery logic** → test in `toManaged` /
   `toContainerInfo` cases.
6. **Bug fix** → add a regression test that fails before the fix and passes
   after. If the bug was non-obvious, note it in a comment on the test.
7. **Refactor** → existing tests should pass without modification. If they
   don't, the refactor changed observable behavior — re-evaluate.
8. **New multi-step user flow** (anything spanning more than one request, or
   touching cookies/redirects/middleware) → add an E2E test in
   `internal/web/e2e_test.go` using the `e2eEnv` harness. These are the tests
   that catch wrong-redirect, missing-cookie, and stale-cache bugs that pass
   single-request tests.

Tests live next to the code, are intentionally simple, and table-driven where
useful. Don't over-engineer. The goal is regression coverage, not architectural
elegance.
