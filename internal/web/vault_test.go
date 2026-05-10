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
		"/data/projA",      // allowlist row
		"projA/", "projB/", // tree entries (rendered name + slash)
		"hx-post=\"/api/vault/disallow", // remove button
		"hx-post=\"/api/vault/allow",    // + allow button (for projB which isn't allowed)
		"vault-tree-allowed",            // projA already allowed → "allowed" pill
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
