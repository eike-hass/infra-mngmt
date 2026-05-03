package config

import (
	"bufio"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// WSLWindowsHostIP returns the Windows host IP visible from inside WSL2 — the
// gateway of WSL's default route. On legacy WSL2 setups this matched the
// /etc/resolv.conf nameserver, but on Windows 11 with Hyper-V firewall + DNS
// tunneling the nameserver is a DNS proxy bound to WSL's loopback (e.g.
// 10.255.255.254 on lo) and is NOT routable to the host. The default-route
// gateway is the right answer in both topologies.
//
// Returns "" if neither /proc/net/route nor /etc/resolv.conf yields a usable
// address (e.g. running outside WSL2).
func WSLWindowsHostIP() string {
	if data, err := os.ReadFile("/proc/net/route"); err == nil {
		if ip := parseDefaultRouteGateway(string(data)); ip != "" {
			return ip
		}
	}
	return resolvConfNameserver()
}

// parseDefaultRouteGateway extracts the gateway IP of the default route from
// the contents of /proc/net/route. The kernel writes 32-bit IPs as
// little-endian hex (e.g. "0100020A" = 10.2.0.1). Rows with destination
// != 0.0.0.0 are skipped, as are direct routes (gateway 0.0.0.0).
func parseDefaultRouteGateway(content string) string {
	for i, line := range strings.Split(content, "\n") {
		if i == 0 || line == "" {
			continue // skip header and blanks
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		if fields[1] != "00000000" || fields[2] == "00000000" {
			continue
		}
		if ip := parseHexLEIP(fields[2]); ip != "" {
			return ip
		}
	}
	return ""
}

// parseHexLEIP turns a /proc/net/route style 8-char little-endian hex string
// (e.g. "0100020A") into a dotted-quad IPv4 string (e.g. "10.2.0.1").
func parseHexLEIP(hex string) string {
	if len(hex) != 8 {
		return ""
	}
	var b [4]byte
	for i := 0; i < 4; i++ {
		v, err := strconv.ParseUint(hex[i*2:i*2+2], 16, 8)
		if err != nil {
			return ""
		}
		b[3-i] = byte(v)
	}
	return net.IP(b[:]).String()
}

func resolvConfNameserver() string {
	f, err := os.Open("/etc/resolv.conf")
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if after, ok := strings.CutPrefix(line, "nameserver "); ok {
			ip := strings.TrimSpace(after)
			if ip != "" {
				return ip
			}
		}
	}
	return ""
}

// ResolveEndpoint replaces the hostname "wsl-windows" in a URL with the
// Windows host IP discovered from WSL's default route. Any other URL is
// returned unchanged. This lets the config file stay static across WSL2
// restarts even when using NAT networking.
//
// Example: "http://wsl-windows:9999" → "http://172.18.0.1:9999"
func ResolveEndpoint(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() != "wsl-windows" {
		return raw
	}
	ip := WSLWindowsHostIP()
	if ip == "" {
		return raw // not in WSL2, or resolv.conf unreadable — leave as-is
	}
	if port := u.Port(); port != "" {
		u.Host = ip + ":" + port
	} else {
		u.Host = ip
	}
	return u.String()
}
