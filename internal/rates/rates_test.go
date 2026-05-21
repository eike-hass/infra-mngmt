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
