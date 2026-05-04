package deps

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEmptyAndMissing(t *testing.T) {
	f, err := Load("")
	if err != nil || f == nil {
		t.Fatalf("empty path should return empty File, got %v / %v", f, err)
	}
	dir := t.TempDir()
	f, err = Load(filepath.Join(dir, "no-such.yaml"))
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if len(f.Dependencies) != 0 {
		t.Errorf("missing file should produce empty rules; got %d", len(f.Dependencies))
	}
}

func TestLoadValid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deps.yaml")
	body := `dependencies:
  - entity: mcp:llama
    scope: "*"
    needs: [service:llama-server, bridge:llama-cpp]
  - entity: skill:summarize-doc
    scope: host
    needs: [service:llama-server@windows]
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(f.Dependencies) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(f.Dependencies))
	}
	r0 := f.Dependencies[0]
	if r0.Entity != "mcp:llama" || r0.Scope != "*" || len(r0.Needs) != 2 {
		t.Errorf("rule 0: %+v", r0)
	}
	if r0.Needs[0] != (Need{Kind: "service", Name: "llama-server"}) {
		t.Errorf("rule 0 need 0: %+v", r0.Needs[0])
	}
	if r0.Needs[1] != (Need{Kind: "bridge", Name: "llama-cpp"}) {
		t.Errorf("rule 0 need 1: %+v", r0.Needs[1])
	}
	r1 := f.Dependencies[1]
	if r1.Needs[0].Tier != "windows" {
		t.Errorf("tier qualifier lost: %+v", r1.Needs[0])
	}
}

func TestLoadInvalidNeed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deps.yaml")
	body := `dependencies:
  - entity: mcp:llama
    scope: host
    needs: [garbage]
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected parse error for bad need string")
	}
}

func TestLoadInvalidEntity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deps.yaml")
	body := `dependencies:
  - entity: weird:thing
    scope: host
    needs: [service:x]
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected validation error for bad entity kind")
	}
}
