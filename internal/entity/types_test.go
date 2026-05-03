package entity

import "testing"

func TestKindString(t *testing.T) {
	if KindMCPServer.String() != "mcp_server" {
		t.Errorf("KindMCPServer.String() = %q", KindMCPServer.String())
	}
	if Kind("custom").String() != "custom" {
		t.Errorf("Kind.String() should pass through arbitrary values")
	}
}

func TestGlobalScope(t *testing.T) {
	s := GlobalScope()
	if !s.Global {
		t.Error("GlobalScope().Global should be true")
	}
	if s.Project != "" {
		t.Errorf("GlobalScope().Project = %q, want empty", s.Project)
	}
	if s.String() != "global" || s.Label() != "global" {
		t.Errorf("GlobalScope strings = %q/%q", s.String(), s.Label())
	}
}

func TestProjectScope(t *testing.T) {
	s := ProjectScope("/home/u/repo")
	if s.Global {
		t.Error("ProjectScope().Global should be false")
	}
	if s.Project != "/home/u/repo" {
		t.Errorf("ProjectScope().Project = %q", s.Project)
	}
	if s.String() != "project:/home/u/repo" {
		t.Errorf("ProjectScope.String() = %q", s.String())
	}
	if s.Label() != "project" {
		t.Errorf("ProjectScope.Label() = %q", s.Label())
	}
}
