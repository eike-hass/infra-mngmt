package bridge

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"log"
	"os"
	"os/exec"
	"sort"
	"strings"
	"unicode/utf16"
)

// Filter selects bridges by name. Empty names returns all. Unknown names are
// silently skipped (callers can detect by comparing lengths).
func Filter(bridges []Bridge, names []string) []Bridge {
	if len(names) == 0 {
		return append([]Bridge(nil), bridges...)
	}
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}
	var out []Bridge
	for _, b := range bridges {
		if want[b.Name] {
			out = append(out, b)
		}
	}
	return out
}

// FormatStatusTable renders a fixed-width status table for human consumption.
// states must align with bridges by index.
func FormatStatusTable(bridges []Bridge, states []State) string {
	if len(bridges) == 0 {
		return "(no bridges configured)\n"
	}
	type row struct{ name, tier, listen, connect, state string }
	rows := make([]row, len(bridges))
	for i, b := range bridges {
		listen := fmt.Sprintf("%s:%d", b.Listen.Addr, b.Listen.Port)
		connect := fmt.Sprintf("%s:%d", b.Connect.Addr, b.Connect.Port)
		st := "—"
		if i < len(states) {
			st = states[i].String()
		}
		rows[i] = row{b.Name, string(b.Tier), listen, connect, st}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].name < rows[j].name })

	cols := []string{"NAME", "TIER", "LISTEN", "CONNECT", "STATE"}
	widths := make([]int, len(cols))
	for i, c := range cols {
		widths[i] = len(c)
	}
	for _, r := range rows {
		fields := []string{r.name, r.tier, r.listen, r.connect, r.state}
		for i, f := range fields {
			if len(f) > widths[i] {
				widths[i] = len(f)
			}
		}
	}
	var out strings.Builder
	writeRow := func(fields []string) {
		for i, f := range fields {
			if i > 0 {
				out.WriteString("  ")
			}
			fmt.Fprintf(&out, "%-*s", widths[i], f)
		}
		out.WriteByte('\n')
	}
	writeRow(cols)
	for _, r := range rows {
		writeRow([]string{r.name, r.tier, r.listen, r.connect, r.state})
	}
	return out.String()
}

// PortproxyShow runs `netsh interface portproxy show all` via powershell.exe
// and returns the captured stdout. Used by `bridges status`.
func PortproxyShow() (string, error) {
	psPath, err := resolvePowerShell()
	if err != nil {
		return "", err
	}
	cmd := exec.Command(psPath, "-NoProfile", "-NonInteractive", "-Command", "netsh interface portproxy show all")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("netsh portproxy show: %w", err)
	}
	return string(out), nil
}

// resolvePowerShell locates powershell.exe robustly enough to work under
// systemd, where WSL interop's automatic PATH injection doesn't apply.
// Tries $PATH first, then well-known absolute locations under both common
// WSL automount roots (/c/... per user-style /etc/wsl.conf, /mnt/c/... per
// default) before giving up.
func resolvePowerShell() (string, error) {
	if path, err := exec.LookPath("powershell.exe"); err == nil {
		return path, nil
	}
	candidates := []string{
		"/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe",
		"/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe",
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("powershell.exe not found in $PATH or known WSL paths (/c/Windows/System32/WindowsPowerShell/v1.0/, /mnt/c/...) — set PATH in the systemd unit or symlink the binary")
}

// RunPowerShellElevated executes script as a single elevated PowerShell
// invocation, blocking until it exits. The script is passed via
// `-EncodedCommand` (base64 UTF-16LE) instead of a temp file — earlier
// implementations wrote a .ps1 to /tmp and translated via wslpath, but
// elevated Windows processes can't reliably read the resulting `\\wsl$\…`
// UNC paths, which manifested as "click apply, nothing happens".
//
// Requires:
//   - An interactive Windows desktop session (UAC has nowhere to display
//     otherwise — clicks vanish silently).
//   - `powershell.exe` on PATH (WSL2 interop default).
//
// Returns an error from the *outer* powershell.exe (the one we spawn here).
// The elevated child's stdout/stderr are not captured (it runs in a separate
// session); side-effect verification via PortproxyShow is the only way to
// know whether the rules actually applied.
func RunPowerShellElevated(script string) error {
	if script == "" {
		return nil
	}
	psPath, err := resolvePowerShell()
	if err != nil {
		return err
	}
	encoded := encodePSCommand(script)
	psCmd := fmt.Sprintf(
		"Start-Process powershell -Verb RunAs -Wait -ArgumentList '-NoProfile','-ExecutionPolicy','Bypass','-EncodedCommand','%s'",
		encoded,
	)
	log.Printf("bridge: launching elevated PowerShell via %s (%d bytes encoded; UAC prompt should appear)", psPath, len(encoded))

	// Drop -NonInteractive on the outer call: Start-Process wraps the
	// elevation, but if the outer host happens to want a console prompt for
	// some unrelated reason we'd rather see it than have it suppressed.
	cmd := exec.Command(psPath, "-NoProfile", "-Command", psCmd)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("elevated run failed: %w (UAC denied? no active Windows session? check the systemd journal for stderr)", err)
	}
	log.Printf("bridge: elevated PowerShell returned successfully")
	return nil
}

// encodePSCommand returns base64(UTF-16LE(script)), the format expected by
// `powershell.exe -EncodedCommand`. Sidesteps the WSL temp-file UNC path
// problem and any quoting issues with arbitrary script content.
func encodePSCommand(script string) string {
	u16 := utf16.Encode([]rune(script))
	buf := make([]byte, 2*len(u16))
	for i, r := range u16 {
		binary.LittleEndian.PutUint16(buf[i*2:], r)
	}
	return base64.StdEncoding.EncodeToString(buf)
}
