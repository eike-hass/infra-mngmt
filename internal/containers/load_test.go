package containers

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEmptyAndMissing(t *testing.T) {
	f, err := Load("")
	if err != nil || len(f.Containers) != 0 {
		t.Fatalf("empty path should be empty File, got %+v / %v", f, err)
	}
	dir := t.TempDir()
	f, err = Load(filepath.Join(dir, "no-such.yaml"))
	if err != nil || len(f.Containers) != 0 {
		t.Errorf("missing file should be empty File, got %+v / %v", f, err)
	}
}

func TestLoadValid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "containers.yaml")
	body := `containers:
  - name: ident-browser
    description: "MCP via headless Chrome"
  - name: vault-demo
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(f.Containers) != 2 {
		t.Fatalf("expected 2, got %d", len(f.Containers))
	}
	if f.Containers[0].Name != "ident-browser" || f.Containers[0].Description == "" {
		t.Errorf("first entry mismatch: %+v", f.Containers[0])
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte("\tnope:\n  - :"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected parse error")
	}
}
