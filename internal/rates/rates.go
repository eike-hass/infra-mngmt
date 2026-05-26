// Package rates loads operator-supplied per-model token pricing from
// ~/.config/infra-mngmt/model-rates.yaml (or wherever `model_rates_file` in
// config.yaml points). Used by the Open Design card to compute a blended
// cost approximation from the sidecar's token-only `/usage` payload.
//
// No built-in fallback: a model id absent from the file produces no cost.
// The OD chip renders `—` for those models so it's visible to the operator
// that a rate needs to be added.
//
// Equivalent pricing: a model entry can carry an `equivalent_of: <other-id>`
// hint pointing at a paid model whose rate represents "what it would have
// cost on a comparable hosted endpoint." Useful for free or self-hosted
// models so the OD card surfaces a `≈ $X.XX` figure instead of `$0` or
// `—`. The reference is resolved at Load() time — the equivalent rate is
// inlined into Rate.EquivalentIn/Out/CacheRead so the consumer doesn't
// need access to the full table.
package rates

import (
	"fmt"
	"log"
	"os"

	"gopkg.in/yaml.v3"
)

// Rate is the USD-per-million-token pricing for one model. CacheRead is the
// Anthropic prompt-cache read rate (typically ~10% of input). Reasoning
// tokens bill at the Out rate per Anthropic's pricing — handled at the
// usage site, not stored separately here.
//
// EquivalentOf points at another model id in the same file. When set and
// resolvable at Load(), the target's rates are inlined into the Equivalent*
// fields below and HasEquivalent flips true.
type Rate struct {
	In        float64 `yaml:"in"`
	Out       float64 `yaml:"out"`
	CacheRead float64 `yaml:"cache_read"`

	EquivalentOf string `yaml:"equivalent_of,omitempty"`

	// Populated at Load() from the EquivalentOf target. Zero / false when
	// EquivalentOf is unset or the reference doesn't resolve.
	EquivalentIn        float64 `yaml:"-"`
	EquivalentOut       float64 `yaml:"-"`
	EquivalentCacheRead float64 `yaml:"-"`
	HasEquivalent       bool    `yaml:"-"`
}

// File is the on-disk shape of model-rates.yaml.
type File struct {
	Models map[string]Rate `yaml:"models"`
}

// Load reads model-rates.yaml from path. Empty path or missing file returns
// an empty File and no error (the feature is opt-in — when no file is
// present, the OD card simply renders `—` for every cost slot).
//
// After parsing, every entry's `equivalent_of` is resolved against the
// same map and the target's rate is inlined into the Equivalent* fields.
// Unresolved references log a warning and leave HasEquivalent false.
// Aliases are resolved one hop only — a chained equivalent_of pointing at
// another entry with equivalent_of does NOT follow the chain.
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
	f.resolveEquivalents()
	return &f, nil
}

// resolveEquivalents walks the model map and inlines each entry's
// equivalent_of target rate into its Equivalent* fields. A missing target
// logs a warning so the operator notices the typo without blocking the
// rest of the table.
func (f *File) resolveEquivalents() {
	for id, r := range f.Models {
		if r.EquivalentOf == "" {
			continue
		}
		target, ok := f.Models[r.EquivalentOf]
		if !ok {
			log.Printf("model-rates: %q.equivalent_of=%q — target not found in models", id, r.EquivalentOf)
			continue
		}
		r.EquivalentIn = target.In
		r.EquivalentOut = target.Out
		r.EquivalentCacheRead = target.CacheRead
		r.HasEquivalent = true
		f.Models[id] = r
	}
}
