package web

import (
	"context"
	"html/template"
	"log"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/eike-hass/infra-mngmt/internal/containers"
	"github.com/eike-hass/infra-mngmt/internal/vault"
)

// VaultClientFactory returns a vault.Client for the given control-plane
// base URL. Production builds use the default; tests inject one that
// captures requests or returns canned responses.
type VaultClientFactory func(baseURL string) VaultClient

// VaultClient is the subset of vault.Client used by handlers. Kept as an
// interface so handler tests don't need real HTTP traffic.
type VaultClient interface {
	GetAllowlist(ctx context.Context) (*vault.Allowlist, error)
	PutAllowlist(ctx context.Context, allowed []string) (*vault.Allowlist, error)
	Tree(ctx context.Context, at string) (*vault.Tree, error)
}

// defaultVaultClientFactory wraps vault.New into the interface contract.
func defaultVaultClientFactory(baseURL string) VaultClient {
	return vault.New(baseURL)
}

// vaultPanelView is the template-friendly rendering of one vault's state.
type vaultPanelView struct {
	Name        string   // container name (used as the panel anchor)
	Description string   // optional description from the declaration
	Control     string   // the control-plane URL (for diagnostics, not exposed in UI)
	Allowed     []string // current allowlist
	Tree        *vaultTreeView
	Error       string    // populated when the call to the vault failed
	FetchedAt   time.Time // for the "as of" hint
}

// vaultTreeView is one rendered tree level. Path is the directory whose
// children are listed; ParentPath is the path of the parent (empty when at
// the data root). Entries are immediate children only. Name is the vault
// container's name — included in the view so HTMX URLs in the tree
// template can reference the right vault when the partial is fetched in
// isolation (the tree partial is reachable independently from the panel
// partial, so it can't rely on a panel-scoped variable).
type vaultTreeView struct {
	Name       string
	Path       string
	ParentPath string
	Entries    []vaultTreeEntry
}

type vaultTreeEntry struct {
	Name    string
	Full    string
	Allowed bool // true if Full or any ancestor is in the allowlist
}

// vaultDecl looks up a containers.yaml entry that has kind=mcp-fs. Returns
// nil when no match (ordinary container or unknown name).
func (s *Server) vaultDecl(name string) *containers.Container {
	d := s.findContainerDecl(name)
	if d == nil {
		return nil
	}
	if d.Kind != "mcp-fs" || d.MCPFS == nil {
		return nil
	}
	return d
}

// newVaultClient builds a VaultClient from the declaration. The factory
// indirection lets tests inject a fake.
func (s *Server) newVaultClient(d *containers.Container) VaultClient {
	if s.vaultFactory != nil {
		return s.vaultFactory(d.MCPFS.Control)
	}
	return defaultVaultClientFactory(d.MCPFS.Control)
}

// loadVaultPanel fetches the current state for one vault and assembles a
// view. Errors are stored on the view rather than returned, so the panel
// can render gracefully (e.g. "vault unreachable, click to retry") instead
// of returning HTTP 500 on every poll while a vault is starting up.
func (s *Server) loadVaultPanel(ctx context.Context, d *containers.Container, treeAt string) vaultPanelView {
	view := vaultPanelView{
		Name:        d.Name,
		Description: d.Description,
		Control:     d.MCPFS.Control,
		FetchedAt:   time.Now(),
	}
	c := s.newVaultClient(d)

	al, err := c.GetAllowlist(ctx)
	if err != nil {
		view.Error = "fetch allowlist: " + err.Error()
		return view
	}
	view.Allowed = al.Allowed

	t, err := c.Tree(ctx, treeAt)
	if err != nil {
		// Allowlist alone is still useful; show it with a tree-load note.
		view.Error = "fetch tree: " + err.Error()
		return view
	}
	tv := &vaultTreeView{Name: d.Name, Path: t.Path}
	if t.Path != "" && strings.Count(t.Path, "/") > 0 && t.Path != "/data" {
		tv.ParentPath = path.Dir(t.Path)
	}
	allowedSet := stringSet(al.Allowed)
	for _, e := range t.Entries {
		tv.Entries = append(tv.Entries, vaultTreeEntry{
			Name:    e.Name,
			Full:    e.Full,
			Allowed: pathAllowed(e.Full, allowedSet),
		})
	}
	view.Tree = tv
	return view
}

// pathAllowed reports whether `p` (or any of its ancestors) is in the
// allowed set. Mirrors the vault's own check semantics so the UI's
// indicator is accurate.
func pathAllowed(p string, allowed map[string]struct{}) bool {
	for cur := p; cur != "" && cur != "/" && cur != "."; cur = path.Dir(cur) {
		if _, ok := allowed[cur]; ok {
			return true
		}
	}
	return false
}

func stringSet(xs []string) map[string]struct{} {
	out := make(map[string]struct{}, len(xs))
	for _, x := range xs {
		out[x] = struct{}{}
	}
	return out
}

// handleVaultPanel renders the full vault panel (allowlist + tree at root).
// HTMX-targeted by the container card on first reveal and after every
// allow/disallow.
func (s *Server) handleVaultPanel(w http.ResponseWriter, r *http.Request) {
	d := s.vaultDecl(r.URL.Query().Get("name"))
	if d == nil {
		http.Error(w, "no such mcp-fs container", http.StatusNotFound)
		return
	}
	at := r.URL.Query().Get("at") // empty → vault's data root
	view := s.loadVaultPanel(r.Context(), d, at)
	s.renderVaultPanel(w, view)
}

// handleVaultTree renders only the tree section — used when the user
// navigates into a subdirectory and we don't need to re-fetch the
// allowlist.
func (s *Server) handleVaultTree(w http.ResponseWriter, r *http.Request) {
	d := s.vaultDecl(r.URL.Query().Get("name"))
	if d == nil {
		http.Error(w, "no such mcp-fs container", http.StatusNotFound)
		return
	}
	at := r.URL.Query().Get("at")
	c := s.newVaultClient(d)

	al, err := c.GetAllowlist(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	t, err := c.Tree(r.Context(), at)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	tv := vaultTreeView{Name: d.Name, Path: t.Path}
	if t.Path != "" && strings.Count(t.Path, "/") > 0 && t.Path != "/data" {
		tv.ParentPath = path.Dir(t.Path)
	}
	allowedSet := stringSet(al.Allowed)
	for _, e := range t.Entries {
		tv.Entries = append(tv.Entries, vaultTreeEntry{
			Name:    e.Name,
			Full:    e.Full,
			Allowed: pathAllowed(e.Full, allowedSet),
		})
	}
	s.renderVaultTree(w, d.Name, tv)
}

// handleVaultAllow appends a path to the allowlist.
func (s *Server) handleVaultAllow(w http.ResponseWriter, r *http.Request) {
	s.vaultMutate(w, r, func(current []string, p string) []string {
		for _, c := range current {
			if c == p {
				return current
			}
		}
		return append(current, p)
	})
}

// handleVaultDisallow removes a path from the allowlist.
func (s *Server) handleVaultDisallow(w http.ResponseWriter, r *http.Request) {
	s.vaultMutate(w, r, func(current []string, p string) []string {
		out := current[:0]
		for _, c := range current {
			if c != p {
				out = append(out, c)
			}
		}
		return out
	})
}

func (s *Server) vaultMutate(w http.ResponseWriter, r *http.Request, mutate func(current []string, p string) []string) {
	d := s.vaultDecl(r.URL.Query().Get("name"))
	if d == nil {
		http.Error(w, "no such mcp-fs container", http.StatusNotFound)
		return
	}
	p := strings.TrimSpace(r.URL.Query().Get("path"))
	if p == "" {
		http.Error(w, "path query required", http.StatusBadRequest)
		return
	}
	c := s.newVaultClient(d)
	al, err := c.GetAllowlist(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	next := mutate(al.Allowed, p)
	if _, err := c.PutAllowlist(r.Context(), next); err != nil {
		log.Printf("vault: put allowlist on %s: %v", d.Name, err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	at := r.URL.Query().Get("at")
	view := s.loadVaultPanel(r.Context(), d, at)
	s.renderVaultPanel(w, view)
}

// vaultPanelTemplate renders one vault's panel — the persistent target
// HTMX swaps into. The card has two parts:
//
//  1. Allowed paths — always visible, the primary content
//  2. <details> "Browse to add" — collapsed by default, expand to see
//     the tree browser
//
// Both `.Name` (vault container name) and `.Tree.Name` are needed in URLs
// so the tree template can be reached as a standalone partial. URLs always
// include `name=` so the handler can resolve which vault to talk to.
var vaultPanelTemplate = template.Must(template.New("vault-panel").Parse(`
<div class="vault-panel" id="vault-{{.Name}}-panel">
  {{if .Error}}
    <div class="vault-error">{{.Error}}</div>
  {{end}}
  <div class="vault-allow-list">
    <div class="vault-section-label">{{len .Allowed}} allowed path{{if ne (len .Allowed) 1}}s{{end}}</div>
    {{if .Allowed}}
      {{range .Allowed}}
        <div class="vault-allow-row">
          <code>{{.}}</code>
          <button class="vault-revoke-btn"
                  hx-post="/api/vault/disallow?name={{$.Name}}&path={{.}}"
                  hx-target="#vault-{{$.Name}}-panel"
                  hx-swap="outerHTML">remove</button>
        </div>
      {{end}}
    {{else}}
      <div class="vault-empty">No paths allowed yet — agents can't read anything from this vault.</div>
    {{end}}
  </div>
  <details class="vault-browse">
    <summary class="vault-browse-summary">Browse + add paths</summary>
    {{if .Tree}}
      {{template "vault-tree" .Tree}}
    {{end}}
  </details>
</div>
{{define "vault-tree"}}
<div class="vault-tree" id="vault-tree-{{.Name}}">
  <div class="vault-tree-row vault-tree-cwd">
    <code>{{.Path}}</code>
    {{if .ParentPath}}
      <button class="vault-tree-up"
              hx-get="/partials/vault/tree?name={{.Name}}&at={{.ParentPath}}"
              hx-target="#vault-tree-{{.Name}}" hx-swap="outerHTML">..</button>
    {{end}}
  </div>
  {{range .Entries}}
    <div class="vault-tree-row">
      <button class="vault-tree-into"
              hx-get="/partials/vault/tree?name={{$.Name}}&at={{.Full}}"
              hx-target="#vault-tree-{{$.Name}}" hx-swap="outerHTML">{{.Name}}/</button>
      {{if .Allowed}}
        <span class="vault-tree-allowed">allowed</span>
      {{else}}
        <button class="vault-tree-allow-btn"
                hx-post="/api/vault/allow?name={{$.Name}}&path={{.Full}}&at={{$.Path}}"
                hx-target="#vault-{{$.Name}}-panel"
                hx-swap="outerHTML">+ allow</button>
      {{end}}
    </div>
  {{else}}
    <div class="vault-empty">No subdirectories.</div>
  {{end}}
</div>
{{end}}
`))

// renderVaultPanel writes the full panel HTML.
func (s *Server) renderVaultPanel(w http.ResponseWriter, view vaultPanelView) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := vaultPanelTemplate.Execute(w, view); err != nil {
		log.Printf("vault: render panel: %v", err)
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

// renderVaultTree writes only the tree section. Embeds the container name
// so nested calls (drill into a subdir) keep returning to the right vault.
func (s *Server) renderVaultTree(w http.ResponseWriter, _ string, tv vaultTreeView) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := vaultPanelTemplate.ExecuteTemplate(w, "vault-tree", tv); err != nil {
		log.Printf("vault: render tree: %v", err)
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}
