package deps

import (
	"testing"

	"github.com/eike-hass/infra-mngmt/internal/entity"
)

func mcpAt(scope entity.Scope, name string) entity.Entity {
	return entity.Entity{Kind: entity.KindMCPServer, Name: name, Scope: scope}
}

func skillAt(scope entity.Scope, name string) entity.Entity {
	return entity.Entity{Kind: entity.KindSkill, Name: name, Scope: scope}
}

func TestMatchAnyScope(t *testing.T) {
	rules := []Rule{
		{Entity: "mcp:llama", Scope: "*", Needs: []Need{{Kind: "service", Name: "llama-server"}}},
	}
	got := Match(rules, mcpAt(entity.GlobalScope(), "llama"))
	if len(got) != 1 || got[0].Name != "llama-server" {
		t.Errorf("any-scope match failed: %+v", got)
	}
	got = Match(rules, mcpAt(entity.ProjectScope("/proj/a"), "llama"))
	if len(got) != 1 {
		t.Errorf("any-scope should match project too: %+v", got)
	}
}

func TestMatchHostScope(t *testing.T) {
	rules := []Rule{
		{Entity: "mcp:llama", Scope: "host", Needs: []Need{{Kind: "bridge", Name: "x"}}},
	}
	if got := Match(rules, mcpAt(entity.GlobalScope(), "llama")); len(got) != 1 {
		t.Errorf("host scope should match global: %+v", got)
	}
	if got := Match(rules, mcpAt(entity.ProjectScope("/proj"), "llama")); len(got) != 0 {
		t.Errorf("host scope should NOT match project: %+v", got)
	}
}

func TestMatchProjectGlob(t *testing.T) {
	rules := []Rule{
		{Entity: "mcp:llama", Scope: "project:*", Needs: []Need{{Kind: "bridge", Name: "x"}}},
	}
	if got := Match(rules, mcpAt(entity.ProjectScope("/proj/a"), "llama")); len(got) != 1 {
		t.Errorf("project:* should match any project: %+v", got)
	}
	if got := Match(rules, mcpAt(entity.GlobalScope(), "llama")); len(got) != 0 {
		t.Errorf("project:* should NOT match global: %+v", got)
	}
}

func TestMatchProjectExact(t *testing.T) {
	rules := []Rule{
		{Entity: "mcp:llama", Scope: "project:/proj/a", Needs: []Need{{Kind: "bridge", Name: "x"}}},
	}
	if got := Match(rules, mcpAt(entity.ProjectScope("/proj/a"), "llama")); len(got) != 1 {
		t.Errorf("exact project match failed: %+v", got)
	}
	if got := Match(rules, mcpAt(entity.ProjectScope("/proj/b"), "llama")); len(got) != 0 {
		t.Errorf("non-matching project should not match: %+v", got)
	}
}

func TestMatchAccumulatesAcrossRules(t *testing.T) {
	rules := []Rule{
		{Entity: "mcp:llama", Scope: "*", Needs: []Need{{Kind: "service", Name: "llama-server"}}},
		{Entity: "mcp:llama", Scope: "project:/proj/a", Needs: []Need{{Kind: "bridge", Name: "extra"}}},
	}
	got := Match(rules, mcpAt(entity.ProjectScope("/proj/a"), "llama"))
	if len(got) != 2 {
		t.Errorf("expected union of 2 needs, got: %+v", got)
	}
}

func TestMatchDeduplicatesNeeds(t *testing.T) {
	rules := []Rule{
		{Entity: "skill:foo", Scope: "*", Needs: []Need{{Kind: "service", Name: "x"}}},
		{Entity: "skill:foo", Scope: "host", Needs: []Need{{Kind: "service", Name: "x"}}},
	}
	got := Match(rules, skillAt(entity.GlobalScope(), "foo"))
	if len(got) != 1 {
		t.Errorf("dup needs not deduplicated: %+v", got)
	}
}

func TestMatchUnknownEntity(t *testing.T) {
	rules := []Rule{
		{Entity: "mcp:llama", Scope: "*", Needs: []Need{{Kind: "service", Name: "x"}}},
	}
	if got := Match(rules, mcpAt(entity.GlobalScope(), "different")); len(got) != 0 {
		t.Errorf("non-matching entity returned needs: %+v", got)
	}
}

func TestEntityKey(t *testing.T) {
	cases := []struct {
		in   entity.Entity
		want string
	}{
		{entity.Entity{Kind: entity.KindMCPServer, Name: "llama"}, "mcp:llama"},
		{entity.Entity{Kind: entity.KindSkill, Name: "summarize-doc"}, "skill:summarize-doc"},
		{entity.Entity{Kind: entity.KindHook, Name: "PreToolUse"}, "hook:PreToolUse"},
	}
	for _, tc := range cases {
		if got := EntityKey(tc.in); got != tc.want {
			t.Errorf("EntityKey(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
