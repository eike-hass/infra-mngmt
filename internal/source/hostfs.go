package source

import (
	"context"
	"encoding/json"
	"errors"
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
	// pathIndex maps "<kind>:<name>" → absolute file path for entities whose
	// path can't be reconstructed from kind+name alone (auto-memory, alternate
	// CLAUDE.md locations). Populated each time Entities() is called.
	pathIndex map[string]string
}

func NewHostFS(baseDir string, scope entity.Scope) *HostFSSource {
	return &HostFSSource{
		id:      "host:" + baseDir,
		baseDir: baseDir,
		scope:   scope,
	}
}

// hostKey is the pathIndex key for a (kind, name) pair. Last-write-wins on
// collisions — adequate for the current set of entity types since
// (kind, name) is unique within a single source.
func hostKey(kind entity.Kind, name string) string { return string(kind) + ":" + name }

func (s *HostFSSource) ID() string          { return s.id }
func (s *HostFSSource) Scope() entity.Scope { return s.scope }
func (s *HostFSSource) Writable() bool      { return true }

func (s *HostFSSource) Entities(ctx context.Context) ([]entity.Entity, error) {
	s.pathIndex = map[string]string{} // reset on each scan
	var out []entity.Entity

	for _, item := range []struct {
		subdir  string
		kind    entity.Kind
		pattern string
	}{
		{"commands", entity.KindCommand, "*.md"},
		{"agents", entity.KindAgent, "*.md"},
		// `memory/*.md` is legacy/manual; Claude Code's auto-memory lives at
		// `<base>/projects/<encoded>/memory/*.md` for the global scope — see
		// scanGlobalProjectMemories below.
		{"memory", entity.KindMemory, "*.md"},
	} {
		entities, _ := s.scanGlob(item.subdir, item.kind, item.pattern)
		out = append(out, entities...)
	}

	skills, _ := s.scanSkills()
	out = append(out, skills...)

	out = append(out, s.claudeMDEntities()...)

	// settings.json applies to all scopes.
	if mcps, hooks, err := s.parseSettings(filepath.Join(s.baseDir, "settings.json")); err == nil {
		out = append(out, mcps...)
		out = append(out, hooks...)
	}

	parent := filepath.Dir(s.baseDir)
	if s.scope.Global {
		// User scope: ~/.claude.json carries top-level mcpServers/hooks (User scope)
		// AND projects."<absPath>".mcpServers (Local scope, project-bound).
		userMCPs, userHooks, localMCPs, _ := s.parseClaudeJSON(filepath.Join(parent, ".claude.json"))
		out = append(out, userMCPs...)
		out = append(out, userHooks...)
		out = append(out, localMCPs...)

		// Auto-memory: ~/.claude/projects/<encoded-path>/memory/<name>.md, scoped
		// to the project the path encodes.
		out = append(out, s.scanGlobalProjectMemories()...)
	} else {
		// Project scope only: settings.local.json overlay (gitignored, per docs)
		if mcps, hooks, err := s.parseSettings(filepath.Join(s.baseDir, "settings.local.json")); err == nil {
			out = append(out, mcps...)
			out = append(out, hooks...)
		}
		// Project scope: <projectRoot>/.mcp.json carries Project-scope MCPs.
		if mcps, err := s.parseMCPJSON(filepath.Join(parent, ".mcp.json")); err == nil {
			out = append(out, mcps...)
		}
	}

	return out, nil
}

// scanGlobalProjectMemories scans ~/.claude/projects/<encoded>/memory/*.md
// (excluding MEMORY.md) and emits each as a memory entity scoped to the
// project that the encoded segment represents. Encoding is `/` → `-`; the
// decode is best-effort (paths whose components contain `-` round-trip
// incorrectly).
func (s *HostFSSource) scanGlobalProjectMemories() []entity.Entity {
	if !s.scope.Global {
		return nil
	}
	projectsDir := filepath.Join(s.baseDir, "projects")
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return nil
	}
	var out []entity.Entity
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		memDir := filepath.Join(projectsDir, e.Name(), "memory")
		matches, err := filepath.Glob(filepath.Join(memDir, "*.md"))
		if err != nil || len(matches) == 0 {
			continue
		}
		decoded := decodeProjectPath(e.Name())
		scope := entity.ProjectScope(decoded)
		for _, m := range matches {
			name := strings.TrimSuffix(filepath.Base(m), ".md")
			if name == "MEMORY" {
				continue
			}
			out = append(out, s.entityWithScope(entity.KindMemory, name, m, scope))
			s.pathIndex[hostKey(entity.KindMemory, name)] = m
		}
	}
	return out
}

// decodeProjectPath converts Claude Code's encoded project segment back to an
// absolute path. Encoding replaces `/` with `-`. This decode is lossy for
// project paths whose components contain `-`.
func decodeProjectPath(encoded string) string {
	return strings.ReplaceAll(encoded, "-", "/")
}

// claudeMDFile is one absolute path that may contain a CLAUDE.md.
type claudeMDFile struct {
	path  string
	scope entity.Scope
}

// claudeMDEntities returns CLAUDE.md entities for this scope. For projects,
// Claude Code accepts both `<projectRoot>/CLAUDE.md` and
// `<projectRoot>/.claude/CLAUDE.md`; the local override is `CLAUDE.local.md`
// next to the main file.
func (s *HostFSSource) claudeMDEntities() []entity.Entity {
	var candidates []claudeMDFile
	if s.scope.Global {
		candidates = append(candidates, claudeMDFile{filepath.Join(s.baseDir, "CLAUDE.md"), s.scope})
	} else {
		repoRoot := filepath.Dir(s.baseDir)
		// Both locations are valid per the memory docs.
		candidates = append(candidates,
			claudeMDFile{filepath.Join(repoRoot, "CLAUDE.md"), s.scope},
			claudeMDFile{filepath.Join(s.baseDir, "CLAUDE.md"), s.scope},
			// Local override (gitignored). Keep distinct so users can tell them apart.
			claudeMDFile{filepath.Join(repoRoot, "CLAUDE.local.md"), s.scope},
		)
	}
	var out []entity.Entity
	for _, c := range candidates {
		if _, err := os.Stat(c.path); err != nil {
			continue
		}
		// Use the file's basename as the entity name so that "CLAUDE.md" and
		// "CLAUDE.local.md" are distinguishable in the list.
		name := filepath.Base(c.path)
		out = append(out, s.entityWithScope(entity.KindClaudeMD, name, c.path, c.scope))
		s.pathIndex[hostKey(entity.KindClaudeMD, name)] = c.path
	}
	return out
}

// parseClaudeJSON reads ~/.claude.json. Returns:
//   - userMCPs:  top-level mcpServers (User scope)
//   - userHooks: top-level hooks
//   - localMCPs: projects."<absPath>".mcpServers, each scoped to ProjectScope("<absPath>")
func (s *HostFSSource) parseClaudeJSON(path string) (userMCPs, userHooks, localMCPs []entity.Entity, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, nil, err
	}
	var raw struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
		Hooks      map[string]json.RawMessage `json:"hooks"`
		Projects   map[string]struct {
			MCPServers map[string]json.RawMessage `json:"mcpServers"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, nil, nil, err
	}
	for name, body := range raw.MCPServers {
		e := s.entity(entity.KindMCPServer, name, path)
		e.Attrs = parseMCPAttrs(body)
		userMCPs = append(userMCPs, e)
	}
	for name := range raw.Hooks {
		userHooks = append(userHooks, s.entity(entity.KindHook, name, path))
	}
	for projectPath, p := range raw.Projects {
		scope := entity.ProjectScope(projectPath)
		for name, body := range p.MCPServers {
			e := s.entityWithScope(entity.KindMCPServer, name, path, scope)
			e.Attrs = parseMCPAttrs(body)
			localMCPs = append(localMCPs, e)
		}
	}
	return userMCPs, userHooks, localMCPs, nil
}

// parseMCPJSON reads a project's .mcp.json file. The format can be either
// flat ({"name": {...}}) — Claude Code's default — or wrapped under an
// "mcpServers" key.
func (s *HostFSSource) parseMCPJSON(path string) ([]entity.Entity, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// Try wrapped form first.
	var wrapped settingsFile
	if json.Unmarshal(data, &wrapped) == nil && len(wrapped.MCPServers) > 0 {
		var out []entity.Entity
		for name, raw := range wrapped.MCPServers {
			e := s.entity(entity.KindMCPServer, name, path)
			e.Attrs = parseMCPAttrs(raw)
			out = append(out, e)
		}
		return out, nil
	}
	// Flat form: top-level keys are MCP names.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	var out []entity.Entity
	for name, msg := range raw {
		attrs := parseMCPAttrs(msg)
		if attrs == nil {
			continue // skip top-level keys that don't look like an MCP server config
		}
		e := s.entity(entity.KindMCPServer, name, path)
		e.Attrs = attrs
		out = append(out, e)
	}
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

// entityWithScope is like entity() but lets the caller override the scope.
// Used when a single source surfaces entities for multiple scopes — e.g. the
// global ~/.claude source emits Local-scope MCPs from ~/.claude.json and
// auto-memory entities from ~/.claude/projects/<encoded>/memory/.
//
// The ID embeds the scope's project so we don't collide when two projects
// have a Local-scope MCP with the same name.
func (s *HostFSSource) entityWithScope(kind entity.Kind, name, path string, scope entity.Scope) entity.Entity {
	return entity.Entity{
		ID:     fmt.Sprintf("%s:%s:%s:%s", s.id, kind, scope.Project, name),
		Kind:   kind,
		Name:   name,
		Scope:  scope,
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
	// Prefer the indexed path (auto-memory, alternate CLAUDE.md locations) before
	// falling back to the conventional layout.
	if path, ok := s.pathIndex[hostKey(kind, name)]; ok {
		return os.ReadFile(path)
	}
	path, err := s.filePath(kind, name)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

// readSettingsBlock looks for `section.name` (e.g. mcpServers/llama-cpp) in any
// of the known config locations for this scope, returning the first match.
// The .mcp.json fallback (project scope, mcpServers section) accepts the flat
// top-level format too.
func (s *HostFSSource) readSettingsBlock(section, name string) ([]byte, error) {
	parent := filepath.Dir(s.baseDir)
	candidates := []string{
		filepath.Join(s.baseDir, "settings.json"),
		filepath.Join(s.baseDir, "settings.local.json"),
	}
	if s.scope.Global {
		candidates = append(candidates, filepath.Join(parent, ".claude.json"))
	}
	for _, p := range candidates {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var sf map[string]map[string]json.RawMessage
		if err := json.Unmarshal(data, &sf); err != nil {
			continue
		}
		if block, ok := sf[section][name]; ok {
			return prettyJSON(block), nil
		}
	}
	// Local-scope MCP fallback: ~/.claude.json's projects."<path>".mcpServers.
	// Returns the first by-name match across all projects (collisions across
	// projects are theoretically possible but rare in practice).
	if s.scope.Global && section == "mcpServers" {
		if data, err := os.ReadFile(filepath.Join(parent, ".claude.json")); err == nil {
			var raw struct {
				Projects map[string]struct {
					MCPServers map[string]json.RawMessage `json:"mcpServers"`
				} `json:"projects"`
			}
			if json.Unmarshal(data, &raw) == nil {
				for _, p := range raw.Projects {
					if block, ok := p.MCPServers[name]; ok {
						return prettyJSON(block), nil
					}
				}
			}
		}
	}
	// .mcp.json fallback (project scope only, mcpServers only)
	if !s.scope.Global && section == "mcpServers" {
		if data, err := os.ReadFile(filepath.Join(parent, ".mcp.json")); err == nil {
			// Wrapped form
			var sf map[string]map[string]json.RawMessage
			if json.Unmarshal(data, &sf) == nil {
				if block, ok := sf[section][name]; ok {
					return prettyJSON(block), nil
				}
			}
			// Flat form: top-level keys are MCP names
			var raw map[string]json.RawMessage
			if json.Unmarshal(data, &raw) == nil {
				if block, ok := raw[name]; ok {
					return prettyJSON(block), nil
				}
			}
		}
	}
	return nil, fmt.Errorf("%w: %s/%s not in any settings file", ErrNotFound, section, name)
}

// prettyJSON returns a re-indented copy of raw JSON; falls back to the input
// unchanged when re-marshaling fails.
func prettyJSON(raw json.RawMessage) []byte {
	var v any
	if json.Unmarshal(raw, &v) == nil {
		if pretty, err := json.MarshalIndent(v, "", "  "); err == nil {
			return pretty
		}
	}
	return raw
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

// Has reports whether (kind, name) exists at this source. For file-backed
// kinds it stats the canonical path; for settings-backed kinds (MCP, hook) it
// scans the same set of settings files Read consults.
func (s *HostFSSource) Has(ctx context.Context, kind entity.Kind, name string) (bool, error) {
	switch kind {
	case entity.KindMCPServer:
		_, err := s.readSettingsBlock("mcpServers", name)
		if err == nil {
			return true, nil
		}
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, err
	case entity.KindHook:
		_, err := s.readSettingsBlock("hooks", name)
		if err == nil {
			return true, nil
		}
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, err
	case entity.KindClaudeMD:
		// Name is the basename; resolve it to the actual file location.
		path := s.claudeMDPathForName(name)
		if path == "" {
			return false, nil
		}
		_, err := os.Stat(path)
		if err == nil {
			return true, nil
		}
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	path, err := s.filePath(kind, name)
	if err != nil {
		return false, nil
	}
	_, err = os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// ReadFiles returns the full file set comprising an entity. For skills, walks
// the skill directory; for settings-backed kinds returns the JSON block as a
// single file with empty RelPath.
func (s *HostFSSource) ReadFiles(ctx context.Context, kind entity.Kind, name string) ([]EntityFile, error) {
	if kind == entity.KindSkill {
		return s.readSkillFiles(name)
	}
	data, err := s.Read(ctx, kind, name)
	if err != nil {
		return nil, err
	}
	return []EntityFile{{Data: data}}, nil
}

// readSkillFiles walks <baseDir>/skills/<name>/ and returns every regular
// file with its path relative to the skill root.
func (s *HostFSSource) readSkillFiles(name string) ([]EntityFile, error) {
	root := filepath.Join(s.baseDir, "skills", name)
	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: skill %s", ErrNotFound, name)
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%w: skills/%s is not a directory", ErrNotFound, name)
	}
	var out []EntityFile
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		// Normalize to forward slashes so the payload is portable across OS.
		out = append(out, EntityFile{RelPath: filepath.ToSlash(rel), Data: data})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// WriteFiles writes a file set into this source. For skills it writes the
// directory tree; for settings-backed kinds it splices into settings.json.
func (s *HostFSSource) WriteFiles(ctx context.Context, kind entity.Kind, name string, files []EntityFile) error {
	switch kind {
	case entity.KindSkill:
		return s.writeSkillFiles(name, files)
	case entity.KindMCPServer:
		if len(files) != 1 {
			return fmt.Errorf("mcp_server write expects exactly one file, got %d", len(files))
		}
		return s.spliceSettings("mcpServers", name, files[0].Data)
	case entity.KindHook:
		if len(files) != 1 {
			return fmt.Errorf("hook write expects exactly one file, got %d", len(files))
		}
		return s.spliceSettings("hooks", name, files[0].Data)
	case entity.KindClaudeMD:
		if len(files) != 1 {
			return fmt.Errorf("claude_md write expects exactly one file, got %d", len(files))
		}
		path := s.claudeMDPathForName(name)
		if path == "" {
			return fmt.Errorf("%w: cannot determine CLAUDE.md path for name %q", ErrNotFound, name)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, files[0].Data, 0o644)
	}
	if len(files) != 1 {
		return fmt.Errorf("kind %s write expects exactly one file, got %d", kind, len(files))
	}
	return s.Write(ctx, kind, name, files[0].Data)
}

// writeSkillFiles writes a skill's file set under <baseDir>/skills/<name>/.
// Existing files at the same paths are overwritten; files outside the file
// set are left in place (caller decides whether to clean up first).
func (s *HostFSSource) writeSkillFiles(name string, files []EntityFile) error {
	root := filepath.Join(s.baseDir, "skills", name)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	for _, f := range files {
		if f.RelPath == "" {
			return fmt.Errorf("skill file requires non-empty RelPath")
		}
		if strings.Contains(f.RelPath, "..") {
			return fmt.Errorf("skill file path %q escapes skill root", f.RelPath)
		}
		dst := filepath.Join(root, filepath.FromSlash(f.RelPath))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, f.Data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// spliceSettings loads <baseDir>/settings.json (or starts a new doc), sets
// section[name] to the given JSON block, and writes the file back. The block
// is parsed first to ensure it's valid JSON, since Read returns pretty-printed
// bytes that need to round-trip.
func (s *HostFSSource) spliceSettings(section, name string, block []byte) error {
	path := filepath.Join(s.baseDir, "settings.json")
	var doc map[string]json.RawMessage
	if existing, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(existing, &doc); err != nil {
			return fmt.Errorf("parse existing settings.json: %w", err)
		}
	}
	if doc == nil {
		doc = map[string]json.RawMessage{}
	}
	var sectionMap map[string]json.RawMessage
	if existing, ok := doc[section]; ok {
		if err := json.Unmarshal(existing, &sectionMap); err != nil {
			return fmt.Errorf("parse settings.json[%s]: %w", section, err)
		}
	}
	if sectionMap == nil {
		sectionMap = map[string]json.RawMessage{}
	}
	// Validate the block parses as JSON before splicing in.
	var probe any
	if err := json.Unmarshal(block, &probe); err != nil {
		return fmt.Errorf("invalid JSON for %s/%s: %w", section, name, err)
	}
	sectionMap[name] = json.RawMessage(block)
	updatedSection, err := json.Marshal(sectionMap)
	if err != nil {
		return err
	}
	doc[section] = updatedSection
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}

// validateEntityName rejects names that could escape the source's directory
// or otherwise resolve to something the caller didn't mean. Empty names are
// the most dangerous: filepath.Join(baseDir, "skills", "") = "<baseDir>/skills/",
// and a follow-up RemoveAll would wipe the whole skills tree.
func validateEntityName(name string) error {
	if name == "" {
		return fmt.Errorf("entity name is empty")
	}
	if name == "." || name == ".." {
		return fmt.Errorf("entity name %q is not a valid filename", name)
	}
	if strings.ContainsAny(name, "/\\\x00") {
		return fmt.Errorf("entity name %q contains illegal path characters", name)
	}
	return nil
}

// Clear removes the entity from disk. For skills it removes the entire skill
// directory (the only multi-file kind, where stale files matter). For file-
// backed kinds it removes the single file. For settings-backed kinds it
// deletes the named key from settings.json (siblings preserved).
//
// Returns ErrNotFound when the entity doesn't exist. promote's mirror=true
// mode tolerates that — it's the natural state after a clear.
//
// The name is validated up-front: empty, "." / "..", and names containing
// path separators are rejected outright. Without that check, an empty name
// on a skill would resolve to "<baseDir>/skills/" and RemoveAll would wipe
// every skill in this source.
func (s *HostFSSource) Clear(_ context.Context, kind entity.Kind, name string) error {
	if err := validateEntityName(name); err != nil {
		return err
	}
	switch kind {
	case entity.KindSkill:
		root := filepath.Join(s.baseDir, "skills", name)
		// Defense-in-depth: confirm the resolved path stays inside <baseDir>/skills/
		// before recursive delete. If the validator above ever lets something
		// through, this catches the escape.
		skillsDir := filepath.Join(s.baseDir, "skills")
		if !strings.HasPrefix(root+string(filepath.Separator), skillsDir+string(filepath.Separator)) {
			return fmt.Errorf("skill path %q escapes skills root", root)
		}
		if _, err := os.Stat(root); err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("%w: skill %s", ErrNotFound, name)
			}
			return err
		}
		return os.RemoveAll(root)
	case entity.KindMCPServer:
		return s.clearSettingsKey("mcpServers", name)
	case entity.KindHook:
		return s.clearSettingsKey("hooks", name)
	case entity.KindClaudeMD:
		path := s.claudeMDPathForName(name)
		if path == "" {
			return fmt.Errorf("%w: CLAUDE.md entity %q", ErrNotFound, name)
		}
		if err := os.Remove(path); err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("%w: %s", ErrNotFound, path)
			}
			return err
		}
		return nil
	}
	path, err := s.filePath(kind, name)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s", ErrNotFound, path)
		}
		return err
	}
	return nil
}

// clearSettingsKey removes section[name] from <baseDir>/settings.json.
// Returns ErrNotFound if the file or key doesn't exist.
func (s *HostFSSource) clearSettingsKey(section, name string) error {
	path := filepath.Join(s.baseDir, "settings.json")
	existing, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s/%s", ErrNotFound, section, name)
		}
		return err
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(existing, &doc); err != nil {
		return fmt.Errorf("parse settings.json: %w", err)
	}
	rawSection, ok := doc[section]
	if !ok {
		return fmt.Errorf("%w: %s/%s", ErrNotFound, section, name)
	}
	var sectionMap map[string]json.RawMessage
	if err := json.Unmarshal(rawSection, &sectionMap); err != nil {
		return fmt.Errorf("parse settings.json[%s]: %w", section, err)
	}
	if _, has := sectionMap[name]; !has {
		return fmt.Errorf("%w: %s/%s", ErrNotFound, section, name)
	}
	delete(sectionMap, name)
	if len(sectionMap) == 0 {
		delete(doc, section)
	} else {
		updated, err := json.Marshal(sectionMap)
		if err != nil {
			return err
		}
		doc[section] = updated
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}

// claudeMDPathForName returns the on-disk path for a CLAUDE.md entity name
// at this scope. Empty result means there's no canonical location for this
// (scope, name) pair.
func (s *HostFSSource) claudeMDPathForName(name string) string {
	if s.scope.Global {
		// Only one canonical location at global scope.
		return filepath.Join(s.baseDir, "CLAUDE.md")
	}
	repoRoot := filepath.Dir(s.baseDir)
	switch name {
	case "CLAUDE.md":
		return filepath.Join(repoRoot, "CLAUDE.md")
	case "CLAUDE.local.md":
		return filepath.Join(repoRoot, "CLAUDE.local.md")
	}
	// Default: project-root CLAUDE.md.
	return filepath.Join(repoRoot, "CLAUDE.md")
}
