package bridge

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Load reads bridges.yaml from path and validates the result. A missing file
// returns an empty File and no error — bridges are an optional feature.
func Load(path string) (*File, error) {
	if path == "" {
		return &File{}, nil
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &File{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read bridges file: %w", err)
	}
	var f File
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse bridges file: %w", err)
	}
	if err := f.Validate(); err != nil {
		return nil, fmt.Errorf("validate bridges file: %w", err)
	}
	return &f, nil
}
