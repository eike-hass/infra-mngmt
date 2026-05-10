// Package bridge models persistent network bridges between tiers (e.g. Windows
// portproxy + firewall rules, WSL socat relays). Bridges are not processes —
// they are state that survives reboots — so they live outside process-compose
// and are applied via a dedicated CLI subcommand.
package bridge

import (
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"strings"
)

// Tier identifies where a bridge's listener is established. The applier
// dispatches on this.
type Tier string

const (
	TierWSL     Tier = "wsl"
	TierWindows Tier = "windows"
)

// Type identifies the underlying mechanism used to forward traffic.
type Type string

const (
	// TypePortproxy is netsh interface portproxy + a Windows Firewall rule.
	// Listeners live on the Windows tier and forward to a 127.0.0.1 service.
	TypePortproxy Type = "portproxy+firewall"
	// TypeSocat is a long-running socat relay. Listeners live on the WSL tier
	// and forward to a target address. Use this when you want the persistent-
	// state semantics (declarative re-apply on drift) instead of a process-
	// compose entry's restart-loop semantics.
	TypeSocat Type = "socat"
)

// Family selects between IPv4 and IPv6 for the connect-side address.
// "auto" probes the listener side at apply time (matches the bash script's
// detect_connect_address logic).
type Family string

const (
	FamilyAuto Family = "auto"
	FamilyV4   Family = "v4"
	FamilyV6   Family = "v6"
)

// Endpoint is one side of a bridge — either listen or connect.
type Endpoint struct {
	Addr   string `yaml:"addr"`
	Port   int    `yaml:"port"`
	Family Family `yaml:"family,omitempty"`
}

// Firewall holds the Windows Firewall rule parameters for portproxy bridges.
// It's empty for socat bridges.
type Firewall struct {
	Remote      string `yaml:"remote"`       // CIDR of allowed remote addresses (e.g. 172.18.0.0/16)
	DisplayName string `yaml:"display_name"` // friendly name; also used for cleanup
}

// Kind identifies whether an entry is an actual forwarding rule or a virtual
// "composite" parent that groups other entries. Empty/missing defaults to
// KindBridge for backwards compatibility with bridges.yaml files written
// before composites existed.
type Kind string

const (
	KindBridge    Kind = "bridge"    // a real forwarding rule (default)
	KindComposite Kind = "composite" // a virtual parent that groups bridges
)

// Bridge is one persistent network forwarding rule, OR a composite parent
// that groups multiple rules under one logical name. Composites have
// kind=composite + Description; their members link back via CompositeOf.
type Bridge struct {
	Name        string   `yaml:"name"`
	Kind        Kind     `yaml:"kind,omitempty"`         // "bridge" (default) | "composite"
	Description string   `yaml:"description,omitempty"`  // free-text; used in UI subtitle
	CompositeOf string   `yaml:"composite_of,omitempty"` // name of the composite parent, if any
	Tier        Tier     `yaml:"tier"`
	Type        Type     `yaml:"type"`
	Listen      Endpoint `yaml:"listen"`
	Connect     Endpoint `yaml:"connect"`
	Firewall    Firewall `yaml:"firewall,omitempty"`
}

// File is the on-disk shape of bridges.yaml.
type File struct {
	Bridges []Bridge `yaml:"bridges"`
}

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// Validate checks the file-level invariants (unique names, supported tier+type
// combos, valid endpoints/firewall, composite-parent references resolve).
func (f *File) Validate() error {
	seen := make(map[string]struct{}, len(f.Bridges))
	composites := make(map[string]struct{})
	// First pass: catch duplicate names and remember composite parents so
	// member references can be resolved in the second pass regardless of
	// order in the YAML.
	for i := range f.Bridges {
		b := &f.Bridges[i]
		if !nameRe.MatchString(b.Name) {
			return fmt.Errorf("bridges[%d]: name %q must match %s", i, b.Name, nameRe.String())
		}
		if _, dup := seen[b.Name]; dup {
			return fmt.Errorf("bridges[%d]: duplicate name %q", i, b.Name)
		}
		seen[b.Name] = struct{}{}
		if b.Kind == KindComposite {
			composites[b.Name] = struct{}{}
		}
	}
	// Second pass: per-entry validation (with composite-aware rules).
	for i := range f.Bridges {
		b := &f.Bridges[i]
		if err := b.validate(composites); err != nil {
			return fmt.Errorf("bridges[%d] (%q): %w", i, b.Name, err)
		}
	}
	return nil
}

// Validate is the per-entry public form. Use File.Validate for file-level
// checks (it resolves composite cross-references).
func (b *Bridge) Validate() error {
	return b.validate(nil)
}

// validate is the inner form used by File.Validate. composites is the set of
// names whose entries have Kind=KindComposite. Pass nil to skip cross-
// reference resolution (single-entry validation).
func (b *Bridge) validate(composites map[string]struct{}) error {
	if !nameRe.MatchString(b.Name) {
		return fmt.Errorf("name %q must match %s", b.Name, nameRe.String())
	}

	// Default Kind so consumers can rely on it being non-empty.
	if b.Kind == "" {
		b.Kind = KindBridge
	}
	switch b.Kind {
	case KindComposite:
		// Composites are virtual parents — they have no own forwarding state.
		// Reject any of the per-mechanism fields to fail loud on misconfig
		// instead of silently ignoring them.
		if b.Tier != "" {
			return fmt.Errorf("kind=composite must not set tier (members carry tier); got %q", b.Tier)
		}
		if b.Type != "" {
			return fmt.Errorf("kind=composite must not set type; got %q", b.Type)
		}
		if b.Listen.Addr != "" || b.Listen.Port != 0 {
			return errors.New("kind=composite must not set listen — members carry endpoints")
		}
		if b.Connect.Addr != "" || b.Connect.Port != 0 {
			return errors.New("kind=composite must not set connect — members carry endpoints")
		}
		if b.Firewall.DisplayName != "" || b.Firewall.Remote != "" {
			return errors.New("kind=composite must not set firewall — members carry firewall rules")
		}
		if b.CompositeOf != "" {
			return errors.New("kind=composite must not be a member of another composite (no nesting)")
		}
		return nil
	case KindBridge:
		// Fall through to the existing tier/type/endpoint checks below.
	default:
		return fmt.Errorf("kind %q: must be %q or %q", b.Kind, KindBridge, KindComposite)
	}

	// composite_of must reference an existing composite parent. Skip when
	// composites is nil (single-entry call).
	if b.CompositeOf != "" && composites != nil {
		if _, ok := composites[b.CompositeOf]; !ok {
			return fmt.Errorf("composite_of %q: no composite entry with that name", b.CompositeOf)
		}
	}

	switch b.Tier {
	case TierWindows:
		if b.Type != TypePortproxy {
			return fmt.Errorf("tier=windows requires type=portproxy+firewall, got %q", b.Type)
		}
	case TierWSL:
		if b.Type != TypeSocat {
			return fmt.Errorf("tier=wsl requires type=socat, got %q", b.Type)
		}
	case "":
		return errors.New("tier is required")
	default:
		if strings.HasPrefix(string(b.Tier), "container:") {
			return fmt.Errorf("tier=%q: container bridges are not yet implemented", b.Tier)
		}
		return fmt.Errorf("tier %q: must be one of wsl, windows", b.Tier)
	}
	if err := validateEndpoint(b.Listen, "listen"); err != nil {
		return err
	}
	if err := validateEndpoint(b.Connect, "connect"); err != nil {
		return err
	}
	if b.Connect.Family == "" {
		b.Connect.Family = FamilyAuto
	}
	switch b.Connect.Family {
	case FamilyAuto, FamilyV4, FamilyV6:
	default:
		return fmt.Errorf("connect.family %q: must be auto, v4, or v6", b.Connect.Family)
	}
	if b.Type == TypePortproxy {
		if err := validateFirewall(b.Firewall); err != nil {
			return err
		}
	}
	return nil
}

func validateEndpoint(e Endpoint, label string) error {
	if e.Port < 1 || e.Port > 65535 {
		return fmt.Errorf("%s.port %d: must be 1-65535", label, e.Port)
	}
	if e.Addr == "" {
		return fmt.Errorf("%s.addr is required", label)
	}
	// Allow sentinels (${...}) — they're resolved at apply time.
	if strings.HasPrefix(e.Addr, "${") && strings.HasSuffix(e.Addr, "}") {
		return nil
	}
	if _, err := netip.ParseAddr(e.Addr); err != nil {
		return fmt.Errorf("%s.addr %q: %w", label, e.Addr, err)
	}
	return nil
}

func validateFirewall(fw Firewall) error {
	if fw.DisplayName == "" {
		return errors.New("firewall.display_name is required for portproxy+firewall bridges")
	}
	if fw.Remote == "" {
		return errors.New("firewall.remote is required for portproxy+firewall bridges")
	}
	if _, err := netip.ParsePrefix(fw.Remote); err != nil {
		return fmt.Errorf("firewall.remote %q: %w", fw.Remote, err)
	}
	return nil
}
