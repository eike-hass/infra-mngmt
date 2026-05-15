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

// TestLoadComposeProjectWithServices exercises the YAML round-trip for
// the project-group schema: top-level `services:` on a Container, with
// optional `role:` and `url:` per service. Without this test, a refactor
// that renames a yaml tag would only get caught further down (view
// builder + template), with an opaque error like "WebURL empty".
func TestLoadComposeProjectWithServices(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "containers.yaml")
	body := `containers:
  - name: open-design
    kind: open-design
    compose_file: /abs/compose.yaml
    services:
      - container: open-design
        role: web
        url: http://localhost:7456
      - container: od-token-stats
        role: token-stats
        url: http://localhost:7460

  - name: my-stack
    compose_file: /abs/other.yaml
    services:
      - container: web
      - container: api
        role: api
        url: http://localhost:8080
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(f.Containers) != 2 {
		t.Fatalf("want 2 containers, got %d", len(f.Containers))
	}

	od := f.Containers[0]
	if od.Kind != "open-design" || od.ComposeFile != "/abs/compose.yaml" {
		t.Errorf("OD entry: kind=%q compose_file=%q", od.Kind, od.ComposeFile)
	}
	if len(od.Services) != 2 {
		t.Fatalf("OD services: want 2, got %d", len(od.Services))
	}
	if od.Services[0].Role != "web" || od.Services[0].URL != "http://localhost:7456" {
		t.Errorf("OD services[0] role/url: %+v", od.Services[0])
	}
	if od.Services[1].Role != "token-stats" || od.Services[1].URL != "http://localhost:7460" {
		t.Errorf("OD services[1] role/url: %+v", od.Services[1])
	}

	plain := f.Containers[1]
	if plain.Kind != "" {
		t.Errorf("plain stack should have no kind, got %q", plain.Kind)
	}
	if len(plain.Services) != 2 {
		t.Fatalf("plain stack services: want 2, got %d", len(plain.Services))
	}
	if plain.Services[0].Container != "web" || plain.Services[0].Role != "" {
		t.Errorf("plain services[0]: %+v", plain.Services[0])
	}
}
