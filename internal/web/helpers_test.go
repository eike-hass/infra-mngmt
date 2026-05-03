package web

import (
	"testing"

	"github.com/eike-hass/infra-mngmt/internal/entity"
)

func TestSseEscape(t *testing.T) {
	cases := map[string]string{
		"plain":             "plain",
		"with\nnewline":     "with newline",
		"with\r\ncrlf\nend": "with crlf end",
		"trailing\r\n\n":    "trailing",
		"":                  "",
	}
	for in, want := range cases {
		if got := sseEscape(in); got != want {
			t.Errorf("sseEscape(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidProcessName(t *testing.T) {
	good := []string{"foo", "foo-bar", "foo_bar", "abc123", "X", "a"}
	bad := []string{"", "../etc", "foo bar", "foo;rm", "foo/bar", "foo.bar"}
	for _, s := range good {
		if !validProcessName(s) {
			t.Errorf("validProcessName(%q) = false, want true", s)
		}
	}
	for _, s := range bad {
		if validProcessName(s) {
			t.Errorf("validProcessName(%q) = true, want false", s)
		}
	}
	// length cap (128 chars max)
	long := make([]byte, 129)
	for i := range long {
		long[i] = 'a'
	}
	if validProcessName(string(long)) {
		t.Error("129-char name should be rejected")
	}
}

func TestSourceLevel(t *testing.T) {
	cases := []struct {
		id     string
		global bool
		want   string
	}{
		{"host:/home/u/.claude", true, "global"},
		{"host:/home/u/repo/.claude", false, "project"},
		{"vol:claude-vol", false, "devcontainer"},
		{"ctr:abc123", false, "devcontainer"},
		{"weird:thing", false, "project"}, // unknown prefixes default to project
	}
	for _, tc := range cases {
		if got := sourceLevel(tc.id, tc.global); got != tc.want {
			t.Errorf("sourceLevel(%q, %v) = %q, want %q", tc.id, tc.global, got, tc.want)
		}
	}
}

func TestSourceLabelHost(t *testing.T) {
	if got := sourceLabel("host:/home/u/.claude", true, ""); got != "~/.claude" {
		t.Errorf("global host label = %q", got)
	}
	if got := sourceLabel("host:/home/u/repo/.claude", false, "/home/u/repo"); got != "repo" {
		t.Errorf("project host label = %q", got)
	}
}

func TestSourceLabelVolume(t *testing.T) {
	if got := sourceLabel("vol:claude-config-abc", false, "/home/u/repo"); got != "repo" {
		t.Errorf("vol with projectRoot should use repo basename, got %q", got)
	}
	// Volume name truncation when no projectRoot
	long := "1234567890abcdefghij"
	got := sourceLabel("vol:"+long, false, "")
	if got != "vol:1234567890abcdef…" {
		t.Errorf("long vol label = %q", got)
	}
}

func TestSourceLabelContainer(t *testing.T) {
	if got := sourceLabel("ctr:abc123def456789", false, ""); got != "ctr:abc123def456" {
		t.Errorf("ctr label = %q", got)
	}
	if got := sourceLabel("ctr:abc", false, "/home/u/repo"); got != "repo" {
		t.Errorf("ctr with projectRoot = %q", got)
	}
}

func TestSourceLabelMalformed(t *testing.T) {
	if got := sourceLabel("no-colon", false, ""); got != "no-colon" {
		t.Errorf("malformed id should pass through, got %q", got)
	}
}

func TestKindIcon(t *testing.T) {
	// Spot-check a few — ensures the switch covers each kind without crashing.
	cases := map[entity.Kind]string{
		entity.KindMCPServer: "⬡",
		entity.KindCommand:   "$",
		entity.KindAgent:     "◉",
		entity.KindSkill:     "✦",
		entity.KindMemory:    "▤",
		entity.KindHook:      "↪",
		entity.KindClaudeMD:  "#",
		entity.Kind("???"):   "·", // default
	}
	for k, want := range cases {
		if got := kindIcon(k); got != want {
			t.Errorf("kindIcon(%q) = %q, want %q", k, got, want)
		}
	}
}

func TestFormatMem(t *testing.T) {
	cases := map[int64]string{
		0: "—", 512: "512B", 1024: "1K", 1500: "1K",
		1024 * 1024:     "1M",
		1024 * 1024 * 8: "8M",
	}
	for in, want := range cases {
		if got := formatMem(in); got != want {
			t.Errorf("formatMem(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestStatusClass(t *testing.T) {
	// Upstream status constants (src/types/process.go): Disabled, Foreground,
	// Pending, Running, Launching, Launched, Restarting, Terminating,
	// Completed, Skipped, Error, Scheduled.
	cases := map[string]string{
		"Running": "running", "running": "running",
		"Foreground": "running", "Launched": "running",
		"Completed":   "stopped",
		"Error":       "error",
		"Pending":     "starting",
		"Launching":   "starting",
		"Restarting":  "starting",
		"Terminating": "starting",
		"Scheduled":   "starting",
		"Disabled":    "disabled", "Skipped": "disabled",
		"":     "unknown",
		"Wat?": "unknown",
	}
	for in, want := range cases {
		if got := statusClass(in); got != want {
			t.Errorf("statusClass(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHealthClass(t *testing.T) {
	cases := map[string]string{
		"Ready":     "ready",
		"Not Ready": "not-ready",
		"-":         "unknown",
		"":          "unknown",
	}
	for in, want := range cases {
		if got := healthClass(in); got != want {
			t.Errorf("healthClass(%q) = %q, want %q", in, got, want)
		}
	}
}
