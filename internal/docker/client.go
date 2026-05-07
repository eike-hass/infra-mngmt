package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

// Client wraps the official Docker SDK with infra-mngmt-specific helpers
// (managed-container discovery, sidecar volume IO, log demux into a channel).
// Callers should not depend on the underlying SDK type directly — that lets us
// swap implementations later if needed.
type Client struct {
	cli *client.Client
}

func New() (*Client, error) {
	cli, err := client.NewClientWithOpts(
		client.FromEnv,
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}
	return &Client{cli: cli}, nil
}

func (c *Client) Close() error {
	if c == nil || c.cli == nil {
		return nil
	}
	return c.cli.Close()
}

// Ping verifies the Docker daemon is reachable. The SDK already retries the
// API-version probe internally, so a single Ping is enough for a UI health LED.
func (c *Client) Ping(ctx context.Context) error {
	if c == nil || c.cli == nil {
		return fmt.Errorf("docker client: not initialized")
	}
	if _, err := c.cli.Ping(ctx); err != nil {
		return fmt.Errorf("docker ping: %w", err)
	}
	return nil
}

// DaemonHost returns the resolved host of the Docker daemon (e.g.
// "unix:///var/run/docker.sock" or "tcp://10.0.0.5:2375"). Empty string when
// no client is configured.
func (c *Client) DaemonHost() string {
	if c == nil || c.cli == nil {
		return ""
	}
	return c.cli.DaemonHost()
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

// ── managed-container discovery ──────────────────────────────────────────────

// ListManaged returns containers that infra-mngmt should read config from.
// Strategy 1: standard devcontainer label (devcontainer.local_folder).
// Strategy 2: explicit opt-in label claude.managed=true.
func (c *Client) ListManaged(ctx context.Context) ([]ManagedContainer, error) {
	seen := map[string]bool{}
	var out []ManagedContainer

	// Strategy 1: standard devcontainer labels (canonical + legacy typo variant)
	for _, labelKey := range []string{LabelDevcontainerLocalFolder, LabelDevcontainerLocalFolderLegacy} {
		ctrs, err := c.listByLabel(ctx, labelKey, "")
		if err != nil {
			continue
		}
		for _, ctr := range ctrs {
			if seen[ctr.ID] {
				continue
			}
			mc := toManaged(ctr)
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

	// Strategy 2: explicit opt-in label
	if ctrs, err := c.listByLabel(ctx, LabelManaged, "true"); err == nil {
		for _, ctr := range ctrs {
			if seen[ctr.ID] {
				continue
			}
			seen[ctr.ID] = true
			out = append(out, toManaged(ctr))
		}
	}

	return out, nil
}

// listByLabel runs ContainerList with one label filter (presence-only when value is "").
func (c *Client) listByLabel(ctx context.Context, key, value string) ([]types.Container, error) {
	expr := key
	if value != "" {
		expr = key + "=" + value
	}
	return c.cli.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: filters.NewArgs(filters.KeyValuePair{Key: "label", Value: expr}),
	})
}

// toManaged converts an SDK Container summary into our ManagedContainer.
func toManaged(ctr types.Container) ManagedContainer {
	mc := ManagedContainer{
		ID:     ctr.ID,
		Labels: ctr.Labels,
		State:  ctr.State,
	}
	if len(ctr.Names) > 0 {
		mc.Name = strings.TrimPrefix(ctr.Names[0], "/")
	}
	// Explicit project-root label takes precedence.
	if p := ctr.Labels[LabelProjectRoot]; p != "" {
		mc.ProjectRoot = p
	}
	for _, m := range ctr.Mounts {
		if !isClaudeMount(m.Destination) {
			continue
		}
		switch m.Type {
		case mount.TypeVolume:
			mc.ConfigMount = &ClaudeMount{IsVolume: true, VolumeName: m.Name}
		case mount.TypeBind:
			mc.ConfigMount = &ClaudeMount{IsVolume: false, HostPath: m.Source}
			if mc.ProjectRoot == "" {
				mc.ProjectRoot = filepath.Dir(m.Source)
			}
		}
		break
	}
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
// Windows (e.g. "\\wsl.localhost\Ubuntu\home\user\project") to the
// corresponding WSL2 POSIX path ("/home/user/project").
func normalizeDevcontainerPath(p string) string {
	trimmed := strings.TrimLeft(p, `\/`)
	lower := strings.ToLower(trimmed)
	if !strings.HasPrefix(lower, "wsl.localhost") && !strings.HasPrefix(lower, "wsl$") {
		return p
	}
	parts := strings.SplitN(strings.ReplaceAll(trimmed, `\`, "/"), "/", 3)
	if len(parts) < 3 {
		return p
	}
	return "/" + parts[2]
}

// ── container lifecycle ──────────────────────────────────────────────────────

// FindByName returns the first container whose name (case-insensitive) matches
// the argument. Includes stopped containers. Returns ok=false if nothing
// matches. Used for the dependencies-resolver `container:<name>` lookup.
func (c *Client) FindByName(ctx context.Context, name string) (ManagedContainer, bool, error) {
	ctrs, err := c.cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return ManagedContainer{}, false, fmt.Errorf("list containers: %w", err)
	}
	want := strings.ToLower(name)
	for _, ctr := range ctrs {
		for _, n := range ctr.Names {
			if strings.EqualFold(strings.TrimPrefix(n, "/"), name) ||
				strings.ToLower(strings.TrimPrefix(n, "/")) == want {
				return toManaged(ctr), true, nil
			}
		}
	}
	return ManagedContainer{}, false, nil
}

// InspectContainer returns the runtime state of a container, including health.
func (c *Client) InspectContainer(ctx context.Context, id string) (ContainerState, error) {
	insp, err := c.cli.ContainerInspect(ctx, id)
	if err != nil {
		return ContainerState{}, fmt.Errorf("inspect container: %w", err)
	}
	cs := ContainerState{
		Status:  insp.State.Status,
		Running: insp.State.Running,
	}
	if insp.State.Health != nil {
		cs.Health = &ContainerHealth{Status: insp.State.Health.Status}
	}
	return cs, nil
}

// StartContainer starts a stopped managed container.
func (c *Client) StartContainer(ctx context.Context, id string) error {
	if err := c.cli.ContainerStart(ctx, id, container.StartOptions{}); err != nil {
		return fmt.Errorf("start container: %w", err)
	}
	return nil
}

// StopContainer stops a running container, waiting up to 10 s before forcing.
func (c *Client) StopContainer(ctx context.Context, id string) error {
	timeout := 10
	if err := c.cli.ContainerStop(ctx, id, container.StopOptions{Timeout: &timeout}); err != nil {
		return fmt.Errorf("stop container: %w", err)
	}
	return nil
}

// ContainerStats is a one-shot CPU%/memory snapshot for a running container.
// CPU is the percentage of all cores (e.g. 150.0 = 1.5 cores). Mem is the
// RSS-equivalent in bytes — on cgroup v1 we subtract the page-cache term, on
// cgroup v2 we use Usage directly. Returns zero values if the daemon hasn't
// produced a usable delta yet (newly started containers).
type ContainerStats struct {
	CPU float64
	Mem int64
}

// Stats requests a single primed stat from the daemon. The daemon waits ~1 s
// internally to populate precpu_stats so we get a real CPU delta in one call.
// Latency is therefore ~1 s — call this in parallel goroutines, not serially,
// when fanning out across containers.
func (c *Client) Stats(ctx context.Context, id string) (ContainerStats, error) {
	resp, err := c.cli.ContainerStats(ctx, id, false)
	if err != nil {
		return ContainerStats{}, fmt.Errorf("container stats: %w", err)
	}
	defer resp.Body.Close()
	var s container.StatsResponse
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return ContainerStats{}, fmt.Errorf("decode stats: %w", err)
	}
	return ContainerStats{CPU: cpuPercent(&s), Mem: memUsage(&s)}, nil
}

// cpuPercent computes the docker-stats-equivalent CPU% from a primed stats
// snapshot. Returns 0 when the delta isn't usable (e.g. no SystemUsage on
// Windows or the precpu fields are still zero).
func cpuPercent(s *container.StatsResponse) float64 {
	cpuDelta := float64(s.CPUStats.CPUUsage.TotalUsage) - float64(s.PreCPUStats.CPUUsage.TotalUsage)
	sysDelta := float64(s.CPUStats.SystemUsage) - float64(s.PreCPUStats.SystemUsage)
	if cpuDelta <= 0 || sysDelta <= 0 {
		return 0
	}
	cores := float64(s.CPUStats.OnlineCPUs)
	if cores == 0 {
		cores = float64(len(s.CPUStats.CPUUsage.PercpuUsage))
	}
	if cores == 0 {
		cores = 1
	}
	return (cpuDelta / sysDelta) * cores * 100
}

// memUsage returns the container's RSS-equivalent memory in bytes. cgroup v1
// reports a "cache" entry that's safe to subtract; cgroup v2 uses "file"
// instead. If neither is present (Windows, or older daemons), Usage is
// returned as-is.
func memUsage(s *container.StatsResponse) int64 {
	usage := int64(s.MemoryStats.Usage)
	if v, ok := s.MemoryStats.Stats["cache"]; ok {
		usage -= int64(v)
	} else if v, ok := s.MemoryStats.Stats["file"]; ok {
		usage -= int64(v)
	}
	if usage < 0 {
		return 0
	}
	return usage
}

// Event is an infra-mngmt-shaped wrapper around a Docker engine event.
type Event struct {
	Action string            `json:"action"` // "start", "die", "health_status: healthy", "exec_create", ...
	Time   int64             `json:"time"`   // unix seconds
	Attrs  map[string]string `json:"attrs,omitempty"`
}

// StreamEvents streams Docker engine events for a single container until the
// container is removed or ctx is canceled.
func (c *Client) StreamEvents(ctx context.Context, containerID string) <-chan Event {
	out := make(chan Event, 32)
	go func() {
		defer close(out)
		msgCh, errCh := c.cli.Events(ctx, events.ListOptions{
			Filters: filters.NewArgs(
				filters.KeyValuePair{Key: "type", Value: string(events.ContainerEventType)},
				filters.KeyValuePair{Key: "container", Value: containerID},
			),
		})
		for {
			select {
			case <-ctx.Done():
				return
			case err := <-errCh:
				if err != nil {
					return
				}
			case m, ok := <-msgCh:
				if !ok {
					return
				}
				ev := Event{
					Action: string(m.Action),
					Time:   m.Time,
					Attrs:  m.Actor.Attributes,
				}
				select {
				case out <- ev:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out
}

// StreamLogs streams stdout+stderr from a container as line-by-line strings.
// The returned channel is closed when the container exits or ctx is canceled.
// Handles both multiplexed (non-TTY) and raw (TTY) Docker log streams.
func (c *Client) StreamLogs(ctx context.Context, id string) <-chan string {
	out := make(chan string, 64)
	go func() {
		defer close(out)
		insp, err := c.cli.ContainerInspect(ctx, id)
		if err != nil {
			return
		}
		rc, err := c.cli.ContainerLogs(ctx, id, container.LogsOptions{
			ShowStdout: true,
			ShowStderr: true,
			Follow:     true,
		})
		if err != nil {
			return
		}
		defer rc.Close()

		send := func(line string) bool {
			select {
			case out <- line:
				return true
			case <-ctx.Done():
				return false
			}
		}

		// TTY containers send a raw byte stream; non-TTY containers use the
		// 8-byte multiplexed frame format that stdcopy.StdCopy demuxes for us.
		if insp.Config != nil && insp.Config.Tty {
			emitLines(rc, send)
			return
		}

		// Demux multiplexed stdout/stderr into one stream of lines.
		// stdcopy.StdCopy writes interleaved into the writer; we accumulate
		// then emit by line.
		pr, pw := io.Pipe()
		go func() {
			defer func() { _ = pw.Close() }()
			_, _ = stdcopy.StdCopy(pw, pw, rc)
		}()
		emitLines(pr, send)
	}()
	return out
}

// emitLines reads a stream and pushes complete lines to the send fn.
func emitLines(r io.Reader, send func(string) bool) {
	buf := make([]byte, 4096)
	var pending bytes.Buffer
	for {
		n, err := r.Read(buf)
		if n > 0 {
			pending.Write(buf[:n])
			for {
				idx := bytes.IndexByte(pending.Bytes(), '\n')
				if idx < 0 {
					break
				}
				line := strings.TrimRight(string(pending.Next(idx+1)), "\r\n")
				if !send(line) {
					return
				}
			}
		}
		if err != nil {
			if pending.Len() > 0 {
				send(strings.TrimRight(pending.String(), "\r\n"))
			}
			return
		}
	}
}

// ── volume access via sidecar containers ─────────────────────────────────────

// ReadVolume reads a single file from a named Docker volume.
func (c *Client) ReadVolume(ctx context.Context, volumeName, path string) ([]byte, error) {
	return c.runSidecar(ctx, volumeName, true, []string{"cat", "/data/" + path})
}

// ListVolume returns `find /data -type f` output for the volume contents.
func (c *Client) ListVolume(ctx context.Context, volumeName string) ([]byte, error) {
	return c.runSidecar(ctx, volumeName, true, []string{"find", "/data", "-type", "f"})
}

// WriteVolume is not yet implemented for the read-only MVP; stubbed to satisfy
// callers that use it as a feature flag.
func (c *Client) WriteVolume(_ context.Context, _, _ string, _ []byte) error {
	return fmt.Errorf("WriteVolume: not implemented")
}

// runSidecar runs a one-shot busybox container with the given volume mounted
// at /data, captures stdout, removes the container, and returns the output.
func (c *Client) runSidecar(ctx context.Context, volumeName string, readOnly bool, cmd []string) ([]byte, error) {
	// Pull busybox if missing (best-effort; create will fail with a clearer error if not).
	c.ensureImage(ctx, "busybox:latest")

	bind := volumeName + ":/data"
	if readOnly {
		bind += ":ro"
	}
	created, err := c.cli.ContainerCreate(ctx,
		&container.Config{Image: "busybox:latest", Cmd: cmd, AttachStdout: true, AttachStderr: true},
		&container.HostConfig{Binds: []string{bind}},
		nil, nil, "")
	if err != nil {
		return nil, fmt.Errorf("create sidecar: %w", err)
	}
	defer func() {
		// Best-effort removal — container is throwaway.
		_ = c.cli.ContainerRemove(context.Background(), created.ID, container.RemoveOptions{Force: true})
	}()

	if err := c.cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		return nil, fmt.Errorf("start sidecar: %w", err)
	}

	waitCh, errCh := c.cli.ContainerWait(ctx, created.ID, container.WaitConditionNotRunning)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case err := <-errCh:
		if err != nil {
			return nil, fmt.Errorf("wait sidecar: %w", err)
		}
	case wr := <-waitCh:
		if wr.StatusCode != 0 {
			return nil, fmt.Errorf("sidecar exited with status %d", wr.StatusCode)
		}
	}

	rc, err := c.cli.ContainerLogs(ctx, created.ID, container.LogsOptions{ShowStdout: true})
	if err != nil {
		return nil, fmt.Errorf("sidecar logs: %w", err)
	}
	defer rc.Close()

	var stdout bytes.Buffer
	if _, err := stdcopy.StdCopy(&stdout, io.Discard, rc); err != nil {
		return nil, fmt.Errorf("demux sidecar logs: %w", err)
	}
	return stdout.Bytes(), nil
}

// ensureImage pulls an image if it's not already present locally. Errors are
// ignored so the caller's create call can produce a clearer "image not found"
// failure when needed.
func (c *Client) ensureImage(ctx context.Context, ref string) {
	pullCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	rc, err := c.cli.ImagePull(pullCtx, ref, image.PullOptions{})
	if err == nil {
		_, _ = io.Copy(io.Discard, rc)
		rc.Close()
	}
}
