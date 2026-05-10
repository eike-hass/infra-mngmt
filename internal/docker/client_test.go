package docker

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
)

func TestNormalizeDevcontainerPathPassThrough(t *testing.T) {
	for _, p := range []string{
		"/home/user/project",
		"C:\\Users\\me\\proj",
		"",
		"relative/path",
	} {
		if got := normalizeDevcontainerPath(p); got != p {
			t.Errorf("normalize(%q) = %q, want unchanged", p, got)
		}
	}
}

func TestNormalizeDevcontainerPathWSL(t *testing.T) {
	cases := map[string]string{
		`\\wsl.localhost\Ubuntu\home\user\project`: "/home/user/project",
		`\\wsl$\Ubuntu-22.04\home\u\repo`:          "/home/u/repo",
		`//wsl.localhost/Ubuntu/home/user/p`:       "/home/user/p",
	}
	for in, want := range cases {
		if got := normalizeDevcontainerPath(in); got != want {
			t.Errorf("normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsConfigDirMount(t *testing.T) {
	cases := map[string]bool{
		// Claude Code paths.
		"/root/.claude":           true,
		"/home/node/.claude":      true,
		"/workspaces/foo/.claude": true,
		// OpenCode paths.
		"/root/.opencode":           true,
		"/home/node/.opencode":      true,
		"/workspaces/foo/.opencode": true,
		// Negative cases.
		"/root/.config":  false,
		"/.claude/foo":   false,
		"/.opencode/bar": false,
		"":               false,
	}
	for in, want := range cases {
		if got := isConfigDirMount(in); got != want {
			t.Errorf("isConfigDirMount(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestToManagedExtractsBindMount(t *testing.T) {
	ctr := types.Container{
		ID:    "abc123",
		Names: []string{"/cool_name"},
		Labels: map[string]string{
			LabelDevcontainerLocalFolder: `\\wsl.localhost\Ubuntu\home\u\repo`,
		},
		State: "running",
		Mounts: []types.MountPoint{
			{Type: mount.TypeBind, Source: "/home/u/repo/.claude", Destination: "/home/node/.claude"},
		},
	}
	mc := toManaged(ctr)
	if mc.Name != "cool_name" {
		t.Errorf("Name = %q", mc.Name)
	}
	if mc.State != "running" {
		t.Errorf("State = %q", mc.State)
	}
	if mc.ConfigMount == nil || mc.ConfigMount.IsVolume {
		t.Fatalf("expected non-volume config mount, got %+v", mc.ConfigMount)
	}
	if mc.ConfigMount.HostPath != "/home/u/repo/.claude" {
		t.Errorf("HostPath = %q", mc.ConfigMount.HostPath)
	}
	// ProjectRoot is inferred from bind mount parent dir when no explicit
	// project-root label is set. The local_folder label is a UNC path that
	// only ListManaged populates from outside toManaged, so we expect the
	// bind-mount-derived value here.
	if mc.ProjectRoot != "/home/u/repo" {
		t.Errorf("ProjectRoot = %q", mc.ProjectRoot)
	}
}

func TestToManagedExtractsVolumeMount(t *testing.T) {
	ctr := types.Container{
		ID:     "xyz",
		Names:  []string{"/proj-dev"},
		Labels: map[string]string{LabelManaged: "true"},
		Mounts: []types.MountPoint{
			{Type: mount.TypeVolume, Name: "claude-config-vol", Destination: "/home/node/.claude"},
		},
	}
	mc := toManaged(ctr)
	if mc.ConfigMount == nil || !mc.ConfigMount.IsVolume {
		t.Fatalf("expected volume config mount, got %+v", mc.ConfigMount)
	}
	if mc.ConfigMount.VolumeName != "claude-config-vol" {
		t.Errorf("VolumeName = %q", mc.ConfigMount.VolumeName)
	}
}

func TestToManagedFallbackToConfigVolLabel(t *testing.T) {
	ctr := types.Container{
		ID:     "no-mount",
		Names:  []string{"/x"},
		Labels: map[string]string{LabelConfigVol: "fallback-vol"},
	}
	mc := toManaged(ctr)
	if mc.ConfigMount == nil || mc.ConfigMount.VolumeName != "fallback-vol" {
		t.Fatalf("expected volume from label fallback, got %+v", mc.ConfigMount)
	}
}

func TestToManagedExplicitProjectRootWins(t *testing.T) {
	ctr := types.Container{
		ID:    "p1",
		Names: []string{"/p"},
		Labels: map[string]string{
			LabelProjectRoot: "/explicit/root",
		},
		Mounts: []types.MountPoint{
			{Type: mount.TypeBind, Source: "/somewhere/else/.claude", Destination: "/root/.claude"},
		},
	}
	mc := toManaged(ctr)
	if mc.ProjectRoot != "/explicit/root" {
		t.Errorf("explicit project_root label should win; got %q", mc.ProjectRoot)
	}
}

func TestFindWorkspaceMount(t *testing.T) {
	mounts := []types.MountPoint{
		{Type: mount.TypeBind, Source: "/home/u/repo", Destination: "/workspace"},
		{Type: mount.TypeBind, Source: "/etc/cfg", Destination: "/etc/cfg"},
		{Type: mount.TypeVolume, Name: "vol", Destination: "/var/data"},
	}
	if got := findWorkspaceMount(mounts, "/home/u/repo"); got != "/workspace" {
		t.Errorf("matching bind: got %q, want %q", got, "/workspace")
	}
	if got := findWorkspaceMount(mounts, "/missing"); got != "" {
		t.Errorf("non-matching: got %q, want empty", got)
	}
	if got := findWorkspaceMount(mounts, ""); got != "" {
		t.Errorf("empty projectRoot: got %q, want empty", got)
	}
	// A volume mount whose name happens to match projectRoot must NOT be
	// treated as the workspace — only bind mounts represent the host project.
	volOnly := []types.MountPoint{
		{Type: mount.TypeVolume, Name: "/home/u/repo", Destination: "/wrong"},
	}
	if got := findWorkspaceMount(volOnly, "/home/u/repo"); got != "" {
		t.Errorf("volume should not match: got %q, want empty", got)
	}
}

func TestEmitLinesYieldsLineByLine(t *testing.T) {
	var got []string
	send := func(s string) bool { got = append(got, s); return true }
	emitLines(strings.NewReader("line1\nline2\nincomplete"), send)
	want := []string{"line1", "line2", "incomplete"}
	if len(got) != len(want) {
		t.Fatalf("got %d lines (%v), want %d (%v)", len(got), got, len(want), want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("line %d = %q, want %q", i, got[i], w)
		}
	}
}

func TestEmitLinesStopsOnSendFalse(t *testing.T) {
	count := 0
	send := func(string) bool { count++; return false }
	emitLines(strings.NewReader("a\nb\nc\n"), send)
	if count != 1 {
		t.Errorf("send returning false should stop iteration; count = %d, want 1", count)
	}
}

func TestEmitLinesTrimsCRLF(t *testing.T) {
	var got []string
	send := func(s string) bool { got = append(got, s); return true }
	emitLines(strings.NewReader("a\r\nb\r\n"), send)
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("got %v, want [a b]", got)
	}
}

func TestCPUPercent(t *testing.T) {
	// Mirrors the docker stats formula:
	// cpuDelta=2_000_000_000, sysDelta=8_000_000_000, cores=4
	// → (2/8)*4*100 = 100.0%
	s := &container.StatsResponse{}
	s.CPUStats.CPUUsage.TotalUsage = 3_000_000_000
	s.PreCPUStats.CPUUsage.TotalUsage = 1_000_000_000
	s.CPUStats.SystemUsage = 16_000_000_000
	s.PreCPUStats.SystemUsage = 8_000_000_000
	s.CPUStats.OnlineCPUs = 4
	if got := cpuPercent(s); got != 100.0 {
		t.Errorf("cpuPercent = %v, want 100.0", got)
	}
}

func TestCPUPercentZeroDeltas(t *testing.T) {
	// Newly-started container: precpu_stats not yet populated → return 0,
	// not NaN/Inf.
	if got := cpuPercent(&container.StatsResponse{}); got != 0 {
		t.Errorf("cpuPercent on zero stats = %v, want 0", got)
	}
}

func TestCPUPercentFallsBackToPercpuLen(t *testing.T) {
	// Older daemons populate PercpuUsage but not OnlineCPUs.
	s := &container.StatsResponse{}
	s.CPUStats.CPUUsage.TotalUsage = 100
	s.PreCPUStats.CPUUsage.TotalUsage = 0
	s.CPUStats.SystemUsage = 200
	s.PreCPUStats.SystemUsage = 0
	s.CPUStats.CPUUsage.PercpuUsage = []uint64{0, 0}
	if got := cpuPercent(s); got != 100.0 {
		t.Errorf("cpuPercent with PercpuUsage fallback = %v, want 100.0", got)
	}
}

func TestMemUsageSubtractsCache(t *testing.T) {
	// cgroup v1: subtract "cache".
	s := &container.StatsResponse{}
	s.MemoryStats.Usage = 1_000_000
	s.MemoryStats.Stats = map[string]uint64{"cache": 200_000}
	if got := memUsage(s); got != 800_000 {
		t.Errorf("memUsage(cgroup v1) = %d, want 800000", got)
	}
}

func TestMemUsageSubtractsFile(t *testing.T) {
	// cgroup v2: subtract "file".
	s := &container.StatsResponse{}
	s.MemoryStats.Usage = 1_000_000
	s.MemoryStats.Stats = map[string]uint64{"file": 300_000}
	if got := memUsage(s); got != 700_000 {
		t.Errorf("memUsage(cgroup v2) = %d, want 700000", got)
	}
}

func TestMemUsageNoStats(t *testing.T) {
	// Windows / older daemons return raw usage with no stats map.
	s := &container.StatsResponse{}
	s.MemoryStats.Usage = 500_000
	if got := memUsage(s); got != 500_000 {
		t.Errorf("memUsage(no stats) = %d, want 500000", got)
	}
}

// ── volume cache ──────────────────────────────────────────────────────────────

func newCacheTestClient() *Client {
	return &Client{
		volumeCache:          map[string]volumeCacheEntry{},
		volumeContainerCache: map[string]volumeContainerCacheEntry{},
	}
}

func TestVolumeCacheHitWithinTTL(t *testing.T) {
	c := newCacheTestClient()
	c.volumeCachePut("read:vol-a:settings.json", []byte("payload"), nil)

	got, ok := c.volumeCacheGet("read:vol-a:settings.json")
	if !ok {
		t.Fatal("expected cache hit")
	}
	if string(got) != "payload" {
		t.Errorf("data = %q, want %q", got, "payload")
	}
}

func TestVolumeCacheMissAfterTTL(t *testing.T) {
	c := newCacheTestClient()
	c.volumeCache["read:vol-a:settings.json"] = volumeCacheEntry{
		data:    []byte("stale"),
		fetchAt: time.Now().Add(-2 * volumeCacheTTL),
	}
	if _, ok := c.volumeCacheGet("read:vol-a:settings.json"); ok {
		t.Fatal("expected miss for entry past TTL")
	}
}

func TestVolumeCacheDoesNotServeErrors(t *testing.T) {
	// Errors are stored (so callers see the most recent failure within the
	// dedupe window) but should not be served as a positive cache hit.
	c := newCacheTestClient()
	c.volumeCachePut("read:vol-a:missing", nil, fmt.Errorf("boom"))
	if _, ok := c.volumeCacheGet("read:vol-a:missing"); ok {
		t.Errorf("error entry should not be served as a hit")
	}
}

func TestInvalidateVolumeCacheTargetsOneVolume(t *testing.T) {
	c := newCacheTestClient()
	c.volumeCachePut("list:vol-a", []byte("la"), nil)
	c.volumeCachePut("read:vol-a:foo", []byte("af"), nil)
	c.volumeCachePut("list:vol-b", []byte("lb"), nil)
	c.volumeCachePut("read:vol-b:bar", []byte("bb"), nil)

	c.InvalidateVolumeCache("vol-a")

	if _, ok := c.volumeCacheGet("list:vol-a"); ok {
		t.Errorf("vol-a list entry should be evicted")
	}
	if _, ok := c.volumeCacheGet("read:vol-a:foo"); ok {
		t.Errorf("vol-a read entry should be evicted")
	}
	if _, ok := c.volumeCacheGet("list:vol-b"); !ok {
		t.Errorf("vol-b list entry should be preserved")
	}
	if _, ok := c.volumeCacheGet("read:vol-b:bar"); !ok {
		t.Errorf("vol-b read entry should be preserved")
	}
}
