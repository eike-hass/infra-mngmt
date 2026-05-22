package web

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/eike-hass/infra-mngmt/internal/containers"
	"github.com/eike-hass/infra-mngmt/internal/rates"
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
		"12",                // sessions
		"345",               // messages
		"1.0k",              // input tokens (fmtNum: 1000 → "1.0k")
		"200",               // output tokens
		"50k",               // cache_read (50000 → "50k")
		"87.3%",             // cache hit pct (totals.cache_hit_pct passthrough)
		`class="ods-body"`,  // new wrapper
		`class="ods-stat"`,  // aggregate cell
		`ods-stat-accent`,   // total cost cell
		`id="ods-snapshot-`, // OOB snapshot slot
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
		"ods-body-error",
		"ods-body-error-unreachable", // network failure kind
		"cannot connect to sidecar",  // user-facing copy for network failures
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

	body := w.Body.String()
	for _, want := range []string{
		"ods-body-error-http", // HTTP-error kind, distinct from network failure
		"HTTP 500",            // status code surfaced
		"boom",                // response body snippet surfaced so user sees why
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\n--- body ---\n%s", want, body)
		}
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
		// OD card section: per-project chrome + meta row.
		`id="open-design-open-design"`,              // per-project wrapper
		`<span class="svc-name">open design</span>`, // section header label
		`class="ods-name`,                           // meta-row project name
		`localhost:7456`,                            // web URL chip (scheme stripped)
		`hx-get="/partials/open-design/token-stats?name=od-token-stats"`,
		`hx-trigger="load"`,
		`id="ods-pill-open-design"`,        // rolled-up pill on the project chrome
		`id="ods-snapshot-od-token-stats"`, // snapshot slot keyed off TokenStatsContainer (OOB target)
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

// ── Per-model rollup + cost / color / label helpers ─────────────────────────
//
// These functions were shipped without unit coverage; backfilling now so the
// behavior is locked before the next refactor pass.

// TestRollupByModel covers the three things rollupByModel does that the
// caller relies on: group by model id, sum tokens + messages, weighted
// cache-hit %, and sort by total tokens descending.
func TestRollupByModel(t *testing.T) {
	// Two sessions of sonnet, one of haiku, one of an unknown id (no rate).
	report := tokenStatsReport{}
	report.Sessions = []struct {
		Model       string  `json:"model"`
		Messages    int     `json:"messages"`
		CacheHitPct float64 `json:"cache_hit_pct"`
		Tokens      struct {
			Input      int64 `json:"input"`
			Output     int64 `json:"output"`
			Reasoning  int64 `json:"reasoning"`
			CacheRead  int64 `json:"cache_read"`
			CacheWrite int64 `json:"cache_write"`
		} `json:"tokens"`
	}{
		{
			Model: "claude-sonnet-4-5", Messages: 10,
			Tokens: struct {
				Input      int64 `json:"input"`
				Output     int64 `json:"output"`
				Reasoning  int64 `json:"reasoning"`
				CacheRead  int64 `json:"cache_read"`
				CacheWrite int64 `json:"cache_write"`
			}{Input: 1_000_000, Output: 200_000, Reasoning: 50_000, CacheRead: 3_000_000, CacheWrite: 100_000},
		},
		{
			Model: "claude-sonnet-4-5", Messages: 5,
			Tokens: struct {
				Input      int64 `json:"input"`
				Output     int64 `json:"output"`
				Reasoning  int64 `json:"reasoning"`
				CacheRead  int64 `json:"cache_read"`
				CacheWrite int64 `json:"cache_write"`
			}{Input: 500_000, Output: 100_000, Reasoning: 0, CacheRead: 1_500_000, CacheWrite: 50_000},
		},
		{
			Model: "claude-haiku-4-5", Messages: 20,
			Tokens: struct {
				Input      int64 `json:"input"`
				Output     int64 `json:"output"`
				Reasoning  int64 `json:"reasoning"`
				CacheRead  int64 `json:"cache_read"`
				CacheWrite int64 `json:"cache_write"`
			}{Input: 200_000, Output: 50_000, Reasoning: 0, CacheRead: 100_000, CacheWrite: 0},
		},
		{
			Model: "exotic/unknown-v0", Messages: 3,
			Tokens: struct {
				Input      int64 `json:"input"`
				Output     int64 `json:"output"`
				Reasoning  int64 `json:"reasoning"`
				CacheRead  int64 `json:"cache_read"`
				CacheWrite int64 `json:"cache_write"`
			}{Input: 50_000, Output: 10_000, Reasoning: 0, CacheRead: 0, CacheWrite: 0},
		},
	}

	rates := map[string]rates.Rate{
		"claude-sonnet-4-5": {In: 3, Out: 15, CacheRead: 0.30},
		"claude-haiku-4-5":  {In: 1, Out: 5, CacheRead: 0.10},
		// exotic/unknown-v0: deliberately absent → HasCost should be false
	}
	out := rollupByModel(&report, rates)

	if len(out) != 3 {
		t.Fatalf("rollup returned %d entries, want 3 distinct models", len(out))
	}

	byID := map[string]openDesignModelStats{}
	for _, m := range out {
		byID[m.ID] = m
	}

	// Aggregation: two sonnet sessions should fold into one entry.
	sonnet, ok := byID["claude-sonnet-4-5"]
	if !ok {
		t.Fatalf("missing claude-sonnet-4-5 in rollup")
	}
	if sonnet.Sessions != 2 {
		t.Errorf("sonnet.Sessions = %d, want 2", sonnet.Sessions)
	}
	if sonnet.Messages != 15 {
		t.Errorf("sonnet.Messages = %d, want 15", sonnet.Messages)
	}
	if sonnet.TokensIn != 1_500_000 || sonnet.TokensOut != 300_000 || sonnet.Reasoning != 50_000 {
		t.Errorf("sonnet tokens summed wrong: in=%d out=%d reasoning=%d", sonnet.TokensIn, sonnet.TokensOut, sonnet.Reasoning)
	}
	if sonnet.CacheRead != 4_500_000 {
		t.Errorf("sonnet.CacheRead = %d, want 4_500_000", sonnet.CacheRead)
	}
	// Weighted cache-hit %: cache_read / (cache_read + input) = 4.5M / 6M = 75%
	if got := sonnet.CacheHitPct; got < 74.9 || got > 75.1 {
		t.Errorf("sonnet.CacheHitPct = %.2f, want ~75.0", got)
	}
	// Cost rollup: sonnet has a rate, should compute.
	if !sonnet.HasCost {
		t.Errorf("sonnet.HasCost = false, want true (rate configured)")
	}
	// Expected cost: (1.5M × $3 + 4.5M × $0.30 + (300k + 50k) × $15) / 1M
	//              = (4.5 + 1.35 + 5.25) = $11.10
	if got := sonnet.Cost; got < 11.09 || got > 11.11 {
		t.Errorf("sonnet.Cost = $%.4f, want ~$11.10", got)
	}

	// Unknown model: should still be in the rollup but with HasCost=false.
	exotic, ok := byID["exotic/unknown-v0"]
	if !ok {
		t.Fatalf("missing exotic/unknown-v0 in rollup")
	}
	if exotic.HasCost {
		t.Errorf("exotic.HasCost = true, want false (no rate configured)")
	}
	if exotic.Cost != 0 {
		t.Errorf("exotic.Cost = %f, want 0 when HasCost=false", exotic.Cost)
	}

	// Sort order: descending by (input + output). Sonnet=1.8M > haiku=0.25M > exotic=0.06M.
	if out[0].ID != "claude-sonnet-4-5" {
		t.Errorf("out[0].ID = %q, want claude-sonnet-4-5 (heaviest spender first)", out[0].ID)
	}
	if out[1].ID != "claude-haiku-4-5" {
		t.Errorf("out[1].ID = %q, want claude-haiku-4-5", out[1].ID)
	}
	if out[2].ID != "exotic/unknown-v0" {
		t.Errorf("out[2].ID = %q, want exotic/unknown-v0", out[2].ID)
	}
}

func TestRollupByModelEmptySessions(t *testing.T) {
	out := rollupByModel(&tokenStatsReport{}, map[string]rates.Rate{})
	if len(out) != 0 {
		t.Errorf("empty rollup returned %d entries, want 0", len(out))
	}
}

func TestOdCostFor(t *testing.T) {
	rates := map[string]rates.Rate{
		"claude-sonnet-4-5": {In: 3, Out: 15, CacheRead: 0.30},
	}

	// Known model, normal token mix.
	cost, ok := odCostFor("claude-sonnet-4-5", 1_000_000, 200_000, 50_000, 3_000_000, rates)
	if !ok {
		t.Fatalf("odCostFor returned ok=false for a configured model")
	}
	// (1M × 3 + 3M × 0.3 + (200k + 50k) × 15) / 1M = 3.0 + 0.9 + 3.75 = $7.65
	if cost < 7.64 || cost > 7.66 {
		t.Errorf("odCostFor returned %.4f, want ~7.65", cost)
	}

	// Unknown model — no fallback.
	_, ok = odCostFor("unknown-model", 1000, 200, 0, 0, rates)
	if ok {
		t.Errorf("odCostFor returned ok=true for an unknown model; expected (0, false)")
	}

	// Empty token totals on a known model — cost should be zero, not error.
	cost, ok = odCostFor("claude-sonnet-4-5", 0, 0, 0, 0, rates)
	if !ok {
		t.Errorf("odCostFor returned ok=false for zero tokens; expected (0, true)")
	}
	if cost != 0 {
		t.Errorf("odCostFor returned %f for zero tokens, want 0", cost)
	}
}

func TestOdColorFor(t *testing.T) {
	// Determinism: same id → same color across calls.
	a := odColorFor("claude-sonnet-4-5")
	b := odColorFor("claude-sonnet-4-5")
	if a != b {
		t.Errorf("odColorFor non-deterministic: %q vs %q", a, b)
	}

	// Format: oklch(68% 0.18 <hue>) where 0 ≤ hue < 360.
	for _, id := range []string{"claude-sonnet-4-5", "x", "unsloth/gemma-4-26B"} {
		got := odColorFor(id)
		if !strings.HasPrefix(got, "oklch(68% 0.18 ") {
			t.Errorf("odColorFor(%q) = %q, missing oklch prefix", id, got)
		}
		if !strings.HasSuffix(got, ")") {
			t.Errorf("odColorFor(%q) = %q, missing closing paren", id, got)
		}
	}

	// Empty id → safe fallback (the var(--text2) sentinel).
	if got := odColorFor(""); got != "var(--text2)" {
		t.Errorf("odColorFor(\"\") = %q, want var(--text2)", got)
	}

	// Spread: close inputs that hashed badly under FNV (the original bug)
	// now land far apart with SHA-256. Both sonnet variants should be
	// > 30° apart to be visually distinct.
	hueOf := func(s string) int {
		// Extract the integer between the last space and the closing paren.
		p := strings.LastIndex(s, " ")
		q := strings.LastIndex(s, ")")
		if p < 0 || q < 0 || q <= p {
			t.Fatalf("could not parse hue from %q", s)
		}
		var n int
		_, _ = fmt.Sscanf(s[p+1:q], "%d", &n)
		return n
	}
	h1 := hueOf(odColorFor("claude-sonnet-4-5"))
	h2 := hueOf(odColorFor("claude-sonnet-4"))
	d := h1 - h2
	if d < 0 {
		d = -d
	}
	if d > 180 {
		d = 360 - d
	}
	if d < 30 {
		t.Errorf("sonnet-4-5 (hue %d) and sonnet-4 (hue %d) only %d° apart; SHA-256 spread regression?", h1, h2, d)
	}
}

func TestOdShortFor(t *testing.T) {
	// Curated short labels for well-known model ids.
	curated := map[string]string{
		"claude-sonnet-4-5":   "sonnet 4.5",
		"claude-haiku-4-5":    "haiku 4.5",
		"claude-opus-4-1":     "opus 4.1",
		"claude-sonnet-4":     "sonnet 4",
		"claude-haiku-3-5":    "haiku 3.5",
		"llama-3.1-70b-local": "llama 3.1 70b",
	}
	for id, want := range curated {
		if got := odShortFor(id); got != want {
			t.Errorf("odShortFor(%q) = %q, want %q", id, got, want)
		}
	}

	// Auto-derivation fallback: claude- prefix stripped, dashes → spaces.
	autoCases := map[string]string{
		"claude-sonnet-4-7": "sonnet 4 7", // future Anthropic id with no curated label
		"some-other-model":  "some other model",
		"":                  "",
	}
	for id, want := range autoCases {
		if got := odShortFor(id); got != want {
			t.Errorf("odShortFor(%q) = %q, want %q", id, got, want)
		}
	}
}

func TestOdMetaFor(t *testing.T) {
	// Combines odShortFor + odColorFor. Verify both surfaces are wired.
	meta := odMetaFor("claude-sonnet-4-5")
	if meta.Short != "sonnet 4.5" {
		t.Errorf("meta.Short = %q, want sonnet 4.5", meta.Short)
	}
	if !strings.HasPrefix(meta.Color, "oklch(") {
		t.Errorf("meta.Color = %q, expected oklch(...)", meta.Color)
	}
}

// TestHandleOpenDesignTokenStatsRendersFullCard exercises the partial with
// realistic sessions[] data + a configured rate. Confirms:
//   - per-model chips render with id-derived colors
//   - per-chip cost line uses the configured rate
//   - aggregate row's ≈ cost cell is HasTotalCost=true ($..., not —)
//   - generated_at timestamp lands in the OOB snapshot swap with HH:MM:SS format
func TestHandleOpenDesignTokenStatsRendersFullCard(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/usage" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"generated_at": "2026-05-21T12:34:02Z",
			"totals": {
				"sessions": 1, "messages": 10, "cache_hit_pct": 60.0,
				"tokens": {"input": 1000000, "output": 200000, "reasoning": 0, "cache_read": 1500000, "cache_write": 0}
			},
			"sessions": [
				{"model": "claude-sonnet-4-5", "messages": 10, "cache_hit_pct": 60.0,
				 "tokens": {"input": 1000000, "output": 200000, "reasoning": 0, "cache_read": 1500000, "cache_write": 0}}
			]
		}`))
	}))
	defer stub.Close()

	s := &Server{
		containerDecls: []containers.Container{odDecl(stub.URL)},
		modelRates: map[string]rates.Rate{
			"claude-sonnet-4-5": {In: 3, Out: 15, CacheRead: 0.30},
		},
	}

	req := httptest.NewRequest(http.MethodGet,
		"/partials/open-design/token-stats?name=od-token-stats", nil)
	w := httptest.NewRecorder()
	s.handleOpenDesignTokenStats(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{
		`class="ods-chip"`,   // per-model chip rendered (sessions[] consumed)
		"sonnet 4.5",         // curated short label
		`oklch(68% 0.18`,     // derived chip color (via safeCSS in template)
		`200k out`,           // tokens out via fmtNum
		`ods-stat-accent">$`, // ≈ cost cell renders a dollar value, not "—"
		`hx-swap-oob="true"`, // OOB snapshot patch
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\n--- body ---\n%s", want, body)
		}
	}
	// Snapshot timestamp is rendered in local time (TZ-dependent), so just
	// confirm the slot contains a HH:MM:SS-shaped value, not a specific time.
	if !regexp.MustCompile(`snapshot · \d{2}:\d{2}:\d{2}`).MatchString(body) {
		t.Errorf("snapshot timestamp missing or malformed; expected `snapshot · HH:MM:SS`, body:\n%s", body)
	}
}

// TestHandleOpenDesignTokenStatsNoRateConfigured confirms a model id absent
// from the operator's rates map renders "—" in the per-chip cost slot and
// the aggregate cost cell — no built-in fallback.
func TestHandleOpenDesignTokenStatsNoRateConfigured(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"generated_at": "2026-05-21T12:00:00Z",
			"totals": {
				"sessions": 1, "messages": 1, "cache_hit_pct": 0,
				"tokens": {"input": 100, "output": 50, "reasoning": 0, "cache_read": 0, "cache_write": 0}
			},
			"sessions": [
				{"model": "some/unknown-model", "messages": 1, "cache_hit_pct": 0,
				 "tokens": {"input": 100, "output": 50, "reasoning": 0, "cache_read": 0, "cache_write": 0}}
			]
		}`))
	}))
	defer stub.Close()

	// modelRates is nil — no rates configured at all.
	s := &Server{containerDecls: []containers.Container{odDecl(stub.URL)}}

	req := httptest.NewRequest(http.MethodGet,
		"/partials/open-design/token-stats?name=od-token-stats", nil)
	w := httptest.NewRecorder()
	s.handleOpenDesignTokenStats(w, req)

	body := w.Body.String()
	// Chip renders the no-cost variant.
	if !strings.Contains(body, `ods-chip-cost-none">—`) {
		t.Errorf("per-chip cost should render the dash placeholder, got:\n%s", body)
	}
	// Aggregate cost cell also renders "—" (no model had a rate, HasTotalCost=false).
	if !strings.Contains(body, `ods-stat-accent">—`) {
		t.Errorf("aggregate ≈ cost cell should render —, got:\n%s", body)
	}
}
