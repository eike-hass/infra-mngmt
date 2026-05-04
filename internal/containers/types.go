// Package containers loads declarations for plain Docker containers that
// back infra-mngmt entities. It's intentionally narrower than the bridge or
// process-compose pipelines: the model is "match a container by name and
// surface its running state". Compose-file ownership and multi-service
// projects are deliberately out of scope for this iteration.
package containers

import (
	"fmt"
	"regexp"
)

// Container is a single declared container that infra-mngmt should track.
// Name is matched against the Docker container name (the one shown by
// `docker ps`, without the leading slash). Case-insensitive.
type Container struct {
	Name string `yaml:"name"`
	// Description is a free-form label shown in the UI; if empty the name
	// is rendered alone.
	Description string `yaml:"description,omitempty"`
}

// File is the on-disk shape of containers.yaml.
type File struct {
	Containers []Container `yaml:"containers"`
}

var nameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,79}$`)

// Validate checks file-level invariants: each entry has a Docker-legal name
// (per Docker's container-name rules — a superset of process-compose's, so
// any legal name here can also be referenced from dependencies.yaml).
func (f *File) Validate() error {
	seen := make(map[string]struct{}, len(f.Containers))
	for i, c := range f.Containers {
		if !nameRe.MatchString(c.Name) {
			return fmt.Errorf("containers[%d]: name %q must match %s", i, c.Name, nameRe.String())
		}
		key := lowerASCII(c.Name)
		if _, dup := seen[key]; dup {
			return fmt.Errorf("containers[%d]: duplicate name %q (case-insensitive)", i, c.Name)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func lowerASCII(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 32
		}
		b[i] = c
	}
	return string(b)
}
