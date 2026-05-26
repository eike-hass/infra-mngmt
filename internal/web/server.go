package web

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"log"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path/filepath"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/eike-hass/infra-mngmt/internal/compose"
	"github.com/eike-hass/infra-mngmt/internal/containers"
	"github.com/eike-hass/infra-mngmt/internal/deps"
	"github.com/eike-hass/infra-mngmt/internal/docker"
	"github.com/eike-hass/infra-mngmt/internal/graph"
	"github.com/eike-hass/infra-mngmt/internal/llama"
	"github.com/eike-hass/infra-mngmt/internal/rates"
	"github.com/eike-hass/infra-mngmt/internal/source"
)

// LlamaEntry is a flat struct mirroring config.LlamaServer, kept here so the
// web layer doesn't import config. The (Instance, Process) pair is matched
// against process-compose process rows to decide whether to render the
// "llama" stats button on a row.
type LlamaEntry struct {
	Instance string
	Process  string
	Endpoint string
	APIKey   string
}

// BuildInfo describes the binary's identity. Set once at startup via
// SetBuildInfo and surfaced through /api/version so external clients (deploy
// scripts, smoke tests, the UI footer) can confirm the running binary matches
// what was built.
type BuildInfo struct {
	BuildEpoch string `json:"build_epoch"` // unix seconds; empty when unstamped
	Commit     string `json:"commit"`      // short git SHA from runtime/debug.ReadBuildInfo
	Dirty      bool   `json:"dirty"`       // working tree had uncommitted changes at build
	VCSTime    string `json:"vcs_time"`    // commit timestamp from VCS
	GoVersion  string `json:"go_version"`
}

// RediscoverFn returns a fresh slice of sources from the configured discovery
// path (host fs walk + docker container scan). The server uses it to satisfy
// /api/sources/rescan: the returned slice is merged with the current source
// list by ID, so existing sources keep their cached entity IDs while newly
// discovered ones become visible without a process restart.
type RediscoverFn func() []source.Source

type Server struct {
	sources            []source.Source
	sourcesMu          sync.RWMutex // guards sources during /api/sources/rescan
	rediscover         RediscoverFn // optional; nil → /api/sources/rescan returns 503
	compose            []*compose.Client
	booters            map[string]*compose.Bootstrapper // keyed by client name
	bridges            []graph.BridgeInfo               // bridges.yaml entries with observed state; mutated under bridgesMu
	bridgesMu          sync.RWMutex
	bridgesFile        string                 // path to bridges.yaml so /bridges/refresh can reload
	bridgesComposeFile string                 // path to process-compose.bridges.yaml for tier=wsl apply
	composeFiles       map[string]string      // compose-instance name → its compose_file path; used to find the WSL PC
	containerDecls     []containers.Container // containers.yaml entries
	containersFile     string                 // path to containers.yaml so /containers/refresh can reload
	depRules           []deps.Rule            // dependencies.yaml rules
	modelRates         map[string]rates.Rate  // model-rates.yaml; nil/empty → OD card renders "—" for every cost
	docker             *docker.Client         // nil if Docker unavailable
	llamaServers       []LlamaEntry           // declared in config.yaml; lookup keyed by (Instance,Process)
	llamaClients       map[string]*llama.Client
	mux                *chi.Mux
	token              string         // required bearer token; empty = auth disabled
	trustedNetworks    []netip.Prefix // CIDRs whose source IPs bypass auth
	sessions           sync.Map       // session ID (string) → struct{}
	entityCache        entityCacheEntry
	buildInfo          BuildInfo // populated via SetBuildInfo; surfaced at /api/version
	wakeURL            string    // populated via SetWakeURL; rendered into the page so JS can wake WSL via the Windows-side wake-proxy

	// vaultFactory builds a VaultClient for a given control-plane URL.
	// Tests inject a fake; production leaves nil and gets the default
	// (a real HTTP client).
	vaultFactory VaultClientFactory
}

func New(sources []source.Source, composeCfg []ComposeEntry, token string, dc *docker.Client, bridges []graph.BridgeInfo, depRules []deps.Rule, containerDecls []containers.Container, modelRates map[string]rates.Rate, trustedCIDRs []string) *Server {
	s := &Server{
		sources:         sources,
		booters:         map[string]*compose.Bootstrapper{},
		composeFiles:    map[string]string{},
		token:           token,
		docker:          dc,
		bridges:         bridges,
		containerDecls:  containerDecls,
		depRules:        depRules,
		modelRates:      modelRates,
		trustedNetworks: parseTrustedCIDRs(trustedCIDRs),
	}
	for _, e := range composeCfg {
		s.compose = append(s.compose, compose.New(e.Name, e.Endpoint, e.Token))
		if e.ComposeFile != "" {
			s.composeFiles[e.Name] = e.ComposeFile
		}
		if e.Binary != "" && e.ComposeFile != "" {
			s.booters[e.Name] = &compose.Bootstrapper{
				Name:        e.Name,
				Binary:      e.Binary,
				ComposeFile: e.ComposeFile,
				Endpoint:    e.Endpoint,
				TokenFile:   e.TokenFile,
			}
		}
	}
	// Inject the bridges fragment as an extra `-f` for the bootstrapper of the
	// WSL PC instance — keeps the user's main YAML untouched while still
	// loading the generated socat relays. We can't do this in the loop above
	// because the bridges fragment path is set later via SetBridgesComposeFile.
	// See attachBridgesFragmentToBootstrap, called from that setter.

	s.mux = chi.NewRouter()
	s.mux.Use(middleware.Logger)
	s.mux.Use(middleware.Recoverer)
	s.mux.Use(s.securityHeadersMiddleware)

	// Public routes — no auth required.
	s.mux.Get("/login", s.handleLoginGet)
	s.mux.Post("/login", s.handleLoginPost)
	s.mux.Post("/logout", s.handleLogout)
	s.mux.Get("/favicon.svg", handleFavicon)
	// /static/* serves vendored JS, CSS, and other embedded assets.
	// No auth required — assets are public-equivalent (vendored libraries,
	// non-sensitive page-scoped scripts/styles).
	s.mux.Mount("/static", staticHandler())
	// /api/version is public so deploy scripts and external smoke tests can
	// confirm a restart picked up the new binary without holding a session
	// cookie. The exposed fields (epoch, short SHA, dirty bit, go version)
	// are not security-sensitive — they're already in the server log line.
	s.mux.Get("/api/version", s.handleVersion)
	// /sw.js is public (browsers fetch service workers without page context)
	// and served at the root path so its scope covers everything. The shell
	// it caches is harmless (markup, no privileged data); fresh visits still
	// hit handlers under auth as normal.
	s.mux.Get("/sw.js", s.handleServiceWorker)

	// All other routes require authentication (when a token is configured).
	s.mux.Group(func(r chi.Router) {
		r.Use(s.authMiddleware)

		// entity routes
		r.Get("/", s.handleIndex)
		r.Get("/api/sources", s.handleSources)
		r.Get("/api/entities", s.handleEntities)
		r.Get("/api/entity/content", s.handleEntityContent)
		r.Post("/api/entity", s.handleEntityWrite)
		r.Get("/partials/entity", s.handleEntityPreview)
		r.Get("/partials/entity-list", s.handleEntityListPartial)
		r.Get("/partials/promote-picker", s.handlePromotePicker)
		r.Get("/partials/promote-clear", s.handlePromoteClear)
		r.Post("/api/promote", s.handlePromote)
		r.Post("/api/sources/rescan", s.handleSourcesRescan)

		// services routes
		r.Get("/partials/services", s.handleServicesPartial)
		r.Post("/process/start", s.handleProcessStart) // ?instance=&process=
		r.Post("/process/stop", s.handleProcessStop)   // ?instance=&process=
		r.Post("/process/restart", s.handleProcessRestart)
		r.Post("/compose/start", s.handleComposeStart)   // ?instance=  — bootstrap
		r.Post("/compose/reload", s.handleComposeReload) // ?instance=  — re-read YAML, reconcile
		// bridge routes
		r.Post("/bridge/apply", s.handleBridgeApply) // ?name=  — bring active (UAC if windows missing/drifted)
		r.Post("/bridge/pause", s.handleBridgePause) // ?name=  — pause WSL relay; no UAC, leaves netsh in place
		r.Post("/bridge/reset", s.handleBridgeReset) // ?name=  — full removal incl. netsh (UAC for windows)
		r.Post("/bridges/apply", s.handleBridgesApplyAll)
		r.Post("/bridges/refresh", s.handleBridgesRefresh)
		// container routes (declared via containers.yaml; distinct from
		// devcontainer-discovery routes under /api/container/*)
		r.Post("/decl-container/start", s.handleContainerStartByName) // ?name=
		r.Post("/decl-container/stop", s.handleContainerStopByName)   // ?name=
		r.Post("/containers/refresh", s.handleContainersRefresh)

		// Vault (mcp-fs) panel: lazy-loaded into the container card via
		// HTMX. ?name= is the container declaration name; ?at= is the
		// optional tree path (defaults to the vault's data root).
		r.Get("/partials/vault/panel", s.handleVaultPanel)
		r.Get("/partials/vault/tree", s.handleVaultTree)
		r.Post("/api/vault/allow", s.handleVaultAllow)       // ?name=&path=
		r.Post("/api/vault/disallow", s.handleVaultDisallow) // ?name=&path=

		// Open Design (open-design) inline token-stats summary, lazy-loaded
		// from the OD card via HTMX. ?name= is the docker container name of
		// the token-stats service (resolved against declarations to find the
		// configured usage_url).
		r.Get("/partials/open-design/token-stats", s.handleOpenDesignTokenStats)
		// OD daemon version badge, lazy-loaded from the OD card meta row.
		// ?name= is the OD project name (containers.yaml entry).
		r.Get("/partials/open-design/version", s.handleOpenDesignVersion)
		r.Get("/partials/logs", s.handleProcessLogs)     // ?instance=&process=
		r.Get("/partials/llama", s.handleLlamaAll)       // standalone "llama" view body
		r.Post("/api/llama/drain", s.handleLlamaDrain)   // ?instance=&process=  destructive: cancel all in-flight + queued tasks
		r.Post("/api/llama/load", s.handleLlamaLoad)     // ?instance=&process=&model=  router-mode: load preset (LRU may evict another)
		r.Post("/api/llama/unload", s.handleLlamaUnload) // ?instance=&process=&model=  router-mode: evict preset, frees VRAM
		r.Post("/api/refresh", s.handleRefresh)
		r.Get("/partials/container-controls", s.handleContainerControls)
		r.Get("/api/containers", s.handleContainers)
		r.Post("/api/container/start", s.handleContainerStart)
		r.Post("/api/container/stop", s.handleContainerStop)
		r.Post("/api/container/devcontainer-up", s.handleContainerDevcontainerUp)
		r.Post("/api/container/open-vscode", s.handleContainerOpenVSCode)
		r.Get("/api/container/events-stream", s.handleContainerEventsStream)
	})

	return s
}

// ComposeEntry is a flat struct passed from main to avoid importing config.
type ComposeEntry struct {
	Name, Endpoint, Token, Binary, ComposeFile, TokenFile string
}

// SetBridgesFile records the path to bridges.yaml so the /bridges/refresh
// route can reload it on demand without a server restart.
func (s *Server) SetBridgesFile(path string) {
	s.bridgesFile = path
}

// SetRediscover wires the source-discovery callback used by
// /api/sources/rescan. main.go passes a closure capturing the loaded config
// so the web layer doesn't need to import config.
func (s *Server) SetRediscover(fn RediscoverFn) {
	s.rediscover = fn
}

// allSources returns the current source slice as a snapshot. Callers iterate
// the returned slice without holding the lock — the slice header is captured,
// and rescan only ever replaces the slice (it never mutates the underlying
// array in place), so a reader's snapshot stays consistent for its lifetime.
func (s *Server) allSources() []source.Source {
	s.sourcesMu.RLock()
	defer s.sourcesMu.RUnlock()
	return s.sources
}

// SetBridgesComposeFile records the path to the generated process-compose
// fragment that runs tier=wsl, type=socat bridges. /bridge/apply uses this
// to materialize new socat relays without dropping to the CLI. Also propagates
// the path into the matching PC instance's bootstrap config so a /compose/start
// loads the fragment alongside the user's main YAML.
func (s *Server) SetBridgesComposeFile(path string) {
	s.bridgesComposeFile = path
	if path == "" {
		return
	}
	wantDir := filepath.Dir(path)
	for name, cfPath := range s.composeFiles {
		if filepath.Dir(cfPath) != wantDir {
			continue
		}
		if b, ok := s.booters[name]; ok {
			b.ExtraFiles = append([]string(nil), path)
		}
	}
}

// SetContainersFile records the path to containers.yaml for hot-reload.
func (s *Server) SetContainersFile(path string) {
	s.containersFile = path
}

// SetLlamaServers wires the configured llama-server endpoints. The web layer
// keeps both the flat declaration (so templates can decide which rows show
// the "llama" button) and a per-entry probe Client cache (so each refresh
// doesn't allocate a new http.Client).
func (s *Server) SetLlamaServers(entries []LlamaEntry) {
	s.llamaServers = entries
	clients := make(map[string]*llama.Client, len(entries))
	for _, e := range entries {
		clients[llamaKey(e.Instance, e.Process)] = llama.New(e.Endpoint, e.APIKey)
	}
	s.llamaClients = clients
}

// llamaKey is the lookup key for a llama-server entry. Instance+Process is
// already validated by validProcessName at call sites, so the `|` separator
// is unambiguous.
func llamaKey(instance, process string) string { return instance + "|" + process }

// findComposeClient returns the *compose.Client for the named instance, or
// nil if no such client is configured. Used by the llama view to tail logs
// for the underlying process from the same compose instance that supervises
// it.
func (s *Server) findComposeClient(instance string) *compose.Client {
	for _, c := range s.compose {
		if c.Name() == instance {
			return c
		}
	}
	return nil
}

// SetWakeURL wires the browser-facing wake-proxy URL into the server. When
// non-empty, handlers include it in pageData so the index template can render
// the wake status pill and the wake.js island can POST to it on backend
// failure. Empty disables the feature end-to-end (no pill, no SW registration).
// The URL's origin is also added to the CSP's connect-src so the cross-origin
// fetch from page JS isn't blocked — see contentSecurityPolicy().
func (s *Server) SetWakeURL(u string) {
	s.wakeURL = u
}

// SetBuildInfo wires the binary's build identity into the server. Surfaced
// at /api/version (no auth) and rendered as a footer so deploys can be
// verified by hitting either endpoint without parsing systemd logs.
func (s *Server) SetBuildInfo(b BuildInfo) {
	s.buildInfo = b
	// Cache-buster token for {{static}} URLs. Commit is preferred (stable
	// across rebuilds of the same source); fall back to build epoch for
	// unstamped builds so we still bust the cache on each rebuild.
	bust := b.Commit
	if bust == "" {
		bust = b.BuildEpoch
	}
	setAssetCacheBuster(bust)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// ── auth middleware ───────────────────────────────────────────────────────────

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.token == "" {
			next.ServeHTTP(w, r)
			return
		}
		if s.requestIsTrusted(r) {
			next.ServeHTTP(w, r)
			return
		}
		if c, err := r.Cookie("im_session"); err == nil {
			if _, ok := s.sessions.Load(c.Value); ok {
				next.ServeHTTP(w, r)
				return
			}
		}
		target := "/login?next=" + url.QueryEscape(r.URL.RequestURI())
		http.Redirect(w, r, target, http.StatusSeeOther)
	})
}

// requestIsTrusted reports whether r's source IP falls in one of the
// configured trustedNetworks. Used to bypass auth for local clients.
func (s *Server) requestIsTrusted(r *http.Request) bool {
	if len(s.trustedNetworks) == 0 {
		return false
	}
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		host = h
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	for _, p := range s.trustedNetworks {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// parseTrustedCIDRs converts string CIDRs to netip.Prefix, dropping invalid
// entries with a log warning. Empty input returns nil.
func parseTrustedCIDRs(cidrs []string) []netip.Prefix {
	if len(cidrs) == 0 {
		return nil
	}
	out := make([]netip.Prefix, 0, len(cidrs))
	for _, c := range cidrs {
		p, err := netip.ParsePrefix(strings.TrimSpace(c))
		if err != nil {
			log.Printf("warning: trusted_networks %q: %v — skipping", c, err)
			continue
		}
		out = append(out, p)
	}
	return out
}

func (s *Server) handleLoginGet(w http.ResponseWriter, r *http.Request) {
	// Already logged in — skip straight to the app.
	if s.token == "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if c, err := r.Cookie("im_session"); err == nil {
		if _, ok := s.sessions.Load(c.Value); ok {
			next := r.URL.Query().Get("next")
			if next == "" || !strings.HasPrefix(next, "/") {
				next = "/"
			}
			http.Redirect(w, r, next, http.StatusSeeOther)
			return
		}
	}
	tmpl := parseTemplate("login", "templates/login.html.tmpl")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = tmpl.Execute(w, map[string]string{"Next": r.URL.Query().Get("next"), "Error": ""})
}

func (s *Server) handleLoginPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	submitted := r.FormValue("token")
	if subtle.ConstantTimeCompare([]byte(submitted), []byte(s.token)) != 1 {
		tmpl := parseTemplate("login", "templates/login.html.tmpl")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		_ = tmpl.Execute(w, map[string]string{"Next": r.FormValue("next"), "Error": "invalid token"})
		return
	}
	sid := make([]byte, 16)
	rand.Read(sid)
	sessionID := hex.EncodeToString(sid)
	s.sessions.Store(sessionID, struct{}{})
	http.SetCookie(w, &http.Cookie{
		Name:     "im_session",
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	next := r.FormValue("next")
	if next == "" || !strings.HasPrefix(next, "/") {
		next = "/"
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// faviconSVG is served at /favicon.svg — dual interlocking hexagons (host + container) on a transparent background.
const faviconSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32">
  <polygon points="9,3 18,3 22.5,10.5 18,18 9,18 4.5,10.5" fill="oklch(68% 0.18 200)" opacity="0.95"/>
  <polygon points="14,14 23,14 27.5,21.5 23,29 14,29 9.5,21.5" fill="none" stroke="oklch(68% 0.18 200)" stroke-width="1.5" opacity="0.55"/>
</svg>`

func handleFavicon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write([]byte(faviconSVG))
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("im_session"); err == nil {
		s.sessions.Delete(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: "im_session", Value: "", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
