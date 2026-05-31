package compose

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// maxRespBytes caps how much of any response body we read. process-compose's
// /processes and /process/logs payloads are small JSON; the cap is a guardrail
// so a misbehaving upstream can't OOM the dashboard.
const maxRespBytes = 8 << 20 // 8 MiB

// ProcessState mirrors the process-compose process object returned by /processes.
// Field set and JSON tags match upstream src/types/process.go exactly so we can
// trust the API rather than recompute (e.g. IsRunning, SystemTime).
type ProcessState struct {
	Name           string  `json:"name"`
	Namespace      string  `json:"namespace"`
	Status         string  `json:"status"`          // Disabled|Foreground|Pending|Running|Launching|Launched|Restarting|Terminating|Completed|Skipped|Error|Scheduled
	SystemTime     string  `json:"system_time"`     // pre-formatted age, e.g. "1h2m3s" or "-"
	Health         string  `json:"is_ready"`        // "Ready" | "Not Ready" | "-"
	HasHealthProbe bool    `json:"has_ready_probe"` // true if a readiness/liveness probe is configured
	Pid            int     `json:"pid"`
	ExitCode       int     `json:"exit_code"`
	Restarts       int     `json:"restarts"`
	CPU            float64 `json:"cpu"`
	Mem            int64   `json:"mem"` // bytes
	IsRunning      bool    `json:"is_running"`
}

// Client is an HTTP client for the process-compose REST API.
// All requests are done with pure stdlib — no external dependencies.
type Client struct {
	name     string
	endpoint string // e.g. "http://localhost:9998"
	token    string // process-compose API token (X-PC-Token-Key); empty = no auth
	http     *http.Client
}

func New(name, endpoint, token string) *Client {
	return &Client{
		name:     name,
		endpoint: strings.TrimRight(endpoint, "/"),
		token:    token,
		http:     &http.Client{Timeout: 5 * time.Second},
	}
}

func (c *Client) Name() string     { return c.name }
func (c *Client) Endpoint() string { return c.endpoint }

// Ping returns true if the process-compose instance is reachable.
func (c *Client) Ping(ctx context.Context) bool {
	req, err := c.newReq(ctx, http.MethodGet, "/live", nil)
	if err != nil {
		return false
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode < 400
}

// Processes returns all processes from this instance.
func (c *Client) Processes(ctx context.Context) ([]ProcessState, error) {
	resp, err := c.do(ctx, http.MethodGet, "/processes", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxRespBytes))
	if err != nil {
		return nil, err
	}

	// process-compose returns either a flat array or {"data": [...]}
	var list []ProcessState
	if err := json.Unmarshal(body, &list); err == nil {
		return list, nil
	}
	var wrapped struct {
		Data []ProcessState `json:"data"`
	}
	if err := json.Unmarshal(body, &wrapped); err != nil {
		return nil, fmt.Errorf("parse processes: %w", err)
	}
	return wrapped.Data, nil
}

// Start starts a named process. process-compose's REST API routes process
// verbs as /process/<verb>/<name>, NOT /process/<name>/<verb>.
func (c *Client) Start(ctx context.Context, process string) error {
	resp, err := c.do(ctx, http.MethodPost, "/process/start/"+process, nil)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// Stop stops a named process and clears its restart loop. The upstream route
// is PATCH-only — using POST gets a 404 from gin's method-aware router, which
// surfaced as the "stop errors" symptom on the Windows tier.
func (c *Client) Stop(ctx context.Context, process string) error {
	resp, err := c.do(ctx, http.MethodPatch, "/process/stop/"+process, nil)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// Restart restarts a named process.
func (c *Client) Restart(ctx context.Context, process string) error {
	resp, err := c.do(ctx, http.MethodPost, "/process/restart/"+process, nil)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// Reload re-reads the compose file and reconciles the running process set
// against it: new entries are added, changed entries are restarted, and
// entries removed from the YAML are stopped + dropped. The upstream route
// is `POST /project/configuration` — there is no `/reload` suffix; sending
// to /project/configuration/reload returns 404.
//
// Caveat: behavior varies between process-compose versions. Recent versions
// (v1.41+) handle full reconciliation; older ones may only add/update and
// leave deleted entries running until a full restart.
func (c *Client) Reload(ctx context.Context) error {
	resp, err := c.do(ctx, http.MethodPost, "/project/configuration", nil)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// LogLine is a single log entry returned by process-compose.
type LogLine struct {
	Time    string `json:"time"`
	Process string `json:"process"`
	Message string `json:"message"`
}

// Logs returns the last n log lines for a process. process-compose's REST
// API uses path-based parameters: /process/logs/{name}/{endOffset}/{limit}.
// endOffset=0 means "from the start of the in-memory buffer"; combined with
// a limit equal to the buffer size this returns up to `lines` of recent log.
func (c *Client) Logs(ctx context.Context, process string, lines int) ([]LogLine, error) {
	url := fmt.Sprintf("/process/logs/%s/0/%d", process, lines)
	resp, err := c.do(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxRespBytes))
	if err != nil {
		return nil, err
	}

	// process-compose returns {"logs": ["line1", "line2", ...]} — strings,
	// not structured LogLine objects. Try that first, then the structured
	// shapes that older builds may use, then plain text.
	var stringWrapped struct {
		Logs []string `json:"logs"`
	}
	if err := json.Unmarshal(body, &stringWrapped); err == nil && len(stringWrapped.Logs) > 0 {
		out := make([]LogLine, 0, len(stringWrapped.Logs))
		for _, ln := range stringWrapped.Logs {
			ln = strings.TrimRight(ln, "\r\n")
			if ln == "" {
				continue
			}
			out = append(out, LogLine{Message: ln})
		}
		return out, nil
	}
	var wrapped struct {
		Logs []LogLine `json:"logs"`
	}
	if err := json.Unmarshal(body, &wrapped); err == nil && len(wrapped.Logs) > 0 {
		return wrapped.Logs, nil
	}
	var list []LogLine
	if err := json.Unmarshal(body, &list); err == nil && len(list) > 0 {
		return list, nil
	}
	// Fallback: plain text, one line per entry.
	var out []LogLine
	for _, ln := range strings.Split(string(body), "\n") {
		if ln = strings.TrimSpace(ln); ln != "" {
			out = append(out, LogLine{Message: ln})
		}
	}
	return out, nil
}

// ── HTTP helpers ──────────────────────────────────────────────────────────────

func (c *Client) do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	req, err := c.newReq(ctx, method, path, body)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("compose %s %s: %w", method, path, err)
	}
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, maxRespBytes))
		resp.Body.Close()
		return nil, fmt.Errorf("compose %s %s: %s", method, path, bytes.TrimSpace(b))
	}
	return resp, nil
}

func (c *Client) newReq(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	if body == nil {
		body = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("X-PC-Token-Key", c.token)
	}
	return req, nil
}
