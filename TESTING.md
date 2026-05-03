# Testing

The suite covers parsers, source implementations, the cross-reference resolver,
the compose REST client, the auth flow, and HTTP handler contracts — without
requiring Docker, process-compose, or a network.

## Running

```bash
go test ./...                  # all tests
go test -cover ./...           # with coverage by package
go test -run TestFoo ./pkg     # single test
go test -v ./internal/web      # verbose, single package
```

`go test ./...` must be green before any change is considered done.

## Layout

Tests live next to the code they cover:

| Package | Test files | Covers |
|---|---|---|
| `config/` | `config_test.go` | Load/Save round-trip, token gen, WSL endpoint resolution |
| `internal/entity/` | `types_test.go` | `Scope`, `Kind` helpers |
| `internal/source/` | `hostfs_test.go`, `dockervol_test.go` | Source implementations and path↔entity mapping |
| `internal/docker/` | `client_test.go` | Path normalization, frame parsing, `toManaged` label/mount logic |
| `internal/compose/` | `client_test.go` | REST client driven by `httptest.Server` |
| `internal/graph/` | `refs_test.go` | MCP↔process matching, broken-ref detection |
| `internal/web/` | `helpers_test.go`, `handlers_http_test.go`, `template_test.go`, `e2e_test.go` | Helper funcs, HTTP routing/auth, template smoke tests, end-to-end flows |

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

| Package | Coverage |
|---|---|
| `internal/entity` | 100% |
| `internal/graph` | 97% |
| `config` | 78% |
| `internal/compose` | 64% |
| `internal/source` | 62% |
| `internal/web` | 46% |
| `internal/docker` | 17% (pure helpers only — HTTP path needs a Docker socket) |

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
