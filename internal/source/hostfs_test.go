package source

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/eike-hass/infra-mngmt/internal/entity"
)

// buildClaudeDir creates a populated .claude directory for testing.
// Returns the absolute path to the .claude/ dir.
func buildClaudeDir(t *testing.T, scope entity.Scope) string {
	t.Helper()
	root := t.TempDir()
	claude := filepath.Join(root, ".claude")
	if scope.Global {
		// Global: simulate ~/.claude → claude dir is inside root with no parent CLAUDE.md
	}

	// Standard subdirectories
	for _, sub := range []string{"commands", "agents", "memory", "skills", "skills/example", "hooks"} {
		if err := os.MkdirAll(filepath.Join(claude, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		"commands/run.md":          "# run",
		"agents/helper.md":         "# helper",
		"memory/note.md":           "remember this",
		"memory/MEMORY.md":         "should be skipped",
		"skills/example/SKILL.md":  "skill body",
		"settings.json":            `{"mcpServers":{"local-py":{"command":"python","args":["-m","srv"]},"remote":{"type":"sse","url":"http://x"}},"hooks":{"on-save":{"cmd":"echo"}}}`,
	}
	if scope.Global {
		files["CLAUDE.md"] = "global memory"
	} else {
		files["../CLAUDE.md"] = "project memory"
	}
	for rel, body := range files {
		p := filepath.Join(claude, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return claude
}

func TestHostFSEntitiesGlobal(t *testing.T) {
	scope := entity.GlobalScope()
	dir := buildClaudeDir(t, scope)
	src := NewHostFS(dir, scope)

	ents, err := src.Entities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]entity.Kind{}
	for _, e := range ents {
		got[e.Name] = e.Kind
	}

	want := map[string]entity.Kind{
		"run":       entity.KindCommand,
		"helper":    entity.KindAgent,
		"note":      entity.KindMemory,
		"example":   entity.KindSkill,
		"CLAUDE.md": entity.KindClaudeMD,
		"local-py":  entity.KindMCPServer,
		"remote":    entity.KindMCPServer,
		"on-save":   entity.KindHook,
	}
	for name, kind := range want {
		if got[name] != kind {
			t.Errorf("missing or wrong kind for %q: got %q, want %q", name, got[name], kind)
		}
	}
	if got["MEMORY"] != "" {
		t.Error("MEMORY.md should be skipped")
	}
}

func TestHostFSEntitiesProject(t *testing.T) {
	scope := entity.ProjectScope("/fake/project") // scope path doesn't have to match dir
	dir := buildClaudeDir(t, scope)
	src := NewHostFS(dir, scope)
	ents, err := src.Entities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Project CLAUDE.md should be picked up from one level above .claude/
	for _, e := range ents {
		if e.Kind == entity.KindClaudeMD {
			expected := filepath.Join(filepath.Dir(dir), "CLAUDE.md")
			if e.Path != expected {
				t.Errorf("project CLAUDE.md path = %q, want %q", e.Path, expected)
			}
			return
		}
	}
	t.Error("project CLAUDE.md entity not found")
}

func TestHostFSReadWriteRoundtrip(t *testing.T) {
	scope := entity.GlobalScope()
	dir := buildClaudeDir(t, scope)
	src := NewHostFS(dir, scope)

	if err := src.Write(context.Background(), entity.KindCommand, "newcmd", []byte("body")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := src.Read(context.Background(), entity.KindCommand, "newcmd")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(got) != "body" {
		t.Errorf("Read returned %q, want %q", got, "body")
	}
}

func TestHostFSReadMCPServerFromSettings(t *testing.T) {
	scope := entity.GlobalScope()
	dir := buildClaudeDir(t, scope)
	src := NewHostFS(dir, scope)

	body, err := src.Read(context.Background(), entity.KindMCPServer, "local-py")
	if err != nil {
		t.Fatalf("Read MCP block: %v", err)
	}
	// Should be pretty-printed JSON of the inner block.
	s := string(body)
	if !strings.Contains(s, `"command": "python"`) {
		t.Errorf("MCP block read missing command field:\n%s", s)
	}
}

func TestHostFSReadMissing(t *testing.T) {
	scope := entity.GlobalScope()
	dir := buildClaudeDir(t, scope)
	src := NewHostFS(dir, scope)

	_, err := src.Read(context.Background(), entity.KindMCPServer, "nonexistent")
	if err == nil {
		t.Fatal("expected error for missing MCP entry")
	}
}

func TestHostFSEntitiesEmpty(t *testing.T) {
	dir := t.TempDir()
	claude := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	src := NewHostFS(claude, entity.GlobalScope())
	ents, err := src.Entities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 0 {
		names := []string{}
		for _, e := range ents {
			names = append(names, e.Name)
		}
		sort.Strings(names)
		t.Errorf("empty .claude/ should yield 0 entities, got %d: %v", len(ents), names)
	}
}

func TestParseMCPAttrsCommandType(t *testing.T) {
	raw := []byte(`{"command":"python","args":["-m","srv"]}`)
	attrs := parseMCPAttrs(raw)
	if attrs["type"] != "command" {
		t.Errorf("type = %q, want command", attrs["type"])
	}
	if attrs["command"] != "python" {
		t.Errorf("command = %q", attrs["command"])
	}
	if attrs["args"] != "-m srv" {
		t.Errorf("args = %q, want %q", attrs["args"], "-m srv")
	}
}

func TestParseMCPAttrsURLType(t *testing.T) {
	raw := []byte(`{"url":"http://example/sse"}`)
	attrs := parseMCPAttrs(raw)
	if attrs["type"] != "sse" {
		t.Errorf("type = %q, want sse (inferred)", attrs["type"])
	}
	if attrs["url"] != "http://example/sse" {
		t.Errorf("url = %q", attrs["url"])
	}
}

func TestParseMCPAttrsExplicitType(t *testing.T) {
	raw := []byte(`{"type":"http","url":"http://x"}`)
	attrs := parseMCPAttrs(raw)
	if attrs["type"] != "http" {
		t.Errorf("explicit type should win, got %q", attrs["type"])
	}
}

func TestParseMCPAttrsInvalidJSON(t *testing.T) {
	if attrs := parseMCPAttrs([]byte("not json")); attrs != nil {
		t.Errorf("expected nil for invalid JSON, got %v", attrs)
	}
}

func TestHostFSWatchReturnsNil(t *testing.T) {
	src := NewHostFS(t.TempDir(), entity.GlobalScope())
	ch, err := src.Watch(context.Background())
	if err != nil {
		t.Fatalf("Watch err: %v", err)
	}
	if ch != nil {
		t.Error("HostFS Watch should return nil channel (polling-based)")
	}
}

// ─── extra MCP config locations ─────────────────────────────────────────────

func TestHostFSGlobalReadsClaudeJSON(t *testing.T) {
	// ~/.claude.json (sibling to ~/.claude/) carries Claude Code's main MCP
	// config — must be picked up alongside .claude/settings.json.
	root := t.TempDir()
	claude := filepath.Join(root, ".claude")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".claude.json"),
		[]byte(`{"mcpServers":{"from-claude-json":{"command":"x"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	src := NewHostFS(claude, entity.GlobalScope())
	ents, err := src.Entities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range ents {
		if e.Kind == entity.KindMCPServer && e.Name == "from-claude-json" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("MCP from ~/.claude.json not detected; got %d entities", len(ents))
	}

	// Read must work too.
	body, err := src.Read(context.Background(), entity.KindMCPServer, "from-claude-json")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !strings.Contains(string(body), `"command": "x"`) {
		t.Errorf("read body = %q", body)
	}
}

func TestHostFSProjectReadsMCPJSONFlat(t *testing.T) {
	// .mcp.json typically uses the flat top-level format.
	root := t.TempDir()
	claude := filepath.Join(root, ".claude")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"),
		[]byte(`{"github":{"command":"gh-mcp"},"docker":{"type":"sse","url":"http://localhost:9000"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	src := NewHostFS(claude, entity.ProjectScope(root))
	ents, err := src.Entities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]map[string]string{}
	for _, e := range ents {
		if e.Kind == entity.KindMCPServer {
			got[e.Name] = e.Attrs
		}
	}
	if got["github"]["command"] != "gh-mcp" {
		t.Errorf("github attrs = %v", got["github"])
	}
	if got["docker"]["type"] != "sse" || got["docker"]["url"] != "http://localhost:9000" {
		t.Errorf("docker attrs = %v", got["docker"])
	}

	body, err := src.Read(context.Background(), entity.KindMCPServer, "github")
	if err != nil {
		t.Fatalf("Read flat: %v", err)
	}
	if !strings.Contains(string(body), `"command": "gh-mcp"`) {
		t.Errorf("flat read body = %q", body)
	}
}

func TestHostFSProjectReadsMCPJSONWrapped(t *testing.T) {
	root := t.TempDir()
	claude := filepath.Join(root, ".claude")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"),
		[]byte(`{"mcpServers":{"wrapped":{"command":"foo"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	src := NewHostFS(claude, entity.ProjectScope(root))
	ents, _ := src.Entities(context.Background())
	for _, e := range ents {
		if e.Name == "wrapped" && e.Kind == entity.KindMCPServer {
			return
		}
	}
	t.Errorf("wrapped MCP not detected; got %d entities", len(ents))
}

func TestHostFSReadsSettingsLocal(t *testing.T) {
	root := t.TempDir()
	claude := filepath.Join(root, ".claude")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claude, "settings.local.json"),
		[]byte(`{"mcpServers":{"local-only":{"command":"y"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// settings.local.json only exists at the project scope per the docs
	// (it's gitignored and added automatically). The global ~/.claude/ has
	// no settings.local.json equivalent.
	src := NewHostFS(claude, entity.ProjectScope(root))
	ents, _ := src.Entities(context.Background())
	for _, e := range ents {
		if e.Name == "local-only" {
			return
		}
	}
	t.Errorf("settings.local.json MCP not detected at project scope")
}

func TestHostFSGlobalSkipsSettingsLocal(t *testing.T) {
	// Per docs there is no ~/.claude/settings.local.json; if such a file
	// somehow exists it should NOT be scanned for the global scope.
	root := t.TempDir()
	claude := filepath.Join(root, ".claude")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claude, "settings.local.json"),
		[]byte(`{"mcpServers":{"should-not-show":{"command":"y"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	src := NewHostFS(claude, entity.GlobalScope())
	ents, _ := src.Entities(context.Background())
	for _, e := range ents {
		if e.Name == "should-not-show" {
			t.Error("settings.local.json should not be scanned at the global scope")
		}
	}
}

func TestHostFSLocalScopeMCPsFromClaudeJSON(t *testing.T) {
	// ~/.claude.json carries Local-scope MCPs under projects."<absPath>".mcpServers.
	// They're stored under the user's home but logically belong to that project.
	root := t.TempDir()
	claude := filepath.Join(root, ".claude")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{
  "mcpServers": { "user-wide": {"command":"u"} },
  "projects": {
    "/home/u/projA": { "mcpServers": { "projA-only": {"command":"a"} } },
    "/home/u/projB": { "mcpServers": { "projB-only": {"command":"b"} } }
  }
}`
	if err := os.WriteFile(filepath.Join(root, ".claude.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	src := NewHostFS(claude, entity.GlobalScope())
	ents, err := src.Entities(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	type key struct {
		name    string
		scopeID string
	}
	got := map[key]bool{}
	for _, e := range ents {
		if e.Kind == entity.KindMCPServer {
			got[key{e.Name, e.Scope.String()}] = true
		}
	}
	want := []key{
		{"user-wide", "global"},
		{"projA-only", "project:/home/u/projA"},
		{"projB-only", "project:/home/u/projB"},
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing MCP entity name=%q scope=%q (got %v)", w.name, w.scopeID, got)
		}
	}
}

func TestHostFSReadsLocalScopeMCPFromClaudeJSON(t *testing.T) {
	// Read should be able to fetch a Local-scope MCP back out of the projects map.
	root := t.TempDir()
	claude := filepath.Join(root, ".claude")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{
  "projects": {
    "/home/u/projA": { "mcpServers": { "private": {"command":"x","args":["--db","prod"]} } }
  }
}`
	if err := os.WriteFile(filepath.Join(root, ".claude.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	src := NewHostFS(claude, entity.GlobalScope())
	if _, err := src.Entities(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := src.Read(context.Background(), entity.KindMCPServer, "private")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !strings.Contains(string(got), `"command": "x"`) {
		t.Errorf("body = %q, want command:x", got)
	}
}

func TestHostFSReadsAutoMemory(t *testing.T) {
	// Read should fetch auto-memory content via the path index.
	root := t.TempDir()
	claude := filepath.Join(root, ".claude")
	memDir := filepath.Join(claude, "projects", "-home-u-projX", "memory")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(memDir, "session.md"), []byte("auto-mem body"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := NewHostFS(claude, entity.GlobalScope())
	if _, err := src.Entities(context.Background()); err != nil {
		t.Fatal(err)
	}
	body, err := src.Read(context.Background(), entity.KindMemory, "session")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(body) != "auto-mem body" {
		t.Errorf("body = %q", body)
	}
}

func TestHostFSAutoMemoryFromProjectsDir(t *testing.T) {
	// ~/.claude/projects/<encoded>/memory/<name>.md is Claude Code's auto-memory
	// location. Each entry is scoped to the project encoded in the dir name
	// (encoding: `/` → `-`).
	root := t.TempDir()
	claude := filepath.Join(root, ".claude")
	memDir := filepath.Join(claude, "projects", "-home-u-projX", "memory")
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(memDir, "session.md"), []byte("ctx"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(memDir, "MEMORY.md"), []byte("idx"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := NewHostFS(claude, entity.GlobalScope())
	ents, _ := src.Entities(context.Background())
	var found *entity.Entity
	for i := range ents {
		if ents[i].Kind == entity.KindMemory && ents[i].Name == "session" {
			found = &ents[i]
			break
		}
	}
	if found == nil {
		t.Fatal("auto-memory not detected from ~/.claude/projects/<encoded>/memory/")
	}
	if found.Scope.Global || found.Scope.Project != "/home/u/projX" {
		t.Errorf("auto-memory scope = %q, want project:/home/u/projX", found.Scope.String())
	}
	// MEMORY.md index should be skipped.
	for _, e := range ents {
		if e.Name == "MEMORY" {
			t.Error("MEMORY.md should not be emitted as an entity")
		}
	}
}

func TestHostFSProjectClaudeMDInsideDotClaude(t *testing.T) {
	// Per docs, project CLAUDE.md may live at <root>/CLAUDE.md OR
	// <root>/.claude/CLAUDE.md — we should detect both.
	root := t.TempDir()
	claude := filepath.Join(root, ".claude")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claude, "CLAUDE.md"), []byte("inside .claude"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := NewHostFS(claude, entity.ProjectScope(root))
	ents, _ := src.Entities(context.Background())
	for _, e := range ents {
		if e.Kind == entity.KindClaudeMD {
			return
		}
	}
	t.Error("CLAUDE.md inside .claude/ not detected")
}

func TestHostFSProjectClaudeLocalMD(t *testing.T) {
	// CLAUDE.local.md is the local override sibling of CLAUDE.md.
	root := t.TempDir()
	claude := filepath.Join(root, ".claude")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("main"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.local.md"), []byte("local"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := NewHostFS(claude, entity.ProjectScope(root))
	ents, _ := src.Entities(context.Background())
	names := map[string]bool{}
	for _, e := range ents {
		if e.Kind == entity.KindClaudeMD {
			names[e.Name] = true
		}
	}
	if !names["CLAUDE.md"] {
		t.Error("CLAUDE.md not detected")
	}
	if !names["CLAUDE.local.md"] {
		t.Error("CLAUDE.local.md not detected")
	}
}

func TestParseMCPJSONFlatSkipsNonMCPKeys(t *testing.T) {
	// If a flat .mcp.json contains non-server keys, they should be filtered
	// out (parseMCPAttrs returns nil for objects without command/url/type).
	root := t.TempDir()
	claude := filepath.Join(root, ".claude")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"),
		[]byte(`{"real":{"command":"x"},"random":{"foo":"bar"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	src := NewHostFS(claude, entity.ProjectScope(root))
	ents, _ := src.Entities(context.Background())
	names := []string{}
	for _, e := range ents {
		if e.Kind == entity.KindMCPServer {
			names = append(names, e.Name)
		}
	}
	if len(names) != 1 || names[0] != "real" {
		t.Errorf("expected only 'real' to be picked up, got %v", names)
	}
}
