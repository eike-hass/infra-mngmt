package web

import (
	"context"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/eike-hass/infra-mngmt/internal/containers"
	"github.com/eike-hass/infra-mngmt/internal/docker"
	"github.com/eike-hass/infra-mngmt/internal/graph"
)

// containerView is the template-friendly representation of one declared
// container: declaration metadata + observed Docker state.
type containerView struct {
	Name        string
	Description string
	Image       string
	State       string // "running" | "stopped" | "missing" | "unknown"
	StateClass  string // CSS class for the status pill
	Status      string // raw Docker status string ("Up 2h", "Exited (0) 5m ago", …)
	ID          string // short container ID, empty if missing
	HasStats    bool   // true when a CPU/Mem snapshot was sampled for this row
	CPU         float64
	Mem         int64

	// Kind discriminator from containers.yaml. When "mcp-fs" the template
	// renders an HTMX-loaded vault panel (allowlist editor + tree browser)
	// inside the container card. Empty for ordinary containers.
	Kind string
}

// snapshotContainers queries Docker for the state of every declared container
// and returns a slice in the order declarations appear in containers.yaml.
// Returns nil when no Docker client is available (Docker discovery skipped at
// startup) — the resolver treats that as "no container needs satisfied".
func (s *Server) snapshotContainers(ctx context.Context) []graph.ContainerInfo {
	if len(s.containerDecls) == 0 || s.docker == nil {
		return nil
	}
	out := make([]graph.ContainerInfo, 0, len(s.containerDecls))
	for _, c := range s.containerDecls {
		ci := graph.ContainerInfo{Name: c.Name, Description: c.Description, State: graph.ContainerMissing}
		mc, found, err := s.docker.FindByName(ctx, c.Name)
		if err != nil {
			log.Printf("containers: lookup %q: %v", c.Name, err)
			ci.State = graph.ContainerUnknown
			out = append(out, ci)
			continue
		}
		if !found {
			out = append(out, ci)
			continue
		}
		ci.ID = mc.ID
		ci.Status = mc.State
		switch strings.ToLower(mc.State) {
		case "running", "restarting":
			ci.State = graph.ContainerRunning
		case "exited", "dead", "paused", "created":
			ci.State = graph.ContainerStopped
		default:
			ci.State = graph.ContainerUnknown
		}
		out = append(out, ci)
	}
	return out
}

// rebuildContainerViews returns view structs for the services-panel template.
// CPU/mem stats for running containers are sampled in parallel — each call
// blocks ~1 s server-side for delta priming, so serial sampling would balloon
// the partial-refresh latency with every additional container.
func (s *Server) rebuildContainerViews(ctx context.Context) []containerView {
	if len(s.containerDecls) == 0 {
		return nil
	}
	infos := s.snapshotContainers(ctx)
	views := make([]containerView, len(infos))
	var wg sync.WaitGroup
	for i, ci := range infos {
		v := containerView{
			Name:        ci.Name,
			Description: ci.Description,
			State:       ci.State.String(),
			StateClass:  containerStateCSS(ci.State),
			Status:      ci.Status,
			ID:          shortID(ci.ID),
		}
		// Look up the kind discriminator from the declaration so the
		// template can branch on mcp-fs etc.
		if d := s.findContainerDecl(ci.Name); d != nil {
			v.Kind = d.Kind
		}
		views[i] = v
		if ci.State == graph.ContainerRunning && ci.ID != "" && s.docker != nil {
			wg.Add(1)
			go func(idx int, fullID string) {
				defer wg.Done()
				st, err := s.docker.Stats(ctx, fullID)
				if err != nil {
					return
				}
				views[idx].HasStats = true
				views[idx].CPU = st.CPU
				views[idx].Mem = st.Mem
			}(i, ci.ID)
		}
	}
	wg.Wait()
	sort.Slice(views, func(i, j int) bool { return views[i].Name < views[j].Name })
	return views
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// dockerHealth pings the daemon to drive the LED in the containers panel
// header. Configured=false suppresses the indicator entirely when no Docker
// client was discovered at startup.
func (s *Server) dockerHealth(ctx context.Context) dockerHealthView {
	if s.docker == nil {
		return dockerHealthView{}
	}
	v := dockerHealthView{Configured: true, Endpoint: s.docker.DaemonHost()}
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := s.docker.Ping(pingCtx); err != nil {
		v.Error = err.Error()
		return v
	}
	v.Online = true
	return v
}

func containerStateCSS(s graph.ContainerState) string {
	switch s {
	case graph.ContainerRunning:
		return "running"
	case graph.ContainerStopped, graph.ContainerMissing:
		return "stopped"
	default:
		return "unknown"
	}
}

// handleContainerStartByName looks up the named container and starts it.
// Distinct from the /api/container/start used by the entity-side container
// management UI (which takes a Docker ID).
//
// Branches on the declaration's lifecycle mode:
//   - compose_file set    → `docker compose -f <file> up -d`
//   - image set           → `docker run` with docker_args (not yet implemented;
//     falls through to find-and-start for now)
//   - external-only       → find the existing container by name, start it
func (s *Server) handleContainerStartByName(w http.ResponseWriter, r *http.Request) {
	s.containerLifecycleByName(w, r, lifecycleStart)
}

func (s *Server) handleContainerStopByName(w http.ResponseWriter, r *http.Request) {
	s.containerLifecycleByName(w, r, lifecycleStop)
}

type lifecycleAction int

const (
	lifecycleStart lifecycleAction = iota
	lifecycleStop
)

func (s *Server) containerLifecycleByName(w http.ResponseWriter, r *http.Request, action lifecycleAction) {
	name := r.URL.Query().Get("name")
	if !validProcessName(name) {
		http.Error(w, "invalid container name", http.StatusBadRequest)
		return
	}
	if s.docker == nil {
		http.Error(w, "Docker client unavailable", http.StatusServiceUnavailable)
		return
	}

	// Find the declaration, if any. External-only mode (no declaration
	// owning the lifecycle) falls back to find-and-start the existing
	// container by name. That keeps the historic behavior for entries
	// without compose_file/image.
	decl := s.findContainerDecl(name)

	switch {
	case decl != nil && decl.ComposeFile != "":
		s.composeAction(w, r, *decl, action)
		return
	case decl != nil && decl.Image != "":
		// Inline `docker run` mode is declared but the runner isn't wired
		// in yet. Surface an explicit 501 rather than silently falling
		// back to find-and-start (which would do nothing useful).
		http.Error(w, "inline image lifecycle not yet implemented; use compose_file or external-only mode", http.StatusNotImplemented)
		return
	default:
		s.externalContainerAction(w, r, name, action)
	}
}

// composeAction runs `docker compose up -d` or `down` for a declared stack,
// then verifies via the SDK that the container reached the requested state.
// If `docker compose` was a no-op (which happens when the running container
// was started externally with a different compose project name — labels
// don't match our `-p decl.Name` filter), the SDK fallback ensures the
// stop/start actually happens.
//
// This dual-path is the simplest fix for a real-world workflow: the user
// runs `docker compose up` from a shell with the compose file mounted at a
// different path than what's in containers.yaml, then expects the panel
// button to control the resulting container. Compose's project/label
// filtering rules don't help us in that case, so we fall through to the
// label-agnostic SDK call.
func (s *Server) composeAction(w http.ResponseWriter, r *http.Request, decl containers.Container, action lifecycleAction) {
	cmp := docker.Compose{File: decl.ComposeFile, Project: decl.Name}
	verb := "up"
	op := cmp.Up
	if action == lifecycleStop {
		verb = "down"
		op = cmp.Down
	}
	// Compose pass is best-effort: it cleans up networks/anon volumes when
	// the project matches; when it doesn't match, it silently exits 0 with
	// "no resources to remove" / "no service to start" — those aren't errors,
	// they just mean the labels don't line up and the SDK fallback below
	// will do the actual work.
	if out, err := op(r.Context()); err != nil {
		log.Printf("container: compose %s on %s soft-failed (will try SDK fallback): %v / %s", verb, decl.Name, err, strings.TrimSpace(string(out)))
	} else {
		log.Printf("container: compose %s on %s OK (%d bytes output)", verb, decl.Name, len(out))
	}

	// Verify the requested state via the SDK and correct if needed.
	mc, found, err := s.docker.FindByName(r.Context(), decl.Name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if found {
		switch action {
		case lifecycleStop:
			if mc.State == "running" {
				if err := s.docker.StopContainer(r.Context(), mc.ID); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				log.Printf("container: SDK stop on %s (id=%s) reconciled state after compose no-op", decl.Name, mc.ID[:min(12, len(mc.ID))])
			}
		case lifecycleStart:
			if mc.State != "running" {
				if err := s.docker.StartContainer(r.Context(), mc.ID); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				log.Printf("container: SDK start on %s (id=%s) reconciled state after compose no-op", decl.Name, mc.ID[:min(12, len(mc.ID))])
			}
		}
	}

	s.handleServicesPartial(w, r)
}

// externalContainerAction is the legacy path: look up an existing container
// by name and start/stop it via the Docker SDK. Used for declarations with
// neither compose_file nor image (the original "just observe + start/stop"
// pattern).
func (s *Server) externalContainerAction(w http.ResponseWriter, r *http.Request, name string, action lifecycleAction) {
	mc, found, err := s.docker.FindByName(r.Context(), name)
	if err != nil {
		log.Printf("container: lookup %s: %v", name, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, "container not found", http.StatusNotFound)
		return
	}
	log.Printf("container: action on %s (id=%s)", name, mc.ID[:min(12, len(mc.ID))])
	var actErr error
	switch action {
	case lifecycleStart:
		actErr = s.docker.StartContainer(r.Context(), mc.ID)
	case lifecycleStop:
		actErr = s.docker.StopContainer(r.Context(), mc.ID)
	}
	if actErr != nil {
		log.Printf("container: action on %s failed: %v", name, actErr)
		http.Error(w, actErr.Error(), http.StatusInternalServerError)
		return
	}
	s.handleServicesPartial(w, r)
}

// findContainerDecl returns the matching declaration (case-insensitive), or
// nil if no entry matches. Mirrors the container loader's name-matching
// rules.
func (s *Server) findContainerDecl(name string) *containers.Container {
	want := lowerASCII(name)
	for i := range s.containerDecls {
		if lowerASCII(s.containerDecls[i].Name) == want {
			return &s.containerDecls[i]
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

// handleContainersRefresh re-reads containers.yaml from disk so YAML edits
// take effect without a server restart.
func (s *Server) handleContainersRefresh(w http.ResponseWriter, r *http.Request) {
	if s.containersFile != "" {
		f, err := containers.Load(s.containersFile)
		if err != nil {
			http.Error(w, "reload containers.yaml: "+err.Error(), http.StatusInternalServerError)
			return
		}
		s.containerDecls = f.Containers
		log.Printf("containers: refreshed — %d entries from %s", len(f.Containers), s.containersFile)
	}
	s.handleServicesPartial(w, r)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
