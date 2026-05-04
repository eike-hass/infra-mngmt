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

// Bridge is one persistent network forwarding rule.
type Bridge struct {
	Name     string   `yaml:"name"`
	Tier     Tier     `yaml:"tier"`
	Type     Type     `yaml:"type"`
	Listen   Endpoint `yaml:"listen"`
	Connect  Endpoint `yaml:"connect"`
	Firewall Firewall `yaml:"firewall,omitempty"`
}

// File is the on-disk shape of bridges.yaml.
type File struct {
	Bridges []Bridge `yaml:"bridges"`
}

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// Validate checks the file-level invariants (unique names, supported tier+type
// combos, valid endpoints/firewall).
func (f *File) Validate() error {
	seen := make(map[string]struct{}, len(f.Bridges))
	for i := range f.Bridges {
		b := &f.Bridges[i]
		if err := b.Validate(); err != nil {
			return fmt.Errorf("bridges[%d] (%q): %w", i, b.Name, err)
		}
		if _, dup := seen[b.Name]; dup {
			return fmt.Errorf("bridges[%d]: duplicate name %q", i, b.Name)
		}
		seen[b.Name] = struct{}{}
	}
	return nil
}

// Validate checks one bridge entry.
func (b *Bridge) Validate() error {
	if !nameRe.MatchString(b.Name) {
		return fmt.Errorf("name %q must match %s", b.Name, nameRe.String())
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
