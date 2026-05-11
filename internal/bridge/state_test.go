package bridge

import (
	"os"
	"testing"
)

// TestMain swaps probeListen for a deterministic stub. Default = success so
// the existing StateActive tests still mean "rule matches"; tests that need
// to exercise the probe-failure path override probeListen locally.
func TestMain(m *testing.M) {
	orig := probeListen
	probeListen = func(string, int) bool { return true }
	defer func() { probeListen = orig }()
	os.Exit(m.Run())
}

func withProbe(stub func(string, int) bool) func() {
	orig := probeListen
	probeListen = stub
	return func() { probeListen = orig }
}

const sampleNetshOutput = `
Listen on ipv4:             Connect to ipv4:

Address         Port        Address         Port
--------------- ----------  --------------- ----------
172.18.0.1      3350        127.0.0.1       3350
172.18.0.1      8080        127.0.0.1       8080

Listen on ipv4:             Connect to ipv6:

Address         Port        Address         Port
--------------- ----------  --------------- ----------
172.18.0.1      9090        ::1             9090
`

func TestParsePortproxyShow(t *testing.T) {
	got := ParsePortproxyShow(sampleNetshOutput)
	if len(got) != 3 {
		t.Fatalf("expected 3 entries, got %d: %+v", len(got), got)
	}
	if got[0] != (PortproxyEntry{ProxyType: "v4tov4", ListenAddr: "172.18.0.1", ListenPort: 3350, ConnectAddr: "127.0.0.1", ConnectPort: 3350}) {
		t.Errorf("entry 0 mismatch: %+v", got[0])
	}
	if got[2].ProxyType != "v4tov6" {
		t.Errorf("entry 2 should be v4tov6, got %s", got[2].ProxyType)
	}
}

func TestParsePortproxyShowEmpty(t *testing.T) {
	if got := ParsePortproxyShow(""); len(got) != 0 {
		t.Errorf("empty input should produce 0 entries, got %d", len(got))
	}
	if got := ParsePortproxyShow("garbage line\n"); len(got) != 0 {
		t.Errorf("garbage should produce 0 entries, got %d", len(got))
	}
}

func TestStatusActive(t *testing.T) {
	b := validBridge() // listen ${wsl-host-ip}:3350, connect 127.0.0.1:3350
	entries := ParsePortproxyShow(sampleNetshOutput)
	got := b.Status(entries, "172.18.0.1")
	if got != StateActive {
		t.Errorf("expected active, got %s", got)
	}
}

func TestStatusMissing(t *testing.T) {
	b := validBridge()
	b.Listen.Port = 9999 // no entry on this port in the fixture
	b.Connect.Port = 9999
	entries := ParsePortproxyShow(sampleNetshOutput)
	got := b.Status(entries, "172.18.0.1")
	if got != StateMissing {
		t.Errorf("expected missing, got %s", got)
	}
}

func TestStatusDriftedConnectPort(t *testing.T) {
	b := validBridge()
	b.Connect.Port = 4444 // listener exists but connect target differs
	entries := ParsePortproxyShow(sampleNetshOutput)
	got := b.Status(entries, "172.18.0.1")
	if got != StateDrifted {
		t.Errorf("expected drifted, got %s", got)
	}
}

func TestStatusFamilyMismatchIsDrifted(t *testing.T) {
	b := validBridge()
	b.Listen.Port = 9090
	b.Connect.Port = 9090
	b.Connect.Family = FamilyV4 // entry is v4tov6 in the fixture
	entries := ParsePortproxyShow(sampleNetshOutput)
	got := b.Status(entries, "172.18.0.1")
	if got != StateDrifted {
		t.Errorf("expected drifted (family mismatch), got %s", got)
	}
}

func TestStatusFamilyAutoMatchesEither(t *testing.T) {
	b := validBridge()
	b.Listen.Port = 9090
	b.Connect.Port = 9090
	b.Connect.Family = FamilyAuto
	entries := ParsePortproxyShow(sampleNetshOutput)
	// connect addr is 127.0.0.1 in our bridge but the entry has ::1 — drifted, not active.
	if got := b.Status(entries, "172.18.0.1"); got != StateDrifted {
		t.Errorf("expected drifted (connect addr differs), got %s", got)
	}
}

func TestStatusDegradedWhenProbeFails(t *testing.T) {
	// netsh entry matches the declared bridge exactly, but the TCP connect
	// to the listener fails — the iphlpsvc-inert pattern we see after some
	// Windows boots. Should surface as Degraded, not Active or Missing.
	defer withProbe(func(string, int) bool { return false })()
	b := validBridge()
	entries := ParsePortproxyShow(sampleNetshOutput)
	if got := b.Status(entries, "172.18.0.1"); got != StateDegraded {
		t.Errorf("expected degraded when probe fails, got %s", got)
	}
}

func TestStatusActiveWhenProbeSucceeds(t *testing.T) {
	// Belt-and-braces: explicitly verify the active path still works when
	// the probe is the one returning true.
	defer withProbe(func(addr string, port int) bool {
		return addr == "172.18.0.1" && port == 3350
	})()
	b := validBridge()
	entries := ParsePortproxyShow(sampleNetshOutput)
	if got := b.Status(entries, "172.18.0.1"); got != StateActive {
		t.Errorf("expected active when probe succeeds, got %s", got)
	}
}

func TestStatusUnknownWithoutResolvedSentinel(t *testing.T) {
	b := validBridge()
	got := b.Status(nil, "")
	if got != StateUnknown {
		t.Errorf("expected unknown when sentinel can't be resolved, got %s", got)
	}
}

func TestAtoiSafe(t *testing.T) {
	cases := map[string]int{
		"0":     0,
		"3350":  3350,
		"":      0,
		"abc":   0,
		"123x":  0,
		"00080": 80,
	}
	for in, want := range cases {
		if got := atoiSafe(in); got != want {
			t.Errorf("atoiSafe(%q) = %d, want %d", in, got, want)
		}
	}
}
