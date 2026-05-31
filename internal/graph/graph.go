package graph

import (
	"sort"
	"strings"

	"github.com/eike-hass/infra-mngmt/internal/bridge"
	"github.com/eike-hass/infra-mngmt/internal/deps"
	"github.com/eike-hass/infra-mngmt/internal/entity"
)

// This file builds a typed, bidirectional dependency graph over entities and
// the suppliers that back them (services, bridges, containers) plus the tiers
// (process-compose instances) those suppliers run on. It reuses the same
// inputs as Resolve but adds two things Resolve can't express: a reverse index
// (who depends on X — for blast radius) and a causal chain (service runs-on
// instance — for root-cause diagnosis across tiers).
//
// See docs/proposals/dependency-graph.md.

// NodeKind enumerates the node types in the graph.
type NodeKind string

const (
	NodeEntity    NodeKind = "entity"
	NodeService   NodeKind = "service"
	NodeBridge    NodeKind = "bridge"
	NodeContainer NodeKind = "container"
	NodeInstance  NodeKind = "instance" // a process-compose tier (wsl, windows)
)

// Health is a node's observed condition. Higher ordinal = "worse" for the
// worst-of rollup; isBad() draws the line between "definitely a problem" and
// "fine / merely transient / merely unknown".
type Health int

const (
	HealthOK       Health = iota // running / active
	HealthDegraded               // starting — transient, not a fault
	HealthOffline                // the tier it runs on is unreachable — unknown, not proven broken
	HealthDown                   // present but stopped / errored / drifted
	HealthMissing                // a declared supplier that isn't present at all
	HealthUnknown                // couldn't classify
)

func (h Health) String() string {
	switch h {
	case HealthOK:
		return "ok"
	case HealthDegraded:
		return "degraded"
	case HealthOffline:
		return "offline"
	case HealthDown:
		return "down"
	case HealthMissing:
		return "missing"
	default:
		return "unknown"
	}
}

// isBad reports whether a health state is a definite or likely fault worth
// tracing through. Degraded (starting) and OK are not faults; Offline is
// included because an unreachable tier is the most common root cause.
func (h Health) isBad() bool {
	switch h {
	case HealthDown, HealthMissing, HealthOffline, HealthUnknown:
		return true
	}
	return false
}

// Node is one vertex in the dependency graph.
type Node struct {
	ID     string // unique, "<kind>:<key>" — e.g. "service:windows/llama", "instance:windows"
	Kind   NodeKind
	Label  string // human-facing short label
	Health Health
	Detail string // optional one-liner, e.g. "stopped", "tier unreachable"
}

// Graph is a directed dependency graph: out edges point consumer → supplier
// (depends-on); in edges are the reverse (dependents).
type Graph struct {
	nodes map[string]*Node
	out   map[string][]string // dependency edges (consumer → supplier)
	in    map[string][]string // reverse edges (supplier → consumer)
}

// Node returns the node with the given ID, or nil.
func (g *Graph) Node(id string) *Node { return g.nodes[id] }

func (g *Graph) ensure(n *Node) *Node {
	if existing, ok := g.nodes[n.ID]; ok {
		return existing
	}
	g.nodes[n.ID] = n
	return n
}

// edge records consumer → supplier (and the reverse), de-duplicated.
func (g *Graph) edge(consumer, supplier string) {
	if consumer == supplier {
		return
	}
	for _, s := range g.out[consumer] {
		if s == supplier {
			return
		}
	}
	g.out[consumer] = append(g.out[consumer], supplier)
	g.in[supplier] = append(g.in[supplier], consumer)
}

// instanceID / serviceID / bridgeID / containerID / entityID build stable node IDs.
func instanceID(name string) string { return "instance:" + strings.ToLower(name) }
func serviceID(inst, name string) string {
	return "service:" + strings.ToLower(inst) + "/" + strings.ToLower(name)
}
func bridgeID(name string) string    { return "bridge:" + strings.ToLower(name) }
func containerID(name string) string { return "container:" + strings.ToLower(name) }
func entityNodeID(id string) string  { return "entity:" + id }

// BuildGraph assembles the dependency graph from the same observed inputs the
// status resolver gathers. instanceUp maps a process-compose instance name to
// whether it pinged reachable this cycle; an instance absent from the map is
// treated as reachable only if it appears in procs (we have data for it).
func BuildGraph(
	entities []entity.Entity,
	procs []ProcInfo,
	bridges []BridgeInfo,
	containers []ContainerInfo,
	rules []deps.Rule,
	instanceUp map[string]bool,
) *Graph {
	g := &Graph{
		nodes: map[string]*Node{},
		out:   map[string][]string{},
		in:    map[string][]string{},
	}

	// Instance (tier) nodes. Seed from instanceUp plus any instance seen in procs.
	seenInst := map[string]bool{}
	addInstance := func(name string) {
		if name == "" {
			return
		}
		key := strings.ToLower(name)
		if seenInst[key] {
			return
		}
		seenInst[key] = true
		up, known := instanceUp[name]
		if !known {
			up, known = instanceUp[key]
		}
		h := HealthOK
		detail := ""
		if known && !up {
			h, detail = HealthOffline, "process-compose unreachable"
		}
		g.ensure(&Node{ID: instanceID(name), Kind: NodeInstance, Label: name, Health: h, Detail: detail})
	}
	for name := range instanceUp {
		addInstance(name)
	}

	// Service nodes from observed processes, each runs-on its instance.
	for _, p := range procs {
		addInstance(p.Instance)
		sn := g.ensure(&Node{
			ID:     serviceID(p.Instance, p.Name),
			Kind:   NodeService,
			Label:  p.Name,
			Health: healthFromCSS(p.CSSState),
			Detail: p.CSSState,
		})
		if inst := g.nodes[instanceID(p.Instance)]; inst != nil {
			g.edge(sn.ID, inst.ID)
			// A reachable instance whose service is unprobeable shouldn't happen,
			// but if the tier is offline the service health is unknown, not down.
			if inst.Health == HealthOffline {
				sn.Health = HealthOffline
			}
		}
	}

	// Bridge nodes.
	for _, b := range bridges {
		g.ensure(&Node{
			ID:     bridgeID(b.Bridge.Name),
			Kind:   NodeBridge,
			Label:  b.Bridge.Name,
			Health: healthFromBridge(b.State),
			Detail: b.State.String(),
		})
	}

	// Container nodes.
	for _, c := range containers {
		g.ensure(&Node{
			ID:     containerID(c.Name),
			Kind:   NodeContainer,
			Label:  c.Name,
			Health: healthFromContainer(c.State),
			Detail: c.Status,
		})
	}

	// Entity nodes + their dependency edges, derived from the same rule/legacy
	// matching Resolve uses.
	anyOnline := false
	for _, up := range instanceUp {
		if up {
			anyOnline = true
			break
		}
	}
	resolved := Resolve(entities, procs, bridges, containers, rules, anyOnline)
	byID := make(map[string]EntityRefs, len(resolved))
	for _, er := range resolved {
		byID[er.EntityID] = er
	}
	for _, e := range entities {
		er := byID[e.ID]
		if len(er.Refs) == 0 {
			continue // no declared needs and no fallback — nothing to graph
		}
		en := g.ensure(&Node{
			ID: entityNodeID(e.ID), Kind: NodeEntity, Label: e.Name,
			Health: healthFromRefState(er.State), Detail: string(e.Kind),
		})
		for _, r := range er.Refs {
			g.edge(en.ID, g.supplierNodeFor(r, instanceUp).ID)
		}
		// An entity's health reflects the worst of its (graphed) suppliers,
		// including the offline-via-tier propagation captured on supplier nodes.
		en.Health = g.worstSupplier(en.ID, en.Health)
	}

	return g
}

// supplierNodeFor returns (creating if needed) the node a resolved Ref points
// at. For service needs that matched no running process — typically because the
// owning tier is unreachable or the service is absent — it synthesizes the
// supplier node so the chain (and its root cause) is still walkable.
func (g *Graph) supplierNodeFor(r Ref, instanceUp map[string]bool) *Node {
	switch {
	case r.Bridge != "" || r.Need.Kind == "bridge":
		name := r.Bridge
		if name == "" {
			name = r.Need.Name
		}
		return g.ensure(&Node{ID: bridgeID(name), Kind: NodeBridge, Label: name, Health: healthFromRefState(r.State), Detail: r.State.String()})
	case r.Container != "" || r.Need.Kind == "container":
		name := r.Container
		if name == "" {
			name = r.Need.Name
		}
		return g.ensure(&Node{ID: containerID(name), Kind: NodeContainer, Label: name, Health: healthFromRefState(r.State), Detail: r.State.String()})
	default: // service
		inst := r.Instance
		if inst == "" {
			inst = r.Need.Tier
		}
		name := r.Process
		if name == "" {
			name = r.Need.Name
		}
		id := serviceID(inst, name)
		if n, ok := g.nodes[id]; ok {
			return n
		}
		// Synthesize a service node for an unmatched need and link it to its
		// tier so diagnosis can attribute it (offline tier vs. truly missing).
		sn := g.ensure(&Node{ID: id, Kind: NodeService, Label: name, Health: healthFromRefState(r.State), Detail: r.State.String()})
		if inst != "" {
			up, known := instanceUp[inst]
			if !known {
				up, known = instanceUp[strings.ToLower(inst)]
			}
			ih := HealthOK
			idetail := ""
			if known && !up {
				ih, idetail = HealthOffline, "process-compose unreachable"
				sn.Health = HealthOffline
			}
			in := g.ensure(&Node{ID: instanceID(inst), Kind: NodeInstance, Label: inst, Health: ih, Detail: idetail})
			g.edge(sn.ID, in.ID)
		}
		return sn
	}
}

// worstSupplier returns the worse of base and the worst health among the node's
// direct suppliers — so an entity inherits an offline/down supplier's state.
func (g *Graph) worstSupplier(id string, base Health) Health {
	worst := base
	for _, s := range g.out[id] {
		if n := g.nodes[s]; n != nil && n.Health > worst {
			worst = n.Health
		}
	}
	return worst
}

// Diagnose walks from a node up its dependency edges, following an unhealthy
// supplier at each step, and returns the path ending at the root cause (the
// deepest node whose own suppliers are all healthy). Returns nil if the node is
// unknown; a single-element path if the node itself is the root cause.
func (g *Graph) Diagnose(id string) []*Node {
	start, ok := g.nodes[id]
	if !ok {
		return nil
	}
	var path []*Node
	visited := map[string]bool{}
	cur := start
	for cur != nil && !visited[cur.ID] {
		visited[cur.ID] = true
		path = append(path, cur)
		// Among suppliers, follow the worst unhealthy one (deterministically).
		deps := append([]string(nil), g.out[cur.ID]...)
		sort.Strings(deps)
		var next *Node
		for _, dep := range deps {
			dn := g.nodes[dep]
			if dn == nil || !dn.Health.isBad() {
				continue
			}
			if next == nil || dn.Health > next.Health {
				next = dn
			}
		}
		if next == nil {
			break // cur has no unhealthy supplier → it is the root cause
		}
		cur = next
	}
	return path
}

// DiagnoseEntity is Diagnose for an entity addressed by its entity ID, so
// callers (the web layer) don't need to know the node-ID scheme.
func (g *Graph) DiagnoseEntity(entityID string) []*Node {
	return g.Diagnose(entityNodeID(entityID))
}

// BlastRadius returns every node that (transitively) depends on id, via the
// reverse edges. Order is breadth-first from id; id itself is excluded.
func (g *Graph) BlastRadius(id string) []*Node {
	if _, ok := g.nodes[id]; !ok {
		return nil
	}
	seen := map[string]bool{id: true}
	var out []*Node
	queue := []string{id}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		deps := append([]string(nil), g.in[cur]...)
		sort.Strings(deps)
		for _, c := range deps {
			if seen[c] {
				continue
			}
			seen[c] = true
			if n := g.nodes[c]; n != nil {
				out = append(out, n)
			}
			queue = append(queue, c)
		}
	}
	return out
}

func healthFromCSS(css string) Health {
	switch css {
	case "running":
		return HealthOK
	case "starting":
		return HealthDegraded
	case "":
		return HealthMissing
	default: // stopped, error, disabled, unknown
		return HealthDown
	}
}

func healthFromBridge(s bridge.State) Health {
	switch s {
	case bridge.StateActive:
		return HealthOK
	case bridge.StateDrifted:
		return HealthDown
	case bridge.StateMissing:
		return HealthMissing
	default:
		return HealthUnknown
	}
}

func healthFromContainer(s ContainerState) Health {
	switch s {
	case ContainerRunning:
		return HealthOK
	case ContainerStopped:
		return HealthDown
	case ContainerMissing:
		return HealthMissing
	default:
		return HealthUnknown
	}
}

func healthFromRefState(r RefState) Health {
	switch r {
	case RefOK:
		return HealthOK
	case RefStopped:
		return HealthDown
	case RefMissing:
		return HealthMissing
	case RefOffline:
		return HealthOffline
	case RefUnresolved:
		return HealthMissing
	default:
		return HealthUnknown
	}
}
