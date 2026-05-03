package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/eike-hass/infra-mngmt/config"
	"github.com/eike-hass/infra-mngmt/internal/docker"
	"github.com/eike-hass/infra-mngmt/internal/entity"
	"github.com/eike-hass/infra-mngmt/internal/source"
	"github.com/eike-hass/infra-mngmt/internal/web"
)

func main() {
	configPath := flag.String("config", config.DefaultPath(), "config file path")
	flag.Parse()

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

	srv := web.New(sources, compose, token, dc)
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
