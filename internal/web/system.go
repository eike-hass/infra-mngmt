package web

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/eike-hass/infra-mngmt/internal/system"
)

// The System tab: WSL2 + Windows disk footprint and the docker/podman
// `system df` breakdown, plus prune/remove cleanup. Reads fan out to the
// host (wsl.exe/powershell.exe interop, the Docker SDK, the podman CLI) and
// degrade per-source — a failed read renders a card-level error/unreachable
// state, never a failed view. Compaction (returning freed space to drive C:)
// is intentionally not wired here; the disk card's compact controls are
// disabled pending that later phase.

const (
	sysReadTimeout = 20 * time.Second // disk/df reads — should be quick
	// sysOpTimeout bounds destructive ops. Pruning a large build cache or image
	// set is disk-bound and can run far longer than a read; 20s was timing these
	// out (→ 500 → htmx silently no-ops → "nothing happens"). Generous cap, not
	// a target.
	sysOpTimeout    = 5 * time.Minute
	dockerGlyph     = "⬢"
	podmanGlyph     = "⬡"
	sysTmpl         = "templates/system.html.tmpl"
	sysDockerTarget = "#sys-docker"
	sysPodmanTarget = "#sys-podman"
)

// sysEngineCard is the data bound by the shared engine-card template. The
// per-engine glyph color is driven by the .Slug CSS class (not an inline
// style) to avoid html/template's CSS sanitizer mangling oklch()/var() values.
type sysEngineCard struct {
	DF    system.EngineDF
	Glyph string
	Slug  string // "docker" | "podman" — drives action URLs, the wrapper id, and color class
}

// sysConfirmLine is one labeled fact in the confirm modal. Yellow drives a CSS
// class (not an inline color) so html/template's CSS sanitizer doesn't mangle
// the oklch()/var() token value.
type sysConfirmLine struct {
	Label  string
	Value  string
	Yellow bool // render the value in the reclaim/warning accent color
}

// sysConfirmData drives the destructive-action confirm modal.
type sysConfirmData struct {
	Title        string
	Command      string
	Lines        []sysConfirmLine
	Warning      string
	WarnClass    string // severity class: "sys-warn-danger" | "sys-warn" | "sys-warn-info"
	ConfirmLabel string
	ActionURL    string // POST endpoint the "proceed" button hits
	CardTarget   string // element id (with #) the action result swaps into
}

// pruneWarning returns the confirm-modal warning text and its severity class
// for a given prune target. Volumes (and whole-engine prune, which includes
// --volumes) are the data-loss hazards → danger; build cache and the safe prune
// are loss-free → info; images/containers are in between → caution.
func pruneWarning(key string) (text, class string) {
	switch key {
	case "volumes":
		return "Volumes hold data you can't recover — databases, uploads, app state. A volume counts as \"unused\" whenever the app that owns it isn't running, so deleting it is permanent and easy to regret. Check the volume names before pruning.", "sys-warn-danger"
	case "__all__":
		return "Includes --volumes: permanently deletes data held by any volume not currently in use (databases, uploads), on top of every unused image and stopped container. Irreversible.", "sys-warn-danger"
	case "buildcache":
		return "Only removes build cache — no images, containers, or volumes are touched. Your next build is slower; nothing is lost.", "sys-warn-info"
	case "__safe__":
		return "Safe cleanup: removes only dangling (untagged) images and build cache. No tagged images, containers, or volumes are touched — nothing is lost beyond a slower next build.", "sys-warn-info"
	case "containers":
		return "Removes stopped containers. Anything written inside a container's own filesystem (not a mounted volume) is lost; mounted volumes are untouched.", ""
	case "images":
		return "Removes images not used by any container, including tagged ones. Nothing running is affected — you'll just re-pull or rebuild them next time.", ""
	default:
		return "This permanently deletes the selected objects. It can't be undone.", ""
	}
}

// handleSystem renders the System tab shell: three self-loading section
// wrappers. The wrappers persist (innerHTML swaps) so the disk card can keep
// listening for the sys-refresh event a prune emits.
func (s *Server) handleSystem(w http.ResponseWriter, r *http.Request) {
	tmpl := parseTemplate("sys", sysTmpl)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, "system-shell", nil); err != nil {
		log.Printf("system shell: render: %v", err)
	}
}

// handleSystemDisk renders the wsl2 disk card.
func (s *Server) handleSystemDisk(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), sysReadTimeout)
	defer cancel()
	disk := system.ReadDisk(ctx)
	tagEngineHosts(&disk)
	s.renderSysSection(w, "sys-disk-card", disk)
}

func (s *Server) handleSystemDocker(w http.ResponseWriter, r *http.Request) {
	s.renderSysSection(w, "sys-engine-card", s.dockerCard(r.Context()))
}

func (s *Server) handleSystemPodman(w http.ResponseWriter, r *http.Request) {
	s.renderSysSection(w, "sys-engine-card", s.podmanCard(r.Context()))
}

func (s *Server) renderSysSection(w http.ResponseWriter, block string, data any) {
	tmpl := parseTemplate("sys", sysTmpl)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, block, data); err != nil {
		log.Printf("system %s: render: %v", block, err)
	}
}

// dockerCard reads docker's df into card data. A nil/erroring client yields an
// unreachable card (Available=false) rather than an error.
func (s *Server) dockerCard(parent context.Context) sysEngineCard {
	ctx, cancel := context.WithTimeout(parent, sysReadTimeout)
	defer cancel()
	df := system.EngineDF{Engine: system.EngineDocker}
	if s.docker != nil {
		if got, err := s.docker.DiskUsageDF(ctx); err == nil {
			df = got
		} else {
			df.Err = err.Error()
		}
	}
	return sysEngineCard{DF: df, Glyph: dockerGlyph, Slug: "docker"}
}

func (s *Server) podmanCard(parent context.Context) sysEngineCard {
	ctx, cancel := context.WithTimeout(parent, sysReadTimeout)
	defer cancel()
	df := system.EngineDF{Engine: system.EnginePodman}
	if s.podman != nil {
		df = s.podman.DiskUsageDF(ctx)
	}
	return sysEngineCard{DF: df, Glyph: podmanGlyph, Slug: "podman"}
}

// handleSystemConfirm renders the prune/remove confirm modal into #blast-slot.
// Params: engine=docker|podman, op=prune|remove, key=<category>, id=<item>.
func (s *Server) handleSystemConfirm(w http.ResponseWriter, r *http.Request) {
	engine := r.URL.Query().Get("engine")
	op := r.URL.Query().Get("op")
	key := r.URL.Query().Get("key")
	id := r.URL.Query().Get("id")
	if !validEngine(engine) {
		http.Error(w, "unknown engine", http.StatusBadRequest)
		return
	}
	df := s.engineDF(r.Context(), engine)
	target := engineTarget(engine)

	var data sysConfirmData
	switch op {
	case "prune":
		var ok bool
		if key == "__safe__" {
			data, ok = buildSafeConfirm(engine, df, target)
		} else {
			data, ok = buildPruneConfirm(engine, key, df, target)
		}
		if !ok {
			http.Error(w, "nothing to prune", http.StatusBadRequest)
			return
		}
	case "remove":
		var ok bool
		data, ok = buildRemoveConfirm(engine, key, id, df, target)
		if !ok {
			http.Error(w, "item not found or in use", http.StatusBadRequest)
			return
		}
	default:
		http.Error(w, "unknown op", http.StatusBadRequest)
		return
	}

	tmpl := parseTemplate("sys", sysTmpl)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, "sys-confirm", data); err != nil {
		log.Printf("system confirm: render: %v", err)
	}
}

// buildPruneConfirm assembles the modal facts for a category (or whole-engine
// "__all__") prune. Returns ok=false when nothing is reclaimable.
func buildPruneConfirm(engine, key string, df system.EngineDF, target string) (sysConfirmData, bool) {
	var rows []system.DFRow
	if key == "__all__" {
		rows = df.Rows
	} else if r, ok := df.Row(key); ok {
		rows = []system.DFRow{r}
	}
	var reclaim int64
	var removed int
	for _, r := range rows {
		reclaim += r.Reclaimable()
		for _, it := range r.Items {
			if !it.InUse {
				removed++
			}
		}
	}
	if reclaim <= 0 {
		return sysConfirmData{}, false
	}
	cmd := fmt.Sprintf("%s system prune -a --volumes", engine)
	label := ""
	if key != "__all__" && len(rows) == 1 {
		cmd = rows[0].Prune
		label = " · " + strings.ToLower(rows[0].Label)
	}
	warn, cls := pruneWarning(key)
	return sysConfirmData{
		Title:   fmt.Sprintf("prune %s%s", engine, label),
		Command: cmd,
		Lines: []sysConfirmLine{
			{Label: "will remove", Value: fmt.Sprintf("%d unused object%s", removed, plural(removed))},
			{Label: "reclaims", Value: fmtSize(reclaim), Yellow: true},
			{Label: "frees inside", Value: distroOr(df.Distro, "the engine's distro")},
		},
		Warning:      warn,
		WarnClass:    cls,
		ConfirmLabel: "prune",
		ActionURL:    fmt.Sprintf("/system/prune?engine=%s&key=%s", engine, key),
		CardTarget:   target,
	}, true
}

// buildSafeConfirm assembles the modal for a "safe prune" — dangling images
// plus (for docker) build cache. Returns ok=false when nothing safe is
// reclaimable.
func buildSafeConfirm(engine string, df system.EngineDF, target string) (sysConfirmData, bool) {
	if !df.HasSafeReclaim() {
		return sysConfirmData{}, false
	}
	dangling := df.DanglingImages()
	_, hasBuildCache := df.Row("buildcache")
	cmd := engine + " image prune"
	removes := fmt.Sprintf("%d dangling image%s", dangling, plural(dangling))
	if hasBuildCache {
		cmd += "  ·  " + engine + " builder prune -a"
		removes += " + build cache"
	}
	warn, cls := pruneWarning("__safe__")
	lines := []sysConfirmLine{{Label: "removes", Value: removes}}
	// A conservative reclaim estimate (dangling unique layers + build cache),
	// capped so it never exceeds prune-all; the toast reports the exact figure.
	if est := df.SafeReclaim(); est > 0 {
		lines = append(lines, sysConfirmLine{Label: "reclaims (est.)", Value: "~" + fmtSize(est), Yellow: true})
	}
	lines = append(lines, sysConfirmLine{Label: "frees inside", Value: distroOr(df.Distro, "the engine's distro")})
	return sysConfirmData{
		Title:        fmt.Sprintf("safe prune %s", engine),
		Command:      cmd,
		Lines:        lines,
		Warning:      warn,
		WarnClass:    cls,
		ConfirmLabel: "safe prune",
		ActionURL:    fmt.Sprintf("/system/prune?engine=%s&key=__safe__", engine),
		CardTarget:   target,
	}, true
}

// buildRemoveConfirm assembles the modal facts for removing one not-in-use
// object. Returns ok=false if the item is missing or in use.
func buildRemoveConfirm(engine, key, id string, df system.EngineDF, target string) (sysConfirmData, bool) {
	row, ok := df.Row(key)
	if !ok {
		return sysConfirmData{}, false
	}
	var item *system.DFItem
	for i := range row.Items {
		if row.Items[i].Key() == id {
			item = &row.Items[i]
			break
		}
	}
	if item == nil || item.InUse {
		return sysConfirmData{}, false
	}
	warn, cls := "This permanently deletes the object. It can't be undone.", ""
	if key == "volumes" {
		warn, cls = "This volume may hold data you can't recover (a database, uploads, app state). Removing it is permanent — make sure you know what it contains.", "sys-warn-danger"
	}
	cmd := removeCommand(engine, key, id)
	return sysConfirmData{
		Title:   fmt.Sprintf("remove %s", id),
		Command: cmd,
		Lines: []sysConfirmLine{
			{Label: "frees", Value: fmtSize(item.Size), Yellow: true},
			{Label: "frees inside", Value: distroOr(df.Distro, "the engine's distro")},
		},
		Warning:      warn,
		WarnClass:    cls,
		ConfirmLabel: "remove",
		ActionURL:    fmt.Sprintf("/system/remove?engine=%s&key=%s&id=%s", engine, key, id),
		CardTarget:   target,
	}, true
}

// handleSystemPrune performs a prune and re-renders the engine card.
func (s *Server) handleSystemPrune(w http.ResponseWriter, r *http.Request) {
	engine := r.URL.Query().Get("engine")
	key := r.URL.Query().Get("key")
	if !validEngine(engine) {
		http.Error(w, "unknown engine", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), sysOpTimeout)
	defer cancel()

	var freed uint64
	var err error
	switch {
	case engine == "docker" && s.docker != nil:
		switch key {
		case "__safe__":
			freed, err = s.docker.SafePrune(ctx)
		case "__all__":
			freed, err = s.docker.PruneAll(ctx)
		default:
			freed, err = s.docker.PruneCategory(ctx, key)
		}
	case engine == "podman" && s.podman != nil:
		switch key {
		case "__safe__":
			err = s.podman.SafePrune(ctx)
		case "__all__":
			err = s.podman.PruneAll(ctx)
		default:
			err = s.podman.PruneCategory(ctx, key)
		}
	default:
		err = fmt.Errorf("%s engine unavailable", engine)
	}
	if err != nil {
		http.Error(w, fmt.Sprintf("prune failed: %v", err), http.StatusInternalServerError)
		return
	}

	title := fmt.Sprintf("pruned %s", engine)
	if key == "__safe__" {
		title = fmt.Sprintf("safe-pruned %s", engine)
	}
	body := "freed space inside the distro — compact it to return space to drive C:"
	if freed > 0 {
		body = fmt.Sprintf("freed %s inside the distro — compact it to return space to drive C:", fmtSize(int64(freed)))
	}
	s.setSysTrigger(w, title, body)
	s.renderEngineCard(w, r, engine)
}

// handleSystemRemove removes a single object and re-renders the engine card.
func (s *Server) handleSystemRemove(w http.ResponseWriter, r *http.Request) {
	engine := r.URL.Query().Get("engine")
	key := r.URL.Query().Get("key")
	id := r.URL.Query().Get("id")
	if !validEngine(engine) || key == "" || id == "" {
		http.Error(w, "missing engine/key/id", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), sysOpTimeout)
	defer cancel()

	var err error
	if engine == "docker" && s.docker != nil {
		err = s.docker.RemoveItem(ctx, key, id)
	} else if engine == "podman" && s.podman != nil {
		err = s.podman.RemoveItem(ctx, key, id)
	} else {
		err = fmt.Errorf("%s engine unavailable", engine)
	}
	if err != nil {
		http.Error(w, fmt.Sprintf("remove failed: %v", err), http.StatusInternalServerError)
		return
	}
	s.setSysTrigger(w, fmt.Sprintf("removed %s", id), "freed space inside the distro")
	s.renderEngineCard(w, r, engine)
}

func (s *Server) renderEngineCard(w http.ResponseWriter, r *http.Request, engine string) {
	if engine == "podman" {
		s.renderSysSection(w, "sys-engine-card", s.podmanCard(r.Context()))
		return
	}
	s.renderSysSection(w, "sys-engine-card", s.dockerCard(r.Context()))
}

// setSysTrigger asks the client to toast and to refresh the disk card (a prune
// frees space inside the distro, growing its compact gap). Both ride one
// HX-Trigger header.
func (s *Server) setSysTrigger(w http.ResponseWriter, title, body string) {
	payload := map[string]any{
		"sys-toast":   map[string]string{"title": title, "body": body, "kind": "ok"},
		"sys-refresh": true,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return
	}
	w.Header().Set("HX-Trigger", string(b))
}

// engineDF reads the current df for an engine (used by the confirm modal).
func (s *Server) engineDF(ctx context.Context, engine string) system.EngineDF {
	if engine == "podman" {
		return s.podmanCard(ctx).DF
	}
	return s.dockerCard(ctx).DF
}

// ── helpers ──────────────────────────────────────────────────────────────

func validEngine(e string) bool { return e == "docker" || e == "podman" }

func engineTarget(engine string) string {
	if engine == "podman" {
		return sysPodmanTarget
	}
	return sysDockerTarget
}

func removeCommand(engine, key, id string) string {
	switch key {
	case "images":
		return fmt.Sprintf("%s rmi %s", engine, id)
	case "containers":
		return fmt.Sprintf("%s rm %s", engine, id)
	case "volumes":
		return fmt.Sprintf("%s volume rm %s", engine, id)
	default:
		return fmt.Sprintf("%s rm %s", engine, id)
	}
}

func distroOr(distro, fallback string) string {
	if distro == "" {
		return fallback
	}
	return distro
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// tagEngineHosts marks which distro hosts which engine, by name heuristic
// (podman runs in a "podman"-named distro; docker conventionally in the default
// distro). Cosmetic — drives only the per-distro sub-label.
func tagEngineHosts(d *system.DiskInfo) {
	for i := range d.Distros {
		name := strings.ToLower(d.Distros[i].Name)
		switch {
		case strings.Contains(name, "podman"):
			d.Distros[i].Engine = "podman"
		case d.Distros[i].Default:
			d.Distros[i].Engine = "docker"
		}
	}
}

// fmtSize formats a byte count the way the design's fmtSize does: GB with one
// decimal under 100 (rounded at/above 100), else MB/KB/B. Non-positive → "0".
func fmtSize(b int64) string {
	const gb = 1024 * 1024 * 1024
	const mb = 1024 * 1024
	switch {
	case b <= 0:
		return "0"
	case b >= gb:
		v := float64(b) / gb
		if v >= 100 {
			return fmt.Sprintf("%.0f GB", v)
		}
		return fmt.Sprintf("%.1f GB", v)
	case b >= mb:
		return fmt.Sprintf("%d MB", b/mb)
	case b >= 1024:
		return fmt.Sprintf("%d KB", b/1024)
	default:
		return fmt.Sprintf("%d B", b)
	}
}

// pctOf returns part as a whole-number percentage of total (0 when total ≤ 0).
func pctOf(part, total int64) int {
	if total <= 0 {
		return 0
	}
	return int(float64(part) / float64(total) * 100)
}

// shortDigest trims a "sha256:" prefix and truncates to a docker-style short id
// for display (the full id stays the action target). Distinct from containers.go's
// shortID, which doesn't strip the algorithm prefix.
func shortDigest(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
