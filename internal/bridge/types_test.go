package bridge

import (
	"strings"
	"testing"
)

func validBridge() Bridge {
	return Bridge{
		Name:    "producer-pal",
		Tier:    TierWindows,
		Type:    TypePortproxy,
		Listen:  Endpoint{Addr: "${wsl-host-ip}", Port: 3350},
		Connect: Endpoint{Addr: "127.0.0.1", Port: 3350, Family: FamilyAuto},
		Firewall: Firewall{
			Remote:      "172.18.0.0/16",
			DisplayName: "Producer Pal MCP",
		},
	}
}

func TestBridgeValidateValid(t *testing.T) {
	b := validBridge()
	if err := b.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestBridgeValidateBadName(t *testing.T) {
	cases := []string{"", "Bad Name", "with_underscore", "-leading-dash",
		strings.Repeat("a", 41)}
	for _, n := range cases {
		t.Run(n, func(t *testing.T) {
			b := validBridge()
			b.Name = n
			if err := b.Validate(); err == nil {
				t.Errorf("expected error for name %q", n)
			}
		})
	}
}

func TestBridgeValidateTierTypeMismatch(t *testing.T) {
	b := validBridge()
	b.Tier = TierWSL
	if err := b.Validate(); err == nil {
		t.Error("expected error for tier=wsl + type=portproxy+firewall")
	}
	b = validBridge()
	b.Tier = TierWindows
	b.Type = TypeSocat
	if err := b.Validate(); err == nil {
		t.Error("expected error for tier=windows + type=socat")
	}
}

func TestBridgeValidateContainerTierReserved(t *testing.T) {
	b := validBridge()
	b.Tier = "container:abc123"
	if err := b.Validate(); err == nil {
		t.Error("expected container tier to be rejected")
	}
}

func TestBridgeValidateBadEndpoint(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Bridge)
	}{
		{"listen port 0", func(b *Bridge) { b.Listen.Port = 0 }},
		{"listen port too high", func(b *Bridge) { b.Listen.Port = 70000 }},
		{"connect addr empty", func(b *Bridge) { b.Connect.Addr = "" }},
		{"listen addr garbage", func(b *Bridge) { b.Listen.Addr = "not.an.ip" }},
		{"connect family invalid", func(b *Bridge) { b.Connect.Family = "v7" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := validBridge()
			tc.mut(&b)
			if err := b.Validate(); err == nil {
				t.Error("expected validation error")
			}
		})
	}
}

func TestBridgeValidateAcceptsSentinel(t *testing.T) {
	b := validBridge()
	b.Listen.Addr = "${wsl-host-ip}"
	if err := b.Validate(); err != nil {
		t.Errorf("sentinel should be accepted: %v", err)
	}
}

func TestBridgeValidateFirewallRequired(t *testing.T) {
	b := validBridge()
	b.Firewall.DisplayName = ""
	if err := b.Validate(); err == nil {
		t.Error("expected error when firewall.display_name missing on portproxy bridge")
	}
	b = validBridge()
	b.Firewall.Remote = "not-cidr"
	if err := b.Validate(); err == nil {
		t.Error("expected error for invalid CIDR")
	}
}

func TestFileValidateUniqueNames(t *testing.T) {
	b1 := validBridge()
	b2 := validBridge()
	b2.Listen.Port = 8080
	b2.Connect.Port = 8080
	f := File{Bridges: []Bridge{b1, b2}}
	if err := f.Validate(); err == nil {
		t.Error("expected duplicate-name error")
	}
}

func TestFileValidateDefaultsFamily(t *testing.T) {
	b := validBridge()
	b.Connect.Family = ""
	f := File{Bridges: []Bridge{b}}
	if err := f.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if f.Bridges[0].Connect.Family != FamilyAuto {
		t.Errorf("expected default family 'auto', got %q", f.Bridges[0].Connect.Family)
	}
}

func TestValidateCompositeAcceptsBareDescription(t *testing.T) {
	parent := Bridge{
		Name:        "llama-cpp",
		Kind:        KindComposite,
		Description: "llama.cpp end-to-end",
	}
	if err := parent.Validate(); err != nil {
		t.Errorf("composite with only description should validate; got %v", err)
	}
}

func TestValidateCompositeRejectsForwardingFields(t *testing.T) {
	cases := []struct {
		name string
		mod  func(*Bridge)
	}{
		{"with tier", func(b *Bridge) { b.Tier = TierWindows }},
		{"with type", func(b *Bridge) { b.Type = TypePortproxy }},
		{"with listen", func(b *Bridge) { b.Listen = Endpoint{Addr: "127.0.0.1", Port: 80} }},
		{"with connect", func(b *Bridge) { b.Connect = Endpoint{Addr: "127.0.0.1", Port: 80} }},
		{"with firewall", func(b *Bridge) { b.Firewall = Firewall{DisplayName: "x", Remote: "10.0.0.0/8"} }},
		{"with composite_of", func(b *Bridge) { b.CompositeOf = "other" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parent := Bridge{Name: "llama-cpp", Kind: KindComposite}
			tc.mod(&parent)
			if err := parent.Validate(); err == nil {
				t.Errorf("composite %s: expected validation error, got nil", tc.name)
			}
		})
	}
}

func TestValidateUnknownKind(t *testing.T) {
	b := validBridge()
	b.Kind = "weird"
	if err := b.Validate(); err == nil {
		t.Error("expected error for unknown kind")
	}
}

func TestFileValidateCompositeOfResolvesParent(t *testing.T) {
	parent := Bridge{Name: "llama-cpp", Kind: KindComposite, Description: "llama group"}
	child := validBridge()
	child.Name = "llama-cpp-windows"
	child.CompositeOf = "llama-cpp"
	f := File{Bridges: []Bridge{parent, child}}
	if err := f.Validate(); err != nil {
		t.Errorf("expected valid composite group, got %v", err)
	}
}

func TestFileValidateCompositeOfRequiresExistingParent(t *testing.T) {
	child := validBridge()
	child.Name = "orphan"
	child.CompositeOf = "ghost"
	f := File{Bridges: []Bridge{child}}
	if err := f.Validate(); err == nil {
		t.Error("expected error when composite_of points at a non-existent parent")
	}
}

func TestFileValidateCompositeOfRejectsNonComposite(t *testing.T) {
	// Pointing at a regular bridge (not kind=composite) is also wrong.
	other := validBridge()
	other.Name = "real-bridge"
	child := validBridge()
	child.Name = "child"
	child.CompositeOf = "real-bridge"
	f := File{Bridges: []Bridge{other, child}}
	if err := f.Validate(); err == nil {
		t.Error("expected error when composite_of points at a non-composite entry")
	}
}

func TestFileValidateAllowsCompositeBeforeOrAfterMembers(t *testing.T) {
	parent := Bridge{Name: "llama-cpp", Kind: KindComposite}
	child := validBridge()
	child.Name = "child"
	child.CompositeOf = "llama-cpp"

	// Order: members first, then parent. File-level validate should resolve
	// regardless of order.
	f := File{Bridges: []Bridge{child, parent}}
	if err := f.Validate(); err != nil {
		t.Errorf("validate should accept member-before-parent ordering; got %v", err)
	}
}
