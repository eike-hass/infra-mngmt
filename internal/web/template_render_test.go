package web

// Render-coverage tests for the HTML templates migrated in M4 from Go
// raw-string constants into templates/*.html.tmpl files served via embed.FS
// (see docs/frontend-architecture.md §16). These pin down parse-time and
// render-time behavior so the migration cannot silently drop fields, named
// templates, or FuncMap dependencies.

import (
	"bytes"
	"strings"
	"testing"

	"github.com/eike-hass/infra-mngmt/internal/compose"
	"github.com/eike-hass/infra-mngmt/internal/entity"
)

// htmlTemplateFiles is the canonical list of every embedded template file the
// web package executes. Adding a new file without adding it here is a gap —
// the smoke test below would not protect the new template.
var htmlTemplateFiles = map[string]string{
	"index":              "templates/index.html.tmpl",
	"entity_list":        "templates/entity_list.html.tmpl",
	"preview":            "templates/preview.html.tmpl",
	"services":           "templates/services.html.tmpl",
	"logs":               "templates/logs.html.tmpl",
	"llama":              "templates/llama.html.tmpl",
	"login":              "templates/login.html.tmpl",
	"promote_picker":     "templates/promote_picker.html.tmpl",
	"promote_result":     "templates/promote_result.html.tmpl",
	"container_controls": "templates/container_controls.html.tmpl",
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
		`id="vtab-config"`,
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
		`scope-badge global`, `scope-badge project`, // levelShort outputs
		`scope-badge devcontainer`,
		`>glb<`, `>prj<`, `>ctr<`, // levelShort short forms
		`abc12345`, // build chip commit short SHA
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
		`hx-get="/partials/promote-picker?id=host:/x:command:deploy"`,
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
		`from=host:/a:command:deploy`,
		`to=host:/b:command:deploy`,
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
