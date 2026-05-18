package web

import (
	"context"
	"log"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/eike-hass/infra-mngmt/config"
	"github.com/eike-hass/infra-mngmt/internal/bridge"
	"github.com/eike-hass/infra-mngmt/internal/compose"
	"github.com/eike-hass/infra-mngmt/internal/graph"
)

// bridgeView is the template-friendly representation of one bridge — either
// a standalone bridge, a composite parent (with Members populated), or a
// composite member (rendered inside its parent's row when expanded).
type bridgeView struct {
	Name        string
	Kind        string // "bridge" | "composite"
	Description string // human-friendly subtitle (composites only)
	Tier        string
	Type        string
	Listen      string // "addr:port" with sentinels resolved
	Connect     string // "addr:port"
	State       string // "active" | "drifted" | "missing" | "unknown"
	StateClass  string // CSS class: "running" | "error" | "stopped" | "unknown"
	DisplayName string // firewall display name (Windows) or empty
	// Members is populated for composite parents only — leaf bridges have nil.
	Members []bridgeView
}

// rebuildBridgeViews re-snapshots the underlying bridge state and returns
// view structs ready for the template. Composite parents are rendered as a
// single row with Members populated; their children are NOT included in the
// top-level slice. Standalone bridges (no composite_of) render as today.
//
// State sources by tier:
//   - tier=windows: netsh portproxy show + firewall rule comparison
//   - tier=wsl: process-compose state of the corresponding bridge-<name>
//     process. Active when running and (ready or no probe); drifted when
//     running but probe failed; missing otherwise.
//   - composite: rolled-up worst-of state across all members.
func (s *Server) rebuildBridgeViews(ctx context.Context) []bridgeView {
	s.bridgesMu.Lock()
	defer s.bridgesMu.Unlock()
	if len(s.bridges) == 0 {
		return nil
	}
	wslHostIP := config.WSLWindowsHostIP()
	out, err := bridge.PortproxyShow()
	var entries []bridge.PortproxyEntry
	if err == nil {
		entries = bridge.ParsePortproxyShow(out)
	}
	wslProcs := s.snapshotWSLBridgeProcesses(ctx)

	// First pass: compute leaf state + view per bridge entry. Composites are
	// handled separately in the second pass since their state rolls up.
	leafViews := make(map[string]bridgeView, len(s.bridges))
	leafState := make(map[string]bridge.State, len(s.bridges))
	for i := range s.bridges {
		bi := &s.bridges[i]
		if bi.Bridge.Kind == bridge.KindComposite {
			continue
		}
		switch bi.Bridge.Tier {
		case bridge.TierWSL:
			bi.State = wslBridgeState(&bi.Bridge, wslProcs)
		default:
			bi.State = bi.Bridge.Status(entries, wslHostIP)
		}
		leafState[bi.Bridge.Name] = bi.State
		listen := bi.Bridge.Listen.Addr
		if listen == "${wsl-host-ip}" && wslHostIP != "" {
			listen = wslHostIP
		}
		leafViews[bi.Bridge.Name] = bridgeView{
			Name:        bi.Bridge.Name,
			Kind:        string(bridge.KindBridge),
			Tier:        string(bi.Bridge.Tier),
			Type:        string(bi.Bridge.Type),
			Listen:      formatHostPort(listen, bi.Bridge.Listen.Port),
			Connect:     formatHostPort(bi.Bridge.Connect.Addr, bi.Bridge.Connect.Port),
			State:       bi.State.String(),
			StateClass:  bridgeStateCSS(bi.State),
			DisplayName: bi.Bridge.Firewall.DisplayName,
		}
	}

	// Second pass: assemble the top-level view list.
	//   - Composite parents become rows with Members populated and rolled-up state.
	//   - Standalone leaves (no composite_of) become rows.
	//   - Member leaves (composite_of != "") are folded INTO their parent and
	//     omitted from the top level.
	var out2 []bridgeView
	memberOf := make(map[string][]string)
	for i := range s.bridges {
		b := &s.bridges[i].Bridge
		if b.CompositeOf != "" {
			memberOf[b.CompositeOf] = append(memberOf[b.CompositeOf], b.Name)
		}
	}
	for i := range s.bridges {
		b := s.bridges[i].Bridge
		switch {
		case b.Kind == bridge.KindComposite:
			members := make([]bridgeView, 0, len(memberOf[b.Name]))
			states := make([]bridge.State, 0, len(memberOf[b.Name]))
			for _, mn := range memberOf[b.Name] {
				if v, ok := leafViews[mn]; ok {
					members = append(members, v)
				}
				states = append(states, leafState[mn])
			}
			rolled := rollupState(states)
			out2 = append(out2, bridgeView{
				Name:        b.Name,
				Kind:        string(bridge.KindComposite),
				Description: b.Description,
				State:       rolled.String(),
				StateClass:  bridgeStateCSS(rolled),
				Members:     members,
			})
		case b.CompositeOf != "":
			// Member: skip — already nested under its parent.
		default:
			// Standalone leaf.
			out2 = append(out2, leafViews[b.Name])
		}
	}
	sort.Slice(out2, func(i, j int) bool { return out2[i].Name < out2[j].Name })
	return out2
}

// snapshotWSLBridgeProcesses queries the WSL process-compose instance for the
// state of every bridge-* process. Returns a map keyed by process name. Empty
// (not nil) on any error so callers can range over it without nil checks.
func (s *Server) snapshotWSLBridgeProcesses(ctx context.Context) map[string]compose.ProcessState {
	out := map[string]compose.ProcessState{}
	c := s.findWSLComposeClient()
	if c == nil {
		return out
	}
	rctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	procs, err := c.Processes(rctx)
	if err != nil {
		return out
	}
	for _, p := range procs {
		if strings.HasPrefix(p.Name, "bridge-") {
			out[p.Name] = p
		}
	}
	return out
}

// wslBridgeState derives a bridge.State from a process-compose process state.
// Treats running+ready (or running with no probe) as active; running but not-
// ready as drifted; everything else as missing.
func wslBridgeState(br *bridge.Bridge, procs map[string]compose.ProcessState) bridge.State {
	p, ok := procs[bridge.BridgeProcessName(br)]
	if !ok {
		return bridge.StateMissing
	}
	if !p.IsRunning {
		return bridge.StateMissing
	}
	if p.HasHealthProbe && p.Health != "Ready" {
		return bridge.StateDrifted
	}
	return bridge.StateActive
}

func formatHostPort(addr string, port int) string {
	if addr == "" {
		return ""
	}
	return strings.TrimSpace(addr) + ":" + itoa(port)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func bridgeStateCSS(s bridge.State) string {
	switch s {
	case bridge.StateActive:
		return "running"
	case bridge.StateDegraded:
		// "rule is right but traffic doesn't flow" — distinct from drifted
		// (config wrong) and stopped (rule missing). Renders amber.
		return "warning"
	case bridge.StateDrifted:
		return "error"
	case bridge.StateMissing:
		return "stopped"
	default:
		return "unknown"
	}
}

// expandToMembers returns the leaf bridges referenced by a name. If the name
// resolves to a composite parent, returns its members; if it resolves to a
// regular bridge, returns just that one. Returns nil if the name is unknown.
// Composite parents themselves are never returned (they have no own state).
func (s *Server) expandToMembers(name string) []bridge.Bridge {
	s.bridgesMu.RLock()
	defer s.bridgesMu.RUnlock()
	target, ok := s.findBridgeLocked(name)
	if !ok {
		return nil
	}
	if target.Kind != bridge.KindComposite {
		return []bridge.Bridge{target}
	}
	var out []bridge.Bridge
	for i := range s.bridges {
		if s.bridges[i].Bridge.CompositeOf == name {
			out = append(out, s.bridges[i].Bridge)
		}
	}
	return out
}

// findBridgeLocked is the un-locked form for callers that already hold
// bridgesMu.RLock.
func (s *Server) findBridgeLocked(name string) (bridge.Bridge, bool) {
	for i := range s.bridges {
		if s.bridges[i].Bridge.Name == name {
			return s.bridges[i].Bridge, true
		}
	}
	return bridge.Bridge{}, false
}

// snapshotAllBridges returns a copy of every declared *leaf* bridge — i.e.,
// composite parents are excluded since they have no own forwarding state.
// Used by the apply/reset paths and the WSL fragment renderer.
func (s *Server) snapshotAllBridges() []bridge.Bridge {
	s.bridgesMu.RLock()
	defer s.bridgesMu.RUnlock()
	out := make([]bridge.Bridge, 0, len(s.bridges))
	for i := range s.bridges {
		if s.bridges[i].Bridge.Kind == bridge.KindComposite {
			continue
		}
		out = append(out, s.bridges[i].Bridge)
	}
	return out
}

// rollupState combines several leaf states into the effective state of a
// composite. Bad states bubble up so the parent reflects the worst child:
//
//	any Degraded         → Degraded   (rule looks right but traffic isn't)
//	any Drifted          → Drifted    (config mismatch)
//	mixed Active+Missing → Drifted    (partial up — existing convention)
//	all Active           → Active
//	all Missing          → Missing
//	all Unknown          → Unknown
func rollupState(states []bridge.State) bridge.State {
	if len(states) == 0 {
		return bridge.StateUnknown
	}
	var hasActive, hasDegraded, hasDrifted, hasMissing, hasUnknown bool
	for _, st := range states {
		switch st {
		case bridge.StateActive:
			hasActive = true
		case bridge.StateDegraded:
			hasDegraded = true
		case bridge.StateDrifted:
			hasDrifted = true
		case bridge.StateMissing:
			hasMissing = true
		case bridge.StateUnknown:
			hasUnknown = true
		}
	}
	switch {
	case hasDegraded:
		return bridge.StateDegraded
	case hasDrifted:
		return bridge.StateDrifted
	case hasActive && hasMissing:
		return bridge.StateDrifted
	case hasActive:
		return bridge.StateActive
	case hasMissing:
		return bridge.StateMissing
	case hasUnknown:
		return bridge.StateUnknown
	default:
		return bridge.StateUnknown
	}
}

// handleBridgeApply applies one bridge (or all members of a composite) by
// name. Triggers a UAC prompt on Windows when at least one Windows-tier leaf
// needs work; cheap when smart-apply determines everything is already active.
func (s *Server) handleBridgeApply(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	log.Printf("bridge: HTTP apply request name=%q from=%s", name, r.RemoteAddr)
	if !validProcessName(name) {
		http.Error(w, "invalid name", http.StatusBadRequest)
		return
	}
	members := s.expandToMembers(name)
	if len(members) == 0 {
		log.Printf("bridge: apply %q — not found", name)
		http.Error(w, "bridge not found", http.StatusNotFound)
		return
	}
	if err := s.applyBridges(members); err != nil {
		log.Printf("bridge: apply %q failed: %v", name, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.handleServicesPartial(w, r)
}

// handleBridgesApplyAll applies every Windows-tier bridge in one elevated
// invocation (single UAC prompt for the whole batch).
func (s *Server) handleBridgesApplyAll(w http.ResponseWriter, r *http.Request) {
	all := s.snapshotAllBridges()
	log.Printf("bridge: HTTP apply-all request (%d bridges loaded) from=%s", len(all), r.RemoteAddr)
	if err := s.applyBridges(all); err != nil {
		log.Printf("bridge: apply-all failed: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.handleServicesPartial(w, r)
}

// handleBridgePause pauses a bridge without removing its persistent state.
// For WSL members, the `bridge-<name>` PC process is stopped (kills the
// socat). For Windows members, this is a no-op — netsh state is preserved
// so we don't trigger UAC just to flip a bridge off-and-on. Pause is the
// "I don't need this right now but might bring it back later" verb; for
// full removal incl. netsh + firewall, use Reset.
func (s *Server) handleBridgePause(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	log.Printf("bridge: HTTP pause request name=%q from=%s", name, r.RemoteAddr)
	if !validProcessName(name) {
		http.Error(w, "invalid name", http.StatusBadRequest)
		return
	}
	members := s.expandToMembers(name)
	if len(members) == 0 {
		http.Error(w, "bridge not found", http.StatusNotFound)
		return
	}
	if err := s.pauseBridges(members); err != nil {
		log.Printf("bridge: pause %q failed: %v", name, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.handleServicesPartial(w, r)
}

// pauseBridges takes a bridge offline: stops the WSL relay process and
// removes the Windows portproxy + firewall rule. The Windows side requires
// UAC, so we smart-skip when the netsh entry is already absent — clicking
// pause on an already-paused composite is a no-op without elevation.
//
// Differs from Reset only in semantic intent: pause = "off for now, will
// likely apply again"; reset = "I'm done with this". Functionally similar.
func (s *Server) pauseBridges(toPause []bridge.Bridge) error {
	wslSelected := filterByTier(toPause, bridge.TierWSL)
	winSelected := filterByTier(toPause, bridge.TierWindows)

	// Windows: smart-skip when the portproxy is already gone (state ==
	// Missing means nothing to remove → no UAC). Only elevate when we
	// actually have netsh state to clean up.
	winNeeding := s.windowsBridgesNeedingRemoval(winSelected)
	switch {
	case len(winNeeding) > 0:
		names := bridgeNames(winNeeding)
		log.Printf("bridge: pausing %d Windows-tier bridge(s) — UAC required: %s", len(winNeeding), strings.Join(names, ","))
		script := bridge.PowerShellRemove(winNeeding)
		if err := bridge.RunPowerShellElevated(script); err != nil {
			return err
		}
	case len(winSelected) > 0:
		log.Printf("bridge: %d Windows-tier bridge(s) already inactive — skipping elevation: %s", len(winSelected), strings.Join(bridgeNames(winSelected), ","))
	}
	if len(wslSelected) > 0 {
		log.Printf("bridge: pausing %d WSL-tier bridge process(es): %s", len(wslSelected), strings.Join(bridgeNames(wslSelected), ","))
		s.stopWSLBridgeProcesses(wslSelected)
	}
	return nil
}

// windowsBridgesNeedingRemoval returns the subset of Windows-tier bridges
// whose netsh state is currently Active or Drifted — i.e., there's
// something for PowerShellRemove to do. Bridges already in StateMissing are
// skipped so we don't ask for UAC just to confirm an absence.
func (s *Server) windowsBridgesNeedingRemoval(bridges []bridge.Bridge) []bridge.Bridge {
	if len(bridges) == 0 {
		return nil
	}
	out, err := bridgePortproxyShow()
	if err != nil {
		// Probe failed (no PowerShell? netsh quirky?) — apply removals to be
		// safe; the user's UAC click is at worst a no-op.
		log.Printf("bridge: removal-needed probe failed (%v) — applying all %d Windows-tier removal(s)", err, len(bridges))
		return bridges
	}
	entries := bridge.ParsePortproxyShow(out)
	wslHostIP := config.WSLWindowsHostIP()
	var needs []bridge.Bridge
	for _, b := range bridges {
		if b.Status(entries, wslHostIP) != bridge.StateMissing {
			needs = append(needs, b)
		}
	}
	return needs
}

// handleBridgeReset removes one bridge (or all members of a composite) by
// name. UAC required for any Windows-tier members.
func (s *Server) handleBridgeReset(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	log.Printf("bridge: HTTP reset request name=%q from=%s", name, r.RemoteAddr)
	if !validProcessName(name) {
		http.Error(w, "invalid name", http.StatusBadRequest)
		return
	}
	members := s.expandToMembers(name)
	if len(members) == 0 {
		http.Error(w, "bridge not found", http.StatusNotFound)
		return
	}
	if err := s.resetBridges(members); err != nil {
		log.Printf("bridge: reset %q failed: %v", name, err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.handleServicesPartial(w, r)
}

// handleBridgesRefresh reloads bridges.yaml from disk (so YAML edits take
// effect without a restart) and re-snapshots state. Does not run any
// elevated PowerShell — read-only on the system.
func (s *Server) handleBridgesRefresh(w http.ResponseWriter, r *http.Request) {
	log.Printf("bridge: HTTP refresh request from=%s (file=%s)", r.RemoteAddr, s.bridgesFile)
	if s.bridgesFile != "" {
		f, err := bridge.Load(s.bridgesFile)
		if err != nil {
			log.Printf("bridge: refresh failed loading %s: %v", s.bridgesFile, err)
			http.Error(w, "reload bridges.yaml: "+err.Error(), http.StatusInternalServerError)
			return
		}
		newBridges := make([]graph.BridgeInfo, 0, len(f.Bridges))
		for _, b := range f.Bridges {
			newBridges = append(newBridges, graph.BridgeInfo{Bridge: b})
		}
		s.bridgesMu.Lock()
		s.bridges = newBridges
		s.bridgesMu.Unlock()
		log.Printf("bridge: refreshed — %d bridges loaded from %s", len(newBridges), s.bridgesFile)
	}
	// rebuildBridgeViews (called by handleServicesPartial) re-queries
	// portproxy state, so the rendered partial is up-to-date on both
	// declarations and observed state.
	s.handleServicesPartial(w, r)
}

// applyBridges generates the PowerShell payload for any selected Windows
// bridges and (re-)materializes the WSL fragment file when any selected entry
// is tier=wsl. The fragment is a full re-render from the loaded bridges.yaml
// — partial selection only gates whether we touch it at all, not its content.
//
// Smart-apply: Windows bridges that are already in StateActive are skipped,
// so we don't trigger UAC just because the user clicked Apply on something
// that's already in place. Drifted/missing bridges still go through the
// elevated path. Same elevation-batched-into-one-call invariant as before:
// at most ONE UAC prompt per click, regardless of how many bridges need work.
//
// For WSL leaves, after the fragment rewrite + PC reload we also issue an
// explicit /process/start for each selected bridge-* process. PC's reload
// adds NEW process definitions but does not restart entries that are in a
// terminal state (Completed/Stopped) — without the explicit start, an apply
// after a previous stop/reset would leave the relay declared but not running.
func (s *Server) applyBridges(bridges []bridge.Bridge) error {
	winBridges := filterByTier(bridges, bridge.TierWindows)
	wslSelected := filterByTier(bridges, bridge.TierWSL)

	winNeeding := s.windowsBridgesNeedingApply(winBridges)
	switch {
	case len(winNeeding) > 0:
		names := bridgeNames(winNeeding)
		log.Printf("bridge: applying %d Windows-tier bridge(s) — UAC required: %s", len(winNeeding), strings.Join(names, ","))
		script := bridge.PowerShellApply(winNeeding)
		if err := bridge.RunPowerShellElevated(script); err != nil {
			return err
		}
	case len(winBridges) > 0:
		log.Printf("bridge: %d Windows-tier bridge(s) already active — skipping elevation: %s", len(winBridges), strings.Join(bridgeNames(winBridges), ","))
	}
	if len(wslSelected) > 0 {
		if err := s.applyWSLFragment(); err != nil {
			return err
		}
		// After the reload, ensure each selected WSL leaf is actually running.
		// /process/start is idempotent (no-op when already Running/Pending) so
		// this is safe even on a fresh apply where the relays just came up.
		s.startWSLBridgeProcesses(wslSelected)
	}
	if len(winBridges) == 0 && len(wslSelected) == 0 {
		log.Printf("bridge: apply skipped — selection of %d had nothing actionable", len(bridges))
	}
	return nil
}

// startWSLBridgeProcesses ensures each `bridge-<name>` PC process is running.
// Called from the apply path after fragment rewrite + reload — covers the
// case where the relay was previously Stopped/Completed and PC's reload
// doesn't restart it on its own.
func (s *Server) startWSLBridgeProcesses(bridges []bridge.Bridge) {
	c := s.findWSLComposeClient()
	if c == nil {
		log.Printf("bridge: no WSL compose client — skipping process start for %d bridge(s)", len(bridges))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, b := range bridges {
		name := bridge.BridgeProcessName(&b)
		if err := c.Start(ctx, name); err != nil {
			log.Printf("bridge: start %q on %q: %v (continuing — may already be running)", name, c.Name(), err)
		}
	}
}

// windowsBridgesNeedingApply returns the subset of Windows-tier bridges whose
// observed state is NOT StateActive. Used to skip the elevated PowerShell
// call (and the attendant UAC prompt) when the netsh + firewall state is
// already in line with the declared form.
//
// If we can't snapshot the current state (e.g., powershell.exe is unreachable
// from inside a container), we fall back to "apply everything" so user-visible
// behavior matches the pre-smart-apply baseline.
func (s *Server) windowsBridgesNeedingApply(bridges []bridge.Bridge) []bridge.Bridge {
	if len(bridges) == 0 {
		return nil
	}
	out, err := bridgePortproxyShow()
	if err != nil {
		log.Printf("bridge: smart-apply state probe failed (%v) — applying all %d Windows-tier selection(s)", err, len(bridges))
		return bridges
	}
	entries := bridge.ParsePortproxyShow(out)
	wslHostIP := config.WSLWindowsHostIP()
	var needs []bridge.Bridge
	for _, b := range bridges {
		if b.Status(entries, wslHostIP) != bridge.StateActive {
			needs = append(needs, b)
		}
	}
	return needs
}

// bridgePortproxyShow is a package var so tests can stub the netsh probe
// without spawning PowerShell. Production points at bridge.PortproxyShow.
var bridgePortproxyShow = bridge.PortproxyShow

// resetBridges removes Windows-tier bridges (uninstall netsh + firewall rule)
// and rewrites the WSL fragment from the remaining bridges.yaml entries minus
// any selected wsl-tier entries.
//
// Order of operations for the WSL path matters:
//  1. Stop the to-be-removed bridge-* processes via /process/stop. PC's
//     /project/configuration reload doesn't reliably reconcile deletions on
//     all versions (it adds/updates, but may leave dropped entries running).
//  2. Rewrite the fragment file minus those entries — declarative source of
//     truth for any future restart.
//  3. Reload PC so its in-memory definitions match the new fragment.
func (s *Server) resetBridges(toReset []bridge.Bridge) error {
	winBridges := filterByTier(toReset, bridge.TierWindows)
	wslSelected := filterByTier(toReset, bridge.TierWSL)

	if len(winBridges) > 0 {
		names := bridgeNames(winBridges)
		log.Printf("bridge: resetting %d Windows-tier bridge(s): %s", len(winBridges), strings.Join(names, ","))
		script := bridge.PowerShellRemove(winBridges)
		if err := bridge.RunPowerShellElevated(script); err != nil {
			return err
		}
	}
	if len(wslSelected) > 0 {
		// Stop the processes explicitly first — reload alone leaks them on
		// older PC versions.
		s.stopWSLBridgeProcesses(wslSelected)

		dropped := make(map[string]struct{}, len(toReset))
		for _, b := range toReset {
			dropped[b.Name] = struct{}{}
		}
		all := s.snapshotAllBridges()
		remaining := make([]bridge.Bridge, 0, len(all))
		for _, b := range all {
			if _, drop := dropped[b.Name]; !drop {
				remaining = append(remaining, b)
			}
		}
		if err := s.writeWSLFragment(remaining); err != nil {
			return err
		}
		s.reloadWSLProcessCompose()
	}
	return nil
}

// stopWSLBridgeProcesses asks the WSL PC instance to stop each bridge-* whose
// Bridge entry is in the selection. Best-effort — a failure to stop one
// process should not abort the rest, since the fragment rewrite + reload that
// follows is the authoritative reconciliation step.
func (s *Server) stopWSLBridgeProcesses(bridges []bridge.Bridge) {
	c := s.findWSLComposeClient()
	if c == nil {
		log.Printf("bridge: no WSL compose client — skipping process stop for %d bridge(s)", len(bridges))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, b := range bridges {
		name := bridge.BridgeProcessName(&b)
		if err := c.Stop(ctx, name); err != nil {
			log.Printf("bridge: stop %q on %q: %v (continuing)", name, c.Name(), err)
		}
	}
}

// applyWSLFragment renders the fragment from the full loaded bridges set and
// reloads the WSL PC. Failure to reload is logged but does not fail the
// request — the on-disk fragment is the authoritative state.
func (s *Server) applyWSLFragment() error {
	if s.bridgesComposeFile == "" {
		log.Print("bridge: WSL apply skipped — bridges_compose_file not configured")
		return nil
	}
	all := s.snapshotAllBridges()
	if err := s.writeWSLFragment(all); err != nil {
		return err
	}
	s.reloadWSLProcessCompose()
	return nil
}

func (s *Server) writeWSLFragment(bridges []bridge.Bridge) error {
	count, err := wsbWriteFragment(bridges, s.bridgesComposeFile)
	if err != nil {
		return err
	}
	log.Printf("bridge: WSL fragment rewritten with %d socat process(es) → %s", count, s.bridgesComposeFile)
	return nil
}

// wsbWriteFragment is a package var so tests can stub it. Production points
// at bridge.WriteFragment.
var wsbWriteFragment = bridge.WriteFragment

func (s *Server) reloadWSLProcessCompose() {
	c := s.findWSLComposeClient()
	if c == nil {
		log.Printf("bridge: no compose instance colocated with %s — fragment will activate on next PC start", s.bridgesComposeFile)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Reload(ctx); err != nil {
		log.Printf("bridge: reload %q failed: %v — fragment is on disk and will activate on next PC start", c.Name(), err)
		return
	}
	log.Printf("bridge: reloaded process-compose instance %q after fragment rewrite", c.Name())
}

// findWSLComposeClient returns the compose.Client whose compose_file lives in
// the same directory as the bridges fragment. That's the instance whose user-
// owned process-compose.yaml is expected to `extends:` the fragment.
func (s *Server) findWSLComposeClient() *compose.Client {
	if s.bridgesComposeFile == "" {
		return nil
	}
	wantDir := filepath.Dir(s.bridgesComposeFile)
	for name, path := range s.composeFiles {
		if filepath.Dir(path) != wantDir {
			continue
		}
		for _, c := range s.compose {
			if c.Name() == name {
				return c
			}
		}
	}
	return nil
}

func filterByTier(bs []bridge.Bridge, tier bridge.Tier) []bridge.Bridge {
	var out []bridge.Bridge
	for _, b := range bs {
		if b.Tier == tier {
			out = append(out, b)
		}
	}
	return out
}

func bridgeNames(bs []bridge.Bridge) []string {
	out := make([]string, 0, len(bs))
	for _, b := range bs {
		out = append(out, b.Name)
	}
	return out
}

// servicesPageData wraps compose-instance, bridge, and declared-container views.
type servicesPageData struct {
	Instances          []instanceView
	Bridges            []bridgeView
	Containers         []containerView
	ContainerProjects  []containerProjectView // all compose-project entries (services[] non-empty)
	OpenDesignProjects []containerProjectView // pre-filtered subset where Kind == "open-design"; drives the dedicated card section
	Vaults             []vaultCardView
	Docker             dockerHealthView
}

// vaultCardView is the metadata an mcp-fs container surfaces in the
// dedicated vaults section. The actual allowlist + tree content is loaded
// lazily by HTMX from /partials/vault/panel and preserved across polls.
type vaultCardView struct {
	Name         string
	Description  string
	State        string // mirrored from the container's State
	StateClass   string // CSS class for the status pill
	AllowedCount int    // number of allowed paths; -1 when unknown (vault unreachable)
}

// dockerHealthView drives the LED in the containers panel header. Configured
// is false when no Docker client was wired up at startup (e.g. the daemon
// socket wasn't available); in that case the panel hides the indicator.
type dockerHealthView struct {
	Configured bool
	Online     bool
	Endpoint   string
	Error      string
}
