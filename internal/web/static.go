package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
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

// staticAssetURL is the FuncMap helper that templates use to reference
// vendored or page-scoped assets. Today it's a passthrough; once we wire
// build-time cache-busting (open question §18.1 in the architecture doc),
// it will append `?v=<commit>` so browser caches invalidate per release.
//
// Always pass the path relative to the static root: {{static "vendor/htmx.min.js"}}
// renders as /static/vendor/htmx.min.js. A leading slash is stripped to
// keep call sites tolerant.
func staticAssetURL(path string) string {
	return "/static/" + strings.TrimPrefix(path, "/")
}
