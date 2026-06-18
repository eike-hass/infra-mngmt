package system

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
)

// winDisk is the shape of the winDiskPS JSON payload.
type winDisk struct {
	Drive   string          `json:"drive"`
	Total   int64           `json:"total"`
	Used    int64           `json:"used"`
	Distros json.RawMessage `json:"distros"`
}

type winDistro struct {
	Name     string `json:"name"`
	VhdxPath string `json:"vhdxPath"`
	VhdxSize int64  `json:"vhdxSize"`
}

// parseWinDisk parses the winDiskPS JSON into the Windows drive plus a
// name→vhdx lookup. PowerShell's ConvertTo-Json unwraps a single-element array
// into a bare object, so the distros field is decoded leniently as either an
// array or a single object.
func parseWinDisk(b []byte) (WindowsDrive, map[string]winDistro, error) {
	var wd winDisk
	if err := json.Unmarshal(bytes.TrimSpace(b), &wd); err != nil {
		return WindowsDrive{}, nil, fmt.Errorf("parse windows disk json: %w", err)
	}
	drive := WindowsDrive{Drive: wd.Drive, Total: wd.Total, Used: wd.Used}
	if drive.Drive == "" {
		drive.Drive = "C:"
	}

	byName := map[string]winDistro{}
	for _, d := range decodeOneOrMany[winDistro](wd.Distros) {
		if d.Name != "" {
			byName[d.Name] = d
		}
	}
	return drive, byName, nil
}

// decodeOneOrMany unmarshals a JSON value that may be either a single object
// or an array of objects into a slice — the shape PowerShell's ConvertTo-Json
// produces depending on element count.
func decodeOneOrMany[T any](raw json.RawMessage) []T {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil
	}
	if raw[0] == '[' {
		var many []T
		if err := json.Unmarshal(raw, &many); err == nil {
			return many
		}
		return nil
	}
	var one T
	if err := json.Unmarshal(raw, &one); err == nil {
		return []T{one}
	}
	return nil
}

// ReadDisk gathers the full DiskInfo: the Windows host drive, every WSL distro,
// each distro's backing vhdx size, and the in-guest filesystem usage of every
// running distro. It is best-effort and degrades per-source: a failure to read
// the distro list (the core of the card) sets Err; lesser failures (a missing
// vhdx, an un-measurable stopped distro) just leave the corresponding fields
// zero/unknown.
//
// Only callable on the WSL host (it spawns wsl.exe + powershell.exe via
// interop); elsewhere resolveExe fails fast and Err is set.
func ReadDisk(ctx context.Context) DiskInfo {
	wslExe, err := wslPath()
	if err != nil {
		return DiskInfo{Err: err.Error()}
	}
	listOut, err := runCapture(ctx, wslExe, "--list", "--verbose")
	if err != nil {
		return DiskInfo{Err: fmt.Sprintf("wsl --list --verbose: %v", err)}
	}
	distros := parseWSLList(decodeMaybeUTF16(listOut))

	info := DiskInfo{Distros: distros}

	// Windows drive + per-distro vhdx sizes (one PowerShell batch).
	if psExe, perr := powershellPath(); perr == nil {
		if out, rerr := runCapture(ctx, psExe, "-NoProfile", "-NonInteractive", "-Command", winDiskPS); rerr == nil {
			if drive, byName, jerr := parseWinDisk(out); jerr == nil {
				info.Windows = drive
				for i := range info.Distros {
					if wd, ok := byName[info.Distros[i].Name]; ok {
						info.Distros[i].VhdxSize = wd.VhdxSize
						info.Distros[i].VhdxPath = wd.VhdxPath
					}
				}
			}
		}
	}

	// In-guest filesystem usage — running distros only (don't boot stopped ones).
	for i := range info.Distros {
		if !info.Distros[i].Running() {
			continue
		}
		if total, used, ok := distroFsUsage(ctx, wslExe, info.Distros[i].Name); ok {
			info.Distros[i].FsTotal = total
			info.Distros[i].FsUsed = used
			info.Distros[i].FsKnown = true
		}
	}
	return info
}

// TagEngines marks which distro hosts each engine, using the hosting-distro
// name each engine reports. Used only for cosmetic sub-labels and footer copy;
// the prune→fsUsed causal chain is kept honest by re-reading after each op, not
// by this tag.
func (d *DiskInfo) TagEngines(dockerDistro, podmanDistro string) {
	for i := range d.Distros {
		switch d.Distros[i].Name {
		case dockerDistro:
			d.Distros[i].Engine = "docker"
		case podmanDistro:
			d.Distros[i].Engine = "podman"
		}
	}
}
