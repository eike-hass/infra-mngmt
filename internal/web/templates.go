package web

import (
	"embed"
	"html/template"
	"path"
)

//go:embed templates
var templatesFS embed.FS

// parseTemplate parses one or more template files from the embedded FS and
// applies the shared FuncMap. The first file listed becomes the "main"
// template executed by tmpl.Execute() — its base filename is used as the
// template set name. Files are parsed in the order given; later files may
// define named sub-templates ({{define "..."}}) referenced by the first.
//
// The name argument is kept for call-site readability but is not used to
// name the underlying template; the first file's base name is used instead
// (matching the behavior of template.ParseFiles).
func parseTemplate(_ string, files ...string) *template.Template {
	main := path.Base(files[0])
	return template.Must(template.New(main).Funcs(tmplFuncs).ParseFS(templatesFS, files...))
}
