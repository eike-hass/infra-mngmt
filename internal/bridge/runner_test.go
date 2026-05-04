package bridge

import (
	"strings"
	"testing"
)

func TestFilterAllWhenNamesEmpty(t *testing.T) {
	bridges := []Bridge{validBridge(), validBridge()}
	bridges[1].Name = "llama-cpp"
	got := Filter(bridges, nil)
	if len(got) != 2 {
		t.Errorf("expected all bridges, got %d", len(got))
	}
}

func TestFilterByName(t *testing.T) {
	bridges := []Bridge{validBridge(), validBridge(), validBridge()}
	bridges[1].Name = "llama-cpp"
	bridges[2].Name = "whisper"
	got := Filter(bridges, []string{"llama-cpp", "whisper"})
	if len(got) != 2 {
		t.Fatalf("expected 2 filtered, got %d", len(got))
	}
	if got[0].Name != "llama-cpp" || got[1].Name != "whisper" {
		t.Errorf("wrong filter result: %+v", got)
	}
}

func TestFilterUnknownNamesAreSilentlyDropped(t *testing.T) {
	bridges := []Bridge{validBridge()}
	got := Filter(bridges, []string{"nonexistent", "producer-pal"})
	if len(got) != 1 {
		t.Errorf("expected 1 bridge, got %d", len(got))
	}
}

func TestFormatStatusTableEmpty(t *testing.T) {
	got := FormatStatusTable(nil, nil)
	if !strings.Contains(got, "no bridges configured") {
		t.Errorf("expected empty-state message, got %q", got)
	}
}

func TestFormatStatusTableHeaders(t *testing.T) {
	bridges := []Bridge{validBridge()}
	states := []State{StateActive}
	got := FormatStatusTable(bridges, states)
	for _, want := range []string{"NAME", "TIER", "LISTEN", "CONNECT", "STATE", "producer-pal", "windows", "${wsl-host-ip}:3350", "127.0.0.1:3350", "active"} {
		if !strings.Contains(got, want) {
			t.Errorf("table missing %q\nfull table:\n%s", want, got)
		}
	}
}

func TestFormatStatusTableSortsByName(t *testing.T) {
	a := validBridge()
	a.Name = "zebra"
	b := validBridge()
	b.Name = "alpha"
	bridges := []Bridge{a, b}
	states := []State{StateActive, StateMissing}
	got := FormatStatusTable(bridges, states)
	idxA := strings.Index(got, "alpha")
	idxZ := strings.Index(got, "zebra")
	if idxA == -1 || idxZ == -1 || idxA >= idxZ {
		t.Errorf("table not sorted alphabetically:\n%s", got)
	}
}

func TestEncodePSCommandRoundTripsKnownExample(t *testing.T) {
	// Verified against `powershell.exe -EncodedCommand` semantics: UTF-16LE,
	// no BOM, base64-std. "echo hi" → 4 chars × 2 bytes = 8 bytes → 12 b64.
	got := encodePSCommand("echo hi")
	want := "ZQBjAGgAbwAgAGgAaQA="
	if got != want {
		t.Errorf("encodePSCommand(\"echo hi\") = %q, want %q", got, want)
	}
}

func TestEncodePSCommandIsBase64Safe(t *testing.T) {
	// The encoded output must be quote-safe (it gets embedded inside a PS
	// single-quoted string). Base64 alphabet is [A-Za-z0-9+/=] only.
	cases := []string{
		"netsh interface portproxy add v4tov4 listenaddress=$wslIp listenport=8080 connectaddress=127.0.0.1 connectport=8080",
		"$weird = 'has ''quotes'' and \"doubles\" and \\backslashes\\'",
		"",
	}
	for _, in := range cases {
		got := encodePSCommand(in)
		for _, c := range got {
			isB64 := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '+' || c == '/' || c == '='
			if !isB64 {
				t.Errorf("encodePSCommand(%q) contains unsafe char %q", in, c)
			}
		}
	}
}

func TestFormatStatusTableHandlesShortStates(t *testing.T) {
	// Defensive: states slice shorter than bridges shouldn't panic.
	bridges := []Bridge{validBridge(), validBridge()}
	bridges[1].Name = "second"
	got := FormatStatusTable(bridges, []State{StateActive})
	if !strings.Contains(got, "—") {
		t.Errorf("expected — placeholder for missing state, got:\n%s", got)
	}
}
