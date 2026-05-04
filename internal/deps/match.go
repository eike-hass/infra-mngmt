package deps

import (
	"strings"

	"github.com/eike-hass/infra-mngmt/internal/entity"
)

// EntityKey returns the "kind:name" string used to match a Rule's Entity field.
// Maps entity.Kind values onto the short forms we use in dependencies.yaml.
func EntityKey(e entity.Entity) string {
	return shortKind(e.Kind) + ":" + e.Name
}

func shortKind(k entity.Kind) string {
	switch k {
	case entity.KindMCPServer:
		return "mcp"
	case entity.KindSkill:
		return "skill"
	case entity.KindHook:
		return "hook"
	case entity.KindAgent:
		return "agent"
	case entity.KindMemory:
		return "memory"
	case entity.KindCommand:
		return "command"
	case entity.KindClaudeMD:
		return "claude_md"
	default:
		return string(k)
	}
}

// Match returns the union of needs from every rule whose Entity and Scope
// match the given entity. Order is preserved relative to the rules slice.
func Match(rules []Rule, e entity.Entity) []Need {
	key := EntityKey(e)
	var needs []Need
	seen := make(map[string]struct{})
	for _, r := range rules {
		if !strings.EqualFold(r.Entity, key) {
			continue
		}
		if !scopeMatches(r.Scope, e.Scope) {
			continue
		}
		for _, n := range r.Needs {
			id := n.String()
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			needs = append(needs, n)
		}
	}
	return needs
}

// scopeMatches tells whether a rule's scope pattern includes the given entity
// scope.
//
//	"*"                 — matches any scope
//	"host"              — matches global scope
//	"project:*"         — matches any project scope
//	"project:<path>"    — matches the specific project root (string equality
//	                      after filepath.Clean-equivalent normalization)
//	"container:*"       — matches any container scope (not implemented yet:
//	                      entity.Scope has no container variant; reserved)
//	"container:<id>"    — same caveat
func scopeMatches(pattern string, scope entity.Scope) bool {
	switch pattern {
	case "*":
		return true
	case "host":
		return scope.Global
	case "project:*":
		return scope.Project != ""
	case "container:*":
		return false // not yet representable in entity.Scope
	}
	if strings.HasPrefix(pattern, "project:") {
		want := strings.TrimPrefix(pattern, "project:")
		return scope.Project != "" && pathsEqual(want, scope.Project)
	}
	if strings.HasPrefix(pattern, "container:") {
		return false
	}
	return false
}

// pathsEqual is a minimal path comparison: trim trailing slashes, treat as
// case-sensitive (Linux). We deliberately avoid filepath.Clean here because
// rule authors are expected to write canonical absolute paths.
func pathsEqual(a, b string) bool {
	return strings.TrimRight(a, "/") == strings.TrimRight(b, "/")
}
