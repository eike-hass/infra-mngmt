package bridge

import (
	"os"
	"path/filepath"
	"testing"
)

const sampleBridgesYAML = `bridges:
  - name: producer-pal
    tier: windows
    type: portproxy+firewall
    listen: { addr: "${wsl-host-ip}", port: 3350 }
    connect: { addr: 127.0.0.1, port: 3350, family: auto }
    firewall:
      remote: 172.18.0.0/16
      display_name: "Producer Pal MCP"
  - name: llama-cpp
    tier: windows
    type: portproxy+firewall
    listen: { addr: "${wsl-host-ip}", port: 8080 }
    connect: { addr: 127.0.0.1, port: 8080, family: auto }
    firewall:
      remote: 172.18.0.0/16
      display_name: "llama.cpp Server"
`

func TestLoadEmptyPath(t *testing.T) {
	f, err := Load("")
	if err != nil {
		t.Fatalf("Load(\"\"): unexpected error %v", err)
	}
	if len(f.Bridges) != 0 {
		t.Errorf("Load(\"\") should return empty File; got %d bridges", len(f.Bridges))
	}
}

func TestLoadMissingFile(t *testing.T) {
	dir := t.TempDir()
	f, err := Load(filepath.Join(dir, "no-such.yaml"))
	if err != nil {
		t.Fatalf("Load missing: %v", err)
	}
	if len(f.Bridges) != 0 {
		t.Errorf("missing file should return empty File; got %d bridges", len(f.Bridges))
	}
}

func TestLoadValid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bridges.yaml")
	if err := os.WriteFile(path, []byte(sampleBridgesYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(f.Bridges) != 2 {
		t.Fatalf("expected 2 bridges, got %d", len(f.Bridges))
	}
	if f.Bridges[0].Name != "producer-pal" || f.Bridges[1].Name != "llama-cpp" {
		t.Errorf("bridge names mismatch: %+v", f.Bridges)
	}
	// Validate normalises empty Family to "auto".
	if f.Bridges[0].Connect.Family != FamilyAuto {
		t.Errorf("expected Family=auto after Validate, got %q", f.Bridges[0].Connect.Family)
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

func TestLoadValidationFailsOnDup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dup.yaml")
	body := `bridges:
  - name: x
    tier: windows
    type: portproxy+firewall
    listen: { addr: "${wsl-host-ip}", port: 1000 }
    connect: { addr: 127.0.0.1, port: 1000 }
    firewall: { remote: 10.0.0.0/8, display_name: X }
  - name: x
    tier: windows
    type: portproxy+firewall
    listen: { addr: "${wsl-host-ip}", port: 2000 }
    connect: { addr: 127.0.0.1, port: 2000 }
    firewall: { remote: 10.0.0.0/8, display_name: X2 }
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected validation error for duplicate name")
	}
}
