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

	args := bootstrapArgs(b.ComposeFile, portFromEndpoint(b.Endpoint), b.TokenFile)

	cmd := exec.CommandContext(ctx, b.Binary, args...)
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
