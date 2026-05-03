package compose

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Bootstrapper can start and stop a process-compose instance when its
// REST endpoint is unreachable. This solves the chicken-and-egg problem:
// the app must be able to *start* the supervisor, not just query it.
type Bootstrapper struct {
	Name        string
	Binary      string // path to process-compose binary
	ComposeFile string // path to the process-compose.yaml
	Endpoint    string // expected REST endpoint after start
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

	// Extract port from endpoint for --port flag.
	port := portFromEndpoint(b.Endpoint)

	args := []string{
		"-f", b.ComposeFile,
		"--tui=false", // never hijack the terminal
	}
	if port != "" {
		args = append(args, "--port", port)
	}

	cmd := exec.CommandContext(ctx, b.Binary, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start process-compose: %w", err)
	}

	// Wait for the REST endpoint to become ready.
	client := New(b.Name, b.Endpoint, "")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if client.Ping(ctx) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("process-compose started but endpoint %s not ready after 5s", b.Endpoint)
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
