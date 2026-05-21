package web

// End-to-end tests: spin up the real Server behind httptest.NewServer and
// drive multi-step flows over actual HTTP. These complement handlers_http_test.go
// by exercising the full chain — middleware, redirects, cookies, response
// headers — instead of calling ServeHTTP directly with a recorder.
//
// Conventions:
//   - The HTTP client does NOT auto-follow redirects (so tests can inspect them).
//   - The client has a cookie jar so session cookies persist across calls.
//   - Each test gets its own server and source set; teardown via t.Cleanup.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/eike-hass/infra-mngmt/internal/entity"
	"github.com/eike-hass/infra-mngmt/internal/source"
)

type e2eEnv struct {
	t      *testing.T
	URL    string
	client *http.Client
}

func newE2E(t *testing.T, token string, srcs ...source.Source) *e2eEnv {
	t.Helper()
	srv := New(srcs, nil, token, nil, nil, nil, nil, nil, nil)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	jar, _ := cookiejar.New(nil)
	return &e2eEnv{
		t:   t,
		URL: ts.URL,
		client: &http.Client{
			Jar: jar,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// login submits the login form and verifies the 303 redirect.
func (e *e2eEnv) login(token string) {
	e.t.Helper()
	form := url.Values{"token": {token}, "next": {"/"}}
	resp, err := e.client.PostForm(e.URL+"/login", form)
	if err != nil {
		e.t.Fatalf("login POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		e.t.Fatalf("login status = %d, want 303 (token mismatch?)", resp.StatusCode)
	}
}

func (e *e2eEnv) get(path string) *http.Response {
	e.t.Helper()
	resp, err := e.client.Get(e.URL + path)
	if err != nil {
		e.t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

func (e *e2eEnv) post(path string, body io.Reader) *http.Response {
	e.t.Helper()
	resp, err := e.client.Post(e.URL+path, "application/octet-stream", body)
	if err != nil {
		e.t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

func (e *e2eEnv) getJSON(path string, v any) {
	e.t.Helper()
	resp := e.get(path)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		e.t.Fatalf("GET %s status = %d: %s", path, resp.StatusCode, body)
	}
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		e.t.Fatalf("decode JSON from %s: %v", path, err)
	}
}

// readBody reads + closes a response body and returns the string contents.
func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(body)
}

// ─── auth flow ──────────────────────────────────────────────────────────────

func TestE2E_UnauthenticatedRedirectsToLogin(t *testing.T) {
	env := newE2E(t, "secret")
	resp := env.get("/api/sources")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, "/login") {
		t.Errorf("Location = %q, want /login*", loc)
	}
	// "next" should round-trip the original path.
	if !strings.Contains(loc, "next=") {
		t.Errorf("expected next= param in redirect, got %q", loc)
	}
}

func TestE2E_LoginPageAccessibleWithoutAuth(t *testing.T) {
	env := newE2E(t, "secret")
	resp := env.get("/login")
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("/login status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
}

func TestE2E_LoginThenAccessProtectedRoute(t *testing.T) {
	m := newMockSource("host:/x", entity.GlobalScope())
	m.addEntity(entity.KindCommand, "run", nil)
	env := newE2E(t, "secret", m)

	env.login("secret")
	resp := env.get("/api/sources")
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("post-login status = %d", resp.StatusCode)
	}
}

func TestE2E_LogoutDeauthenticates(t *testing.T) {
	env := newE2E(t, "secret")
	env.login("secret")

	// Confirm authed.
	resp := env.get("/api/sources")
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("pre-logout status = %d", resp.StatusCode)
	}

	// Logout.
	resp = env.post("/logout", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("logout status = %d", resp.StatusCode)
	}

	// Now unauthenticated.
	resp = env.get("/api/sources")
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("post-logout status = %d, want 303", resp.StatusCode)
	}
}

func TestE2E_LoginWrongTokenReturns401(t *testing.T) {
	env := newE2E(t, "secret")
	form := url.Values{"token": {"wrong"}}
	resp, err := env.client.PostForm(env.URL+"/login", form)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

// ─── entity browse + edit flow ──────────────────────────────────────────────

func TestE2E_EntityListAndContentFlow(t *testing.T) {
	m := newMockSource("host:/x", entity.GlobalScope())
	e := m.addEntity(entity.KindCommand, "run", []byte("hello"))
	env := newE2E(t, "", m)

	var ents []entity.Entity
	env.getJSON("/api/entities", &ents)
	if len(ents) != 1 || ents[0].Name != "run" {
		t.Errorf("entity list = %+v", ents)
	}

	resp := env.get("/api/entity/content?id=" + url.QueryEscape(e.ID))
	if got := readBody(t, resp); got != "hello" {
		t.Errorf("content = %q, want hello", got)
	}
}

func TestE2E_WriteThenReadShowsUpdate(t *testing.T) {
	m := newMockSource("host:/x", entity.GlobalScope())
	e := m.addEntity(entity.KindCommand, "cmd", []byte("v1"))
	env := newE2E(t, "", m)

	resp := env.get("/api/entity/content?id=" + url.QueryEscape(e.ID))
	if got := readBody(t, resp); got != "v1" {
		t.Errorf("initial content = %q", got)
	}

	resp = env.post("/api/entity?id="+url.QueryEscape(e.ID), strings.NewReader("v2"))
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("write status = %d", resp.StatusCode)
	}

	resp = env.get("/api/entity/content?id=" + url.QueryEscape(e.ID))
	if got := readBody(t, resp); got != "v2" {
		t.Errorf("updated content = %q, want v2", got)
	}
}

func TestE2E_WriteToReadOnlySourceReturns403(t *testing.T) {
	m := newMockSource("vol:x", entity.GlobalScope())
	m.readOnly = true
	e := m.addEntity(entity.KindCommand, "run", nil)
	env := newE2E(t, "", m)

	resp := env.post("/api/entity?id="+url.QueryEscape(e.ID), strings.NewReader("data"))
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

// ─── refresh / cache invalidation flow ──────────────────────────────────────

func TestE2E_RefreshCausesNewSourceFetch(t *testing.T) {
	m := newMockSource("host:/x", entity.GlobalScope())
	m.addEntity(entity.KindCommand, "first", nil)
	env := newE2E(t, "", m)

	// Prime cache.
	var first []entity.Entity
	env.getJSON("/api/entities", &first)
	if len(first) != 1 {
		t.Fatalf("got %d entities", len(first))
	}
	primeCalls := atomic.LoadInt32(&m.calls)

	// Mutate source out-of-band.
	m.addEntity(entity.KindCommand, "second", nil)

	// Without refresh, cached result should still show 1 entity.
	var cached []entity.Entity
	env.getJSON("/api/entities", &cached)
	if len(cached) != 1 {
		t.Errorf("expected cache hit (1 entity), got %d", len(cached))
	}
	if calls := atomic.LoadInt32(&m.calls); calls != primeCalls {
		t.Errorf("expected no source re-fetch from cache; %d → %d", primeCalls, calls)
	}

	// Refresh.
	resp := env.post("/api/refresh", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("refresh status = %d", resp.StatusCode)
	}

	// Now the new entity should appear.
	var refreshed []entity.Entity
	env.getJSON("/api/entities", &refreshed)
	if len(refreshed) != 2 {
		t.Errorf("expected 2 entities after refresh, got %d (%+v)", len(refreshed), refreshed)
	}
	if calls := atomic.LoadInt32(&m.calls); calls <= primeCalls {
		t.Errorf("expected source re-fetch after refresh; %d → %d", primeCalls, calls)
	}
}

// ─── HTML / HTMX response shape ─────────────────────────────────────────────

func TestE2E_IndexRendersEntities(t *testing.T) {
	m := newMockSource("host:/x", entity.GlobalScope())
	m.addEntity(entity.KindCommand, "deploy-cmd", nil)
	env := newE2E(t, "", m)

	resp := env.get("/")
	if resp.StatusCode != 200 {
		t.Fatalf("/ status = %d", resp.StatusCode)
	}
	body := readBody(t, resp)

	// Server-rendered entity name should be in the HTML.
	if !strings.Contains(body, "deploy-cmd") {
		t.Errorf("rendered HTML missing entity name 'deploy-cmd'")
	}
	// HTMX must be loaded for the partial-swap UX.
	if !strings.Contains(body, "htmx") {
		t.Error("HTMX script not in rendered HTML")
	}
}

func TestE2E_PartialEntityIsHTML(t *testing.T) {
	m := newMockSource("host:/x", entity.GlobalScope())
	e := m.addEntity(entity.KindCommand, "deploy", []byte("# deploy script"))
	env := newE2E(t, "", m)

	resp := env.get("/partials/entity?id=" + url.QueryEscape(e.ID))
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "deploy") {
		t.Errorf("partial missing entity name: %s", body)
	}
	if !strings.Contains(body, "# deploy script") {
		t.Errorf("partial missing entity content: %s", body)
	}
}

// ─── containers (no docker) ─────────────────────────────────────────────────

func TestE2E_ContainersEndpointWithoutDocker(t *testing.T) {
	env := newE2E(t, "")
	resp := env.get("/api/containers")
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := strings.TrimSpace(readBody(t, resp)); got != "[]" {
		t.Errorf("body = %q, want []", got)
	}
}

func TestE2E_ContainerStartWithoutDockerReturns503(t *testing.T) {
	env := newE2E(t, "")
	resp := env.post("/api/container/start?id=abc", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

// ─── shell + asset reachability ─────────────────────────────────────────────
//
// These tests guard the M1 (vendoring + embed.FS) and M3 (JS-as-module)
// migrations described in docs/frontend-architecture.md §16. Today they
// pass vacuously for same-origin asset URLs because the only same-origin
// asset is /favicon.svg; once vendored CDN deps land they start protecting
// every <link>/<script src> tag the index page emits.

// TestE2E_FaviconReachable pins the only same-origin static asset the page
// references today. After M1 the asset-link crawler subsumes this, but the
// dedicated test stays as a stable contract for any deploy smoke check.
func TestE2E_FaviconReachable(t *testing.T) {
	env := newE2E(t, "")
	resp := env.get("/favicon.svg")
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("/favicon.svg status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "image/svg") {
		t.Errorf("Content-Type = %q, want image/svg+xml", ct)
	}
}

// TestE2E_IndexShellHasJSSelectors asserts every id/data-* the inline JS
// reaches for is present in the rendered shell. This is the contract the
// M3 step (move inline JS into static/js/app.js) relies on — Go cannot
// parse the JS, so the test guarantees the DOM hooks the JS expects exist.
func TestE2E_IndexShellHasJSSelectors(t *testing.T) {
	env := newE2E(t, "")
	resp := env.get("/")
	if resp.StatusCode != 200 {
		t.Fatalf("/ status = %d", resp.StatusCode)
	}
	body := readBody(t, resp)
	for _, want := range []string{
		// Containers reached for by id in the inline JS.
		`id="entity-list"`,
		`id="preview"`,
		`id="search"`,
		`id="kind-bar"`,
		`id="source-tabs-bar"`,
		`id="empty-list"`,
		`id="project-overview"`,
		// View-tab buttons used by showView().
		`id="vtab-entities"`,
		`id="vtab-llama"`,
		`id="vtab-services"`,
		// Pills the JS toggles by data-kind.
		`data-kind="all"`,
		`data-kind="mcp_server"`,
		`data-kind="command"`,
		`data-kind="agent"`,
		`data-kind="skill"`,
		`data-kind="hook"`,
		`data-kind="memory"`,
		`data-kind="claude_md"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("rendered index missing JS selector %q", want)
		}
	}
}

// TestE2E_LoginPageHasFormFields verifies the login template renders the
// fields the auth flow needs. TestE2E_LoginPageAccessibleWithoutAuth only
// checked status + Content-Type; this fills in the form contract.
func TestE2E_LoginPageHasFormFields(t *testing.T) {
	env := newE2E(t, "secret")
	resp := env.get("/login?next=/services")
	body := readBody(t, resp)
	for _, want := range []string{
		`method="POST"`,
		`action="/login"`,
		`name="token"`,
		`type="password"`,
		`name="next"`,
		`value="/services"`,
		`type="submit"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("login page missing %q", want)
		}
	}
}

// TestE2E_SameOriginAssetsResolve crawls every <link href="/..."> and
// <script src="/..."> URL in the rendered index page and asserts each one
// returns 200. Today this is largely vacuous (the page references CDN URLs
// for htmx/fuse/marked/codemirror, which have a scheme and are skipped, plus
// /favicon.svg which is dedicated-tested above). After M1 lands the same
// test starts protecting the full vendored asset pipeline — every missing
// embed.FS entry surfaces here.
//
// Deliberately scheme-aware: external URLs (http://, https://) are recorded
// in the test log for visibility but not fetched, so the test does not flake
// on offline CI runners.
func TestE2E_SameOriginAssetsResolve(t *testing.T) {
	env := newE2E(t, "")
	resp := env.get("/")
	body := readBody(t, resp)

	urls := extractAssetURLs(body)
	if len(urls) == 0 {
		t.Fatal("no <link>/<script> asset URLs found — extraction regex likely broken")
	}

	sameOrigin := 0
	external := 0
	for _, u := range urls {
		switch {
		case strings.HasPrefix(u, "http://"), strings.HasPrefix(u, "https://"):
			t.Logf("external asset (skipped): %s", u)
			external++
		case strings.HasPrefix(u, "/"):
			r := env.get(u)
			r.Body.Close()
			if r.StatusCode != 200 {
				t.Errorf("asset %s returned %d, want 200", u, r.StatusCode)
			}
			sameOrigin++
		default:
			t.Errorf("unexpected asset URL form: %q", u)
		}
	}
	t.Logf("checked %d same-origin assets, skipped %d external", sameOrigin, external)
}

// extractAssetURLs returns every URL referenced by a <link href="..."> or
// <script src="..."> tag in the given HTML. Pragmatic regex extraction is
// adequate here — the full HTML parser would add a dependency for one test.
func extractAssetURLs(html string) []string {
	var out []string
	for _, attr := range []string{"href", "src"} {
		needle := attr + `="`
		i := 0
		for {
			start := strings.Index(html[i:], needle)
			if start < 0 {
				break
			}
			start += i + len(needle)
			end := strings.Index(html[start:], `"`)
			if end < 0 {
				break
			}
			u := html[start : start+end]
			i = start + end + 1
			// Skip data: URIs, anchors, and inline SVG attributes that
			// happen to use href= (e.g. SVG xlink:href). Asset reachability
			// only cares about loadable URLs.
			if strings.HasPrefix(u, "data:") || strings.HasPrefix(u, "#") {
				continue
			}
			// We only care about <link href> and <script src> — the regex-y
			// approach catches anchor href= too, but those aren't asset
			// links. Filter by scheme/path shape: external (with scheme)
			// or absolute path, anything else is page navigation.
			if strings.HasPrefix(u, "/") || strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
				out = append(out, u)
			}
		}
	}
	return out
}

// TestE2E_IndexHasNoLeftoverCDN_AfterM1 guards against CDN URLs sneaking
// back in for the three libraries M1 vendored (htmx, fuse, marked).
//
// esm.sh is intentionally NOT in the banned list yet — CodeMirror is loaded
// from esm.sh as a module graph and vendoring it requires a bundle step
// that's deferred to M5 (see docs/frontend-architecture.md §16). Add
// "esm.sh" here once M5 lands.
func TestE2E_IndexHasNoLeftoverCDN_AfterM1(t *testing.T) {
	env := newE2E(t, "")
	resp := env.get("/")
	body := readBody(t, resp)
	for _, banned := range []string{"unpkg.com", "cdn.jsdelivr.net"} {
		if strings.Contains(body, banned) {
			t.Errorf("rendered index references CDN %q after M1", banned)
		}
	}
}

// ─── multi-step session flow ────────────────────────────────────────────────

// Walks the same path a real user follows: redirect-to-login → login →
// list entities → preview one → edit → confirm update → logout → redirect.
func TestE2E_FullUserSession(t *testing.T) {
	m := newMockSource("host:/x", entity.GlobalScope())
	e := m.addEntity(entity.KindCommand, "deploy", []byte("v1"))
	env := newE2E(t, "secret", m)

	// 1. Anonymous request → redirect.
	resp := env.get("/api/entities")
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("anonymous request status = %d, want 303", resp.StatusCode)
	}

	// 2. Login.
	env.login("secret")

	// 3. List entities.
	var ents []entity.Entity
	env.getJSON("/api/entities", &ents)
	if len(ents) != 1 {
		t.Fatalf("listed %d entities, want 1", len(ents))
	}

	// 4. Preview the entity (HTMX partial).
	resp = env.get("/partials/entity?id=" + url.QueryEscape(e.ID))
	body := readBody(t, resp)
	if !strings.Contains(body, "v1") {
		t.Errorf("preview missing v1 content: %s", body)
	}

	// 5. Write new content.
	resp = env.post("/api/entity?id="+url.QueryEscape(e.ID), strings.NewReader("v2"))
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("write status = %d", resp.StatusCode)
	}

	// 6. Re-read content directly — should reflect the write.
	resp = env.get("/api/entity/content?id=" + url.QueryEscape(e.ID))
	if got := readBody(t, resp); got != "v2" {
		t.Errorf("post-write content = %q, want v2", got)
	}

	// 7. Logout.
	resp = env.post("/logout", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("logout status = %d", resp.StatusCode)
	}

	// 8. Final request without session → redirect.
	resp = env.get("/api/entities")
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("post-logout status = %d, want 303", resp.StatusCode)
	}
}
