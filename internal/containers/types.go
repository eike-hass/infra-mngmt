// Package containers loads declarations for Docker containers that back
// infra-mngmt entities. Each entry can be in one of three lifecycle modes:
//
//  1. External-only: name + (optional description). infra-mngmt observes
//     running state by name; it does not start or stop the container. This
//     is the original (and still-supported) shape, used for containers
//     managed by some other tool (e.g. an external compose project).
//
//  2. Inline: name + image + (optional docker_args). infra-mngmt can also
//     start the container via `docker run <args> <image>` and stop it. Use
//     this for trivial single-container services where authoring a compose
//     file would be overkill.
//
//  3. Compose: name + compose_file. infra-mngmt invokes
//     `docker compose -f <file> {up,down}` on the user-authored compose
//     file. Use this for anything non-trivial (multi-service, multi-mount,
//     custom networks, complex security flags). The compose file remains
//     runnable standalone — infra-mngmt is just a supervisor.
//
// Orthogonal to lifecycle, a Compose entry MAY declare its services[] —
// the docker containers belonging to the project. When set, the UI renders
// the entry as a project group (header row + per-service rows) inside the
// services panel instead of as a single flat row; the project header owns
// the lifecycle button.
//
// A container may additionally declare a `kind` (e.g. "mcp-fs",
// "open-design") that activates kind-specific behavior in the UI — vault
// allowlist editor for mcp-fs, privileged web-URL + token-stats card for
// open-design.
package containers

import (
	"fmt"
	"path/filepath"
	"regexp"
)

// Container is a single declared container that infra-mngmt should track,
// and may optionally manage the lifecycle of.
//
// Name is matched against the Docker container name (the one shown by
// `docker ps`, without the leading slash). Case-insensitive.
type Container struct {
	Name string `yaml:"name"`
	// Description is a free-form label shown in the UI; if empty the name
	// is rendered alone.
	Description string `yaml:"description,omitempty"`

	// Inline lifecycle (mutually exclusive with ComposeFile). When set,
	// infra-mngmt can run `docker run <DockerArgs...> <Image>` to start
	// the container, and stop+remove it on stop.
	Image      string   `yaml:"image,omitempty"`
	DockerArgs []string `yaml:"docker_args,omitempty"`

	// Compose lifecycle (mutually exclusive with Image). Path to a
	// docker-compose.yaml that infra-mngmt will pass to
	// `docker compose -f <ComposeFile> ...`. Relative paths are resolved
	// against the directory holding the containers.yaml file at load time.
	ComposeFile string `yaml:"compose_file,omitempty"`

	// Kind is an optional discriminator that activates kind-specific UI
	// and API integrations. Recognized values: "mcp-fs" (vault allowlist
	// panel) and "open-design" (privileged compose-project card with
	// web-URL link and token-stats summary). Unrecognized values are
	// rejected at validation time.
	Kind string `yaml:"kind,omitempty"`

	// MCPFS is the kind-specific extension for `kind: mcp-fs`. Required
	// when Kind == "mcp-fs"; forbidden otherwise.
	MCPFS *MCPFSConfig `yaml:"api,omitempty"`

	// Services, when non-empty, declares this entry as a compose project
	// with multiple docker containers, enabling project-group rendering in
	// the services panel (one header row with project lifecycle controls,
	// per-service rows underneath). Requires ComposeFile to be set — the
	// project lifecycle is driven via `docker compose -f <file> {up,down}`.
	//
	// `kind: open-design` requires services with role "web" and "token-stats"
	// (each with a non-empty URL). Other kinds may add their own service-role
	// requirements as they're introduced. Plain compose projects (no kind)
	// have no per-role requirements — services[] is purely structural.
	Services []ServiceEntry `yaml:"services,omitempty"`
}

// ServiceEntry is one docker container inside a compose-project Container.
//
// Container is the docker container name (case-insensitive FindByName).
//
// Role is an optional, free-form label rendered in the services panel
// alongside the row. Kind-specific renderers may attach behavior to known
// roles (e.g. `kind: open-design` reads role="token-stats" + URL to load
// an inline usage summary).
//
// URL is an optional service-level URL — for role="web" it's the link
// shown next to the row; for role="token-stats" it's the base URL whose
// /usage path returns JSON. Other roles ignore URL.
type ServiceEntry struct {
	Container string `yaml:"container"`
	Role      string `yaml:"role,omitempty"`
	URL       string `yaml:"url,omitempty"`
}

// MCPFSConfig is the kind=mcp-fs extension. Control is the base URL of the
// vault's JSON control plane (e.g. "http://workspace-vault:3002") that
// infra-mngmt calls to read/write the allowlist. Reached over the shared
// Docker network — must NOT be host-published, since the control plane has
// no auth of its own and relies on infra-mngmt as the auth perimeter.
type MCPFSConfig struct {
	Control string `yaml:"control"`
}

// File is the on-disk shape of containers.yaml.
type File struct {
	Containers []Container `yaml:"containers"`
}

var (
	nameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,79}$`)

	// knownKinds is the set of `kind:` values infra-mngmt understands.
	// Adding a new kind requires a new extension struct on Container plus
	// matching handling downstream — explicit allowlist forces that
	// thinking rather than silently accepting typos.
	knownKinds = map[string]struct{}{
		"mcp-fs":      {},
		"open-design": {},
	}
)

// Validate checks file-level invariants. Names are Docker-legal and unique
// case-insensitively (per Docker's container-name rules — a superset of
// process-compose's, so any legal name here can also be referenced from
// dependencies.yaml). Lifecycle modes are mutually exclusive. Kind, when
// set, must be in the known set, and the matching extension must be
// present (and absent for other kinds).
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

		// Lifecycle modes are mutually exclusive.
		if c.Image != "" && c.ComposeFile != "" {
			return fmt.Errorf("containers[%d] %q: image and compose_file are mutually exclusive", i, c.Name)
		}
		// docker_args without image is a config smell — silently accepting
		// would let typos drift past review.
		if len(c.DockerArgs) > 0 && c.Image == "" {
			return fmt.Errorf("containers[%d] %q: docker_args requires image", i, c.Name)
		}
		// Resolve relative compose paths defensively (the loader will pass
		// the canonical path; this rejects accidentally-empty entries).
		if c.ComposeFile != "" && filepath.Clean(c.ComposeFile) == "." {
			return fmt.Errorf("containers[%d] %q: compose_file must be a path", i, c.Name)
		}

		// Kind discriminator. Unrecognized kinds are rejected so typos don't
		// silently produce default rendering.
		if c.Kind != "" {
			if _, ok := knownKinds[c.Kind]; !ok {
				return fmt.Errorf("containers[%d] %q: unknown kind %q", i, c.Name, c.Kind)
			}
		}

		// Generic services[] validation. When set, this entry represents a
		// compose project — ComposeFile is required to drive lifecycle, and
		// each ServiceEntry's container name must be Docker-legal. Roles are
		// free-form except where a kind constrains them (below).
		if len(c.Services) > 0 {
			if c.ComposeFile == "" {
				return fmt.Errorf("containers[%d] %q: services[] requires compose_file", i, c.Name)
			}
			for j, s := range c.Services {
				if !nameRe.MatchString(s.Container) {
					return fmt.Errorf("containers[%d] %q: services[%d].container %q must match %s", i, c.Name, j, s.Container, nameRe.String())
				}
			}
		}

		// Kind-specific extension + service-role constraints.
		switch c.Kind {
		case "mcp-fs":
			if c.MCPFS == nil {
				return fmt.Errorf("containers[%d] %q: kind %q requires `api: { control: ... }`", i, c.Name, c.Kind)
			}
			if c.MCPFS.Control == "" {
				return fmt.Errorf("containers[%d] %q: api.control must be a non-empty URL", i, c.Name)
			}
		case "open-design":
			// kind=open-design requires a web service (URL is the OD UI link)
			// and a token-stats service (URL is the sidecar's base for /usage).
			// Both back the dedicated card; the card cannot render without them.
			if len(c.Services) == 0 {
				return fmt.Errorf("containers[%d] %q: kind %q requires services[] (with roles web + token-stats)", i, c.Name, c.Kind)
			}
			var hasWeb, hasTokenStats bool
			for _, s := range c.Services {
				switch s.Role {
				case "web":
					if s.URL == "" {
						return fmt.Errorf("containers[%d] %q: kind %q service role=web requires url", i, c.Name, c.Kind)
					}
					hasWeb = true
				case "token-stats":
					if s.URL == "" {
						return fmt.Errorf("containers[%d] %q: kind %q service role=token-stats requires url", i, c.Name, c.Kind)
					}
					hasTokenStats = true
				}
			}
			if !hasWeb {
				return fmt.Errorf("containers[%d] %q: kind %q requires a service with role: web", i, c.Name, c.Kind)
			}
			if !hasTokenStats {
				return fmt.Errorf("containers[%d] %q: kind %q requires a service with role: token-stats", i, c.Name, c.Kind)
			}
			if c.MCPFS != nil {
				return fmt.Errorf("containers[%d] %q: api block requires kind: mcp-fs", i, c.Name)
			}
		default:
			if c.MCPFS != nil {
				return fmt.Errorf("containers[%d] %q: api block requires kind: mcp-fs", i, c.Name)
			}
		}
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
