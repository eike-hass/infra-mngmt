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
func bootstrapArgs(composeFile, port, tokenFile string) []string {
	args := []string{
		"-f", composeFile,
		"--tui=false", // never hijack the terminal
		// Keep the supervisor alive even when every process is in a terminal
		// state (or the YAML's processes map is empty). Without this, calling
		// stop on the last running process exits the whole supervisor as a
		// side-effect, taking the REST API with it.
		"--keep-project",
	}
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
	Endpoint    string // expected REST endpoint after start
	TokenFile   string // path to file containing the API token; passed via --token-file when set
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
	if isWindowsBinary(b.Binary) {
		if wp, err := wslpathToWindows(b.ComposeFile); err == nil {
			composePath = wp
		}
		if b.TokenFile != "" {
			if wp, err := wslpathToWindows(b.TokenFile); err == nil {
				tokenPath = wp
			}
		}
	}

	args := bootstrapArgs(composePath, portFromEndpoint(b.Endpoint), tokenPath)

	var cmd *exec.Cmd
	if isWindowsBinary(b.Binary) {
		// When launching a Windows .exe from WSL, the child inherits the
		// caller's process group and dies when the HTTP request handler
		// returns. Wrap with PowerShell's Start-Process to fully detach,
		// matching what autostart.ps1 does for Task-Scheduler-launched runs.
		winBinary, err := wslpathToWindows(b.Binary)
		if err != nil {
			return fmt.Errorf("translate binary path: %w", err)
		}
		quoted := make([]string, 0, len(args))
		for _, a := range args {
			quoted = append(quoted, "'"+strings.ReplaceAll(a, "'", "''")+"'")
		}
		ps := fmt.Sprintf("Start-Process -FilePath '%s' -ArgumentList %s -WindowStyle Hidden",
			strings.ReplaceAll(winBinary, "'", "''"), strings.Join(quoted, ","))
		psPath, err := resolvePowerShell()
		if err != nil {
			return err
		}
		cmd = exec.CommandContext(ctx, psPath, "-NoProfile", "-Command", ps)
	} else {
		cmd = exec.CommandContext(ctx, b.Binary, args...)
	}
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start process-compose: %w", err)
	}

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
