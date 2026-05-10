package web

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
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

// handleVersion exposes the running binary's build identity. Public so that
// deploy scripts can confirm a restart picked up the new code without auth:
//
//	curl -s http://<host>:<port>/api/version | jq .build_epoch
//
// Compare with: `<binary> version` to verify they match.
func (s *Server) handleVersion(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, s.buildInfo)
}

func (s *Server) handleSources(w http.ResponseWriter, r *http.Request) {
	type sourceInfo struct {
		ID    string `json:"id"`
		Scope string `json:"scope"`
	}
	srcs := s.allSources()
	infos := make([]sourceInfo, 0, len(srcs))
	for _, src := range srcs {
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
		for _, src := range s.allSources() {
			if src.ID() != e.Source {
				continue
			}
			data, err := src.Read(r.Context(), e.Kind, e.Name)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write(data)
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

// claudeIconSVG is the official Claude mark from https://claude.ai/favicon.svg
// (Anthropic). Embedded inline so the icon survives offline / firewalled
// environments and renders without a network round-trip.
const claudeIconSVG = `<svg viewBox="0 0 248 248" width="12" height="12" aria-hidden="true" style="flex-shrink:0;display:block"><path fill="#D97757" d="M52.4285 162.873L98.7844 136.879L99.5485 134.602L98.7844 133.334H96.4921L88.7237 132.862L62.2346 132.153L39.3113 131.207L17.0249 130.026L11.4214 128.844L6.2 121.873L6.7094 118.447L11.4214 115.257L18.171 115.847L33.0711 116.911L55.485 118.447L71.6586 119.392L95.728 121.873H99.5485L100.058 120.337L98.7844 119.392L97.7656 118.447L74.5877 102.732L49.4995 86.1905L36.3823 76.62L29.3779 71.7757L25.8121 67.2858L24.2839 57.3608L30.6515 50.2716L39.3113 50.8623L41.4763 51.4531L50.2636 58.1879L68.9842 72.7209L93.4357 90.6804L97.0015 93.6343L98.4374 92.6652L98.6571 91.9801L97.0015 89.2625L83.757 65.2772L69.621 40.8192L63.2534 30.6579L61.5978 24.632C60.9565 22.1032 60.579 20.0111 60.579 17.4246L67.8381 7.49965L71.9133 6.19995L81.7193 7.49965L85.7946 11.0443L91.9074 24.9865L101.714 46.8451L116.996 76.62L121.453 85.4816L123.873 93.6343L124.764 96.1155H126.292V94.6976L127.566 77.9197L129.858 57.3608L132.15 30.8942L132.915 23.4505L136.608 14.4708L143.994 9.62643L149.725 12.344L154.437 19.0788L153.8 23.4505L150.998 41.6463L145.522 70.1215L141.957 89.2625H143.994L146.414 86.7813L156.093 74.0206L172.266 53.698L179.398 45.6635L187.803 36.802L193.152 32.5484H203.34L210.726 43.6549L207.415 55.1159L196.972 68.3492L188.312 79.5739L175.896 96.2095L168.191 109.585L168.882 110.689L170.738 110.53L198.755 104.504L213.91 101.787L231.994 98.7149L240.144 102.496L241.036 106.395L237.852 114.311L218.495 119.037L195.826 123.645L162.07 131.592L161.696 131.893L162.137 132.547L177.36 133.925L183.855 134.279H199.774L229.447 136.524L237.215 141.605L241.8 147.867L241.036 152.711L229.065 158.737L213.019 154.956L175.45 145.977L162.587 142.787H160.805V143.85L171.502 154.366L191.242 172.089L215.82 195.011L217.094 200.682L213.91 205.172L210.599 204.699L188.949 188.394L180.544 181.069L161.696 165.118H160.422V166.772L164.752 173.152L187.803 207.771L188.949 218.405L187.294 221.832L181.308 223.959L174.813 222.777L161.187 203.754L147.305 182.486L136.098 163.345L134.745 164.2L128.075 235.42L125.019 239.082L117.887 241.8L111.902 237.31L108.718 229.984L111.902 215.452L115.722 196.547L118.779 181.541L121.58 162.873L123.291 156.636L123.14 156.219L121.773 156.449L107.699 175.752L86.304 204.699L69.3663 222.777L65.291 224.431L58.2867 220.768L58.9235 214.27L62.8713 208.48L86.304 178.705L100.44 160.155L109.551 149.507L109.462 147.967L108.959 147.924L46.6977 188.512L35.6182 189.93L30.7788 185.44L31.4156 178.115L33.7079 175.752L52.4285 162.873Z"/></svg>`

// opencodeIconSVG is the official OpenCode mark, derived from
// packages/console/app/src/asset/brand/opencode-logo-dark-square.svg in the
// upstream repo. The original art uses fixed brand colors; this monochrome
// version drives both paths from currentColor so the badge tint flows
// through.
// viewBox is padded out to 300×300 (vs the upstream 240×300) so the mark
// renders into a square box that matches the Claude icon's footprint —
// otherwise the 4:5 aspect ratio makes the OpenCode badge visibly narrower
// than the Claude one. The original art is centered horizontally inside.
const opencodeIconSVG = `<svg viewBox="0 0 300 300" width="12" height="12" fill="currentColor" aria-hidden="true" style="flex-shrink:0;display:block"><g transform="translate(30,0)"><path fill-rule="evenodd" d="M180 60H60V240H180V60ZM240 300H0V0H240V300Z"/><path d="M180 240H60V120H180V240Z"/></g></svg>`

// sourceTool derives which coding-assistant the source backs ("claude" /
// "opencode") so the UI can disambiguate when one project ships both
// .claude/ and .opencode/. Returns "" when the source ID doesn't carry a
// recognized tool marker (e.g. unlabeled container sources).
func sourceTool(id string) string {
	parts := strings.SplitN(id, ":", 2)
	if len(parts) < 2 {
		return ""
	}
	kind, rest := parts[0], parts[1]
	switch kind {
	case "host":
		// host:/abs/path/to/.claude  OR  host:/abs/path/to/.opencode
		base := filepath.Base(rest)
		if name := strings.TrimPrefix(base, "."); name == "claude" || name == "opencode" {
			return name
		}
	case "vol":
		// Devcontainer volume names typically encode the tool, e.g.
		// "claude-code-config-…", "opencode-config-…". Match a leading
		// token before the first '-'.
		if i := strings.IndexByte(rest, '-'); i > 0 {
			head := rest[:i]
			if head == "claude" || head == "opencode" {
				return head
			}
		}
	}
	// "ctr:<id>" sources don't encode the tool — leave it to caller to
	// fall back (typically "claude" since that's the original assumption).
	return ""
}

type pageData struct {
	Sources     []projectTab
	Entities    []entity.Entity
	MCPStatuses map[string]*MCPStatus
	Build       BuildInfo // for the version chip in the header
}

// kindGroup is one section of entities sharing a kind, used for the grouped
// "all" view in the entity list.
type kindGroup struct {
	Kind     entity.Kind
	Label    string
	Entities []entity.Entity
}

// kindOrder is the canonical display order for the entity list.
var kindOrder = []entity.Kind{
	entity.KindMCPServer, entity.KindCommand, entity.KindAgent, entity.KindSkill,
	entity.KindHook, entity.KindMemory, entity.KindClaudeMD,
}

var kindLabels = map[entity.Kind]string{
	entity.KindMCPServer: "MCP Servers",
	entity.KindCommand:   "Commands",
	entity.KindAgent:     "Agents",
	entity.KindSkill:     "Skills",
	entity.KindHook:      "Hooks",
	entity.KindMemory:    "Memory",
	entity.KindClaudeMD:  "CLAUDE.md",
}

func groupEntitiesByKind(ents []entity.Entity) []kindGroup {
	byKind := map[entity.Kind][]entity.Entity{}
	for _, e := range ents {
		byKind[e.Kind] = append(byKind[e.Kind], e)
	}
	out := make([]kindGroup, 0, len(kindOrder))
	for _, k := range kindOrder {
		if items, ok := byKind[k]; ok {
			out = append(out, kindGroup{Kind: k, Label: kindLabels[k], Entities: items})
		}
	}
	return out
}

// cpuBarWidth maps a CPU% to a bar fill width 0-100. Bars cap at 25% CPU = full
// (since most processes idle low and we want subtle rises to be visible).
func cpuBarWidth(cpu float64) int {
	w := int(cpu * 4)
	if w > 100 {
		w = 100
	}
	if w < 0 {
		w = 0
	}
	return w
}

// cpuBarClass returns the bar color class based on usage level.
func cpuBarClass(cpu float64) string {
	switch {
	case cpu > 50:
		return "cpu-high"
	case cpu > 20:
		return "cpu-mid"
	default:
		return "cpu-low"
	}
}

// memBarWidth maps memory bytes to a bar fill width 0-100, capped at 512MB = full.
func memBarWidth(b int64) int {
	w := int(float64(b) / float64(1024*1024*512) * 100)
	if w > 100 {
		w = 100
	}
	if w < 0 {
		w = 0
	}
	return w
}

// runningCount returns the number of running processes in a slice.
func runningCount(procs []compose.ProcessState) int {
	n := 0
	for _, p := range procs {
		if p.IsRunning {
			n++
		}
	}
	return n
}

// activeBridgeCount returns the number of bridges whose state pill maps to
// "running" (i.e. fully active). Used for the bridges-panel header summary.
func activeBridgeCount(bs []bridgeView) int {
	n := 0
	for _, b := range bs {
		if b.StateClass == "running" {
			n++
		}
	}
	return n
}

// runningContainerCount returns the number of declared containers reported as
// running by Docker. Used for the containers-panel header summary.
func runningContainerCount(cs []containerView) int {
	n := 0
	for _, c := range cs {
		if c.StateClass == "running" {
			n++
		}
	}
	return n
}

var tmplFuncs = template.FuncMap{
	"kindIcon":              kindIcon,
	"formatMem":             formatMem,
	"statusClass":           statusClass,
	"healthClass":           healthClass,
	"cpuBarWidth":           cpuBarWidth,
	"cpuBarClass":           cpuBarClass,
	"memBarWidth":           memBarWidth,
	"runningCount":          runningCount,
	"activeBridgeCount":     activeBridgeCount,
	"runningContainerCount": runningContainerCount,
	"canStop":               canStop,
	"canStart":              canStart,
	"isInternalProcess":     IsInternalProcess,
	"pctClass":              pctClass,
	"pctWidth":              pctWidth,
	"formatTokensPerSec":    formatTokensPerSec,
	"formatCount":           formatCount,
	"slotProgress":          slotProgress,
	"add":                   addInts,
	"statusHelp":            statusHelp,
	"exitCodeHelp":          exitCodeHelp,
	"entityLevel":           func(e entity.Entity) string { return sourceLevel(e.Source, e.Scope.Global) },
	"entityLevelShort": func(e entity.Entity) string {
		switch sourceLevel(e.Source, e.Scope.Global) {
		case "global":
			return "glb"
		case "project":
			return "prj"
		case "devcontainer":
			return "ctr"
		}
		return "?"
	},
	"levelShort": func(level string) string {
		switch level {
		case "global":
			return "glb"
		case "project":
			return "prj"
		case "devcontainer":
			return "ctr"
		}
		return level
	},
	"entityTool": func(e entity.Entity) string { return sourceTool(e.Source) },
	"entityToolShort": func(e entity.Entity) string {
		switch sourceTool(e.Source) {
		case "claude":
			return "cld"
		case "opencode":
			return "opc"
		}
		return ""
	},
	// toolIcon returns the inline SVG mark for the named tool, or empty
	// HTML when the tool is unknown. Returning template.HTML bypasses
	// auto-escaping so the SVG renders inline.
	"toolIcon": func(name string) template.HTML {
		switch name {
		case "claude":
			return template.HTML(claudeIconSVG)
		case "opencode":
			return template.HTML(opencodeIconSVG)
		}
		return ""
	},
	"groupEntitiesByKind": groupEntitiesByKind,
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

	for _, src := range s.allSources() {
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
	tmpl := template.Must(template.New("index").Funcs(tmplFuncs).Parse(entityListInnerHTML))
	tmpl = template.Must(tmpl.Parse(indexHTML))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = tmpl.Execute(w, pageData{Sources: tabs, Entities: all, MCPStatuses: statuses, Build: s.buildInfo})
}

// handleEntityListPartial returns just the entity-list inner HTML for in-place
// refresh by the client (avoids a full page reload).
func (s *Server) handleEntityListPartial(w http.ResponseWriter, r *http.Request) {
	all, err := s.allEntities(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	statuses := s.resolveMCPStatuses(r.Context(), all)
	tmpl := template.Must(template.New("partial").Funcs(tmplFuncs).Parse(entityListInnerHTML))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = tmpl.ExecuteTemplate(w, "entity-list-inner", pageData{Entities: all, MCPStatuses: statuses})
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
		for _, src := range s.allSources() {
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
			_ = tmpl.Execute(w, map[string]any{
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
		for _, src := range s.allSources() {
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

// rescanResult is the JSON shape returned by /api/sources/rescan. It includes
// the IDs *discovered* by the rediscovery callback (before merge), the IDs
// *added* to the active list (those not already present), and the resulting
// total. The detail is verbose on purpose — without it, a user who clicks
// rescan and sees `added: 0` has no way to tell whether their new project
// wasn't discovered, was discovered but already known, or was filtered out
// (e.g. devcontainer with no .claude mount).
type rescanResult struct {
	Discovered []string `json:"discovered"` // every ID returned by the discovery callback
	Added      []string `json:"added"`      // subset of Discovered that wasn't already in s.sources
	Total      int      `json:"total"`      // len(s.sources) after merge
}

// handleSourcesRescan re-runs source discovery and merges the result into the
// active source list by ID. New sources (e.g. a project that didn't have a
// .claude/ at startup, or a devcontainer that came up later) become visible
// without a process restart. Existing sources are preserved so cached entity
// IDs stay valid.
//
// Returns 200 with a rescanResult on success and 503 when no rediscovery
// callback was configured (only happens in tests).
func (s *Server) handleSourcesRescan(w http.ResponseWriter, _ *http.Request) {
	if s.rediscover == nil {
		http.Error(w, "rescan not configured", http.StatusServiceUnavailable)
		return
	}
	fresh := s.rediscover()
	out := rescanResult{
		Discovered: make([]string, 0, len(fresh)),
		Added:      []string{},
	}
	for _, src := range fresh {
		out.Discovered = append(out.Discovered, src.ID())
	}

	s.sourcesMu.Lock()
	have := map[string]bool{}
	for _, src := range s.sources {
		have[src.ID()] = true
	}
	merged := append([]source.Source(nil), s.sources...)
	for _, src := range fresh {
		if have[src.ID()] {
			continue
		}
		merged = append(merged, src)
		have[src.ID()] = true
		out.Added = append(out.Added, src.ID())
		log.Printf("rescan: added source %s (%s)", src.ID(), src.Scope().Label())
	}
	s.sources = merged
	out.Total = len(merged)
	s.sourcesMu.Unlock()

	log.Printf("rescan: discovered=%d added=%d total=%d", len(out.Discovered), len(out.Added), out.Total)
	if len(out.Added) > 0 {
		s.invalidateEntityCache()
	}
	writeJSON(w, out)
}

type containerInfoJSON struct {
	ID              string `json:"id"`
	ShortID         string `json:"shortId"`
	Name            string `json:"name"`
	ProjectRoot     string `json:"projectRoot"`
	WorkspaceFolder string `json:"workspaceFolder,omitempty"`
	State           string `json:"state"`
}

func toContainerInfo(mc docker.ManagedContainer) containerInfoJSON {
	short := mc.ID
	if len(short) > 12 {
		short = short[:12]
	}
	return containerInfoJSON{
		ID:              mc.ID,
		ShortID:         short,
		Name:            mc.Name,
		ProjectRoot:     mc.ProjectRoot,
		WorkspaceFolder: mc.WorkspaceFolder,
		State:           mc.State,
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

// buildAttachedContainerURI returns the canonical attach URI consumed by
// `code --folder-uri`. The hex segment is a hex-encoded JSON envelope —
// passing raw bytes silently mis-parses inside the Dev Containers extension.
// workspace defaults to "/" when undetected.
func buildAttachedContainerURI(containerRef, workspace string) string {
	payload, _ := json.Marshal(struct {
		ContainerName string `json:"containerName"`
	}{ContainerName: containerRef})
	if workspace == "" {
		workspace = "/"
	}
	return "vscode-remote://attached-container+" + hex.EncodeToString(payload) + workspace
}

// buildDevContainerURI returns the URI that triggers VS Code's Dev Containers
// extension to bring up (create-or-start-or-attach) the dev container for a
// project, running full lifecycle and port forwarding — no "Reopen in
// Container" prompt required. hostPath must equal the raw value VS Code
// stored when creating the container (the devcontainer.local_folder label),
// otherwise the hash mismatch causes VS Code to spawn a new container.
func buildDevContainerURI(hostPath, workspace string) string {
	payload, _ := json.Marshal(struct {
		HostPath string `json:"hostPath"`
	}{HostPath: hostPath})
	if workspace == "" {
		workspace = "/"
	}
	return "vscode-remote://dev-container+" + hex.EncodeToString(payload) + workspace
}

// findVSCodeCLI locates a usable VS Code CLI binary. process-compose typically
// inherits a stripped PATH from its supervisor, so a bare exec.LookPath("code")
// fails even when the user's interactive shell finds it fine. The probe order:
//
//  1. $INFRAMNGMT_VSCODE_CLI override (escape hatch)
//  2. exec.LookPath("code")
//  3. /c/Users/*/AppData/Local/Programs/Microsoft VS Code/bin/code (Windows
//     per-user install via WSL automount; also tries /mnt/c/...)
//  4. /c/Program Files/Microsoft VS Code/bin/code (Windows system install)
//  5. ~/.vscode-server/bin/<commit>/bin/remote-cli/code (LAST resort —
//     this CLI silently no-ops when there's no active VS Code session
//     listening on VSCODE_IPC_HOOK_CLI, which is exactly the scenario
//     where a user clicks "open VS Code" from a browser).
//
// findVSCodeCLI is cached after the first successful resolution. The probe
// touches /c/Users/*/AppData/Local/Programs/... which on WSL2's 9p mount can
// take seconds — every click was paying that cost. sync.OnceValues caches
// the result for the process lifetime; a redeploy resets it.
var findVSCodeCLI = sync.OnceValues(findVSCodeCLIImpl)

func findVSCodeCLIImpl() (string, error) {
	start := time.Now()
	defer func() { log.Printf("findVSCodeCLI: probe took %s", time.Since(start)) }()
	if p := os.Getenv("INFRAMNGMT_VSCODE_CLI"); p != "" {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
		return "", fmt.Errorf("INFRAMNGMT_VSCODE_CLI=%q is not a regular file", p)
	}
	if p, err := exec.LookPath("code"); err == nil {
		return p, nil
	}
	var candidates []string
	for _, base := range []string{"/c/Users", "/mnt/c/Users"} {
		matches, _ := filepath.Glob(filepath.Join(base, "*", "AppData", "Local", "Programs", "Microsoft VS Code", "bin", "code"))
		candidates = append(candidates, matches...)
	}
	candidates = append(candidates,
		"/c/Program Files/Microsoft VS Code/bin/code",
		"/mnt/c/Program Files/Microsoft VS Code/bin/code",
	)
	if home, err := os.UserHomeDir(); err == nil {
		matches, _ := filepath.Glob(filepath.Join(home, ".vscode-server", "bin", "*", "bin", "remote-cli", "code"))
		sort.Slice(matches, func(i, j int) bool {
			si, _ := os.Stat(matches[i])
			sj, _ := os.Stat(matches[j])
			if si == nil || sj == nil {
				return false
			}
			return si.ModTime().After(sj.ModTime())
		})
		candidates = append(candidates, matches...)
	}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c, nil
		}
	}
	return "", fmt.Errorf("VS Code CLI not found in PATH, /c/Users/*/AppData/Local/Programs/Microsoft VS Code/, or ~/.vscode-server/")
}

// findNode locates a node binary, falling back across NVM and standard
// install paths because process-compose typically inherits a stripped PATH.
func findNode() (string, error) {
	if p, err := exec.LookPath("node"); err == nil {
		return p, nil
	}
	var candidates []string
	if home, err := os.UserHomeDir(); err == nil {
		matches, _ := filepath.Glob(filepath.Join(home, ".nvm", "versions", "node", "*", "bin", "node"))
		sort.Strings(matches) // version dirs sort lexicographically; v9 < v10 unfortunately, but for our user it's v21+ so fine
		for i := len(matches) - 1; i >= 0; i-- {
			candidates = append(candidates, matches[i])
		}
	}
	candidates = append(candidates, "/usr/local/bin/node", "/usr/bin/node")
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c, nil
		}
	}
	return "", fmt.Errorf("node not found in PATH, ~/.nvm/versions/node/, or /usr/")
}

// findDevcontainerCLI returns the command (and any prefixed args, e.g. node
// before the bundled JS) to invoke the devcontainers CLI. Probe order:
//
//  1. $INFRAMNGMT_DEVCONTAINER_CLI (space-separated, full command line)
//  2. `devcontainer` on PATH (npm install -g @devcontainers/cli)
//  3. node + Dev Containers extension's bundled CLI under
//     /c/Users/*/.vscode/extensions/ms-vscode-remote.remote-containers-*/
//     and the WSL-server equivalent. Highest version wins (lex sort).
var findDevcontainerCLI = sync.OnceValues(findDevcontainerCLIImpl)

func findDevcontainerCLIImpl() ([]string, error) {
	if p := os.Getenv("INFRAMNGMT_DEVCONTAINER_CLI"); p != "" {
		return strings.Fields(p), nil
	}
	if p, err := exec.LookPath("devcontainer"); err == nil {
		return []string{p}, nil
	}
	var cliCandidates []string
	for _, base := range []string{"/c/Users", "/mnt/c/Users"} {
		matches, _ := filepath.Glob(filepath.Join(base, "*", ".vscode", "extensions", "ms-vscode-remote.remote-containers-*", "dist", "spec-node", "devContainersSpecCLI.js"))
		cliCandidates = append(cliCandidates, matches...)
	}
	if home, err := os.UserHomeDir(); err == nil {
		matches, _ := filepath.Glob(filepath.Join(home, ".vscode-server", "extensions", "ms-vscode-remote.remote-containers-*", "dist", "spec-node", "devContainersSpecCLI.js"))
		cliCandidates = append(cliCandidates, matches...)
	}
	sort.Strings(cliCandidates)
	for i := len(cliCandidates) - 1; i >= 0; i-- {
		c := cliCandidates[i]
		st, err := os.Stat(c)
		if err != nil || st.IsDir() {
			continue
		}
		nodePath, err := findNode()
		if err != nil {
			return nil, fmt.Errorf("found bundled CLI at %s but %w", c, err)
		}
		return []string{nodePath, c}, nil
	}
	return nil, fmt.Errorf("devcontainer CLI not found in PATH or VS Code extension dirs (probed /c/Users/*/.vscode/extensions/ms-vscode-remote.remote-containers-* and ~/.vscode-server/extensions/)")
}

// handleContainerDevcontainerUp invokes `devcontainer up --workspace-folder
// <projectRoot>` so the bring-up runs all devcontainer.json lifecycle scripts
// (onCreate/postCreate/postStart/postAttach), unlike a bare `docker start`
// which is daemon-level only. Async — returns 202 once spawned; full output
// is logged server-side.
func (s *Server) handleContainerDevcontainerUp(w http.ResponseWriter, r *http.Request) {
	if s.docker == nil {
		http.Error(w, "docker not available", http.StatusServiceUnavailable)
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	mcs, err := s.docker.ListManaged(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var found *docker.ManagedContainer
	for i := range mcs {
		if mcs[i].ID == id {
			found = &mcs[i]
			break
		}
	}
	if found == nil {
		http.Error(w, "container not found", http.StatusNotFound)
		return
	}
	if found.ProjectRoot == "" {
		http.Error(w, "container has no project root recorded; cannot resolve --workspace-folder for devcontainer CLI", http.StatusUnprocessableEntity)
		return
	}
	cli, err := findDevcontainerCLI()
	if err != nil {
		http.Error(w, "devcontainer CLI not found — `npm install -g @devcontainers/cli` on the WSL host, or set INFRAMNGMT_DEVCONTAINER_CLI=\"node /path/to/devContainersSpecCLI.js\". Probed: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	args := append([]string{}, cli[1:]...)
	args = append(args, "up", "--workspace-folder", found.ProjectRoot)
	cmd := exec.CommandContext(r.Context(), cli[0], args...)
	log.Printf("devcontainer up: workspace=%s cmd=%v", found.ProjectRoot, cmd.Args)
	out, runErr := cmd.CombinedOutput()
	trimmed := strings.TrimSpace(string(out))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if runErr != nil {
		log.Printf("devcontainer up failed (workspace=%s): %v\n%s", found.ProjectRoot, runErr, trimmed)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprintf(w, "devcontainer up exited %v\n\n%s", runErr, trimmed)
		return
	}
	log.Printf("devcontainer up done (workspace=%s)\n%s", found.ProjectRoot, trimmed)
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, trimmed)
}

// handleContainerOpenVSCode launches VS Code on the host via the `code` CLI,
// bypassing the browser's external-protocol prompt. Requires `code` on PATH
// for the infra-mngmt process (typically present once VS Code's WSL
// integration has been installed).
func (s *Server) handleContainerOpenVSCode(w http.ResponseWriter, r *http.Request) {
	if s.docker == nil {
		http.Error(w, "docker not available", http.StatusServiceUnavailable)
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	mcs, err := s.docker.ListManaged(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var found *docker.ManagedContainer
	for i := range mcs {
		if mcs[i].ID == id {
			found = &mcs[i]
			break
		}
	}
	if found == nil {
		http.Error(w, "container not found", http.StatusNotFound)
		return
	}
	// running + healthy → attach to the existing container.
	// stopped/unhealthy → open the project folder so VS Code's Dev Containers
	// extension prompts "Reopen in Container" (its supported start path).
	codePath, err := findVSCodeCLI()
	if err != nil {
		http.Error(w, "VS Code CLI not found — set INFRAMNGMT_VSCODE_CLI=/path/to/code in infra-mngmt's process env, or open VS Code with Remote-WSL once on this distro to install ~/.vscode-server. Probed: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	var cmdArgs []string
	if found.State == "running" {
		ref := found.Name
		if ref == "" {
			ref = found.ID
		}
		uri := buildAttachedContainerURI(ref, found.WorkspaceFolder)
		cmdArgs = []string{"--new-window", "--folder-uri", uri}
		log.Printf("VS Code launch (attach %s): %s %v", ref, codePath, cmdArgs)
	} else {
		// Stopped devcontainer → use the dev-container+<hex> URI form so VS
		// Code's Dev Containers extension directly brings it up (full
		// lifecycle, port forwarding, attach) without a "Reopen in Container"
		// prompt. The hex segment is a hex-encoded JSON envelope; hostPath
		// must match exactly what VS Code stored when creating the container,
		// which is the raw devcontainer.local_folder label (UNC on Windows).
		var rawLocal string
		for _, k := range []string{docker.LabelDevcontainerLocalFolder, docker.LabelDevcontainerLocalFolderLegacy} {
			if v := found.Labels[k]; v != "" {
				rawLocal = v
				break
			}
		}
		if rawLocal != "" {
			uri := buildDevContainerURI(rawLocal, found.WorkspaceFolder)
			cmdArgs = []string{"--new-window", "--folder-uri", uri}
			log.Printf("VS Code launch (devcontainer %s): %s %v", rawLocal, codePath, cmdArgs)
		} else if found.ProjectRoot != "" {
			// Fallback: no localFolder label (rare). Open the project folder
			// and let VS Code prompt "Reopen in Container".
			cmdArgs = []string{"--new-window", found.ProjectRoot}
			log.Printf("VS Code launch (open folder %s; no local_folder label): %s %v", found.ProjectRoot, codePath, cmdArgs)
		} else {
			http.Error(w, "container is stopped and has no project root or local_folder label — cannot determine workspace for VS Code", http.StatusUnprocessableEntity)
			return
		}
	}
	cmd := exec.Command(codePath, cmdArgs...)
	stderrPipe, _ := cmd.StderrPipe()
	stdoutPipe, _ := cmd.StdoutPipe()
	startedAt := time.Now()
	if err := cmd.Start(); err != nil {
		http.Error(w, "failed to launch VS Code: "+err.Error(), http.StatusInternalServerError)
		return
	}
	go func() {
		var stdout, stderr []byte
		if stdoutPipe != nil {
			stdout, _ = io.ReadAll(stdoutPipe)
		}
		if stderrPipe != nil {
			stderr, _ = io.ReadAll(stderrPipe)
		}
		err := cmd.Wait()
		elapsed := time.Since(startedAt)
		if err != nil {
			log.Printf("VS Code launch (%s) failed after %s: %v\nstdout=%q\nstderr=%q", codePath, elapsed, err, string(stdout), string(stderr))
		} else if len(stdout) > 0 || len(stderr) > 0 {
			log.Printf("VS Code launch (%s) exit 0 after %s\nstdout=%q\nstderr=%q", codePath, elapsed, string(stdout), string(stderr))
		} else {
			log.Printf("VS Code launch (%s) exit 0 after %s (no output)", codePath, elapsed)
		}
	}()
	w.WriteHeader(http.StatusNoContent)
}

// handleContainerEventsStream streams Docker engine events for a container as SSE.
// Events: "state" (initial ContainerState JSON), "docker_event" (per-event JSON),
// "done" (stream ended). Replaces the older log-tailing endpoint — devcontainer
// entrypoints rarely produce useful stdout, but engine events (start, exec_create,
// health_status:healthy, die) reflect real lifecycle activity.
func (s *Server) handleContainerEventsStream(w http.ResponseWriter, r *http.Request) {
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

	// Send the current container state once so the client doesn't have to
	// wait for the next event to know if it's running/healthy.
	if state, err := s.docker.InspectContainer(ctx, id); err == nil {
		if data, jerr := json.Marshal(state); jerr == nil {
			_, _ = fmt.Fprintf(w, "event: state\ndata: %s\n\n", data)
			flusher.Flush()
		}
	}

	evCh := s.docker.StreamEvents(ctx, id)
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-evCh:
			if !ok {
				_, _ = fmt.Fprintf(w, "event: done\ndata: \n\n")
				flusher.Flush()
				return
			}
			data, _ := json.Marshal(ev)
			_, _ = fmt.Fprintf(w, "event: docker_event\ndata: %s\n\n", data)
			flusher.Flush()

			// Health-status transitions don't update overall State.Status, so
			// re-inspect after each event so the client can refresh badge state.
			if state, err := s.docker.InspectContainer(ctx, id); err == nil {
				if sd, jerr := json.Marshal(state); jerr == nil {
					_, _ = fmt.Fprintf(w, "event: state\ndata: %s\n\n", sd)
					flusher.Flush()
				}
			}
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
	srcs := s.allSources()
	results := make([]result, len(srcs))
	var wg sync.WaitGroup
	for i, src := range srcs {
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
	_ = json.NewEncoder(w).Encode(v)
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
		return "$"
	case entity.KindAgent:
		return "◉"
	case entity.KindSkill:
		return "✦"
	case entity.KindMemory:
		return "▤"
	case entity.KindHook:
		return "↪"
	case entity.KindClaudeMD:
		return "#"
	default:
		return "·"
	}
}

// ── services handlers ─────────────────────────────────────────────────────────

type instanceView struct {
	Name      string
	Endpoint  string
	Online    bool
	CanBoot   bool // has a bootstrapper configured
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

// IsInternalProcess reports whether a process row is an infra-mngmt
// implementation detail (currently: `bridge-*` in the `bridges` namespace).
// The template uses this to tag those rows with class="proc-internal", which
// CSS hides by default. The "show internals" toggle is purely client-side
// (body class + localStorage) for instant feedback — no server round-trip.
func IsInternalProcess(p compose.ProcessState) bool {
	return p.Namespace == "bridges" && strings.HasPrefix(p.Name, "bridge-")
}

func (s *Server) handleServicesPartial(w http.ResponseWriter, r *http.Request) {
	containers := s.rebuildContainerViews(r.Context())
	data := servicesPageData{
		Instances:  s.buildInstanceViews(r.Context()),
		Bridges:    s.rebuildBridgeViews(r.Context()),
		Containers: containers,
		Vaults:     buildVaultCardViews(containers),
		Docker:     s.dockerHealth(r.Context()),
	}
	tmpl := template.Must(template.New("svc").Funcs(tmplFuncs).Parse(servicesHTML))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = tmpl.Execute(w, data)
}

// buildVaultCardViews extracts vault-specific cards from the container
// view list. Reusing the polled container snapshot avoids a second Docker
// round-trip per services-partial fetch.
func buildVaultCardViews(containers []containerView) []vaultCardView {
	var out []vaultCardView
	for _, c := range containers {
		if c.Kind != "mcp-fs" {
			continue
		}
		out = append(out, vaultCardView{
			Name:        c.Name,
			Description: c.Description,
			State:       c.State,
			StateClass:  c.StateClass,
		})
	}
	return out
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
	verb := strings.TrimPrefix(r.URL.Path, "/process/")
	for _, c := range s.compose {
		if c.Name() == instName {
			log.Printf("compose: %s %s/%s", verb, instName, procName)
			if err := fn(c, procName); err != nil {
				log.Printf("compose: %s %s/%s failed: %v", verb, instName, procName, err)
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

// handleComposeReload re-reads the named instance's compose YAML and
// reconciles the running process set. Useful after editing the YAML to drop
// processes without a full systemd restart. See compose.Client.Reload for
// version-specific caveats.
func (s *Server) handleComposeReload(w http.ResponseWriter, r *http.Request) {
	instName := r.URL.Query().Get("instance")
	if !validProcessName(instName) {
		http.Error(w, "invalid instance", http.StatusBadRequest)
		return
	}
	for _, c := range s.compose {
		if c.Name() == instName {
			log.Printf("compose: reload request from=%s instance=%s", r.RemoteAddr, instName)
			if err := c.Reload(r.Context()); err != nil {
				log.Printf("compose: reload %s failed: %v", instName, err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			s.handleServicesPartial(w, r)
			return
		}
	}
	http.Error(w, "instance not found", http.StatusNotFound)
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
	matched := false
	for _, c := range s.compose {
		if c.Name() == instName {
			matched = true
			lines, err := c.Logs(r.Context(), procName, 200)
			if err != nil {
				log.Printf("logs: %s/%s: %v", instName, procName, err)
				data.Err = err.Error()
			} else {
				data.Lines = lines
				if len(lines) == 0 {
					log.Printf("logs: %s/%s: 0 lines returned", instName, procName)
				}
			}
			break
		}
	}
	if !matched {
		data.Err = "instance " + instName + " not configured"
	}
	tmpl := template.Must(template.New("logs").Funcs(tmplFuncs).Parse(logsHTML))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = tmpl.Execute(w, data)
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

// healthClass maps a process-compose readiness value to a CSS pill class.
// Values come from src/types/process.go: "Ready", "Not Ready", "-".
func healthClass(health string) string {
	switch health {
	case "Ready":
		return "ready"
	case "Not Ready":
		return "not-ready"
	default:
		return "unknown"
	}
}

// canStop reports whether the stop button should render. True for any state
// where there's something to halt (or a restart loop to break) — including
// Error/Restarting, which the bare `IsRunning` check misses.
func canStop(status string) bool {
	switch strings.ToLower(status) {
	case "running", "foreground", "launched", "launching",
		"pending", "restarting", "scheduled", "terminating", "error":
		return true
	}
	return false
}

// canStart reports whether the start button should render. True only for
// states where the process is definitely not running and not on its way up.
func canStart(status string) bool {
	switch strings.ToLower(status) {
	case "completed", "disabled", "skipped":
		return true
	case "":
		return true // process-compose hasn't reported yet — let the user try
	}
	return false
}

// statusClass maps a process-compose process status to a CSS pill class.
// Statuses come from the upstream constants in src/types/process.go: Disabled,
// Foreground, Pending, Running, Launching, Launched, Restarting, Terminating,
// Completed, Skipped, Error, Scheduled.
func statusClass(status string) string {
	switch strings.ToLower(status) {
	case "running", "foreground", "launched":
		return "running"
	case "completed":
		return "stopped"
	case "error":
		return "error"
	case "pending", "launching", "restarting", "terminating", "scheduled":
		return "starting"
	case "disabled", "skipped":
		return "disabled"
	default:
		return "unknown"
	}
}
