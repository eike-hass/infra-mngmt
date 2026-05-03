package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefault(t *testing.T) {
	cfg := Default()
	if cfg.Bind != "127.0.0.1:7842" {
		t.Errorf("Default Bind = %q, want 127.0.0.1:7842", cfg.Bind)
	}
	if !strings.HasSuffix(cfg.TokenFile, "infra-mngmt/token") {
		t.Errorf("Default TokenFile = %q, want suffix infra-mngmt/token", cfg.TokenFile)
	}
}

func TestLoadMissingReturnsDefault(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(filepath.Join(dir, "does-not-exist.json"))
	if err != nil {
		t.Fatalf("Load nonexistent: unexpected error %v", err)
	}
	if cfg.Bind != "127.0.0.1:7842" {
		t.Errorf("expected default Bind, got %q", cfg.Bind)
	}
}

func TestLoadSaveRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	in := &Config{
		Bind:      "0.0.0.0:8080",
		TokenFile: "/etc/token",
		ProcessCompose: []ProcessCompose{
			{Name: "wsl", Endpoint: "http://localhost:9998", Binary: "/usr/bin/pc", ComposeFile: "/etc/pc.yaml", Token: "abc"},
			{Name: "windows", Endpoint: "http://wsl-windows:9999", TokenFile: "/etc/pc.token"},
		},
		ExtraPaths: []string{"/home/u/projA"},
	}
	if err := Save(path, in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	out, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if out.Bind != in.Bind || out.TokenFile != in.TokenFile {
		t.Errorf("scalar fields not preserved: got %+v", out)
	}
	if len(out.ProcessCompose) != 2 {
		t.Fatalf("expected 2 ProcessCompose, got %d", len(out.ProcessCompose))
	}
	if out.ProcessCompose[0].Token != "abc" {
		t.Errorf("token not preserved: %q", out.ProcessCompose[0].Token)
	}
	if out.ProcessCompose[1].TokenFile != "/etc/pc.token" {
		t.Errorf("token_file not preserved: %q", out.ProcessCompose[1].TokenFile)
	}
	if len(out.ExtraPaths) != 1 || out.ExtraPaths[0] != "/home/u/projA" {
		t.Errorf("ExtraPaths not preserved: %+v", out.ExtraPaths)
	}
}

func TestLoadInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestLoadOrCreateTokenEmpty(t *testing.T) {
	tok, err := LoadOrCreateToken("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok != "" {
		t.Errorf("expected empty token, got %q", tok)
	}
}

func TestLoadOrCreateTokenGenerates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "token") // dir doesn't exist yet
	tok, err := LoadOrCreateToken(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tok) != 64 { // 32 bytes hex-encoded = 64 chars
		t.Errorf("expected 64-char token, got %d (%q)", len(tok), tok)
	}
	// File should exist with 0600 perms
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("token file not created: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("token file perms = %v, want 0600", info.Mode().Perm())
	}

	// Second call should read same token
	tok2, err := LoadOrCreateToken(path)
	if err != nil {
		t.Fatal(err)
	}
	if tok2 != tok {
		t.Errorf("second call returned different token: %q vs %q", tok, tok2)
	}
}

func TestLoadOrCreateTokenTrimsWhitespace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token")
	if err := os.WriteFile(path, []byte("  hello-token\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tok, err := LoadOrCreateToken(path)
	if err != nil {
		t.Fatal(err)
	}
	if tok != "hello-token" {
		t.Errorf("expected trimmed token, got %q", tok)
	}
}

func TestResolveEndpointPassThrough(t *testing.T) {
	cases := []string{
		"http://localhost:9998",
		"http://10.0.0.1:8080",
		"https://example.com",
		"",
		"not-a-url",
	}
	for _, in := range cases {
		if got := ResolveEndpoint(in); got != in {
			t.Errorf("ResolveEndpoint(%q) = %q, want unchanged", in, got)
		}
	}
}

func TestResolveEndpointSubstitutesWSL(t *testing.T) {
	// Can only verify substitution if we're actually in WSL2 (resolv.conf has nameserver).
	// In CI / containers without WSL, WSLWindowsHostIP() returns "" and the input
	// should be returned unchanged.
	in := "http://wsl-windows:9999"
	got := ResolveEndpoint(in)
	if ip := WSLWindowsHostIP(); ip != "" {
		want := "http://" + ip + ":9999"
		if got != want {
			t.Errorf("ResolveEndpoint(%q) = %q, want %q", in, got, want)
		}
	} else {
		if got != in {
			t.Errorf("no WSL host IP available; expected pass-through, got %q", got)
		}
	}
}
