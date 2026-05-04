package bridge

import (
	"strings"
)

// State is the observed runtime status of a bridge.
type State int

const (
	StateUnknown State = iota
	StateActive        // present in the system and matches the declared form
	StateDrifted       // present but listenaddr/connect/etc. differ from declared
	StateMissing       // not present in the system
)

func (s State) String() string {
	switch s {
	case StateActive:
		return "active"
	case StateDrifted:
		return "drifted"
	case StateMissing:
		return "missing"
	default:
		return "unknown"
	}
}

// PortproxyEntry is one row from `netsh interface portproxy show all` output.
type PortproxyEntry struct {
	ProxyType   string // "v4tov4" or "v4tov6"
	ListenAddr  string
	ListenPort  int
	ConnectAddr string
	ConnectPort int
}

// ParsePortproxyShow turns the textual output of `netsh interface portproxy
// show all` into a slice of entries. The output format is roughly:
//
//	Listen on ipv4:             Connect to ipv4:
//
//	Address         Port        Address         Port
//	--------------- ----------  --------------- ----------
//	172.18.0.1      3350        127.0.0.1       3350
//
// Multiple sections (one per proxy type) may appear; the section header
// determines the ProxyType for the rows that follow.
func ParsePortproxyShow(output string) []PortproxyEntry {
	var entries []PortproxyEntry
	currentType := ""
	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "---") || strings.HasPrefix(line, "Address") {
			continue
		}
		lower := strings.ToLower(line)
		switch {
		case strings.Contains(lower, "listen on ipv4") && strings.Contains(lower, "connect to ipv4"):
			currentType = "v4tov4"
			continue
		case strings.Contains(lower, "listen on ipv4") && strings.Contains(lower, "connect to ipv6"):
			currentType = "v4tov6"
			continue
		case strings.Contains(lower, "listen on ipv6") && strings.Contains(lower, "connect to ipv4"):
			currentType = "v6tov4"
			continue
		case strings.Contains(lower, "listen on ipv6") && strings.Contains(lower, "connect to ipv6"):
			currentType = "v6tov6"
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 4 || currentType == "" {
			continue
		}
		port1 := atoiSafe(fields[1])
		port2 := atoiSafe(fields[3])
		if port1 == 0 || port2 == 0 {
			continue
		}
		entries = append(entries, PortproxyEntry{
			ProxyType:   currentType,
			ListenAddr:  fields[0],
			ListenPort:  port1,
			ConnectAddr: fields[2],
			ConnectPort: port2,
		})
	}
	return entries
}

func atoiSafe(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// Compare tells whether the declared bridge matches an observed portproxy
// entry. wslHostIP is the resolved value of the ${wsl-host-ip} sentinel.
//
// Returns (active, drifted) flags: active = exact match, drifted = same
// listen ip+port but different connect target or proxy type. Family=auto is
// considered to match either v4tov4 or v4tov6.
func (b *Bridge) ComparePortproxy(e PortproxyEntry, wslHostIP string) (active, drifted bool) {
	if b.Type != TypePortproxy {
		return false, false
	}
	listen := resolveSentinel(b.Listen.Addr, wslHostIP)
	if e.ListenAddr != listen || e.ListenPort != b.Listen.Port {
		return false, false
	}
	wantConnect := b.Connect.Addr
	if e.ConnectAddr != wantConnect || e.ConnectPort != b.Connect.Port {
		return false, true
	}
	switch b.Connect.Family {
	case FamilyV4:
		if e.ProxyType != "v4tov4" {
			return false, true
		}
	case FamilyV6:
		if e.ProxyType != "v4tov6" {
			return false, true
		}
	case FamilyAuto, "":
		if e.ProxyType != "v4tov4" && e.ProxyType != "v4tov6" {
			return false, true
		}
	}
	return true, false
}

// Status walks the observed portproxy entries and returns the State of the
// bridge. Use ComparePortproxy directly for richer diagnostics.
func (b *Bridge) Status(entries []PortproxyEntry, wslHostIP string) State {
	if b.Type != TypePortproxy {
		return StateUnknown
	}
	listen := resolveSentinel(b.Listen.Addr, wslHostIP)
	if listen == "" {
		return StateUnknown
	}
	for _, e := range entries {
		active, drifted := b.ComparePortproxy(e, wslHostIP)
		if active {
			return StateActive
		}
		if drifted {
			return StateDrifted
		}
	}
	return StateMissing
}

// resolveSentinel substitutes the ${wsl-host-ip} placeholder used in the YAML
// with the actual address. Other sentinels are returned unchanged so the
// caller can fail loudly.
func resolveSentinel(addr, wslHostIP string) string {
	if addr == "${wsl-host-ip}" {
		return wslHostIP
	}
	return addr
}
