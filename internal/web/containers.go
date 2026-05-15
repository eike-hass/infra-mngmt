package web

import (
	"context"
	"encoding/json"
	"fmt"
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
//
// Declarations with non-empty Services[] are excluded; those render as
// project groups inside the containers section (one header row + per-service
// rows), not as a single flat row. See buildContainerProjectGroups.
func (s *Server) snapshotContainers(ctx context.Context) []graph.ContainerInfo {
	if len(s.containerDecls) == 0 || s.docker == nil {
		return nil
	}
	out := make([]graph.ContainerInfo, 0, len(s.containerDecls))
	for _, c := range s.containerDecls {
		if len(c.Services) > 0 {
			continue
		}
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

// containerProjectView represents one compose-project entry (any
// declaration with non-empty Services[]) as a grouped row block in the
// services panel. The header carries the rolled-up state and the project
// lifecycle buttons; the body holds per-service rows for each docker
// container in the project.
//
// Kind-specific renderers (currently `open-design`) layer extra display
// on top — the OD card section iterates ContainerProjects filtered to
// Kind=="open-design" and renders WebURL + the HTMX token-stats slot.
type containerProjectView struct {
	Name        string
	Description string
	ComposeFile string
	Kind        string

	// Rolled-up state across all services. "running" when every service is
	// running; "stopped" when none are; "degraded" when some are. Drives the
	// pill at the top of the group/card.
	State      string
	StateClass string

	// WebURL, when non-empty, is the URL of the role=web service — rendered
	// as a clickable link on the OD card (kind-specific use).
	WebURL string

	// TokenStatsContainer, when non-empty, is the docker container name of
	// the role=token-stats service. The OD card's HTMX slot uses this name
	// to build the /partials/open-design/token-stats?name=<...> URL.
	TokenStatsContainer string

	// Containers is one row per service in the project, populated via
	// FindByName. A missing service shows as "missing" rather than failing
	// the whole group.
	Containers []containerView
}

// buildContainerProjectGroups enumerates entries with non-empty Services[]
// and returns project-group views. Per-service status is resolved via
// FindByName (case-insensitive) — same logic as snapshotContainers, applied
// per service.Container.
//
// The token-stats summary for kind=open-design is loaded asynchronously via
// HTMX from /partials/open-design/token-stats — this builder only carries
// the container name needed to construct the URL.
func (s *Server) buildContainerProjectGroups(ctx context.Context) []containerProjectView {
	if len(s.containerDecls) == 0 {
		return nil
	}
	var views []containerProjectView
	for _, c := range s.containerDecls {
		if len(c.Services) == 0 {
			continue
		}
		v := containerProjectView{
			Name:        c.Name,
			Description: c.Description,
			ComposeFile: c.ComposeFile,
			Kind:        c.Kind,
		}
		// Surface known service roles onto the project view for kind-
		// specific renderers. role=web → clickable link; role=token-stats
		// → HTMX target for the usage summary.
		for _, sd := range c.Services {
			switch sd.Role {
			case "web":
				if v.WebURL == "" {
					v.WebURL = sd.URL
				}
			case "token-stats":
				if v.TokenStatsContainer == "" {
					v.TokenStatsContainer = sd.Container
				}
			}
		}

		var running, total int
		for _, sd := range c.Services {
			row := containerView{
				Name:       sd.Container,
				State:      graph.ContainerMissing.String(),
				StateClass: containerStateCSS(graph.ContainerMissing),
			}
			total++
			if s.docker != nil {
				mc, found, err := s.docker.FindByName(ctx, sd.Container)
				if err != nil {
					log.Printf("project %q: lookup %q: %v", c.Name, sd.Container, err)
					row.State = graph.ContainerUnknown.String()
					row.StateClass = containerStateCSS(graph.ContainerUnknown)
				} else if found {
					row.ID = shortID(mc.ID)
					row.Status = mc.State
					switch strings.ToLower(mc.State) {
					case "running", "restarting":
						row.State = graph.ContainerRunning.String()
						row.StateClass = containerStateCSS(graph.ContainerRunning)
						running++
					case "exited", "dead", "paused", "created":
						row.State = graph.ContainerStopped.String()
						row.StateClass = containerStateCSS(graph.ContainerStopped)
					default:
						row.State = graph.ContainerUnknown.String()
						row.StateClass = containerStateCSS(graph.ContainerUnknown)
					}
				}
			}
			v.Containers = append(v.Containers, row)
		}
		// Roll-up: all running → running; none running → stopped; mixed → degraded.
		switch {
		case total > 0 && running == total:
			v.State = graph.ContainerRunning.String()
			v.StateClass = "running"
		case running == 0:
			v.State = graph.ContainerStopped.String()
			v.StateClass = "stopped"
		default:
			v.State = "degraded"
			v.StateClass = "degraded"
		}
		views = append(views, v)
	}
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

// openDesignTokenStatsView is the data model for the token-stats slot on an
// Open Design card. Either Stats is non-nil (success) or Error is set
// (fetch failed); the template renders one or the other.
type openDesignTokenStatsView struct {
	ServiceName string // for the retry URL
	Stats       *openDesignTokenStats
	Error       string
}

type openDesignTokenStats struct {
	Sessions     int
	Messages     int
	InputTokens  int64
	OutputTokens int64
	CacheRead    int64
	CacheHitPct  float64
}

// tokenStatsReport mirrors the JSON shape returned by od-token-stats's
// /usage endpoint. Only the totals are decoded — per-session data isn't
// rendered inline on the OD card.
type tokenStatsReport struct {
	Totals struct {
		Sessions    int     `json:"sessions"`
		Messages    int     `json:"messages"`
		CacheHitPct float64 `json:"cache_hit_pct"`
		Tokens      struct {
			Input      int64 `json:"input"`
			Output     int64 `json:"output"`
			Reasoning  int64 `json:"reasoning"`
			CacheRead  int64 `json:"cache_read"`
			CacheWrite int64 `json:"cache_write"`
		} `json:"tokens"`
	} `json:"totals"`
}

// handleOpenDesignTokenStats fetches usage from a token-stats sidecar and
// renders the inline summary partial. Request: GET ?name=<container>.
// The container name is looked up against open-design declarations to find
// the configured usage_url — operator-controlled input via containers.yaml.
func (s *Server) handleOpenDesignTokenStats(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		http.Error(w, "missing name", http.StatusBadRequest)
		return
	}

	var usageURL string
	for _, c := range s.containerDecls {
		if c.Kind != "open-design" {
			continue
		}
		for _, svc := range c.Services {
			if strings.EqualFold(svc.Container, name) && svc.Role == "token-stats" {
				usageURL = svc.URL
				break
			}
		}
		if usageURL != "" {
			break
		}
	}

	view := openDesignTokenStatsView{ServiceName: name}
	if usageURL == "" {
		view.Error = fmt.Sprintf("no token-stats service declared for %q", name)
		s.renderOpenDesignTokenStats(w, view)
		return
	}

	fetchCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(fetchCtx, http.MethodGet, strings.TrimRight(usageURL, "/")+"/usage", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		view.Error = "unreachable: " + err.Error()
		s.renderOpenDesignTokenStats(w, view)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		view.Error = fmt.Sprintf("HTTP %d from %s", resp.StatusCode, usageURL)
		s.renderOpenDesignTokenStats(w, view)
		return
	}

	var report tokenStatsReport
	if err := json.NewDecoder(resp.Body).Decode(&report); err != nil {
		view.Error = "bad JSON: " + err.Error()
		s.renderOpenDesignTokenStats(w, view)
		return
	}

	view.Stats = &openDesignTokenStats{
		Sessions:     report.Totals.Sessions,
		Messages:     report.Totals.Messages,
		InputTokens:  report.Totals.Tokens.Input,
		OutputTokens: report.Totals.Tokens.Output,
		CacheRead:    report.Totals.Tokens.CacheRead,
		CacheHitPct:  report.Totals.CacheHitPct,
	}
	s.renderOpenDesignTokenStats(w, view)
}

func (s *Server) renderOpenDesignTokenStats(w http.ResponseWriter, view openDesignTokenStatsView) {
	tmpl := parseTemplate("ods", "templates/open_design_token_stats.html.tmpl")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = tmpl.Execute(w, view)
}
