// Package deps loads and matches central dependency rules — declarations of
// which backing services or bridges each Claude Code entity needs. Rules
// live in dependencies.yaml under the infra-mngmt config directory; consumer
// entities never carry this metadata themselves (no .claude/ pollution).
package deps

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// Need is one supplier reference declared by a Rule. Kind is "service" (a
// process-compose process) or "bridge" (a bridges.yaml entry). Tier is
// optional — used to disambiguate when a service name exists on multiple
// tiers.
type Need struct {
	Kind string // "service" | "bridge"
	Name string
	Tier string // "wsl" | "windows" | "" (any)
}

func (n Need) String() string {
	s := n.Kind + ":" + n.Name
	if n.Tier != "" {
		s += "@" + n.Tier
	}
	return s
}

// Rule binds a consumer entity (matched by kind + name + scope pattern) to
// the suppliers it requires. Multiple rules can match the same entity; their
// needs are unioned.
type Rule struct {
	Entity string `yaml:"entity"` // "<kind>:<name>"
	Scope  string `yaml:"scope"`  // "*" | "host" | "project:*" | "project:<abs-path>" | "container:*" | "container:<folder>"
	Needs  []Need `yaml:"needs"`
}

// File is the on-disk shape of dependencies.yaml.
type File struct {
	Dependencies []Rule `yaml:"dependencies"`
}

var (
	entityRe = regexp.MustCompile(`^(mcp|skill|hook|agent|memory|setting|command|claude_md):[a-zA-Z0-9_.-]+$`)
	needRe   = regexp.MustCompile(`^(service|bridge|container):[a-z0-9][a-z0-9_.-]*(@(wsl|windows|container:[^@]+))?$`)
)

// Validate checks file-level invariants. Each rule's `needs` is parsed in
// place (turning string "service:foo@wsl" into a Need struct), so callers
// must use Validate before reading needs.
func (f *File) Validate() error {
	for i := range f.Dependencies {
		r := &f.Dependencies[i]
		if err := r.Validate(); err != nil {
			return fmt.Errorf("dependencies[%d]: %w", i, err)
		}
	}
	return nil
}

// Validate normalises and checks one rule.
func (r *Rule) Validate() error {
	if !entityRe.MatchString(r.Entity) {
		return fmt.Errorf("entity %q: must match %s", r.Entity, entityRe.String())
	}
	if err := validateScope(r.Scope); err != nil {
		return err
	}
	if len(r.Needs) == 0 {
		return fmt.Errorf("entity %q: needs is empty", r.Entity)
	}
	return nil
}

func validateScope(s string) error {
	switch s {
	case "*", "host", "project:*", "container:*":
		return nil
	}
	if strings.HasPrefix(s, "project:") {
		path := strings.TrimPrefix(s, "project:")
		if !filepath.IsAbs(path) {
			return fmt.Errorf("scope %q: project: must be either '*' or an absolute path", s)
		}
		return nil
	}
	if strings.HasPrefix(s, "container:") {
		// any non-empty folder name is fine; we don't enforce shape here.
		return nil
	}
	return fmt.Errorf("scope %q: unknown form", s)
}

// ParseNeedString parses a need string like "service:foo@wsl" into a Need
// struct. Used by the YAML unmarshaller via UnmarshalYAML.
func ParseNeedString(s string) (Need, error) {
	if !needRe.MatchString(s) {
		return Need{}, fmt.Errorf("need %q: must match %s", s, needRe.String())
	}
	colon := strings.IndexByte(s, ':')
	at := strings.IndexByte(s, '@')
	n := Need{Kind: s[:colon]}
	if at == -1 {
		n.Name = s[colon+1:]
	} else {
		n.Name = s[colon+1 : at]
		n.Tier = s[at+1:]
	}
	return n, nil
}
