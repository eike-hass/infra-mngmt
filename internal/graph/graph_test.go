package graph

import (
	"testing"

	"github.com/eike-hass/infra-mngmt/internal/bridge"
	"github.com/eike-hass/infra-mngmt/internal/deps"
	"github.com/eike-hass/infra-mngmt/internal/entity"
)

func ent(id, name string) entity.Entity {
	return entity.Entity{ID: id, Kind: entity.KindMCPServer, Name: name, Scope: entity.GlobalScope()}
}

func rule(name string, needs ...deps.Need) deps.Rule {
	return deps.Rule{Entity: "mcp:" + name, Scope: "host", Needs: needs}
}

// Cross-tier root cause: an MCP needs a Windows service, but the Windows
// process-compose tier is unreachable. Diagnose must trace MCP → service →
// instance and bottom out at the offline tier.
func TestDiagnose_OfflineTierIsRootCause(t *testing.T) {
	entities := []entity.Entity{ent("e1", "foo")}
	rules := []deps.Rule{rule("foo", deps.Need{Kind: "service", Name: "llama", Tier: "windows"})}
	// windows is unreachable → no procs from it.
	g := BuildGraph(entities, nil, nil, nil, rules, map[string]bool{"windows": false})

	path := g.Diagnose(entityNodeID("e1"))
	if len(path) != 3 {
		t.Fatalf("path len = %d, want 3 (entity→service→instance); path=%v", len(path), ids(path))
	}
	root := path[len(path)-1]
	if root.ID != instanceID("windows") {
		t.Errorf("root cause = %q, want %q", root.ID, instanceID("windows"))
	}
	if root.Health != HealthOffline {
		t.Errorf("root health = %s, want offline", root.Health)
	}
}

// Service down on a reachable tier: the root cause is the service itself, not
// the (healthy) instance.
func TestDiagnose_StoppedServiceIsRootCause(t *testing.T) {
	entities := []entity.Entity{ent("e1", "bar")}
	rules := []deps.Rule{rule("bar", deps.Need{Kind: "service", Name: "db", Tier: "wsl"})}
	procs := []ProcInfo{{Instance: "wsl", Name: "db", CSSState: "stopped"}}
	g := BuildGraph(entities, procs, nil, nil, rules, map[string]bool{"wsl": true})

	path := g.Diagnose(entityNodeID("e1"))
	root := path[len(path)-1]
	if root.ID != serviceID("wsl", "db") {
		t.Fatalf("root cause = %q, want %q; path=%v", root.ID, serviceID("wsl", "db"), ids(path))
	}
	if root.Health != HealthDown {
		t.Errorf("root health = %s, want down", root.Health)
	}
}

// A healthy entity diagnoses to just itself (nothing bad downstream).
func TestDiagnose_HealthyEntityTerminatesAtItself(t *testing.T) {
	entities := []entity.Entity{ent("e1", "ok")}
	rules := []deps.Rule{rule("ok", deps.Need{Kind: "service", Name: "live", Tier: "wsl"})}
	procs := []ProcInfo{{Instance: "wsl", Name: "live", CSSState: "running"}}
	g := BuildGraph(entities, procs, nil, nil, rules, map[string]bool{"wsl": true})

	if h := g.Node(entityNodeID("e1")).Health; h != HealthOK {
		t.Fatalf("entity health = %s, want ok", h)
	}
	path := g.Diagnose(entityNodeID("e1"))
	if len(path) != 1 || path[0].ID != entityNodeID("e1") {
		t.Errorf("healthy diagnose path = %v, want [entity:e1]", ids(path))
	}
}

// Entity nodes carry their entity kind (not the bare structural "entity") and
// their project scope, so a blast-radius dependent reads "skill deploy · my-repo"
// — telling you which project a destructive action would break, not just that
// "an entity" depends on it.
func TestEntityNodeCarriesKindAndProjectScope(t *testing.T) {
	projEnt := skill("deploy", entity.ProjectScope("/home/u/repos/my-repo"))
	globalEnt := ent("g1", "foo") // global mcp_server named "foo"

	rules := []deps.Rule{
		{Entity: "skill:deploy", Scope: "project:*", Needs: []deps.Need{{Kind: "service", Name: "live", Tier: "wsl"}}},
		rule("foo", deps.Need{Kind: "service", Name: "live", Tier: "wsl"}),
	}
	procs := []ProcInfo{{Instance: "wsl", Name: "live", CSSState: "running"}}
	g := BuildGraph([]entity.Entity{projEnt, globalEnt}, procs, nil, nil, rules, map[string]bool{"wsl": true})

	pn := g.Node(entityNodeID(projEnt.ID))
	if pn == nil {
		t.Fatal("project entity node was not created")
	}
	if pn.Sub != "my-repo" {
		t.Errorf("project node Sub = %q, want repo name %q", pn.Sub, "my-repo")
	}
	if got := pn.TypeLabel(); got != string(entity.KindSkill) {
		t.Errorf("project node TypeLabel = %q, want entity kind %q (not structural %q)", got, entity.KindSkill, NodeEntity)
	}

	gn := g.Node(entityNodeID(globalEnt.ID))
	if gn == nil {
		t.Fatal("global entity node was not created")
	}
	if gn.Sub != "global" {
		t.Errorf("global node Sub = %q, want %q", gn.Sub, "global")
	}
}

// scopeLabel renders an entity scope as a short, display-ready project name.
func TestScopeLabel(t *testing.T) {
	cases := []struct {
		name  string
		scope entity.Scope
		want  string
	}{
		{"global", entity.GlobalScope(), "global"},
		{"project repo basename", entity.ProjectScope("/home/u/repos/infra-mngmt"), "infra-mngmt"},
		{"project trailing slash", entity.ProjectScope("/home/u/repos/app/"), "app"},
		{"empty project", entity.Scope{}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := scopeLabel(c.scope); got != c.want {
				t.Errorf("scopeLabel(%+v) = %q, want %q", c.scope, got, c.want)
			}
		})
	}
}

// Blast radius: stopping the Windows tier affects the service that runs on it
// and the entity that needs that service.
func TestBlastRadius_TierAffectsServiceAndEntity(t *testing.T) {
	entities := []entity.Entity{ent("e1", "foo")}
	rules := []deps.Rule{rule("foo", deps.Need{Kind: "service", Name: "llama", Tier: "windows"})}
	procs := []ProcInfo{{Instance: "windows", Name: "llama", CSSState: "running"}}
	g := BuildGraph(entities, procs, nil, nil, rules, map[string]bool{"windows": true})

	dependents := idset(g.BlastRadius(instanceID("windows")))
	for _, want := range []string{serviceID("windows", "llama"), entityNodeID("e1")} {
		if !dependents[want] {
			t.Errorf("blast radius of windows missing %q; got %v", want, keys(dependents))
		}
	}
}

// Bridge supplier: a drifted bridge is the root cause for an entity that needs it.
func TestDiagnose_DriftedBridge(t *testing.T) {
	entities := []entity.Entity{ent("e1", "br")}
	rules := []deps.Rule{rule("br", deps.Need{Kind: "bridge", Name: "relay"})}
	bridges := []BridgeInfo{{Bridge: bridge.Bridge{Name: "relay"}, State: bridge.StateDrifted}}
	g := BuildGraph(entities, nil, bridges, nil, rules, map[string]bool{"wsl": true})

	path := g.Diagnose(entityNodeID("e1"))
	root := path[len(path)-1]
	if root.ID != bridgeID("relay") || root.Health != HealthDown {
		t.Errorf("root = %q (%s), want %q (down); path=%v", root.ID, root.Health, bridgeID("relay"), ids(path))
	}
}

func ids(ns []*Node) []string {
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = n.ID
	}
	return out
}
func idset(ns []*Node) map[string]bool {
	m := map[string]bool{}
	for _, n := range ns {
		m[n.ID] = true
	}
	return m
}
func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
