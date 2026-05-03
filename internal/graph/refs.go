// Package graph resolves cross-references between Claude Code entity configs
// (e.g. MCP server URLs / commands) and the process-compose processes that
// back them.
package graph

import (
	"strings"

	"github.com/eike-hass/infra-mngmt/internal/entity"
)

// ProcInfo is the minimal representation of a running (or stopped) process
// that graph needs for resolution. Callers fill this from compose.ProcessState.
type ProcInfo struct {
	Instance string // compose instance name
	Name     string // process name
	CSSState string // normalised CSS class: "running","stopped","error","starting","disabled","unknown"
}

// RefState describes how well an MCP server's service dependency is satisfied.
type RefState int

const (
	RefOK         RefState = iota // matched process is running (or starting)
	RefStopped                    // matched process exists but is not running
	RefUnresolved                 // no name-match found while at least one compose instance was online
	RefOffline                    // all compose instances were unreachable
)

func (r RefState) String() string {
	switch r {
	case RefOK:
		return "ok"
	case RefStopped:
		return "stopped"
	case RefUnresolved:
		return "unresolved"
	case RefOffline:
		return "offline"
	default:
		return "unknown"
	}
}

// Ref holds the cross-reference resolution result for one MCP server entity.
type Ref struct {
	EntityID  string
	MCPName   string
	State     RefState
	ProcState string // CSS class of the matched process ("running", "error", …); empty if no match
	Process   string // matched process name, if any
	Instance  string // compose instance, if any
}

// IsBroken returns true when the reference cannot currently be satisfied.
func (r Ref) IsBroken() bool {
	return r.State == RefStopped || r.State == RefUnresolved
}

// Resolve matches each MCP server entity to a process-compose process using
// case-insensitive name substring matching. procs is the union of all processes
// across all compose instances; anyOnline indicates whether at least one
// instance responded.
func Resolve(entities []entity.Entity, procs []ProcInfo, anyOnline bool) []Ref {
	var refs []Ref
	for _, e := range entities {
		if e.Kind != entity.KindMCPServer {
			continue
		}
		refs = append(refs, matchMCP(e, procs, anyOnline))
	}
	return refs
}

func matchMCP(e entity.Entity, procs []ProcInfo, anyOnline bool) Ref {
	nameLower := strings.ToLower(e.Name)

	var best *Ref
	for _, p := range procs {
		pLower := strings.ToLower(p.Name)
		if !strings.Contains(pLower, nameLower) && !strings.Contains(nameLower, pLower) {
			continue
		}
		r := Ref{
			EntityID:  e.ID,
			MCPName:   e.Name,
			State:     refStateFromCSS(p.CSSState),
			ProcState: p.CSSState,
			Process:   p.Name,
			Instance:  p.Instance,
		}
		if best == nil || r.State < best.State { // lower enum = better
			best = &r
		}
		if best.State == RefOK {
			break
		}
	}
	if best != nil {
		return *best
	}
	if !anyOnline {
		return Ref{EntityID: e.ID, MCPName: e.Name, State: RefOffline}
	}
	return Ref{EntityID: e.ID, MCPName: e.Name, State: RefUnresolved}
}

func refStateFromCSS(css string) RefState {
	switch css {
	case "running", "starting":
		return RefOK
	default:
		return RefStopped
	}
}
