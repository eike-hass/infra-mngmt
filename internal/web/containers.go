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
func (s *Server) handleContainerStartByName(w http.ResponseWriter, r *http.Request) {
	s.containerActionByName(w, r, func(id string) error {
		return s.docker.StartContainer(r.Context(), id)
	})
}

func (s *Server) handleContainerStopByName(w http.ResponseWriter, r *http.Request) {
	s.containerActionByName(w, r, func(id string) error {
		return s.docker.StopContainer(r.Context(), id)
	})
}

func (s *Server) containerActionByName(w http.ResponseWriter, r *http.Request, fn func(string) error) {
	name := r.URL.Query().Get("name")
	if !validProcessName(name) {
		http.Error(w, "invalid container name", http.StatusBadRequest)
		return
	}
	if s.docker == nil {
		http.Error(w, "Docker client unavailable", http.StatusServiceUnavailable)
		return
	}
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
	if err := fn(mc.ID); err != nil {
		log.Printf("container: action on %s failed: %v", name, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.handleServicesPartial(w, r)
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
