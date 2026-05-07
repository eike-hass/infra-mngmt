package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/eike-hass/infra-mngmt/config"
	"github.com/eike-hass/infra-mngmt/internal/bridge"
	"github.com/eike-hass/infra-mngmt/internal/containers"
	"github.com/eike-hass/infra-mngmt/internal/deps"
	"github.com/eike-hass/infra-mngmt/internal/docker"
	"github.com/eike-hass/infra-mngmt/internal/entity"
	"github.com/eike-hass/infra-mngmt/internal/graph"
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
  apply [name...]            (re)apply bridges (UAC prompt; all if no names)
  reset [name...]            remove bridges (UAC prompt; all if no names)
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
		fmt.Fprintln(os.Stderr, "usage: infra-mngmt bridges <list|status|apply|reset> [args...]")
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
		runBridgesApply(file, rest)
	case "reset":
		runBridgesReset(file, rest)
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

func runBridgesApply(f *bridge.File, names []string) {
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
	// WSL bridges currently print a hint (we don't manage long-running socat
	// processes from the bridge applier — use a process-compose entry).
	var winBridges []bridge.Bridge
	var wslBridges []bridge.Bridge
	for _, b := range selected {
		switch b.Tier {
		case bridge.TierWindows:
			winBridges = append(winBridges, b)
		case bridge.TierWSL:
			wslBridges = append(wslBridges, b)
		}
	}

	if len(winBridges) > 0 {
		script := bridge.PowerShellApply(winBridges)
		fmt.Printf("Applying %d Windows bridge(s) (UAC prompt may appear)...\n", len(winBridges))
		if err := bridge.RunPowerShellElevated(script); err != nil {
			fmt.Fprintf(os.Stderr, "apply: %v\n", err)
			os.Exit(1)
		}
	}
	for _, b := range wslBridges {
		fmt.Printf("WSL bridge %q: long-running socat relays should run as a process-compose entry, not via apply.\n", b.Name)
		fmt.Printf("  suggested command: %s\n", bridge.SocatCommand(&b, ""))
	}

	// Reverify and print updated status.
	runBridgesStatus(f)
}

func runBridgesReset(f *bridge.File, names []string) {
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
	runBridgesStatus(f)
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
func loadResolverInputs(configPath string, cfg *config.Config) ([]graph.BridgeInfo, []deps.Rule, string, []containers.Container, string) {
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

	return bridgesInfo, depRules, bridgesPath, containerDecls, containersPath
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

	sources, dc := discoverSources(cfg)
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

	bridgesInfo, depRules, bridgesPath, containerDecls, containersPath := loadResolverInputs(*configPath, cfg)
	srv := web.New(sources, compose, token, dc, bridgesInfo, depRules, containerDecls, cfg.TrustedNetworks)
	srv.SetBridgesFile(bridgesPath)
	srv.SetContainersFile(containersPath)
	srv.SetBuildInfo(collectBuildInfo())
	if buildEpoch != "" {
		log.Printf("infra-mngmt build_epoch=%s", buildEpoch)
	} else {
		log.Print("infra-mngmt build_epoch=(unstamped — built without Makefile)")
	}
	log.Printf("infra-mngmt listening on http://%s", cfg.Bind)
	if err := http.ListenAndServe(cfg.Bind, srv); err != nil {
		log.Fatal(err)
	}
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

func discoverSources(cfg *config.Config) ([]source.Source, *docker.Client) {
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

	home, _ := os.UserHomeDir()
	addHostFS(filepath.Join(home, ".claude"), entity.GlobalScope())

	if cwd, err := os.Getwd(); err == nil {
		addHostFS(filepath.Join(cwd, ".claude"), entity.ProjectScope(cwd))
	}

	for _, p := range cfg.ExtraPaths {
		abs, err := filepath.Abs(p)
		if err != nil {
			log.Printf("warning: invalid extra_path %q: %v", p, err)
			continue
		}
		addHostFS(filepath.Join(abs, ".claude"), entity.ProjectScope(abs))
	}

	var dc *docker.Client
	dockerSources, projectRoots, dockerClient, err := discoverDockerSources()
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
			addHostFS(filepath.Join(root, ".claude"), entity.ProjectScope(root))
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
				addHostFS(filepath.Join(root, ".claude"), entity.ProjectScope(root))
			}
		}
	}

	return sources, dc
}

func discoverDockerSources() ([]source.Source, []string, *docker.Client, error) {
	dc, err := docker.New()
	if err != nil {
		return nil, nil, nil, err
	}

	ctx := context.Background()
	containers, err := dc.ListManaged(ctx)
	if err != nil {
		_ = dc.Close()
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
