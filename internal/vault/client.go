// Package vault is an HTTP client for the mcp-fs control plane. The control
// plane has no auth — it relies on being reached only intra-Docker (no
// host-published port) with infra-mngmt as the only legitimate caller. This
// package gives infra-mngmt a typed surface to read and write the allowlist
// + browse the data root, instead of curling raw JSON in handlers.
package vault

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

// Client talks to one mcp-fs control plane (typically reachable as
// http://<container-name>:3002 over a shared internal Docker network).
type Client struct {
	// BaseURL is the control-plane root, e.g. "http://workspace-vault:3002".
	// Trailing slash is optional; normalized on construction.
	BaseURL string
	// HTTP is the http.Client to use; defaults to a 5s-timeout client when nil.
	HTTP *http.Client
}

// New returns a Client with sensible defaults.
func New(baseURL string) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/")}
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 5 * time.Second}
}

// Allowlist is the response shape for GET/PUT /api/allowlist.
type Allowlist struct {
	Allowed []string `json:"allowed"`
}

// GetAllowlist fetches the current allowlist.
func (c *Client) GetAllowlist(ctx context.Context) (*Allowlist, error) {
	var out Allowlist
	if err := c.do(ctx, http.MethodGet, "/api/allowlist", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PutAllowlist replaces the allowlist with `allowed`. The vault canonicalizes
// (realpath, dedup) and returns the resulting list. Use that as the source
// of truth — paths the user requested may be filtered out (non-directory,
// outside data root, etc.).
func (c *Client) PutAllowlist(ctx context.Context, allowed []string) (*Allowlist, error) {
	body := struct {
		Allowed []string `json:"allowed"`
	}{Allowed: allowed}
	var out Allowlist
	if err := c.do(ctx, http.MethodPut, "/api/allowlist", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// TreeEntry is one immediate-child directory under a tree root.
type TreeEntry struct {
	Name string `json:"name"`
	Full string `json:"full"`
}

// Tree is the response shape for GET /api/tree.
type Tree struct {
	Path    string      `json:"path"`
	Entries []TreeEntry `json:"entries"`
}

// Tree returns the immediate-child directories of `at` (pass "" for the
// data root). Useful for building the allowlist UI's tree browser.
func (c *Client) Tree(ctx context.Context, at string) (*Tree, error) {
	path := "/api/tree"
	if at != "" {
		path += "?at=" + queryEscape(at)
	}
	var out Tree
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// do performs an HTTP request and decodes JSON into out. Server-side
// {"error": "..."} payloads are turned into Go errors regardless of status
// code (the vault is a small Express app and is consistent about this).
func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	buf, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode >= 400 {
		// Try to surface the structured {"error": "..."} the vault returns.
		var errBody struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(buf, &errBody)
		if errBody.Error != "" {
			return fmt.Errorf("vault %s %s: %s (HTTP %d)", method, path, errBody.Error, resp.StatusCode)
		}
		return fmt.Errorf("vault %s %s: HTTP %d", method, path, resp.StatusCode)
	}
	if out != nil {
		if err := json.Unmarshal(buf, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// queryEscape is a tiny URL escape that handles the cases we care about
// (spaces, slashes are fine in path components but %-encoded as 2F at the
// boundary, etc.). Go's url.QueryEscape is overkill here — but if the deps
// graph already pulls net/url for other reasons, swap in.
func queryEscape(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == ' ':
			b.WriteByte('+')
		case r == '/' || r == '.' || r == '_' || r == '-' || r == '~' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		default:
			fmt.Fprintf(&b, "%%%02X", r)
		}
	}
	return b.String()
}
