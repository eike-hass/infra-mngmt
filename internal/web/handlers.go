package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/eike-hass/infra-mngmt/internal/compose"
	"github.com/eike-hass/infra-mngmt/internal/docker"
	"github.com/eike-hass/infra-mngmt/internal/entity"
	"github.com/eike-hass/infra-mngmt/internal/source"
)

var validName = regexp.MustCompile(`^[a-zA-Z0-9_\-]{1,128}$`)

// validProcessName rejects names that could be used to build malicious API paths.
func validProcessName(s string) bool { return validName.MatchString(s) }

// --- JSON API ---

func (s *Server) handleSources(w http.ResponseWriter, r *http.Request) {
	type sourceInfo struct {
		ID    string `json:"id"`
		Scope string `json:"scope"`
	}
	infos := make([]sourceInfo, 0, len(s.sources))
	for _, src := range s.sources {
		infos = append(infos, sourceInfo{ID: src.ID(), Scope: src.Scope().String()})
	}
	writeJSON(w, infos)
}

func (s *Server) handleEntities(w http.ResponseWriter, r *http.Request) {
	kindFilter := r.URL.Query().Get("kind")
	all, err := s.allEntities(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if kindFilter != "" {
		filtered := all[:0]
		for _, e := range all {
			if string(e.Kind) == kindFilter {
				filtered = append(filtered, e)
			}
		}
		all = filtered
	}
	writeJSON(w, all)
}

func (s *Server) handleEntityContent(w http.ResponseWriter, r *http.Request) {
	rawID := r.URL.Query().Get("id")
	if rawID == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	all, _ := s.allEntities(r.Context())
	for _, e := range all {
		if e.ID != rawID {
			continue
		}
		for _, src := range s.sources {
			if src.ID() != e.Source {
				continue
			}
			data, err := src.Read(r.Context(), e.Kind, e.Name)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Write(data)
			return
		}
	}
	http.NotFound(w, r)
}

// --- HTML UI ---

// projectTab represents one tab in the source bar, grouping all sources that
// share the same project root (host fs + devcontainer volume appear together).
type projectTab struct {
	Key    string   // "" = global; otherwise the project root path
	Label  string   // human-readable name, e.g. "~/.claude" or "my-repo"
	Levels []string // present scope levels, e.g. ["project","devcontainer"]
}

// sourceLevel returns "global", "project", or "devcontainer" for a source.
func sourceLevel(id string, global bool) string {
	if global {
		return "global"
	}
	if strings.HasPrefix(id, "vol:") || strings.HasPrefix(id, "ctr:") {
		return "devcontainer"
	}
	return "project"
}

type pageData struct {
	Sources     []projectTab
	Entities    []entity.Entity
	MCPStatuses map[string]*MCPStatus
}

var tmplFuncs = template.FuncMap{
	"kindIcon":    kindIcon,
	"formatAge":   formatAge,
	"formatMem":   formatMem,
	"statusClass": statusClass,
	"entityLevel": func(e entity.Entity) string { return sourceLevel(e.Source, e.Scope.Global) },
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	all, err := s.allEntities(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Build set of project keys that have at least one entity.
	populated := make(map[string]bool)
	for _, e := range all {
		populated[e.Scope.Project] = true // "" for global entities
	}

	// Group sources by project root, preserving discovery order.
	type tabEntry struct {
		label      string
		levelsSeen map[string]bool
		levels     []string // ordered, deduplicated
	}
	tabMap := map[string]*tabEntry{}
	tabOrder := []string{}

	for _, src := range s.sources {
		key := src.Scope().Project // "" for global
		if !populated[key] {
			continue
		}
		if tabMap[key] == nil {
			label := "~/.claude"
			if key != "" {
				if name := filepath.Base(key); name != "." && name != "/" {
					label = name
				} else {
					label = key
				}
			}
			tabMap[key] = &tabEntry{label: label, levelsSeen: map[string]bool{}}
			tabOrder = append(tabOrder, key)
		}
		lv := sourceLevel(src.ID(), src.Scope().Global)
		if !tabMap[key].levelsSeen[lv] {
			tabMap[key].levelsSeen[lv] = true
			tabMap[key].levels = append(tabMap[key].levels, lv)
		}
	}

	tabs := make([]projectTab, 0, len(tabOrder))
	for _, key := range tabOrder {
		te := tabMap[key]
		tabs = append(tabs, projectTab{Key: key, Label: te.label, Levels: te.levels})
	}

	statuses := s.resolveMCPStatuses(r.Context(), all)
	tmpl := template.Must(template.New("index").Funcs(tmplFuncs).Parse(indexHTML))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl.Execute(w, pageData{Sources: tabs, Entities: all, MCPStatuses: statuses})
}

// handleEntityPreview returns the preview pane HTML fragment (HTMX target).
func (s *Server) handleEntityPreview(w http.ResponseWriter, r *http.Request) {
	rawID := r.URL.Query().Get("id")
	if rawID == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	all, _ := s.allEntities(r.Context())
	for _, e := range all {
		if e.ID != rawID {
			continue
		}
		for _, src := range s.sources {
			if src.ID() != e.Source {
				continue
			}
			content, err := src.Read(r.Context(), e.Kind, e.Name)
			if err != nil {
				// For kinds stored in settings.json, Read returns ErrNotFound.
				// Show metadata only.
				content = nil
			}
			var mcpStatus *MCPStatus
			if e.Kind == entity.KindMCPServer {
				all2, _ := s.allEntities(r.Context())
				statuses := s.resolveMCPStatuses(r.Context(), all2)
				mcpStatus = statuses[e.ID]
			}
			tmpl := template.Must(template.New("preview").Funcs(tmplFuncs).Parse(previewHTML))
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			tmpl.Execute(w, map[string]any{
				"Entity":    e,
				"Content":   string(content),
				"MCPStatus": mcpStatus,
			})
			return
		}
	}
	http.NotFound(w, r)
}

func (s *Server) handleEntityWrite(w http.ResponseWriter, r *http.Request) {
	rawID := r.URL.Query().Get("id")
	if rawID == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	all, _ := s.allEntities(r.Context())
	for _, e := range all {
		if e.ID != rawID {
			continue
		}
		for _, src := range s.sources {
			if src.ID() != e.Source {
				continue
			}
			if err := src.Write(r.Context(), e.Kind, e.Name, body); err != nil {
				if errors.Is(err, source.ErrReadOnly) {
					http.Error(w, "this source is read-only", http.StatusForbidden)
					return
				}
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			s.invalidateEntityCache()
			return
		}
	}
	http.NotFound(w, r)
}

// --- helpers ---

const entityCacheTTL = 30 * time.Second

type entityCacheEntry struct {
	mu      sync.RWMutex
	entries []entity.Entity
	fetchAt time.Time
}

func (s *Server) invalidateEntityCache() {
	s.entityCache.mu.Lock()
	s.entityCache.fetchAt = time.Time{}
	s.entityCache.mu.Unlock()
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	s.invalidateEntityCache()
	w.WriteHeader(http.StatusNoContent)
}

type containerInfoJSON struct {
	ID          string `json:"id"`
	ShortID     string `json:"shortId"`
	Name        string `json:"name"`
	ProjectRoot string `json:"projectRoot"`
	State       string `json:"state"`
}

func toContainerInfo(mc docker.ManagedContainer) containerInfoJSON {
	short := mc.ID
	if len(short) > 12 {
		short = short[:12]
	}
	return containerInfoJSON{
		ID:          mc.ID,
		ShortID:     short,
		Name:        mc.Name,
		ProjectRoot: mc.ProjectRoot,
		State:       mc.State,
	}
}

func (s *Server) handleContainers(w http.ResponseWriter, r *http.Request) {
	if s.docker == nil {
		writeJSON(w, []containerInfoJSON{})
		return
	}
	ctrs, err := s.docker.ListManaged(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]containerInfoJSON, len(ctrs))
	for i, c := range ctrs {
		out[i] = toContainerInfo(c)
	}
	writeJSON(w, out)
}

func (s *Server) handleContainerStart(w http.ResponseWriter, r *http.Request) {
	if s.docker == nil {
		http.Error(w, "docker not available", http.StatusServiceUnavailable)
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	if err := s.docker.StartContainer(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleContainerStop(w http.ResponseWriter, r *http.Request) {
	if s.docker == nil {
		http.Error(w, "docker not available", http.StatusServiceUnavailable)
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	if err := s.docker.StopContainer(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleContainerLogsStream streams Docker container logs as Server-Sent Events.
// Events: "log" (one line), "state" (ContainerState JSON), "done" (stream ended).
func (s *Server) handleContainerLogsStream(w http.ResponseWriter, r *http.Request) {
	if s.docker == nil {
		http.Error(w, "docker not available", http.StatusServiceUnavailable)
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")

	ctx := r.Context()
	logCh := s.docker.StreamLogs(ctx, id)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case line, ok := <-logCh:
			if !ok {
				fmt.Fprintf(w, "event: done\ndata: \n\n")
				flusher.Flush()
				return
			}
			fmt.Fprintf(w, "event: log\ndata: %s\n\n", sseEscape(line))
			flusher.Flush()
		case <-ticker.C:
			state, err := s.docker.InspectContainer(ctx, id)
			if err != nil {
				continue
			}
			data, _ := json.Marshal(state)
			fmt.Fprintf(w, "event: state\ndata: %s\n\n", data)
			flusher.Flush()
		}
	}
}

// sseEscape makes a string safe to embed in an SSE data field.
func sseEscape(s string) string {
	s = strings.TrimRight(s, "\r\n")
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

// allEntities returns all entities from all sources, using a short-lived cache
// to avoid spinning up Docker sidecars on every HTTP request. Sources are
// fetched in parallel so latency = slowest single source, not their sum.
func (s *Server) allEntities(ctx context.Context) ([]entity.Entity, error) {
	s.entityCache.mu.RLock()
	if len(s.entityCache.entries) > 0 && time.Since(s.entityCache.fetchAt) < entityCacheTTL {
		out := s.entityCache.entries
		s.entityCache.mu.RUnlock()
		return out, nil
	}
	s.entityCache.mu.RUnlock()

	// Fetch all sources concurrently.
	type result struct {
		entities []entity.Entity
		err      error
	}
	results := make([]result, len(s.sources))
	var wg sync.WaitGroup
	for i, src := range s.sources {
		wg.Add(1)
		go func(i int, src source.Source) {
			defer wg.Done()
			ents, err := src.Entities(ctx)
			results[i] = result{ents, err}
		}(i, src)
	}
	wg.Wait()

	var all []entity.Entity
	for _, r := range results {
		if r.err == nil {
			all = append(all, r.entities...)
		}
	}

	s.entityCache.mu.Lock()
	s.entityCache.entries = all
	s.entityCache.fetchAt = time.Now()
	s.entityCache.mu.Unlock()

	return all, nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// sourceLabel turns a source ID into a short human-readable label.
// projectRoot is the Scope.Project value (empty for global sources).
func sourceLabel(id string, global bool, projectRoot string) string {
	// id format: "host:<absPath>" or "vol:<name>" or "ctr:<id>"
	parts := strings.SplitN(id, ":", 2)
	if len(parts) < 2 {
		return id
	}
	kind, path := parts[0], parts[1]
	switch kind {
	case "host":
		if global {
			return "~/.claude"
		}
		// show the parent dir name (repo name) for project scopes
		parent := filepath.Dir(path) // .claude/ -> repo root
		name := filepath.Base(parent)
		if name == "." || name == "/" {
			return path
		}
		return name
	case "vol":
		if projectRoot != "" {
			if name := filepath.Base(projectRoot); name != "." && name != "/" {
				return name
			}
		}
		// volume name is a long hash; truncate so it fits in the tab bar
		if len(path) > 16 {
			return "vol:" + path[:16] + "…"
		}
		return "vol:" + path
	case "ctr":
		if projectRoot != "" {
			if name := filepath.Base(projectRoot); name != "." && name != "/" {
				return name
			}
		}
		short := path
		if len(short) > 12 {
			short = short[:12]
		}
		return fmt.Sprintf("ctr:%s", short)
	default:
		return id
	}
}

// kindIcon returns a short prefix icon for each entity kind.
func kindIcon(k entity.Kind) string {
	switch k {
	case entity.KindMCPServer:
		return "⬡"
	case entity.KindCommand:
		return "/"
	case entity.KindAgent:
		return "◈"
	case entity.KindSkill:
		return "◆"
	case entity.KindMemory:
		return "◎"
	case entity.KindHook:
		return "⚡"
	case entity.KindClaudeMD:
		return "≡"
	default:
		return "·"
	}
}

// ── services handlers ─────────────────────────────────────────────────────────

type instanceView struct {
	Name     string
	Endpoint string
	Online   bool
	CanBoot  bool // has a bootstrapper configured
	Processes []compose.ProcessState
}

func (s *Server) buildInstanceViews(ctx context.Context) []instanceView {
	views := make([]instanceView, 0, len(s.compose))
	for _, c := range s.compose {
		online := c.Ping(ctx)
		iv := instanceView{
			Name:     c.Name(),
			Endpoint: c.Endpoint(),
			Online:   online,
			CanBoot:  s.booters[c.Name()] != nil,
		}
		if iv.Online {
			procs, err := c.Processes(ctx)
			if err == nil {
				iv.Processes = procs
			}
		}
		views = append(views, iv)
	}
	return views
}

func (s *Server) handleServicesPartial(w http.ResponseWriter, r *http.Request) {
	views := s.buildInstanceViews(r.Context())
	tmpl := template.Must(template.New("svc").Funcs(tmplFuncs).Parse(servicesHTML))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl.Execute(w, views)
}

func (s *Server) handleProcessStart(w http.ResponseWriter, r *http.Request) {
	s.processAction(w, r, func(c *compose.Client, proc string) error {
		return c.Start(r.Context(), proc)
	})
}

func (s *Server) handleProcessStop(w http.ResponseWriter, r *http.Request) {
	s.processAction(w, r, func(c *compose.Client, proc string) error {
		return c.Stop(r.Context(), proc)
	})
}

func (s *Server) handleProcessRestart(w http.ResponseWriter, r *http.Request) {
	s.processAction(w, r, func(c *compose.Client, proc string) error {
		return c.Restart(r.Context(), proc)
	})
}

func (s *Server) processAction(w http.ResponseWriter, r *http.Request, fn func(*compose.Client, string) error) {
	instName := r.URL.Query().Get("instance")
	procName := r.URL.Query().Get("process")
	if !validProcessName(instName) || !validProcessName(procName) {
		http.Error(w, "invalid instance or process name", http.StatusBadRequest)
		return
	}
	for _, c := range s.compose {
		if c.Name() == instName {
			if err := fn(c, procName); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			// Return refreshed services partial.
			s.handleServicesPartial(w, r)
			return
		}
	}
	http.Error(w, "instance not found", http.StatusNotFound)
}

func (s *Server) handleComposeStart(w http.ResponseWriter, r *http.Request) {
	instName := r.URL.Query().Get("instance")
	b, ok := s.booters[instName]
	if !ok {
		http.Error(w, "no bootstrapper for "+instName, http.StatusNotFound)
		return
	}
	if err := b.Start(r.Context()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.handleServicesPartial(w, r)
}

type logsData struct {
	Instance string
	Process  string
	Lines    []compose.LogLine
	Err      string
}

func (s *Server) handleProcessLogs(w http.ResponseWriter, r *http.Request) {
	instName := r.URL.Query().Get("instance")
	procName := r.URL.Query().Get("process")
	if !validProcessName(instName) || !validProcessName(procName) {
		http.Error(w, "invalid instance or process name", http.StatusBadRequest)
		return
	}
	data := logsData{Instance: instName, Process: procName}
	for _, c := range s.compose {
		if c.Name() == instName {
			lines, err := c.Logs(r.Context(), procName, 100)
			if err != nil {
				data.Err = err.Error()
			} else {
				data.Lines = lines
			}
			break
		}
	}
	tmpl := template.Must(template.New("logs").Funcs(tmplFuncs).Parse(logsHTML))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl.Execute(w, data)
}

// formatAge converts a duration in seconds to a compact human string.
func formatAge(secs float64) string {
	if secs < 60 {
		return fmt.Sprintf("%ds", int(secs))
	}
	if secs < 3600 {
		return fmt.Sprintf("%dm%ds", int(secs)/60, int(secs)%60)
	}
	h := int(secs) / 3600
	m := (int(secs) % 3600) / 60
	return fmt.Sprintf("%dh%dm", h, m)
}

// formatMem formats bytes as a compact string.
func formatMem(b int64) string {
	if b == 0 {
		return "—"
	}
	switch {
	case b < 1024:
		return fmt.Sprintf("%dB", b)
	case b < 1024*1024:
		return fmt.Sprintf("%dK", b/1024)
	default:
		return fmt.Sprintf("%dM", b/(1024*1024))
	}
}

// statusClass maps a process status string to a CSS class.
func statusClass(status string) string {
	switch strings.ToLower(status) {
	case "running":
		return "running"
	case "stopped", "completed":
		return "stopped"
	case "error", "dead":
		return "error"
	case "starting", "launched", "restarting":
		return "starting"
	case "disabled", "skipped":
		return "disabled"
	default:
		return "unknown"
	}
}


