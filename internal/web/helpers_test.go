package web

import (
	"html/template"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/eike-hass/infra-mngmt/internal/bridge"
	"github.com/eike-hass/infra-mngmt/internal/entity"
)

// TestMain disables bridge TCP probing for the whole web test suite.
// Status() would otherwise try to dial test listen IPs that don't exist,
// reporting StateDegraded and breaking the existing "netsh match = Active"
// expectations in handler/smart-apply tests. Real-mode behavior is
// covered by the bridge package's own tests.
func TestMain(m *testing.M) {
	bridge.ProbeTimeout = 0
	os.Exit(m.Run())
}

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

func TestSourceTool(t *testing.T) {
	cases := []struct {
		id   string
		want string
	}{
		// host paths — tool comes from the trailing config-dir name
		{"host:/home/u/.claude", "claude"},
		{"host:/home/u/.opencode", "opencode"},
		{"host:/home/u/repo/.claude", "claude"},
		{"host:/home/u/repo/.opencode", "opencode"},
		// host paths that don't end in a recognized config dir
		{"host:/home/u/repo/.something-else", ""},
		{"host:/some/random/path", ""},
		// volume sources — tool comes from the leading token of the volume name
		{"vol:claude-code-config-abcdef", "claude"},
		{"vol:opencode-config-xyz", "opencode"},
		{"vol:other-volume-name", ""},
		{"vol:noprefix", ""},
		// container sources don't encode the tool
		{"ctr:abc123", ""},
		// malformed inputs
		{"", ""},
		{"host:", ""},
		{"weird:thing", ""},
	}
	for _, tc := range cases {
		if got := sourceTool(tc.id); got != tc.want {
			t.Errorf("sourceTool(%q) = %q, want %q", tc.id, got, tc.want)
		}
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

func TestCanStop(t *testing.T) {
	yes := []string{"Running", "running", "RESTARTING", "Error", "Pending", "Launching", "Launched", "Foreground", "Terminating", "Scheduled"}
	no := []string{"Completed", "Disabled", "Skipped", "stopped", "Unknown"}
	for _, s := range yes {
		if !canStop(s) {
			t.Errorf("canStop(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if canStop(s) {
			t.Errorf("canStop(%q) = true, want false", s)
		}
	}
}

func TestCanStart(t *testing.T) {
	yes := []string{"Completed", "Disabled", "Skipped", ""}
	no := []string{"Running", "Restarting", "Error", "Pending", "Launching"}
	for _, s := range yes {
		if !canStart(s) {
			t.Errorf("canStart(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if canStart(s) {
			t.Errorf("canStart(%q) = true, want false", s)
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

// ── FuncMap helpers introduced for the Open Design card ────────────────────
//
// Per the architecture-doc rule (§4.2): every entry in `tmplFuncs` gets a
// unit test covering its happy path + the empty/zero input. These helpers
// were shipped without tests and are being backfilled. The helpers are
// looked up by name from the live tmplFuncs map so the test fails loudly
// if a helper is renamed or removed.

// callFuncMap is a tiny helper to invoke a tmplFuncs entry by name with a
// single argument. Returns the result as `any` so callers can type-assert
// to whatever the helper actually returns.
func callFuncMap(t *testing.T, name string, arg any) any {
	t.Helper()
	fn, ok := tmplFuncs[name]
	if !ok {
		t.Fatalf("tmplFuncs[%q] missing", name)
	}
	fv := reflect.ValueOf(fn)
	if fv.Kind() != reflect.Func {
		t.Fatalf("tmplFuncs[%q] is not a function (kind=%s)", name, fv.Kind())
	}
	var in []reflect.Value
	if arg != nil || fv.Type().NumIn() == 1 {
		if arg == nil {
			in = []reflect.Value{reflect.New(fv.Type().In(0)).Elem()}
		} else {
			in = []reflect.Value{reflect.ValueOf(arg)}
		}
	}
	out := fv.Call(in)
	if len(out) != 1 {
		t.Fatalf("tmplFuncs[%q] returned %d values, want 1", name, len(out))
	}
	return out[0].Interface()
}

func TestFmtNum(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{0, "0"},
		{1, "1"},
		{999, "999"},
		{1000, "1.0k"},
		{1234, "1.2k"},
		{9999, "10.0k"}, // boundary: still < 10k
		{10000, "10k"},  // boundary: switches to integer-k format
		{12345, "12k"},
		{999_999, "999k"}, // just below the M boundary, still in k territory
		{1_000_000, "1.00M"},
		{1_234_567, "1.23M"},
		{9_999_999, "10.00M"}, // boundary: still < 10M
		{10_000_000, "10.0M"}, // boundary: switches to 1-decimal-M format
		{12_345_678, "12.3M"},
		{int64(50_000), "50k"},
		{int32(2_500), "2.5k"},
		{float64(1234), "1.2k"}, // float64 also accepted
		{"abc", "abc"},          // unknown type → falls through to %v
	}
	for _, c := range cases {
		got := callFuncMap(t, "fmtNum", c.in).(string)
		if got != c.want {
			t.Errorf("fmtNum(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFmtUSD(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "$0.00"},
		{0.01, "$0.01"},
		{1.23, "$1.23"},
		{9.99, "$9.99"}, // boundary: still < 10
		{10.0, "$10.0"}, // boundary: switches to 1-decimal
		{12.4, "$12.4"},
		{99.9, "$99.9"},
		{100.0, "$100"},    // boundary: switches to integer + thousand-sep
		{999.4, "$999"},    // truncated by the +0.5 rounding (.4 → floor)
		{1234.5, "$1,235"}, // .5 → rounds up via int64(n+0.5)
		{1_200_000.0, "$1,200,000"},
	}
	for _, c := range cases {
		got := callFuncMap(t, "fmtUSD", c.in).(string)
		if got != c.want {
			t.Errorf("fmtUSD(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFmtPct(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "0.0%"},
		{0.1, "0.1%"},
		{50, "50.0%"},
		{62.4, "62.4%"},
		{99.95, "100.0%"}, // rounds at one decimal
		{100, "100.0%"},
	}
	for _, c := range cases {
		got := callFuncMap(t, "fmtPct", c.in).(string)
		if got != c.want {
			t.Errorf("fmtPct(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestStripScheme(t *testing.T) {
	cases := map[string]string{
		"http://localhost:7456":          "localhost:7456",
		"https://example.com/foo":        "example.com/foo",
		"http://172.17.0.1:8080/v1/path": "172.17.0.1:8080/v1/path",
		"localhost:7456":                 "localhost:7456", // no scheme → passthrough
		"":                               "",
		"ftp://hopefully-not":            "ftp://hopefully-not", // unknown scheme → passthrough
	}
	for in, want := range cases {
		got := callFuncMap(t, "stripScheme", in).(string)
		if got != want {
			t.Errorf("stripScheme(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCommaInt(t *testing.T) {
	cases := map[int64]string{
		0:           "0",
		1:           "1",
		999:         "999",
		1000:        "1,000",
		1234:        "1,234",
		12345:       "12,345",
		1_234_567:   "1,234,567",
		12_345_678:  "12,345,678",
		123_456_789: "123,456,789",
	}
	for in, want := range cases {
		if got := commaInt(in); got != want {
			t.Errorf("commaInt(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestCardDelayMs(t *testing.T) {
	cases := map[int]int{
		0:   0,
		1:   15,
		10:  150,
		29:  435,
		30:  450, // cap
		31:  450, // capped
		100: 450, // capped
	}
	for in, want := range cases {
		got := callFuncMap(t, "cardDelayMs", in).(int)
		if got != want {
			t.Errorf("cardDelayMs(%d) = %d, want %d", in, got, want)
		}
	}
}

func TestChevSVG(t *testing.T) {
	// Returns template.HTML containing the triangle SVG. The path is the
	// design-handoff spec — must include `<svg`, the path `M2 4 L5 7 L8 4 Z`,
	// `fill="currentColor"`, and `aria-hidden="true"`.
	got := string(callFuncMap(t, "chevSVG", nil).(template.HTML))
	for _, want := range []string{
		"<svg",
		`viewBox="0 0 10 10"`,
		`fill="currentColor"`,
		`aria-hidden="true"`,
		`M2 4 L5 7 L8 4 Z`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("chevSVG missing %q in output: %s", want, got)
		}
	}
}

func TestSafeCSS(t *testing.T) {
	// safeCSS wraps a string as template.CSS without modification — used so
	// trusted OKLCH strings inside `style=` attributes don't get autoescaped
	// to ZgotmplZ. The contract is exact passthrough.
	in := "oklch(68% 0.18 263)"
	got := callFuncMap(t, "safeCSS", in).(template.CSS)
	if string(got) != in {
		t.Errorf("safeCSS(%q) = %q, want exact passthrough", in, string(got))
	}
	// Empty input → empty template.CSS (no panic).
	empty := callFuncMap(t, "safeCSS", "").(template.CSS)
	if string(empty) != "" {
		t.Errorf("safeCSS(\"\") = %q, want empty string", string(empty))
	}
}

func TestModelColor(t *testing.T) {
	// modelColor is a thin wrapper over odColorFor that returns template.CSS.
	// Determinism + non-empty are the only contract — odColorFor has its
	// own dedicated test for the hue-distribution claim.
	a := string(callFuncMap(t, "modelColor", "claude-sonnet-4-5").(template.CSS))
	b := string(callFuncMap(t, "modelColor", "claude-sonnet-4-5").(template.CSS))
	if a != b {
		t.Errorf("modelColor is non-deterministic: %q vs %q", a, b)
	}
	if !strings.HasPrefix(a, "oklch(68% 0.18 ") {
		t.Errorf("modelColor produced unexpected format: %q", a)
	}
	// Empty model id → fallback color, not panic.
	empty := string(callFuncMap(t, "modelColor", "").(template.CSS))
	if empty == "" {
		t.Errorf("modelColor(\"\") returned empty; expected fallback color string")
	}
}
