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
