package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"golang.org/x/sync/singleflight"
)

// Client wraps the official Docker SDK with infra-mngmt-specific helpers
// (managed-container discovery, sidecar volume IO, log demux into a channel).
// Callers should not depend on the underlying SDK type directly — that lets us
// swap implementations later if needed.
type Client struct {
	cli *client.Client
	// volumeIO dedupes concurrent ReadVolume/ListVolume calls so two HTTP
	// requests for the same (volume, path) only spawn one sidecar. Without
	// this, an HTMX-driven page render with two volume-backed entities can
	// double the container churn for nothing.
	volumeIO singleflight.Group

	// volumeCacheMu guards volumeCache, a per-(volumeName, path) bytes cache.
	// TTL bounds staleness (Source.Watch is nil so external mutations are
	// only seen on next refresh). volumeContainerCache stores the result of
	// ContainerForVolume with a shorter TTL since container lifecycles can
	// change faster than file contents.
	volumeCacheMu sync.RWMutex
	volumeCache   map[string]volumeCacheEntry

	volumeContainerMu    sync.RWMutex
	volumeContainerCache map[string]volumeContainerCacheEntry
}

type volumeCacheEntry struct {
	data    []byte
	err     error
	fetchAt time.Time
}

type volumeContainerCacheEntry struct {
	id, mountPath string
	fetchAt       time.Time
}

const (
	// volumeCacheTTL bounds staleness on cached file/list bytes from Docker
	// volumes. 30s matches the entity-list cache TTL above.
	volumeCacheTTL = 30 * time.Second
	// volumeContainerCacheTTL bounds staleness of the running-container
	// lookup. Containers come/go faster than file contents, so TTL is
	// shorter; 5s is a balance between freshness and not pestering Docker.
	volumeContainerCacheTTL = 5 * time.Second
)

func New() (*Client, error) {
	cli, err := client.NewClientWithOpts(
		client.FromEnv,
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}
	return &Client{
		cli:                  cli,
		volumeCache:          map[string]volumeCacheEntry{},
		volumeContainerCache: map[string]volumeContainerCacheEntry{},
	}, nil
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
	ID              string
	Name            string
	Labels          map[string]string
	ConfigMount     *ClaudeMount
	ProjectRoot     string
	WorkspaceFolder string // in-container path of the host-bind workspace mount; "" if not detected
	State           string // "running", "exited", "paused", etc.
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
			mc.WorkspaceFolder = findWorkspaceMount(ctr.Mounts, mc.ProjectRoot)
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
			mc := toManaged(ctr)
			mc.WorkspaceFolder = findWorkspaceMount(ctr.Mounts, mc.ProjectRoot)
			out = append(out, mc)
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

// findWorkspaceMount returns the in-container destination of the bind mount
// whose host-side Source matches projectRoot. Used for VS Code attach URIs
// so the remote opens at the workspace folder rather than the container root.
func findWorkspaceMount(mounts []types.MountPoint, projectRoot string) string {
	if projectRoot == "" {
		return ""
	}
	for _, m := range mounts {
		if m.Type == mount.TypeBind && m.Source == projectRoot {
			return m.Destination
		}
	}
	return ""
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
		if !isConfigDirMount(m.Destination) {
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

// configMountDirs is the set of in-container destination paths that count as
// a coding-assistant config mount. Each entry is matched both as a suffix
// (e.g. /home/node/.claude, /workspaces/foo/.claude) and as the absolute
// path under /root (devcontainers running as root).
var configMountDirs = []string{".claude", ".opencode"}

// isConfigDirMount reports whether dst is a mount destination for a
// recognized coding-assistant config dir. Currently matches Claude Code
// (.claude) and OpenCode (.opencode); add new tools to configMountDirs.
func isConfigDirMount(dst string) bool {
	for _, name := range configMountDirs {
		if strings.HasSuffix(dst, "/"+name) || dst == "/root/"+name {
			return true
		}
	}
	return false
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
//
// Order: cache → singleflight → (exec into running container if available,
// else throwaway sidecar).
//
// Cache hits return immediately (typical sub-ms). On miss, singleflight
// dedupes concurrent fetches for the same (volume, path) so only one Docker
// call runs per key. The exec fast-path is ~10× faster than container
// creation; sidecar is the universal fallback.
func (c *Client) ReadVolume(ctx context.Context, volumeName, path string) ([]byte, error) {
	key := "read:" + volumeName + ":" + path
	if data, ok := c.volumeCacheGet(key); ok {
		return data, nil
	}
	v, err, _ := c.volumeIO.Do(key, func() (any, error) {
		// Re-check cache inside the singleflight crit — another caller may
		// have populated it while we waited for the lock.
		if data, ok := c.volumeCacheGet(key); ok {
			return data, nil
		}
		data, err := c.readVolumeExecOrSidecar(ctx, volumeName, path)
		c.volumeCachePut(key, data, err)
		return data, err
	})
	if err != nil {
		return nil, err
	}
	return v.([]byte), nil
}

// ListVolume returns `find /data -type f` output for the volume contents.
// Same caching + singleflight + exec/sidecar layering as ReadVolume.
func (c *Client) ListVolume(ctx context.Context, volumeName string) ([]byte, error) {
	key := "list:" + volumeName
	if data, ok := c.volumeCacheGet(key); ok {
		return data, nil
	}
	v, err, _ := c.volumeIO.Do(key, func() (any, error) {
		if data, ok := c.volumeCacheGet(key); ok {
			return data, nil
		}
		data, err := c.listVolumeExecOrSidecar(ctx, volumeName)
		c.volumeCachePut(key, data, err)
		return data, err
	})
	if err != nil {
		return nil, err
	}
	return v.([]byte), nil
}

// readVolumeExecOrSidecar tries `docker exec` into a running container that
// has this volume mounted. Falls back to spawning a throwaway sidecar when
// no such container exists or when the exec fails for any reason.
func (c *Client) readVolumeExecOrSidecar(ctx context.Context, volumeName, path string) ([]byte, error) {
	if ctrID, mountPath, ok := c.containerForVolume(ctx, volumeName); ok {
		full := mountPath
		if !strings.HasSuffix(full, "/") {
			full += "/"
		}
		full += path
		if data, err := c.execStdout(ctx, ctrID, []string{"cat", full}); err == nil {
			return data, nil
		}
	}
	return c.runSidecar(ctx, volumeName, true, []string{"cat", "/data/" + path})
}

func (c *Client) listVolumeExecOrSidecar(ctx context.Context, volumeName string) ([]byte, error) {
	if ctrID, mountPath, ok := c.containerForVolume(ctx, volumeName); ok {
		if data, err := c.execStdout(ctx, ctrID, []string{"find", mountPath, "-type", "f"}); err == nil {
			// Sidecar emits paths under /data; rewrite the running-container
			// paths to that namespace so callers (e.g. parseFileList) don't
			// need to care which mechanism was used.
			out := bytes.ReplaceAll(data, []byte(mountPath), []byte("/data"))
			return out, nil
		}
	}
	return c.runSidecar(ctx, volumeName, true, []string{"find", "/data", "-type", "f"})
}

// containerForVolume returns (containerID, mountPath, true) for a running
// container that has volumeName mounted; (false) otherwise. Cached briefly
// (volumeContainerCacheTTL) since the lookup is more volatile than file
// contents.
func (c *Client) containerForVolume(ctx context.Context, volumeName string) (string, string, bool) {
	c.volumeContainerMu.RLock()
	if e, ok := c.volumeContainerCache[volumeName]; ok && time.Since(e.fetchAt) < volumeContainerCacheTTL {
		c.volumeContainerMu.RUnlock()
		if e.id == "" {
			return "", "", false
		}
		return e.id, e.mountPath, true
	}
	c.volumeContainerMu.RUnlock()

	id, mp, ok := c.lookupContainerForVolume(ctx, volumeName)
	c.volumeContainerMu.Lock()
	c.volumeContainerCache[volumeName] = volumeContainerCacheEntry{id: id, mountPath: mp, fetchAt: time.Now()}
	c.volumeContainerMu.Unlock()
	return id, mp, ok
}

func (c *Client) lookupContainerForVolume(ctx context.Context, volumeName string) (string, string, bool) {
	ctrs, err := c.cli.ContainerList(ctx, container.ListOptions{
		Filters: filters.NewArgs(filters.KeyValuePair{Key: "volume", Value: volumeName}),
	})
	if err != nil {
		return "", "", false
	}
	for _, ctr := range ctrs {
		if ctr.State != "running" {
			continue
		}
		for _, m := range ctr.Mounts {
			if m.Type == mount.TypeVolume && m.Name == volumeName {
				return ctr.ID, m.Destination, true
			}
		}
	}
	return "", "", false
}

// execStdout runs cmd inside a running container via the Docker exec API and
// returns the captured stdout. Returns an error when the exec exits non-zero.
func (c *Client) execStdout(ctx context.Context, containerID string, cmd []string) ([]byte, error) {
	created, err := c.cli.ContainerExecCreate(ctx, containerID, container.ExecOptions{
		AttachStdout: true, AttachStderr: true, Cmd: cmd,
	})
	if err != nil {
		return nil, fmt.Errorf("exec create: %w", err)
	}
	resp, err := c.cli.ContainerExecAttach(ctx, created.ID, container.ExecStartOptions{})
	if err != nil {
		return nil, fmt.Errorf("exec attach: %w", err)
	}
	defer resp.Close()

	var stdout bytes.Buffer
	if _, err := stdcopy.StdCopy(&stdout, io.Discard, resp.Reader); err != nil {
		return nil, fmt.Errorf("demux exec output: %w", err)
	}
	insp, err := c.cli.ContainerExecInspect(ctx, created.ID)
	if err == nil && insp.ExitCode != 0 {
		return nil, fmt.Errorf("exec exited %d", insp.ExitCode)
	}
	return stdout.Bytes(), nil
}

func (c *Client) volumeCacheGet(key string) ([]byte, bool) {
	c.volumeCacheMu.RLock()
	defer c.volumeCacheMu.RUnlock()
	e, ok := c.volumeCache[key]
	if !ok || time.Since(e.fetchAt) >= volumeCacheTTL {
		return nil, false
	}
	if e.err != nil {
		return nil, false
	}
	return e.data, true
}

func (c *Client) volumeCachePut(key string, data []byte, err error) {
	c.volumeCacheMu.Lock()
	defer c.volumeCacheMu.Unlock()
	c.volumeCache[key] = volumeCacheEntry{data: data, err: err, fetchAt: time.Now()}
}

// InvalidateVolumeCache drops cached entries matching volumeName. Called by
// callers that just wrote to the volume so subsequent reads see the change
// without waiting for TTL.
func (c *Client) InvalidateVolumeCache(volumeName string) {
	c.volumeCacheMu.Lock()
	defer c.volumeCacheMu.Unlock()
	prefix := ":" + volumeName + ":"
	listKey := "list:" + volumeName
	for k := range c.volumeCache {
		if k == listKey || strings.Contains(k, prefix) {
			delete(c.volumeCache, k)
		}
	}
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
