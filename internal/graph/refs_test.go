package graph

import (
	"testing"

	"github.com/eike-hass/infra-mngmt/internal/bridge"
	"github.com/eike-hass/infra-mngmt/internal/deps"
	"github.com/eike-hass/infra-mngmt/internal/entity"
)

func mcp(name string) entity.Entity {
	return entity.Entity{
		ID:    "src:mcp_server:" + name,
		Kind:  entity.KindMCPServer,
		Name:  name,
		Scope: entity.GlobalScope(),
	}
}

func mcpInProject(name, projectRoot string) entity.Entity {
	return entity.Entity{
		ID:    "src:mcp_server:" + name + "@" + projectRoot,
		Kind:  entity.KindMCPServer,
		Name:  name,
		Scope: entity.ProjectScope(projectRoot),
	}
}

func skill(name string, scope entity.Scope) entity.Entity {
	return entity.Entity{
		ID:    "src:skill:" + name,
		Kind:  entity.KindSkill,
		Name:  name,
		Scope: scope,
	}
}

// ── shape tests ────────────────────────────────────────────────────────────

func TestResolveReturnsOneEntityRefsPerEntity(t *testing.T) {
	ents := []entity.Entity{mcp("a"), {Kind: entity.KindAgent, Name: "b"}, skill("c", entity.GlobalScope())}
	got := Resolve(ents, nil, nil, nil, nil, true)
	if len(got) != 3 {
		t.Fatalf("expected 3 EntityRefs (one per entity), got %d", len(got))
	}
}

func TestResolveNonMCPWithoutRulesIsOK(t *testing.T) {
	// Non-MCP entities without matching rules have no declared needs → status OK.
	got := Resolve(
		[]entity.Entity{{Kind: entity.KindAgent, Name: "helper"}},
		nil, nil, nil, nil, true,
	)
	if got[0].State != RefOK {
		t.Errorf("non-MCP w/o rules: state = %s, want ok", got[0].State)
	}
	if got[0].IsBroken() {
		t.Error("non-MCP w/o rules should not be broken")
	}
}

// ── legacy substring fallback (MCP-only, when no rule matches) ─────────────

func TestLegacySubstringMatchProcessContainsEntity(t *testing.T) {
	got := Resolve(
		[]entity.Entity{mcp("llama")},
		[]ProcInfo{{Instance: "wsl", Name: "llama-server", CSSState: "running"}},
		nil, nil, nil, true,
	)
	if got[0].State != RefOK {
		t.Errorf("substring match should succeed, got %s", got[0].State)
	}
	if !got[0].Legacy {
		t.Error("expected Legacy=true on fallback resolution")
	}
	if len(got[0].Refs) != 1 || got[0].Refs[0].Process != "llama-server" {
		t.Errorf("unexpected ref: %+v", got[0].Refs)
	}
}

func TestLegacyEntityContainsProcess(t *testing.T) {
	got := Resolve(
		[]entity.Entity{mcp("llama-server-mcp")},
		[]ProcInfo{{Instance: "wsl", Name: "llama-server", CSSState: "running"}},
		nil, nil, nil, true,
	)
	if got[0].State != RefOK {
		t.Errorf("reverse substring match should succeed, got %s", got[0].State)
	}
}

func TestLegacyMatchedButStopped(t *testing.T) {
	got := Resolve(
		[]entity.Entity{mcp("llama")},
		[]ProcInfo{{Instance: "wsl", Name: "llama-server", CSSState: "stopped"}},
		nil, nil, nil, true,
	)
	if got[0].State != RefStopped {
		t.Errorf("state = %s, want stopped", got[0].State)
	}
	if !got[0].IsBroken() {
		t.Error("stopped should be broken")
	}
}

func TestLegacyUnresolved(t *testing.T) {
	got := Resolve(
		[]entity.Entity{mcp("nope")},
		[]ProcInfo{{Instance: "wsl", Name: "other", CSSState: "running"}},
		nil, nil, nil, true,
	)
	if got[0].State != RefUnresolved {
		t.Errorf("state = %s, want unresolved", got[0].State)
	}
}

func TestLegacyOfflineWhenNoneOnline(t *testing.T) {
	got := Resolve(
		[]entity.Entity{mcp("llama")},
		nil, nil, nil, nil, false,
	)
	if got[0].State != RefOffline {
		t.Errorf("state = %s, want offline", got[0].State)
	}
	if got[0].IsBroken() {
		t.Error("offline should not be broken")
	}
}

func TestLegacyPicksRunningOverStopped(t *testing.T) {
	got := Resolve(
		[]entity.Entity{mcp("llama")},
		[]ProcInfo{
			{Instance: "wsl", Name: "llama-stopped", CSSState: "stopped"},
			{Instance: "win", Name: "llama-running", CSSState: "running"},
		},
		nil, nil, nil, true,
	)
	if got[0].State != RefOK {
		t.Errorf("expected best-of: ok, got %s (process=%s)", got[0].State, got[0].Refs[0].Process)
	}
}

// ── rule-based resolution ──────────────────────────────────────────────────

func TestRuleExactServiceMatch(t *testing.T) {
	rules := []deps.Rule{
		{Entity: "mcp:llama", Scope: "*", Needs: []deps.Need{{Kind: "service", Name: "llama-server"}}},
	}
	got := Resolve(
		[]entity.Entity{mcp("llama")},
		[]ProcInfo{{Instance: "windows", Name: "llama-server", CSSState: "running"}},
		nil, nil, rules, true,
	)
	if got[0].State != RefOK {
		t.Errorf("expected ok via rule, got %s", got[0].State)
	}
	if got[0].Legacy {
		t.Error("rule-based match should not be flagged as legacy")
	}
	if got[0].Refs[0].Need.Name != "llama-server" {
		t.Errorf("ref need not preserved: %+v", got[0].Refs[0])
	}
}

func TestRuleSubstringDoesNotMatch(t *testing.T) {
	// With explicit rules, names are matched exactly — not by substring.
	rules := []deps.Rule{
		{Entity: "mcp:llama", Scope: "*", Needs: []deps.Need{{Kind: "service", Name: "llama"}}},
	}
	got := Resolve(
		[]entity.Entity{mcp("llama")},
		[]ProcInfo{{Instance: "wsl", Name: "llama-server", CSSState: "running"}}, // close, no cigar
		nil, nil, rules, true,
	)
	if got[0].State != RefMissing {
		t.Errorf("rule needs an exact name; got state %s", got[0].State)
	}
}

func TestRuleManyToMany(t *testing.T) {
	// One process backs three entities across host + two projects.
	rules := []deps.Rule{
		{Entity: "mcp:llama", Scope: "*", Needs: []deps.Need{{Kind: "service", Name: "llama-server"}}},
	}
	ents := []entity.Entity{
		mcp("llama"),
		mcpInProject("llama", "/proj/a"),
		mcpInProject("llama", "/proj/b"),
	}
	got := Resolve(ents,
		[]ProcInfo{{Instance: "windows", Name: "llama-server", CSSState: "running"}},
		nil, nil, rules, true,
	)
	for i, er := range got {
		if er.State != RefOK {
			t.Errorf("entity %d: state %s, want ok", i, er.State)
		}
	}
}

func TestRuleProjectOverrideAccumulates(t *testing.T) {
	rules := []deps.Rule{
		{Entity: "mcp:llama", Scope: "*", Needs: []deps.Need{{Kind: "service", Name: "llama-server"}}},
		{Entity: "mcp:llama", Scope: "project:/proj/a", Needs: []deps.Need{{Kind: "bridge", Name: "extra"}}},
	}
	bi := []BridgeInfo{
		{Bridge: bridge.Bridge{Name: "extra"}, State: bridge.StateMissing},
	}
	got := Resolve(
		[]entity.Entity{mcpInProject("llama", "/proj/a")},
		[]ProcInfo{{Instance: "windows", Name: "llama-server", CSSState: "running"}},
		bi, nil, rules, true,
	)
	if len(got[0].Refs) != 2 {
		t.Fatalf("expected 2 refs (service ok + bridge missing), got %d: %+v", len(got[0].Refs), got[0].Refs)
	}
	if got[0].State != RefMissing {
		t.Errorf("worst-of rollup should be missing (bridge), got %s", got[0].State)
	}
}

func TestRuleTierDisambiguation(t *testing.T) {
	rules := []deps.Rule{
		{Entity: "mcp:llama", Scope: "host", Needs: []deps.Need{{Kind: "service", Name: "llama-server", Tier: "windows"}}},
	}
	procs := []ProcInfo{
		{Instance: "wsl", Name: "llama-server", CSSState: "stopped"},     // wrong tier
		{Instance: "windows", Name: "llama-server", CSSState: "running"}, // right one
	}
	got := Resolve([]entity.Entity{mcp("llama")}, procs, nil, nil, rules, true)
	if got[0].State != RefOK {
		t.Errorf("tier-qualified rule should pick the windows one; got %s (instance=%s)", got[0].State, got[0].Refs[0].Instance)
	}
	if got[0].Refs[0].Instance != "windows" {
		t.Errorf("expected instance=windows, got %q", got[0].Refs[0].Instance)
	}
}

// ── bridge resolution ──────────────────────────────────────────────────────

func TestBridgeActiveResolves(t *testing.T) {
	rules := []deps.Rule{
		{Entity: "mcp:producer-pal", Scope: "host", Needs: []deps.Need{{Kind: "bridge", Name: "producer-pal"}}},
	}
	bi := []BridgeInfo{
		{Bridge: bridge.Bridge{Name: "producer-pal"}, State: bridge.StateActive},
	}
	got := Resolve([]entity.Entity{mcp("producer-pal")}, nil, bi, nil, rules, true)
	if got[0].State != RefOK {
		t.Errorf("active bridge should resolve to ok, got %s", got[0].State)
	}
}

func TestBridgeMissingMakesEntityBroken(t *testing.T) {
	rules := []deps.Rule{
		{Entity: "mcp:producer-pal", Scope: "host", Needs: []deps.Need{{Kind: "bridge", Name: "producer-pal"}}},
	}
	bi := []BridgeInfo{
		{Bridge: bridge.Bridge{Name: "producer-pal"}, State: bridge.StateMissing},
	}
	got := Resolve([]entity.Entity{mcp("producer-pal")}, nil, bi, nil, rules, true)
	if got[0].State != RefMissing {
		t.Errorf("missing bridge should mark entity missing, got %s", got[0].State)
	}
	if !got[0].IsBroken() {
		t.Error("missing bridge should make entity broken")
	}
}

func TestBridgeNotInListIsMissing(t *testing.T) {
	// Rule references a bridge name that doesn't exist in bridges.yaml.
	rules := []deps.Rule{
		{Entity: "mcp:x", Scope: "host", Needs: []deps.Need{{Kind: "bridge", Name: "nonexistent"}}},
	}
	got := Resolve([]entity.Entity{mcp("x")}, nil, nil, nil, rules, true)
	if got[0].State != RefMissing {
		t.Errorf("nonexistent bridge → missing; got %s", got[0].State)
	}
}

// ── skills as first-class consumers ────────────────────────────────────────

func TestSkillResolvesViaRule(t *testing.T) {
	rules := []deps.Rule{
		{Entity: "skill:summarize-doc", Scope: "host", Needs: []deps.Need{{Kind: "service", Name: "llama-server"}}},
	}
	got := Resolve(
		[]entity.Entity{skill("summarize-doc", entity.GlobalScope())},
		[]ProcInfo{{Instance: "windows", Name: "llama-server", CSSState: "running"}},
		nil, nil, rules, true,
	)
	if got[0].State != RefOK {
		t.Errorf("skill should resolve to ok via rule, got %s", got[0].State)
	}
}

func TestSkillMissingBackendBreaksIt(t *testing.T) {
	rules := []deps.Rule{
		{Entity: "skill:summarize-doc", Scope: "host", Needs: []deps.Need{{Kind: "service", Name: "llama-server"}}},
	}
	got := Resolve(
		[]entity.Entity{skill("summarize-doc", entity.GlobalScope())},
		nil, // no procs, no instances online
		nil, nil, rules, false,
	)
	if got[0].State != RefOffline {
		t.Errorf("expected offline when no compose instances online; got %s", got[0].State)
	}
}

// ── enum strings ───────────────────────────────────────────────────────────

func TestRefStateString(t *testing.T) {
	cases := map[RefState]string{
		RefOK: "ok", RefStopped: "stopped", RefMissing: "missing",
		RefUnresolved: "unresolved", RefOffline: "offline",
	}
	for s, want := range cases {
		if s.String() != want {
			t.Errorf("%d.String() = %q, want %q", s, s.String(), want)
		}
	}
}
