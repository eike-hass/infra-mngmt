package web

import (
	"context"
	"time"

	"github.com/eike-hass/infra-mngmt/internal/entity"
	"github.com/eike-hass/infra-mngmt/internal/graph"
)

// MCPStatus holds the resolved runtime state for a single MCP server entity,
// ready to be handed to Go templates.
type MCPStatus struct {
	State    string // CSS class: "running","stopped","error","starting","unresolved","offline","unknown"
	Process  string // matched process-compose process name (empty if no match)
	Instance string // compose instance name (empty if no match)
}

// resolveMCPStatuses matches MCP server entities to process-compose processes
// and returns a map keyed by entity ID. Returns nil when no compose instances
// are configured.
func (s *Server) resolveMCPStatuses(ctx context.Context, entities []entity.Entity) map[string]*MCPStatus {
	if len(s.compose) == 0 {
		return nil
	}

	ctx2, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	var procs []graph.ProcInfo
	anyOnline := false
	for _, c := range s.compose {
		if !c.Ping(ctx2) {
			continue
		}
		anyOnline = true
		ps, err := c.Processes(ctx2)
		if err != nil {
			continue
		}
		for _, p := range ps {
			procs = append(procs, graph.ProcInfo{
				Instance: c.Name(),
				Name:     p.Name,
				CSSState: statusClass(p.Status),
			})
		}
	}

	refs := graph.Resolve(entities, procs, anyOnline)
	result := make(map[string]*MCPStatus, len(refs))
	for _, ref := range refs {
		result[ref.EntityID] = &MCPStatus{
			State:    refCSSState(ref),
			Process:  ref.Process,
			Instance: ref.Instance,
		}
	}
	return result
}

// refCSSState converts a graph.Ref to a CSS class string for the template.
func refCSSState(ref graph.Ref) string {
	switch ref.State {
	case graph.RefOK, graph.RefStopped:
		if ref.ProcState != "" {
			return ref.ProcState
		}
		return "stopped"
	case graph.RefUnresolved:
		return "unresolved"
	case graph.RefOffline:
		return "offline"
	default:
		return "unknown"
	}
}
