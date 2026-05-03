package docker

import (
	"strings"
	"testing"

	"github.com/docker/docker/api/types"
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

func TestIsClaudeMount(t *testing.T) {
	cases := map[string]bool{
		"/root/.claude":           true,
		"/home/node/.claude":      true,
		"/workspaces/foo/.claude": true,
		"/root/.config":           false,
		"/.claude/foo":            false,
		"":                        false,
	}
	for in, want := range cases {
		if got := isClaudeMount(in); got != want {
			t.Errorf("isClaudeMount(%q) = %v, want %v", in, got, want)
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
