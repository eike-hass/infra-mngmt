package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/eike-hass/infra-mngmt/internal/containers"
	"github.com/eike-hass/infra-mngmt/internal/vault"
)

// fakeVaultClient implements VaultClient for handler tests, capturing calls
// so we can assert the handler did the right thing.
type fakeVaultClient struct {
	allow vault.Allowlist
	tree  vault.Tree
	puts  [][]string
}

func (f *fakeVaultClient) GetAllowlist(ctx context.Context) (*vault.Allowlist, error) {
	cp := f.allow
	cp.Allowed = append([]string(nil), f.allow.Allowed...)
	return &cp, nil
}

func (f *fakeVaultClient) PutAllowlist(ctx context.Context, allowed []string) (*vault.Allowlist, error) {
	cp := append([]string(nil), allowed...)
	f.puts = append(f.puts, cp)
	f.allow = vault.Allowlist{Allowed: cp}
	return &f.allow, nil
}

func (f *fakeVaultClient) Tree(ctx context.Context, at string) (*vault.Tree, error) {
	cp := f.tree
	cp.Entries = append([]vault.TreeEntry(nil), f.tree.Entries...)
	if at != "" {
		cp.Path = at
	}
	return &cp, nil
}

func newVaultTestServer(decl containers.Container, fake *fakeVaultClient) *Server {
	s := &Server{
		containerDecls: []containers.Container{decl},
		vaultFactory: func(_ string) VaultClient {
			return fake
		},
	}
	return s
}

func mcpFSDecl() containers.Container {
	return containers.Container{
		Name:        "workspace-vault",
		Kind:        "mcp-fs",
		ComposeFile: "vault.yaml",
		MCPFS:       &containers.MCPFSConfig{Control: "http://workspace-vault:3002"},
	}
}

func TestHandleVaultPanelRendersAllowedAndTree(t *testing.T) {
	fake := &fakeVaultClient{
		allow: vault.Allowlist{Allowed: []string{"/data/projA"}},
		tree: vault.Tree{
			Path: "/data",
			Entries: []vault.TreeEntry{
				{Name: "projA", Full: "/data/projA"},
				{Name: "projB", Full: "/data/projB"},
			},
		},
	}
	s := newVaultTestServer(mcpFSDecl(), fake)

	req := httptest.NewRequest(http.MethodGet, "/partials/vault/panel?name=workspace-vault", nil)
	w := httptest.NewRecorder()
	s.handleVaultPanel(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{
		"/data/projA",                     // allowlist row
		"vault-tree-name\">projA</span>/", // tree entry projA (wrapped in name span)
		"vault-tree-name\">projB</span>/", // tree entry projB
		"hx-post=\"/api/vault/disallow",   // remove button
		"hx-post=\"/api/vault/allow",      // + allow button (for projB which isn't allowed)
		"vault-flag-allowed",              // projA already allowed → "allowed" pill
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\n--- body ---\n%s", want, body)
		}
	}
}

func TestHandleVaultAllowAppendsAndPersists(t *testing.T) {
	fake := &fakeVaultClient{
		allow: vault.Allowlist{Allowed: []string{"/data/projA"}},
		tree:  vault.Tree{Path: "/data"},
	}
	s := newVaultTestServer(mcpFSDecl(), fake)

	req := httptest.NewRequest(http.MethodPost,
		"/api/vault/allow?name=workspace-vault&path=/data/projB", nil)
	w := httptest.NewRecorder()
	s.handleVaultAllow(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", w.Code, w.Body.String())
	}
	if len(fake.puts) != 1 {
		t.Fatalf("expected 1 PUT, got %d", len(fake.puts))
	}
	got := fake.puts[0]
	if len(got) != 2 || got[0] != "/data/projA" || got[1] != "/data/projB" {
		t.Errorf("PUT body = %v, want [/data/projA /data/projB]", got)
	}
}

func TestHandleVaultAllowIsIdempotent(t *testing.T) {
	fake := &fakeVaultClient{
		allow: vault.Allowlist{Allowed: []string{"/data/projA"}},
		tree:  vault.Tree{Path: "/data"},
	}
	s := newVaultTestServer(mcpFSDecl(), fake)

	req := httptest.NewRequest(http.MethodPost,
		"/api/vault/allow?name=workspace-vault&path=/data/projA", nil)
	w := httptest.NewRecorder()
	s.handleVaultAllow(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if len(fake.puts) != 1 || len(fake.puts[0]) != 1 {
		t.Errorf("expected single PUT with 1 entry, got %v", fake.puts)
	}
}

func TestHandleVaultDisallowRemovesPath(t *testing.T) {
	fake := &fakeVaultClient{
		allow: vault.Allowlist{Allowed: []string{"/data/projA", "/data/projB"}},
		tree:  vault.Tree{Path: "/data"},
	}
	s := newVaultTestServer(mcpFSDecl(), fake)

	req := httptest.NewRequest(http.MethodPost,
		"/api/vault/disallow?name=workspace-vault&path=/data/projA", nil)
	w := httptest.NewRecorder()
	s.handleVaultDisallow(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if len(fake.puts) != 1 {
		t.Fatalf("expected 1 PUT, got %d", len(fake.puts))
	}
	if got := fake.puts[0]; len(got) != 1 || got[0] != "/data/projB" {
		t.Errorf("PUT body = %v, want [/data/projB]", got)
	}
}

func TestHandleVaultPanelRejectsNonMCPFS(t *testing.T) {
	// Container exists but isn't a vault — should 404.
	s := &Server{
		containerDecls: []containers.Container{
			{Name: "ident-browser"},
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/partials/vault/panel?name=ident-browser", nil)
	w := httptest.NewRecorder()
	s.handleVaultPanel(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestPathAllowed(t *testing.T) {
	allowed := stringSet([]string{"/data/projA", "/data/shared"})
	cases := map[string]bool{
		"/data/projA":         true,
		"/data/projA/src":     true,
		"/data/projA/src/foo": true,
		"/data/projB":         false,
		"/data/projB/src":     false,
		"/data/shared/file":   true,
		"/data":               false,
	}
	for in, want := range cases {
		if got := pathAllowed(in, allowed); got != want {
			t.Errorf("pathAllowed(%q) = %v, want %v", in, got, want)
		}
	}
}

// TestClosestAllowedAncestor regression-guards the vault tree's "covered by"
// UI bug: when a broad ancestor (e.g. `/data`) is in the allowlist, every
// subdir was rendered as "allowed" with no add button, leaving the user
// unable to list specific narrower paths. The fix is to distinguish
// "explicitly listed" from "covered by ancestor" using this helper.
func TestClosestAllowedAncestor(t *testing.T) {
	cases := []struct {
		name    string
		allowed []string
		in      string
		want    string
	}{
		// Exact match doesn't count as its own ancestor — return ""
		// so the caller's "explicit listing" check is the source of
		// truth for the literal entry.
		{"exact match returns empty", []string{"/data/x"}, "/data/x", ""},
		// Subdir of a listed ancestor: the ancestor is returned.
		{"covered by direct parent", []string{"/data"}, "/data/sub", "/data"},
		{"covered by grandparent", []string{"/data"}, "/data/sub/deep", "/data"},
		// Multiple ancestors in the allowlist: return the longest
		// (most specific) one.
		{"longest-match wins", []string{"/data", "/data/sub"}, "/data/sub/deep", "/data/sub"},
		// No covering ancestor.
		{"uncovered", []string{"/data/x"}, "/data/y", ""},
		{"uncovered deep", []string{"/data/x"}, "/data/y/z", ""},
		// Root path corner case.
		{"root path", []string{"/data"}, "/data", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := closestAllowedAncestor(c.in, stringSet(c.allowed))
			if got != c.want {
				t.Errorf("closestAllowedAncestor(%q, %v) = %q, want %q",
					c.in, c.allowed, got, c.want)
			}
		})
	}
}
