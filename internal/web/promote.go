package web

import (
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"sort"
	"strings"

	"github.com/eike-hass/infra-mngmt/internal/entity"
	"github.com/eike-hass/infra-mngmt/internal/source"
)

// promoteTarget is one row in the target picker. Status flags are pre-resolved
// server-side so the user sees existence / read-only state before clicking
// rather than discovering it from a 4xx response.
type promoteTarget struct {
	SourceID    string
	Label       string
	Scope       string // "global" / "project"
	Level       string // "global" / "project" / "devcontainer"
	ProjectPath string // for project-scoped targets, the repo root
	Exists      bool   // (kind, name) already present at this target
	ReadOnly    bool   // WriteFiles will reject (e.g. Docker volumes)
}

// promoteGroup is a tier-grouped slice of targets shown as a section in the
// picker (e.g. "Projects" containing every host:/<project> source).
type promoteGroup struct {
	Title   string
	Level   string
	Targets []promoteTarget
}

type promotePickerData struct {
	Entity entity.Entity
	Groups []promoteGroup
	// AnyTargets is false when no candidate sources exist; the template uses it
	// to render an empty state instead of a list.
	AnyTargets bool
	// SuggestedName is what the rename input is pre-filled with (the entity's
	// own name, so the default is "no rename").
	SuggestedName string
}

type promoteResult struct {
	OK       bool
	Conflict bool
	ReadOnly bool
	Message  string
	From     string
	To       string
	NewName  string // echoed into the conflict-confirm form so rename survives
}

// handlePromotePicker returns the modal HTML fragment listing every other
// source as a candidate target, grouped by tier and pre-annotated with
// existence/read-only flags.
func (s *Server) handlePromotePicker(w http.ResponseWriter, r *http.Request) {
	rawID := r.URL.Query().Get("id")
	if rawID == "" {
		http.Error(w, "missing id", http.StatusBadRequest)
		return
	}
	all, _ := s.allEntities(r.Context())
	var ent entity.Entity
	found := false
	for _, e := range all {
		if e.ID == rawID {
			ent = e
			found = true
			break
		}
	}
	if !found {
		http.NotFound(w, r)
		return
	}

	// Pre-build an existence index from the cached entity list so we don't
	// fan out one Has() call per source. For Docker volume sources Has() is
	// expensive (spins up a throwaway sidecar container), and for N sources
	// the picker would block for N×~1s before rendering. The cache is
	// already populated from the page load, so the lookup is an in-memory
	// map hit.
	type existsKey struct {
		sourceID string
		kind     entity.Kind
		name     string
	}
	existsAt := map[existsKey]bool{}
	for _, e := range all {
		existsAt[existsKey{e.Source, e.Kind, e.Name}] = true
	}

	groupsByLevel := map[string]*promoteGroup{}
	levelOrder := []string{"global", "project", "devcontainer"}
	titles := map[string]string{
		"global":       "Global",
		"project":      "Projects",
		"devcontainer": "Devcontainers",
	}

	for _, src := range s.sources {
		if src.ID() == ent.Source {
			continue
		}
		level := sourceLevel(src.ID(), src.Scope().Global)
		t := promoteTarget{
			SourceID:    src.ID(),
			Label:       sourceLabel(src.ID(), src.Scope().Global, src.Scope().Project),
			Scope:       src.Scope().Label(),
			Level:       level,
			ProjectPath: src.Scope().Project,
			Exists:      existsAt[existsKey{src.ID(), ent.Kind, ent.Name}],
			ReadOnly:    !src.Writable(),
		}
		if groupsByLevel[level] == nil {
			groupsByLevel[level] = &promoteGroup{Title: titles[level], Level: level}
		}
		groupsByLevel[level].Targets = append(groupsByLevel[level].Targets, t)
	}

	// Sort each group: writable first, then by label ascending so the most
	// useful destinations float to the top.
	for _, g := range groupsByLevel {
		sort.SliceStable(g.Targets, func(i, j int) bool {
			a, b := g.Targets[i], g.Targets[j]
			if a.ReadOnly != b.ReadOnly {
				return !a.ReadOnly
			}
			return a.Label < b.Label
		})
	}

	data := promotePickerData{
		Entity:        ent,
		SuggestedName: ent.Name,
	}
	for _, lv := range levelOrder {
		if g, ok := groupsByLevel[lv]; ok {
			data.Groups = append(data.Groups, *g)
			data.AnyTargets = data.AnyTargets || len(g.Targets) > 0
		}
	}

	tmpl := template.Must(template.New("promote-picker").Funcs(tmplFuncs).Parse(promotePickerHTML))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = tmpl.Execute(w, data)
}

// handlePromote copies an entity to a target source. Optional ?name=<newName>
// renames it during the copy (validated against validName). On conflict
// (existing entity at target without ?overwrite=true) returns 409 + a confirm
// fragment.
func (s *Server) handlePromote(w http.ResponseWriter, r *http.Request) {
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")
	overwrite := r.URL.Query().Get("overwrite") == "true"
	// FormValue checks both URL query and form body — picker uses hx-include
	// (form body); the conflict-confirm button rides the rename in the URL.
	newName := strings.TrimSpace(r.FormValue("name"))
	if from == "" || to == "" {
		http.Error(w, "missing from/to", http.StatusBadRequest)
		return
	}

	all, err := s.allEntities(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var ent entity.Entity
	srcFound := false
	for _, e := range all {
		if e.ID == from {
			ent = e
			srcFound = true
			break
		}
	}
	if !srcFound {
		http.NotFound(w, r)
		return
	}

	targetName := ent.Name
	if newName != "" && newName != ent.Name {
		if !validName.MatchString(newName) {
			s.writePromoteResult(w, http.StatusBadRequest, promoteResult{
				Message: fmt.Sprintf("invalid name %q (must match %s)", newName, validName.String()),
			})
			return
		}
		targetName = newName
	}

	var srcSource, dstSource source.Source
	for _, src := range s.sources {
		if src.ID() == ent.Source {
			srcSource = src
		}
		if src.ID() == to {
			dstSource = src
		}
	}
	if srcSource == nil || dstSource == nil {
		http.NotFound(w, r)
		return
	}
	if srcSource.ID() == dstSource.ID() && targetName == ent.Name {
		s.writePromoteResult(w, http.StatusBadRequest, promoteResult{
			Message: "source and target are the same and no rename was requested",
		})
		return
	}

	dstLabel := sourceLabel(dstSource.ID(), dstSource.Scope().Global, dstSource.Scope().Project)

	if !overwrite {
		exists, herr := dstSource.Has(r.Context(), ent.Kind, targetName)
		if herr != nil {
			http.Error(w, "check target: "+herr.Error(), http.StatusInternalServerError)
			return
		}
		if exists {
			s.writePromoteResult(w, http.StatusConflict, promoteResult{
				Conflict: true,
				Message:  fmt.Sprintf("%s already has %s/%s", dstLabel, ent.Kind, targetName),
				From:     from,
				To:       to,
				NewName:  targetName,
			})
			return
		}
	}

	files, err := srcSource.ReadFiles(r.Context(), ent.Kind, ent.Name)
	if err != nil {
		http.Error(w, "read source: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := dstSource.WriteFiles(r.Context(), ent.Kind, targetName, files); err != nil {
		if errors.Is(err, source.ErrReadOnly) {
			s.writePromoteResult(w, http.StatusForbidden, promoteResult{
				ReadOnly: true,
				Message:  fmt.Sprintf("%s is read-only — Docker volume writes are not yet implemented", dstLabel),
			})
			return
		}
		http.Error(w, "write target: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.invalidateEntityCache()
	msg := fmt.Sprintf("copied %s/%s to %s", ent.Kind, targetName, dstLabel)
	if targetName != ent.Name {
		msg = fmt.Sprintf("copied %s/%s to %s as %s", ent.Kind, ent.Name, dstLabel, targetName)
	}
	s.writePromoteResult(w, http.StatusOK, promoteResult{
		OK:      true,
		Message: msg,
	})
}

func (s *Server) writePromoteResult(w http.ResponseWriter, status int, r promoteResult) {
	tmpl := template.Must(template.New("promote-result").Funcs(tmplFuncs).Parse(promoteResultHTML))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = tmpl.Execute(w, r)
}

// handlePromoteClear empties the promote slot. Dismiss buttons in the picker
// and result fragments swap this in to reset the slot back to empty.
func (s *Server) handlePromoteClear(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
}
