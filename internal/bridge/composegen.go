package bridge

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ComposeFragment is the on-disk shape of process-compose.bridges.yaml. The
// user's main process-compose.yaml references it via `extends:`, so PC merges
// the two configs at startup and at every reload. infra-mngmt fully owns this
// file — `bridges apply` rewrites it from scratch on every invocation.
type ComposeFragment struct {
	Version   string                          `yaml:"version"`
	Processes map[string]composeFragmentEntry `yaml:"processes"`
}

type composeFragmentEntry struct {
	Command        string                      `yaml:"command"`
	Description    string                      `yaml:"description,omitempty"`
	Namespace      string                      `yaml:"namespace,omitempty"`
	Availability   composeFragmentAvailability `yaml:"availability"`
	ReadinessProbe *composeFragmentExecProbe   `yaml:"readiness_probe,omitempty"`
}

type composeFragmentAvailability struct {
	Restart        string `yaml:"restart"`
	BackoffSeconds int    `yaml:"backoff_seconds,omitempty"`
}

type composeFragmentExecProbe struct {
	Exec                composeFragmentExec `yaml:"exec"`
	InitialDelaySeconds int                 `yaml:"initial_delay_seconds,omitempty"`
	PeriodSeconds       int                 `yaml:"period_seconds,omitempty"`
	TimeoutSeconds      int                 `yaml:"timeout_seconds,omitempty"`
	FailureThreshold    int                 `yaml:"failure_threshold,omitempty"`
}

type composeFragmentExec struct {
	Command string `yaml:"command"`
}

// BridgeProcessName returns the process-compose process name used for a wsl-
// tier socat bridge. Prefixed with "bridge-" so they're easy to spot in `pc
// process list` and to avoid collisions with user-defined processes.
func BridgeProcessName(br *Bridge) string {
	return "bridge-" + br.Name
}

// RenderComposeFragment builds the process-compose fragment that runs every
// tier=wsl, type=socat bridge in the given file. Bridges of other tiers are
// ignored — Windows portproxy is netsh state, not a process.
//
// Even when no WSL bridges are configured, the returned fragment has an empty
// processes map (and a valid version string) so the on-disk file always
// exists. That's important: the user's main process-compose.yaml references
// the fragment via `extends:`, and PC fails to start if the extended path is
// missing.
//
// Sentinel resolution:
//   - `${windows-host-ip}` on the connect side → an inline backtick subshell
//     in the command that runs `ip route | awk '/^default/ {print $3}'` at
//     socat exec time. We tried `env_cmds` first, but PC's reload endpoint
//     (`POST /project/configuration`) doesn't re-evaluate env_cmds on all
//     versions — only a full restart does. Backticks resolve every time
//     socat is (re)launched, which is correct: a `wsl --shutdown` cycle
//     bounces PC + restarts each bridge process with a fresh IP, and a
//     reload after `bridges apply` similarly restarts them. Backticks are
//     invisible to envsubst (it only recognizes `$`/`${}`/`$$`), so they
//     pass through to bash unchanged.
//   - `${wsl-host-ip}` on the listen side → not yet supported; bridges using
//     it will fall through to an empty `bind=` and a loud failure.
func RenderComposeFragment(bridges []Bridge) ComposeFragment {
	frag := ComposeFragment{
		Version:   "0.5",
		Processes: map[string]composeFragmentEntry{},
	}
	for i := range bridges {
		b := &bridges[i]
		if b.Tier != TierWSL || b.Type != TypeSocat {
			continue
		}
		// Render the socat command with the windows-host-ip sentinel
		// rewritten as a bash backtick subshell. Then escape `$` → `$$` so
		// process-compose's drone/envsubst preprocessor passes any dollar in
		// the awk action (`$3`) through to bash unchanged.
		cmd := escapeDollar(socatCommandForFragment(b))
		desc := escapeDollar(fmt.Sprintf("socat relay for bridge %q (%s:%d → %s:%d)", b.Name, b.Listen.Addr, b.Listen.Port, b.Connect.Addr, b.Connect.Port))
		frag.Processes[BridgeProcessName(b)] = composeFragmentEntry{
			Command:     cmd,
			Description: desc,
			Namespace:   "bridges",
			Availability: composeFragmentAvailability{
				Restart:        "always",
				BackoffSeconds: 2,
			},
			ReadinessProbe: &composeFragmentExecProbe{
				// `nc -z` returns 0 if the listener is reachable on the
				// configured listen IP/port. Cheap and works without
				// extra dependencies on most distros.
				Exec: composeFragmentExec{
					Command: fmt.Sprintf("nc -z %s %d", listenAddrForProbe(b), b.Listen.Port),
				},
				InitialDelaySeconds: 1,
				PeriodSeconds:       10,
				TimeoutSeconds:      2,
				FailureThreshold:    3,
			},
		}
	}
	return frag
}

// socatCommandForFragment renders the bash command that the fragment YAML
// will hand to process-compose. It deliberately doesn't reuse SocatCommand —
// that helper substitutes `${windows-host-ip}` with `$(infra-mngmt addr ...)`
// for human-friendly CLI hints, but the spawned PC process won't have
// infra-mngmt on PATH (systemd's user PATH excludes ~/.local/bin), so the
// subshell evaluates to empty and socat ends up forwarding to localhost.
//
// For the WSL fragment we substitute the sentinel with a backtick subshell
// running `ip route | awk '/^default/ {print $3}'`. Backticks are invisible
// to drone/envsubst, so they pass through to bash unchanged; bash evaluates
// them at every socat exec, picking up whatever the current default-route
// gateway is. iproute2 is stock on every WSL2 distro and absolute path
// works under systemd's reduced user PATH.
func socatCommandForFragment(br *Bridge) string {
	listen := br.Listen.Addr // ${wsl-host-ip} will pass through escaped; see Render docs.
	connect := br.Connect.Addr
	if connect == "${windows-host-ip}" {
		connect = windowsHostIPShellExpr
	}
	return fmt.Sprintf("socat TCP-LISTEN:%d,fork,reuseaddr,bind=%s TCP:%s:%d",
		br.Listen.Port, listen, connect, br.Connect.Port)
}

// windowsHostIPShellExpr is the bash backtick expression that resolves WSL2's
// default-route gateway (= the Windows host IP under NAT mode). Embedded
// directly in the socat command line so it re-resolves on every (re)start.
//
// iproute2's `ip route` produces a parseable "default via <gw> dev <iface>"
// line on every stock WSL2 distro. We use absolute /usr/sbin/ip because
// systemd's user PATH excludes /usr/sbin on some distros. The raw `$3` in
// the awk action would be eaten by envsubst as a variable reference; once
// the surrounding `escapeDollar` step doubles dollars, this becomes `$$3`
// in the YAML and awk gets `$3` after envsubst at PC parse time.
const windowsHostIPShellExpr = "`/usr/sbin/ip route | awk '/^default/ {print $3; exit}'`"

// MarshalComposeFragment renders the fragment to YAML bytes with a leading
// header comment that warns the user not to hand-edit the file.
func MarshalComposeFragment(frag ComposeFragment) ([]byte, error) {
	body, err := yaml.Marshal(frag)
	if err != nil {
		return nil, fmt.Errorf("marshal compose fragment: %w", err)
	}
	// Stable key order: yaml.Marshal sorts map keys alphabetically, but we
	// run the keys ourselves first to get a deterministic message in the
	// preface. Useful for diffing two apply runs.
	names := make([]string, 0, len(frag.Processes))
	for k := range frag.Processes {
		names = append(names, k)
	}
	sort.Strings(names)

	var b strings.Builder
	b.WriteString("# Generated by `infra-mngmt bridges apply` — do not edit by hand.\n")
	b.WriteString("# This fragment runs tier=wsl, type=socat bridges declared in bridges.yaml.\n")
	b.WriteString("# The user's main process-compose.yaml is expected to reference it via:\n")
	b.WriteString("#     extends: process-compose.bridges.yaml\n")
	if len(names) == 0 {
		b.WriteString("# (no wsl/socat bridges configured)\n")
	} else {
		fmt.Fprintf(&b, "# Generated processes: %s\n", strings.Join(names, ", "))
	}
	b.WriteByte('\n')
	b.Write(body)
	return []byte(b.String()), nil
}

// escapeDollar doubles every `$` so process-compose's envsubst preprocessor
// passes them through verbatim. After envsubst, `$$` → `$`, and bash gets
// the original string for command-time evaluation. Without this, envsubst
// fails on bash command substitutions like `$(cmd)` and on hyphenated
// "variables" like `${windows-host-ip}` (the hyphen ends the identifier in
// POSIX var name rules, leaving an unclosed `${...` that breaks the parser).
func escapeDollar(s string) string {
	return strings.ReplaceAll(s, "$", "$$")
}

// listenAddrForProbe returns the address used by the readiness probe's
// `nc -z` check. Mirrors SocatCommand's substitution but resolves the listen
// addr as something a probe inside the same WSL host can dial — which for
// literal IPs is just the IP, and for ${wsl-host-ip} is best-served by
// 127.0.0.1 since the listener will accept on whatever interface PC bound.
func listenAddrForProbe(br *Bridge) string {
	if br.Listen.Addr == "${wsl-host-ip}" {
		return "127.0.0.1"
	}
	return br.Listen.Addr
}

// WriteFragment renders allBridges into the process-compose fragment file at
// path. The render is declarative: the file is rewritten from scratch each
// call. Returns the count of socat processes written so callers can log
// progress. Atomic via temp file + rename — readers (process-compose tailing
// the file on reload) never see a half-written fragment.
//
// Always succeeds writing even when no WSL/socat bridges are present (writes
// an empty processes map). The on-disk file always exists, which matters
// because the user's main process-compose.yaml references it via `extends:`
// and PC fails to start if the extended path is missing.
func WriteFragment(allBridges []Bridge, path string) (int, error) {
	frag := RenderComposeFragment(allBridges)
	data, err := MarshalComposeFragment(frag)
	if err != nil {
		return 0, fmt.Errorf("marshal fragment: %w", err)
	}
	if err := writeAtomic(path, data, 0o644); err != nil {
		return 0, fmt.Errorf("write fragment: %w", err)
	}
	return len(frag.Processes), nil
}

// writeAtomic writes data to path via a temp file in the same directory and a
// rename — ensures readers never see a half-written file.
func writeAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".bridges.*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		// Clean up the temp file if rename didn't claim it.
		if _, statErr := os.Stat(tmpPath); statErr == nil {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
