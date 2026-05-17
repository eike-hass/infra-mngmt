package bridge

import (
	"strings"
	"testing"
)

func TestPowerShellApplyEmpty(t *testing.T) {
	if got := PowerShellApply(nil); got != "" {
		t.Errorf("empty bridge list should produce empty script, got %q", got)
	}
	if got := PowerShellRemove(nil); got != "" {
		t.Errorf("empty bridge list should produce empty remove script, got %q", got)
	}
}

func TestPowerShellApplyHeaderSetsWslIp(t *testing.T) {
	got := PowerShellApply([]Bridge{validBridge()})
	if !strings.Contains(got, "Get-NetIPAddress") {
		t.Errorf("apply script should resolve $wslIp via Get-NetIPAddress; got:\n%s", got)
	}
	if !strings.Contains(got, "vEthernet (WSL*)") {
		t.Errorf("apply script should target the WSL adapter; got:\n%s", got)
	}
}

func TestPowerShellApplyAddsPortproxyAndFirewall(t *testing.T) {
	got := PowerShellApply([]Bridge{validBridge()})

	mustContain := []string{
		// Stale-rule cleanup for both proxy types (handles migration off a
		// previously-emitted v4tov6 entry).
		"netsh interface portproxy delete v4tov4 listenaddress=$wslIp listenport=3350",
		"netsh interface portproxy delete v4tov6 listenaddress=$wslIp listenport=3350",
		// Family=auto now deterministically emits v4tov4 — no runtime
		// IPv6-listener probe, no v4tov6 add.
		"netsh interface portproxy add v4tov4 listenaddress=$wslIp listenport=3350 connectaddress=127.0.0.1 connectport=3350",
		// Firewall rule delete + recreate
		"Remove-NetFirewallRule -DisplayName 'Producer Pal MCP'",
		"New-NetFirewallRule -DisplayName 'Producer Pal MCP'",
		"-LocalPort 3350",
		"-LocalAddress $wslIp",
		"-RemoteAddress '172.18.0.0/16'",
		// Hyper-V firewall companion rule (recent Win updates default the
		// WSL profile to Block; without this, traffic from WSL → Windows
		// is silently dropped at the vSwitch even with portproxy + WF in
		// place).
		"Remove-NetFirewallHyperVRule -DisplayName 'Producer Pal MCP [Hyper-V]'",
		"New-NetFirewallHyperVRule -DisplayName 'Producer Pal MCP [Hyper-V]'",
		"-LocalPorts 3350",
		"-VMCreatorId '{40E0AC32-46A5-438A-A0B2-2B479E8F2E90}'",
		// iphlpsvc restart at the end (mirrors the bash script)
		"Restart-Service iphlpsvc",
	}
	for _, want := range mustContain {
		if !strings.Contains(got, want) {
			t.Errorf("script missing %q\nfull script:\n%s", want, got)
		}
	}
	// Auto-detect is gone: no runtime IPv6 probe, no conditional v4tov6 add.
	for _, mustNot := range []string{
		"Get-NetTCPConnection",
		"netsh interface portproxy add v4tov6",
	} {
		if strings.Contains(got, mustNot) {
			t.Errorf("auto-detect should be removed; script still contains %q\nfull script:\n%s", mustNot, got)
		}
	}
}

func TestPowerShellApplyFamilyV4(t *testing.T) {
	b := validBridge()
	b.Connect.Family = FamilyV4
	got := PowerShellApply([]Bridge{b})
	if !strings.Contains(got, "netsh interface portproxy add v4tov4 listenaddress=$wslIp") {
		t.Errorf("explicit family=v4 should add v4tov4 directly; got:\n%s", got)
	}
	if strings.Contains(got, "v4tov6") && !strings.Contains(got, "portproxy delete v4tov6") {
		// The delete-v4tov6 cleanup line is allowed; an add-v4tov6 line is not.
		t.Errorf("family=v4 must not emit v4tov6 add; got:\n%s", got)
	}
}

func TestPowerShellApplyFamilyV6(t *testing.T) {
	b := validBridge()
	b.Connect.Family = FamilyV6
	b.Connect.Addr = "::1"
	got := PowerShellApply([]Bridge{b})
	if !strings.Contains(got, "netsh interface portproxy add v4tov6 listenaddress=$wslIp listenport=3350 connectaddress=::1") {
		t.Errorf("v6 connect address not used in add line; got:\n%s", got)
	}
}

func TestPowerShellApplyEscapesQuotesInDisplayName(t *testing.T) {
	b := validBridge()
	b.Firewall.DisplayName = "He said 'hi'"
	got := PowerShellApply([]Bridge{b})
	// PowerShell single-quote escaping doubles the quote.
	if !strings.Contains(got, "'He said ''hi'''") {
		t.Errorf("expected escaped display name; got:\n%s", got)
	}
}

func TestPowerShellRemoveSkipsAdd(t *testing.T) {
	got := PowerShellRemove([]Bridge{validBridge()})
	if strings.Contains(got, "portproxy add") {
		t.Errorf("remove script should not contain add lines; got:\n%s", got)
	}
	if !strings.Contains(got, "portproxy delete v4tov4 listenaddress=$wslIp listenport=3350") {
		t.Errorf("remove script missing delete line:\n%s", got)
	}
	if !strings.Contains(got, "Remove-NetFirewallRule -DisplayName 'Producer Pal MCP'") {
		t.Errorf("remove script missing firewall removal:\n%s", got)
	}
	if !strings.Contains(got, "Remove-NetFirewallHyperVRule -DisplayName 'Producer Pal MCP [Hyper-V]'") {
		t.Errorf("remove script missing Hyper-V firewall companion removal:\n%s", got)
	}
	if !strings.Contains(got, "Restart-Service iphlpsvc") {
		t.Errorf("remove script should still restart iphlpsvc")
	}
}

func TestPowerShellApplyMultipleBridges(t *testing.T) {
	a := validBridge()
	bb := validBridge()
	bb.Name = "llama-cpp"
	bb.Listen.Port = 8080
	bb.Connect.Port = 8080
	bb.Firewall.DisplayName = "llama.cpp Server"
	got := PowerShellApply([]Bridge{a, bb})
	for _, want := range []string{"# bridge producer-pal", "# bridge llama-cpp", "listenport=3350", "listenport=8080"} {
		if !strings.Contains(got, want) {
			t.Errorf("multi-bridge script missing %q", want)
		}
	}
	// Single iphlpsvc restart at the end, not per-bridge.
	if strings.Count(got, "Restart-Service iphlpsvc") != 1 {
		t.Errorf("expected exactly one Restart-Service call; got %d", strings.Count(got, "Restart-Service iphlpsvc"))
	}
}

func TestSocatCommand(t *testing.T) {
	b := Bridge{
		Name:    "socat-test",
		Tier:    TierWSL,
		Type:    TypeSocat,
		Listen:  Endpoint{Addr: "172.17.0.1", Port: 3350},
		Connect: Endpoint{Addr: "${windows-host-ip}", Port: 3350},
	}
	got := SocatCommand(&b, "")
	want := "socat TCP-LISTEN:3350,fork,reuseaddr,bind=172.17.0.1 TCP:$(infra-mngmt addr windows-host):3350"
	if got != want {
		t.Errorf("SocatCommand mismatch:\n  got:  %s\n  want: %s", got, want)
	}
}

func TestSocatCommandEmptyForNonSocat(t *testing.T) {
	b := validBridge() // portproxy type
	if got := SocatCommand(&b, ""); got != "" {
		t.Errorf("expected empty for non-socat bridge, got %q", got)
	}
}
