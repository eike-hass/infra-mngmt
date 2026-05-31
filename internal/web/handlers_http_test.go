package web

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/eike-hass/infra-mngmt/internal/docker"
	"github.com/eike-hass/infra-mngmt/internal/entity"
	"github.com/eike-hass/infra-mngmt/internal/source"
)

// mockSource is an in-memory source.Source for testing.
type mockSource struct {
	id       string
	scope    entity.Scope
	entities []entity.Entity
	files    map[string][]byte // key = "<kind>:<name>"
	readOnly bool
	calls    int32 // count of Entities() calls (atomic)
}

func newMockSource(id string, scope entity.Scope) *mockSource {
	return &mockSource{id: id, scope: scope, files: map[string][]byte{}}
}

func (m *mockSource) ID() string          { return m.id }
func (m *mockSource) Scope() entity.Scope { return m.scope }
func (m *mockSource) Writable() bool      { return !m.readOnly }
func (m *mockSource) Entities(_ context.Context) ([]entity.Entity, error) {
	atomic.AddInt32(&m.calls, 1)
	return m.entities, nil
}
func (m *mockSource) Read(_ context.Context, kind entity.Kind, name string) ([]byte, error) {
	if v, ok := m.files[string(kind)+":"+name]; ok {
		return v, nil
	}
	return nil, source.ErrNotFound
}
func (m *mockSource) Write(_ context.Context, kind entity.Kind, name string, data []byte) error {
	if m.readOnly {
		return source.ErrReadOnly
	}
	m.files[string(kind)+":"+name] = data
	return nil
}
func (m *mockSource) Watch(_ context.Context) (<-chan source.ChangeEvent, error) {
	return nil, nil
}

func (m *mockSource) Has(_ context.Context, kind entity.Kind, name string) (bool, error) {
	for _, e := range m.entities {
		if e.Kind == kind && e.Name == name {
			return true, nil
		}
	}
	return false, nil
}

func (m *mockSource) ReadFiles(ctx context.Context, kind entity.Kind, name string) ([]source.EntityFile, error) {
	data, err := m.Read(ctx, kind, name)
	if err != nil {
		return nil, err
	}
	return []source.EntityFile{{Data: data}}, nil
}

func (m *mockSource) WriteFiles(ctx context.Context, kind entity.Kind, name string, files []source.EntityFile) error {
	if m.readOnly {
		return source.ErrReadOnly
	}
	if len(files) != 1 {
		// mockSource collapses multi-file payloads — sufficient for the
		// promote handler tests that don't exercise skill copy here.
		return source.ErrReadOnly
	}
	if err := m.Write(ctx, kind, name, files[0].Data); err != nil {
		return err
	}
	// Ensure the entity is listed (Write alone updates files map).
	for _, e := range m.entities {
		if e.Kind == kind && e.Name == name {
			return nil
		}
	}
	m.addEntity(kind, name, files[0].Data)
	return nil
}

func (m *mockSource) Clear(_ context.Context, kind entity.Kind, name string) error {
	if m.readOnly {
		return source.ErrReadOnly
	}
	key := string(kind) + ":" + name
	if _, ok := m.files[key]; !ok {
		// Also tolerate a missing entity (already cleared).
		found := false
		for _, e := range m.entities {
			if e.Kind == kind && e.Name == name {
				found = true
				break
			}
		}
		if !found {
			return source.ErrNotFound
		}
	}
	delete(m.files, key)
	out := m.entities[:0]
	for _, e := range m.entities {
		if e.Kind == kind && e.Name == name {
			continue
		}
		out = append(out, e)
	}
	m.entities = out
	return nil
}

func (m *mockSource) addEntity(kind entity.Kind, name string, content []byte) entity.Entity {
	e := entity.Entity{
		ID:     m.id + ":" + string(kind) + ":" + name,
		Kind:   kind,
		Name:   name,
		Scope:  m.scope,
		Source: m.id,
	}
	m.entities = append(m.entities, e)
	if content != nil {
		m.files[string(kind)+":"+name] = content
	}
	return e
}

func newServerWithSource(srcs ...source.Source) *Server {
	s := New(srcs, nil, "", nil, nil, nil, nil, nil, nil)
	return s
}

// ─── /api/version ───────────────────────────────────────────────────────────

func TestHandleVersionReturnsBuildInfo(t *testing.T) {
	srv := newServerWithSource(newMockSource("host:/x", entity.GlobalScope()))
	srv.SetBuildInfo(BuildInfo{
		BuildEpoch: "1700000000",
		Commit:     "abc12345",
		Dirty:      false,
		VCSTime:    "2023-11-14T22:13:20Z",
		GoVersion:  "go1.23.0",
	})
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/version", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rr.Code, rr.Body.String())
	}
	var got BuildInfo
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v; body = %s", err, rr.Body.String())
	}
	if got.BuildEpoch != "1700000000" {
		t.Errorf("BuildEpoch = %q", got.BuildEpoch)
	}
	if got.Commit != "abc12345" {
		t.Errorf("Commit = %q", got.Commit)
	}
	if got.GoVersion != "go1.23.0" {
		t.Errorf("GoVersion = %q", got.GoVersion)
	}
}

func TestHandleVersionUnauthenticatedAllowed(t *testing.T) {
	// /api/version is a public route — must succeed even when a token is set
	// AND the request comes from an untrusted source (no session cookie, no
	// allowlisted CIDR). Deploy scripts should be able to hit it without auth.
	srv := New(nil, nil, "secret-token", nil, nil, nil, nil, nil, nil)
	srv.SetBuildInfo(BuildInfo{BuildEpoch: "1700000000", Commit: "deadbeef"})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/version", nil)
	req.RemoteAddr = "8.8.8.8:1234" // not in trusted networks
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 even unauthenticated", rr.Code)
	}
}

// ─── /sw.js ─────────────────────────────────────────────────────────────────

// Service worker file is publicly fetchable (browsers fetch SWs without page
// context and without sending the auth cookie reliably) and served as JS so
// the browser will accept the registration.
func TestHandleServiceWorker_PublicAndCorrectType(t *testing.T) {
	srv := New(nil, nil, "secret-token", nil, nil, nil, nil, nil, nil)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/sw.js", nil)
	req.RemoteAddr = "8.8.8.8:1234" // unauthenticated
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (public); body = %s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/javascript" {
		t.Errorf("Content-Type = %q, want application/javascript", ct)
	}
	// Quick smoke check that the embedded body is actually our SW, not an
	// empty 200 or someone else's JS.
	body := rr.Body.String()
	if !strings.Contains(body, "infra-mngmt-shell-") {
		t.Errorf("/sw.js body doesn't look like our SW (missing CACHE_NAME prefix)")
	}
}

// ─── /api/sources ───────────────────────────────────────────────────────────

func TestHandleSources(t *testing.T) {
	m := newMockSource("host:/foo", entity.GlobalScope())
	srv := newServerWithSource(m)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/sources", nil))
	if rr.Code != 200 {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var got []map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0]["id"] != "host:/foo" || got[0]["scope"] != "global" {
		t.Errorf("got %v", got)
	}
}

// ─── /api/entities ──────────────────────────────────────────────────────────

func TestHandleEntities(t *testing.T) {
	m := newMockSource("host:/x", entity.GlobalScope())
	m.addEntity(entity.KindCommand, "run", []byte("body"))
	m.addEntity(entity.KindAgent, "helper", []byte("body"))

	srv := newServerWithSource(m)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/entities", nil))
	if rr.Code != 200 {
		t.Fatal(rr.Body.String())
	}
	var got []entity.Entity
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("got %d entities, want 2", len(got))
	}
}

func TestHandleEntitiesKindFilter(t *testing.T) {
	m := newMockSource("host:/x", entity.GlobalScope())
	m.addEntity(entity.KindCommand, "run", nil)
	m.addEntity(entity.KindAgent, "helper", nil)
	srv := newServerWithSource(m)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/entities?kind=command", nil))
	var got []entity.Entity
	json.Unmarshal(rr.Body.Bytes(), &got)
	if len(got) != 1 || got[0].Kind != entity.KindCommand {
		t.Errorf("kind filter not applied: %+v", got)
	}
}

// ─── /api/entity/content ────────────────────────────────────────────────────

func TestHandleEntityContent(t *testing.T) {
	m := newMockSource("host:/x", entity.GlobalScope())
	e := m.addEntity(entity.KindCommand, "run", []byte("hello"))
	srv := newServerWithSource(m)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/entity/content?id="+e.ID, nil))
	if rr.Code != 200 {
		t.Fatalf("%d: %s", rr.Code, rr.Body.String())
	}
	if rr.Body.String() != "hello" {
		t.Errorf("body = %q", rr.Body.String())
	}
}

func TestHandleEntityContentMissingID(t *testing.T) {
	srv := newServerWithSource(newMockSource("x", entity.GlobalScope()))
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/entity/content", nil))
	if rr.Code != 400 {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestHandleEntityContentNotFound(t *testing.T) {
	srv := newServerWithSource(newMockSource("x", entity.GlobalScope()))
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/entity/content?id=nope", nil))
	if rr.Code != 404 {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

// ─── POST /api/entity (write) ───────────────────────────────────────────────

func TestHandleEntityWrite(t *testing.T) {
	m := newMockSource("host:/x", entity.GlobalScope())
	e := m.addEntity(entity.KindCommand, "run", []byte("old"))
	srv := newServerWithSource(m)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/entity?id="+e.ID, strings.NewReader("new content"))
	srv.ServeHTTP(rr, req)
	if rr.Code != 204 {
		t.Fatalf("%d: %s", rr.Code, rr.Body.String())
	}
	if string(m.files["command:run"]) != "new content" {
		t.Errorf("file not updated: %q", m.files["command:run"])
	}
}

func TestHandleEntityWriteReadOnly(t *testing.T) {
	m := newMockSource("vol:x", entity.GlobalScope())
	m.readOnly = true
	e := m.addEntity(entity.KindCommand, "run", nil)
	srv := newServerWithSource(m)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/entity?id="+e.ID, strings.NewReader("x"))
	srv.ServeHTTP(rr, req)
	if rr.Code != 403 {
		t.Errorf("status = %d, want 403", rr.Code)
	}
}

func TestHandleEntityWriteTooLargeReturns413(t *testing.T) {
	m := newMockSource("host:/x", entity.GlobalScope())
	e := m.addEntity(entity.KindCommand, "big", []byte("old"))
	srv := newServerWithSource(m)
	// One byte over the cap must be rejected before the source is written.
	body := strings.NewReader(strings.Repeat("a", maxEntityBytes+1))
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/entity?id="+e.ID, body))
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413; body=%s", rr.Code, rr.Body.String())
	}
	if string(m.files["command:big"]) != "old" {
		t.Errorf("oversize write should not have touched the source, got %q", m.files["command:big"])
	}
}

// ─── /api/refresh ───────────────────────────────────────────────────────────

func TestHandleRefreshInvalidatesCache(t *testing.T) {
	m := newMockSource("host:/x", entity.GlobalScope())
	m.addEntity(entity.KindCommand, "run", nil)
	srv := newServerWithSource(m)

	// Prime the cache
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/entities", nil))
	cnt1 := atomic.LoadInt32(&m.calls)

	// Second request should hit cache (no new source call)
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/entities", nil))
	cnt2 := atomic.LoadInt32(&m.calls)
	if cnt2 != cnt1 {
		t.Errorf("expected cache hit; calls went %d → %d", cnt1, cnt2)
	}

	// Refresh and request again — should hit source.
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/refresh", nil))
	if rr.Code != 204 {
		t.Errorf("refresh status = %d", rr.Code)
	}
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/entities", nil))
	cnt3 := atomic.LoadInt32(&m.calls)
	if cnt3 != cnt2+1 {
		t.Errorf("expected fresh fetch after refresh; calls went %d → %d", cnt2, cnt3)
	}
}

// ─── /api/containers (no docker) ────────────────────────────────────────────

func TestHandleContainersNoDocker(t *testing.T) {
	srv := New(nil, nil, "", nil, nil, nil, nil, nil, nil) // dc = nil
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/containers", nil))
	if rr.Code != 200 {
		t.Fatalf("status = %d", rr.Code)
	}
	if strings.TrimSpace(rr.Body.String()) != "[]" {
		t.Errorf("expected empty array, got %q", rr.Body.String())
	}
}

func TestHandleContainerStartNoDocker(t *testing.T) {
	srv := New(nil, nil, "", nil, nil, nil, nil, nil, nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/container/start?id=abc", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rr.Code)
	}
}

func TestHandleContainerOpenVSCodeNoDocker(t *testing.T) {
	srv := New(nil, nil, "", nil, nil, nil, nil, nil, nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/container/open-vscode?id=abc", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rr.Code)
	}
}

func TestHandleContainerOpenVSCodeMissingID(t *testing.T) {
	// Passing nil docker would short-circuit before ID validation, so we
	// only validate the URI builder + the no-docker path here. The
	// missing-id branch is covered indirectly via the same code path.
	srv := New(nil, nil, "", nil, nil, nil, nil, nil, nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/container/open-vscode", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503 when docker not available, got %d", rr.Code)
	}
}

func TestBuildAttachedContainerURI(t *testing.T) {
	got := buildAttachedContainerURI("thirsty_moore", "/workspace")
	// {"containerName":"thirsty_moore"} hex-encoded
	want := "vscode-remote://attached-container+7b22636f6e7461696e65724e616d65223a227468697273747a5f6d6f6f7265227d/workspace"
	// Quick sanity check: the full hex is 60 chars + "vscode-remote://attached-container+" prefix + "/workspace" suffix
	if !strings.HasPrefix(got, "vscode-remote://attached-container+") {
		t.Fatalf("missing canonical prefix: %q", got)
	}
	if !strings.HasSuffix(got, "/workspace") {
		t.Errorf("missing workspace path suffix: %q", got)
	}
	// Decode the hex back to JSON and verify the envelope shape (more robust
	// than a fixed hex string — passes regardless of unicode escaping etc.).
	hexStart := len("vscode-remote://attached-container+")
	hexEnd := strings.LastIndex(got, "/")
	hexPart := got[hexStart:hexEnd]
	raw, err := hex.DecodeString(hexPart)
	if err != nil {
		t.Fatalf("hex segment is not valid hex: %v (in %q)", err, got)
	}
	var env struct {
		ContainerName string `json:"containerName"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("hex segment is not JSON: %v (raw=%q)", err, string(raw))
	}
	if env.ContainerName != "thirsty_moore" {
		t.Errorf("ContainerName = %q, want %q", env.ContainerName, "thirsty_moore")
	}
	_ = want // kept as documentation of the expected exact shape
}

func TestBuildAttachedContainerURIEmptyWorkspaceDefaultsToRoot(t *testing.T) {
	got := buildAttachedContainerURI("c1", "")
	if !strings.HasSuffix(got, "/") {
		t.Errorf("empty workspace should default to /, got %q", got)
	}
}

func TestBuildDevContainerURIRoundtrip(t *testing.T) {
	hostPath := `\\wsl.localhost\Ubuntu-18.04\home\eike\Workspace\open-design`
	got := buildDevContainerURI(hostPath, "/workspace")
	if !strings.HasPrefix(got, "vscode-remote://dev-container+") {
		t.Fatalf("missing canonical prefix: %q", got)
	}
	if !strings.HasSuffix(got, "/workspace") {
		t.Errorf("missing workspace path suffix: %q", got)
	}
	hexStart := len("vscode-remote://dev-container+")
	hexEnd := strings.LastIndex(got, "/")
	hexPart := got[hexStart:hexEnd]
	raw, err := hex.DecodeString(hexPart)
	if err != nil {
		t.Fatalf("hex segment is not valid hex: %v (in %q)", err, got)
	}
	var env struct {
		HostPath string `json:"hostPath"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("hex segment is not JSON: %v (raw=%q)", err, string(raw))
	}
	if env.HostPath != hostPath {
		t.Errorf("HostPath = %q, want %q", env.HostPath, hostPath)
	}
}

func TestFindDevcontainerCLIEnvOverride(t *testing.T) {
	// Use the test binary itself as a stand-in for "any executable file" so
	// the helper's stat check passes without needing a real devcontainer CLI.
	t.Setenv("INFRAMNGMT_DEVCONTAINER_CLI", "node /tmp/foo.js extra-arg")
	got, err := findDevcontainerCLI()
	if err != nil {
		t.Fatalf("env override should bypass stat checks; got %v", err)
	}
	want := []string{"node", "/tmp/foo.js", "extra-arg"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("arg[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestHandleContainerDevcontainerUpNoDocker(t *testing.T) {
	srv := New(nil, nil, "", nil, nil, nil, nil, nil, nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/container/devcontainer-up?id=abc", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rr.Code)
	}
}

// ─── auth middleware ─────────────────────────────────────────────────────────

func TestAuthMiddlewareNoTokenAllowsAll(t *testing.T) {
	m := newMockSource("host:/x", entity.GlobalScope())
	srv := newServerWithSource(m)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/sources", nil))
	if rr.Code != 200 {
		t.Errorf("token disabled should allow request, got %d", rr.Code)
	}
}

func TestAuthMiddlewareRedirectsWhenNoSession(t *testing.T) {
	m := newMockSource("host:/x", entity.GlobalScope())
	srv := New([]source.Source{m}, nil, "secret", nil, nil, nil, nil, nil, nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/sources", nil))
	if rr.Code != http.StatusSeeOther {
		t.Errorf("expected 303, got %d", rr.Code)
	}
	if !strings.HasPrefix(rr.Header().Get("Location"), "/login") {
		t.Errorf("expected /login redirect, got %q", rr.Header().Get("Location"))
	}
}

// requestFrom builds a request whose RemoteAddr is the given source IP.
// httptest.NewRequest defaults to "192.0.2.1:1234" so we override.
func requestFrom(method, target, sourceIP string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	req.RemoteAddr = sourceIP + ":4242"
	return req
}

func TestAuthBypassedFromTrustedNetwork(t *testing.T) {
	m := newMockSource("host:/x", entity.GlobalScope())
	srv := New([]source.Source{m}, nil, "secret", nil, nil, nil, nil, nil,
		[]string{"127.0.0.0/8", "172.17.0.0/16"})

	// Loopback bypasses auth without a session cookie.
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, requestFrom(http.MethodGet, "/api/sources", "127.0.0.1"))
	if rr.Code != 200 {
		t.Errorf("loopback should bypass auth, got %d", rr.Code)
	}

	// Docker bridge IP bypasses auth without a session cookie.
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, requestFrom(http.MethodGet, "/api/sources", "172.17.0.5"))
	if rr.Code != 200 {
		t.Errorf("docker-bridge IP should bypass auth, got %d", rr.Code)
	}
}

func TestAuthRequiredFromUntrustedNetwork(t *testing.T) {
	m := newMockSource("host:/x", entity.GlobalScope())
	srv := New([]source.Source{m}, nil, "secret", nil, nil, nil, nil, nil,
		[]string{"127.0.0.0/8"})

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, requestFrom(http.MethodGet, "/api/sources", "10.0.0.5"))
	if rr.Code != http.StatusSeeOther {
		t.Errorf("untrusted IP should be redirected to login, got %d", rr.Code)
	}
}

func TestAuthBypassRequiresExplicitConfig(t *testing.T) {
	// Empty trustedNetworks: even loopback needs a session cookie.
	m := newMockSource("host:/x", entity.GlobalScope())
	srv := New([]source.Source{m}, nil, "secret", nil, nil, nil, nil, nil, nil)

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, requestFrom(http.MethodGet, "/api/sources", "127.0.0.1"))
	if rr.Code != http.StatusSeeOther {
		t.Errorf("loopback without config should still be redirected, got %d", rr.Code)
	}
}

func TestAuthInvalidCIDRsAreSkipped(t *testing.T) {
	// Garbage entries should be ignored, not crash. Valid one still works.
	m := newMockSource("host:/x", entity.GlobalScope())
	srv := New([]source.Source{m}, nil, "secret", nil, nil, nil, nil, nil,
		[]string{"not-a-cidr", "127.0.0.0/8", "still-not-a-cidr"})

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, requestFrom(http.MethodGet, "/api/sources", "127.0.0.1"))
	if rr.Code != 200 {
		t.Errorf("valid CIDR should still bypass after parse errors; got %d", rr.Code)
	}
}

// Smoke-test the /compose/reload route. We can't easily plumb a fake
// compose.Client into Server (the field is unexported and slice-typed) so
// this just verifies the route exists and rejects bad input — the
// happy-path integration is exercised by client_test's Reload tests.
func TestComposeReloadInvalidInstance(t *testing.T) {
	srv := New(nil, nil, "", nil, nil, nil, nil, nil, nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/compose/reload?instance=has%20space", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid instance name, got %d", rr.Code)
	}
}

func TestComposeReloadUnknownInstance(t *testing.T) {
	srv := New(nil, nil, "", nil, nil, nil, nil, nil, nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/compose/reload?instance=ghost", nil))
	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown instance, got %d", rr.Code)
	}
}

func TestBridgesRefreshReloadsYAML(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/bridges.yaml"
	initial := `bridges:
  - name: alpha
    tier: windows
    type: portproxy+firewall
    listen: { addr: "${wsl-host-ip}", port: 1000 }
    connect: { addr: 127.0.0.1, port: 1000, family: auto }
    firewall: { remote: 10.0.0.0/8, display_name: Alpha }
`
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := New(nil, nil, "", nil, nil, nil, nil, nil, nil)
	srv.SetBridgesFile(path)

	// First refresh picks up alpha. /bridges/refresh now returns the shell
	// (per §7.10 per-section poll architecture); the bridges content
	// itself lives at /partials/services/bridges, which the shell's
	// placeholder triggers via hx-trigger="load" in the browser flow.
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/bridges/refresh", nil))
	if rr.Code != 200 {
		t.Fatalf("first refresh: got %d, body: %s", rr.Code, rr.Body.String())
	}
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/partials/services/bridges", nil))
	if !strings.Contains(rr.Body.String(), "alpha") {
		t.Errorf("rendered bridges section should mention alpha:\n%s", rr.Body.String())
	}

	// Edit YAML to add beta, refresh again.
	updated := initial + `  - name: beta
    tier: windows
    type: portproxy+firewall
    listen: { addr: "${wsl-host-ip}", port: 2000 }
    connect: { addr: 127.0.0.1, port: 2000, family: auto }
    firewall: { remote: 10.0.0.0/8, display_name: Beta }
`
	if err := os.WriteFile(path, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/bridges/refresh", nil))
	if rr.Code != 200 {
		t.Fatalf("second refresh: got %d, body: %s", rr.Code, rr.Body.String())
	}
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/partials/services/bridges", nil))
	body := rr.Body.String()
	if !strings.Contains(body, "alpha") || !strings.Contains(body, "beta") {
		t.Errorf("after edit, refresh should pick up the new entry:\n%s", body)
	}
}

// ─── /partials/llama (top-level llama view) ──────────────────────────────

func TestLlamaPageEmptyWhenNoServersConfigured(t *testing.T) {
	srv := New(nil, nil, "", nil, nil, nil, nil, nil, nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/partials/llama", nil))
	if rr.Code != 200 {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "no <code>llama_servers</code>") {
		t.Errorf("body should hint at empty config; got: %s", rr.Body.String())
	}
}

func TestLlamaPageSingleModel(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/props", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"model_path":"/m/llama-7b.gguf","total_slots":2,"build_info":"b3000-abc","is_sleeping":false,"default_generation_settings":{"n_ctx":4096}}`))
	})
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, _ *http.Request) {
		// No `status` field → single-model mode.
		_, _ = w.Write([]byte(`{"data":[{"id":"single","object":"model","owned_by":"llamacpp"}]}`))
	})
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("llamacpp:kv_cache_usage_ratio 0.33\nllamacpp:requests_processing 1\n"))
	})
	mux.HandleFunc("/slots", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"id":0,"id_task":1,"n_ctx":4096,"is_processing":true,"next_token":[{"has_next_token":true,"n_remain":40,"n_decoded":60}]}]`))
	})
	upstream := httptest.NewServer(mux)
	defer upstream.Close()

	srv := New(nil, nil, "", nil, nil, nil, nil, nil, nil)
	srv.SetLlamaServers([]LlamaEntry{{Instance: "wsl", Process: "llama-server", Endpoint: upstream.URL}})

	// Per-card endpoint — the shell at /partials/llama just lists
	// placeholders; the actual probe + rendering happens in /partials/llama/card.
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/partials/llama/card?instance=wsl&process=llama-server", nil))
	if rr.Code != 200 {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	for _, want := range []string{
		"llama-server",
		"wsl",
		"llama-head-dot running",
		"/m/llama-7b.gguf",
		"b3000-abc",
		"in flight",
		"slots ",
		"#0",
		"60 / 100 tok",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("rendered page missing %q\nbody: %s", want, body)
		}
	}
	if strings.Contains(body, "router") {
		t.Errorf("single-model server should NOT show router pill; body: %s", body)
	}
}

func TestLlamaPageRouterMode(t *testing.T) {
	var slotsCalls, metricsCalls int
	var slotsModel, metricsModel string
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/props", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"model_path":"none","total_slots":0,"build_info":"b9080-abcdef","is_sleeping":false,"default_generation_settings":{"n_ctx":0}}`))
	})
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[
			{"id":"alpha-q4","object":"model","owned_by":"llamacpp","status":{"value":"loaded","args":["--alias","alpha"]}},
			{"id":"beta-q8","object":"model","owned_by":"llamacpp","status":{"value":"unloaded","exit_code":10,"failed":true}}
		]}`))
	})
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		metricsCalls++
		metricsModel = r.URL.Query().Get("model")
		_, _ = w.Write([]byte("llamacpp:kv_cache_usage_ratio 0.5\nllamacpp:requests_processing 1\n"))
	})
	mux.HandleFunc("/slots", func(w http.ResponseWriter, r *http.Request) {
		slotsCalls++
		slotsModel = r.URL.Query().Get("model")
		_, _ = w.Write([]byte(`[{"id":0,"id_task":7,"n_ctx":8192,"is_processing":false,"next_token":[{"has_next_token":false,"n_remain":0,"n_decoded":0}]}]`))
	})
	upstream := httptest.NewServer(mux)
	defer upstream.Close()

	srv := New(nil, nil, "", nil, nil, nil, nil, nil, nil)
	srv.SetLlamaServers([]LlamaEntry{{Instance: "windows", Process: "llama-server", Endpoint: upstream.URL}})

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/partials/llama/card?instance=windows&process=llama-server", nil))
	if rr.Code != 200 {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()

	// Router-mode pill present.
	if !strings.Contains(body, ">router<") {
		t.Errorf("expected router pill; body: %s", body)
	}
	// Both models rendered. For the failed model, the status badge is suppressed
	// and replaced by a single combined pill (e.g. "● failed exit 10").
	for _, want := range []string{"alpha-q4", "beta-q8", "loaded", "failed exit"} {
		if !strings.Contains(body, want) {
			t.Errorf("rendered page missing %q\nbody: %s", want, body)
		}
	}
	// Failed model must NOT render a separate status badge ("unloaded") — the
	// combined pill carries the full signal.
	if strings.Contains(body, `class="llama-model-status status-unloaded"`) {
		t.Errorf("failed model should suppress the status-unloaded badge; found redundant badge in body: %s", body)
	}
	// Loaded model gets a slot probe with ?model=; failed/unloaded do not.
	if slotsCalls != 1 {
		t.Errorf("slots calls = %d, want 1 (loaded model only)", slotsCalls)
	}
	if metricsCalls != 1 {
		t.Errorf("metrics calls = %d, want 1 (loaded model only)", metricsCalls)
	}
	if slotsModel != "alpha-q4" {
		t.Errorf("slots model param = %q, want alpha-q4", slotsModel)
	}
	if metricsModel != "alpha-q4" {
		t.Errorf("metrics model param = %q, want alpha-q4", metricsModel)
	}
}

func TestLlamaPageRendersHealthErrorWithoutCrash(t *testing.T) {
	// Server unreachable — no goroutine should panic; the card still renders
	// with the unreachable pill.
	srv := New(nil, nil, "", nil, nil, nil, nil, nil, nil)
	srv.SetLlamaServers([]LlamaEntry{{Instance: "ghost", Process: "llama-server", Endpoint: "http://127.0.0.1:1"}})
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/partials/llama/card?instance=ghost&process=llama-server", nil))
	if rr.Code != 200 {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "unreachable") {
		t.Errorf("body should show unreachable pill; got: %s", rr.Body.String())
	}
}

func TestBridgesRefreshSurfacesYAMLErrors(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/bridges.yaml"
	if err := os.WriteFile(path, []byte("bridges:\n  - name: bad\n    tier: nonsense\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := New(nil, nil, "", nil, nil, nil, nil, nil, nil)
	srv.SetBridgesFile(path)

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/bridges/refresh", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 for bad YAML, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "tier") {
		t.Errorf("error response should mention the failing field; got %s", rr.Body.String())
	}
}

func TestAuthBypassIPv6Loopback(t *testing.T) {
	m := newMockSource("host:/x", entity.GlobalScope())
	srv := New([]source.Source{m}, nil, "secret", nil, nil, nil, nil, nil,
		[]string{"::1/128"})

	req := httptest.NewRequest(http.MethodGet, "/api/sources", nil)
	req.RemoteAddr = "[::1]:4242"
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Errorf("ipv6 loopback should bypass, got %d", rr.Code)
	}
}

// ─── bearer-token auth (headless / deploy clients) ───────────────────────────

// bearerReq builds an untrusted-origin request carrying the given
// Authorization header value. Untrusted RemoteAddr ensures the bearer path —
// not the trusted-network bypass — is what's under test.
func bearerReq(method, target, authHeader string) *http.Request {
	req := requestFrom(method, target, "8.8.8.8")
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	return req
}

func TestAuthBearerValidTokenAllows(t *testing.T) {
	m := newMockSource("host:/x", entity.GlobalScope())
	srv := New([]source.Source{m}, nil, "secret", nil, nil, nil, nil, nil, nil)

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, bearerReq(http.MethodGet, "/api/sources", "Bearer secret"))
	if rr.Code != 200 {
		t.Errorf("valid bearer token from untrusted IP should be allowed, got %d", rr.Code)
	}
}

func TestAuthBearerSchemeIsCaseInsensitive(t *testing.T) {
	m := newMockSource("host:/x", entity.GlobalScope())
	srv := New([]source.Source{m}, nil, "secret", nil, nil, nil, nil, nil, nil)

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, bearerReq(http.MethodGet, "/api/sources", "bearer secret"))
	if rr.Code != 200 {
		t.Errorf("lowercase bearer scheme should be accepted, got %d", rr.Code)
	}
}

func TestAuthBearerWrongTokenReturns401(t *testing.T) {
	m := newMockSource("host:/x", entity.GlobalScope())
	srv := New([]source.Source{m}, nil, "secret", nil, nil, nil, nil, nil, nil)

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, bearerReq(http.MethodGet, "/api/sources", "Bearer wrong"))
	// A present-but-wrong bearer must be a clean 401, not a /login redirect:
	// scripts can't follow the HTML login flow.
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("wrong bearer token should return 401, got %d", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "" {
		t.Errorf("wrong bearer token should not redirect, got Location %q", loc)
	}
}

func TestAuthBearerEmptyTokenFallsThroughToLogin(t *testing.T) {
	m := newMockSource("host:/x", entity.GlobalScope())
	srv := New([]source.Source{m}, nil, "secret", nil, nil, nil, nil, nil, nil)

	// "Bearer " with no token is treated as no bearer header, so the request
	// falls through to the browser flow and is redirected (not 401'd).
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, bearerReq(http.MethodGet, "/api/sources", "Bearer "))
	if rr.Code != http.StatusSeeOther {
		t.Errorf("empty bearer token should fall through to login redirect, got %d", rr.Code)
	}
}

func TestAuthBearerWorksOnMutatingPost(t *testing.T) {
	// The deploy flow POSTs to a mutating route from an untrusted (bridge)
	// origin; a valid bearer must carry it past auth (404 here = past auth,
	// unknown instance — not a 303 login redirect or 401).
	m := newMockSource("host:/x", entity.GlobalScope())
	srv := New([]source.Source{m}, nil, "secret", nil, nil, nil, nil, nil, nil)

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, bearerReq(http.MethodPost, "/process/start?instance=nope&process=x", "Bearer secret"))
	if rr.Code == http.StatusSeeOther || rr.Code == http.StatusUnauthorized {
		t.Errorf("valid bearer should pass auth on mutating POST, got %d", rr.Code)
	}
}

func TestAuthBearerTakesPrecedenceOverTrustedIP(t *testing.T) {
	// Deliberate design choice: bearer is checked before the trusted-network
	// bypass, so a request from a trusted IP that presents a WRONG bearer
	// fails closed (401) rather than being waved through on IP alone. No real
	// client hits this (the browser never sends Authorization), but lock the
	// fail-closed-on-explicit-bad-credential behavior in.
	m := newMockSource("host:/x", entity.GlobalScope())
	srv := New([]source.Source{m}, nil, "secret", nil, nil, nil, nil, nil,
		[]string{"127.0.0.0/8"})

	// Wrong bearer from a trusted IP → 401, not the IP bypass.
	rr := httptest.NewRecorder()
	req := requestFrom(http.MethodGet, "/api/sources", "127.0.0.1")
	req.Header.Set("Authorization", "Bearer wrong")
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("wrong bearer from trusted IP should 401 (bearer precedence), got %d", rr.Code)
	}

	// No Authorization header from the same trusted IP → IP bypass still works.
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, requestFrom(http.MethodGet, "/api/sources", "127.0.0.1"))
	if rr.Code != 200 {
		t.Errorf("trusted IP without bearer should still bypass, got %d", rr.Code)
	}
}

func TestAuthBearerIgnoredWhenAuthDisabled(t *testing.T) {
	// With no token configured, a (wrong) bearer header must not flip an
	// open server into returning 401.
	m := newMockSource("host:/x", entity.GlobalScope())
	srv := newServerWithSource(m) // token == ""

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, bearerReq(http.MethodGet, "/api/sources", "Bearer anything"))
	if rr.Code != 200 {
		t.Errorf("auth disabled should allow regardless of bearer header, got %d", rr.Code)
	}
}

func TestBearerTokenParsing(t *testing.T) {
	cases := []struct {
		header  string
		wantTok string
		wantOK  bool
	}{
		{"", "", false},
		{"Bearer abc", "abc", true},
		{"bearer abc", "abc", true},
		{"BEARER abc", "abc", true},
		{"Bearer   abc  ", "abc", true},
		{"Bearer ", "", false},
		{"Bearer", "", false},
		{"Basic abc", "", false},
		{"Token abc", "", false},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if c.header != "" {
			req.Header.Set("Authorization", c.header)
		}
		tok, ok := bearerToken(req)
		if ok != c.wantOK || tok != c.wantTok {
			t.Errorf("bearerToken(%q) = (%q,%v), want (%q,%v)", c.header, tok, ok, c.wantTok, c.wantOK)
		}
	}
}

// loginPost posts the login form with the given token/next and returns the recorder.
func loginPost(srv *Server, token, next string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("token="+token+"&next="+url.QueryEscape(next)))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	srv.ServeHTTP(rr, req)
	return rr
}

func TestLoginPostAuthDisabledMintsNoSession(t *testing.T) {
	// token == "" → login is a no-op redirect to "/", and crucially must not
	// mint a phantom session cookie (authMiddleware ignores it anyway).
	srv := newServerWithSource(newMockSource("host:/x", entity.GlobalScope())) // token ""
	rr := loginPost(srv, "anything", "/")
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/" {
		t.Fatalf("auth-disabled login: got %d Location=%q, want 303 /", rr.Code, rr.Header().Get("Location"))
	}
	for _, c := range rr.Result().Cookies() {
		if c.Name == "im_session" {
			t.Errorf("auth-disabled login must not set im_session cookie")
		}
	}
	got := false
	srv.sessions.Range(func(_, _ any) bool { got = true; return false })
	if got {
		t.Errorf("auth-disabled login must not store a session")
	}
}

func TestLoginPostRejectsProtocolRelativeNext(t *testing.T) {
	cases := []struct{ next, wantLoc string }{
		{"/partials/services", "/partials/services"}, // legit relative path preserved
		{"//evil.com", "/"},                          // protocol-relative open redirect
		{`/\evil.com`, "/"},                          // backslash-folding (browsers treat \ as /)
		{"/\tx", "/"},                                // control char
		{"https://evil.com", "/"},                    // absolute URL
		{"", "/"},                                    // empty
	}
	for _, c := range cases {
		srv := New(nil, nil, "secret", nil, nil, nil, nil, nil, nil)
		rr := loginPost(srv, "secret", c.next)
		if rr.Code != http.StatusSeeOther {
			t.Fatalf("next=%q: status %d, want 303", c.next, rr.Code)
		}
		if loc := rr.Header().Get("Location"); loc != c.wantLoc {
			t.Errorf("next=%q: Location=%q, want %q", c.next, loc, c.wantLoc)
		}
	}
}

func TestLoginGetEvictsStaleSessionAndShowsForm(t *testing.T) {
	// handleLoginGet must apply the same TTL+eviction as authMiddleware: a
	// stale cookie should NOT bounce the user back into the app — it should
	// be evicted and the login form shown.
	m := newMockSource("host:/x", entity.GlobalScope())
	srv := New([]source.Source{m}, nil, "secret", nil, nil, nil, nil, nil, nil)
	stale := "stale-sid"
	srv.sessions.Store(stale, session{created: time.Now().Add(-sessionTTL - time.Minute)})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	req.AddCookie(&http.Cookie{Name: "im_session", Value: stale})
	srv.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("stale cookie on /login should render the form (200), got %d Location=%q", rr.Code, rr.Header().Get("Location"))
	}
	if _, ok := srv.sessions.Load(stale); ok {
		t.Errorf("stale session should have been evicted by handleLoginGet")
	}
}

func TestSessionExpiryRejectsStaleCookieAndEvicts(t *testing.T) {
	m := newMockSource("host:/x", entity.GlobalScope())
	srv := New([]source.Source{m}, nil, "secret", nil, nil, nil, nil, nil, nil)

	// Fresh session authenticates.
	fresh := "fresh-sid"
	srv.sessions.Store(fresh, session{created: time.Now()})
	rr := httptest.NewRecorder()
	req := requestFrom(http.MethodGet, "/api/sources", "8.8.8.8") // untrusted → cookie path
	req.AddCookie(&http.Cookie{Name: "im_session", Value: fresh})
	srv.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("fresh session should authenticate, got %d", rr.Code)
	}

	// Session older than sessionTTL is rejected and evicted from the map.
	stale := "stale-sid"
	srv.sessions.Store(stale, session{created: time.Now().Add(-sessionTTL - time.Minute)})
	rr = httptest.NewRecorder()
	req = requestFrom(http.MethodGet, "/api/sources", "8.8.8.8")
	req.AddCookie(&http.Cookie{Name: "im_session", Value: stale})
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusSeeOther || !strings.HasPrefix(rr.Header().Get("Location"), "/login") {
		t.Fatalf("stale session should redirect to login, got %d Location=%q", rr.Code, rr.Header().Get("Location"))
	}
	if _, ok := srv.sessions.Load(stale); ok {
		t.Errorf("stale session should have been evicted from the map on access")
	}
}

func TestLoginPostInvalidToken(t *testing.T) {
	srv := New(nil, nil, "secret", nil, nil, nil, nil, nil, nil)
	rr := httptest.NewRecorder()
	body := strings.NewReader("token=wrong&next=/")
	req := httptest.NewRequest(http.MethodPost, "/login", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

func TestLoginPostValidTokenSetsSession(t *testing.T) {
	srv := New(nil, nil, "secret", nil, nil, nil, nil, nil, nil)
	// Login
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/login",
		strings.NewReader("token=secret&next=/api/sources"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusSeeOther {
		t.Fatalf("login status = %d", rr.Code)
	}
	// Cookie should be set; following request with the cookie should succeed.
	cookies := rr.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("no cookie set")
	}
	rr2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/api/sources", nil)
	for _, c := range cookies {
		req2.AddCookie(c)
	}
	srv.ServeHTTP(rr2, req2)
	if rr2.Code != 200 {
		t.Errorf("authed request status = %d", rr2.Code)
	}
}

func TestLogoutClearsSession(t *testing.T) {
	srv := New(nil, nil, "secret", nil, nil, nil, nil, nil, nil)
	// Authenticate
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/login",
		strings.NewReader("token=secret"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	srv.ServeHTTP(rr, req)
	cookies := rr.Result().Cookies()
	// Logout
	rr2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/logout", nil)
	for _, c := range cookies {
		req2.AddCookie(c)
	}
	srv.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusSeeOther {
		t.Errorf("logout status = %d", rr2.Code)
	}
	// Subsequent authed request with the same cookie should redirect to /login.
	rr3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodGet, "/api/sources", nil)
	for _, c := range cookies {
		req3.AddCookie(c)
	}
	srv.ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusSeeOther {
		t.Errorf("post-logout request status = %d, want 303 redirect", rr3.Code)
	}
}

// ─── parallel source fetch + cache ──────────────────────────────────────────

func TestAllEntitiesParallelAndCached(t *testing.T) {
	a := newMockSource("host:/a", entity.GlobalScope())
	b := newMockSource("host:/b", entity.GlobalScope())
	a.addEntity(entity.KindCommand, "ar", nil)
	b.addEntity(entity.KindAgent, "br", nil)
	srv := New([]source.Source{a, b}, nil, "", nil, nil, nil, nil, nil, nil)

	ents, err := srv.allEntities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 2 {
		t.Fatalf("got %d entities, want 2", len(ents))
	}
	if a.calls != 1 || b.calls != 1 {
		t.Errorf("each source should be called once, got a=%d b=%d", a.calls, b.calls)
	}

	// Second call hits cache.
	if _, err := srv.allEntities(context.Background()); err != nil {
		t.Fatal(err)
	}
	if a.calls != 1 || b.calls != 1 {
		t.Errorf("cache miss: a=%d b=%d (expected 1/1)", a.calls, b.calls)
	}
}

// ─── helper: ensure a source returning an error doesn't break the page ───────

type errSource struct{ mockSource }

func (e *errSource) Entities(_ context.Context) ([]entity.Entity, error) {
	return nil, errors.New("boom")
}

func TestAllEntitiesIgnoresFailingSource(t *testing.T) {
	good := newMockSource("host:/g", entity.GlobalScope())
	good.addEntity(entity.KindCommand, "run", nil)
	bad := &errSource{mockSource: *newMockSource("host:/b", entity.GlobalScope())}
	srv := New([]source.Source{good, bad}, nil, "", nil, nil, nil, nil, nil, nil)
	ents, err := srv.allEntities(context.Background())
	if err != nil {
		t.Fatalf("allEntities should not propagate per-source errors: %v", err)
	}
	if len(ents) != 1 || ents[0].Name != "run" {
		t.Errorf("expected 1 entity from good source, got %v", ents)
	}
}

// ─── /api/entity payload roundtrip ──────────────────────────────────────────

func TestEntityWriteReadsBody(t *testing.T) {
	m := newMockSource("host:/x", entity.GlobalScope())
	e := m.addEntity(entity.KindCommand, "run", nil)
	srv := newServerWithSource(m)
	rr := httptest.NewRecorder()
	body := bytes.NewReader([]byte("payload"))
	req := httptest.NewRequest(http.MethodPost, "/api/entity?id="+e.ID, body)
	srv.ServeHTTP(rr, req)
	if rr.Code != 204 {
		t.Fatal(rr.Body.String())
	}
	if got := string(m.files["command:run"]); got != "payload" {
		t.Errorf("write body = %q", got)
	}
}

// silence unused import
var _ io.Reader = (*bytes.Reader)(nil)

// ─── /api/sources/rescan ────────────────────────────────────────────────────

func TestSourcesRescanReturns503WhenNotConfigured(t *testing.T) {
	srv := newServerWithSource(newMockSource("host:/a", entity.GlobalScope()))
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/sources/rescan", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rr.Code)
	}
}

func TestSourcesRescanAddsNewSources(t *testing.T) {
	a := newMockSource("host:/a", entity.GlobalScope())
	srv := newServerWithSource(a)
	b := newMockSource("host:/b", entity.ProjectScope("/b"))
	srv.SetRediscover(func() []source.Source { return []source.Source{a, b} })

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/sources/rescan", nil))
	if rr.Code != 200 {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var got struct {
		Discovered []string
		Added      []string
		Total      int
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Discovered) != 2 || got.Discovered[0] != "host:/a" || got.Discovered[1] != "host:/b" {
		t.Errorf("Discovered = %v, want [host:/a host:/b]", got.Discovered)
	}
	if len(got.Added) != 1 || got.Added[0] != "host:/b" {
		t.Errorf("Added = %v, want [host:/b]", got.Added)
	}
	if got.Total != 2 {
		t.Errorf("Total = %d, want 2", got.Total)
	}
	if len(srv.allSources()) != 2 {
		t.Errorf("source list not updated, got %d", len(srv.allSources()))
	}
}

func TestSourcesRescanIsIdempotent(t *testing.T) {
	a := newMockSource("host:/a", entity.GlobalScope())
	srv := newServerWithSource(a)
	srv.SetRediscover(func() []source.Source { return []source.Source{a} })

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/sources/rescan", nil))
	var got struct {
		Discovered []string
		Added      []string
		Total      int
	}
	json.Unmarshal(rr.Body.Bytes(), &got)
	if len(got.Discovered) != 1 || got.Discovered[0] != "host:/a" {
		t.Errorf("Discovered should still report the existing source: %v", got.Discovered)
	}
	if len(got.Added) != 0 {
		t.Errorf("Added should be empty on idempotent rescan, got %v", got.Added)
	}
	if got.Total != 1 {
		t.Errorf("Total = %d, want 1", got.Total)
	}
}

func TestSourcesRescanInvalidatesEntityCacheOnAdd(t *testing.T) {
	a := newMockSource("host:/a", entity.GlobalScope())
	a.addEntity(entity.KindCommand, "run", nil)
	srv := newServerWithSource(a)

	// Prime the cache.
	if _, err := srv.allEntities(context.Background()); err != nil {
		t.Fatal(err)
	}
	cnt1 := atomic.LoadInt32(&a.calls)

	b := newMockSource("host:/b", entity.GlobalScope())
	b.addEntity(entity.KindAgent, "helper", nil)
	srv.SetRediscover(func() []source.Source { return []source.Source{a, b} })

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/sources/rescan", nil))
	if rr.Code != 200 {
		t.Fatalf("rescan status = %d", rr.Code)
	}

	// After rescan, the next entity fetch must hit each source again — proving
	// the cache was invalidated.
	if _, err := srv.allEntities(context.Background()); err != nil {
		t.Fatal(err)
	}
	cnt2 := atomic.LoadInt32(&a.calls)
	if cnt2 != cnt1+1 {
		t.Errorf("expected fresh fetch after rescan: a.calls went %d → %d", cnt1, cnt2)
	}
	if atomic.LoadInt32(&b.calls) != 1 {
		t.Errorf("expected new source b to be queried once, got %d", b.calls)
	}
}

func TestSourcesRescanDoesNotInvalidateWhenNoAddition(t *testing.T) {
	a := newMockSource("host:/a", entity.GlobalScope())
	a.addEntity(entity.KindCommand, "run", nil)
	srv := newServerWithSource(a)
	srv.SetRediscover(func() []source.Source { return []source.Source{a} })

	if _, err := srv.allEntities(context.Background()); err != nil {
		t.Fatal(err)
	}
	cnt1 := atomic.LoadInt32(&a.calls)

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/sources/rescan", nil))
	if rr.Code != 200 {
		t.Fatalf("rescan status = %d", rr.Code)
	}

	if _, err := srv.allEntities(context.Background()); err != nil {
		t.Fatal(err)
	}
	cnt2 := atomic.LoadInt32(&a.calls)
	if cnt2 != cnt1 {
		t.Errorf("rescan with no new sources should not invalidate cache: a.calls went %d → %d", cnt1, cnt2)
	}
}

// ─── /partials/logs ─────────────────────────────────────────────────────────

// The logs handler had zero coverage prior to the frontend refactor work
// (see docs/frontend-architecture.md §16). Without these the M4 step that
// moves logsHTML into templates/*.html.tmpl would silently regress.

func TestHandleProcessLogsRejectsBadInstanceName(t *testing.T) {
	srv := newServerWithSource(newMockSource("host:/x", entity.GlobalScope()))
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/partials/logs?instance=../etc&process=foo", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestHandleProcessLogsRejectsBadProcessName(t *testing.T) {
	srv := newServerWithSource(newMockSource("host:/x", entity.GlobalScope()))
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/partials/logs?instance=wsl&process=foo;rm", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

// With no compose instance configured, the handler still renders the partial
// successfully — populating the .Err field with "instance ... not configured".
// This is the path HTMX hits when the user opens a log panel for a non-existent
// instance. Renders the html shell with the error inline.
func TestHandleProcessLogsRendersErrorWhenInstanceUnknown(t *testing.T) {
	srv := newServerWithSource(newMockSource("host:/x", entity.GlobalScope()))
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/partials/logs?instance=wsl&process=infra-mngmt", nil))
	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "wsl") {
		t.Errorf("rendered partial missing instance name:\n%s", body)
	}
	if !strings.Contains(body, "not configured") {
		t.Errorf("expected 'not configured' error inline:\n%s", body)
	}
}

// ─── /api/container/events-stream ───────────────────────────────────────────

// SSE is the highest-risk live-update mechanism in the codebase and had zero
// tests before the refactor work. The full streaming path needs a docker-client
// interface to mock; until that lands, lock down the two precondition branches.

func TestHandleContainerEventsStreamReturns503WithoutDocker(t *testing.T) {
	srv := New(nil, nil, "", nil, nil, nil, nil, nil, nil) // dc = nil
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/container/events-stream?id=abc", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rr.Code)
	}
}

// Even with docker missing, the missing-id branch is unreachable today
// because the docker check runs first. This test pins the current ordering
// so the M7 refactor (HTMX-ifying the container panel) does not silently
// reverse it — a 400 surfaced before 503 would change client behavior.
func TestHandleContainerEventsStreamPrioritizesDockerCheck(t *testing.T) {
	srv := New(nil, nil, "", nil, nil, nil, nil, nil, nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/container/events-stream", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("missing-docker should win over missing-id; status = %d", rr.Code)
	}
}

// ─── /partials/container-controls (M7) ─────────────────────────────────────

// TestHandleContainerControls_NoDocker verifies that the controls partial
// returns 200 + empty body when docker is not configured. The client should
// receive an empty #ov-ctr swap (panel goes blank, not broken).
func TestHandleContainerControls_NoDocker(t *testing.T) {
	srv := New(nil, nil, "", nil, nil, nil, nil, nil, nil) // dc = nil
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/partials/container-controls?project=/home/user/repo", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if strings.TrimSpace(rr.Body.String()) != "" {
		t.Errorf("expected empty body when docker=nil, got %q", rr.Body.String())
	}
}

// TestHandleContainerControls_MissingProject verifies that omitting ?project=
// returns 200 + empty body regardless of docker availability.
func TestHandleContainerControls_MissingProject(t *testing.T) {
	srv := New(nil, nil, "", nil, nil, nil, nil, nil, nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/partials/container-controls", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if strings.TrimSpace(rr.Body.String()) != "" {
		t.Errorf("expected empty body when project is empty, got %q", rr.Body.String())
	}
}

// TestHandleContainerStart_NoDocker_Returns503 confirms the no-docker check
// is preserved after M7 (start now renders a partial on success instead of 204).
func TestHandleContainerStart_NoDocker_Returns503(t *testing.T) {
	srv := New(nil, nil, "", nil, nil, nil, nil, nil, nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/container/start?id=abc&project=/repo", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rr.Code)
	}
}

// TestHandleContainerStop_NoDocker_Returns503 confirms the same for stop.
func TestHandleContainerStop_NoDocker_Returns503(t *testing.T) {
	srv := New(nil, nil, "", nil, nil, nil, nil, nil, nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/container/stop?id=abc&project=/repo", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rr.Code)
	}
}

// TestBuildContainerControlsView_NoMatch verifies that an empty result is
// returned when no containers match the requested project.
func TestBuildContainerControlsView_NoMatch(t *testing.T) {
	view := buildContainerControlsView("/other/project", nil)
	if view.HasContainer {
		t.Errorf("expected HasContainer=false for empty list")
	}
}

// TestBuildContainerControlsView_RunningPreferred verifies that a running
// container is preferred over an exited one when multiple containers match.
func TestBuildContainerControlsView_RunningPreferred(t *testing.T) {
	ctrs := []docker.ManagedContainer{
		{ID: "aaa111aaa111", ProjectRoot: "/repo", State: "exited"},
		{ID: "bbb222bbb222", ProjectRoot: "/repo", State: "running"},
	}
	view := buildContainerControlsView("/repo", ctrs)
	if !view.HasContainer {
		t.Fatal("expected HasContainer=true")
	}
	if view.ID != "bbb222bbb222" {
		t.Errorf("ID = %q, want running container bbb222bbb222", view.ID)
	}
	if view.ActionFn != "stop" {
		t.Errorf("ActionFn = %q, want stop for running container", view.ActionFn)
	}
}

// TestBuildContainerControlsView_StoppedDevcontainer verifies that a stopped
// devcontainer (has projectRoot) shows VS Code button but hides the action button.
func TestBuildContainerControlsView_StoppedDevcontainer(t *testing.T) {
	ctrs := []docker.ManagedContainer{
		{ID: "ccc333ccc333", ProjectRoot: "/repo", State: "exited", Name: "my-container"},
	}
	view := buildContainerControlsView("/repo", ctrs)
	if !view.HasContainer {
		t.Fatal("expected HasContainer=true")
	}
	// hasDev=true and not running → ShowActionBtn=false
	if view.ShowActionBtn {
		t.Errorf("ShowActionBtn should be false for stopped devcontainer")
	}
	// VS Code button should still show
	if !view.VSCodeShow {
		t.Errorf("VSCodeShow should be true for devcontainer (stopped)")
	}
}

// NOTE: TestHandleContainerStart_ReturnsControlsHTMLOnSuccess and
// TestHandleContainerStop_ReturnsControlsHTMLOnSuccess require a docker client
// mock interface to inject into *Server. The docker.Client is a concrete type
// with no interface seam today. Until a DockerClient interface is extracted,
// the success-path partial-render behavior can only be verified via integration
// tests. Tracked as a follow-up for the docker package refactor.

// TODO(docker-mock): add TestHandleContainerStart_ReturnsControlsHTMLOnSuccess
// TODO(docker-mock): add TestHandleContainerStop_ReturnsControlsHTMLOnSuccess

// Note: structural-ETag tests removed when the services panel moved to
// per-section polling (docs/frontend-architecture.md §7.10). The 304
// fast-path no longer exists; each section's wrapper is replaced
// outerHTML in place every 8s, and the static parts of the panel
// (bridges, OD chrome, vault chrome) live in dedicated endpoints that
// rarely re-render.

// TestMapContainerState covers the shared raw-state → CSS-class mapping that
// snapshotContainers and buildContainerProjectGroups now both call. The CSS
// class uniquely identifies the graph.ContainerState bucket (running/stopped/
// unknown), so asserting it validates the full switch including case-folding.
func TestMapContainerState(t *testing.T) {
	cases := map[string]string{
		"running":    "running",
		"RUNNING":    "running", // case-insensitive
		"restarting": "running",
		"exited":     "stopped",
		"dead":       "stopped",
		"paused":     "stopped",
		"created":    "stopped",
		"weird":      "unknown",
		"":           "unknown",
	}
	for in, wantCSS := range cases {
		if _, css := mapContainerState(in); css != wantCSS {
			t.Errorf("mapContainerState(%q) css = %q, want %q", in, css, wantCSS)
		}
	}
}

// TestQesc pins the query-escaping helper used throughout the HTMX templates:
// the metacharacters that would otherwise truncate or corrupt a query string
// (& ? # / space) must be percent-encoded.
func TestQesc(t *testing.T) {
	cases := map[string]string{
		"plain":             "plain",
		"a&b":               "a%26b",
		"a?b":               "a%3Fb",
		"a#b":               "a%23b",
		"a b":               "a+b",
		"host:/x:command:n": "host%3A%2Fx%3Acommand%3An",
	}
	for in, want := range cases {
		if got := qesc(in); got != want {
			t.Errorf("qesc(%q) = %q, want %q", in, got, want)
		}
	}
}
