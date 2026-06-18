package system

import (
	"context"
	"strconv"
	"strings"
)

// parseWSLList parses `wsl --list --verbose` output (already decoded from
// UTF-16LE). The table looks like:
//
//	  NAME                     STATE           VERSION
//	* Ubuntu-24.04             Running         2
//	  podman-machine-default   Running         2
//	  Ubuntu-22.04             Stopped         2
//
// The leading "*" marks the default distro. Localized headers vary, so the
// header row is detected by content (a line containing both NAME and STATE)
// rather than by position, and skipped.
func parseWSLList(s string) []Distro {
	var out []Distro
	for _, raw := range strings.Split(s, "\n") {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// Skip the header row (matched by content to survive localization).
		up := strings.ToUpper(trimmed)
		if strings.Contains(up, "NAME") && strings.Contains(up, "STATE") {
			continue
		}

		def := false
		if strings.HasPrefix(trimmed, "*") {
			def = true
			trimmed = strings.TrimSpace(trimmed[1:])
		}
		fields := strings.Fields(trimmed)
		if len(fields) < 3 {
			continue
		}
		// NAME is fields[0]; the last two fields are STATE and VERSION.
		ver, _ := strconv.Atoi(fields[len(fields)-1])
		state := normalizeState(fields[len(fields)-2])
		name := strings.Join(fields[:len(fields)-2], " ")
		out = append(out, Distro{
			Name:    name,
			Version: ver,
			Default: def,
			State:   state,
		})
	}
	return out
}

// normalizeState canonicalizes WSL's state strings to "Running"/"Stopped".
// WSL reports "Running", "Stopped", or transient "Installing"/"Converting";
// anything not clearly running is treated as stopped for display purposes.
func normalizeState(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "running":
		return "Running"
	default:
		return "Stopped"
	}
}

// parseDF parses `df -B1 <mount>` output and returns the total and used bytes
// of the data row. Returns ok=false if no usable data row is found.
//
//	Filesystem      1B-blocks          Used     Available Use% Mounted on
//	/dev/sdd     1081101176832  213674192896  812363405312  21% /
func parseDF(out string) (total, used int64, ok bool) {
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimSpace(strings.TrimRight(raw, "\r"))
		if line == "" || strings.HasPrefix(line, "Filesystem") {
			continue
		}
		f := strings.Fields(line)
		// A data row has at least: fs, total, used, avail, use%, mount.
		if len(f) < 4 {
			continue
		}
		t, errT := strconv.ParseInt(f[1], 10, 64)
		u, errU := strconv.ParseInt(f[2], 10, 64)
		if errT != nil || errU != nil {
			continue
		}
		return t, u, true
	}
	return 0, 0, false
}

// distroFsUsage runs df inside a running distro. It is deliberately only
// called for running distros — `wsl -d <name>` against a stopped distro would
// boot it, an unwanted side effect of merely reading disk stats.
func distroFsUsage(ctx context.Context, wslExe, distro string) (total, used int64, ok bool) {
	out, err := runCapture(ctx, wslExe, "-d", distro, "--", "df", "-B1", "/")
	if err != nil {
		return 0, 0, false
	}
	return parseDF(decodeMaybeUTF16(out))
}
