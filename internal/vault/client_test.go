package vault

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func newServer(t *testing.T, h http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return New(srv.URL), srv
}

func TestGetAllowlist(t *testing.T) {
	c, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/allowlist" || r.Method != http.MethodGet {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"allowed": ["/data/projA", "/data/shared"]}`)
	})
	got, err := c.GetAllowlist(context.Background())
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	want := []string{"/data/projA", "/data/shared"}
	if !reflect.DeepEqual(got.Allowed, want) {
		t.Errorf("got %v, want %v", got.Allowed, want)
	}
}

func TestPutAllowlistSendsBody(t *testing.T) {
	var captured map[string][]string
	c, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("expected JSON content-type, got %q", ct)
		}
		_ = json.NewDecoder(r.Body).Decode(&captured)
		_, _ = io.WriteString(w, `{"allowed": ["/data/x"]}`)
	})
	got, err := c.PutAllowlist(context.Background(), []string{"/data/x", "/data/y"})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if want := []string{"/data/x", "/data/y"}; !reflect.DeepEqual(captured["allowed"], want) {
		t.Errorf("server received %v, want %v", captured["allowed"], want)
	}
	if !reflect.DeepEqual(got.Allowed, []string{"/data/x"}) {
		t.Errorf("response %v not propagated", got.Allowed)
	}
}

func TestTreeWithAt(t *testing.T) {
	c, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tree" {
			t.Errorf("path %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("at"); got != "/data/projA" {
			t.Errorf("at query = %q", got)
		}
		_, _ = io.WriteString(w, `{"path":"/data/projA","entries":[{"name":"src","full":"/data/projA/src"}]}`)
	})
	got, err := c.Tree(context.Background(), "/data/projA")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got.Path != "/data/projA" || len(got.Entries) != 1 || got.Entries[0].Name != "src" {
		t.Errorf("unexpected tree: %+v", got)
	}
}

func TestErrorBodyIsSurfaced(t *testing.T) {
	c, _ := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"outside /data"}`)
	})
	_, err := c.Tree(context.Background(), "/etc")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "outside /data") {
		t.Errorf("error %q should include server message", err.Error())
	}
	if !strings.Contains(err.Error(), "HTTP 400") {
		t.Errorf("error %q should include status code", err.Error())
	}
}

func TestNewTrimsTrailingSlash(t *testing.T) {
	c := New("http://x:3002/")
	if c.BaseURL != "http://x:3002" {
		t.Errorf("got %q", c.BaseURL)
	}
}

func TestQueryEscapeBasics(t *testing.T) {
	cases := map[string]string{
		"":                        "",
		"foo":                     "foo",
		"/data/projA":             "/data/projA",
		"a b c":                   "a+b+c",
		"x:y":                     "x%3Ay",
		"file_with-dots.and.dash": "file_with-dots.and.dash",
	}
	for in, want := range cases {
		if got := queryEscape(in); got != want {
			t.Errorf("queryEscape(%q) = %q, want %q", in, got, want)
		}
	}
}
