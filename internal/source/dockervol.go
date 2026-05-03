package source

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/eike-hass/infra-mngmt/internal/docker"
	"github.com/eike-hass/infra-mngmt/internal/entity"
)

// DockerVolumeSource reads .claude/ state from a named Docker volume
// by running throwaway sidecar containers. There is no Watch support —
// use periodic polling if live updates are needed.
type DockerVolumeSource struct {
	id         string
	volumeName string
	scope      entity.Scope
	docker     *docker.Client
	// pathIndex maps "kind:name" → rel path inside the volume.
	// Populated on each Entities() call to handle non-standard paths like
	// projects/<encoded>/memory/<name>.md.
	pathIndex map[string]string
}

func entityKey(kind entity.Kind, name string) string { return string(kind) + ":" + name }

func NewDockerVolume(volumeName string, scope entity.Scope, dc *docker.Client) *DockerVolumeSource {
	return &DockerVolumeSource{
		id:         "vol:" + volumeName,
		volumeName: volumeName,
		scope:      scope,
		docker:     dc,
	}
}

func (s *DockerVolumeSource) ID() string          { return s.id }
func (s *DockerVolumeSource) Scope() entity.Scope { return s.scope }

func (s *DockerVolumeSource) Entities(ctx context.Context) ([]entity.Entity, error) {
	raw, err := s.docker.ListVolume(ctx, s.volumeName)
	if err != nil {
		return nil, fmt.Errorf("list volume %s: %w", s.volumeName, err)
	}
	s.pathIndex = map[string]string{} // reset on each call
	out := s.parseFileList(raw)

	// Also parse settings.json for MCP servers and hooks.
	if settings, err := s.settingsEntities(ctx); err == nil {
		out = append(out, settings...)
	}
	return out, nil
}

// parseFileList converts `find /data -type f` output into entities.
// Paths look like: /data/commands/foo.md, /data/skills/bar/SKILL.md, etc.
func (s *DockerVolumeSource) parseFileList(raw []byte) []entity.Entity {
	var out []entity.Entity
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	for scanner.Scan() {
		path := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(path, "/data/") {
			continue
		}
		rel := strings.TrimPrefix(path, "/data/")
		if e, ok := s.pathToEntity(rel); ok {
			// Record the actual volume path for reading.
			if s.pathIndex != nil {
				s.pathIndex[entityKey(e.Kind, e.Name)] = rel
			}
			out = append(out, e)
		}
	}
	return out
}

// pathToEntity maps a relative path inside the volume to an entity.
func (s *DockerVolumeSource) pathToEntity(rel string) (entity.Entity, bool) {
	parts := strings.SplitN(rel, "/", 3)
	if len(parts) == 0 {
		return entity.Entity{}, false
	}

	make := func(kind entity.Kind, name string) (entity.Entity, bool) {
		return entity.Entity{
			ID:     fmt.Sprintf("%s:%s:%s", s.id, kind, name),
			Kind:   kind,
			Name:   name,
			Scope:  s.scope,
			Source: s.id,
			Path:   "vol:" + s.volumeName + ":/" + rel,
		}, true
	}

	switch parts[0] {
	case "commands":
		if len(parts) == 2 && strings.HasSuffix(parts[1], ".md") {
			return make(entity.KindCommand, strings.TrimSuffix(parts[1], ".md"))
		}
	case "agents":
		if len(parts) == 2 && strings.HasSuffix(parts[1], ".md") {
			return make(entity.KindAgent, strings.TrimSuffix(parts[1], ".md"))
		}
	case "skills":
		if len(parts) == 3 && parts[2] == "SKILL.md" {
			return make(entity.KindSkill, parts[1])
		}
	case "memory":
		if len(parts) == 2 && strings.HasSuffix(parts[1], ".md") && parts[1] != "MEMORY.md" {
			return make(entity.KindMemory, strings.TrimSuffix(parts[1], ".md"))
		}
	case "projects":
		// Claude Code stores per-project memories at projects/<encoded>/memory/<name>.md.
		// The entire volume is one project's ~/.claude, so these all belong to this scope.
		if len(parts) == 3 {
			rest := strings.SplitN(parts[2], "/", 2)
			if len(rest) == 2 && rest[0] == "memory" &&
				strings.HasSuffix(rest[1], ".md") && rest[1] != "MEMORY.md" {
				return make(entity.KindMemory, strings.TrimSuffix(rest[1], ".md"))
			}
		}
	case "CLAUDE.md":
		return make(entity.KindClaudeMD, "CLAUDE.md")
	case "settings.json":
		// settings.json is at the volume root — parse it for MCPs and hooks.
		// We can't do this in pathToEntity without reading the file;
		// instead, settings.json entities are handled separately in Entities().
		return entity.Entity{}, false
	}
	return entity.Entity{}, false
}

func (s *DockerVolumeSource) Read(ctx context.Context, kind entity.Kind, name string) ([]byte, error) {
	switch kind {
	case entity.KindMCPServer:
		return s.readVolumeSettingsBlock(ctx, "mcpServers", name)
	case entity.KindHook:
		return s.readVolumeSettingsBlock(ctx, "hooks", name)
	}
	// Use the recorded path from the index when available (e.g. nested project memories).
	if s.pathIndex != nil {
		if rel, ok := s.pathIndex[entityKey(kind, name)]; ok {
			return s.docker.ReadVolume(ctx, s.volumeName, rel)
		}
	}
	path, err := s.volumePath(kind, name)
	if err != nil {
		return nil, err
	}
	return s.docker.ReadVolume(ctx, s.volumeName, path)
}

func (s *DockerVolumeSource) readVolumeSettingsBlock(ctx context.Context, section, name string) ([]byte, error) {
	data, err := s.docker.ReadVolume(ctx, s.volumeName, "settings.json")
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

func (s *DockerVolumeSource) Write(_ context.Context, _ entity.Kind, _ string, _ []byte) error {
	return fmt.Errorf("%w: writing to Docker volumes is not yet implemented", ErrReadOnly)
}

func (s *DockerVolumeSource) volumePath(kind entity.Kind, name string) (string, error) {
	switch kind {
	case entity.KindCommand:
		return filepath.Join("commands", name+".md"), nil
	case entity.KindAgent:
		return filepath.Join("agents", name+".md"), nil
	case entity.KindSkill:
		return filepath.Join("skills", name, "SKILL.md"), nil
	case entity.KindMemory:
		return filepath.Join("memory", name+".md"), nil
	case entity.KindClaudeMD:
		return "CLAUDE.md", nil
	default:
		return "", fmt.Errorf("%w: no volume path for kind %s", ErrNotFound, kind)
	}
}

func (s *DockerVolumeSource) Watch(_ context.Context) (<-chan ChangeEvent, error) {
	return nil, nil // Docker volumes don't support filesystem events
}

// settingsEntities reads settings.json from the volume and returns MCP + hook entities.
func (s *DockerVolumeSource) settingsEntities(ctx context.Context) ([]entity.Entity, error) {
	data, err := s.docker.ReadVolume(ctx, s.volumeName, "settings.json")
	if err != nil {
		return nil, err
	}
	var sf struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
		Hooks      map[string]json.RawMessage `json:"hooks"`
	}
	if err := json.Unmarshal(data, &sf); err != nil {
		return nil, err
	}
	settingsPath := "vol:" + s.volumeName + ":/settings.json"
	var out []entity.Entity
	for name, raw := range sf.MCPServers {
		e := entity.Entity{
			ID:     fmt.Sprintf("%s:%s:%s", s.id, entity.KindMCPServer, name),
			Kind:   entity.KindMCPServer,
			Name:   name,
			Scope:  s.scope,
			Source: s.id,
			Path:   settingsPath,
			Attrs:  parseMCPAttrs(raw),
		}
		out = append(out, e)
	}
	for name := range sf.Hooks {
		out = append(out, entity.Entity{
			ID:     fmt.Sprintf("%s:%s:%s", s.id, entity.KindHook, name),
			Kind:   entity.KindHook,
			Name:   name,
			Scope:  s.scope,
			Source: s.id,
			Path:   settingsPath,
		})
	}
	return out, nil
}
