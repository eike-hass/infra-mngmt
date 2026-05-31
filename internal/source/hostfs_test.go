package source

import (
	"context"
	"errors"
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
	// Global scope simulates ~/.claude (claude dir is inside root, no parent
	// CLAUDE.md). Project scope adds a CLAUDE.md one level up.

	// Standard subdirectories
	for _, sub := range []string{"commands", "agents", "memory", "skills", "skills/example", "hooks"} {
		if err := os.MkdirAll(filepath.Join(claude, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		"commands/run.md":         "# run",
		"agents/helper.md":        "# helper",
		"memory/note.md":          "remember this",
		"memory/MEMORY.md":        "should be skipped",
		"skills/example/SKILL.md": "skill body",
		"settings.json":           `{"mcpServers":{"local-py":{"command":"python","args":["-m","srv"]},"remote":{"type":"sse","url":"http://x"}},"hooks":{"on-save":{"cmd":"echo"}}}`,
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

// ── ReadFiles / WriteFiles / Has ──────────────────────────────────────────────

func TestHostFSHasFileBacked(t *testing.T) {
	scope := entity.GlobalScope()
	dir := buildClaudeDir(t, scope)
	src := NewHostFS(dir, scope)
	if ok, err := src.Has(context.Background(), entity.KindCommand, "run"); err != nil || !ok {
		t.Errorf("Has(command/run) = %v, %v; want true, nil", ok, err)
	}
	if ok, err := src.Has(context.Background(), entity.KindCommand, "missing"); err != nil || ok {
		t.Errorf("Has(command/missing) = %v, %v; want false, nil", ok, err)
	}
}

func TestHostFSHasSettingsBacked(t *testing.T) {
	scope := entity.GlobalScope()
	dir := buildClaudeDir(t, scope)
	src := NewHostFS(dir, scope)
	if ok, err := src.Has(context.Background(), entity.KindMCPServer, "local-py"); err != nil || !ok {
		t.Errorf("Has(mcp/local-py) = %v, %v; want true", ok, err)
	}
	if ok, err := src.Has(context.Background(), entity.KindHook, "on-save"); err != nil || !ok {
		t.Errorf("Has(hook/on-save) = %v, %v; want true", ok, err)
	}
	if ok, err := src.Has(context.Background(), entity.KindMCPServer, "ghost"); err != nil || ok {
		t.Errorf("Has(mcp/ghost) = %v, %v; want false", ok, err)
	}
}

func TestHostFSReadFilesSkillRecursive(t *testing.T) {
	scope := entity.GlobalScope()
	dir := buildClaudeDir(t, scope)
	// Add a nested file under skills/example to verify the walk picks it up.
	if err := os.MkdirAll(filepath.Join(dir, "skills", "example", "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "skills", "example", "scripts", "go.sh"),
		[]byte("#!/bin/sh"), 0o644); err != nil {
		t.Fatal(err)
	}

	src := NewHostFS(dir, scope)
	files, err := src.ReadFiles(context.Background(), entity.KindSkill, "example")
	if err != nil {
		t.Fatalf("ReadFiles: %v", err)
	}
	got := map[string]string{}
	for _, f := range files {
		got[f.RelPath] = string(f.Data)
	}
	if got["SKILL.md"] != "skill body" {
		t.Errorf("SKILL.md content = %q, want %q", got["SKILL.md"], "skill body")
	}
	if got["scripts/go.sh"] != "#!/bin/sh" {
		t.Errorf("nested file = %q, want %q", got["scripts/go.sh"], "#!/bin/sh")
	}
	if len(got) != 2 {
		t.Errorf("expected 2 files, got %d: %v", len(got), got)
	}
}

func TestHostFSWriteFilesSkillRoundtrip(t *testing.T) {
	dir := t.TempDir()
	claude := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	src := NewHostFS(claude, entity.GlobalScope())

	files := []EntityFile{
		{RelPath: "SKILL.md", Data: []byte("---\nname: x\n---\nbody")},
		{RelPath: "templates/foo.md", Data: []byte("template")},
	}
	if err := src.WriteFiles(context.Background(), entity.KindSkill, "x", files); err != nil {
		t.Fatalf("WriteFiles: %v", err)
	}

	got1, err := os.ReadFile(filepath.Join(claude, "skills", "x", "SKILL.md"))
	if err != nil || string(got1) != "---\nname: x\n---\nbody" {
		t.Errorf("SKILL.md: got %q, err %v", got1, err)
	}
	got2, err := os.ReadFile(filepath.Join(claude, "skills", "x", "templates", "foo.md"))
	if err != nil || string(got2) != "template" {
		t.Errorf("nested file: got %q, err %v", got2, err)
	}
}

func TestHostFSWriteFilesSkillRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	claude := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	src := NewHostFS(claude, entity.GlobalScope())
	files := []EntityFile{{RelPath: "../etc/passwd", Data: []byte("oops")}}
	err := src.WriteFiles(context.Background(), entity.KindSkill, "x", files)
	if err == nil {
		t.Fatal("expected traversal to be rejected")
	}
}

func TestHostFSWriteFilesMCPSpliceCreatesFile(t *testing.T) {
	dir := t.TempDir()
	claude := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	src := NewHostFS(claude, entity.GlobalScope())

	block := []byte(`{"command":"node","args":["server.js"]}`)
	if err := src.WriteFiles(context.Background(), entity.KindMCPServer, "node-srv",
		[]EntityFile{{Data: block}}); err != nil {
		t.Fatalf("WriteFiles: %v", err)
	}
	// Read back through the source to confirm round-trip.
	out, err := src.Read(context.Background(), entity.KindMCPServer, "node-srv")
	if err != nil {
		t.Fatalf("Read after splice: %v", err)
	}
	if !strings.Contains(string(out), `"command": "node"`) {
		t.Errorf("readback missing command:\n%s", out)
	}
}

func TestHostFSWriteFilesMCPSplicePreservesOthers(t *testing.T) {
	scope := entity.GlobalScope()
	dir := buildClaudeDir(t, scope) // pre-populated settings.json with local-py + remote
	src := NewHostFS(dir, scope)

	block := []byte(`{"command":"new","args":[]}`)
	if err := src.WriteFiles(context.Background(), entity.KindMCPServer, "added",
		[]EntityFile{{Data: block}}); err != nil {
		t.Fatalf("WriteFiles: %v", err)
	}

	// Existing entries must still be readable.
	for _, name := range []string{"local-py", "remote", "added"} {
		if _, err := src.Read(context.Background(), entity.KindMCPServer, name); err != nil {
			t.Errorf("Read(%s) after splice: %v", name, err)
		}
	}
	// Existing hook should also still be readable — the splice must not
	// drop sibling top-level keys.
	if _, err := src.Read(context.Background(), entity.KindHook, "on-save"); err != nil {
		t.Errorf("hook clobbered by mcp splice: %v", err)
	}
}

func TestHostFSWriteFilesMCPRejectsInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	claude := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	src := NewHostFS(claude, entity.GlobalScope())
	err := src.WriteFiles(context.Background(), entity.KindMCPServer, "bad",
		[]EntityFile{{Data: []byte(`not json`)}})
	if err == nil {
		t.Fatal("expected error for invalid JSON block")
	}
}

// ── Clear ────────────────────────────────────────────────────────────────────
//
// Clear deletes user data — these tests are biased toward catching
// over-deletion. Each test verifies that:
//  1. The targeted entity is removed
//  2. Sibling entities at the same source are NOT removed
//  3. Anything outside the source's directory tree is NOT removed
// The first half of the suite hammers on path-traversal / empty-name edge
// cases that could otherwise cause RemoveAll to wipe more than intended.

func TestValidateEntityNameRejectsDangerous(t *testing.T) {
	for _, name := range []string{
		"",                 // empty → would resolve to skills/ root
		".",                // would resolve to "skills/."
		"..",               // parent dir
		"../etc",           // traversal
		"../../etc/passwd", // multi-level traversal
		"foo/bar",          // unintended subpath
		"foo\\bar",         // backslash separator (Windows-style)
		"\x00null",         // NUL byte
		"foo\x00.md",       // embedded NUL
	} {
		if err := validateEntityName(name); err == nil {
			t.Errorf("validateEntityName(%q) = nil, want error", name)
		}
	}
}

func TestValidateEntityNameAcceptsValid(t *testing.T) {
	for _, name := range []string{
		"deploy",
		"deploy-prod",
		"deploy.v2",
		"CLAUDE.md",
		"CLAUDE.local.md",
		".bashrc",     // leading dot is allowed (some entity names have it)
		"foo..bar",    // double-dot in middle is allowed (not a traversal token)
		"with space",  // spaces allowed
		"héllo-world", // unicode allowed
	} {
		if err := validateEntityName(name); err != nil {
			t.Errorf("validateEntityName(%q) = %v, want nil", name, err)
		}
	}
}

func TestHostFSClearRejectsEmptyName(t *testing.T) {
	// CRITICAL: empty name on skills resolves to "<base>/skills/" and
	// RemoveAll would wipe every skill in the source. The validator must
	// catch this before the path resolution.
	scope := entity.GlobalScope()
	dir := buildClaudeDir(t, scope)
	src := NewHostFS(dir, scope)

	for _, kind := range []entity.Kind{entity.KindSkill, entity.KindCommand,
		entity.KindAgent, entity.KindMemory, entity.KindMCPServer,
		entity.KindHook, entity.KindClaudeMD,
	} {
		if err := src.Clear(context.Background(), kind, ""); err == nil {
			t.Errorf("Clear(%s, \"\") returned nil; should reject empty name", kind)
		}
	}
	// Skills tree must still be intact after the rejected calls.
	if _, err := os.Stat(filepath.Join(dir, "skills", "example", "SKILL.md")); err != nil {
		t.Fatalf("skills tree damaged by empty-name Clear: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "commands", "run.md")); err != nil {
		t.Fatalf("commands tree damaged by empty-name Clear: %v", err)
	}
}

func TestHostFSClearRejectsTraversal(t *testing.T) {
	scope := entity.GlobalScope()
	dir := buildClaudeDir(t, scope)
	src := NewHostFS(dir, scope)

	// Sentinel files outside .claude/ that must NOT be touched even if a
	// traversal escape slips past the validator.
	parentDir := filepath.Dir(dir)
	sentinel := filepath.Join(parentDir, "do-not-delete")
	if err := os.WriteFile(sentinel, []byte("sentinel"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{
		"..", ".", "../etc", "../../etc/passwd",
		"foo/bar", "foo\\bar", "\x00null",
	} {
		for _, kind := range []entity.Kind{entity.KindSkill, entity.KindCommand} {
			if err := src.Clear(context.Background(), kind, name); err == nil {
				t.Errorf("Clear(%s, %q) returned nil; should reject", kind, name)
			}
		}
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("sentinel removed by Clear traversal: %v", err)
	}
	// Skills tree should still be intact.
	if _, err := os.Stat(filepath.Join(dir, "skills", "example", "SKILL.md")); err != nil {
		t.Fatalf("skills tree damaged by traversal Clear: %v", err)
	}
}

func TestHostFSClearSkillRemovesOnlyTargetDir(t *testing.T) {
	scope := entity.GlobalScope()
	root := t.TempDir()
	claude := filepath.Join(root, ".claude")
	for _, sk := range []string{"keep", "kill"} {
		skDir := filepath.Join(claude, "skills", sk, "scripts")
		if err := os.MkdirAll(skDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(claude, "skills", sk, "SKILL.md"),
			[]byte(sk), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(skDir, "a.sh"),
			[]byte("a"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	src := NewHostFS(claude, scope)

	if err := src.Clear(context.Background(), entity.KindSkill, "kill"); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if _, err := os.Stat(filepath.Join(claude, "skills", "kill")); !os.IsNotExist(err) {
		t.Errorf("target skill 'kill' not removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(claude, "skills", "keep", "SKILL.md")); err != nil {
		t.Errorf("sibling skill 'keep' was removed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(claude, "skills", "keep", "scripts", "a.sh")); err != nil {
		t.Errorf("sibling skill nested file was removed: %v", err)
	}
}

func TestHostFSClearSkillNotFound(t *testing.T) {
	scope := entity.GlobalScope()
	root := t.TempDir()
	claude := filepath.Join(root, ".claude")
	if err := os.MkdirAll(filepath.Join(claude, "skills", "exists"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claude, "skills", "exists", "SKILL.md"),
		[]byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := NewHostFS(claude, scope)

	err := src.Clear(context.Background(), entity.KindSkill, "ghost")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
	// 'exists' skill should still be there.
	if _, err := os.Stat(filepath.Join(claude, "skills", "exists", "SKILL.md")); err != nil {
		t.Errorf("Clear of missing skill must not affect siblings: %v", err)
	}
}

func TestHostFSClearFileBackedKinds(t *testing.T) {
	scope := entity.GlobalScope()
	dir := buildClaudeDir(t, scope)
	src := NewHostFS(dir, scope)

	if err := src.Clear(context.Background(), entity.KindCommand, "run"); err != nil {
		t.Fatalf("Clear command: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "commands", "run.md")); !os.IsNotExist(err) {
		t.Errorf("commands/run.md should be removed")
	}
	// Other file-backed kinds untouched.
	for _, p := range []string{"agents/helper.md", "memory/note.md", "skills/example/SKILL.md"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Errorf("collateral damage: %s removed: %v", p, err)
		}
	}
}

func TestHostFSClearFileNotFound(t *testing.T) {
	scope := entity.GlobalScope()
	dir := buildClaudeDir(t, scope)
	src := NewHostFS(dir, scope)
	err := src.Clear(context.Background(), entity.KindCommand, "nonexistent")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestHostFSClearMCPPreservesSiblings(t *testing.T) {
	// settings.json from buildClaudeDir has local-py + remote MCPs and on-save hook.
	scope := entity.GlobalScope()
	dir := buildClaudeDir(t, scope)
	src := NewHostFS(dir, scope)

	if err := src.Clear(context.Background(), entity.KindMCPServer, "local-py"); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	// Cleared MCP gone.
	if _, err := src.Read(context.Background(), entity.KindMCPServer, "local-py"); err == nil {
		t.Errorf("cleared MCP still readable")
	}
	// Sibling MCP preserved.
	if _, err := src.Read(context.Background(), entity.KindMCPServer, "remote"); err != nil {
		t.Errorf("sibling MCP 'remote' was removed: %v", err)
	}
	// Hook section preserved.
	if _, err := src.Read(context.Background(), entity.KindHook, "on-save"); err != nil {
		t.Errorf("hook 'on-save' was removed by MCP clear: %v", err)
	}
}

func TestHostFSClearMCPMissingSettingsFile(t *testing.T) {
	scope := entity.GlobalScope()
	root := t.TempDir()
	claude := filepath.Join(root, ".claude")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	src := NewHostFS(claude, scope)
	err := src.Clear(context.Background(), entity.KindMCPServer, "x")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for missing settings.json, got %v", err)
	}
	// Settings file must not have been created by the failed Clear.
	if _, err := os.Stat(filepath.Join(claude, "settings.json")); !os.IsNotExist(err) {
		t.Errorf("Clear must not create settings.json on missing-file path")
	}
}

func TestHostFSClearMCPMissingKey(t *testing.T) {
	scope := entity.GlobalScope()
	root := t.TempDir()
	claude := filepath.Join(root, ".claude")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	original := `{"mcpServers":{"x":{"command":"y"}}}`
	if err := os.WriteFile(filepath.Join(claude, "settings.json"),
		[]byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	src := NewHostFS(claude, scope)
	if err := src.Clear(context.Background(), entity.KindMCPServer, "ghost"); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for missing key, got %v", err)
	}
	// Settings file untouched (existing key still readable).
	if _, err := src.Read(context.Background(), entity.KindMCPServer, "x"); err != nil {
		t.Errorf("missing-key Clear must not touch existing entries: %v", err)
	}
}

func TestHostFSClearMCPRemovesEmptySection(t *testing.T) {
	// After clearing the only MCP, the mcpServers key should be removed
	// from settings.json (no empty `"mcpServers": {}` left behind).
	scope := entity.GlobalScope()
	root := t.TempDir()
	claude := filepath.Join(root, ".claude")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claude, "settings.json"),
		[]byte(`{"mcpServers":{"only":{"command":"y"}},"hooks":{"h1":{"cmd":"x"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	src := NewHostFS(claude, scope)
	if err := src.Clear(context.Background(), entity.KindMCPServer, "only"); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(claude, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "mcpServers") {
		t.Errorf("empty mcpServers section should be removed; got: %s", data)
	}
	if !strings.Contains(string(data), "hooks") {
		t.Errorf("unrelated hooks section was removed: %s", data)
	}
}

func TestHostFSClearMCPCorruptSettingsLeavesFileIntact(t *testing.T) {
	// Critical: a parse error must not silently truncate or rewrite the
	// settings file. Surface the error and leave the file alone.
	scope := entity.GlobalScope()
	root := t.TempDir()
	claude := filepath.Join(root, ".claude")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	corrupt := `{not json`
	if err := os.WriteFile(filepath.Join(claude, "settings.json"),
		[]byte(corrupt), 0o644); err != nil {
		t.Fatal(err)
	}
	src := NewHostFS(claude, scope)
	if err := src.Clear(context.Background(), entity.KindMCPServer, "x"); err == nil {
		t.Fatal("expected error on corrupt settings.json, got nil")
	}
	data, err := os.ReadFile(filepath.Join(claude, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != corrupt {
		t.Errorf("corrupt settings.json was modified: %q", data)
	}
}

func TestHostFSClearClaudeMDLocalPreservesMain(t *testing.T) {
	scope := entity.ProjectScope("/proj")
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
	src := NewHostFS(claude, scope)

	if err := src.Clear(context.Background(), entity.KindClaudeMD, "CLAUDE.local.md"); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "CLAUDE.local.md")); !os.IsNotExist(err) {
		t.Errorf("CLAUDE.local.md should be removed")
	}
	data, err := os.ReadFile(filepath.Join(root, "CLAUDE.md"))
	if err != nil || string(data) != "main" {
		t.Errorf("main CLAUDE.md was clobbered: data=%q err=%v", data, err)
	}
}

func TestHostFSReadFilesSingleFileFallback(t *testing.T) {
	scope := entity.GlobalScope()
	dir := buildClaudeDir(t, scope)
	src := NewHostFS(dir, scope)
	// Trigger Entities so pathIndex is primed (Read needs it for some kinds).
	if _, err := src.Entities(context.Background()); err != nil {
		t.Fatal(err)
	}
	files, err := src.ReadFiles(context.Background(), entity.KindCommand, "run")
	if err != nil {
		t.Fatalf("ReadFiles: %v", err)
	}
	if len(files) != 1 || files[0].RelPath != "" || string(files[0].Data) != "# run" {
		t.Errorf("unexpected payload: %+v", files)
	}
}

func TestHostFSWriteFilesExclCreateThenConflict(t *testing.T) {
	scope := entity.GlobalScope()
	dir := t.TempDir()
	src := NewHostFS(dir, scope)
	ctx := context.Background()
	payload := []EntityFile{{Data: []byte("# fresh")}}

	// First create succeeds and writes the data.
	if err := src.WriteFilesExcl(ctx, entity.KindCommand, "promoted", payload); err != nil {
		t.Fatalf("first WriteFilesExcl: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "commands", "promoted.md"))
	if err != nil || string(got) != "# fresh" {
		t.Fatalf("file not written: data=%q err=%v", got, err)
	}

	// Second create (a double-submit / concurrent writer) must not clobber and
	// must report a conflict.
	err = src.WriteFilesExcl(ctx, entity.KindCommand, "promoted", []EntityFile{{Data: []byte("# clobber")}})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("second WriteFilesExcl = %v; want ErrConflict", err)
	}
	got, _ = os.ReadFile(filepath.Join(dir, "commands", "promoted.md"))
	if string(got) != "# fresh" {
		t.Errorf("existing file was overwritten: %q", got)
	}
}

func TestHostFSWriteFilesExclSkillConflict(t *testing.T) {
	scope := entity.GlobalScope()
	dir := t.TempDir()
	src := NewHostFS(dir, scope)
	ctx := context.Background()
	files := []EntityFile{{RelPath: "SKILL.md", Data: []byte("# skill")}}

	if err := src.WriteFilesExcl(ctx, entity.KindSkill, "demo", files); err != nil {
		t.Fatalf("first WriteFilesExcl: %v", err)
	}
	if err := src.WriteFilesExcl(ctx, entity.KindSkill, "demo", files); !errors.Is(err, ErrConflict) {
		t.Fatalf("second WriteFilesExcl = %v; want ErrConflict", err)
	}
}

func TestHostFSWriteFilesExclRejectsEmptyName(t *testing.T) {
	src := NewHostFS(t.TempDir(), entity.GlobalScope())
	if err := src.WriteFilesExcl(context.Background(), entity.KindCommand, "", []EntityFile{{Data: []byte("x")}}); err == nil {
		t.Fatal("WriteFilesExcl(empty name) = nil; want error")
	}
}

func TestHostFSWriteRejectsEmptyName(t *testing.T) {
	src := NewHostFS(t.TempDir(), entity.GlobalScope())
	if err := src.Write(context.Background(), entity.KindCommand, "", []byte("x")); err == nil {
		t.Fatal("Write(empty name) = nil; want error")
	}
}
