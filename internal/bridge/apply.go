package bridge

import (
	"fmt"
	"strings"
)

// Action describes what the applier intends to do for one bridge.
type Action int

const (
	ActionNoop Action = iota
	ActionAdd
	ActionUpdate
	ActionRemove
)

func (a Action) String() string {
	switch a {
	case ActionNoop:
		return "noop"
	case ActionAdd:
		return "add"
	case ActionUpdate:
		return "update"
	case ActionRemove:
		return "remove"
	default:
		return "unknown"
	}
}

// PowerShellApply generates the PowerShell snippet that applies the given set
// of Windows bridges in one elevated invocation. It assumes wsl-host-ip is
// resolved on the Windows side via Get-NetIPAddress, so the snippet starts by
// computing $wslIp itself — the caller doesn't need to know the IP ahead of
// time.
//
// Behavior mirrors the bash script: clean stale rules, add fresh ones,
// recreate the firewall rule, restart iphlpsvc to flush the listener cache.
func PowerShellApply(bridges []Bridge) string {
	if len(bridges) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(psHeader)
	for i := range bridges {
		writePortproxyApply(&b, &bridges[i])
	}
	b.WriteString("Restart-Service iphlpsvc -Force\n")
	return b.String()
}

// PowerShellRemove generates the PowerShell snippet that removes the given
// Windows bridges. Idempotent.
func PowerShellRemove(bridges []Bridge) string {
	if len(bridges) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(psHeader)
	for i := range bridges {
		writePortproxyRemove(&b, &bridges[i])
	}
	b.WriteString("Restart-Service iphlpsvc -Force\n")
	return b.String()
}

const psHeader = `$ErrorActionPreference = 'Stop'
$wslIp = (Get-NetIPAddress -InterfaceAlias 'vEthernet (WSL*)' -AddressFamily IPv4 -ErrorAction SilentlyContinue | Select-Object -First 1).IPAddress
if (-not $wslIp) { throw "could not resolve WSL adapter IP — is WSL running?" }
`

// wslVMCreatorID is the well-known Hyper-V VM creator ID for WSL. Used to
// scope Hyper-V firewall rules so they apply to traffic on the WSL vSwitch
// only, not other VM creators (e.g., Hyper-V Manager VMs, Windows Sandbox).
const wslVMCreatorID = "{40E0AC32-46A5-438A-A0B2-2B479E8F2E90}"

func writePortproxyApply(b *strings.Builder, br *Bridge) {
	listen := psListenAddr(br)
	connect := br.Connect.Addr
	proxyType := familyToProxyType(br.Connect.Family)
	rule := psEscape(br.Firewall.DisplayName)

	fmt.Fprintf(b, "# bridge %s\n", br.Name)
	fmt.Fprintf(b, "netsh interface portproxy delete v4tov4 listenaddress=%s listenport=%d 2>$null | Out-Null\n", listen, br.Listen.Port)
	fmt.Fprintf(b, "netsh interface portproxy delete v4tov6 listenaddress=%s listenport=%d 2>$null | Out-Null\n", listen, br.Listen.Port)

	if proxyType == "auto" {
		fmt.Fprintf(b, "$ipv6 = Get-NetTCPConnection -LocalPort %d -State Listen -ErrorAction SilentlyContinue | Where-Object { $_.LocalAddress -eq '::1' -or $_.LocalAddress -eq '::' }\n", br.Connect.Port)
		fmt.Fprintf(b, "if ($ipv6) {\n  netsh interface portproxy add v4tov6 listenaddress=%s listenport=%d connectaddress=%s connectport=%d | Out-Null\n} else {\n  netsh interface portproxy add v4tov4 listenaddress=%s listenport=%d connectaddress=%s connectport=%d | Out-Null\n}\n",
			listen, br.Listen.Port, connect, br.Connect.Port,
			listen, br.Listen.Port, connect, br.Connect.Port)
	} else {
		fmt.Fprintf(b, "netsh interface portproxy add %s listenaddress=%s listenport=%d connectaddress=%s connectport=%d | Out-Null\n",
			proxyType, listen, br.Listen.Port, connect, br.Connect.Port)
	}

	fmt.Fprintf(b, "Remove-NetFirewallRule -DisplayName '%s' -ErrorAction SilentlyContinue | Out-Null\n", rule)
	fmt.Fprintf(b, "New-NetFirewallRule -DisplayName '%s' -Direction Inbound -Protocol TCP -LocalPort %d -LocalAddress %s -RemoteAddress '%s' -Action Allow | Out-Null\n",
		rule, br.Listen.Port, listen, br.Firewall.Remote)

	// Hyper-V firewall is a separate enforcement layer applied at the WSL
	// vSwitch. Recent Windows updates flipped its WSL profile to default-
	// Block — without an explicit allow rule scoped to the VM creator ID,
	// inbound packets from WSL are silently dropped before reaching the
	// Windows-side listener. The cmdlets are gated with
	// -ErrorAction SilentlyContinue so older Windows builds without the
	// Hyper-V firewall module degrade gracefully.
	fmt.Fprintf(b, "Remove-NetFirewallHyperVRule -DisplayName '%s [Hyper-V]' -ErrorAction SilentlyContinue | Out-Null\n", rule)
	fmt.Fprintf(b, "New-NetFirewallHyperVRule -DisplayName '%s [Hyper-V]' -Direction Inbound -Action Allow -Protocol TCP -LocalPorts %d -VMCreatorId '%s' -ErrorAction SilentlyContinue | Out-Null\n",
		rule, br.Listen.Port, wslVMCreatorID)
}

func writePortproxyRemove(b *strings.Builder, br *Bridge) {
	listen := psListenAddr(br)
	rule := psEscape(br.Firewall.DisplayName)

	fmt.Fprintf(b, "# bridge %s\n", br.Name)
	fmt.Fprintf(b, "netsh interface portproxy delete v4tov4 listenaddress=%s listenport=%d 2>$null | Out-Null\n", listen, br.Listen.Port)
	fmt.Fprintf(b, "netsh interface portproxy delete v4tov6 listenaddress=%s listenport=%d 2>$null | Out-Null\n", listen, br.Listen.Port)
	fmt.Fprintf(b, "Remove-NetFirewallRule -DisplayName '%s' -ErrorAction SilentlyContinue | Out-Null\n", rule)
	fmt.Fprintf(b, "Remove-NetFirewallHyperVRule -DisplayName '%s [Hyper-V]' -ErrorAction SilentlyContinue | Out-Null\n", rule)
}

// psListenAddr returns the PowerShell expression for the listen address — a
// literal IP for static addresses, or the $wslIp variable for the sentinel.
func psListenAddr(br *Bridge) string {
	if br.Listen.Addr == "${wsl-host-ip}" {
		return "$wslIp"
	}
	return br.Listen.Addr
}

func familyToProxyType(f Family) string {
	switch f {
	case FamilyV4:
		return "v4tov4"
	case FamilyV6:
		return "v4tov6"
	default:
		return "auto"
	}
}

// psEscape doubles single quotes for safe inclusion in PowerShell single-
// quoted strings. We use single-quoted PS strings throughout so $ doesn't
// expand; only ' itself needs escaping.
func psEscape(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

// SocatCommand returns the shell command for a wsl-tier socat bridge. The
// caller is expected to run it (or feed it into a process-compose entry) — we
// don't manage long-running socat processes from the bridge applier.
func SocatCommand(br *Bridge, wslHostIP string) string {
	if br.Type != TypeSocat {
		return ""
	}
	listen := br.Listen.Addr
	if listen == "${wsl-host-ip}" {
		listen = wslHostIP
	}
	connect := br.Connect.Addr
	if connect == "${windows-host-ip}" {
		connect = "$(infra-mngmt addr windows-host)"
	}
	return fmt.Sprintf("socat TCP-LISTEN:%d,fork,reuseaddr,bind=%s TCP:%s:%d",
		br.Listen.Port, listen, connect, br.Connect.Port)
}
