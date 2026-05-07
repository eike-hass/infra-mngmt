package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

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
	s := New(srcs, nil, "", nil, nil, nil, nil, nil)
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
	srv := New(nil, nil, "secret-token", nil, nil, nil, nil, nil)
	srv.SetBuildInfo(BuildInfo{BuildEpoch: "1700000000", Commit: "deadbeef"})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/version", nil)
	req.RemoteAddr = "8.8.8.8:1234" // not in trusted networks
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 even unauthenticated", rr.Code)
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
	srv := New(nil, nil, "", nil, nil, nil, nil, nil) // dc = nil
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
	srv := New(nil, nil, "", nil, nil, nil, nil, nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/container/start?id=abc", nil))
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
	srv := New([]source.Source{m}, nil, "secret", nil, nil, nil, nil, nil)
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
	srv := New([]source.Source{m}, nil, "secret", nil, nil, nil, nil,
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
	srv := New([]source.Source{m}, nil, "secret", nil, nil, nil, nil,
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
	srv := New([]source.Source{m}, nil, "secret", nil, nil, nil, nil, nil)

	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, requestFrom(http.MethodGet, "/api/sources", "127.0.0.1"))
	if rr.Code != http.StatusSeeOther {
		t.Errorf("loopback without config should still be redirected, got %d", rr.Code)
	}
}

func TestAuthInvalidCIDRsAreSkipped(t *testing.T) {
	// Garbage entries should be ignored, not crash. Valid one still works.
	m := newMockSource("host:/x", entity.GlobalScope())
	srv := New([]source.Source{m}, nil, "secret", nil, nil, nil, nil,
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
	srv := New(nil, nil, "", nil, nil, nil, nil, nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/compose/reload?instance=has%20space", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid instance name, got %d", rr.Code)
	}
}

func TestComposeReloadUnknownInstance(t *testing.T) {
	srv := New(nil, nil, "", nil, nil, nil, nil, nil)
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
	srv := New(nil, nil, "", nil, nil, nil, nil, nil)
	srv.SetBridgesFile(path)

	// First refresh picks up alpha.
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/bridges/refresh", nil))
	if rr.Code != 200 {
		t.Fatalf("first refresh: got %d, body: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "alpha") {
		t.Errorf("rendered services partial should mention alpha:\n%s", rr.Body.String())
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
	body := rr.Body.String()
	if !strings.Contains(body, "alpha") || !strings.Contains(body, "beta") {
		t.Errorf("after edit, refresh should pick up the new entry:\n%s", body)
	}
}

func TestBridgesRefreshSurfacesYAMLErrors(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/bridges.yaml"
	if err := os.WriteFile(path, []byte("bridges:\n  - name: bad\n    tier: nonsense\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := New(nil, nil, "", nil, nil, nil, nil, nil)
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
	srv := New([]source.Source{m}, nil, "secret", nil, nil, nil, nil,
		[]string{"::1/128"})

	req := httptest.NewRequest(http.MethodGet, "/api/sources", nil)
	req.RemoteAddr = "[::1]:4242"
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Errorf("ipv6 loopback should bypass, got %d", rr.Code)
	}
}

func TestLoginPostInvalidToken(t *testing.T) {
	srv := New(nil, nil, "secret", nil, nil, nil, nil, nil)
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
	srv := New(nil, nil, "secret", nil, nil, nil, nil, nil)
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
	srv := New(nil, nil, "secret", nil, nil, nil, nil, nil)
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
	srv := New([]source.Source{a, b}, nil, "", nil, nil, nil, nil, nil)

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
	srv := New([]source.Source{good, bad}, nil, "", nil, nil, nil, nil, nil)
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
