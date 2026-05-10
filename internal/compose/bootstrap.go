package compose

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// bootstrapArgs builds the CLI args passed to process-compose at start.
// Pulled out as a pure function so it can be tested without exec.
//
// extraFiles is a list of additional `-f` paths to merge in after the primary
// compose file. process-compose merges in order: later files override earlier
// ones for scalar fields and merge for `environment`/`depends_on`. Used to
// stitch in the generated socat-bridges fragment without requiring `extends:`
// (which is unreliable across PC versions).
func bootstrapArgs(composeFile, port, tokenFile string, extraFiles []string) []string {
	args := []string{
		"-f", composeFile,
	}
	for _, ef := range extraFiles {
		if ef == "" {
			continue
		}
		args = append(args, "-f", ef)
	}
	args = append(args,
		"--tui=false", // never hijack the terminal
		// Keep the supervisor alive even when every process is in a terminal
		// state (or the YAML's processes map is empty). Without this, calling
		// stop on the last running process exits the whole supervisor as a
		// side-effect, taking the REST API with it.
		"--keep-project",
	)
	if port != "" {
		args = append(args, "--port", port)
	}
	if tokenFile != "" {
		args = append(args, "--token-file", tokenFile)
	}
	return args
}

// Bootstrapper can start and stop a process-compose instance when its
// REST endpoint is unreachable. This solves the chicken-and-egg problem:
// the app must be able to *start* the supervisor, not just query it.
type Bootstrapper struct {
	Name        string
	Binary      string // path to process-compose binary
	ComposeFile string // path to the process-compose.yaml
	// ExtraFiles are additional compose files merged into ComposeFile via the
	// `-f file1 -f file2` form. Used for the generated bridges fragment so
	// the user's main YAML stays unmodified.
	ExtraFiles []string
	Endpoint   string // expected REST endpoint after start
	TokenFile  string // path to file containing the API token; passed via --token-file when set
}

// Start launches process-compose in the background and waits up to 5 seconds
// for it to become reachable. Returns an error if it fails to start or
// does not respond in time.
func (b *Bootstrapper) Start(ctx context.Context) error {
	if _, err := os.Stat(b.Binary); err != nil {
		return fmt.Errorf("process-compose binary not found at %q: %w", b.Binary, err)
	}
	if _, err := os.Stat(b.ComposeFile); err != nil {
		return fmt.Errorf("compose file not found at %q: %w", b.ComposeFile, err)
	}

	// Read the token now so the post-start Ping can authenticate.
	// A missing token file is non-fatal — process-compose itself will fail
	// fast with a clearer error than we could synthesize.
	var token string
	if b.TokenFile != "" {
		if data, err := os.ReadFile(b.TokenFile); err == nil {
			token = strings.TrimSpace(string(data))
		}
	}

	// When the target is a Windows binary launched via WSL interop, the
	// args must use Windows-style paths — process-compose.exe doesn't
	// understand /c/Users/... or /mnt/c/Users/.... wslpath translates them.
	composePath := b.ComposeFile
	tokenPath := b.TokenFile
	extraPaths := append([]string(nil), b.ExtraFiles...)
	if isWindowsBinary(b.Binary) {
		if wp, err := wslpathToWindows(b.ComposeFile); err == nil {
			composePath = wp
		}
		if b.TokenFile != "" {
			if wp, err := wslpathToWindows(b.TokenFile); err == nil {
				tokenPath = wp
			}
		}
		for i, ep := range extraPaths {
			if wp, err := wslpathToWindows(ep); err == nil {
				extraPaths[i] = wp
			}
		}
	}
	// Drop extras that don't exist on disk — process-compose errors out on
	// missing -f targets, which would block bootstrap of the unrelated
	// primary compose file.
	extraPaths = filterExisting(extraPaths)

	args := bootstrapArgs(composePath, portFromEndpoint(b.Endpoint), tokenPath, extraPaths)

	cmd, err := buildSpawnCmd(ctx, b.Binary, args)
	if err != nil {
		return err
	}
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start process-compose: %w", err)
	}
	// Once the child is launched we don't care about the cmd handle — the
	// process is already in its own session and will outlive this goroutine.
	// Reap the zombie in the background so it doesn't sit in the Z state if
	// it exits before the next bootstrap call.
	go func() { _ = cmd.Wait() }()

	// Wait for the REST endpoint to become ready.
	client := New(b.Name, b.Endpoint, token)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if client.Ping(ctx) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("process-compose started but endpoint %s not ready after 5s", b.Endpoint)
}

// buildSpawnCmd returns an *exec.Cmd configured to launch process-compose in
// a way that survives this HTTP request's lifecycle.
//
// The Windows path wraps the binary in PowerShell's `Start-Process -WindowStyle
// Hidden`, which detaches the child fully from our caller. We can keep the
// outer PowerShell tied to ctx — its only job is to fire-and-return.
//
// The Linux path is the trap: `exec.CommandContext(ctx, …)` ties the spawned
// child's lifetime to ctx via Cmd.Cancel, so when the HTTP handler returns
// (and ctx is canceled) Go's runtime SIGKILLs process-compose. Use plain
// `exec.Command` (no ctx) and `Setsid: true` to put the child in a fresh
// session — the supervisor outlives our request and is independent of our
// process group's signals.
func buildSpawnCmd(ctx context.Context, binary string, args []string) (*exec.Cmd, error) {
	if isWindowsBinary(binary) {
		winBinary, err := wslpathToWindows(binary)
		if err != nil {
			return nil, fmt.Errorf("translate binary path: %w", err)
		}
		quoted := make([]string, 0, len(args))
		for _, a := range args {
			quoted = append(quoted, "'"+strings.ReplaceAll(a, "'", "''")+"'")
		}
		ps := fmt.Sprintf("Start-Process -FilePath '%s' -ArgumentList %s -WindowStyle Hidden",
			strings.ReplaceAll(winBinary, "'", "''"), strings.Join(quoted, ","))
		psPath, err := resolvePowerShell()
		if err != nil {
			return nil, err
		}
		return exec.CommandContext(ctx, psPath, "-NoProfile", "-Command", ps), nil
	}
	cmd := exec.Command(binary, args...)
	detachLinuxCmd(cmd)
	return cmd, nil
}

// isWindowsBinary tells whether the binary path points at a Windows
// executable runnable via WSL interop. We detect by extension since the
// bootstrap path on Windows always ends in `.exe`.
func isWindowsBinary(path string) bool {
	return strings.HasSuffix(strings.ToLower(path), ".exe")
}

// resolvePowerShell finds powershell.exe robustly under systemd-launched
// processes where WSL's automatic Windows-PATH injection isn't applied.
// Mirrors the same lookup used in internal/bridge.
func resolvePowerShell() (string, error) {
	if path, err := exec.LookPath("powershell.exe"); err == nil {
		return path, nil
	}
	for _, p := range []string{
		"/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe",
		"/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe",
	} {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("powershell.exe not found in $PATH or known WSL paths")
}

// wslpathToWindows converts a WSL-side path (e.g. `/c/Users/foo`) into the
// Windows form (`C:\Users\foo`) so it can be passed as an argument to a
// Windows binary launched via interop. Falls back to the original path if
// `wslpath` is unavailable (caller decides whether to surface the error).
func wslpathToWindows(p string) (string, error) {
	out, err := exec.Command("wslpath", "-w", p).Output()
	if err != nil {
		return p, err
	}
	return strings.TrimSpace(string(out)), nil
}

// filterExisting returns the subset of paths that exist on disk. Used to
// drop optional `-f` extras that haven't been materialized yet (e.g. the
// bridges fragment before the first `bridges apply`).
func filterExisting(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	out := paths[:0]
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// portFromEndpoint extracts the port number from a URL like "http://localhost:9998".
func portFromEndpoint(endpoint string) string {
	// Strip scheme.
	s := endpoint
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	// Find last colon.
	if i := strings.LastIndex(s, ":"); i >= 0 {
		return s[i+1:]
	}
	return ""
}
