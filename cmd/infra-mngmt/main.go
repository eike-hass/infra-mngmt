package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/eike-hass/infra-mngmt/config"
	"github.com/eike-hass/infra-mngmt/internal/bridge"
	"github.com/eike-hass/infra-mngmt/internal/compose"
	"github.com/eike-hass/infra-mngmt/internal/containers"
	"github.com/eike-hass/infra-mngmt/internal/deps"
	"github.com/eike-hass/infra-mngmt/internal/docker"
	"github.com/eike-hass/infra-mngmt/internal/entity"
	"github.com/eike-hass/infra-mngmt/internal/graph"
	"github.com/eike-hass/infra-mngmt/internal/rates"
	"github.com/eike-hass/infra-mngmt/internal/source"
	"github.com/eike-hass/infra-mngmt/internal/web"
)

// buildEpoch is the Unix-epoch build timestamp, injected via -ldflags
// "-X main.buildEpoch=<epoch>" by the Makefile. Empty for ad-hoc `go build`
// invocations that don't pass the flag.
var buildEpoch = ""

const usage = `infra-mngmt — Claude Code config viewer + process supervisor

usage:
  infra-mngmt [server] [-config <path>]    start the HTTP server (default)
  infra-mngmt addr <kind>                  print a network address
  infra-mngmt bridges <subcommand>         manage persistent network bridges
  infra-mngmt version                      print build info (commit, dirty, go version)

addr kinds:
  windows-host    Windows host IP visible from inside WSL2 (default-route gateway)

bridges subcommands:
  list                       list bridges declared in bridges.yaml
  status                     show observed state of each bridge
  apply [name...]            (re)apply bridges (UAC prompt only when needed)
  pause [name...]            pause WSL relays without removing netsh state (no UAC)
  reset [name...]            remove bridges incl. netsh + firewall (UAC prompt)
`

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		runServer(args)
		return
	}
	switch args[0] {
	case "server":
		runServer(args[1:])
	case "addr":
		runAddr(args[1:])
	case "bridges":
		runBridges(args[1:])
	case "version":
		runVersion()
	case "-h", "--help", "help":
		fmt.Print(usage)
	case "-config", "--config":
		// Top-level flag; treat as implicit `server`.
		runServer(args)
	default:
		// Unknown first token — could be a flag the legacy invocation passes.
		// Treat the whole arg list as server flags for backwards compat.
		if strings.HasPrefix(args[0], "-") {
			runServer(args)
			return
		}
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n\n%s", args[0], usage)
		os.Exit(2)
	}
}

func runAddr(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: infra-mngmt addr <kind>")
		os.Exit(2)
	}
	switch args[0] {
	case "windows-host":
		ip := config.WSLWindowsHostIP()
		if ip == "" {
			fmt.Fprintln(os.Stderr, "windows-host IP unavailable (not running inside WSL2?)")
			os.Exit(1)
		}
		fmt.Println(ip)
	default:
		fmt.Fprintf(os.Stderr, "unknown addr kind %q (known: windows-host)\n", args[0])
		os.Exit(2)
	}
}

func runBridges(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: infra-mngmt bridges <list|status|apply|pause|reset> [args...]")
		os.Exit(2)
	}
	fs := flag.NewFlagSet("bridges", flag.ExitOnError)
	configPath := fs.String("config", config.DefaultPath(), "config file path")
	bridgesPath := fs.String("bridges", "", "override bridges.yaml path (default: from config)")
	_ = fs.Parse(args[1:])

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}
	resolvedPath := *bridgesPath
	if resolvedPath == "" {
		resolvedPath = cfg.BridgesFile
	}
	if resolvedPath == "" {
		// Default convention: alongside the config file.
		resolvedPath = filepath.Join(filepath.Dir(*configPath), "bridges.yaml")
	}

	file, err := bridge.Load(resolvedPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load bridges: %v\n", err)
		os.Exit(1)
	}

	rest := fs.Args()
	switch args[0] {
	case "list":
		runBridgesList(file)
	case "status":
		runBridgesStatus(file)
	case "apply":
		runBridgesApply(file, cfg, rest)
	case "pause":
		runBridgesPause(file, cfg, rest)
	case "reset":
		runBridgesReset(file, cfg, rest)
	default:
		fmt.Fprintf(os.Stderr, "unknown bridges subcommand %q\n", args[0])
		os.Exit(2)
	}
}

func runBridgesList(f *bridge.File) {
	if len(f.Bridges) == 0 {
		fmt.Println("(no bridges configured)")
		return
	}
	for _, b := range f.Bridges {
		fmt.Printf("%-20s tier=%-7s type=%-22s listen=%s:%d connect=%s:%d\n",
			b.Name, b.Tier, b.Type, b.Listen.Addr, b.Listen.Port, b.Connect.Addr, b.Connect.Port)
	}
}

func runBridgesStatus(f *bridge.File) {
	if len(f.Bridges) == 0 {
		fmt.Println("(no bridges configured)")
		return
	}
	out, err := bridge.PortproxyShow()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: %v — bridge state unknown\n", err)
	}
	entries := bridge.ParsePortproxyShow(out)
	wslHostIP := windowsAdapterIP() // best-effort; "" is OK
	states := make([]bridge.State, len(f.Bridges))
	for i := range f.Bridges {
		states[i] = f.Bridges[i].Status(entries, wslHostIP)
	}
	fmt.Print(bridge.FormatStatusTable(f.Bridges, states))
}

func runBridgesApply(f *bridge.File, cfg *config.Config, names []string) {
	selected := bridge.Filter(f.Bridges, names)
	if len(selected) == 0 {
		if len(names) > 0 {
			fmt.Fprintf(os.Stderr, "no matching bridges for: %s\n", strings.Join(names, ", "))
			os.Exit(1)
		}
		fmt.Println("(no bridges configured)")
		return
	}

	// Split by tier — Windows bridges go through one elevated PS call;
	// WSL bridges flow through the process-compose fragment file (rewritten
	// from scratch each apply, then PC is told to reload + each selected
	// bridge-* process explicitly started).
	var winBridges []bridge.Bridge
	var wslSelected []bridge.Bridge
	for _, b := range selected {
		switch b.Tier {
		case bridge.TierWindows:
			winBridges = append(winBridges, b)
		case bridge.TierWSL:
			wslSelected = append(wslSelected, b)
		}
	}

	if len(winBridges) > 0 {
		needing := windowsBridgesNeedingApply(winBridges)
		switch {
		case len(needing) > 0:
			fmt.Printf("Applying %d Windows bridge(s) (UAC prompt may appear): %s\n", len(needing), strings.Join(bridgeNamesSlice(needing), ", "))
			script := bridge.PowerShellApply(needing)
			if err := bridge.RunPowerShellElevated(script); err != nil {
				fmt.Fprintf(os.Stderr, "apply: %v\n", err)
				os.Exit(1)
			}
		default:
			fmt.Printf("All %d Windows bridge(s) already active — skipping elevation: %s\n", len(winBridges), strings.Join(bridgeNamesSlice(winBridges), ", "))
		}
	}

	// The fragment is declarative — always render from the FULL bridges.yaml,
	// not just the selected subset. A targeted apply still reconciles the
	// fragment so it matches the file. This mirrors the Windows path, which
	// uses the same all-or-subset toggle but writes idempotent netsh rules.
	if len(wslSelected) > 0 || cfg.BridgesComposeFile != "" {
		applyWSLFragment(f.Bridges, cfg)
	}
	// PC's /project/configuration reload adds new process definitions but
	// doesn't restart processes already in a terminal state (Completed/
	// Stopped) — without an explicit start, an apply after a previous stop
	// would leave the relay declared but not running. /process/start is
	// idempotent on already-Running entries.
	if len(wslSelected) > 0 {
		startWSLBridgeProcesses(wslSelected, cfg)
	}

	// Reverify and print updated status.
	runBridgesStatus(f)
}

// startWSLBridgeProcesses ensures each `bridge-<name>` PC process is running.
// Called from the apply path so previously-stopped relays come back online
// without a separate `infra-mngmt bridges` invocation.
func startWSLBridgeProcesses(bridges []bridge.Bridge, cfg *config.Config) {
	pc, ok := findWSLProcessCompose(cfg)
	if !ok {
		return
	}
	tok := pc.Token
	if tok == "" && pc.TokenFile != "" {
		if data, err := os.ReadFile(pc.TokenFile); err == nil {
			tok = strings.TrimSpace(string(data))
		} else {
			log.Printf("warning: process_compose %q: read token_file %q: %v — instance will be queried without auth", pc.Name, pc.TokenFile, err)
		}
	}
	client := compose.New(pc.Name, config.ResolveEndpoint(pc.Endpoint), tok)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, b := range bridges {
		name := bridge.BridgeProcessName(&b)
		if err := client.Start(ctx, name); err != nil {
			fmt.Fprintf(os.Stderr, "start %s on %s: %v (continuing — may already be running)\n", name, pc.Name, err)
		}
	}
}

// windowsBridgesNeedingApply mirrors the web layer's smart-apply filter:
// returns only bridges whose observed netsh state isn't already StateActive.
// On state-probe failure (e.g., PowerShell unreachable in a devcontainer),
// returns the full input so behavior degrades gracefully to the pre-smart
// baseline.
func windowsBridgesNeedingApply(bridges []bridge.Bridge) []bridge.Bridge {
	if len(bridges) == 0 {
		return nil
	}
	out, err := bridge.PortproxyShow()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: state probe failed (%v) — applying all %d Windows-tier bridge(s)\n", err, len(bridges))
		return bridges
	}
	entries := bridge.ParsePortproxyShow(out)
	wslHostIP := windowsAdapterIP()
	var needs []bridge.Bridge
	for _, b := range bridges {
		if b.Status(entries, wslHostIP) != bridge.StateActive {
			needs = append(needs, b)
		}
	}
	return needs
}

func bridgeNamesSlice(bs []bridge.Bridge) []string {
	out := make([]string, 0, len(bs))
	for _, b := range bs {
		out = append(out, b.Name)
	}
	return out
}

// runBridgesPause takes selected bridges offline: stops the WSL relay
// process(es) and removes the Windows portproxy + firewall rule(s). The
// Windows side requires UAC; pause smart-skips when nothing's there to
// remove (state == Missing). Mirrors the web layer's /bridge/pause route.
func runBridgesPause(f *bridge.File, cfg *config.Config, names []string) {
	selected := bridge.Filter(f.Bridges, names)
	if len(selected) == 0 {
		if len(names) > 0 {
			fmt.Fprintf(os.Stderr, "no matching bridges for: %s\n", strings.Join(names, ", "))
			os.Exit(1)
		}
		fmt.Println("(no bridges configured)")
		return
	}

	var wslSelected []bridge.Bridge
	var winSelected []bridge.Bridge
	for _, b := range selected {
		switch b.Tier {
		case bridge.TierWSL:
			wslSelected = append(wslSelected, b)
		case bridge.TierWindows:
			winSelected = append(winSelected, b)
		}
	}
	if len(winSelected) > 0 {
		needing := windowsBridgesNeedingRemoval(winSelected)
		switch {
		case len(needing) > 0:
			fmt.Printf("Pausing %d Windows bridge(s) (UAC prompt may appear): %s\n", len(needing), strings.Join(bridgeNamesSlice(needing), ", "))
			script := bridge.PowerShellRemove(needing)
			if err := bridge.RunPowerShellElevated(script); err != nil {
				fmt.Fprintf(os.Stderr, "pause: %v\n", err)
				os.Exit(1)
			}
		default:
			fmt.Printf("All %d Windows bridge(s) already inactive — skipping elevation: %s\n", len(winSelected), strings.Join(bridgeNamesSlice(winSelected), ", "))
		}
	}
	if len(wslSelected) > 0 {
		fmt.Printf("Pausing %d WSL bridge process(es): %s\n", len(wslSelected), strings.Join(bridgeNamesSlice(wslSelected), ", "))
		stopWSLBridgeProcesses(wslSelected, cfg)
	}
	runBridgesStatus(f)
}

// windowsBridgesNeedingRemoval mirrors the web layer's smart-skip filter
// for the removal direction: returns bridges that are currently Active or
// Drifted (i.e., have netsh state to remove). Bridges already Missing get
// skipped so we don't ask for UAC just to confirm an absence.
func windowsBridgesNeedingRemoval(bridges []bridge.Bridge) []bridge.Bridge {
	if len(bridges) == 0 {
		return nil
	}
	out, err := bridge.PortproxyShow()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: removal-needed probe failed (%v) — applying all %d Windows-tier removal(s)\n", err, len(bridges))
		return bridges
	}
	entries := bridge.ParsePortproxyShow(out)
	wslHostIP := windowsAdapterIP()
	var needs []bridge.Bridge
	for _, b := range bridges {
		if b.Status(entries, wslHostIP) != bridge.StateMissing {
			needs = append(needs, b)
		}
	}
	return needs
}

func runBridgesReset(f *bridge.File, cfg *config.Config, names []string) {
	selected := bridge.Filter(f.Bridges, names)
	if len(selected) == 0 {
		fmt.Println("(no bridges to reset)")
		return
	}
	var winBridges []bridge.Bridge
	for _, b := range selected {
		if b.Tier == bridge.TierWindows {
			winBridges = append(winBridges, b)
		}
	}
	if len(winBridges) > 0 {
		script := bridge.PowerShellRemove(winBridges)
		fmt.Printf("Removing %d Windows bridge(s) (UAC prompt may appear)...\n", len(winBridges))
		if err := bridge.RunPowerShellElevated(script); err != nil {
			fmt.Fprintf(os.Stderr, "reset: %v\n", err)
			os.Exit(1)
		}
	}

	// Recompute the WSL fragment from f.Bridges minus the reset selection.
	// Always re-render (even if no WSL bridges were touched) so the file on
	// disk stays consistent with bridges.yaml.
	if cfg.BridgesComposeFile != "" {
		// Stop the to-be-removed WSL bridge processes explicitly first.
		// PC's /project/configuration reload doesn't reliably reconcile
		// deletions on all versions — without an explicit stop, the dropped
		// socat keeps running until the next PC restart.
		var wslSelected []bridge.Bridge
		for _, b := range selected {
			if b.Tier == bridge.TierWSL {
				wslSelected = append(wslSelected, b)
			}
		}
		if len(wslSelected) > 0 {
			stopWSLBridgeProcesses(wslSelected, cfg)
		}

		dropped := nameSet(selected)
		var remaining []bridge.Bridge
		for _, b := range f.Bridges {
			if !dropped[b.Name] {
				remaining = append(remaining, b)
			}
		}
		applyWSLFragment(remaining, cfg)
	}

	runBridgesStatus(f)
}

// stopWSLBridgeProcesses asks the WSL process-compose instance to stop each
// `bridge-<name>` process whose Bridge entry is in the selection. Best-
// effort: failure to stop one process is logged but doesn't abort the rest,
// since the fragment rewrite + reload that follows is the authoritative
// reconciliation step.
func stopWSLBridgeProcesses(bridges []bridge.Bridge, cfg *config.Config) {
	pc, ok := findWSLProcessCompose(cfg)
	if !ok {
		return
	}
	tok := pc.Token
	if tok == "" && pc.TokenFile != "" {
		if data, err := os.ReadFile(pc.TokenFile); err == nil {
			tok = strings.TrimSpace(string(data))
		} else {
			log.Printf("warning: process_compose %q: read token_file %q: %v — instance will be queried without auth", pc.Name, pc.TokenFile, err)
		}
	}
	client := compose.New(pc.Name, config.ResolveEndpoint(pc.Endpoint), tok)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, b := range bridges {
		name := bridge.BridgeProcessName(&b)
		if err := client.Stop(ctx, name); err != nil {
			fmt.Fprintf(os.Stderr, "stop %s on %s: %v (continuing)\n", name, pc.Name, err)
		}
	}
}

func nameSet(bridges []bridge.Bridge) map[string]bool {
	out := make(map[string]bool, len(bridges))
	for _, b := range bridges {
		out[b.Name] = true
	}
	return out
}

// applyWSLFragment renders the wsl/socat bridges into a process-compose
// fragment file, then asks the WSL process-compose instance to reload.
//
// Failure modes are surfaced as warnings, not errors: the fragment is
// authoritative state on disk, so even if PC isn't reachable right now the
// next time it starts (or the user reloads manually) it'll pick up the
// changes via the user's main process-compose.yaml `extends:` reference.
func applyWSLFragment(allBridges []bridge.Bridge, cfg *config.Config) {
	if cfg.BridgesComposeFile == "" {
		return
	}
	count, err := bridge.WriteFragment(allBridges, cfg.BridgesComposeFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "write compose fragment: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Wrote %d wsl bridge(s) to %s\n", count, cfg.BridgesComposeFile)

	// Find the WSL process-compose instance to reload — the one whose
	// compose_file lives in the same directory as the fragment, since that's
	// where the user's `extends:` resolves relative paths.
	pc, ok := findWSLProcessCompose(cfg)
	if !ok {
		fmt.Printf("note: no process_compose entry found whose compose_file is colocated with %s — skip reload\n", cfg.BridgesComposeFile)
		fmt.Println("      the fragment is on disk; it will take effect when process-compose next reads its config")
		return
	}
	if err := reloadProcessCompose(pc); err != nil {
		fmt.Fprintf(os.Stderr, "reload %s: %v — fragment is on disk and will be picked up on next process-compose start\n", pc.Name, err)
		return
	}
	fmt.Printf("Reloaded process-compose instance %q\n", pc.Name)
}

// findWSLProcessCompose returns the PC instance whose compose_file shares a
// directory with the bridges fragment file. That's the one whose user-owned
// process-compose.yaml is expected to `extends:` the fragment — reloading it
// makes the new socat entries take effect immediately.
//
// Returns false if no instance is configured or none match — callers should
// fall back to "fragment-only" mode (write file, skip reload).
func findWSLProcessCompose(cfg *config.Config) (config.ProcessCompose, bool) {
	if cfg.BridgesComposeFile == "" {
		return config.ProcessCompose{}, false
	}
	wantDir := filepath.Dir(cfg.BridgesComposeFile)
	for _, pc := range cfg.ProcessCompose {
		if pc.ComposeFile == "" {
			continue
		}
		if filepath.Dir(pc.ComposeFile) == wantDir {
			return pc, true
		}
	}
	return config.ProcessCompose{}, false
}

func reloadProcessCompose(pc config.ProcessCompose) error {
	tok := pc.Token
	if tok == "" && pc.TokenFile != "" {
		if data, err := os.ReadFile(pc.TokenFile); err == nil {
			tok = strings.TrimSpace(string(data))
		} else {
			log.Printf("warning: process_compose %q: read token_file %q: %v — instance will be queried without auth", pc.Name, pc.TokenFile, err)
		}
	}
	client := compose.New(pc.Name, config.ResolveEndpoint(pc.Endpoint), tok)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return client.Reload(ctx)
}

// collectBuildInfo gathers the binary's identity from the Go toolchain's
// embedded VCS settings plus the buildEpoch ldflag. Shared by `version` (CLI
// output) and the /api/version HTTP endpoint so both report the same data.
func collectBuildInfo() web.BuildInfo {
	out := web.BuildInfo{BuildEpoch: buildEpoch}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return out
	}
	out.GoVersion = info.GoVersion
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			out.Commit = s.Value
		case "vcs.modified":
			out.Dirty = s.Value == "true"
		case "vcs.time":
			out.VCSTime = s.Value
		}
	}
	if len(out.Commit) > 8 {
		out.Commit = out.Commit[:8]
	}
	return out
}

// runVersion prints build info embedded by the Go toolchain. Useful to
// confirm a deployment has the expected commit; "dirty=true" means the
// binary was built from a working tree with uncommitted changes.
func runVersion() {
	b := collectBuildInfo()
	if b.Commit == "" && b.GoVersion == "" {
		fmt.Println("infra-mngmt (build info unavailable)")
		return
	}
	commit := b.Commit
	if commit == "" {
		commit = "(unknown)"
	}
	if b.Dirty {
		commit += "-dirty"
	}
	stamp := b.BuildEpoch
	if stamp == "" {
		stamp = "(unstamped)"
	}
	fmt.Printf("infra-mngmt commit=%s vcs_time=%s build_epoch=%s go=%s\n", commit, b.VCSTime, stamp, b.GoVersion)
}

// loadResolverInputs reads bridges.yaml, dependencies.yaml, and
// containers.yaml using the paths declared in cfg, falling back to
// conventional locations alongside the main config file. Failures are
// logged but non-fatal: the server still runs with just the legacy
// substring resolver. The resolved file paths are returned so the server
// can hot-reload them via /*/refresh routes.
func loadResolverInputs(configPath string, cfg *config.Config) ([]graph.BridgeInfo, []deps.Rule, string, []containers.Container, string, map[string]rates.Rate) {
	bridgesPath := cfg.BridgesFile
	if bridgesPath == "" {
		bridgesPath = filepath.Join(filepath.Dir(configPath), "bridges.yaml")
	}
	depsPath := cfg.DependenciesFile
	if depsPath == "" {
		depsPath = filepath.Join(filepath.Dir(configPath), "dependencies.yaml")
	}
	containersPath := cfg.ContainersFile
	if containersPath == "" {
		containersPath = filepath.Join(filepath.Dir(configPath), "containers.yaml")
	}
	ratesPath := cfg.ModelRatesFile
	if ratesPath == "" {
		ratesPath = filepath.Join(filepath.Dir(configPath), "model-rates.yaml")
	}

	var bridgesInfo []graph.BridgeInfo
	if bf, err := bridge.Load(bridgesPath); err != nil {
		log.Printf("warning: load bridges %s: %v", bridgesPath, err)
	} else if len(bf.Bridges) > 0 {
		// Snapshot bridge state once at startup. The services panel will
		// re-snapshot on its 8s auto-refresh and the explicit refresh button.
		entries := snapshotPortproxy()
		wslHostIP := config.WSLWindowsHostIP()
		bridgesInfo = make([]graph.BridgeInfo, 0, len(bf.Bridges))
		for _, b := range bf.Bridges {
			bridgesInfo = append(bridgesInfo, graph.BridgeInfo{
				Bridge: b,
				State:  b.Status(entries, wslHostIP),
			})
		}
		log.Printf("loaded %d bridge(s) from %s", len(bridgesInfo), bridgesPath)
	}

	var depRules []deps.Rule
	if df, err := deps.Load(depsPath); err != nil {
		log.Printf("warning: load dependencies %s: %v", depsPath, err)
	} else if len(df.Dependencies) > 0 {
		depRules = df.Dependencies
		log.Printf("loaded %d dependency rule(s) from %s", len(depRules), depsPath)
	}

	var containerDecls []containers.Container
	if cf, err := containers.Load(containersPath); err != nil {
		log.Printf("warning: load containers %s: %v", containersPath, err)
	} else if len(cf.Containers) > 0 {
		containerDecls = cf.Containers
		log.Printf("loaded %d container declaration(s) from %s", len(containerDecls), containersPath)
	}

	var modelRates map[string]rates.Rate
	if rf, err := rates.Load(ratesPath); err != nil {
		log.Printf("warning: load model-rates %s: %v", ratesPath, err)
	} else if len(rf.Models) > 0 {
		modelRates = rf.Models
		log.Printf("loaded %d model-rate(s) from %s", len(modelRates), ratesPath)
	}

	return bridgesInfo, depRules, bridgesPath, containerDecls, containersPath, modelRates
}

// snapshotPortproxy queries Windows for the current portproxy state. Returns
// an empty slice if powershell.exe isn't reachable — bridges then resolve as
// "missing", which is the right answer when we can't see Windows.
func snapshotPortproxy() []bridge.PortproxyEntry {
	out, err := bridge.PortproxyShow()
	if err != nil {
		return nil
	}
	return bridge.ParsePortproxyShow(out)
}

// windowsAdapterIP returns the IP assigned by Windows to the WSL vEthernet
// adapter (i.e. the resolved value of the ${wsl-host-ip} sentinel). Best-
// effort: returns "" if powershell.exe is unavailable or the query fails.
// Falls back to config.WSLWindowsHostIP() (the default-route gateway), which
// in practice is the same address.
func windowsAdapterIP() string {
	psPath := lookPSOrEmpty()
	if psPath == "" {
		return config.WSLWindowsHostIP()
	}
	cmd := exec.Command(psPath, "-NoProfile", "-NonInteractive", "-Command",
		"(Get-NetIPAddress -InterfaceAlias 'vEthernet (WSL*)' -AddressFamily IPv4 -ErrorAction SilentlyContinue | Select-Object -First 1).IPAddress")
	out, err := cmd.Output()
	if err == nil {
		ip := strings.TrimSpace(string(out))
		if ip != "" {
			return ip
		}
	}
	return config.WSLWindowsHostIP()
}

// lookPSOrEmpty returns a path to powershell.exe or "" if not found. Mirrors
// the resolution in internal/bridge but kept inline here so cmd doesn't have
// to import the bridge package's private helpers.
func lookPSOrEmpty() string {
	if p, err := exec.LookPath("powershell.exe"); err == nil {
		return p
	}
	for _, p := range []string{
		"/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe",
		"/mnt/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe",
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func runServer(args []string) {
	fs := flag.NewFlagSet("server", flag.ExitOnError)
	configPath := fs.String("config", config.DefaultPath(), "config file path")
	_ = fs.Parse(args)

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	sources, dc := discoverSources(cfg, nil)
	if len(sources) == 0 {
		fmt.Fprintln(os.Stderr, "warning: no .claude/ directories found")
	}

	// Build compose entries from config.
	compose := make([]web.ComposeEntry, 0, len(cfg.ProcessCompose))
	for _, pc := range cfg.ProcessCompose {
		tok := pc.Token
		if tok == "" && pc.TokenFile != "" {
			b, err := os.ReadFile(pc.TokenFile)
			if err != nil {
				log.Printf("warning: process_compose %q: read token_file %q: %v — instance will be queried without auth", pc.Name, pc.TokenFile, err)
			} else {
				tok = strings.TrimSpace(string(b))
			}
		}
		compose = append(compose, web.ComposeEntry{
			Name:        pc.Name,
			Endpoint:    config.ResolveEndpoint(pc.Endpoint),
			Token:       tok,
			Binary:      pc.Binary,
			ComposeFile: pc.ComposeFile,
			TokenFile:   pc.TokenFile,
		})
	}
	if len(compose) == 0 {
		log.Print("no process-compose instances configured — services view will be empty")
	}

	token, err := config.LoadOrCreateToken(cfg.TokenFile)
	if err != nil {
		log.Printf("warning: auth token unavailable (%v) — running without authentication", err)
	}
	if token == "" {
		log.Print("warning: no token_file configured — authentication is disabled")
	}

	if !isLoopback(cfg.Bind) {
		log.Printf("warning: binding to %s — service is reachable from the network; ensure auth is enabled", cfg.Bind)
	}

	bridgesInfo, depRules, bridgesPath, containerDecls, containersPath, modelRates := loadResolverInputs(*configPath, cfg)
	srv := web.New(sources, compose, token, dc, bridgesInfo, depRules, containerDecls, modelRates, cfg.TrustedNetworks)
	// Wire /api/sources/rescan: re-run discovery against the loaded config,
	// reusing the existing docker client. Returns just the slice — the server
	// merges it with the current source list by ID.
	srv.SetRediscover(func() []source.Source {
		fresh, _ := discoverSources(cfg, dc)
		return fresh
	})
	srv.SetBridgesFile(bridgesPath)
	srv.SetBridgesComposeFile(cfg.BridgesComposeFile)
	srv.SetContainersFile(containersPath)
	srv.SetLlamaServers(loadLlamaEntries(cfg.LlamaServers))
	srv.SetBuildInfo(collectBuildInfo())
	srv.SetWakeURL(cfg.WakeURL)
	if buildEpoch != "" {
		log.Printf("infra-mngmt build_epoch=%s", buildEpoch)
	} else {
		log.Print("infra-mngmt build_epoch=(unstamped — built without Makefile)")
	}
	if _, _, err := net.SplitHostPort(cfg.Bind); err != nil {
		log.Fatalf("invalid bind address %q: %v", cfg.Bind, err)
	}

	httpSrv := &http.Server{
		Addr:              cfg.Bind,
		Handler:           srv,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	log.Printf("infra-mngmt listening on http://%s", cfg.Bind)

	// Warm the entity cache in the background so the first request isn't cold.
	// A cold scan spins up a Docker volume sidecar per volume (multi-second);
	// warming up front turns that first page load from seconds into the warm
	// ~milliseconds. Non-blocking so it never delays accepting connections.
	go srv.WarmEntityCache()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(sctx)
	}()

	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

// loadLlamaEntries flattens config.LlamaServer into web.LlamaEntry, resolving
// the wsl-windows endpoint sentinel and reading APIKeyFile lazily. Same shape
// as the ProcessCompose loop above — kept separate so a malformed key file
// fails loudly per-entry instead of dropping the whole list.
func loadLlamaEntries(in []config.LlamaServer) []web.LlamaEntry {
	if len(in) == 0 {
		return nil
	}
	out := make([]web.LlamaEntry, 0, len(in))
	for _, ls := range in {
		key := ls.APIKey
		if key == "" && ls.APIKeyFile != "" {
			b, err := os.ReadFile(ls.APIKeyFile)
			if err != nil {
				log.Printf("warning: llama_servers %q/%q: read api_key_file %q: %v — probes will be unauthenticated", ls.Instance, ls.Process, ls.APIKeyFile, err)
			} else {
				key = strings.TrimSpace(string(b))
			}
		}
		out = append(out, web.LlamaEntry{
			Instance: ls.Instance,
			Process:  ls.Process,
			Endpoint: config.ResolveEndpoint(ls.Endpoint),
			APIKey:   key,
		})
	}
	return out
}

func isLoopback(addr string) bool {
	// addr is "host:port"; loopback = 127.x or ::1 or localhost
	host := addr
	if h, _, err := net.SplitHostPort(addr); err == nil {
		host = h
	}
	return host == "localhost" || host == "127.0.0.1" || host == "::1" ||
		strings.HasPrefix(host, "127.")
}

// configDirNames lists the per-tool config directories that infra-mngmt
// scans for. Each entry is treated as `<root>/<name>` for project-scope
// discovery and `~/<name>` for global scope. Add a new tool by appending
// here (and to docker.configMountDirs for in-container mount detection).
var configDirNames = []string{".claude", ".opencode"}

// discoverSources scans the host filesystem and Docker for known config
// directories and returns one Source per discovery. existingDC is reused
// when non-nil so a /api/sources/rescan can re-run discovery without
// leaking a new docker.Client per call. Pass nil at startup to get a
// freshly initialized client.
func discoverSources(cfg *config.Config, existingDC *docker.Client) ([]source.Source, *docker.Client) {
	var sources []source.Source
	seen := map[string]bool{}

	addHostFS := func(claudeDir string, scope entity.Scope) {
		if seen[claudeDir] {
			return
		}
		if stat, err := os.Stat(claudeDir); err != nil || !stat.IsDir() {
			return
		}
		seen[claudeDir] = true
		sources = append(sources, source.NewHostFS(claudeDir, scope))
		log.Printf("source [hostfs] %s (%s)", claudeDir, scope.Label())
	}

	addAllConfigDirs := func(root string, scope entity.Scope) {
		for _, name := range configDirNames {
			addHostFS(filepath.Join(root, name), scope)
		}
	}

	home, _ := os.UserHomeDir()
	addAllConfigDirs(home, entity.GlobalScope())

	if cwd, err := os.Getwd(); err == nil {
		addAllConfigDirs(cwd, entity.ProjectScope(cwd))
	}

	for _, p := range cfg.ExtraPaths {
		abs, err := filepath.Abs(p)
		if err != nil {
			log.Printf("warning: invalid extra_path %q: %v", p, err)
			continue
		}
		addAllConfigDirs(abs, entity.ProjectScope(abs))
	}

	var dc *docker.Client
	dockerSources, projectRoots, dockerClient, err := discoverDockerSources(existingDC)
	if err != nil {
		log.Printf("docker: skipped (%v)", err)
	} else {
		dc = dockerClient
		for _, ds := range dockerSources {
			if !seen[ds.ID()] {
				seen[ds.ID()] = true
				sources = append(sources, ds)
			}
		}
		// For each project root known from devcontainer labels, also try the
		// host filesystem — covers bind-mount setups and projects that had their
		// devcontainer recreated with a fresh volume.
		for _, root := range projectRoots {
			addAllConfigDirs(root, entity.ProjectScope(root))
		}
		// Scan the workspace directories (parents of known project roots) so
		// that projects not currently open in a devcontainer are also visible.
		for _, wsDir := range workspaceDirs(projectRoots) {
			entries, err := os.ReadDir(wsDir)
			if err != nil {
				continue
			}
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				root := filepath.Join(wsDir, entry.Name())
				addAllConfigDirs(root, entity.ProjectScope(root))
			}
		}
	}

	return sources, dc
}

func discoverDockerSources(existingDC *docker.Client) ([]source.Source, []string, *docker.Client, error) {
	dc := existingDC
	if dc == nil {
		newDC, err := docker.New()
		if err != nil {
			return nil, nil, nil, err
		}
		dc = newDC
	}

	ctx := context.Background()
	containers, err := dc.ListManaged(ctx)
	if err != nil {
		// Only close the client we created ourselves — never the caller's.
		if existingDC == nil {
			_ = dc.Close()
		}
		return nil, nil, nil, fmt.Errorf("list managed containers: %w", err)
	}

	var out []source.Source
	var projectRoots []string
	for _, ctr := range containers {
		if ctr.ProjectRoot != "" {
			projectRoots = append(projectRoots, ctr.ProjectRoot)
		}
		if ctr.ConfigMount == nil {
			log.Printf("docker: container %s has no .claude mount, skipping", ctr.Name)
			continue
		}
		var scope entity.Scope
		if ctr.ProjectRoot != "" {
			scope = entity.ProjectScope(ctr.ProjectRoot)
		} else {
			scope = entity.GlobalScope()
		}
		if ctr.ConfigMount.IsVolume {
			vol := source.NewDockerVolume(ctr.ConfigMount.VolumeName, scope, dc)
			out = append(out, vol)
			log.Printf("source [docker-vol] %s (%s)", ctr.ConfigMount.VolumeName, scope.Label())
		} else {
			fs := source.NewHostFS(ctr.ConfigMount.HostPath, scope)
			out = append(out, fs)
			log.Printf("source [hostfs/docker] %s (%s)", ctr.ConfigMount.HostPath, scope.Label())
		}
	}
	return out, projectRoots, dc, nil
}

// workspaceDirs returns the unique parent directories of the given project
// roots. These are the workspace directories that likely contain other
// sibling projects worth scanning.
func workspaceDirs(projectRoots []string) []string {
	seen := map[string]bool{}
	var dirs []string
	for _, root := range projectRoots {
		parent := filepath.Dir(root)
		if parent == "." || parent == "/" || seen[parent] {
			continue
		}
		seen[parent] = true
		dirs = append(dirs, parent)
	}
	return dirs
}
