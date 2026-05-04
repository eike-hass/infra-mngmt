package main

// Smoke tests for the subcommand dispatcher. The heavy logic (config parsing,
// IP resolution) is tested in its own packages; these tests cover the routing
// surface and the legacy "treat first flag as server" backwards-compat path.

import (
	"os/exec"
	"strings"
	"testing"
)

func TestHelpExits(t *testing.T) {
	// Build the binary into the test temp dir and exercise it. The binary
	// should print usage when invoked with `help` and not error.
	if testing.Short() {
		t.Skip("skipping binary-build smoke test in short mode")
	}
	dir := t.TempDir()
	bin := dir + "/infra-mngmt"
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	out, err := exec.Command(bin, "help").CombinedOutput()
	if err != nil {
		t.Fatalf("help failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "infra-mngmt") {
		t.Errorf("help output missing program name: %s", out)
	}
	if !strings.Contains(string(out), "addr") {
		t.Errorf("help output missing addr subcommand: %s", out)
	}
}

func TestUnknownSubcommandExitsTwo(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping binary-build smoke test in short mode")
	}
	dir := t.TempDir()
	bin := dir + "/infra-mngmt"
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	cmd := exec.Command(bin, "totally-not-a-subcommand")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit, got success. output: %s", out)
	}
	if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 2 {
		t.Errorf("expected exit code 2, got %v\noutput: %s", err, out)
	}
}

func TestAddrUnknownKindExitsTwo(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping binary-build smoke test in short mode")
	}
	dir := t.TempDir()
	bin := dir + "/infra-mngmt"
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	cmd := exec.Command(bin, "addr", "moon-host")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit, got success. output: %s", out)
	}
	if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 2 {
		t.Errorf("expected exit code 2, got %v\noutput: %s", err, out)
	}
}
