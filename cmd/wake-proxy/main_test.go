package main

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureLogs redirects the default logger to an in-memory buffer for the
// duration of t. Used to assert the per-request decision log line, which is
// the operator's primary signal that a wake fired and what happened.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	return &buf
}

const (
	testListen  = "127.0.0.1:9920"
	testOrigin  = "http://localhost:7842"
	testRoute   = "POST,/process/start/wsl-wake"
	testPath    = "/process/start/wsl-wake"
	testHostOK  = "127.0.0.1:9920"
	testHostAlt = "localhost:9920"
)

// newTestProxy returns a proxy wired against the given upstream server. tokenBody,
// when non-empty, is written to a temp token file and passed via --token-file.
func newTestProxy(t *testing.T, upstreamURL string, tokenBody string) *proxy {
	t.Helper()
	var tokenPath string
	if tokenBody != "" {
		tokenPath = filepath.Join(t.TempDir(), "token")
		if err := os.WriteFile(tokenPath, []byte(tokenBody), 0o600); err != nil {
			t.Fatalf("write token: %v", err)
		}
	}
	p, err := loadConfig(testListen, upstreamURL, tokenPath, testOrigin, testRoute)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	return p
}

func doRequest(t *testing.T, p *proxy, method, path, host, origin, body string) *httptest.ResponseRecorder {
	t.Helper()
	var br io.Reader
	if body != "" {
		br = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, "http://"+host+path, br)
	req.Host = host
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rr := httptest.NewRecorder()
	p.ServeHTTP(rr, req)
	return rr
}

// Happy path: allowed Host + Origin + route, token injected, upstream answered,
// response forwarded with CORS header.
func TestProxy_HappyPath(t *testing.T) {
	var gotToken string
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("X-PC-Token-Key")
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"name":"wsl-wake"}`))
	}))
	defer upstream.Close()

	p := newTestProxy(t, upstream.URL, "supersecrettoken1234567890")

	rr := doRequest(t, p, http.MethodPost, testPath, testHostOK, testOrigin, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d (body=%s)", rr.Code, http.StatusOK, rr.Body.String())
	}
	if gotToken != "supersecrettoken1234567890" {
		t.Errorf("upstream X-PC-Token-Key: got %q, want injected token", gotToken)
	}
	if gotPath != testPath {
		t.Errorf("upstream path: got %q, want %q", gotPath, testPath)
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != testOrigin {
		t.Errorf("Access-Control-Allow-Origin: got %q, want %q", got, testOrigin)
	}
}

// Host on the localhost alias also accepted — DNS-rebinding defense covers the
// expected aliases (127.0.0.1, localhost) of the listener port.
func TestProxy_AcceptsLocalhostHostAlias(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	p := newTestProxy(t, upstream.URL, "")

	rr := doRequest(t, p, http.MethodPost, testPath, testHostAlt, testOrigin, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", rr.Code, http.StatusOK)
	}
}

// DNS-rebinding scenario: attacker arranges DNS for evil.com → 127.0.0.1, the
// TCP packet reaches us, but the Host header carries evil.com. Rejected.
func TestProxy_RejectsForeignHost(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Errorf("upstream should not be called")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	p := newTestProxy(t, upstream.URL, "")

	rr := doRequest(t, p, http.MethodPost, testPath, "evil.com:9920", testOrigin, "")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status: got %d, want %d", rr.Code, http.StatusForbidden)
	}
}

// Wrong Origin (attacker site running our wake code through CSRF-style trick):
// rejected even if Host happens to match.
func TestProxy_RejectsForeignOrigin(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Errorf("upstream should not be called")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	p := newTestProxy(t, upstream.URL, "")

	rr := doRequest(t, p, http.MethodPost, testPath, testHostOK, "http://evil.com", "")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status: got %d, want %d", rr.Code, http.StatusForbidden)
	}
}

// Missing Origin (non-browser client like curl): rejected. The proxy is
// explicitly meant for browser fetches; tightening here closes a class of
// non-browser bypass attempts.
func TestProxy_RejectsMissingOrigin(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Errorf("upstream should not be called")
	}))
	defer upstream.Close()

	p := newTestProxy(t, upstream.URL, "")

	rr := doRequest(t, p, http.MethodPost, testPath, testHostOK, "", "")
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status: got %d, want %d", rr.Code, http.StatusForbidden)
	}
}

// Method+path outside the allowlist: rejected. Critical: PC's POST /process
// is a command-rewrite primitive (RCE if invoked); the allowlist must hold.
func TestProxy_RejectsUnallowedRoute(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Errorf("upstream should not be called")
	}))
	defer upstream.Close()

	p := newTestProxy(t, upstream.URL, "")

	cases := []struct {
		name, method, path string
	}{
		{"UpdateProcess (RCE primitive)", http.MethodPost, "/process"},
		{"different start target", http.MethodPost, "/process/start/llama-server"},
		{"wrong method on allowed path", http.MethodGet, testPath},
		{"logs read", http.MethodGet, "/process/logs/llama-server/0/100"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rr := doRequest(t, p, c.method, c.path, testHostOK, testOrigin, "")
			if rr.Code != http.StatusForbidden {
				t.Errorf("status: got %d, want %d", rr.Code, http.StatusForbidden)
			}
		})
	}
}

// CORS preflight: returns 204 with the right Access-Control-* headers so the
// browser will subsequently issue the actual POST.
func TestProxy_CORSPreflight(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Errorf("upstream should not be called for preflight")
	}))
	defer upstream.Close()

	p := newTestProxy(t, upstream.URL, "")

	rr := doRequest(t, p, http.MethodOptions, testPath, testHostOK, testOrigin, "")
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status: got %d, want %d", rr.Code, http.StatusNoContent)
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != testOrigin {
		t.Errorf("Allow-Origin: got %q, want %q", got, testOrigin)
	}
	if got := rr.Header().Get("Access-Control-Allow-Methods"); got != "POST" {
		t.Errorf("Allow-Methods: got %q, want %q", got, "POST")
	}
}

// Token file missing at request time → 500 (operator-visible failure rather
// than silently forwarding without auth, which PC would 401 anyway).
func TestProxy_TokenFileVanishesAtRequestTime(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Errorf("upstream should not be called when token unavailable")
	}))
	defer upstream.Close()

	tokenPath := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenPath, []byte("validtoken1234567890ok"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	p, err := loadConfig(testListen, upstream.URL, tokenPath, testOrigin, testRoute)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	// Remove the token file after startup; per-request re-read will fail.
	if err := os.Remove(tokenPath); err != nil {
		t.Fatalf("remove token: %v", err)
	}

	rr := doRequest(t, p, http.MethodPost, testPath, testHostOK, testOrigin, "")
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d, want %d", rr.Code, http.StatusInternalServerError)
	}
}

// Upstream unreachable → 502 (don't pretend success).
func TestProxy_UpstreamUnreachable(t *testing.T) {
	// Spin up + tear down a server to get a guaranteed-dead URL.
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	dead.Close()

	p := newTestProxy(t, dead.URL, "")

	rr := doRequest(t, p, http.MethodPost, testPath, testHostOK, testOrigin, "")
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status: got %d, want %d", rr.Code, http.StatusBadGateway)
	}
}

// Body forwarding: any body the browser sends reaches upstream intact. PC's
// start endpoint doesn't take a body in practice, but other allowed routes
// might in future — guard the contract.
func TestProxy_ForwardsBody(t *testing.T) {
	var gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	p := newTestProxy(t, upstream.URL, "")

	const payload = `{"hello":"world"}`
	rr := doRequest(t, p, http.MethodPost, testPath, testHostOK, testOrigin, payload)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want %d", rr.Code, http.StatusOK)
	}
	if gotBody != payload {
		t.Errorf("upstream body: got %q, want %q", gotBody, payload)
	}
}

// Config validation: refuses to start without explicit allowed-route or
// allowed-origin. Defense against operator misconfiguration that would
// silently widen the attack surface.
func TestLoadConfig_RejectsEmptySafety(t *testing.T) {
	cases := []struct {
		name           string
		listen         string
		upstream       string
		tokenFile      string
		allowedOrigin  string
		allowedRoute   string
		wantSubstrings []string
	}{
		{
			name:           "no allowed-route",
			listen:         testListen,
			upstream:       "http://127.0.0.1:9919",
			allowedOrigin:  testOrigin,
			allowedRoute:   "",
			wantSubstrings: []string{"allowed-route"},
		},
		{
			name:           "no allowed-origin",
			listen:         testListen,
			upstream:       "http://127.0.0.1:9919",
			allowedOrigin:  "",
			allowedRoute:   testRoute,
			wantSubstrings: []string{"allowed-origin"},
		},
		{
			name:           "bad upstream URL",
			listen:         testListen,
			upstream:       "not-a-url",
			allowedOrigin:  testOrigin,
			allowedRoute:   testRoute,
			wantSubstrings: []string{"upstream"},
		},
		{
			name:           "malformed route entry",
			listen:         testListen,
			upstream:       "http://127.0.0.1:9919",
			allowedOrigin:  testOrigin,
			allowedRoute:   "POST/wrong",
			wantSubstrings: []string{"METHOD,PATH"},
		},
		{
			name:           "route path missing leading slash",
			listen:         testListen,
			upstream:       "http://127.0.0.1:9919",
			allowedOrigin:  testOrigin,
			allowedRoute:   "POST,no-slash",
			wantSubstrings: []string{"invalid route"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := loadConfig(c.listen, c.upstream, c.tokenFile, c.allowedOrigin, c.allowedRoute)
			if err == nil {
				t.Fatal("got nil error, want validation failure")
			}
			for _, sub := range c.wantSubstrings {
				if !strings.Contains(err.Error(), sub) {
					t.Errorf("error %q missing substring %q", err.Error(), sub)
				}
			}
		})
	}
}

// Multiple allowed routes via the semicolon-separated flag.
func TestParseRoutes_Multiple(t *testing.T) {
	got, err := parseRoutes("POST,/process/start/wsl-wake ; PATCH,/process/scale/llama-server/3")
	if err != nil {
		t.Fatalf("parseRoutes: %v", err)
	}
	want := []route{
		{method: "POST", path: "/process/start/wsl-wake"},
		{method: "PATCH", path: "/process/scale/llama-server/3"},
	}
	if len(got) != len(want) {
		t.Fatalf("len: got %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("[%d]: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

// loadConfig requires the token file to exist at startup (early operator
// feedback). The validation happens regardless of whether the proxy will
// read it later.
func TestLoadConfig_TokenFileMustExist(t *testing.T) {
	_, err := loadConfig(testListen, "http://127.0.0.1:9919", filepath.Join(t.TempDir(), "missing"), testOrigin, testRoute)
	if err == nil {
		t.Fatal("expected error for missing token file")
	}
}

// Each decision path emits a log line with a distinct decision= tag, so the
// operator can grep wake-proxy logs to answer "did the wake fire and was it
// accepted?" without diffing markup or running interactive tools.
func TestProxy_LogsDecisionPerRequest(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cases := []struct {
		name            string
		method, path    string
		host, origin    string
		wantDecisionTag string
	}{
		{"happy path", http.MethodPost, testPath, testHostOK, testOrigin, "decision=forwarded:200"},
		{"bad host", http.MethodPost, testPath, "evil.com:9920", testOrigin, "decision=denied:bad-host"},
		{"bad origin", http.MethodPost, testPath, testHostOK, "http://evil.com", "decision=denied:bad-origin"},
		{"bad route", http.MethodPost, "/process", testHostOK, testOrigin, "decision=denied:bad-route"},
		{"preflight", http.MethodOptions, testPath, testHostOK, testOrigin, "decision=preflight-ok"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			buf := captureLogs(t)
			p := newTestProxy(t, upstream.URL, "")
			doRequest(t, p, c.method, c.path, c.host, c.origin, "")
			if !strings.Contains(buf.String(), c.wantDecisionTag) {
				t.Errorf("log missing %q\n\tgot: %s", c.wantDecisionTag, buf.String())
			}
		})
	}
}

// Sanity: the parsed upstream URL is preserved verbatim in the proxy.
func TestLoadConfig_PreservesUpstream(t *testing.T) {
	p, err := loadConfig(testListen, "http://127.0.0.1:9919", "", testOrigin, testRoute)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	want, _ := url.Parse("http://127.0.0.1:9919")
	if p.upstream.String() != want.String() {
		t.Errorf("upstream: got %q, want %q", p.upstream.String(), want.String())
	}
}
