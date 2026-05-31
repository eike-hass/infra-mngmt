package web

import (
	"embed"
	"html/template"
	"log"
	"net/http"
	"path"
	"strings"
	"sync"
)

//go:embed templates
var templatesFS embed.FS

// Parsed templates are cached by their file-set key. The templates live in an
// embedded (immutable) FS and the FuncMap is stateless, so a parsed
// *template.Template is safe to reuse across concurrent requests — html/template
// only mutates during parse, not during Execute. Caching avoids re-parsing the
// full set (incl. the 550-line services template) on every request/poll.
var (
	tmplCacheMu sync.Mutex
	tmplCache   = map[string]*template.Template{}
)

// parseTemplate parses one or more template files from the embedded FS and
// applies the shared FuncMap, memoizing the result by file set. The first file
// listed becomes the "main" template executed by tmpl.Execute() — its base
// filename is used as the template set name. Files are parsed in the order
// given; later files may define named sub-templates ({{define "..."}})
// referenced by the first.
//
// The name argument is kept for call-site readability but is not used to
// name the underlying template; the first file's base name is used instead
// (matching the behavior of template.ParseFiles).
func parseTemplate(_ string, files ...string) *template.Template {
	key := strings.Join(files, "\x00")
	tmplCacheMu.Lock()
	defer tmplCacheMu.Unlock()
	if t, ok := tmplCache[key]; ok {
		return t
	}
	main := path.Base(files[0])
	t := template.Must(template.New(main).Funcs(tmplFuncs).ParseFS(templatesFS, files...))
	tmplCache[key] = t
	return t
}

// renderTmpl executes tmpl with data, logging (rather than silently swallowing)
// any execution error. HTMX partials write their status before Execute, so a
// late error can't change the response — but it must still surface in the log
// instead of producing a half-written body with no trace.
func renderTmpl(w http.ResponseWriter, tmpl *template.Template, data any) {
	if err := tmpl.Execute(w, data); err != nil {
		log.Printf("template execute: %v", err)
	}
}
