package source

import (
	"strings"
	"testing"

	"github.com/eike-hass/infra-mngmt/internal/entity"
)

func newTestVolSource() *DockerVolumeSource {
	// nil docker client — the tests below only exercise pure-Go path parsing.
	return &DockerVolumeSource{
		id:         "vol:test",
		volumeName: "test",
		scope:      entity.GlobalScope(),
		pathIndex:  map[string]string{},
	}
}

func TestPathToEntityCommand(t *testing.T) {
	s := newTestVolSource()
	e, ok := s.pathToEntity("commands/run.md")
	if !ok {
		t.Fatal("expected match for commands/run.md")
	}
	if e.Kind != entity.KindCommand || e.Name != "run" {
		t.Errorf("got kind=%q name=%q", e.Kind, e.Name)
	}
	if e.Path != "vol:test:/commands/run.md" {
		t.Errorf("path = %q", e.Path)
	}
}

func TestPathToEntitySkill(t *testing.T) {
	s := newTestVolSource()
	e, ok := s.pathToEntity("skills/foo/SKILL.md")
	if !ok || e.Kind != entity.KindSkill || e.Name != "foo" {
		t.Errorf("skill mapping: ok=%v kind=%q name=%q", ok, e.Kind, e.Name)
	}
	// Subfile of a skill should not match.
	if _, ok := s.pathToEntity("skills/foo/extra.md"); ok {
		t.Error("non-SKILL.md inside a skill should not match")
	}
}

func TestPathToEntityProjectMemory(t *testing.T) {
	s := newTestVolSource()
	e, ok := s.pathToEntity("projects/-encoded-path/memory/note.md")
	if !ok || e.Kind != entity.KindMemory || e.Name != "note" {
		t.Errorf("project memory: ok=%v kind=%q name=%q", ok, e.Kind, e.Name)
	}
	// Index file should be skipped.
	if _, ok := s.pathToEntity("projects/-encoded/memory/MEMORY.md"); ok {
		t.Error("nested MEMORY.md should be skipped")
	}
	// Non-memory subdirs of projects should not match.
	if _, ok := s.pathToEntity("projects/-encoded/other/note.md"); ok {
		t.Error("unrelated project subdirs should not match")
	}
}

func TestPathToEntityClaudeMD(t *testing.T) {
	s := newTestVolSource()
	e, ok := s.pathToEntity("CLAUDE.md")
	if !ok || e.Kind != entity.KindClaudeMD {
		t.Errorf("CLAUDE.md mapping: ok=%v kind=%q", ok, e.Kind)
	}
}

func TestPathToEntitySettingsJSONIgnored(t *testing.T) {
	s := newTestVolSource()
	if _, ok := s.pathToEntity("settings.json"); ok {
		t.Error("settings.json should not map directly via pathToEntity")
	}
}

func TestPathToEntityMemorySkipsIndex(t *testing.T) {
	s := newTestVolSource()
	if _, ok := s.pathToEntity("memory/MEMORY.md"); ok {
		t.Error("memory/MEMORY.md should be skipped")
	}
	e, ok := s.pathToEntity("memory/note.md")
	if !ok || e.Kind != entity.KindMemory || e.Name != "note" {
		t.Errorf("memory mapping: ok=%v kind=%q name=%q", ok, e.Kind, e.Name)
	}
}

func TestPathToEntityUnknown(t *testing.T) {
	s := newTestVolSource()
	for _, p := range []string{"random/file.md", "commands/foo.txt", "agents/", ""} {
		if _, ok := s.pathToEntity(p); ok {
			t.Errorf("expected no match for %q", p)
		}
	}
}

func TestEntityKey(t *testing.T) {
	if got := entityKey(entity.KindCommand, "run"); got != "command:run" {
		t.Errorf("entityKey = %q, want command:run", got)
	}
}

func TestParseFileListIndexesEntities(t *testing.T) {
	s := newTestVolSource()
	raw := []byte(strings.Join([]string{
		"/data/commands/run.md",
		"/data/agents/helper.md",
		"/data/skills/foo/SKILL.md",
		"/data/skills/foo/extra.md", // ignored
		"/data/projects/-pa/memory/note.md",
		"/data/CLAUDE.md",
		"/data/settings.json",   // ignored by pathToEntity
		"/data/random/junk.txt", // ignored
		"/proc/meminfo",         // not under /data
	}, "\n"))
	out := s.parseFileList(raw)
	got := map[string]entity.Kind{}
	for _, e := range out {
		got[e.Name] = e.Kind
	}
	want := map[string]entity.Kind{
		"run":       entity.KindCommand,
		"helper":    entity.KindAgent,
		"foo":       entity.KindSkill,
		"note":      entity.KindMemory,
		"CLAUDE.md": entity.KindClaudeMD,
	}
	if len(got) != len(want) {
		t.Errorf("got %d entities, want %d (%v)", len(got), len(want), got)
	}
	for n, k := range want {
		if got[n] != k {
			t.Errorf("missing %q (kind %q); got %v", n, k, got)
		}
	}
	// pathIndex should have entries for each parsed entity, not for skipped paths.
	if path := s.pathIndex["command:run"]; path != "commands/run.md" {
		t.Errorf("pathIndex[command:run] = %q", path)
	}
	if path := s.pathIndex["memory:note"]; path != "projects/-pa/memory/note.md" {
		t.Errorf("pathIndex should track nested memory path, got %q", path)
	}
	if _, ok := s.pathIndex["random:junk"]; ok {
		t.Error("pathIndex should not contain unmapped paths")
	}
}
