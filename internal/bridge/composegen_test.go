package bridge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestRenderComposeFragmentEmpty(t *testing.T) {
	frag := RenderComposeFragment(nil)
	if frag.Version == "" {
		t.Errorf("expected non-empty version, got %q", frag.Version)
	}
	if frag.Processes == nil {
		t.Fatal("expected non-nil processes map (must round-trip to `processes: {}`)")
	}
	if len(frag.Processes) != 0 {
		t.Errorf("expected zero processes, got %d", len(frag.Processes))
	}
}

func TestRenderComposeFragmentSkipsNonWSLSocat(t *testing.T) {
	bridges := []Bridge{
		// Windows portproxy — not a process, must be ignored.
		{Name: "win-llama", Tier: TierWindows, Type: TypePortproxy,
			Listen:  Endpoint{Addr: "${wsl-host-ip}", Port: 8080},
			Connect: Endpoint{Addr: "127.0.0.1", Port: 8080, Family: FamilyAuto}},
		// WSL socat — included.
		{Name: "wsl-relay", Tier: TierWSL, Type: TypeSocat,
			Listen:  Endpoint{Addr: "172.17.0.1", Port: 8080},
			Connect: Endpoint{Addr: "${windows-host-ip}", Port: 8080, Family: FamilyAuto}},
	}
	frag := RenderComposeFragment(bridges)
	if _, has := frag.Processes["bridge-win-llama"]; has {
		t.Error("windows bridge should not appear in fragment processes")
	}
	if _, has := frag.Processes["bridge-wsl-relay"]; !has {
		t.Error("wsl/socat bridge missing from fragment processes")
	}
}

func TestRenderComposeFragmentEntryShape(t *testing.T) {
	br := Bridge{
		Name:    "llama-cpp-relay",
		Tier:    TierWSL,
		Type:    TypeSocat,
		Listen:  Endpoint{Addr: "172.17.0.1", Port: 8080},
		Connect: Endpoint{Addr: "${windows-host-ip}", Port: 8080, Family: FamilyAuto},
	}
	frag := RenderComposeFragment([]Bridge{br})
	entry, ok := frag.Processes["bridge-llama-cpp-relay"]
	if !ok {
		t.Fatal("expected process bridge-llama-cpp-relay in fragment")
	}
	if !strings.Contains(entry.Command, "TCP-LISTEN:8080") {
		t.Errorf("expected TCP-LISTEN:8080 in command, got %q", entry.Command)
	}
	if !strings.Contains(entry.Command, "bind=172.17.0.1") {
		t.Errorf("expected bind=172.17.0.1 in command, got %q", entry.Command)
	}
	// Connect side resolves via a bash backtick subshell (`/usr/sbin/ip
	// route | awk ...`) that bash evaluates at every socat exec. Don't use
	// `$(infra-mngmt addr ...)` — infra-mngmt isn't on systemd's user PATH,
	// the subshell evaluates to empty, and socat ends up forwarding to
	// localhost (`TCP::8080` → infinite loopback). Don't use env_cmds —
	// PC's `/project/configuration` reload doesn't re-evaluate them on all
	// versions, so an updated IP after `wsl --shutdown` mid-session wouldn't
	// reach the spawned bridge processes.
	if !strings.Contains(entry.Command, "/usr/sbin/ip route") {
		t.Errorf("expected ip-route subshell in command, got %q", entry.Command)
	}
	if !strings.Contains(entry.Command, "`") {
		t.Errorf("expected backtick subshell (envsubst-invisible) in command, got %q", entry.Command)
	}
	if strings.Contains(entry.Command, "infra-mngmt addr") {
		t.Errorf("command should not depend on infra-mngmt being on PATH; got %q", entry.Command)
	}
	if strings.Contains(entry.Command, "WINDOWS_HOST_IP") {
		t.Errorf("command should not reference env_cmds-injected vars (PC reload doesn't re-run env_cmds); got %q", entry.Command)
	}
	// Single `$(` (without preceding `$`) would mean envsubst-broken.
	stripped := strings.ReplaceAll(entry.Command, "$$", "")
	if strings.Contains(stripped, "$(") {
		t.Errorf("command has unescaped $( — envsubst would fail; got %q", entry.Command)
	}
	// Description should also have escaped `$` so the literal `${windows-host-ip}`
	// from the bridge sentinel doesn't crash envsubst.
	if !strings.Contains(entry.Description, "$${windows-host-ip}") {
		t.Errorf("expected escaped sentinel in description, got %q", entry.Description)
	}
	if entry.Availability.Restart != "always" {
		t.Errorf("expected restart=always, got %q", entry.Availability.Restart)
	}
	if entry.Namespace != "bridges" {
		t.Errorf("expected namespace=bridges, got %q", entry.Namespace)
	}
	if entry.ReadinessProbe == nil {
		t.Fatal("expected readiness_probe to be set")
	}
	if !strings.Contains(entry.ReadinessProbe.Exec.Command, "nc -z 172.17.0.1 8080") {
		t.Errorf("expected nc probe targeting listen addr, got %q", entry.ReadinessProbe.Exec.Command)
	}
}

func TestRenderComposeFragmentLiteralConnectStays(t *testing.T) {
	// When the bridge specifies a literal connect addr (no sentinel), the
	// fragment must use it verbatim — no spurious subshells, no env vars.
	br := Bridge{
		Name: "literal-only", Tier: TierWSL, Type: TypeSocat,
		Listen:  Endpoint{Addr: "172.17.0.1", Port: 8080},
		Connect: Endpoint{Addr: "172.18.0.1", Port: 8080, Family: FamilyAuto},
	}
	frag := RenderComposeFragment([]Bridge{br})
	entry := frag.Processes["bridge-literal-only"]
	if !strings.Contains(entry.Command, "TCP:172.18.0.1:8080") {
		t.Errorf("literal connect addr should appear unchanged; got %q", entry.Command)
	}
	if strings.Contains(entry.Command, "ip route") || strings.Contains(entry.Command, "`") {
		t.Errorf("no subshell expected for literal connect addr; got %q", entry.Command)
	}
}

func TestEscapeDollarNoLooseDollarsInRenderedFragment(t *testing.T) {
	// Regression: the rendered YAML must never contain a single `$` that
	// envsubst would try to expand. This guards against future helpers
	// that accidentally inject sentinels or command substitutions.
	br := Bridge{
		Name: "wsl-x", Tier: TierWSL, Type: TypeSocat,
		Listen:  Endpoint{Addr: "${wsl-host-ip}", Port: 9000},
		Connect: Endpoint{Addr: "${windows-host-ip}", Port: 9000, Family: FamilyAuto},
	}
	frag := RenderComposeFragment([]Bridge{br})
	data, err := MarshalComposeFragment(frag)
	if err != nil {
		t.Fatal(err)
	}
	// Every `$` in the rendered YAML must be either part of `$$` (escape) or
	// within the literal "$$" sequence. Walk the bytes and complain on any
	// lone `$`.
	body := string(data)
	for i := 0; i < len(body); i++ {
		if body[i] != '$' {
			continue
		}
		// Either we're at the second `$` of a pair, or the next char is `$`.
		paired := (i > 0 && body[i-1] == '$') || (i+1 < len(body) && body[i+1] == '$')
		if !paired {
			t.Errorf("lone `$` at byte %d in rendered fragment — envsubst will fail. Context: %q", i, body[max(0, i-30):min(len(body), i+30)])
		}
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestRenderComposeFragmentWSLHostListen(t *testing.T) {
	// ${wsl-host-ip} on listen side: probe falls back to 127.0.0.1 (since
	// we're probing from inside the same WSL host).
	br := Bridge{
		Name:    "wsl-listen",
		Tier:    TierWSL,
		Type:    TypeSocat,
		Listen:  Endpoint{Addr: "${wsl-host-ip}", Port: 9000},
		Connect: Endpoint{Addr: "10.0.0.1", Port: 9000, Family: FamilyAuto},
	}
	frag := RenderComposeFragment([]Bridge{br})
	entry := frag.Processes["bridge-wsl-listen"]
	if !strings.Contains(entry.ReadinessProbe.Exec.Command, "nc -z 127.0.0.1 9000") {
		t.Errorf("expected probe to use 127.0.0.1 fallback for ${wsl-host-ip} listen, got %q", entry.ReadinessProbe.Exec.Command)
	}
}

func TestMarshalComposeFragmentRoundTrip(t *testing.T) {
	bridges := []Bridge{
		{Name: "a", Tier: TierWSL, Type: TypeSocat,
			Listen:  Endpoint{Addr: "172.17.0.1", Port: 5000},
			Connect: Endpoint{Addr: "127.0.0.1", Port: 5000, Family: FamilyAuto}},
		{Name: "b", Tier: TierWSL, Type: TypeSocat,
			Listen:  Endpoint{Addr: "172.17.0.1", Port: 5001},
			Connect: Endpoint{Addr: "127.0.0.1", Port: 5001, Family: FamilyAuto}},
	}
	frag := RenderComposeFragment(bridges)
	data, err := MarshalComposeFragment(frag)
	if err != nil {
		t.Fatalf("MarshalComposeFragment: %v", err)
	}
	if !strings.HasPrefix(string(data), "# Generated by") {
		t.Errorf("expected leading header comment; got: %s", firstLine(string(data)))
	}
	if !strings.Contains(string(data), "extends: process-compose.bridges.yaml") {
		t.Errorf("expected extends: hint in header; got: %s", string(data))
	}

	// Round-trip: re-parse and verify both processes survive.
	var parsed ComposeFragment
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("parse generated YAML: %v", err)
	}
	if parsed.Version != "0.5" {
		t.Errorf("parsed version = %q, want 0.5", parsed.Version)
	}
	for _, name := range []string{"bridge-a", "bridge-b"} {
		if _, has := parsed.Processes[name]; !has {
			t.Errorf("process %q missing after round-trip", name)
		}
	}
}

func TestMarshalComposeFragmentEmptyHasNote(t *testing.T) {
	frag := RenderComposeFragment(nil)
	data, err := MarshalComposeFragment(frag)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "(no wsl/socat bridges configured)") {
		t.Errorf("expected empty marker in header; got: %s", string(data))
	}
}

func TestBridgeProcessName(t *testing.T) {
	got := BridgeProcessName(&Bridge{Name: "llama-cpp-relay"})
	if got != "bridge-llama-cpp-relay" {
		t.Errorf("BridgeProcessName: got %q, want bridge-llama-cpp-relay", got)
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func TestWriteFragmentWritesAndOverwrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "frag.yaml")

	// Initial write with a single bridge.
	count, err := WriteFragment([]Bridge{
		{Name: "first", Tier: TierWSL, Type: TypeSocat,
			Listen:  Endpoint{Addr: "172.17.0.1", Port: 5000},
			Connect: Endpoint{Addr: "127.0.0.1", Port: 5000, Family: FamilyAuto}},
	}, path)
	if err != nil {
		t.Fatalf("first WriteFragment: %v", err)
	}
	if count != 1 {
		t.Errorf("expected count=1, got %d", count)
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "bridge-first") {
		t.Fatalf("first write missing process: data=%s err=%v", string(data), err)
	}

	// Rewrite with empty list — file overwritten cleanly, no leftover entries.
	count, err = WriteFragment(nil, path)
	if err != nil {
		t.Fatalf("second WriteFragment: %v", err)
	}
	if count != 0 {
		t.Errorf("expected count=0 after empty rewrite, got %d", count)
	}
	data, err = os.ReadFile(path)
	if err != nil || strings.Contains(string(data), "bridge-first") {
		t.Errorf("second write should drop old entries; got: %s", string(data))
	}

	// No leftover temp files.
	matches, _ := filepath.Glob(filepath.Join(dir, ".bridges.*.tmp"))
	if len(matches) != 0 {
		t.Errorf("temp files left behind: %v", matches)
	}
}

func TestWriteFragmentCreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "subdir", "frag.yaml")
	if _, err := WriteFragment(nil, path); err != nil {
		t.Fatalf("WriteFragment: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("file not created in nested dir: %v", err)
	}
}

func TestWriteFragmentSkipsWindows(t *testing.T) {
	// Mixed input: Windows bridges should be filtered out, only WSL/socat
	// entries written. Verifies WriteFragment delegates to RenderComposeFragment.
	dir := t.TempDir()
	path := filepath.Join(dir, "frag.yaml")
	count, err := WriteFragment([]Bridge{
		{Name: "win", Tier: TierWindows, Type: TypePortproxy,
			Listen:   Endpoint{Addr: "${wsl-host-ip}", Port: 80},
			Connect:  Endpoint{Addr: "127.0.0.1", Port: 80, Family: FamilyAuto},
			Firewall: Firewall{DisplayName: "x", Remote: "172.18.0.0/16"}},
		{Name: "wsl", Tier: TierWSL, Type: TypeSocat,
			Listen:  Endpoint{Addr: "172.17.0.1", Port: 5000},
			Connect: Endpoint{Addr: "127.0.0.1", Port: 5000, Family: FamilyAuto}},
	}, path)
	if err != nil {
		t.Fatalf("WriteFragment: %v", err)
	}
	if count != 1 {
		t.Errorf("expected count=1 (windows skipped), got %d", count)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "bridge-win") {
		t.Errorf("windows bridge should not appear in fragment: %s", string(data))
	}
}
