// Package graph resolves cross-references between Claude Code entity configs
// (skills, hooks, MCP servers, etc.) and the supplier resources that back
// them — process-compose processes and infra-mngmt bridges.
//
// The resolver consumes three inputs:
//
//   - `procs`  : the union of process-compose processes across all instances.
//   - `bridges`: bridges declared in bridges.yaml together with observed state.
//   - `rules`  : dependency rules from dependencies.yaml that map entities to
//     their declared needs (services and bridges).
//
// For each entity, the resolver returns a list of refs (one per declared
// need) plus a worst-of rollup. When an entity has no matching rule and is an
// MCP server, the resolver falls back to legacy substring matching on the
// process name so existing setups keep working through the migration.
package graph

import (
	"strings"

	"github.com/eike-hass/infra-mngmt/internal/bridge"
	"github.com/eike-hass/infra-mngmt/internal/deps"
	"github.com/eike-hass/infra-mngmt/internal/entity"
)

// ProcInfo is the minimal representation of a process-compose process the
// resolver needs.
type ProcInfo struct {
	Instance string // compose instance name ("wsl", "windows", …)
	Name     string // process name
	CSSState string // "running", "stopped", "error", "starting", "disabled", "unknown"
}

// BridgeInfo couples a bridge declaration with its observed state, ready for
// the resolver to consume. Callers build this from bridge.Load + bridge.Status.
type BridgeInfo struct {
	Bridge bridge.Bridge
	State  bridge.State
}

// ContainerState describes the observed state of a declared Docker container.
// Callers populate these by querying the Docker daemon for each name in
// containers.yaml.
type ContainerState int

const (
	ContainerUnknown ContainerState = iota
	ContainerRunning
	ContainerStopped
	ContainerMissing
)

func (s ContainerState) String() string {
	switch s {
	case ContainerRunning:
		return "running"
	case ContainerStopped:
		return "stopped"
	case ContainerMissing:
		return "missing"
	default:
		return "unknown"
	}
}

// ContainerInfo couples a declared container name with its observed state.
type ContainerInfo struct {
	Name        string
	Description string // optional, surfaces in the UI
	State       ContainerState
	ID          string // Docker container ID when matched (empty if missing)
	Status      string // raw Docker status string ("running", "exited (0)", etc.)
}

// RefState describes how well one declared need is satisfied.
type RefState int

const (
	RefOK         RefState = iota // matched supplier is running / active
	RefStopped                    // supplier exists but is not running / drifted
	RefMissing                    // supplier declared but not present
	RefUnresolved                 // no rule and no fallback match
	RefOffline                    // all process-compose instances unreachable
)

func (r RefState) String() string {
	switch r {
	case RefOK:
		return "ok"
	case RefStopped:
		return "stopped"
	case RefMissing:
		return "missing"
	case RefUnresolved:
		return "unresolved"
	case RefOffline:
		return "offline"
	default:
		return "unknown"
	}
}

// Ref is the resolution result for one Need.
type Ref struct {
	Need      deps.Need
	State     RefState
	Process   string // matched process name when Need.Kind == "service"
	Instance  string // compose instance for the matched process
	Bridge    string // matched bridge name when Need.Kind == "bridge"
	Container string // matched container name when Need.Kind == "container"
	// ProcState is the raw CSS class of the matched process, bridge, or
	// container — passed through to the UI so it can render the same color
	// the services panel uses.
	ProcState string
}

// EntityRefs holds the resolution result for one entity.
type EntityRefs struct {
	EntityID string
	Kind     entity.Kind
	Name     string
	Refs     []Ref    // one per declared need; empty if no rules and no fallback
	State    RefState // worst-of rollup across Refs (for the UI badge)
	// Legacy is true when the resolution came from substring fallback rather
	// than an explicit rule. Useful for the UI to nudge the user toward
	// declaring the binding.
	Legacy bool
}

// IsBroken returns true when the entity has a definite problem (stopped,
// missing, unresolved). RefOffline is *not* broken — we just don't know yet.
func (er EntityRefs) IsBroken() bool {
	switch er.State {
	case RefStopped, RefMissing, RefUnresolved:
		return true
	}
	return false
}

// Resolve walks every entity and produces an EntityRefs for each one. Order
// matches the input.
func Resolve(
	entities []entity.Entity,
	procs []ProcInfo,
	bridges []BridgeInfo,
	containers []ContainerInfo,
	rules []deps.Rule,
	anyOnline bool,
) []EntityRefs {
	out := make([]EntityRefs, 0, len(entities))
	for _, e := range entities {
		out = append(out, resolveOne(e, procs, bridges, containers, rules, anyOnline))
	}
	return out
}

func resolveOne(e entity.Entity, procs []ProcInfo, bridges []BridgeInfo, containers []ContainerInfo, rules []deps.Rule, anyOnline bool) EntityRefs {
	er := EntityRefs{
		EntityID: e.ID,
		Kind:     e.Kind,
		Name:     e.Name,
	}
	needs := deps.Match(rules, e)
	if len(needs) == 0 {
		// Legacy fallback for MCP servers only (the pre-rules behavior).
		if e.Kind == entity.KindMCPServer {
			ref, ok := legacyMCPMatch(e, procs, anyOnline)
			if ok {
				er.Refs = []Ref{ref}
				er.Legacy = true
			} else {
				er.Refs = []Ref{{State: refStateForUnresolved(anyOnline)}}
			}
			er.State = rollup(er.Refs)
			return er
		}
		// No rules and not eligible for legacy fallback: leave State as RefOK
		// (no declared needs = nothing to break).
		er.State = RefOK
		return er
	}

	for _, n := range needs {
		switch n.Kind {
		case "service":
			er.Refs = append(er.Refs, resolveService(n, procs, anyOnline))
		case "bridge":
			er.Refs = append(er.Refs, resolveBridge(n, bridges))
		case "container":
			er.Refs = append(er.Refs, resolveContainer(n, containers))
		}
	}
	er.State = rollup(er.Refs)
	return er
}

func resolveService(n deps.Need, procs []ProcInfo, anyOnline bool) Ref {
	ref := Ref{Need: n}
	for _, p := range procs {
		if !strings.EqualFold(p.Name, n.Name) {
			continue
		}
		if n.Tier != "" && !strings.EqualFold(p.Instance, n.Tier) {
			continue
		}
		ref.Process = p.Name
		ref.Instance = p.Instance
		ref.ProcState = p.CSSState
		ref.State = refStateFromCSS(p.CSSState)
		return ref
	}
	if !anyOnline {
		ref.State = RefOffline
		return ref
	}
	ref.State = RefMissing
	return ref
}

func resolveContainer(n deps.Need, containers []ContainerInfo) Ref {
	ref := Ref{Need: n}
	for _, ci := range containers {
		if !strings.EqualFold(ci.Name, n.Name) {
			continue
		}
		ref.Container = ci.Name
		switch ci.State {
		case ContainerRunning:
			ref.State = RefOK
			ref.ProcState = "running"
		case ContainerStopped:
			ref.State = RefStopped
			ref.ProcState = "stopped"
		case ContainerMissing:
			ref.State = RefMissing
			ref.ProcState = "stopped"
		default:
			ref.State = RefUnresolved
			ref.ProcState = "unknown"
		}
		return ref
	}
	ref.State = RefMissing
	return ref
}

func resolveBridge(n deps.Need, bridges []BridgeInfo) Ref {
	ref := Ref{Need: n}
	for _, b := range bridges {
		if !strings.EqualFold(b.Bridge.Name, n.Name) {
			continue
		}
		ref.Bridge = b.Bridge.Name
		switch b.State {
		case bridge.StateActive:
			ref.State = RefOK
			ref.ProcState = "running"
		case bridge.StateDrifted:
			ref.State = RefStopped
			ref.ProcState = "error"
		case bridge.StateMissing:
			ref.State = RefMissing
			ref.ProcState = "stopped"
		default:
			ref.State = RefUnresolved
			ref.ProcState = "unknown"
		}
		return ref
	}
	ref.State = RefMissing
	return ref
}

// rollup returns the worst-state in refs. Higher RefState ordinal = worse.
func rollup(refs []Ref) RefState {
	worst := RefOK
	for _, r := range refs {
		if r.State > worst {
			worst = r.State
		}
	}
	return worst
}

func refStateFromCSS(css string) RefState {
	switch css {
	case "running", "starting":
		return RefOK
	case "":
		return RefMissing
	default:
		return RefStopped
	}
}

func refStateForUnresolved(anyOnline bool) RefState {
	if !anyOnline {
		return RefOffline
	}
	return RefUnresolved
}

// legacyMCPMatch is the pre-rules substring-based MCP→process resolver.
// Kept as a fallback for MCP entries that don't yet have a rule, so existing
// installs keep getting status badges during the migration.
func legacyMCPMatch(e entity.Entity, procs []ProcInfo, anyOnline bool) (Ref, bool) {
	nameLower := strings.ToLower(e.Name)
	var best Ref
	have := false
	for _, p := range procs {
		pLower := strings.ToLower(p.Name)
		if !strings.Contains(pLower, nameLower) && !strings.Contains(nameLower, pLower) {
			continue
		}
		r := Ref{
			State:     refStateFromCSS(p.CSSState),
			ProcState: p.CSSState,
			Process:   p.Name,
			Instance:  p.Instance,
			Need:      deps.Need{Kind: "service", Name: p.Name},
		}
		if !have || r.State < best.State {
			best = r
			have = true
		}
		if best.State == RefOK {
			break
		}
	}
	if !have {
		_ = anyOnline // caller handles offline outside this fn
		return Ref{}, false
	}
	return best, true
}
