package graph

import (
	"testing"

	"github.com/eike-hass/infra-mngmt/internal/entity"
)

func mcp(name string) entity.Entity {
	return entity.Entity{
		ID:   "src:mcp_server:" + name,
		Kind: entity.KindMCPServer,
		Name: name,
	}
}

func TestResolveSkipsNonMCP(t *testing.T) {
	ents := []entity.Entity{
		{ID: "x", Kind: entity.KindCommand, Name: "run"},
		{ID: "y", Kind: entity.KindAgent, Name: "helper"},
	}
	if refs := Resolve(ents, nil, true); len(refs) != 0 {
		t.Errorf("expected 0 refs for non-MCP entities, got %d", len(refs))
	}
}

func TestResolveExactNameMatchRunning(t *testing.T) {
	refs := Resolve(
		[]entity.Entity{mcp("llama-server")},
		[]ProcInfo{{Instance: "wsl", Name: "llama-server", CSSState: "running"}},
		true,
	)
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	r := refs[0]
	if r.State != RefOK {
		t.Errorf("State = %s, want ok", r.State)
	}
	if r.Process != "llama-server" || r.Instance != "wsl" {
		t.Errorf("Process/Instance = %q/%q", r.Process, r.Instance)
	}
	if r.IsBroken() {
		t.Error("running ref should not be broken")
	}
}

func TestResolveSubstringMatch(t *testing.T) {
	// Process name contains entity name
	refs := Resolve(
		[]entity.Entity{mcp("llama")},
		[]ProcInfo{{Instance: "wsl", Name: "llama-server", CSSState: "running"}},
		true,
	)
	if refs[0].State != RefOK {
		t.Errorf("substring match should succeed, got state %s", refs[0].State)
	}
	// Entity name contains process name
	refs = Resolve(
		[]entity.Entity{mcp("llama-server-mcp")},
		[]ProcInfo{{Instance: "wsl", Name: "llama-server", CSSState: "running"}},
		true,
	)
	if refs[0].State != RefOK {
		t.Errorf("reverse substring match should succeed, got %s", refs[0].State)
	}
}

func TestResolveMatchedButStopped(t *testing.T) {
	refs := Resolve(
		[]entity.Entity{mcp("llama")},
		[]ProcInfo{{Instance: "wsl", Name: "llama-server", CSSState: "stopped"}},
		true,
	)
	if refs[0].State != RefStopped {
		t.Errorf("State = %s, want stopped", refs[0].State)
	}
	if !refs[0].IsBroken() {
		t.Error("stopped ref should be broken")
	}
}

func TestResolveUnresolvedWhenOnline(t *testing.T) {
	refs := Resolve(
		[]entity.Entity{mcp("nope")},
		[]ProcInfo{{Instance: "wsl", Name: "other", CSSState: "running"}},
		true,
	)
	if refs[0].State != RefUnresolved {
		t.Errorf("State = %s, want unresolved", refs[0].State)
	}
	if !refs[0].IsBroken() {
		t.Error("unresolved ref should be broken")
	}
}

func TestResolveOfflineWhenNoneOnline(t *testing.T) {
	refs := Resolve(
		[]entity.Entity{mcp("llama")},
		nil, // no procs
		false,
	)
	if refs[0].State != RefOffline {
		t.Errorf("State = %s, want offline", refs[0].State)
	}
	// Offline is NOT considered broken — we just don't know yet.
	if refs[0].IsBroken() {
		t.Error("offline ref should not be broken")
	}
}

func TestResolvePicksRunningOverStopped(t *testing.T) {
	refs := Resolve(
		[]entity.Entity{mcp("llama")},
		[]ProcInfo{
			{Instance: "wsl", Name: "llama-stopped", CSSState: "stopped"},
			{Instance: "win", Name: "llama-running", CSSState: "running"},
		},
		true,
	)
	if refs[0].State != RefOK {
		t.Errorf("expected to prefer running match, got state %s (process=%s)", refs[0].State, refs[0].Process)
	}
}

func TestRefStateString(t *testing.T) {
	cases := map[RefState]string{
		RefOK: "ok", RefStopped: "stopped", RefUnresolved: "unresolved", RefOffline: "offline",
	}
	for s, want := range cases {
		if s.String() != want {
			t.Errorf("%d.String() = %q, want %q", s, s.String(), want)
		}
	}
}

func TestResolveCaseInsensitive(t *testing.T) {
	refs := Resolve(
		[]entity.Entity{mcp("LLama")},
		[]ProcInfo{{Instance: "wsl", Name: "llama-server", CSSState: "running"}},
		true,
	)
	if refs[0].State != RefOK {
		t.Errorf("case-insensitive match failed: state %s", refs[0].State)
	}
}
