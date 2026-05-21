// Package rates loads operator-supplied per-model token pricing from
// ~/.config/infra-mngmt/model-rates.yaml (or wherever `model_rates_file` in
// config.yaml points). Used by the Open Design card to compute a blended
// cost approximation from the sidecar's token-only `/usage` payload.
//
// No built-in fallback: a model id absent from the file produces no cost.
// The OD chip renders `—` for those models so it's visible to the operator
// that a rate needs to be added.
package rates

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Rate is the USD-per-million-token pricing for one model. CacheRead is the
// Anthropic prompt-cache read rate (typically ~10% of input). Reasoning
// tokens bill at the Out rate per Anthropic's pricing — handled at the
// usage site, not stored separately here.
type Rate struct {
	In        float64 `yaml:"in"`
	Out       float64 `yaml:"out"`
	CacheRead float64 `yaml:"cache_read"`
}

// File is the on-disk shape of model-rates.yaml.
type File struct {
	Models map[string]Rate `yaml:"models"`
}

// Load reads model-rates.yaml from path. Empty path or missing file returns
// an empty File and no error (the feature is opt-in — when no file is
// present, the OD card simply renders `—` for every cost slot).
func Load(path string) (*File, error) {
	if path == "" {
		return &File{Models: map[string]Rate{}}, nil
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &File{Models: map[string]Rate{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read model-rates file: %w", err)
	}
	var f File
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse model-rates file: %w", err)
	}
	if f.Models == nil {
		f.Models = map[string]Rate{}
	}
	return &f, nil
}
