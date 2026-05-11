package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
	"sync/atomic"
)

// staticFS holds every asset the frontend serves under /static/. The
// directory layout is documented in docs/frontend-architecture.md §3.2.
//
// Vendored third-party files live under static/vendor/ with a sibling
// LICENSES.md recording each upstream URL — never reference these via
// CDN URLs in templates.
//
//go:embed static
var staticFS embed.FS

// staticSubFS strips the "static/" prefix so URLs map directly to file
// paths. /static/vendor/htmx.min.js → static/vendor/htmx.min.js in the FS.
//
// Initialized once at package load. fs.Sub on an embed.FS does not error
// for a known-good prefix; the panic is a defensive guard against typos
// in the embed directive above.
var staticSubFS = func() fs.FS {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic("web: embed static subFS: " + err.Error())
	}
	return sub
}()

// staticHandler serves /static/* from the embedded FS. Mounted by the
// server router. Same-origin only — no CORS, no auth (assets are public).
//
// Content-Type comes from http.FileServer's extension sniffing. JavaScript
// modules served as text/javascript work in every modern browser; CSS is
// auto-detected from the .css extension.
func staticHandler() http.Handler {
	return http.StripPrefix("/static/", http.FileServer(http.FS(staticSubFS)))
}

// assetCacheBuster is the suffix appended to every {{static}} URL as a
// query-string `?v=<token>`. The token is the running binary's git commit
// hash (or build epoch as fallback), so every deploy produces a different
// URL — bypasses browser HTTP cache without changing the file path on
// disk. Set once per server lifetime by SetBuildInfo; read on every render
// via atomic.LoadPointer for cheap concurrent access.
//
// Empty string → no cache buster appended (test setups, unstamped builds).
var assetCacheBuster atomic.Pointer[string]

// setAssetCacheBuster atomically replaces the buster token. Called from
// Server.SetBuildInfo with the resolved commit/epoch identifier.
func setAssetCacheBuster(s string) {
	v := s
	assetCacheBuster.Store(&v)
}

// contentSecurityPolicy is the CSP header value served on every response.
// Resolves docs/frontend-architecture.md §18.3.
//
// The policy is now `'self'`-only for scripts and connect — every external
// dependency was vendored (M1: htmx/fuse/marked, §10 follow-up: CodeMirror),
// every inline script extracted (M3 + modal-script extraction), and every
// onclick= attribute removed in favor of addEventListener delegation.
//
// Rationale per directive:
//   - default-src 'self': everything not otherwise listed must come from us.
//   - script-src 'self': only our own bundled JS executes. New code MUST
//     NOT add inline scripts or external script URLs (§6.2, enforced by
//     frontend_guidelines_test.go).
//   - style-src 'self' 'unsafe-inline': vendored CSS + inline style="…"
//     attributes used for dynamically-computed values (progress bar widths
//     from server data, conditional display:none on transient elements).
//     Dropping the style-src 'unsafe-inline' requires migrating the
//     display-toggle style attrs to class-based toggles; that's a follow-up.
//   - img-src 'self' data:: favicon + inline SVG icons (no remote images).
//   - connect-src 'self': same-origin XHR (HTMX, fetch). No external
//     network calls from page JS.
//   - frame-ancestors 'none': clickjacking protection — nobody iframes us.
//   - base-uri 'none': prevents <base href> injection altering relative URLs.
//   - object-src 'none': blocks <object>/<embed> plugins.
//   - form-action 'self': forms post only to our origin (login/logout/promote).
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; " +
	"connect-src 'self'; " +
	"frame-ancestors 'none'; " +
	"base-uri 'none'; " +
	"object-src 'none'; " +
	"form-action 'self'"

// securityHeadersMiddleware sets the CSP header (and a few cheap baseline
// headers) on every response. Mounted before any route so it covers the
// /static/* tree and the chi-routed pages alike.
//
// Headers set:
//   - Content-Security-Policy (see contentSecurityPolicy above)
//   - X-Content-Type-Options: nosniff (don't MIME-sniff served bytes —
//     belt-and-suspenders against a misconfigured Content-Type)
//   - Referrer-Policy: no-referrer (we don't link out; nothing useful to leak)
func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// staticAssetURL is the FuncMap helper that templates use to reference
// vendored or page-scoped assets. Returns `/static/<path>?v=<commit>` so
// browsers re-fetch the asset after every deploy that produces a new
// commit/build epoch — resolves docs/frontend-architecture.md §18.1.
//
// Always pass the path relative to the static root: {{static "vendor/htmx.min.js"}}
// renders as /static/vendor/htmx.min.js?v=<…>. A leading slash is stripped
// to keep call sites tolerant.
func staticAssetURL(path string) string {
	p := "/static/" + strings.TrimPrefix(path, "/")
	if vp := assetCacheBuster.Load(); vp != nil && *vp != "" {
		p += "?v=" + *vp
	}
	return p
}
