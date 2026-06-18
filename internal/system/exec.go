package system

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"unicode/utf16"
)

// commandContext is the exec hook, overridable in tests. It mirrors
// exec.CommandContext so a fake can intercept without real subprocesses.
var commandContext = exec.CommandContext

// resolveExe locates a Windows interop binary (powershell.exe / wsl.exe) the
// same way the bridge runner does — $PATH first (WSL2 interop default), then
// the well-known absolute locations under both common automount roots, so it
// keeps working under systemd where interop's PATH injection doesn't apply.
//
// Mirrors internal/bridge/runner.go's resolvePowerShell deliberately rather
// than sharing it: the bridge resolver is unexported and bridge-specific, and
// the System reads must not pull in the bridge package's elevation machinery.
func resolveExe(name string, fallbacks ...string) (string, error) {
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	for _, p := range fallbacks {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("%s not found in $PATH or known WSL interop paths — System disk reads require Windows interop (run on the WSL host, not in a container)", name)
}

func powershellPath() (string, error) {
	return resolveExe("powershell.exe",
		"/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe",
		"/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe",
	)
}

func wslPath() (string, error) {
	return resolveExe("wsl.exe",
		"/c/Windows/System32/wsl.exe",
		"/mnt/c/Windows/System32/wsl.exe",
	)
}

// runCapture runs a command and returns its stdout, capturing stderr in the
// error so failures are diagnosable (the bridge runner's elevated path can't
// capture; these non-elevated reads can and do).
func runCapture(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := commandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := bytes.TrimSpace(stderr.Bytes())
		if len(msg) > 0 {
			return nil, fmt.Errorf("%s: %w: %s", name, err, decodeMaybeUTF16(msg))
		}
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return stdout.Bytes(), nil
}

// decodeMaybeUTF16 decodes wsl.exe / PowerShell output, which on Windows is
// frequently UTF-16LE (with or without a BOM). Plain UTF-8/ASCII passes
// through unchanged. WSL's `--list --verbose` is the canonical UTF-16LE
// offender — without this, every other byte is a NUL and parsing fails.
func decodeMaybeUTF16(b []byte) string {
	// BOM-led UTF-16LE.
	if len(b) >= 2 && b[0] == 0xFF && b[1] == 0xFE {
		return decodeUTF16LE(b[2:])
	}
	// Heuristic: a NUL in the first 64 bytes at an odd-even ASCII cadence
	// means UTF-16LE without a BOM (ASCII chars become "X\x00").
	if looksUTF16LE(b) {
		return decodeUTF16LE(b)
	}
	return string(b)
}

func looksUTF16LE(b []byte) bool {
	n := len(b)
	if n > 64 {
		n = 64
	}
	if n < 2 {
		return false
	}
	nul := 0
	for i := 1; i < n; i += 2 {
		if b[i] == 0 {
			nul++
		}
	}
	// Most high bytes being NUL ⇒ ASCII-range text encoded as UTF-16LE.
	return nul >= n/4
}

func decodeUTF16LE(b []byte) string {
	if len(b)%2 != 0 {
		b = b[:len(b)-1]
	}
	u16 := make([]uint16, len(b)/2)
	for i := range u16 {
		u16[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
	}
	return string(utf16.Decode(u16))
}

// winDiskPS reads the Windows drive free/total and enumerates each registered
// WSL distro's ext4.vhdx size from the Lxss registry, emitting one JSON object.
// Kept as a single batch so the whole Windows-side read costs one interop spawn
// + one UAC-free PowerShell start. SilentlyContinue so a missing vhdx or an
// odd registry entry degrades to size 0 rather than aborting the batch.
const winDiskPS = `$ErrorActionPreference='SilentlyContinue'
$d = Get-PSDrive -Name C
$distros = @()
Get-ChildItem 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Lxss' | ForEach-Object {
  $bp = $_.GetValue('BasePath'); $n = $_.GetValue('DistributionName')
  if ($n) {
    $vhdx = Join-Path $bp 'ext4.vhdx'
    $sz = 0; if (Test-Path -LiteralPath $vhdx) { $sz = (Get-Item -LiteralPath $vhdx).Length }
    $distros += [pscustomobject]@{ name=$n; vhdxPath=$vhdx; vhdxSize=[int64]$sz }
  }
}
[pscustomobject]@{ drive='C:'; total=[int64]($d.Used + $d.Free); used=[int64]$d.Used; distros=$distros } | ConvertTo-Json -Depth 4 -Compress`
