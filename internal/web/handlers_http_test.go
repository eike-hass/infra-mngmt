package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
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
	s := New(srcs, nil, "", nil)
	return s
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
	srv := New(nil, nil, "", nil) // dc = nil
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
	srv := New(nil, nil, "", nil)
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
	srv := New([]source.Source{m}, nil, "secret", nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/sources", nil))
	if rr.Code != http.StatusSeeOther {
		t.Errorf("expected 303, got %d", rr.Code)
	}
	if !strings.HasPrefix(rr.Header().Get("Location"), "/login") {
		t.Errorf("expected /login redirect, got %q", rr.Header().Get("Location"))
	}
}

func TestLoginPostInvalidToken(t *testing.T) {
	srv := New(nil, nil, "secret", nil)
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
	srv := New(nil, nil, "secret", nil)
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
	srv := New(nil, nil, "secret", nil)
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
	srv := New([]source.Source{a, b}, nil, "", nil)

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
	srv := New([]source.Source{good, bad}, nil, "", nil)
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
