package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// Compose wraps `docker compose` invocations against a single compose file.
// It deliberately shells out to the docker compose CLI rather than reaching
// into the official SDK because compose orchestration (multi-service
// dependency ordering, network creation, volume materialization) lives in
// the CLI, not in the API. Reimplementing it would be more code than the
// shell-out + JSON-parse this gives us.
//
// Project name defaults to Docker's convention (the compose file's parent
// directory name). For containers.yaml-managed stacks, callers should pass
// the container's Name as Project so the project is stable regardless of
// where the YAML file lives on disk.
type Compose struct {
	// File is the path to the compose YAML, passed as `-f <File>`.
	File string
	// Project, if non-empty, is passed as `-p <Project>`.
	Project string
	// Runner is the command runner; nil falls back to ExecRunner.
	// Tests inject a fake.
	Runner ComposeRunner
}

// ComposeRunner runs an external command and returns combined stdout+stderr.
// Implementations should respect ctx for cancellation.
type ComposeRunner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// ExecRunner runs commands via os/exec.
type ExecRunner struct{}

// Run executes name with args, returning combined output. ctx cancellation
// kills the subprocess.
func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// runner returns Runner if set, else the default exec-backed runner. Kept
// as a method so callers can construct Compose with a zero Runner field.
func (c *Compose) runner() ComposeRunner {
	if c.Runner != nil {
		return c.Runner
	}
	return ExecRunner{}
}

// args builds the leading `docker compose -f <file> [-p <project>]` portion
// of every invocation. Returned slice is fresh per call so callers can
// append safely.
func (c *Compose) args(sub string, extra ...string) []string {
	a := []string{"compose", "-f", c.File}
	if c.Project != "" {
		a = append(a, "-p", c.Project)
	}
	a = append(a, sub)
	return append(a, extra...)
}

// Up runs `docker compose up -d --no-recreate`. --no-recreate keeps existing
// containers if their config hasn't drifted; pair with Down before Up if you
// want a hard restart. Returns combined output for surfacing in the UI on
// failure.
func (c *Compose) Up(ctx context.Context) ([]byte, error) {
	out, err := c.runner().Run(ctx, "docker", c.args("up", "-d", "--no-recreate")...)
	if err != nil {
		return out, fmt.Errorf("compose up: %w (%s)", err, trimOutput(out))
	}
	return out, nil
}

// Down runs `docker compose down`. Removes containers, default network, and
// containers' anonymous volumes, but leaves named volumes intact (so
// allowlist state under a named volume survives a Down/Up cycle).
func (c *Compose) Down(ctx context.Context) ([]byte, error) {
	out, err := c.runner().Run(ctx, "docker", c.args("down")...)
	if err != nil {
		return out, fmt.Errorf("compose down: %w (%s)", err, trimOutput(out))
	}
	return out, nil
}

// PSEntry is one row from `docker compose ps --format json`.
type PSEntry struct {
	Name    string `json:"Name"`
	Image   string `json:"Image"`
	Service string `json:"Service"`
	State   string `json:"State"` // "running" | "exited" | "restarting" | ...
	Status  string `json:"Status"`
}

// PS returns the live state of services in the stack. Empty slice if the
// stack isn't running. `docker compose ps --format json` emits one JSON
// object per line; we tolerate malformed lines and skip them, but propagate
// command-level failures.
func (c *Compose) PS(ctx context.Context) ([]PSEntry, error) {
	out, err := c.runner().Run(ctx, "docker", c.args("ps", "--format", "json")...)
	if err != nil {
		return nil, fmt.Errorf("compose ps: %w (%s)", err, trimOutput(out))
	}
	return parsePS(out), nil
}

func parsePS(b []byte) []PSEntry {
	var entries []PSEntry
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var e PSEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue // skip malformed lines rather than fail the whole call
		}
		entries = append(entries, e)
	}
	return entries
}

// trimOutput cuts a long combined-output blob down to a single-line
// preview suitable for embedding in an error message. Caller should still
// log the full output separately if needed.
func trimOutput(b []byte) string {
	s := strings.TrimSpace(string(b))
	if i := strings.IndexByte(s, '\n'); i > 0 {
		return s[:i] + " …"
	}
	const max = 240
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
