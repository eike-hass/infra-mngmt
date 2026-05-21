package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/eike-hass/infra-mngmt/internal/bridge"
	"github.com/eike-hass/infra-mngmt/internal/compose"
	"github.com/eike-hass/infra-mngmt/internal/graph"
)

func TestWSLBridgeStateActiveWhenRunningAndReady(t *testing.T) {
	br := bridge.Bridge{Name: "relay", Tier: bridge.TierWSL, Type: bridge.TypeSocat}
	procs := map[string]compose.ProcessState{
		"bridge-relay": {Name: "bridge-relay", IsRunning: true, HasHealthProbe: true, Health: "Ready"},
	}
	if got := wslBridgeState(&br, procs); got != bridge.StateActive {
		t.Errorf("expected StateActive, got %v", got)
	}
}

func TestWSLBridgeStateActiveWhenRunningWithoutProbe(t *testing.T) {
	br := bridge.Bridge{Name: "noprobe", Tier: bridge.TierWSL, Type: bridge.TypeSocat}
	procs := map[string]compose.ProcessState{
		"bridge-noprobe": {Name: "bridge-noprobe", IsRunning: true, HasHealthProbe: false},
	}
	if got := wslBridgeState(&br, procs); got != bridge.StateActive {
		t.Errorf("expected StateActive (no probe = healthy), got %v", got)
	}
}

func TestWSLBridgeStateDriftedWhenRunningButNotReady(t *testing.T) {
	br := bridge.Bridge{Name: "broken", Tier: bridge.TierWSL, Type: bridge.TypeSocat}
	procs := map[string]compose.ProcessState{
		"bridge-broken": {Name: "bridge-broken", IsRunning: true, HasHealthProbe: true, Health: "Not Ready"},
	}
	if got := wslBridgeState(&br, procs); got != bridge.StateDrifted {
		t.Errorf("expected StateDrifted, got %v", got)
	}
}

func TestWSLBridgeStateMissingWhenNotRunning(t *testing.T) {
	br := bridge.Bridge{Name: "stopped", Tier: bridge.TierWSL, Type: bridge.TypeSocat}
	procs := map[string]compose.ProcessState{
		"bridge-stopped": {Name: "bridge-stopped", IsRunning: false},
	}
	if got := wslBridgeState(&br, procs); got != bridge.StateMissing {
		t.Errorf("expected StateMissing for not-running process, got %v", got)
	}
}

func TestWSLBridgeStateMissingWhenAbsent(t *testing.T) {
	br := bridge.Bridge{Name: "ghost", Tier: bridge.TierWSL, Type: bridge.TypeSocat}
	if got := wslBridgeState(&br, map[string]compose.ProcessState{}); got != bridge.StateMissing {
		t.Errorf("expected StateMissing for missing process, got %v", got)
	}
}

func TestFindWSLComposeClientMatchesByDir(t *testing.T) {
	srv := New(nil, []ComposeEntry{
		{Name: "windows", Endpoint: "http://w", ComposeFile: "/c/Users/x/.config/im/process-compose.yaml"},
		{Name: "wsl", Endpoint: "http://l", ComposeFile: "/etc/im/process-compose.yaml"},
	}, "", nil, nil, nil, nil, nil, nil)
	srv.SetBridgesComposeFile("/etc/im/process-compose.bridges.yaml")

	c := srv.findWSLComposeClient()
	if c == nil {
		t.Fatal("expected to find wsl client")
	}
	if c.Name() != "wsl" {
		t.Errorf("matched wrong client: %q", c.Name())
	}
}

func TestFindWSLComposeClientReturnsNilWhenNoMatch(t *testing.T) {
	srv := New(nil, []ComposeEntry{
		{Name: "windows", Endpoint: "http://w", ComposeFile: "/c/Users/x/process-compose.yaml"},
	}, "", nil, nil, nil, nil, nil, nil)
	srv.SetBridgesComposeFile("/etc/im/process-compose.bridges.yaml")
	if c := srv.findWSLComposeClient(); c != nil {
		t.Errorf("expected nil, got %s", c.Name())
	}
}

func TestApplyBridgesWSLWritesFragmentAndReloads(t *testing.T) {
	// Stand up a fake PC server that records reload hits.
	var reloadCalls int32
	pcSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/project/configuration" && r.Method == http.MethodPost {
			atomic.AddInt32(&reloadCalls, 1)
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer pcSrv.Close()

	dir := t.TempDir()
	composePath := filepath.Join(dir, "process-compose.yaml")
	fragPath := filepath.Join(dir, "process-compose.bridges.yaml")

	br := bridge.Bridge{
		Name: "relay", Tier: bridge.TierWSL, Type: bridge.TypeSocat,
		Listen:  bridge.Endpoint{Addr: "172.17.0.1", Port: 5000},
		Connect: bridge.Endpoint{Addr: "127.0.0.1", Port: 5000, Family: bridge.FamilyAuto},
	}
	srv := New(nil, []ComposeEntry{
		{Name: "wsl", Endpoint: pcSrv.URL, ComposeFile: composePath},
	}, "", nil, []graph.BridgeInfo{{Bridge: br}}, nil, nil, nil, nil)
	srv.SetBridgesComposeFile(fragPath)

	if err := srv.applyBridges([]bridge.Bridge{br}); err != nil {
		t.Fatalf("applyBridges: %v", err)
	}

	data, err := os.ReadFile(fragPath)
	if err != nil {
		t.Fatalf("fragment not written: %v", err)
	}
	if !strings.Contains(string(data), "bridge-relay") {
		t.Errorf("fragment missing bridge-relay:\n%s", string(data))
	}
	if got := atomic.LoadInt32(&reloadCalls); got != 1 {
		t.Errorf("reload hit %d times, want 1", got)
	}
}

func TestApplyBridgesWSLToleratesReloadFailure(t *testing.T) {
	// Reload returns 500 — applyBridges should NOT propagate the error
	// (the fragment is on disk).
	pcSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer pcSrv.Close()

	dir := t.TempDir()
	composePath := filepath.Join(dir, "process-compose.yaml")
	fragPath := filepath.Join(dir, "process-compose.bridges.yaml")

	br := bridge.Bridge{
		Name: "relay", Tier: bridge.TierWSL, Type: bridge.TypeSocat,
		Listen:  bridge.Endpoint{Addr: "172.17.0.1", Port: 5000},
		Connect: bridge.Endpoint{Addr: "127.0.0.1", Port: 5000, Family: bridge.FamilyAuto},
	}
	srv := New(nil, []ComposeEntry{
		{Name: "wsl", Endpoint: pcSrv.URL, ComposeFile: composePath},
	}, "", nil, []graph.BridgeInfo{{Bridge: br}}, nil, nil, nil, nil)
	srv.SetBridgesComposeFile(fragPath)

	if err := srv.applyBridges([]bridge.Bridge{br}); err != nil {
		t.Errorf("applyBridges should not fail on reload error; got %v", err)
	}
	if _, err := os.Stat(fragPath); err != nil {
		t.Errorf("fragment must exist on disk after failed reload: %v", err)
	}
}

func TestRebuildBridgeViewsDerivesWSLStateFromCompose(t *testing.T) {
	// Fake PC returns one bridge process running + ready.
	pcSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/processes" {
			w.Write([]byte(`{"data":[{"name":"bridge-relay","is_running":true,"has_ready_probe":true,"is_ready":"Ready","status":"Running"}]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer pcSrv.Close()

	dir := t.TempDir()
	composePath := filepath.Join(dir, "process-compose.yaml")
	fragPath := filepath.Join(dir, "process-compose.bridges.yaml")

	br := bridge.Bridge{
		Name: "relay", Tier: bridge.TierWSL, Type: bridge.TypeSocat,
		Listen:  bridge.Endpoint{Addr: "172.17.0.1", Port: 5000},
		Connect: bridge.Endpoint{Addr: "127.0.0.1", Port: 5000, Family: bridge.FamilyAuto},
	}
	srv := New(nil, []ComposeEntry{
		{Name: "wsl", Endpoint: pcSrv.URL, ComposeFile: composePath},
	}, "", nil, []graph.BridgeInfo{{Bridge: br}}, nil, nil, nil, nil)
	srv.SetBridgesComposeFile(fragPath)

	views := srv.rebuildBridgeViews(context.Background())
	if len(views) != 1 {
		t.Fatalf("expected 1 view, got %d", len(views))
	}
	if views[0].State != "active" {
		t.Errorf("expected state=active for running+ready WSL bridge, got %q", views[0].State)
	}
	if views[0].StateClass != "running" {
		t.Errorf("expected StateClass=running, got %q", views[0].StateClass)
	}
}

func TestApplyBridgesSmartSkipsActiveWindows(t *testing.T) {
	// All selected Windows bridges are already StateActive in the fake
	// portproxy snapshot — applyBridges must NOT spawn PowerShell.
	prevProbe := bridgePortproxyShow
	bridgePortproxyShow = func() (string, error) {
		// Reproduce the exact two-section header format `netsh ... show all`
		// emits — ParsePortproxyShow tracks section headers to set ProxyType.
		return `Listen on ipv4:             Connect to ipv4:

Address         Port        Address         Port
--------------- ----------  --------------- ----------
10.0.0.1        8080        127.0.0.1       8080
`, nil
	}
	defer func() { bridgePortproxyShow = prevProbe }()

	br := bridge.Bridge{
		Name: "active-win", Tier: bridge.TierWindows, Type: bridge.TypePortproxy,
		Listen:   bridge.Endpoint{Addr: "10.0.0.1", Port: 8080},
		Connect:  bridge.Endpoint{Addr: "127.0.0.1", Port: 8080, Family: bridge.FamilyAuto},
		Firewall: bridge.Firewall{DisplayName: "x", Remote: "10.0.0.0/8"},
	}
	srv := New(nil, nil, "", nil, []graph.BridgeInfo{{Bridge: br}}, nil, nil, nil, nil)

	// If smart-skip is broken, RunPowerShellElevated runs and fails (no
	// powershell.exe in the test environment). A nil-error return means
	// PowerShell wasn't invoked.
	if err := srv.applyBridges([]bridge.Bridge{br}); err != nil {
		t.Errorf("applyBridges should skip elevation when state is already Active; got err %v", err)
	}
}

func TestApplyBridgesSmartElevatesWhenWindowsMissing(t *testing.T) {
	// Snapshot returns no portproxy entries — bridge state is Missing, so
	// the smart filter should pass it to PowerShell. Calling that on a
	// devcontainer without powershell.exe should surface a real error
	// (we'd be elevating). We verify by stubbing the probe; the test for
	// "PowerShell actually invoked" is implicit in the error.
	prevProbe := bridgePortproxyShow
	bridgePortproxyShow = func() (string, error) {
		return "", nil // empty output — no entries
	}
	defer func() { bridgePortproxyShow = prevProbe }()

	br := bridge.Bridge{
		Name: "missing-win", Tier: bridge.TierWindows, Type: bridge.TypePortproxy,
		Listen:   bridge.Endpoint{Addr: "10.0.0.1", Port: 8080},
		Connect:  bridge.Endpoint{Addr: "127.0.0.1", Port: 8080, Family: bridge.FamilyAuto},
		Firewall: bridge.Firewall{DisplayName: "x", Remote: "10.0.0.0/8"},
	}
	srv := New(nil, nil, "", nil, []graph.BridgeInfo{{Bridge: br}}, nil, nil, nil, nil)

	// We expect this to attempt elevation and fail with a powershell-related
	// error. A nil error would mean smart-skip wrongly suppressed it.
	err := srv.applyBridges([]bridge.Bridge{br})
	if err == nil {
		t.Error("applyBridges should attempt elevation for a missing Windows bridge — smart-skip wrongly applied")
	}
}

func TestIsInternalProcessTrueForBridgeNamespace(t *testing.T) {
	if !IsInternalProcess(compose.ProcessState{Name: "bridge-foo", Namespace: "bridges"}) {
		t.Error("bridge-* in bridges namespace should be flagged internal")
	}
}

func TestIsInternalProcessFalseForUserProcess(t *testing.T) {
	cases := []compose.ProcessState{
		{Name: "ident-browser", Namespace: "default"},
		{Name: "bridge-something", Namespace: "default"}, // bridge prefix but wrong ns
		{Name: "foo", Namespace: "bridges"},              // bridges ns but no prefix
	}
	for _, p := range cases {
		if IsInternalProcess(p) {
			t.Errorf("%q should not be flagged internal: %+v", p.Name, p)
		}
	}
}

func TestApplyBridgesSmartFallsBackOnProbeFailure(t *testing.T) {
	// If the state probe errors (e.g., powershell not in PATH), the smart
	// filter must fall back to "apply everything" so the user-visible
	// behavior matches the pre-smart baseline. Verified by attempting
	// elevation (which fails in tests, surfacing an error).
	prevProbe := bridgePortproxyShow
	bridgePortproxyShow = func() (string, error) {
		return "", fmt.Errorf("simulated probe failure")
	}
	defer func() { bridgePortproxyShow = prevProbe }()

	br := bridge.Bridge{
		Name: "probe-fail", Tier: bridge.TierWindows, Type: bridge.TypePortproxy,
		Listen:   bridge.Endpoint{Addr: "10.0.0.1", Port: 8080},
		Connect:  bridge.Endpoint{Addr: "127.0.0.1", Port: 8080, Family: bridge.FamilyAuto},
		Firewall: bridge.Firewall{DisplayName: "x", Remote: "10.0.0.0/8"},
	}
	srv := New(nil, nil, "", nil, []graph.BridgeInfo{{Bridge: br}}, nil, nil, nil, nil)

	// Probe fails → fallback to apply-all → elevation attempted → real error.
	if err := srv.applyBridges([]bridge.Bridge{br}); err == nil {
		t.Error("expected fallback to elevation when probe fails; smart-skip should not suppress on uncertainty")
	}
}

func TestExpandToMembersReturnsCompositeChildren(t *testing.T) {
	parent := bridge.Bridge{Name: "stack", Kind: bridge.KindComposite}
	child1 := bridge.Bridge{Name: "win", Tier: bridge.TierWindows, Type: bridge.TypePortproxy, CompositeOf: "stack",
		Listen: bridge.Endpoint{Addr: "10.0.0.1", Port: 80}, Connect: bridge.Endpoint{Addr: "127.0.0.1", Port: 80, Family: bridge.FamilyAuto},
		Firewall: bridge.Firewall{DisplayName: "x", Remote: "10.0.0.0/8"}}
	child2 := bridge.Bridge{Name: "wsl", Tier: bridge.TierWSL, Type: bridge.TypeSocat, CompositeOf: "stack",
		Listen: bridge.Endpoint{Addr: "172.17.0.1", Port: 80}, Connect: bridge.Endpoint{Addr: "127.0.0.1", Port: 80, Family: bridge.FamilyAuto}}
	srv := New(nil, nil, "", nil, []graph.BridgeInfo{
		{Bridge: parent}, {Bridge: child1}, {Bridge: child2},
	}, nil, nil, nil, nil)

	got := srv.expandToMembers("stack")
	if len(got) != 2 {
		t.Fatalf("expected 2 members, got %d", len(got))
	}
	gotNames := map[string]bool{got[0].Name: true, got[1].Name: true}
	if !gotNames["win"] || !gotNames["wsl"] {
		t.Errorf("expected members win + wsl, got %v", gotNames)
	}
}

func TestExpandToMembersReturnsSingleLeaf(t *testing.T) {
	leaf := bridge.Bridge{Name: "alone", Tier: bridge.TierWindows, Type: bridge.TypePortproxy,
		Listen: bridge.Endpoint{Addr: "10.0.0.1", Port: 80}, Connect: bridge.Endpoint{Addr: "127.0.0.1", Port: 80, Family: bridge.FamilyAuto},
		Firewall: bridge.Firewall{DisplayName: "x", Remote: "10.0.0.0/8"}}
	srv := New(nil, nil, "", nil, []graph.BridgeInfo{{Bridge: leaf}}, nil, nil, nil, nil)
	got := srv.expandToMembers("alone")
	if len(got) != 1 || got[0].Name != "alone" {
		t.Errorf("expected single leaf, got %+v", got)
	}
}

func TestExpandToMembersUnknownReturnsNil(t *testing.T) {
	srv := New(nil, nil, "", nil, nil, nil, nil, nil, nil)
	if got := srv.expandToMembers("ghost"); got != nil {
		t.Errorf("expected nil for unknown name, got %+v", got)
	}
}

func TestRollupStateAllActive(t *testing.T) {
	got := rollupState([]bridge.State{bridge.StateActive, bridge.StateActive})
	if got != bridge.StateActive {
		t.Errorf("expected Active when all members active; got %v", got)
	}
}

func TestRollupStateAllMissing(t *testing.T) {
	got := rollupState([]bridge.State{bridge.StateMissing, bridge.StateMissing})
	if got != bridge.StateMissing {
		t.Errorf("expected Missing when all members missing; got %v", got)
	}
}

func TestRollupStateMixedYieldsDrifted(t *testing.T) {
	got := rollupState([]bridge.State{bridge.StateActive, bridge.StateMissing})
	if got != bridge.StateDrifted {
		t.Errorf("expected Drifted for partial up; got %v", got)
	}
}

func TestRollupStateAllUnknown(t *testing.T) {
	got := rollupState([]bridge.State{bridge.StateUnknown, bridge.StateUnknown})
	if got != bridge.StateUnknown {
		t.Errorf("expected Unknown when nothing is observable; got %v", got)
	}
}

func TestRollupStateEmpty(t *testing.T) {
	got := rollupState(nil)
	if got != bridge.StateUnknown {
		t.Errorf("expected Unknown for empty members; got %v", got)
	}
}

func TestRollupStateAnyDegradedSurfacesDegraded(t *testing.T) {
	got := rollupState([]bridge.State{bridge.StateActive, bridge.StateDegraded})
	if got != bridge.StateDegraded {
		t.Errorf("expected Degraded to bubble up over Active; got %v", got)
	}
}

func TestRebuildBridgeViewsCompositeFoldsMembers(t *testing.T) {
	parent := bridge.Bridge{Name: "stack", Kind: bridge.KindComposite, Description: "test stack"}
	winChild := bridge.Bridge{Name: "stack-win", Tier: bridge.TierWindows, Type: bridge.TypePortproxy, CompositeOf: "stack",
		Listen: bridge.Endpoint{Addr: "10.0.0.1", Port: 80}, Connect: bridge.Endpoint{Addr: "127.0.0.1", Port: 80, Family: bridge.FamilyAuto},
		Firewall: bridge.Firewall{DisplayName: "stack-win", Remote: "10.0.0.0/8"}}
	srv := New(nil, nil, "", nil, []graph.BridgeInfo{
		{Bridge: parent}, {Bridge: winChild},
	}, nil, nil, nil, nil)

	views := srv.rebuildBridgeViews(context.Background())
	// Top level should have ONE row (the composite). The member is folded.
	if len(views) != 1 {
		t.Fatalf("expected 1 top-level view (composite only), got %d: %+v", len(views), views)
	}
	if views[0].Name != "stack" {
		t.Errorf("expected composite to be the top-level row, got %q", views[0].Name)
	}
	if views[0].Kind != string(bridge.KindComposite) {
		t.Errorf("expected Kind=composite, got %q", views[0].Kind)
	}
	if len(views[0].Members) != 1 {
		t.Errorf("expected composite to have 1 nested member, got %d", len(views[0].Members))
	}
}

func TestApplyBridgesStartsWSLProcessesAfterReload(t *testing.T) {
	// Apply must explicitly /process/start each WSL leaf — relying on PC's
	// /project/configuration reload to restart processes is unreliable
	// (Completed/Stopped processes stay terminal across reloads).
	var (
		mu           sync.Mutex
		started      []string
		reloadCalled bool
	)
	pcSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.URL.Path == "/project/configuration" && r.Method == http.MethodPost:
			reloadCalled = true
			w.WriteHeader(http.StatusOK)
		case strings.HasPrefix(r.URL.Path, "/process/start/") && r.Method == http.MethodPost:
			started = append(started, strings.TrimPrefix(r.URL.Path, "/process/start/"))
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer pcSrv.Close()

	dir := t.TempDir()
	composePath := filepath.Join(dir, "process-compose.yaml")
	fragPath := filepath.Join(dir, "process-compose.bridges.yaml")

	wslLeaf := bridge.Bridge{
		Name: "relay", Tier: bridge.TierWSL, Type: bridge.TypeSocat,
		Listen:  bridge.Endpoint{Addr: "172.17.0.1", Port: 8080},
		Connect: bridge.Endpoint{Addr: "127.0.0.1", Port: 8080, Family: bridge.FamilyAuto},
	}
	srv := New(nil, []ComposeEntry{
		{Name: "wsl", Endpoint: pcSrv.URL, ComposeFile: composePath},
	}, "", nil, []graph.BridgeInfo{{Bridge: wslLeaf}}, nil, nil, nil, nil)
	srv.SetBridgesComposeFile(fragPath)

	if err := srv.applyBridges([]bridge.Bridge{wslLeaf}); err != nil {
		t.Fatalf("applyBridges: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !reloadCalled {
		t.Error("expected fragment reload to fire after rewrite")
	}
	if len(started) != 1 || started[0] != "bridge-relay" {
		t.Errorf("expected explicit /process/start/bridge-relay after reload; got %v", started)
	}
}

func TestPauseBridgesStopsWSLProcessAndSkipsRemovalWhenWindowsAlreadyMissing(t *testing.T) {
	// Stub the portproxy probe to return no entries — windows leaf is
	// already StateMissing, so pause should NOT attempt elevation. Verify
	// the WSL leaf still gets its /process/stop call.
	prevProbe := bridgePortproxyShow
	bridgePortproxyShow = func() (string, error) { return "", nil } // empty → no entries
	defer func() { bridgePortproxyShow = prevProbe }()

	stopped := map[string]int{}
	pcSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/process/stop/") && r.Method == http.MethodPatch {
			name := strings.TrimPrefix(r.URL.Path, "/process/stop/")
			stopped[name]++
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer pcSrv.Close()

	dir := t.TempDir()
	composePath := filepath.Join(dir, "process-compose.yaml")
	fragPath := filepath.Join(dir, "process-compose.bridges.yaml")

	wslLeaf := bridge.Bridge{
		Name: "relay", Tier: bridge.TierWSL, Type: bridge.TypeSocat,
		Listen:  bridge.Endpoint{Addr: "172.17.0.1", Port: 8080},
		Connect: bridge.Endpoint{Addr: "127.0.0.1", Port: 8080, Family: bridge.FamilyAuto},
	}
	winLeaf := bridge.Bridge{
		Name: "win", Tier: bridge.TierWindows, Type: bridge.TypePortproxy,
		Listen:   bridge.Endpoint{Addr: "10.0.0.1", Port: 8080},
		Connect:  bridge.Endpoint{Addr: "127.0.0.1", Port: 8080, Family: bridge.FamilyAuto},
		Firewall: bridge.Firewall{DisplayName: "x", Remote: "10.0.0.0/8"},
	}
	srv := New(nil, []ComposeEntry{
		{Name: "wsl", Endpoint: pcSrv.URL, ComposeFile: composePath},
	}, "", nil, []graph.BridgeInfo{{Bridge: wslLeaf}, {Bridge: winLeaf}}, nil, nil, nil, nil)
	srv.SetBridgesComposeFile(fragPath)

	if err := srv.pauseBridges([]bridge.Bridge{wslLeaf, winLeaf}); err != nil {
		t.Fatalf("pauseBridges: %v", err)
	}
	if stopped["bridge-relay"] != 1 {
		t.Errorf("expected /process/stop/bridge-relay to fire once, hits=%d", stopped["bridge-relay"])
	}
}

func TestPauseBridgesElevatesWhenWindowsActive(t *testing.T) {
	// Probe returns a matching active entry — pause must attempt
	// PowerShellRemove. We can't easily verify the elevation actually fires
	// without spawning powershell.exe, but we can detect it tried by
	// expecting the elevation error in the test environment (no powershell
	// available in the devcontainer).
	prevProbe := bridgePortproxyShow
	bridgePortproxyShow = func() (string, error) {
		return `Listen on ipv4:             Connect to ipv4:

Address         Port        Address         Port
--------------- ----------  --------------- ----------
10.0.0.1        8080        127.0.0.1       8080
`, nil
	}
	defer func() { bridgePortproxyShow = prevProbe }()

	winLeaf := bridge.Bridge{
		Name: "active-win", Tier: bridge.TierWindows, Type: bridge.TypePortproxy,
		Listen:   bridge.Endpoint{Addr: "10.0.0.1", Port: 8080},
		Connect:  bridge.Endpoint{Addr: "127.0.0.1", Port: 8080, Family: bridge.FamilyAuto},
		Firewall: bridge.Firewall{DisplayName: "x", Remote: "10.0.0.0/8"},
	}
	srv := New(nil, nil, "", nil, []graph.BridgeInfo{{Bridge: winLeaf}}, nil, nil, nil, nil)

	if err := srv.pauseBridges([]bridge.Bridge{winLeaf}); err == nil {
		t.Error("pause should attempt elevation when Windows leaf is active; smart-skip wrongly suppressed it")
	}
}

func TestPauseBridgesSkipsElevationWhenWindowsMissing(t *testing.T) {
	// Empty probe output → state is Missing → pause should skip elevation
	// entirely (no error from absent powershell.exe).
	prevProbe := bridgePortproxyShow
	bridgePortproxyShow = func() (string, error) { return "", nil }
	defer func() { bridgePortproxyShow = prevProbe }()

	winLeaf := bridge.Bridge{
		Name: "missing-win", Tier: bridge.TierWindows, Type: bridge.TypePortproxy,
		Listen:   bridge.Endpoint{Addr: "10.0.0.1", Port: 8080},
		Connect:  bridge.Endpoint{Addr: "127.0.0.1", Port: 8080, Family: bridge.FamilyAuto},
		Firewall: bridge.Firewall{DisplayName: "x", Remote: "10.0.0.0/8"},
	}
	srv := New(nil, nil, "", nil, []graph.BridgeInfo{{Bridge: winLeaf}}, nil, nil, nil, nil)

	if err := srv.pauseBridges([]bridge.Bridge{winLeaf}); err != nil {
		t.Errorf("pause should skip elevation when Windows leaf is already Missing; got %v", err)
	}
}

func TestRebuildBridgeViewsWSLMissingWhenNoProcess(t *testing.T) {
	// Fake PC returns no processes — the WSL bridge should show as missing.
	pcSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/processes" {
			w.Write([]byte(`{"data":[]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer pcSrv.Close()

	dir := t.TempDir()
	br := bridge.Bridge{
		Name: "ghost", Tier: bridge.TierWSL, Type: bridge.TypeSocat,
		Listen:  bridge.Endpoint{Addr: "172.17.0.1", Port: 5001},
		Connect: bridge.Endpoint{Addr: "127.0.0.1", Port: 5001, Family: bridge.FamilyAuto},
	}
	srv := New(nil, []ComposeEntry{
		{Name: "wsl", Endpoint: pcSrv.URL, ComposeFile: filepath.Join(dir, "process-compose.yaml")},
	}, "", nil, []graph.BridgeInfo{{Bridge: br}}, nil, nil, nil, nil)
	srv.SetBridgesComposeFile(filepath.Join(dir, "process-compose.bridges.yaml"))

	views := srv.rebuildBridgeViews(context.Background())
	if len(views) != 1 || views[0].State != "missing" {
		t.Errorf("expected one missing view, got %+v", views)
	}
}
