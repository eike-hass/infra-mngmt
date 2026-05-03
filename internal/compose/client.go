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

// ProcessState mirrors the process-compose process object.
type ProcessState struct {
	Name     string  `json:"name"`
	Status   string  `json:"status"` // "Running","Stopped","Launched","Starting","Error","Completed","Disabled"
	Pid      int     `json:"pid"`
	ExitCode int     `json:"exit_code"`
	Restarts int     `json:"restarts"`
	CPU      float64 `json:"cpu"`
	Mem      int64   `json:"mem"`
	Age      float64 `json:"age"` // seconds
}

// IsRunning returns true for process states that indicate active execution.
func (p ProcessState) IsRunning() bool {
	switch strings.ToLower(p.Status) {
	case "running", "launched", "starting", "restarting":
		return true
	}
	return false
}

// Client is an HTTP client for the process-compose REST API.
// All requests are done with pure stdlib — no external dependencies.
type Client struct {
	name     string
	endpoint string // e.g. "http://localhost:9998"
	token    string // bearer token; empty = no auth
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

	body, err := io.ReadAll(resp.Body)
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

// Start starts a named process.
func (c *Client) Start(ctx context.Context, process string) error {
	resp, err := c.do(ctx, http.MethodPost, "/process/"+process+"/start", nil)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// Stop stops a named process.
func (c *Client) Stop(ctx context.Context, process string) error {
	resp, err := c.do(ctx, http.MethodPost, "/process/"+process+"/stop", nil)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// Restart restarts a named process.
func (c *Client) Restart(ctx context.Context, process string) error {
	resp, err := c.do(ctx, http.MethodPost, "/process/"+process+"/restart", nil)
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

// Logs returns the last n log lines for a process.
func (c *Client) Logs(ctx context.Context, process string, lines int) ([]LogLine, error) {
	url := fmt.Sprintf("/process/%s/logs?endOffset=%d&follow=false", process, lines)
	resp, err := c.do(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// Try {"logs": [...]} first, then flat array, then treat as plain text.
	var wrapped struct {
		Logs []LogLine `json:"logs"`
	}
	if err := json.Unmarshal(body, &wrapped); err == nil && len(wrapped.Logs) > 0 {
		return wrapped.Logs, nil
	}
	var list []LogLine
	if err := json.Unmarshal(body, &list); err == nil {
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
		b, _ := io.ReadAll(resp.Body)
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
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	return req, nil
}
