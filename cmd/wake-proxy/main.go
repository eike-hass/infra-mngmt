// wake-proxy is a single-purpose reverse proxy in front of Windows-side
// process-compose. It exists so browser JS on a different origin can trigger
// specific PC actions (e.g. "start wsl-wake") without holding the PC token
// in the page.
//
// Why this exists rather than calling PC directly: PC requires the custom
// header X-PC-Token-Key for auth and ships no CORS support, so any
// cross-origin fetch that needs to send that header trips a preflight that
// PC's router 404s. Stripping the token from PC is not safe either — PC's
// POST /process endpoint accepts a full ProcessConfig (including command),
// so an unauth attacker reachable via DNS rebinding can rewrite a process's
// command and start it → local code execution.
//
// This proxy:
//   - listens loopback-only
//   - validates Host and Origin (DNS-rebinding defense)
//   - enforces a fixed allowlist of upstream method+path tuples (so even if
//     compromised it cannot reach UpdateProcess)
//   - injects the X-PC-Token-Key from disk
//   - serves CORS so the browser fetch can read the response
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

type route struct {
	method string
	path   string
}

type proxy struct {
	upstream       *url.URL
	tokenPath      string
	allowedOrigins map[string]struct{}
	allowedRoutes  []route
	allowedHosts   map[string]struct{}
	client         *http.Client
}

func main() {
	listen := flag.String("listen", "127.0.0.1:9920", "address to bind (loopback only)")
	upstreamRaw := flag.String("upstream", "http://127.0.0.1:9919", "upstream process-compose URL")
	tokenFile := flag.String("token-file", "", "path to file containing the X-PC-Token-Key value; re-read per request")
	allowedOrigin := flag.String("allowed-origin", "", "comma-separated allowed Origin headers, e.g. http://localhost:7842")
	allowedRouteFlag := flag.String("allowed-route", "", "semicolon-separated METHOD,PATH entries, e.g. 'POST,/process/start/wsl-wake'")
	flag.Parse()

	cfg, err := loadConfig(*listen, *upstreamRaw, *tokenFile, *allowedOrigin, *allowedRouteFlag)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	srv := &http.Server{
		Addr:              *listen,
		Handler:           cfg,
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("wake-proxy listen=%s upstream=%s routes=%d", *listen, cfg.upstream, len(cfg.allowedRoutes))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("listen: %v", err)
	}
}

func loadConfig(listen, upstreamRaw, tokenFile, allowedOrigin, allowedRouteFlag string) (*proxy, error) {
	upstream, err := url.Parse(upstreamRaw)
	if err != nil || upstream.Scheme == "" || upstream.Host == "" {
		return nil, fmt.Errorf("invalid upstream URL %q", upstreamRaw)
	}

	routes, err := parseRoutes(allowedRouteFlag)
	if err != nil {
		return nil, err
	}
	if len(routes) == 0 {
		return nil, errors.New("at least one --allowed-route required")
	}

	origins := parseList(allowedOrigin)
	if len(origins) == 0 {
		return nil, errors.New("at least one --allowed-origin required")
	}
	originSet := make(map[string]struct{}, len(origins))
	for _, o := range origins {
		originSet[o] = struct{}{}
	}

	// Host allowlist derived from the listen port — the only Host headers
	// we accept are ones that name our actual listener. Rejects DNS-rebinding
	// where the attacker's Host would be the rebinding hostname.
	_, port, err := net.SplitHostPort(listen)
	if err != nil {
		return nil, fmt.Errorf("invalid --listen %q: %w", listen, err)
	}
	hostSet := map[string]struct{}{
		"127.0.0.1:" + port: {},
		"localhost:" + port: {},
	}

	if tokenFile != "" {
		if _, err := os.ReadFile(tokenFile); err != nil {
			return nil, fmt.Errorf("cannot read --token-file: %w", err)
		}
	}

	return &proxy{
		upstream:       upstream,
		tokenPath:      tokenFile,
		allowedOrigins: originSet,
		allowedRoutes:  routes,
		allowedHosts:   hostSet,
		client:         &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func parseRoutes(s string) ([]route, error) {
	parts := strings.Split(s, ";")
	out := make([]route, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		mp := strings.SplitN(p, ",", 2)
		if len(mp) != 2 {
			return nil, fmt.Errorf("expected METHOD,PATH, got %q", p)
		}
		method := strings.ToUpper(strings.TrimSpace(mp[0]))
		path := strings.TrimSpace(mp[1])
		if method == "" || !strings.HasPrefix(path, "/") {
			return nil, fmt.Errorf("invalid route %q", p)
		}
		out = append(out, route{method: method, path: path})
	}
	return out, nil
}

func parseList(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (p *proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// One-line log per request: timestamp + method/path + Host + Origin +
	// decision + duration. Lets the operator answer "did a wake fire?",
	// "why was it rejected?", and "did upstream succeed?" from the wake-proxy
	// log alone (which process-compose surfaces via the services panel).
	start := time.Now()
	decision := "denied:unknown"
	defer func() {
		log.Printf("%s %s host=%q origin=%q decision=%s (%dms)",
			r.Method, r.URL.Path, r.Host, r.Header.Get("Origin"),
			decision, time.Since(start).Milliseconds())
	}()

	// DNS-rebinding defense: require Host and Origin to match our allowlists.
	// Both are needed — Host alone misses cases where the attacker proxies
	// through their own server; Origin alone misses non-browser clients that
	// don't send it. Together: anything cross-origin from outside the
	// allowed list is rejected.
	if _, ok := p.allowedHosts[r.Host]; !ok {
		decision = "denied:bad-host"
		http.Error(w, "host not allowed", http.StatusForbidden)
		return
	}
	origin := r.Header.Get("Origin")
	if _, ok := p.allowedOrigins[origin]; !ok {
		decision = "denied:bad-origin"
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}

	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", routeMethodsCSV(p.allowedRoutes))
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Max-Age", "600")
		decision = "preflight-ok"
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if !routeAllowed(p.allowedRoutes, r.Method, r.URL.Path) {
		decision = "denied:bad-route"
		http.Error(w, "route not allowed", http.StatusForbidden)
		return
	}

	// Re-read per request so the operator can rotate the token without
	// restarting the proxy. The file is small (a single line); cost is nil.
	var token string
	if p.tokenPath != "" {
		b, err := os.ReadFile(p.tokenPath)
		if err != nil {
			decision = "error:token-read"
			http.Error(w, "token read failed", http.StatusInternalServerError)
			return
		}
		token = strings.TrimSpace(string(b))
	}

	target := *p.upstream
	target.Path = r.URL.Path
	target.RawQuery = r.URL.RawQuery

	req, err := http.NewRequestWithContext(r.Context(), r.Method, target.String(), r.Body)
	if err != nil {
		decision = "error:build-request"
		http.Error(w, "build upstream request: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if ct := r.Header.Get("Content-Type"); ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	if token != "" {
		req.Header.Set("X-PC-Token-Key", token)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		decision = "error:upstream-unreachable"
		http.Error(w, "upstream call failed", http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	decision = fmt.Sprintf("forwarded:%d", resp.StatusCode)
	w.Header().Set("Access-Control-Allow-Origin", origin)
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func routeMethodsCSV(rs []route) string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		if _, ok := seen[r.method]; ok {
			continue
		}
		seen[r.method] = struct{}{}
		out = append(out, r.method)
	}
	return strings.Join(out, ", ")
}

func routeAllowed(rs []route, method, path string) bool {
	for _, r := range rs {
		if r.method == method && r.path == path {
			return true
		}
	}
	return false
}
