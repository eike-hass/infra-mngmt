package docker

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
)

const (
	dockerSock = "/var/run/docker.sock"
	apiBase    = "http://localhost/v1.41"
)

// Client talks to the Docker daemon via its Unix socket REST API.
// No external dependencies — pure stdlib.
type Client struct {
	http *http.Client
}

func New() (*Client, error) {
	c := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", dockerSock)
			},
		},
	}
	return &Client{http: c}, nil
}

func (c *Client) Close() error { return nil }

// ── container list types ──────────────────────────────────────────────────────

type containerJSON struct {
	ID     string            `json:"Id"`
	Names  []string          `json:"Names"`
	Labels map[string]string `json:"Labels"`
	Mounts []mountJSON       `json:"Mounts"`
	State  string            `json:"State"`
}

type mountJSON struct {
	Type        string `json:"Type"`
	Name        string `json:"Name"`   // volume name (when Type=="volume")
	Source      string `json:"Source"` // host path (when Type=="bind")
	Destination string `json:"Destination"`
}

// ── public types ──────────────────────────────────────────────────────────────

// ManagedContainer describes a container discovered by infra-mngmt, either
// via standard devcontainer labels or the explicit claude.managed=true label.
type ManagedContainer struct {
	ID          string
	Name        string
	Labels      map[string]string
	ConfigMount *ClaudeMount
	ProjectRoot string
	State       string // "running", "exited", "paused", etc.
}

// ClaudeMount describes how the .claude config directory is exposed in a container.
type ClaudeMount struct {
	IsVolume   bool
	VolumeName string
	HostPath   string
}

// ContainerState is the subset of Docker inspect State that infra-mngmt cares about.
type ContainerState struct {
	Status  string           `json:"Status"`
	Running bool             `json:"Running"`
	Health  *ContainerHealth `json:"Health"`
}

// ContainerHealth holds the result of a Docker health check (nil if none configured).
type ContainerHealth struct {
	Status string `json:"Status"` // "starting" | "healthy" | "unhealthy"
}

// InspectContainer returns the runtime state of a container, including health.
func (c *Client) InspectContainer(ctx context.Context, id string) (ContainerState, error) {
	resp, err := c.get(ctx, apiBase+"/containers/"+id+"/json")
	if err != nil {
		return ContainerState{}, err
	}
	defer resp.Body.Close()
	var result struct {
		State ContainerState `json:"State"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return ContainerState{}, fmt.Errorf("inspect container: %w", err)
	}
	return result.State, nil
}

// StreamLogs streams stdout+stderr from a container frame-by-frame.
// The returned channel receives trimmed log lines; it is closed when the
// container exits or ctx is cancelled.
func (c *Client) StreamLogs(ctx context.Context, id string) <-chan string {
	ch := make(chan string, 64)
	go func() {
		defer close(ch)
		u := apiBase + "/containers/" + id + "/logs?follow=true&stdout=true&stderr=true&tail=200"
		resp, err := c.get(ctx, u)
		if err != nil {
			return
		}
		defer resp.Body.Close()
		hdr := make([]byte, 8)
		for {
			if _, err := io.ReadFull(resp.Body, hdr); err != nil {
				return
			}
			size := binary.BigEndian.Uint32(hdr[4:8])
			if size == 0 {
				continue
			}
			data := make([]byte, size)
			if _, err := io.ReadFull(resp.Body, data); err != nil {
				return
			}
			line := strings.TrimRight(string(data), "\r\n")
			select {
			case ch <- line:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch
}

// ListManaged returns containers that infra-mngmt should read config from.
// It merges two discovery strategies:
//  1. Standard devcontainer label (devcontainer.local_folder) — set automatically
//     by VS Code and the devcontainer CLI; no user configuration required.
//  2. Explicit opt-in label (claude.managed=true) — for containers not created
//     by VS Code (plain docker run, etc.).
func (c *Client) ListManaged(ctx context.Context) ([]ManagedContainer, error) {
	seen := map[string]bool{}
	var out []ManagedContainer

	// Strategy 1: standard devcontainer label (auto-discovery, no labels needed).
	// Query both the canonical spelling and the legacy typo variant emitted by
	// older VS Code releases.
	for _, labelKey := range []string{LabelDevcontainerLocalFolder, LabelDevcontainerLocalFolderLegacy} {
		ctrs, err := c.queryContainers(ctx, labelKey)
		if err != nil {
			continue
		}
		for _, ctr := range ctrs {
			if seen[ctr.ID] {
				continue
			}
			mc := c.toManaged(ctr)
			if mc.ProjectRoot == "" {
				for _, k := range []string{LabelDevcontainerLocalFolder, LabelDevcontainerLocalFolderLegacy} {
					if raw := ctr.Labels[k]; raw != "" {
						mc.ProjectRoot = normalizeDevcontainerPath(raw)
						break
					}
				}
			}
			seen[ctr.ID] = true
			out = append(out, mc)
		}
	}

	// Strategy 2: explicit opt-in label.
	if ctrs, err := c.queryContainers(ctx, LabelManaged+"=true"); err == nil {
		for _, ctr := range ctrs {
			if seen[ctr.ID] {
				continue
			}
			seen[ctr.ID] = true
			out = append(out, c.toManaged(ctr))
		}
	}

	return out, nil
}

// queryContainers fetches containers matching a single label filter expression
// (e.g. "somekey" for presence, "somekey=value" for equality).
func (c *Client) queryContainers(ctx context.Context, labelExpr string) ([]containerJSON, error) {
	filters := `{"label":["` + labelExpr + `"]}`
	u := apiBase + "/containers/json?all=true&filters=" + url.QueryEscape(filters)
	resp, err := c.get(ctx, u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var ctrs []containerJSON
	if err := json.NewDecoder(resp.Body).Decode(&ctrs); err != nil {
		return nil, fmt.Errorf("decode container list: %w", err)
	}
	return ctrs, nil
}

// toManaged converts a raw containerJSON to a ManagedContainer by inspecting
// mounts for a .claude directory and falling back to label-based overrides.
func (c *Client) toManaged(ctr containerJSON) ManagedContainer {
	mc := ManagedContainer{
		ID:     ctr.ID,
		Labels: ctr.Labels,
		State:  ctr.State,
	}
	if len(ctr.Names) > 0 {
		mc.Name = strings.TrimPrefix(ctr.Names[0], "/")
	}
	// Explicit project root label takes precedence.
	if p := ctr.Labels[LabelProjectRoot]; p != "" {
		mc.ProjectRoot = p
	}
	// Find the .claude mount.
	for _, m := range ctr.Mounts {
		if !isClaudeMount(m.Destination) {
			continue
		}
		switch m.Type {
		case "volume":
			mc.ConfigMount = &ClaudeMount{IsVolume: true, VolumeName: m.Name}
		case "bind":
			mc.ConfigMount = &ClaudeMount{IsVolume: false, HostPath: m.Source}
			if mc.ProjectRoot == "" {
				mc.ProjectRoot = filepath.Dir(m.Source)
			}
		}
		break
	}
	// Fallback: explicit config-volume label.
	if mc.ConfigMount == nil {
		if vol := ctr.Labels[LabelConfigVol]; vol != "" {
			mc.ConfigMount = &ClaudeMount{IsVolume: true, VolumeName: vol}
		}
	}
	return mc
}

func isClaudeMount(dst string) bool {
	return strings.HasSuffix(dst, "/.claude") || dst == "/root/.claude"
}

// normalizeDevcontainerPath converts Windows UNC paths written by VS Code on
// Windows (e.g. "\\wsl.localhost\Ubuntu-18.04\home\user\project") to the
// corresponding WSL2 POSIX path ("/home/user/project"). Other path formats
// are returned unchanged.
func normalizeDevcontainerPath(p string) string {
	// Strip leading \\ or //
	trimmed := strings.TrimLeft(p, `\/`)
	// Expect "wsl.localhost\<distro>\..." or "wsl$\<distro>\..."
	lower := strings.ToLower(trimmed)
	if !strings.HasPrefix(lower, "wsl.localhost") && !strings.HasPrefix(lower, "wsl$") {
		return p
	}
	// Drop the UNC host ("wsl.localhost") and distro name, keep the rest.
	parts := strings.SplitN(strings.ReplaceAll(trimmed, `\`, "/"), "/", 3)
	if len(parts) < 3 {
		return p
	}
	return "/" + parts[2]
}

// StartContainer starts a stopped managed container.
func (c *Client) StartContainer(ctx context.Context, id string) error {
	return c.startContainer(ctx, id)
}

// StopContainer stops a running container, waiting up to 10 s before forcing.
func (c *Client) StopContainer(ctx context.Context, id string) error {
	resp, err := c.post(ctx, apiBase+"/containers/"+id+"/stop?t=10", "application/json", nil)
	if err != nil {
		return fmt.Errorf("stop container: %w", err)
	}
	resp.Body.Close()
	return nil
}

// ── volume access via sidecar containers ─────────────────────────────────────

// ReadVolume reads a file from a named Docker volume via a throwaway sidecar.
// path is relative to the volume root.
func (c *Client) ReadVolume(ctx context.Context, volumeName, path string) ([]byte, error) {
	return c.runSidecar(ctx, volumeName, true, "cat", "/data/"+path)
}

// ListVolume returns `find /data -type f` output from inside the volume.
func (c *Client) ListVolume(ctx context.Context, volumeName string) ([]byte, error) {
	return c.runSidecar(ctx, volumeName, true, "find", "/data", "-type", "f")
}

// WriteVolume is not yet implemented for the read-only MVP.
// It is stubbed to satisfy the interface.
func (c *Client) WriteVolume(_ context.Context, _, _ string, _ []byte) error {
	return fmt.Errorf("WriteVolume: not implemented")
}

// ── sidecar internals ─────────────────────────────────────────────────────────

func (c *Client) runSidecar(ctx context.Context, volumeName string, readOnly bool, cmd ...string) ([]byte, error) {
	cid, err := c.createSidecar(ctx, volumeName, readOnly, cmd)
	if err != nil {
		return nil, err
	}
	defer c.removeContainer(ctx, cid)

	if err := c.startContainer(ctx, cid); err != nil {
		return nil, err
	}
	if err := c.waitContainer(ctx, cid); err != nil {
		return nil, err
	}
	return c.containerLogs(ctx, cid)
}

type createContainerBody struct {
	Image      string     `json:"Image"`
	Cmd        []string   `json:"Cmd"`
	HostConfig hostConfig `json:"HostConfig"`
}

type hostConfig struct {
	Binds []string `json:"Binds"`
}

type createContainerResponse struct {
	ID string `json:"Id"`
}

func (c *Client) createSidecar(ctx context.Context, volumeName string, readOnly bool, cmd []string) (string, error) {
	c.ensureBusybox(ctx)

	bind := volumeName + ":/data"
	if readOnly {
		bind += ":ro"
	}
	body := createContainerBody{
		Image:      "busybox:latest",
		Cmd:        cmd,
		HostConfig: hostConfig{Binds: []string{bind}},
	}
	data, _ := json.Marshal(body)

	resp, err := c.post(ctx, apiBase+"/containers/create", "application/json", bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("create container: %w", err)
	}
	defer resp.Body.Close()

	var cr createContainerResponse
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return "", fmt.Errorf("decode create response: %w", err)
	}
	return cr.ID, nil
}

func (c *Client) startContainer(ctx context.Context, id string) error {
	resp, err := c.post(ctx, apiBase+"/containers/"+id+"/start", "application/json", nil)
	if err != nil {
		return fmt.Errorf("start container: %w", err)
	}
	resp.Body.Close()
	return nil
}

func (c *Client) waitContainer(ctx context.Context, id string) error {
	resp, err := c.post(ctx, apiBase+"/containers/"+id+"/wait", "application/json", nil)
	if err != nil {
		return fmt.Errorf("wait container: %w", err)
	}
	defer resp.Body.Close()
	var result struct {
		StatusCode int `json:"StatusCode"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil // best-effort; ignore decode errors
	}
	if result.StatusCode != 0 {
		return fmt.Errorf("container exited with status %d", result.StatusCode)
	}
	return nil
}

// containerLogs reads stdout from a finished container, stripping the
// Docker multiplexed-stream 8-byte frame headers.
func (c *Client) containerLogs(ctx context.Context, id string) ([]byte, error) {
	resp, err := c.get(ctx, apiBase+"/containers/"+id+"/logs?stdout=true")
	if err != nil {
		return nil, fmt.Errorf("container logs: %w", err)
	}
	defer resp.Body.Close()
	return stripDockerHeaders(resp.Body)
}

// stripDockerHeaders parses the Docker multiplexed stream format:
// [stream-type:1][pad:3][size:4][data:size] repeated.
func stripDockerHeaders(r io.Reader) ([]byte, error) {
	var out bytes.Buffer
	hdr := make([]byte, 8)
	for {
		if _, err := io.ReadFull(r, hdr); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				break
			}
			return nil, err
		}
		size := binary.BigEndian.Uint32(hdr[4:8])
		if _, err := io.CopyN(&out, r, int64(size)); err != nil {
			return nil, err
		}
	}
	return out.Bytes(), nil
}

func (c *Client) removeContainer(ctx context.Context, id string) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodDelete,
		apiBase+"/containers/"+id+"?force=true", nil)
	resp, err := c.http.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}

// ensureBusybox pulls busybox:latest if not already present. Errors are ignored
// because createSidecar will fail with a clearer error if the image is missing.
func (c *Client) ensureBusybox(ctx context.Context) {
	resp, err := c.post(ctx,
		apiBase+"/images/create?fromImage=busybox&tag=latest",
		"application/json", nil)
	if err == nil {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}

// ── HTTP helpers ──────────────────────────────────────────────────────────────

func (c *Client) get(ctx context.Context, u string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("docker API %s: %s", u, bytes.TrimSpace(body))
	}
	return resp, nil
}

func (c *Client) post(ctx context.Context, u, contentType string, body io.Reader) (*http.Response, error) {
	if body == nil {
		body = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("docker API %s: %s", u, bytes.TrimSpace(b))
	}
	return resp, nil
}
