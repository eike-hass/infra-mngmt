package config

import (
	"bufio"
	"net/url"
	"os"
	"strings"
)

// WSLWindowsHostIP returns the Windows host IP by reading the nameserver
// entry from /etc/resolv.conf, which in WSL2 NAT mode is always the virtual
// switch gateway (i.e. the Windows host). Returns "" if not in WSL2 or on
// any read error.
func WSLWindowsHostIP() string {
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
// actual Windows host IP read from /etc/resolv.conf. Any other URL is
// returned unchanged. This lets the config file stay static across WSL2
// restarts even when using NAT networking.
//
// Example: "http://wsl-windows:9999" → "http://172.26.176.1:9999"
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
