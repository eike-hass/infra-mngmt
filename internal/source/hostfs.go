package source

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/eike-hass/infra-mngmt/internal/entity"
)

// HostFSSource reads .claude/ state directly from the host filesystem.
// baseDir is the absolute path to the .claude/ directory itself.
type HostFSSource struct {
	id      string
	baseDir string
	scope   entity.Scope
}

func NewHostFS(baseDir string, scope entity.Scope) *HostFSSource {
	return &HostFSSource{
		id:      "host:" + baseDir,
		baseDir: baseDir,
		scope:   scope,
	}
}

func (s *HostFSSource) ID() string          { return s.id }
func (s *HostFSSource) Scope() entity.Scope { return s.scope }

func (s *HostFSSource) Entities(ctx context.Context) ([]entity.Entity, error) {
	var out []entity.Entity

	for _, item := range []struct {
		subdir  string
		kind    entity.Kind
		pattern string
	}{
		{"commands", entity.KindCommand, "*.md"},
		{"agents", entity.KindAgent, "*.md"},
		{"memory", entity.KindMemory, "*.md"},
	} {
		entities, _ := s.scanGlob(item.subdir, item.kind, item.pattern)
		out = append(out, entities...)
	}

	skills, _ := s.scanSkills()
	out = append(out, skills...)

	if e, err := s.claudeMD(); err == nil {
		out = append(out, e)
	}

	mcps, hooks, _ := s.parseSettings(filepath.Join(s.baseDir, "settings.json"))
	out = append(out, mcps...)
	out = append(out, hooks...)

	return out, nil
}

func (s *HostFSSource) scanGlob(subdir string, kind entity.Kind, pattern string) ([]entity.Entity, error) {
	matches, err := filepath.Glob(filepath.Join(s.baseDir, subdir, pattern))
	if err != nil || len(matches) == 0 {
		return nil, err
	}
	out := make([]entity.Entity, 0, len(matches))
	for _, m := range matches {
		name := strings.TrimSuffix(filepath.Base(m), filepath.Ext(m))
		if name == "MEMORY" {
			continue // skip the MEMORY.md index file
		}
		out = append(out, s.entity(kind, name, m))
	}
	return out, nil
}

func (s *HostFSSource) scanSkills() ([]entity.Entity, error) {
	entries, err := os.ReadDir(filepath.Join(s.baseDir, "skills"))
	if err != nil {
		return nil, err
	}
	var out []entity.Entity
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		skillMD := filepath.Join(s.baseDir, "skills", e.Name(), "SKILL.md")
		if _, err := os.Stat(skillMD); err != nil {
			continue
		}
		out = append(out, s.entity(entity.KindSkill, e.Name(), skillMD))
	}
	return out, nil
}

func (s *HostFSSource) claudeMD() (entity.Entity, error) {
	var path string
	if s.scope.Global {
		path = filepath.Join(s.baseDir, "CLAUDE.md")
	} else {
		// project CLAUDE.md lives at repo root, one level above .claude/
		path = filepath.Join(filepath.Dir(s.baseDir), "CLAUDE.md")
	}
	if _, err := os.Stat(path); err != nil {
		return entity.Entity{}, err
	}
	return s.entity(entity.KindClaudeMD, "CLAUDE.md", path), nil
}

type settingsFile struct {
	MCPServers map[string]json.RawMessage `json:"mcpServers"`
	Hooks      map[string]json.RawMessage `json:"hooks"`
}

// mcpEntry covers the common fields across Claude Code MCP server config types.
type mcpEntry struct {
	Type    string   `json:"type"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
	URL     string   `json:"url"`
}

func parseMCPAttrs(raw json.RawMessage) map[string]string {
	var cfg mcpEntry
	if json.Unmarshal(raw, &cfg) != nil {
		return nil
	}
	t := cfg.Type
	if t == "" {
		switch {
		case cfg.Command != "":
			t = "command"
		case cfg.URL != "":
			t = "sse"
		}
	}
	attrs := map[string]string{}
	if t != "" {
		attrs["type"] = t
	}
	if cfg.URL != "" {
		attrs["url"] = cfg.URL
	}
	if cfg.Command != "" {
		attrs["command"] = cfg.Command
	}
	if len(cfg.Args) > 0 {
		attrs["args"] = strings.Join(cfg.Args, " ")
	}
	if len(attrs) == 0 {
		return nil
	}
	return attrs
}

func (s *HostFSSource) parseSettings(path string) (mcps, hooks []entity.Entity, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var sf settingsFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return nil, nil, err
	}
	for name, raw := range sf.MCPServers {
		e := s.entity(entity.KindMCPServer, name, path)
		e.Attrs = parseMCPAttrs(raw)
		mcps = append(mcps, e)
	}
	for name := range sf.Hooks {
		hooks = append(hooks, s.entity(entity.KindHook, name, path))
	}
	return mcps, hooks, nil
}

func (s *HostFSSource) entity(kind entity.Kind, name, path string) entity.Entity {
	return entity.Entity{
		ID:     fmt.Sprintf("%s:%s:%s", s.id, kind, name),
		Kind:   kind,
		Name:   name,
		Scope:  s.scope,
		Source: s.id,
		Path:   path,
	}
}

func (s *HostFSSource) Read(_ context.Context, kind entity.Kind, name string) ([]byte, error) {
	switch kind {
	case entity.KindMCPServer:
		return s.readSettingsBlock("mcpServers", name)
	case entity.KindHook:
		return s.readSettingsBlock("hooks", name)
	}
	path, err := s.filePath(kind, name)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

func (s *HostFSSource) readSettingsBlock(section, name string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(s.baseDir, "settings.json"))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	var sf map[string]map[string]json.RawMessage
	if err := json.Unmarshal(data, &sf); err != nil {
		return nil, err
	}
	block, ok := sf[section][name]
	if !ok {
		return nil, fmt.Errorf("%w: %s/%s not in settings.json", ErrNotFound, section, name)
	}
	var v any
	if json.Unmarshal(block, &v) == nil {
		if pretty, err := json.MarshalIndent(v, "", "  "); err == nil {
			return pretty, nil
		}
	}
	return block, nil
}

func (s *HostFSSource) Write(_ context.Context, kind entity.Kind, name string, data []byte) error {
	path, err := s.filePath(kind, name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func (s *HostFSSource) filePath(kind entity.Kind, name string) (string, error) {
	switch kind {
	case entity.KindCommand:
		return filepath.Join(s.baseDir, "commands", name+".md"), nil
	case entity.KindAgent:
		return filepath.Join(s.baseDir, "agents", name+".md"), nil
	case entity.KindSkill:
		return filepath.Join(s.baseDir, "skills", name, "SKILL.md"), nil
	case entity.KindMemory:
		return filepath.Join(s.baseDir, "memory", name+".md"), nil
	case entity.KindClaudeMD:
		if s.scope.Global {
			return filepath.Join(s.baseDir, "CLAUDE.md"), nil
		}
		return filepath.Join(filepath.Dir(s.baseDir), "CLAUDE.md"), nil
	default:
		return "", fmt.Errorf("%w: no file path for kind %s", ErrNotFound, kind)
	}
}

// Watch returns nil — polling is used instead of push-based watching for now.
func (s *HostFSSource) Watch(_ context.Context) (<-chan ChangeEvent, error) {
	return nil, nil
}
