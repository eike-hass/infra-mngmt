package web

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/eike-hass/infra-mngmt/internal/system"
)

// ── pure helpers ─────────────────────────────────────────────────────────

func TestFmtSize(t *testing.T) {
	const gb = 1024 * 1024 * 1024
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0"},
		{-5, "0"},
		{512, "512 B"},
		{2048, "2 KB"},
		{5 * 1024 * 1024, "5 MB"},
		{27 * gb, "27.0 GB"},
		{int64(1.5 * gb), "1.5 GB"},
		{120 * gb, "120 GB"}, // ≥100 GB rounds to whole
	}
	for _, c := range cases {
		if got := fmtSize(c.in); got != c.want {
			t.Errorf("fmtSize(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPctOf(t *testing.T) {
	if got := pctOf(789, 931); got != 84 {
		t.Errorf("pctOf(789,931) = %d, want 84", got)
	}
	if got := pctOf(5, 0); got != 0 {
		t.Errorf("pctOf with zero total = %d, want 0", got)
	}
}

func TestShortDigest(t *testing.T) {
	if got := shortDigest("sha256:9f2b1c0d3e4f5a6b7c8d"); got != "9f2b1c0d3e4f" {
		t.Errorf("shortDigest = %q, want 9f2b1c0d3e4f", got)
	}
	if got := shortDigest("abc"); got != "abc" {
		t.Errorf("shortDigest(short) = %q, want abc", got)
	}
}

func TestTagEngineHosts(t *testing.T) {
	d := system.DiskInfo{Distros: []system.Distro{
		{Name: "Ubuntu-24.04", Default: true},
		{Name: "podman-machine-default"},
		{Name: "Ubuntu-22.04"},
	}}
	tagEngineHosts(&d)
	if d.Distros[0].Engine != "docker" {
		t.Errorf("default distro should be tagged docker, got %q", d.Distros[0].Engine)
	}
	if d.Distros[1].Engine != "podman" {
		t.Errorf("podman-machine should be tagged podman, got %q", d.Distros[1].Engine)
	}
	if d.Distros[2].Engine != "" {
		t.Errorf("plain distro should be untagged, got %q", d.Distros[2].Engine)
	}
}

// ── confirm builders ─────────────────────────────────────────────────────

func sampleDF() system.EngineDF {
	return system.EngineDF{
		Engine: system.EngineDocker, Available: true, Version: "27.1.1", Distro: "Ubuntu-24.04",
		Rows: []system.DFRow{
			{Key: "images", Label: "Images", Prune: "docker image prune -a", Items: []system.DFItem{
				{ID: "sha256:aa", Repo: "node", Tag: "20", Size: 100, InUse: true},
				{ID: "sha256:bb", Repo: "<none>", Tag: "<none>", Size: 300, InUse: false},
			}},
			{Key: "volumes", Label: "Local Volumes", Prune: "docker volume prune -a", Items: []system.DFItem{
				{Name: "scratch", Driver: "local", Size: 200, InUse: false},
			}},
		},
	}
}

func TestBuildPruneConfirmCategory(t *testing.T) {
	data, ok := buildPruneConfirm("docker", "images", sampleDF(), sysDockerTarget)
	if !ok {
		t.Fatal("expected ok for a category with reclaimable images")
	}
	if data.Command != "docker image prune -a" {
		t.Errorf("command = %q", data.Command)
	}
	if data.ActionURL != "/system/prune?engine=docker&key=images" {
		t.Errorf("action url = %q", data.ActionURL)
	}
	if data.CardTarget != sysDockerTarget {
		t.Errorf("target = %q", data.CardTarget)
	}
	// one unused image, 300 bytes reclaimable
	joined := data.Lines[0].Value + "|" + data.Lines[1].Value
	if !strings.Contains(joined, "1 unused object") || !strings.Contains(joined, "300 B") {
		t.Errorf("facts = %+v", data.Lines)
	}
}

func TestBuildPruneConfirmAll(t *testing.T) {
	data, ok := buildPruneConfirm("docker", "__all__", sampleDF(), sysDockerTarget)
	if !ok {
		t.Fatal("expected ok for whole-engine prune")
	}
	if data.Command != "docker system prune -a --volumes" {
		t.Errorf("command = %q, want system prune", data.Command)
	}
	// 2 unused objects total (one image + one volume), 500 bytes
	if !strings.Contains(data.Lines[0].Value, "2 unused objects") {
		t.Errorf("removed count = %q", data.Lines[0].Value)
	}
}

func TestBuildPruneConfirmNothing(t *testing.T) {
	df := system.EngineDF{Available: true, Rows: []system.DFRow{
		{Key: "images", Items: []system.DFItem{{ID: "a", Size: 10, InUse: true}}},
	}}
	if _, ok := buildPruneConfirm("docker", "images", df, sysDockerTarget); ok {
		t.Error("nothing reclaimable should yield ok=false")
	}
}

func TestBuildRemoveConfirm(t *testing.T) {
	data, ok := buildRemoveConfirm("docker", "volumes", "scratch", sampleDF(), sysDockerTarget)
	if !ok {
		t.Fatal("expected ok removing a not-in-use volume")
	}
	if data.Command != "docker volume rm scratch" {
		t.Errorf("command = %q", data.Command)
	}
	if !strings.Contains(data.ActionURL, "op") && !strings.Contains(data.ActionURL, "id=scratch") {
		t.Errorf("action url = %q", data.ActionURL)
	}
}

func TestBuildRemoveConfirmInUseRejected(t *testing.T) {
	if _, ok := buildRemoveConfirm("docker", "images", "sha256:aa", sampleDF(), sysDockerTarget); ok {
		t.Error("removing an in-use object should be rejected")
	}
	if _, ok := buildRemoveConfirm("docker", "images", "nope", sampleDF(), sysDockerTarget); ok {
		t.Error("removing a missing object should be rejected")
	}
}

func TestPruneWarningSeverity(t *testing.T) {
	cases := map[string]string{
		"volumes":    "sys-warn-danger",
		"__all__":    "sys-warn-danger",
		"buildcache": "sys-warn-info",
		"__safe__":   "sys-warn-info",
		"images":     "", // caution tier: base class only, no modifier
		"containers": "",
	}
	for key, wantClass := range cases {
		text, class := pruneWarning(key)
		if class != wantClass {
			t.Errorf("pruneWarning(%q) class = %q, want %q", key, class, wantClass)
		}
		if text == "" {
			t.Errorf("pruneWarning(%q) text is empty", key)
		}
	}
	// The volume warning must mention the data-loss hazard.
	if vt, _ := pruneWarning("volumes"); !strings.Contains(vt, "recover") {
		t.Errorf("volume warning should warn about unrecoverable data: %q", vt)
	}
}

func dfWithBuildCache() system.EngineDF {
	df := sampleDF() // images (1 dangling <none> 300B reclaimable) + volumes
	df.Rows = append(df.Rows, system.DFRow{Key: "buildcache", Label: "Build Cache", Prune: "docker builder prune -a", Items: []system.DFItem{
		{ID: "bc1", Type: "regular", Size: 1000, InUse: false},
	}})
	return df
}

func TestBuildSafeConfirm(t *testing.T) {
	data, ok := buildSafeConfirm("docker", dfWithBuildCache(), sysDockerTarget)
	if !ok {
		t.Fatal("expected ok: dangling image + build cache are safe-reclaimable")
	}
	if !strings.Contains(data.Command, "image prune") || !strings.Contains(data.Command, "builder prune -a") {
		t.Errorf("safe command = %q; want image prune + builder prune -a", data.Command)
	}
	if data.WarnClass != "sys-warn-info" {
		t.Errorf("safe prune WarnClass = %q, want sys-warn-info", data.WarnClass)
	}
	if data.ActionURL != "/system/prune?engine=docker&key=__safe__" {
		t.Errorf("safe action url = %q", data.ActionURL)
	}
	if data.Lines[0].Label != "removes" || !strings.Contains(data.Lines[0].Value, "dangling image") {
		t.Errorf("safe prune 'removes' line = %+v", data.Lines[0])
	}
	// The reclaim estimate is present, capped, and flagged approximate ("~").
	var est *sysConfirmLine
	for i := range data.Lines {
		if strings.HasPrefix(data.Lines[i].Label, "reclaims") {
			est = &data.Lines[i]
		}
	}
	if est == nil || !strings.HasPrefix(est.Value, "~") {
		t.Errorf("safe prune should show an approximate (~) reclaim estimate, lines=%+v", data.Lines)
	}
}

func TestBuildSafeConfirmNothing(t *testing.T) {
	// Only an in-use image: nothing safe.
	df := system.EngineDF{Available: true, Rows: []system.DFRow{
		{Key: "images", Items: []system.DFItem{{Repo: "node", Size: 10, InUse: true}}},
	}}
	if _, ok := buildSafeConfirm("docker", df, sysDockerTarget); ok {
		t.Error("no safe-reclaimable objects should yield ok=false")
	}
}

// ── template rendering ───────────────────────────────────────────────────

func renderBlock(t *testing.T, block string, data any) string {
	t.Helper()
	tmpl := parseTemplate("sys", sysTmpl)
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, block, data); err != nil {
		t.Fatalf("ExecuteTemplate %s: %v", block, err)
	}
	return buf.String()
}

func TestRenderSystemShell(t *testing.T) {
	out := renderBlock(t, "system-shell", nil)
	for _, want := range []string{"sys-disk", "sys-docker", "sys-podman", "/partials/system/disk", "sys-refresh from:body"} {
		if !strings.Contains(out, want) {
			t.Errorf("shell missing %q", want)
		}
	}
}

func TestRenderDiskCard(t *testing.T) {
	const gb = 1024 * 1024 * 1024
	disk := system.DiskInfo{
		Windows: system.WindowsDrive{Drive: "C:", Total: 931 * gb, Used: 789 * gb},
		Distros: []system.Distro{
			{Name: "Ubuntu-24.04", Version: 2, Default: true, Engine: "docker", State: "Running", FsTotal: 251 * gb, FsUsed: 198 * gb, FsKnown: true, VhdxSize: 214 * gb},
			{Name: "Ubuntu-22.04", Version: 2, State: "Stopped", VhdxSize: 38 * gb},
		},
	}
	out := renderBlock(t, "sys-disk-card", disk)
	for _, want := range []string{"wsl2 disk", "Ubuntu-24.04", "789 GB used", "142 GB free", "reclaimable by compact", "WSL2 · default · docker host", "— (stopped)"} {
		if !strings.Contains(out, want) {
			t.Errorf("disk card missing %q\n---\n%s", want, out)
		}
	}
	// compact is disabled in this phase
	if !strings.Contains(out, "<button class=\"proc-btn\" disabled") {
		t.Error("compact button should be disabled")
	}
}

func TestRenderDiskCardError(t *testing.T) {
	out := renderBlock(t, "sys-disk-card", system.DiskInfo{Err: "no interop"})
	if !strings.Contains(out, "can't read disk state") || !strings.Contains(out, "no interop") {
		t.Errorf("error card missing message:\n%s", out)
	}
}

func TestRenderEngineCardAvailable(t *testing.T) {
	card := sysEngineCard{DF: sampleDF(), Glyph: "⬢", Slug: "docker"}
	out := renderBlock(t, "sys-engine-card", card)
	for _, want := range []string{
		"v27.1.1", "Images", "Local Volumes",
		"prune all", "in use", "dangling",
		"safe prune", // sampleDF has a dangling <none> image → safe prune offered
		"op=prune&key=__safe__",
		"/partials/system/confirm?engine=docker&op=prune&key=images",
		"sysd-docker-images", // disclosure target
		"frees space inside Ubuntu-24.04",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("engine card missing %q", want)
		}
	}
}

func TestRenderEngineCardUnavailable(t *testing.T) {
	card := sysEngineCard{DF: system.EngineDF{Engine: system.EnginePodman, Available: false}, Glyph: "⬡", Slug: "podman"}
	out := renderBlock(t, "sys-engine-card", card)
	if !strings.Contains(out, "isn't reachable") || !strings.Contains(out, "podman machine start") {
		t.Errorf("unavailable card missing prompt:\n%s", out)
	}
	if strings.Contains(out, "prune all") {
		t.Error("unavailable card should not offer prune")
	}
}

func TestRenderConfirmModal(t *testing.T) {
	data, _ := buildPruneConfirm("docker", "images", sampleDF(), sysDockerTarget)
	out := renderBlock(t, "sys-confirm", data)
	// ActionURL is interpolated so its "&" renders as the HTML entity "&amp;"
	// (which the browser decodes back to "&" for htmx) — assert around it.
	for _, want := range []string{
		"docker image prune -a", "reclaims",
		`hx-post="/system/prune?engine=docker`, `key=images"`,
		`hx-target="#sys-docker"`,
		"re-pull or rebuild", // images-category warning text
		"sys-warn",           // severity class on the warning box
	} {
		if !strings.Contains(out, want) {
			t.Errorf("confirm modal missing %q", want)
		}
	}
}

func TestRenderConfirmModalVolumeDanger(t *testing.T) {
	data, _ := buildPruneConfirm("docker", "volumes", sampleDF(), sysDockerTarget)
	out := renderBlock(t, "sys-confirm", data)
	if !strings.Contains(out, "sys-warn-danger") {
		t.Errorf("volume prune modal should carry the danger warning class:\n%s", out)
	}
	if !strings.Contains(out, "recover") {
		t.Error("volume prune modal should warn about unrecoverable data")
	}
}

// ── handlers (graceful degradation with no clients) ───────────────────────

func TestHandleSystemShellHandler(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/partials/system", nil)
	w := httptest.NewRecorder()
	s.handleSystem(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "sys-docker") {
		t.Fatalf("status %d body %q", w.Code, w.Body.String())
	}
}

func TestHandleSystemDockerNilClient(t *testing.T) {
	s := &Server{} // docker nil → unreachable card, no panic
	req := httptest.NewRequest(http.MethodGet, "/partials/system/docker", nil)
	w := httptest.NewRecorder()
	s.handleSystemDocker(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "isn't reachable") {
		t.Errorf("nil docker client should render unreachable card:\n%s", w.Body.String())
	}
}

func TestHandleSystemConfirmBadEngine(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/partials/system/confirm?engine=bogus&op=prune&key=images", nil)
	w := httptest.NewRecorder()
	s.handleSystemConfirm(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("bad engine should 400, got %d", w.Code)
	}
}

func TestHandleSystemPruneBadEngine(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodPost, "/system/prune?engine=bogus&key=images", nil)
	w := httptest.NewRecorder()
	s.handleSystemPrune(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("bad engine should 400, got %d", w.Code)
	}
}
