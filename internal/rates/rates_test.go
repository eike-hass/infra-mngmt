package rates

import (
	"path/filepath"
	"testing"
)

func TestLoadEmptyPath(t *testing.T) {
	f, err := Load("")
	if err != nil {
		t.Fatalf("Load(\"\"): %v", err)
	}
	if len(f.Models) != 0 {
		t.Errorf("expected empty map, got %d entries", len(f.Models))
	}
}

func TestLoadMissingFile(t *testing.T) {
	f, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("expected nil error for missing file, got: %v", err)
	}
	if len(f.Models) != 0 {
		t.Errorf("expected empty map, got %d entries", len(f.Models))
	}
}

func TestLoadValidYAML(t *testing.T) {
	p := filepath.Join(t.TempDir(), "rates.yaml")
	const body = `
models:
  claude-sonnet-4-5: { in: 3, out: 15, cache_read: 0.30 }
  claude-haiku-4-5: { in: 1, out: 5, cache_read: 0.10 }
  llama-3.1-70b-local: { in: 0, out: 0, cache_read: 0 }
`
	if err := writeFile(p, body); err != nil {
		t.Fatal(err)
	}
	f, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, want := len(f.Models), 3; got != want {
		t.Fatalf("model count = %d, want %d", got, want)
	}
	if r := f.Models["claude-sonnet-4-5"]; r.In != 3 || r.Out != 15 || r.CacheRead != 0.30 {
		t.Errorf("sonnet 4.5 rate = %+v, want {3 15 0.3}", r)
	}
	if r := f.Models["llama-3.1-70b-local"]; r.In != 0 || r.Out != 0 || r.CacheRead != 0 {
		t.Errorf("local llama rate = %+v, want all-zero", r)
	}
}

func TestLoadBadYAML(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bad.yaml")
	if err := writeFile(p, "models: not-a-map\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil {
		t.Error("expected parse error for non-map models block")
	}
}

func writeFile(path, body string) error {
	return writeStr(path, body)
}

func TestLoadResolvesEquivalentOf(t *testing.T) {
	p := filepath.Join(t.TempDir(), "rates.yaml")
	const body = `
models:
  GLM-4.7: { in: 0.55, out: 2.20, cache_read: 0.055 }
  opencode/big-pickle: { in: 0, out: 0, cache_read: 0, equivalent_of: GLM-4.7 }
`
	if err := writeFile(p, body); err != nil {
		t.Fatal(err)
	}
	f, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	bp := f.Models["opencode/big-pickle"]
	if !bp.HasEquivalent {
		t.Fatalf("expected HasEquivalent=true, got %+v", bp)
	}
	if bp.EquivalentIn != 0.55 || bp.EquivalentOut != 2.20 || bp.EquivalentCacheRead != 0.055 {
		t.Errorf("equivalent rates = (%v, %v, %v), want (0.55, 2.20, 0.055)",
			bp.EquivalentIn, bp.EquivalentOut, bp.EquivalentCacheRead)
	}
	if bp.In != 0 || bp.Out != 0 || bp.CacheRead != 0 {
		t.Errorf("actual rates should remain zero, got %+v", bp)
	}
	// Target itself should NOT gain Equivalent* fields (no equivalent_of on
	// it) — independent entries are unaffected by the resolver.
	if glm := f.Models["GLM-4.7"]; glm.HasEquivalent {
		t.Errorf("target model should not be flagged as having equivalent, got %+v", glm)
	}
}

func TestLoadUnresolvableEquivalentOfWarnsButDoesntFail(t *testing.T) {
	p := filepath.Join(t.TempDir(), "rates.yaml")
	const body = `
models:
  opencode/big-pickle: { in: 0, out: 0, cache_read: 0, equivalent_of: ghost-model }
`
	if err := writeFile(p, body); err != nil {
		t.Fatal(err)
	}
	f, err := Load(p)
	if err != nil {
		t.Fatalf("Load should not fail on unresolvable equivalent_of, got: %v", err)
	}
	bp := f.Models["opencode/big-pickle"]
	if bp.HasEquivalent {
		t.Errorf("HasEquivalent should stay false when target missing, got %+v", bp)
	}
	// EquivalentOf string is preserved so the operator can diagnose.
	if bp.EquivalentOf != "ghost-model" {
		t.Errorf("EquivalentOf string should be preserved for diagnostics, got %q", bp.EquivalentOf)
	}
}

func TestLoadEquivalentOfChainNotFollowed(t *testing.T) {
	// equivalent_of resolution is intentionally one hop only — chaining
	// would invite surprise. A -> B -> C: A gets B's actual rates, NOT C's.
	p := filepath.Join(t.TempDir(), "rates.yaml")
	const body = `
models:
  C: { in: 9, out: 9, cache_read: 0.9 }
  B: { in: 1, out: 2, cache_read: 0.1, equivalent_of: C }
  A: { in: 0, out: 0, cache_read: 0, equivalent_of: B }
`
	if err := writeFile(p, body); err != nil {
		t.Fatal(err)
	}
	f, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	a := f.Models["A"]
	if !a.HasEquivalent {
		t.Fatalf("A should have equivalent from B, got %+v", a)
	}
	if a.EquivalentIn != 1 || a.EquivalentOut != 2 || a.EquivalentCacheRead != 0.1 {
		t.Errorf("A's equivalent should be B's actual rates (1/2/0.1), got (%v/%v/%v)",
			a.EquivalentIn, a.EquivalentOut, a.EquivalentCacheRead)
	}
}
