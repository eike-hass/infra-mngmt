package web

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/eike-hass/infra-mngmt/internal/containers"
)

// odDecl builds a minimal open-design declaration where the token-stats
// service's usage URL is overridable so tests can point it at a stub server.
func odDecl(usageURL string) containers.Container {
	return containers.Container{
		Name:        "open-design",
		Kind:        "open-design",
		ComposeFile: "/abs/compose.yaml",
		Services: []containers.ServiceEntry{
			{Container: "open-design", Role: "web", URL: "http://localhost:7456"},
			{Container: "od-token-stats", Role: "token-stats", URL: usageURL},
		},
	}
}

func TestHandleOpenDesignTokenStatsRendersSummary(t *testing.T) {
	// Stub sidecar returning a realistic /usage payload.
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/usage" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"totals": {
				"sessions": 12, "messages": 345, "cache_hit_pct": 87.3,
				"tokens": {
					"input": 1000, "output": 200, "reasoning": 5,
					"cache_read": 50000, "cache_write": 0
				}
			}
		}`))
	}))
	defer stub.Close()

	s := &Server{containerDecls: []containers.Container{odDecl(stub.URL)}}

	req := httptest.NewRequest(http.MethodGet,
		"/partials/open-design/token-stats?name=od-token-stats", nil)
	w := httptest.NewRecorder()
	s.handleOpenDesignTokenStats(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{
		"12", "345", // sessions, messages
		"1000",  // input tokens
		"200",   // output tokens
		"50000", // cache_read
		"87.3%", // cache hit pct
		"ods-tokens-summary",
		"ods-tokens-refresh",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\n--- body ---\n%s", want, body)
		}
	}
}

func TestHandleOpenDesignTokenStatsSidecarUnreachable(t *testing.T) {
	// Point usage_url at a closed port so Dial fails fast.
	s := &Server{containerDecls: []containers.Container{
		odDecl("http://127.0.0.1:1"), // port 1 is reserved, always refuses
	}}

	req := httptest.NewRequest(http.MethodGet,
		"/partials/open-design/token-stats?name=od-token-stats", nil)
	w := httptest.NewRecorder()
	s.handleOpenDesignTokenStats(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (error partial), got %d", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{
		"ods-tokens-error",
		"unreachable",
		"ods-tokens-refresh", // retry button must still render
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\n--- body ---\n%s", want, body)
		}
	}
}

func TestHandleOpenDesignTokenStatsSidecarNon200(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer stub.Close()

	s := &Server{containerDecls: []containers.Container{odDecl(stub.URL)}}

	req := httptest.NewRequest(http.MethodGet,
		"/partials/open-design/token-stats?name=od-token-stats", nil)
	w := httptest.NewRecorder()
	s.handleOpenDesignTokenStats(w, req)

	if !strings.Contains(w.Body.String(), "HTTP 500") {
		t.Errorf("expected HTTP 500 mentioned in error, got: %s", w.Body.String())
	}
}

func TestHandleOpenDesignTokenStatsMissingName(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet,
		"/partials/open-design/token-stats", nil)
	w := httptest.NewRecorder()
	s.handleOpenDesignTokenStats(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing name, got %d", w.Code)
	}
}

func TestHandleOpenDesignTokenStatsUnknownName(t *testing.T) {
	// Declaration list doesn't contain a token-stats service named "ghost".
	s := &Server{containerDecls: []containers.Container{odDecl("http://x")}}

	req := httptest.NewRequest(http.MethodGet,
		"/partials/open-design/token-stats?name=ghost", nil)
	w := httptest.NewRecorder()
	s.handleOpenDesignTokenStats(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (error partial), got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "no token-stats service declared") {
		t.Errorf("expected unknown-name error in body, got: %s", body)
	}
}

func TestBuildContainerProjectGroupsSkipsDeclsWithoutServices(t *testing.T) {
	s := &Server{containerDecls: []containers.Container{
		{Name: "plain"},
		{Name: "vault", Kind: "mcp-fs", MCPFS: &containers.MCPFSConfig{Control: "http://x"}},
	}}
	views := s.buildContainerProjectGroups(context.Background())
	if len(views) != 0 {
		t.Errorf("expected zero views (no services[]), got %d", len(views))
	}
}

func TestBuildContainerProjectGroupsRollsUpToStoppedWithoutDocker(t *testing.T) {
	// docker == nil → FindByName not invoked, every service stays "missing".
	// With zero services running, the project rolls up to "stopped".
	s := &Server{containerDecls: []containers.Container{odDecl("http://x")}}
	views := s.buildContainerProjectGroups(context.Background())
	if len(views) != 1 {
		t.Fatalf("expected 1 view, got %d", len(views))
	}
	v := views[0]
	if v.Name != "open-design" || v.Kind != "open-design" {
		t.Errorf("name/kind = %q/%q, want open-design/open-design", v.Name, v.Kind)
	}
	if v.WebURL != "http://localhost:7456" {
		t.Errorf("WebURL = %q (should come from role=web service URL)", v.WebURL)
	}
	if v.TokenStatsContainer != "od-token-stats" {
		t.Errorf("TokenStatsContainer = %q, want od-token-stats", v.TokenStatsContainer)
	}
	if len(v.Containers) != 2 {
		t.Errorf("Containers count = %d, want 2", len(v.Containers))
	}
	if v.StateClass != "stopped" {
		t.Errorf("StateClass = %q, want stopped (no services running)", v.StateClass)
	}
}

func TestBuildContainerProjectGroupsHandlesPlainComposeProject(t *testing.T) {
	// Non-OD compose project — no kind, no role-required URLs. The view still
	// gets built (purely structural grouping); WebURL/TokenStatsContainer
	// stay empty since the entry doesn't surface those roles.
	s := &Server{containerDecls: []containers.Container{{
		Name:        "my-stack",
		ComposeFile: "/abs/c.yaml",
		Services: []containers.ServiceEntry{
			{Container: "web"}, {Container: "api"}, {Container: "cache"},
		},
	}}}
	views := s.buildContainerProjectGroups(context.Background())
	if len(views) != 1 {
		t.Fatalf("want 1 view, got %d", len(views))
	}
	v := views[0]
	if v.Name != "my-stack" || v.Kind != "" {
		t.Errorf("name/kind = %q/%q", v.Name, v.Kind)
	}
	if v.WebURL != "" || v.TokenStatsContainer != "" {
		t.Errorf("unexpected OD-specific fields: WebURL=%q TokenStatsContainer=%q", v.WebURL, v.TokenStatsContainer)
	}
	if len(v.Containers) != 3 {
		t.Errorf("Containers count = %d, want 3", len(v.Containers))
	}
}

func TestServicesTemplateRendersProjectGroupAndCard(t *testing.T) {
	project := containerProjectView{
		Name:                "open-design",
		Description:         "OD compose project",
		ComposeFile:         "/abs/compose.yaml",
		Kind:                "open-design",
		WebURL:              "http://localhost:7456",
		TokenStatsContainer: "od-token-stats",
		State:               "running",
		StateClass:          "running",
		Containers: []containerView{
			{Name: "open-design", State: "running", StateClass: "running", Status: "Up 2m", ID: "abc123"},
			{Name: "od-token-stats", State: "running", StateClass: "running", Status: "Up 2m"},
		},
	}
	data := servicesPageData{
		ContainerProjects:  []containerProjectView{project},
		OpenDesignProjects: []containerProjectView{project},
	}
	tmpl := parseTemplate("svc", "templates/services.html.tmpl")
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		t.Fatalf("template.Execute: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		// Containers section: project group with header + member rows.
		`<tr class="project-header" data-project="open-design"`,
		`project-disclosure`, // SVG chevron in the disclosure header
		`project-kind-label">compose`,
		`/decl-container/stop?name=open-design`, // lifecycle button on header
		`<tr class="project-member" data-project="open-design"`,
		// OD card section: slim, no buttons, no per-service pills.
		`id="open-designs-section"`,
		`open-design-card-name">open-design`,
		`http://localhost:7456`, // web URL link
		`hx-get="/partials/open-design/token-stats?name=od-token-stats"`,
		`hx-trigger="load"`,
		`id="ods-pill-open-design"`, // rolled-up pill (also via OOB)
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q", want)
		}
	}
	// Negative check: the card should NOT carry lifecycle buttons anymore.
	if strings.Contains(out, `open-design-card-actions`) {
		t.Errorf("card should not render lifecycle buttons (moved to project group header)")
	}
}
