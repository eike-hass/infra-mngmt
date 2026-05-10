package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/eike-hass/infra-mngmt/config"
	"github.com/eike-hass/infra-mngmt/internal/bridge"
)

func TestFindWSLProcessComposeMatchesByDirectory(t *testing.T) {
	cfg := &config.Config{
		BridgesComposeFile: "/etc/im/process-compose.bridges.yaml",
		ProcessCompose: []config.ProcessCompose{
			{Name: "windows", ComposeFile: "/c/Users/u/.config/im/process-compose.yaml"},
			{Name: "wsl", ComposeFile: "/etc/im/process-compose.yaml"},
		},
	}
	pc, ok := findWSLProcessCompose(cfg)
	if !ok {
		t.Fatal("expected to find a matching process_compose entry")
	}
	if pc.Name != "wsl" {
		t.Errorf("matched the wrong PC instance: got %q, want wsl", pc.Name)
	}
}

func TestFindWSLProcessComposeNoMatch(t *testing.T) {
	cfg := &config.Config{
		BridgesComposeFile: "/etc/im/process-compose.bridges.yaml",
		ProcessCompose: []config.ProcessCompose{
			{Name: "windows", ComposeFile: "/c/Users/u/.config/im/process-compose.yaml"},
		},
	}
	if _, ok := findWSLProcessCompose(cfg); ok {
		t.Error("expected no match when no PC compose_file shares the fragment directory")
	}
}

func TestFindWSLProcessComposeEmptyConfig(t *testing.T) {
	cfg := &config.Config{}
	if _, ok := findWSLProcessCompose(cfg); ok {
		t.Error("expected no match with empty config")
	}
}

func TestFindWSLProcessComposeSkipsEntriesWithoutComposeFile(t *testing.T) {
	cfg := &config.Config{
		BridgesComposeFile: "/etc/im/process-compose.bridges.yaml",
		ProcessCompose: []config.ProcessCompose{
			{Name: "no-file"},
			{Name: "wsl", ComposeFile: "/etc/im/process-compose.yaml"},
		},
	}
	pc, ok := findWSLProcessCompose(cfg)
	if !ok || pc.Name != "wsl" {
		t.Errorf("expected wsl match; got ok=%v pc=%+v", ok, pc)
	}
}

func TestApplyWSLFragmentWritesAndReloads(t *testing.T) {
	// Stand up a fake process-compose REST server that records the reload
	// hit. Reload returns 200 — the success path.
	var reloadCalls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/project/configuration":
			if r.Method != http.MethodPost {
				t.Errorf("reload: wrong method %q", r.Method)
			}
			atomic.AddInt32(&reloadCalls, 1)
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	composePath := filepath.Join(dir, "process-compose.yaml")
	fragPath := filepath.Join(dir, "process-compose.bridges.yaml")
	cfg := &config.Config{
		BridgesComposeFile: fragPath,
		ProcessCompose: []config.ProcessCompose{
			{Name: "wsl", Endpoint: srv.URL, ComposeFile: composePath},
		},
	}
	bridges := []bridge.Bridge{
		{Name: "llama-relay", Tier: bridge.TierWSL, Type: bridge.TypeSocat,
			Listen:  bridge.Endpoint{Addr: "172.17.0.1", Port: 8080},
			Connect: bridge.Endpoint{Addr: "127.0.0.1", Port: 8080, Family: bridge.FamilyAuto}},
		// Windows bridge — must be ignored by the fragment writer.
		{Name: "win-llama", Tier: bridge.TierWindows, Type: bridge.TypePortproxy,
			Listen:   bridge.Endpoint{Addr: "${wsl-host-ip}", Port: 8080},
			Connect:  bridge.Endpoint{Addr: "127.0.0.1", Port: 8080, Family: bridge.FamilyAuto},
			Firewall: bridge.Firewall{DisplayName: "x", Remote: "172.18.0.0/16"}},
	}

	applyWSLFragment(bridges, cfg)

	// Fragment file should exist.
	data, err := os.ReadFile(fragPath)
	if err != nil {
		t.Fatalf("fragment file not written: %v", err)
	}
	if !strings.Contains(string(data), "bridge-llama-relay") {
		t.Errorf("fragment missing wsl/socat process; got:\n%s", string(data))
	}
	if strings.Contains(string(data), "win-llama") {
		t.Errorf("fragment contained windows bridge name; got:\n%s", string(data))
	}

	// Reload was hit exactly once.
	if got := atomic.LoadInt32(&reloadCalls); got != 1 {
		t.Errorf("reload hit %d times, want 1", got)
	}
}

func TestApplyWSLFragmentNoMatchingPCSkipReload(t *testing.T) {
	// No process_compose entry shares a directory with the fragment — apply
	// should still write the file but skip reload (no-op, no panic).
	dir := t.TempDir()
	fragPath := filepath.Join(dir, "process-compose.bridges.yaml")
	cfg := &config.Config{
		BridgesComposeFile: fragPath,
		// PC entry lives in a different directory.
		ProcessCompose: []config.ProcessCompose{
			{Name: "windows", ComposeFile: "/somewhere/else/process-compose.yaml"},
		},
	}
	applyWSLFragment(nil, cfg)
	if _, err := os.Stat(fragPath); err != nil {
		t.Errorf("fragment should still be written when no PC matches; stat err: %v", err)
	}
}

func TestApplyWSLFragmentEmptyConfigBridgesComposeFile(t *testing.T) {
	// BridgesComposeFile unset — apply should be a no-op without error.
	cfg := &config.Config{}
	applyWSLFragment([]bridge.Bridge{
		{Name: "x", Tier: bridge.TierWSL, Type: bridge.TypeSocat,
			Listen:  bridge.Endpoint{Addr: "172.17.0.1", Port: 1},
			Connect: bridge.Endpoint{Addr: "127.0.0.1", Port: 1, Family: bridge.FamilyAuto}},
	}, cfg)
	// no assertions: we just need the call to not panic / not exit.
}

func TestApplyWSLFragmentToleratesReloadFailure(t *testing.T) {
	// Reload returns 500 — apply must NOT exit, since the fragment on disk
	// is the authoritative state.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("simulated failure"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	composePath := filepath.Join(dir, "process-compose.yaml")
	fragPath := filepath.Join(dir, "process-compose.bridges.yaml")
	cfg := &config.Config{
		BridgesComposeFile: fragPath,
		ProcessCompose: []config.ProcessCompose{
			{Name: "wsl", Endpoint: srv.URL, ComposeFile: composePath},
		},
	}
	applyWSLFragment(nil, cfg)
	// The fragment must be written even though reload errored.
	if _, err := os.Stat(fragPath); err != nil {
		t.Errorf("fragment must exist on disk after failed reload: %v", err)
	}
}

func TestNameSet(t *testing.T) {
	got := nameSet([]bridge.Bridge{{Name: "a"}, {Name: "b"}})
	if !got["a"] || !got["b"] {
		t.Errorf("nameSet: missing entries: %+v", got)
	}
	if got["c"] {
		t.Errorf("nameSet: false positive for missing key")
	}
}
