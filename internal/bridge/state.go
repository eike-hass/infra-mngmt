package bridge

import (
	"net"
	"strconv"
	"strings"
	"time"
)

// State is the observed runtime status of a bridge.
type State int

const (
	StateUnknown  State = iota
	StateActive         // present, matches declared form, and a TCP connect to the listener succeeds
	StateDegraded       // present and matches declared form, but the listener does not accept TCP connects
	StateDrifted        // present but listenaddr/connect/etc. differ from declared
	StateMissing        // not present in the system
)

func (s State) String() string {
	switch s {
	case StateActive:
		return "active"
	case StateDegraded:
		return "degraded"
	case StateDrifted:
		return "drifted"
	case StateMissing:
		return "missing"
	default:
		return "unknown"
	}
}

// ProbeTimeout caps how long Status spends checking each listener. Kept
// small because a healthy listener responds within ms; the dominant cost
// is for genuinely dead listeners. Setting it to <= 0 disables probing
// entirely — Status then falls back to the legacy "netsh match = Active"
// semantics, useful for unit tests and any caller that doesn't want the
// network I/O.
var ProbeTimeout = 500 * time.Millisecond

// probeListen attempts a TCP connect to addr:port within ProbeTimeout.
// Returns true iff the connect succeeds (or probing is disabled, in which
// case we trust the netsh match). Overridable in tests.
//
// We use a real TCP connect rather than just checking the netsh entry
// because iphlpsvc can hold a portproxy rule in its database without
// actually forwarding — observed after Windows boot, where the rules
// load before iphlpsvc finishes wiring its proxy state. In that case
// the connect fails outright (no SYN+ACK arrives), and Status reports
// StateDegraded so the UI doesn't claim "active" when traffic isn't
// flowing.
//
// Deliberate scope: this probe is only meaningful for the *bridge*.
// iphlpsvc accepts the local SYN before attempting upstream forwarding,
// so the probe returns success as soon as SYN+ACK arrives — even if the
// upstream service (the thing the bridge points at) is down. That's
// intentional: a bridge's job is to forward traffic; whether the upstream
// is healthy is a separate concern, surfaced by the relevant process /
// container panel. Don't "fix" this by upgrading the probe to write+read
// or HTTP HEAD — you'll start conflating bridge health with upstream
// health, which is exactly what this enum is designed to keep apart.
var probeListen = func(addr string, port int) bool {
	if ProbeTimeout <= 0 {
		return true
	}
	if addr == "" || port <= 0 {
		return false
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(addr, strconv.Itoa(port)), ProbeTimeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
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
	case FamilyV6:
		if e.ProxyType != "v4tov6" {
			return false, true
		}
	default:
		// FamilyV4, FamilyAuto, and empty all expect v4tov4 — apply.go
		// emits v4tov4 for any of those, so a v4tov6 entry on disk is
		// drift that needs reconciliation (e.g. a leftover from before
		// the auto→v4tov4 default change).
		if e.ProxyType != "v4tov4" {
			return false, true
		}
	}
	return true, false
}

// Status walks the observed portproxy entries and returns the State of the
// bridge. When the netsh rule matches the declared form, an additional TCP
// connect probe to the listen address+port determines whether iphlpsvc is
// actually forwarding: success yields StateActive, failure yields
// StateDegraded (rule looks right but traffic doesn't flow). Use
// ComparePortproxy directly for richer diagnostics.
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
			if probeListen(listen, b.Listen.Port) {
				return StateActive
			}
			return StateDegraded
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
