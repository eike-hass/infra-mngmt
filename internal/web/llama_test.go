package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/eike-hass/infra-mngmt/internal/llama"
)

// drainStubServer stands in for a real llama-server during handler tests.
// /v1/models reports the configured model as loaded; /slots reports a busy
// slot that flips to idle after the first erase. /slots/0?action=erase is
// the destructive primitive the handler is supposed to call.
//
// Each test gets its own stub so we can introspect what the handler did.
func drainStubServer(t *testing.T, modelID string) (*httptest.Server, *drainStubState) {
	t.Helper()
	state := &drainStubState{}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"` + modelID + `","status":{"value":"loaded"}}]}`))
	})
	mux.HandleFunc("/slots", func(w http.ResponseWriter, _ *http.Request) {
		state.slotsCalls++
		if state.eraseCalls > 0 {
			_, _ = w.Write([]byte(`[{"id":0,"id_task":2,"is_processing":false,"n_ctx":1024,"speculative":false,"params":{},"next_token":[]}]`))
		} else {
			_, _ = w.Write([]byte(`[{"id":0,"id_task":1,"is_processing":true,"n_ctx":1024,"speculative":false,"params":{},"next_token":[]}]`))
		}
	})
	mux.HandleFunc("/slots/0", func(w http.ResponseWriter, _ *http.Request) {
		state.eraseCalls++
		w.WriteHeader(http.StatusOK)
	})
	// Health/props are needed by handleLlamaAll re-render after the drain.
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/props", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"build_info":"b1234","total_slots":1,"default_generation_settings":{"n_ctx":1024}}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, state
}

type drainStubState struct {
	slotsCalls int
	eraseCalls int
}

func newServerWithLlama(endpoint, instance, process string) *Server {
	s := &Server{
		llamaServers: []LlamaEntry{{Instance: instance, Process: process, Endpoint: endpoint}},
		llamaClients: map[string]*llama.Client{
			llamaKey(instance, process): llama.New(endpoint, ""),
		},
	}
	return s
}

// lifecycleStubServer responds to the minimum probes handleLlamaAll fans out
// after a load/unload, plus tracks calls to /models/load + /models/unload so
// tests can assert the handler hit the right endpoint with the right body.
func lifecycleStubServer(t *testing.T) (*httptest.Server, *lifecycleStubState) {
	t.Helper()
	state := &lifecycleStubState{}
	mux := http.NewServeMux()
	mux.HandleFunc("/models/load", func(w http.ResponseWriter, r *http.Request) {
		state.loadCalls++
		state.loadBody = readAll(t, r)
		_, _ = w.Write([]byte(`{"success":true}`))
	})
	mux.HandleFunc("/models/unload", func(w http.ResponseWriter, r *http.Request) {
		state.unloadCalls++
		state.unloadBody = readAll(t, r)
		_, _ = w.Write([]byte(`{"success":true}`))
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("/props", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"build_info":"b1234","total_slots":1,"default_generation_settings":{"n_ctx":1024}}`))
	})
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"test-model"}]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, state
}

type lifecycleStubState struct {
	loadCalls   int
	unloadCalls int
	loadBody    string
	unloadBody  string
}

func readAll(t *testing.T, r *http.Request) string {
	t.Helper()
	defer r.Body.Close()
	buf := make([]byte, r.ContentLength)
	_, _ = r.Body.Read(buf)
	return string(buf)
}

func TestHandleLlamaLoadHitsModelsLoad(t *testing.T) {
	srv, state := lifecycleStubServer(t)
	s := newServerWithLlama(srv.URL, "wsl", "llama-server")

	req := httptest.NewRequest(http.MethodPost,
		"/api/llama/load?instance=wsl&process=llama-server&model=test-model", nil)
	w := httptest.NewRecorder()
	s.handleLlamaLoad(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got %d, body=%s", w.Code, w.Body.String())
	}
	if state.loadCalls != 1 {
		t.Errorf("loadCalls = %d, want 1", state.loadCalls)
	}
	if !strings.Contains(state.loadBody, `"model":"test-model"`) {
		t.Errorf("loadBody = %q, want model field set", state.loadBody)
	}
}

func TestHandleLlamaUnloadHitsModelsUnload(t *testing.T) {
	srv, state := lifecycleStubServer(t)
	s := newServerWithLlama(srv.URL, "wsl", "llama-server")

	req := httptest.NewRequest(http.MethodPost,
		"/api/llama/unload?instance=wsl&process=llama-server&model=test-model", nil)
	w := httptest.NewRecorder()
	s.handleLlamaUnload(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got %d, body=%s", w.Code, w.Body.String())
	}
	if state.unloadCalls != 1 {
		t.Errorf("unloadCalls = %d, want 1", state.unloadCalls)
	}
}

func TestHandleLlamaLoadRejectsMissingModel(t *testing.T) {
	s := newServerWithLlama("http://127.0.0.1:0", "wsl", "llama-server")
	req := httptest.NewRequest(http.MethodPost,
		"/api/llama/load?instance=wsl&process=llama-server", nil)
	w := httptest.NewRecorder()
	s.handleLlamaLoad(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", w.Code)
	}
}

func TestHandleLlamaDrainRejectsBadName(t *testing.T) {
	s := newServerWithLlama("http://127.0.0.1:0", "wsl", "llama-server")
	req := httptest.NewRequest(http.MethodPost,
		"/api/llama/drain?instance=../etc&process=llama-server", nil)
	w := httptest.NewRecorder()
	s.handleLlamaDrain(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", w.Code)
	}
}

func TestHandleLlamaDrainRejectsUnconfiguredInstance(t *testing.T) {
	s := newServerWithLlama("http://127.0.0.1:0", "wsl", "llama-server")
	req := httptest.NewRequest(http.MethodPost,
		"/api/llama/drain?instance=windows&process=llama-server", nil)
	w := httptest.NewRecorder()
	s.handleLlamaDrain(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404 for non-whitelisted instance", w.Code)
	}
}

func TestHandleLlamaDrainCancelsActiveSlot(t *testing.T) {
	srv, state := drainStubServer(t, "test-model")
	s := newServerWithLlama(srv.URL, "wsl", "llama-server")

	req := httptest.NewRequest(http.MethodPost,
		"/api/llama/drain?instance=wsl&process=llama-server", nil)
	w := httptest.NewRecorder()
	s.handleLlamaDrain(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got %d, body=%s", w.Code, w.Body.String())
	}
	if state.eraseCalls < 1 {
		t.Errorf("expected at least 1 erase call, got %d", state.eraseCalls)
	}
	// Response should be the rendered llama partial — assert at least one
	// stable selector per §14.3.
	if !strings.Contains(w.Body.String(), "llama-card") {
		t.Errorf("response missing llama-card selector; body=%s", w.Body.String()[:min(200, w.Body.Len())])
	}
}

// Note: structural-ETag tests removed when the llama view moved to
// per-card polling (docs/frontend-architecture.md §7.10). The 304
// fast-path no longer exists; each card swaps itself outerHTML on its
// own 10s timer.
