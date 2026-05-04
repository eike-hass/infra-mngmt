package deps

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// rawRule is the on-disk shape: needs are strings, parsed at load time.
type rawRule struct {
	Entity string   `yaml:"entity"`
	Scope  string   `yaml:"scope"`
	Needs  []string `yaml:"needs"`
}

type rawFile struct {
	Dependencies []rawRule `yaml:"dependencies"`
}

// Load reads dependencies.yaml from path. Empty path or missing file returns
// an empty File and no error (the feature is opt-in).
func Load(path string) (*File, error) {
	if path == "" {
		return &File{}, nil
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &File{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read dependencies file: %w", err)
	}
	var raw rawFile
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse dependencies file: %w", err)
	}
	out := &File{Dependencies: make([]Rule, 0, len(raw.Dependencies))}
	for i, r := range raw.Dependencies {
		needs := make([]Need, 0, len(r.Needs))
		for j, ns := range r.Needs {
			n, err := ParseNeedString(ns)
			if err != nil {
				return nil, fmt.Errorf("dependencies[%d].needs[%d]: %w", i, j, err)
			}
			needs = append(needs, n)
		}
		out.Dependencies = append(out.Dependencies, Rule{
			Entity: r.Entity,
			Scope:  r.Scope,
			Needs:  needs,
		})
	}
	if err := out.Validate(); err != nil {
		return nil, fmt.Errorf("validate dependencies file: %w", err)
	}
	return out, nil
}
