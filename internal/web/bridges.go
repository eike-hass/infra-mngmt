package web

import (
	"context"
	"log"
	"net/http"
	"sort"
	"strings"

	"github.com/eike-hass/infra-mngmt/config"
	"github.com/eike-hass/infra-mngmt/internal/bridge"
	"github.com/eike-hass/infra-mngmt/internal/graph"
)

// bridgeView is the template-friendly representation of one bridge.
type bridgeView struct {
	Name        string
	Tier        string
	Type        string
	Listen      string // "addr:port" with sentinels resolved
	Connect     string // "addr:port"
	State       string // "active" | "drifted" | "missing" | "unknown"
	StateClass  string // CSS class: "running" | "error" | "stopped" | "unknown"
	DisplayName string // firewall display name (Windows) or empty
}

// rebuildBridgeViews re-snapshots the underlying bridge state and returns
// view structs ready for the template. Also updates s.bridges so the
// resolver sees current state on the next render.
func (s *Server) rebuildBridgeViews(_ context.Context) []bridgeView {
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
	views := make([]bridgeView, 0, len(s.bridges))
	for i := range s.bridges {
		bi := &s.bridges[i]
		bi.State = bi.Bridge.Status(entries, wslHostIP)
		listen := bi.Bridge.Listen.Addr
		if listen == "${wsl-host-ip}" && wslHostIP != "" {
			listen = wslHostIP
		}
		views = append(views, bridgeView{
			Name:        bi.Bridge.Name,
			Tier:        string(bi.Bridge.Tier),
			Type:        string(bi.Bridge.Type),
			Listen:      formatHostPort(listen, bi.Bridge.Listen.Port),
			Connect:     formatHostPort(bi.Bridge.Connect.Addr, bi.Bridge.Connect.Port),
			State:       bi.State.String(),
			StateClass:  bridgeStateCSS(bi.State),
			DisplayName: bi.Bridge.Firewall.DisplayName,
		})
	}
	sort.Slice(views, func(i, j int) bool { return views[i].Name < views[j].Name })
	return views
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
	case bridge.StateDrifted:
		return "error"
	case bridge.StateMissing:
		return "stopped"
	default:
		return "unknown"
	}
}

// findBridge returns a snapshot of the named bridge, holding the read lock
// only briefly. Returns ok=false if the name doesn't match anything.
func (s *Server) findBridge(name string) (bridge.Bridge, bool) {
	s.bridgesMu.RLock()
	defer s.bridgesMu.RUnlock()
	for i := range s.bridges {
		if s.bridges[i].Bridge.Name == name {
			return s.bridges[i].Bridge, true
		}
	}
	return bridge.Bridge{}, false
}

// snapshotAllBridges returns a copy of every declared bridge.
func (s *Server) snapshotAllBridges() []bridge.Bridge {
	s.bridgesMu.RLock()
	defer s.bridgesMu.RUnlock()
	out := make([]bridge.Bridge, 0, len(s.bridges))
	for i := range s.bridges {
		out = append(out, s.bridges[i].Bridge)
	}
	return out
}

// handleBridgeApply applies one bridge by name. Triggers a UAC prompt on
// Windows; the request blocks until the user accepts/denies and the elevated
// process exits.
func (s *Server) handleBridgeApply(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	log.Printf("bridge: HTTP apply request name=%q from=%s", name, r.RemoteAddr)
	if !validProcessName(name) {
		http.Error(w, "invalid name", http.StatusBadRequest)
		return
	}
	target, ok := s.findBridge(name)
	if !ok {
		log.Printf("bridge: apply %q — not found among %d loaded bridges", name, len(s.snapshotAllBridges()))
		http.Error(w, "bridge not found", http.StatusNotFound)
		return
	}
	if err := s.applyBridges([]bridge.Bridge{target}); err != nil {
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

// handleBridgeReset removes one bridge by name.
func (s *Server) handleBridgeReset(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	log.Printf("bridge: HTTP reset request name=%q from=%s", name, r.RemoteAddr)
	if !validProcessName(name) {
		http.Error(w, "invalid name", http.StatusBadRequest)
		return
	}
	target, ok := s.findBridge(name)
	if !ok {
		http.Error(w, "bridge not found", http.StatusNotFound)
		return
	}
	if err := s.resetBridges([]bridge.Bridge{target}); err != nil {
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

// applyBridges generates the PowerShell payload for the given bridges and
// runs it elevated. Only Windows-tier bridges are passed through to PS; WSL
// bridges are no-ops here (they belong in process-compose entries).
func (s *Server) applyBridges(bridges []bridge.Bridge) error {
	winBridges := filterWindows(bridges)
	if len(winBridges) == 0 {
		log.Printf("bridge: apply skipped — no Windows-tier bridges in selection of %d", len(bridges))
		return nil
	}
	names := make([]string, 0, len(winBridges))
	for _, b := range winBridges {
		names = append(names, b.Name)
	}
	log.Printf("bridge: applying %d Windows-tier bridge(s): %s", len(winBridges), strings.Join(names, ","))
	script := bridge.PowerShellApply(winBridges)
	return bridge.RunPowerShellElevated(script)
}

func (s *Server) resetBridges(bridges []bridge.Bridge) error {
	winBridges := filterWindows(bridges)
	if len(winBridges) == 0 {
		log.Printf("bridge: reset skipped — no Windows-tier bridges in selection of %d", len(bridges))
		return nil
	}
	names := make([]string, 0, len(winBridges))
	for _, b := range winBridges {
		names = append(names, b.Name)
	}
	log.Printf("bridge: resetting %d Windows-tier bridge(s): %s", len(winBridges), strings.Join(names, ","))
	script := bridge.PowerShellRemove(winBridges)
	return bridge.RunPowerShellElevated(script)
}

func filterWindows(bs []bridge.Bridge) []bridge.Bridge {
	var out []bridge.Bridge
	for _, b := range bs {
		if b.Tier == bridge.TierWindows {
			out = append(out, b)
		}
	}
	return out
}

// servicesPageData wraps compose-instance, bridge, and declared-container views.
type servicesPageData struct {
	Instances  []instanceView
	Bridges    []bridgeView
	Containers []containerView
	Docker     dockerHealthView
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
