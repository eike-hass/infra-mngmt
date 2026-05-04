package web

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/eike-hass/infra-mngmt/internal/entity"
	"github.com/eike-hass/infra-mngmt/internal/graph"
)

// Per-source budget for status resolution. Each compose-instance probe and the
// Docker snapshot run in parallel goroutines, each bounded by its own derived
// context. A single dead/slow source therefore costs at most one of these
// budgets in wall-clock time, not the sum across sources.
const (
	composeProbeTimeout   = 1500 * time.Millisecond
	containerProbeTimeout = 1500 * time.Millisecond
)

// MCPStatus holds the resolved runtime state for a single entity, ready to be
// handed to Go templates. Despite the historical name, it now covers any
// entity kind the resolver can handle (skills, hooks, etc.) — the field
// shape is unchanged for template compatibility.
type MCPStatus struct {
	State    string // CSS class: "running","stopped","error","starting","unresolved","offline","unknown"
	Process  string // matched supplier name (process or bridge)
	Instance string // compose instance, or "bridge" for bridge backers
	// BackerCount is the number of declared needs for the entity. >1 indicates
	// the rollup represents multiple suppliers; the UI may want to surface
	// this. 0 means no rule and no fallback (legacy MCPs only).
	BackerCount int
}

// resolveMCPStatuses matches entities to their declared needs and returns a
// map keyed by entity ID. Returns nil when no compose instances are
// configured and no bridges/rules/containers are loaded (nothing to resolve
// against).
func (s *Server) resolveMCPStatuses(ctx context.Context, entities []entity.Entity) map[string]*MCPStatus {
	if len(s.compose) == 0 && len(s.bridges) == 0 && len(s.depRules) == 0 && len(s.containerDecls) == 0 {
		return nil
	}

	var (
		mu             sync.Mutex
		procs          []graph.ProcInfo
		anyOnline      bool
		containerInfos []graph.ContainerInfo
		wg             sync.WaitGroup
	)

	for _, c := range s.compose {
		c := c
		wg.Add(1)
		go func() {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, composeProbeTimeout)
			defer cancel()
			if !c.Ping(cctx) {
				return
			}
			mu.Lock()
			anyOnline = true
			mu.Unlock()
			ps, err := c.Processes(cctx)
			if err != nil {
				return
			}
			local := make([]graph.ProcInfo, 0, len(ps))
			for _, p := range ps {
				local = append(local, graph.ProcInfo{
					Instance: c.Name(),
					Name:     p.Name,
					CSSState: statusClass(p.Status),
				})
			}
			mu.Lock()
			procs = append(procs, local...)
			mu.Unlock()
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		cctx, cancel := context.WithTimeout(ctx, containerProbeTimeout)
		defer cancel()
		ci := s.snapshotContainers(cctx)
		mu.Lock()
		containerInfos = ci
		mu.Unlock()
	}()

	wg.Wait()

	// Stable order so downstream resolver behavior is reproducible regardless
	// of goroutine scheduling. graph.Resolve's matchers are exact-name, so
	// order doesn't change correctness — only test determinism.
	sort.Slice(procs, func(i, j int) bool {
		if procs[i].Instance != procs[j].Instance {
			return procs[i].Instance < procs[j].Instance
		}
		return procs[i].Name < procs[j].Name
	})

	resolved := graph.Resolve(entities, procs, s.bridges, containerInfos, s.depRules, anyOnline)
	result := make(map[string]*MCPStatus, len(resolved))
	for _, er := range resolved {
		// Skip entities with no needs and no fallback — leaving them out of
		// the map preserves the old "absent = no badge" behavior for kinds
		// the UI doesn't yet show status for.
		if len(er.Refs) == 0 {
			continue
		}
		// Pick a representative ref for the single-supplier template fields.
		// For multi-need rollups the UI gets the worst-of state; the
		// representative supplier is the worst-state ref.
		rep := er.Refs[0]
		for _, r := range er.Refs[1:] {
			if r.State > rep.State {
				rep = r
			}
		}
		state := entityRefsCSSState(er, rep)
		supplier := rep.Process
		instance := rep.Instance
		if rep.Bridge != "" {
			supplier = rep.Bridge
			instance = "bridge"
		}
		result[er.EntityID] = &MCPStatus{
			State:       state,
			Process:     supplier,
			Instance:    instance,
			BackerCount: len(er.Refs),
		}
	}
	return result
}

// entityRefsCSSState converts an entity's resolved state to the CSS class
// expected by templates. Pass-through of process state when known so the dot
// matches the services panel's color exactly.
func entityRefsCSSState(er graph.EntityRefs, rep graph.Ref) string {
	switch er.State {
	case graph.RefOK, graph.RefStopped:
		if rep.ProcState != "" {
			return rep.ProcState
		}
		if er.State == graph.RefStopped {
			return "stopped"
		}
		return "running"
	case graph.RefMissing:
		return "stopped"
	case graph.RefUnresolved:
		return "unresolved"
	case graph.RefOffline:
		return "offline"
	default:
		return "unknown"
	}
}
