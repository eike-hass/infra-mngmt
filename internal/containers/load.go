package containers

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Load reads containers.yaml from path. Empty path or missing file returns
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
		return nil, fmt.Errorf("read containers file: %w", err)
	}
	var f File
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse containers file: %w", err)
	}
	if err := f.Validate(); err != nil {
		return nil, fmt.Errorf("validate containers file: %w", err)
	}
	return &f, nil
}
