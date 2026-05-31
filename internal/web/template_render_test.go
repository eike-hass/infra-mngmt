package web

// Render-coverage tests for the HTML templates migrated in M4 from Go
// raw-string constants into templates/*.html.tmpl files served via embed.FS
// (see docs/frontend-architecture.md §16). These pin down parse-time and
// render-time behavior so the migration cannot silently drop fields, named
// templates, or FuncMap dependencies.

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/eike-hass/infra-mngmt/internal/compose"
	"github.com/eike-hass/infra-mngmt/internal/entity"
)

// htmlTemplateFiles is the canonical list of every embedded template file the
// web package executes. Adding a new file without adding it here is a gap —
// the smoke test below would not protect the new template.
var htmlTemplateFiles = map[string]string{
	"index":                   "templates/index.html.tmpl",
	"entity_list":             "templates/entity_list.html.tmpl",
	"preview":                 "templates/preview.html.tmpl",
	"diagnose":                "templates/diagnose.html.tmpl",
	"blast_radius":            "templates/blast_radius.html.tmpl",
	"services":                "templates/services.html.tmpl",
	"logs":                    "templates/logs.html.tmpl",
	"llama":                   "templates/llama.html.tmpl",
	"login":                   "templates/login.html.tmpl",
	"promote_picker":          "templates/promote_picker.html.tmpl",
	"promote_result":          "templates/promote_result.html.tmpl",
	"container_controls":      "templates/container_controls.html.tmpl",
	"open_design_token_stats": "templates/open_design_token_stats.html.tmpl",
}

// TestAllHTMLConstantsParseWithFuncMap is the parse-only smoke test for every
// template the package owns. It catches the failure mode the M4 migration
// will hit most often: a FuncMap helper renamed in handlers.go but still
// referenced by a template, or a `{{define}}` name typo. Cheap to run, gives
// a single per-file error line on failure.
func TestAllHTMLConstantsParseWithFuncMap(t *testing.T) {
	// parseTemplate wraps template.Must — it panics on parse error.
	// Recover and report as a test failure so all templates are checked.
	for name, file := range htmlTemplateFiles {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("parseTemplate(%s, %s) panicked: %v", name, file, r)
				}
			}()
			parseTemplate(name, file)
		}()
	}
	// index composes with entity_list at runtime — assert the composition
	// handleIndex performs still parses cleanly. If this breaks, the index
	// page renders nothing.
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("index + entity_list composition panicked: %v", r)
			}
		}()
		parseTemplate("index", "templates/index.html.tmpl", "templates/entity_list.html.tmpl")
	}()
}

// ─── indexHTML ──────────────────────────────────────────────────────────────

// TestIndexTemplateRendersWithEmptyData verifies the page chrome (header,
// view tabs, kind bar, search) renders even when the entity list is empty.
// The empty case is the one that breaks first when migrating templates —
// `range` over a nil slice is silent, but missing tab markup is not.
func TestIndexTemplateRendersWithEmptyData(t *testing.T) {
	tmpl := parseTemplate("index", "templates/index.html.tmpl", "templates/entity_list.html.tmpl")
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, pageData{}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := buf.String()
	// Page shell must render — these are the structural anchors the JS
	// reaches for in template.go's inline script.
	for _, want := range []string{
		`id="entity-list"`,
		`id="preview"`,
		`id="search"`,
		`id="kind-bar"`,
		`id="source-tabs-bar"`,
		`id="vtab-entities"`,
		`id="vtab-llama"`,
		`id="vtab-services"`,
		`data-kind="all"`,
		`data-kind="mcp_server"`,
		`data-kind="command"`,
		`data-kind="agent"`,
		`data-kind="skill"`,
		`data-kind="hook"`,
		`data-kind="memory"`,
		`data-kind="claude_md"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered index missing %q", want)
		}
	}
	// Empty-state marker should be present and visible (no display:none style)
	// when there are zero entities.
	if !strings.Contains(out, `id="empty-list"`) {
		t.Errorf("rendered index missing #empty-list")
	}
	if strings.Contains(out, `id="empty-list" style="display:none"`) {
		t.Errorf("empty-list should be visible when Entities is empty:\n%s", out)
	}
}

// TestIndexTemplateRendersSourceTabsAndBuildChip verifies the dynamic regions
// of the page shell driven by Sources and Build. Today these are exercised
// only obliquely via TestE2E_IndexRendersEntities; the M6 step (real URL
// routes) will rewrite the tab-rendering path and needs an explicit anchor.
func TestIndexTemplateRendersSourceTabsAndBuildChip(t *testing.T) {
	data := pageData{
		Sources: []projectTab{
			{Key: "", Label: "~/.claude", Levels: []string{"global"}},
			{Key: "/home/u/repo", Label: "repo", Levels: []string{"project", "devcontainer"}},
		},
		Build: BuildInfo{Commit: "abc12345", VCSTime: "2024-01-15T12:00:00Z"},
	}
	tmpl := parseTemplate("index", "templates/index.html.tmpl", "templates/entity_list.html.tmpl")
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		`data-project="__all__"`,                    // "all" tab
		`data-project=""`,                           // global tab key
		`~/.claude`,                                 // global tab label
		`data-project="/home/u/repo"`,               // project tab key
		`repo<span class="scope-badge project"`,     // project tab label adjoins first level badge
		`scope-badge global`, `scope-badge project`, // scope badge class names
		`scope-badge devcontainer`,
		`>global<`, `>project<`, `>devcontainer<`, // full-length level labels in source-tab badges
		`abc12345`, // build chip commit short SHA
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered shell missing %q", want)
		}
	}
}

// Wake pill renders only when WakeURL is non-empty. Empty (default) must
// produce no `wake-chip` element and no wake.js include — otherwise pages
// with the feature disabled would still attempt to fetch /static/js/wake.js
// and run a no-op SW registration cycle.
func TestIndexTemplateWakePillGatedOnWakeURL(t *testing.T) {
	tmpl := parseTemplate("index", "templates/index.html.tmpl", "templates/entity_list.html.tmpl")

	// Off path
	var off bytes.Buffer
	if err := tmpl.Execute(&off, pageData{}); err != nil {
		t.Fatalf("Execute (off): %v", err)
	}
	if strings.Contains(off.String(), `id="wake-chip"`) {
		t.Errorf("wake-chip rendered with empty WakeURL")
	}
	if strings.Contains(off.String(), `js/wake.js`) {
		t.Errorf("wake.js script tag rendered with empty WakeURL")
	}

	// On path
	wakeURL := "http://localhost:9920/process/start/wsl-wake"
	var on bytes.Buffer
	if err := tmpl.Execute(&on, pageData{WakeURL: wakeURL}); err != nil {
		t.Fatalf("Execute (on): %v", err)
	}
	out := on.String()
	for _, want := range []string{
		`id="wake-chip"`,
		`data-wake-url="` + wakeURL + `"`,
		`js/wake.js`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered shell missing %q", want)
		}
	}
}

// ─── entityListInnerHTML ────────────────────────────────────────────────────

// TestEntityListInnerRendersAllKindGroups exercises the kind-grouping path
// that today is only verified by a single substring in TestE2E_IndexRendersEntities.
// One entity per kind, asserts the group header + entity card render for each.
func TestEntityListInnerRendersAllKindGroups(t *testing.T) {
	scope := entity.GlobalScope()
	all := []entity.Entity{
		{ID: "host:/x:mcp_server:m", Kind: entity.KindMCPServer, Name: "m", Scope: scope, Source: "host:/x"},
		{ID: "host:/x:command:c", Kind: entity.KindCommand, Name: "c", Scope: scope, Source: "host:/x"},
		{ID: "host:/x:agent:a", Kind: entity.KindAgent, Name: "a", Scope: scope, Source: "host:/x"},
		{ID: "host:/x:skill:s", Kind: entity.KindSkill, Name: "s", Scope: scope, Source: "host:/x"},
		{ID: "host:/x:hook:h", Kind: entity.KindHook, Name: "h", Scope: scope, Source: "host:/x"},
		{ID: "host:/x:memory:mem", Kind: entity.KindMemory, Name: "mem", Scope: scope, Source: "host:/x"},
		{ID: "host:/x:claude_md:CLAUDE.md", Kind: entity.KindClaudeMD, Name: "CLAUDE.md", Scope: scope, Source: "host:/x"},
	}
	tmpl := parseTemplate("partial", "templates/entity_list.html.tmpl")
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "entity-list-inner", pageData{Entities: all}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := buf.String()
	// Every kind gets its own group with the labels from kindLabels.
	for _, want := range []string{
		`data-kind="mcp_server"`, `>MCP Servers<`,
		`data-kind="command"`, `>Commands<`,
		`data-kind="agent"`, `>Agents<`,
		`data-kind="skill"`, `>Skills<`,
		`data-kind="hook"`, `>Hooks<`,
		`data-kind="memory"`, `>Memory<`,
		`data-kind="claude_md"`, `>CLAUDE.md<`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered list missing %q", want)
		}
	}
	// Entity cards must carry the data-* attributes the JS filter logic uses.
	for _, want := range []string{
		`data-id="host:/x:mcp_server:m"`,
		`data-name="m"`,
		`data-project=""`, // scope.Project is empty for global
		`data-scope="global"`,
		`class="entity-scope-tag global"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered entity card missing %q", want)
		}
	}
	// Empty-state marker should be hidden when entities are present.
	if !strings.Contains(out, `id="empty-list" style="display:none"`) {
		t.Errorf("empty-list should be hidden when Entities is non-empty")
	}
}

// TestEntityListInnerRendersToolBadgeAndLevel covers the entityTool /
// entityLevel / toolIcon pipeline — easy to break in M4 because it depends
// on three FuncMap helpers cooperating.
func TestEntityListInnerRendersToolBadgeAndLevel(t *testing.T) {
	all := []entity.Entity{
		{
			ID:   "host:/home/u/repo/.claude:command:c",
			Kind: entity.KindCommand, Name: "c",
			Scope:  entity.ProjectScope("/home/u/repo"),
			Source: "host:/home/u/repo/.claude",
		},
	}
	tmpl := parseTemplate("partial", "templates/entity_list.html.tmpl")
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "entity-list-inner", pageData{Entities: all}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `entity-tool-tag claude`) {
		t.Errorf("expected claude tool tag for .claude source path:\n%s", out)
	}
	if !strings.Contains(out, `entity-scope-tag project`) {
		t.Errorf("expected project scope tag for project entity:\n%s", out)
	}
}

// ─── previewHTML ────────────────────────────────────────────────────────────

// TestPreviewRendersFileBackedEntity covers the common case: an entity with
// content. Verifies the metadata + content body + edit toolbar render.
func TestPreviewRendersFileBackedEntity(t *testing.T) {
	e := entity.Entity{
		ID: "host:/x:command:deploy", Kind: entity.KindCommand, Name: "deploy",
		Scope: entity.GlobalScope(), Source: "host:/x", Path: "/home/u/.claude/commands/deploy.md",
	}
	tmpl := parseTemplate("preview", "templates/preview.html.tmpl")
	var buf bytes.Buffer
	err := tmpl.Execute(&buf, map[string]any{
		"Entity":  e,
		"Content": "echo hello",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		`class="preview-name">deploy<`,
		`meta-kind command`,
		`>command<`,
		`meta-level global`,
		`/home/u/.claude/commands/deploy.md`,
		`class="raw-src">echo hello<`,
		`class="edit-btn"`,
		`class="save-btn"`,
		`class="cancel-btn"`,
		`promote-btn`,
		// entity ID is now URL-query-escaped (qesc) so ':' and '/' survive
		`hx-get="/partials/promote-picker?id=host%3A%2Fx%3Acommand%3Adeploy"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered preview missing %q\n%s", want, out)
		}
	}
}

// TestPreviewRendersMCPServerCard covers the MCP-server branch where Content
// is empty and the template renders an attribute table instead.
func TestPreviewRendersMCPServerCard(t *testing.T) {
	e := entity.Entity{
		ID:     "host:/x:mcp_server:foo",
		Kind:   entity.KindMCPServer,
		Name:   "foo",
		Scope:  entity.GlobalScope(),
		Source: "host:/x",
		Attrs:  map[string]string{"type": "stdio", "command": "/usr/bin/foo"},
	}
	tmpl := parseTemplate("preview", "templates/preview.html.tmpl")
	var buf bytes.Buffer
	err := tmpl.Execute(&buf, map[string]any{
		"Entity":  e,
		"Content": "",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		`mcp-card`,
		`mcp-transport stdio`,
		`>command<`,
		`/usr/bin/foo`,
		`>scope<`,
		`global (~/.claude)`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered MCP preview missing %q\n%s", want, out)
		}
	}
	// Editor toolbar must NOT render when there is no Content.
	if strings.Contains(out, `class="edit-btn"`) {
		t.Errorf("edit toolbar should be hidden for content-less entity:\n%s", out)
	}
}

// TestPreviewRendersBrokenRefBanner covers the runtime-status branches that
// today are not exercised by any test.
func TestPreviewRendersBrokenRefBanner(t *testing.T) {
	e := entity.Entity{
		ID: "host:/x:mcp_server:foo", Kind: entity.KindMCPServer, Name: "foo",
		Scope: entity.GlobalScope(), Source: "host:/x",
	}
	tmpl := parseTemplate("preview", "templates/preview.html.tmpl")
	cases := []struct {
		state string
		want  string
	}{
		{"unresolved", "no matching process-compose process"},
		{"stopped", "is not running"},
		{"error", "is in error state"},
	}
	for _, tc := range cases {
		var buf bytes.Buffer
		err := tmpl.Execute(&buf, map[string]any{
			"Entity":    e,
			"Content":   "",
			"MCPStatus": &MCPStatus{State: tc.state, Instance: "wsl", Process: "foo"},
		})
		if err != nil {
			t.Fatalf("Execute(%s): %v", tc.state, err)
		}
		if !strings.Contains(buf.String(), tc.want) {
			t.Errorf("state=%s: missing banner text %q\n%s", tc.state, tc.want, buf.String())
		}
	}
}

// ─── loginHTML ──────────────────────────────────────────────────────────────

func TestLoginTemplateRendersFormFields(t *testing.T) {
	tmpl := parseTemplate("login", "templates/login.html.tmpl")
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, map[string]string{"Next": "/services", "Error": ""}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		`<form class="card" method="POST" action="/login">`,
		`name="token"`,
		`type="password"`,
		`name="next"`,
		`value="/services"`,
		`<button type="submit">sign in</button>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("login template missing %q", want)
		}
	}
	// Error block must NOT render when Error is empty.
	if strings.Contains(out, `class="error"`) {
		t.Errorf("error block should be hidden when Error is empty:\n%s", out)
	}
}

func TestLoginTemplateRendersErrorMessage(t *testing.T) {
	tmpl := parseTemplate("login", "templates/login.html.tmpl")
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, map[string]string{"Next": "/", "Error": "invalid token"}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `class="error">invalid token<`) {
		t.Errorf("error block missing or malformed:\n%s", out)
	}
}

// TestLoginTemplateEscapesNextAndError verifies auto-escaping holds for the
// two user-controlled values rendered into the form. If anyone ever migrates
// the login template to use template.HTML on these, this test fails loudly.
func TestLoginTemplateEscapesNextAndError(t *testing.T) {
	tmpl := parseTemplate("login", "templates/login.html.tmpl")
	var buf bytes.Buffer
	err := tmpl.Execute(&buf, map[string]string{
		"Next":  `"><script>alert(1)</script>`,
		"Error": `<img src=x onerror=alert(1)>`,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, `<script>alert(1)`) {
		t.Errorf("Next not escaped:\n%s", out)
	}
	if strings.Contains(out, `<img src=x onerror=`) {
		t.Errorf("Error not escaped:\n%s", out)
	}
}

// ─── logsHTML ──────────────────────────────────────────────────────────────

func TestLogsTemplateRendersLines(t *testing.T) {
	data := logsData{
		Instance: "wsl",
		Process:  "infra-mngmt-deploy",
		Lines: []compose.LogLine{
			{Time: "2024-01-15T12:00:00Z", Process: "deploy", Message: "build start"},
			{Time: "2024-01-15T12:00:01Z", Process: "deploy", Message: "build done"},
		},
	}
	tmpl := parseTemplate("logs", "templates/logs.html.tmpl")
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		`class="logs-panel"`,
		`class="log-time">2024-01-15T12:00:00Z<`,
		`build start`,
		`build done`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("logs template missing %q\n%s", want, out)
		}
	}
	if strings.Contains(out, `class="no-logs"`) {
		t.Errorf("no-logs marker should not appear when Lines is non-empty")
	}
}

func TestLogsTemplateRendersEmptyState(t *testing.T) {
	tmpl := parseTemplate("logs", "templates/logs.html.tmpl")
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, logsData{Instance: "wsl", Process: "p"}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(buf.String(), `class="no-logs">no logs<`) {
		t.Errorf("empty logs should render the no-logs marker:\n%s", buf.String())
	}
}

func TestLogsTemplateRendersError(t *testing.T) {
	tmpl := parseTemplate("logs", "templates/logs.html.tmpl")
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, logsData{Err: "instance wsl not configured"}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `error: instance wsl not configured`) {
		t.Errorf("error path missing in logs template:\n%s", out)
	}
}

// ─── promoteResultHTML ──────────────────────────────────────────────────────

func TestPromoteResultRendersOK(t *testing.T) {
	tmpl := parseTemplate("pr", "templates/promote_result.html.tmpl")
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, promoteResult{OK: true, Message: "copied command/deploy to repo"}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		`promote-result-card ok`,
		`done`,
		`copied command/deploy to repo`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("OK result missing %q\n%s", want, out)
		}
	}
	// Conflict-confirm button must not render in the OK case.
	if strings.Contains(out, `class="promote-confirm"`) {
		t.Errorf("OK result should not render confirm button")
	}
}

func TestPromoteResultRendersConflictWithRetry(t *testing.T) {
	r := promoteResult{
		Conflict: true,
		Message:  "deploy already exists at repo",
		From:     "host:/a:command:deploy",
		To:       "host:/b:command:deploy",
		NewName:  "deploy-prod",
		Mirror:   true,
	}
	tmpl := parseTemplate("pr", "templates/promote_result.html.tmpl")
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, r); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		`promote-result-card warn`,
		`already exists`,
		`deploy already exists at repo`,
		`name=deploy-prod`,
		`overwrite=true`,
		`mirror=true`,
		// from/to source IDs are now URL-query-escaped (qesc) in the hx-post
		// attribute so ':' and '/' don't corrupt the parsed query string.
		`from=host%3A%2Fa%3Acommand%3Adeploy`,
		`to=host%3A%2Fb%3Acommand%3Adeploy`,
		`class="promote-confirm"`,
		`class="promote-cancel"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("conflict result missing %q\n%s", want, out)
		}
	}
}

func TestPromoteResultRendersReadOnly(t *testing.T) {
	tmpl := parseTemplate("pr", "templates/promote_result.html.tmpl")
	var buf bytes.Buffer
	r := promoteResult{ReadOnly: true, Message: "vol:claude-config is read-only"}
	if err := tmpl.Execute(&buf, r); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		`promote-result-card error`,
		`read-only target`,
		`vol:claude-config is read-only`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("read-only result missing %q\n%s", want, out)
		}
	}
}

func TestPromoteResultRendersGenericError(t *testing.T) {
	// Neither OK, Conflict, nor ReadOnly — generic error branch.
	tmpl := parseTemplate("pr", "templates/promote_result.html.tmpl")
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, promoteResult{Message: "something broke"}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `promote-result-card error`) {
		t.Errorf("generic error variant missing class:\n%s", out)
	}
	if !strings.Contains(out, `something broke`) {
		t.Errorf("generic error missing message text:\n%s", out)
	}
}

// ─── §18.1: cache-busting for static assets ─────────────────────────────────

// TestStaticAssetURL_CacheBuster verifies that {{static}}-rendered URLs
// carry a `?v=<token>` query string once SetBuildInfo has run, so browsers
// re-fetch JS/CSS after a deploy without our having to rename files on disk.
//
// The static handler ignores query strings (it serves by path), so an URL
// with `?v=abc` resolves to the same content as one without — the suffix
// only affects HTTP caches keyed by full URL.
func TestStaticAssetURL_CacheBuster(t *testing.T) {
	// Save + restore the package-level buster so this test is hermetic.
	orig := assetCacheBuster.Load()
	t.Cleanup(func() {
		if orig != nil {
			assetCacheBuster.Store(orig)
		} else {
			empty := ""
			assetCacheBuster.Store(&empty)
		}
	})

	// Baseline: empty buster → no query string.
	setAssetCacheBuster("")
	if got := staticAssetURL("vendor/htmx.min.js"); got != "/static/vendor/htmx.min.js" {
		t.Errorf("empty buster: got %q, want plain path", got)
	}

	// After SetBuildInfo with a commit, the buster appears as ?v=<commit>.
	setAssetCacheBuster("abc12345")
	if got := staticAssetURL("vendor/htmx.min.js"); got != "/static/vendor/htmx.min.js?v=abc12345" {
		t.Errorf("with commit buster: got %q", got)
	}

	// Leading slash on the path is tolerated.
	if got := staticAssetURL("/css/app.css"); got != "/static/css/app.css?v=abc12345" {
		t.Errorf("leading-slash path: got %q", got)
	}
}

// TestSetBuildInfo_WiresCacheBuster verifies the integration: calling
// SetBuildInfo with a commit propagates into the {{static}} helper output.
// Guards the wiring in server.go's SetBuildInfo.
func TestSetBuildInfo_WiresCacheBuster(t *testing.T) {
	orig := assetCacheBuster.Load()
	t.Cleanup(func() {
		if orig != nil {
			assetCacheBuster.Store(orig)
		}
	})

	srv := New(nil, nil, "", nil, nil, nil, nil, nil, nil)
	srv.SetBuildInfo(BuildInfo{Commit: "deadbeef"})
	if got := staticAssetURL("vendor/x.js"); got != "/static/vendor/x.js?v=deadbeef" {
		t.Errorf("commit buster: got %q", got)
	}

	// Commit empty, epoch present → falls back to epoch.
	srv.SetBuildInfo(BuildInfo{BuildEpoch: "1700000000"})
	if got := staticAssetURL("vendor/x.js"); got != "/static/vendor/x.js?v=1700000000" {
		t.Errorf("epoch fallback: got %q", got)
	}

	// Both empty → no query string.
	srv.SetBuildInfo(BuildInfo{})
	if got := staticAssetURL("vendor/x.js"); got != "/static/vendor/x.js" {
		t.Errorf("empty BuildInfo: got %q, want plain path", got)
	}
}

// ─── §18.3: CSP + baseline security headers ─────────────────────────────────

// TestSecurityHeaders_SetOnEveryResponse verifies the middleware emits the
// CSP and supporting baseline headers on responses from every layer of the
// chi router: public routes, static assets, and authenticated routes.
// Guards docs/frontend-architecture.md §18.3.
func TestSecurityHeaders_SetOnEveryResponse(t *testing.T) {
	srv := New(nil, nil, "", nil, nil, nil, nil, nil, nil)
	// Pick three response sources that exercise different router subtrees.
	cases := []struct {
		name string
		path string
	}{
		{"public /favicon.svg", "/favicon.svg"},
		{"static /static/css/app.css", "/static/css/app.css"},
		{"public /api/version", "/api/version"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, c.path, nil))
			h := rr.Header()
			if got := h.Get("Content-Security-Policy"); got == "" {
				t.Errorf("Content-Security-Policy header missing")
			} else {
				for _, want := range []string{"default-src 'self'", "frame-ancestors 'none'", "base-uri 'none'"} {
					if !strings.Contains(got, want) {
						t.Errorf("CSP missing %q\nfull policy: %s", want, got)
					}
				}
			}
			if got := h.Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
			}
			// same-origin (not no-referrer) so the wake.js cross-origin POST
			// to the wake-proxy carries an Origin header. The wake-proxy's
			// DNS-rebinding defense requires Origin to be set; no-referrer
			// caused Chromium to null it. See static.go middleware comment.
			if got := h.Get("Referrer-Policy"); got != "same-origin" {
				t.Errorf("Referrer-Policy = %q, want same-origin", got)
			}
		})
	}
}

// TestSecurityHeaders_CSPScriptSrcNoUnsafeInline locks in the result of
// the M3 → modal-extraction → onclick-removal chain: with every inline
// script and on*= attribute removed, the CSP `script-src` directive no
// longer needs `'unsafe-inline'`. Regressing to inline scripts must come
// with a deliberate CSP loosening — making it this test's job to fail
// loudly when that happens.
//
// Doc reference: docs/frontend-architecture.md §6.2, §18.3.
func TestSecurityHeaders_CSPScriptSrcNoUnsafeInline(t *testing.T) {
	srv := New(nil, nil, "", nil, nil, nil, nil, nil, nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/favicon.svg", nil))
	csp := rr.Header().Get("Content-Security-Policy")
	// Extract just the script-src directive — `'unsafe-inline'` is still
	// (intentionally) present in style-src, and we don't want to flag that.
	for _, directive := range strings.Split(csp, ";") {
		directive = strings.TrimSpace(directive)
		if !strings.HasPrefix(directive, "script-src") {
			continue
		}
		if strings.Contains(directive, "'unsafe-inline'") {
			t.Errorf("script-src contains 'unsafe-inline' — see docs/frontend-architecture.md §6.2\n\tdirective: %s", directive)
		}
	}
}

// TestSecurityHeaders_CSPNoExternalHostsAllowed locks in the result of the
// CodeMirror vendoring (§10): script-src and connect-src are now `'self'`
// only — no external CDN allowance. Regressing to a CDN-loaded JS or
// external XHR target requires deliberately weakening the CSP, which
// this test forces into the diff.
//
// Exception: connect-src may be extended with the wake-proxy origin when
// WakeURL is configured (TestSecurityHeaders_CSPExtendsConnectSrcForWakeURL),
// but script-src is always `'self'`-only.
func TestSecurityHeaders_CSPNoExternalHostsAllowed(t *testing.T) {
	srv := New(nil, nil, "", nil, nil, nil, nil, nil, nil)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/favicon.svg", nil))
	csp := rr.Header().Get("Content-Security-Policy")
	for _, directiveName := range []string{"script-src", "connect-src"} {
		for _, directive := range strings.Split(csp, ";") {
			directive = strings.TrimSpace(directive)
			if !strings.HasPrefix(directive, directiveName+" ") {
				continue
			}
			if strings.Contains(directive, "://") {
				t.Errorf("%s contains an external host — see docs/frontend-architecture.md §10\n\tdirective: %s", directiveName, directive)
			}
		}
	}
}

// When WakeURL is configured, connect-src must list its scheme+host so the
// browser permits the cross-origin POST from wake.js. Other directives stay
// untouched — script-src in particular must NOT pick up external hosts
// (the wake JS is served from our own origin).
func TestSecurityHeaders_CSPExtendsConnectSrcForWakeURL(t *testing.T) {
	srv := New(nil, nil, "", nil, nil, nil, nil, nil, nil)
	srv.SetWakeURL("http://localhost:9920/process/start/wsl-wake")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/favicon.svg", nil))
	csp := rr.Header().Get("Content-Security-Policy")
	var connect, script string
	for _, directive := range strings.Split(csp, ";") {
		directive = strings.TrimSpace(directive)
		if strings.HasPrefix(directive, "connect-src ") {
			connect = directive
		}
		if strings.HasPrefix(directive, "script-src ") {
			script = directive
		}
	}
	if !strings.Contains(connect, "http://localhost:9920") {
		t.Errorf("connect-src missing wake origin: %q", connect)
	}
	if !strings.Contains(connect, "'self'") {
		t.Errorf("connect-src dropped 'self': %q", connect)
	}
	if strings.Contains(script, "://") {
		t.Errorf("script-src must not gain an external host: %q", script)
	}
}
