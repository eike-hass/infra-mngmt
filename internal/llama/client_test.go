package llama

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHealthOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()

	h, err := New(srv.URL, "").Health(context.Background())
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if !h.OK || h.Status != "ok" {
		t.Errorf("Health = %+v", h)
	}
}

func TestHealthLoading(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"message":"loading model","type":"unavailable_error"}}`))
	}))
	defer srv.Close()

	h, err := New(srv.URL, "").Health(context.Background())
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if h.OK {
		t.Errorf("Health.OK = true, want false")
	}
	if h.Status != "loading model" {
		t.Errorf("Health.Status = %q, want %q", h.Status, "loading model")
	}
}

func TestPropsParsesNCtxAndSleeping(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("missing/incorrect auth header: %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{
			"model_path":"/models/llama-7b.gguf",
			"total_slots":4,
			"build_info":"b3000-deadbeef",
			"is_sleeping":true,
			"default_generation_settings":{"n_ctx":8192}
		}`))
	}))
	defer srv.Close()

	p, err := New(srv.URL, "secret").Props(context.Background())
	if err != nil {
		t.Fatalf("Props: %v", err)
	}
	if p.ModelPath != "/models/llama-7b.gguf" || p.TotalSlots != 4 || p.NCtx != 8192 || !p.IsSleeping || p.BuildInfo != "b3000-deadbeef" {
		t.Errorf("Props = %+v", p)
	}
}

func TestMetricsParsesAllSeries(t *testing.T) {
	// Body matches the actual emission of llama.cpp b9080 — verified live
	// against ggml-org/llama.cpp build b9080-9f5f0e689 on 2026-05-10. The
	// upstream README's kv_cache_* series do NOT exist in current builds.
	body := `# HELP llamacpp:prompt_tokens_total Number of prompt tokens processed.
# TYPE llamacpp:prompt_tokens_total counter
llamacpp:prompt_tokens_total 12345
llamacpp:prompt_seconds_total 49.4
llamacpp:tokens_predicted_total 6789
llamacpp:tokens_predicted_seconds_total 175.5
llamacpp:n_decode_total 2104
llamacpp:n_tokens_max 32768
llamacpp:prompt_tokens_seconds 250.5
llamacpp:predicted_tokens_seconds 38.7
llamacpp:requests_processing 2
llamacpp:requests_deferred 1
llamacpp:n_busy_slots_per_decode 1.7
llamacpp:something_else 99
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	m, err := New(srv.URL, "").Metrics(context.Background(), "")
	if err != nil {
		t.Fatalf("Metrics: %v", err)
	}
	if !m.Available {
		t.Fatalf("Available = false")
	}
	if m.PromptTokensTotal != 12345 || m.TokensPredictedTotal != 6789 {
		t.Errorf("totals: %+v", m)
	}
	if m.PromptSecondsTotal != 49.4 || m.TokensPredictedSecondsTotal != 175.5 {
		t.Errorf("seconds totals: %+v", m)
	}
	if m.PromptTokensPerSec != 250.5 || m.PredictedPerSec != 38.7 {
		t.Errorf("rates: %+v", m)
	}
	if m.RequestsProcessing != 2 || m.RequestsDeferred != 1 || m.NTokensMax != 32768 {
		t.Errorf("requests: %+v", m)
	}
	if m.NDecodeTotal != 2104 || m.NBusySlotsPerDecode != 1.7 {
		t.Errorf("decode counters: %+v", m)
	}
}

func TestMetricsHandlesLabels(t *testing.T) {
	// Defensive: future versions may add labels. Strip them and still parse.
	body := `llamacpp:requests_processing{slot="0"} 3
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	m, _ := New(srv.URL, "").Metrics(context.Background(), "")
	if m.RequestsProcessing != 3 {
		t.Errorf("RequestsProcessing = %v, want 3", m.RequestsProcessing)
	}
}

func TestMetricsNotImplementedReturnsUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = w.Write([]byte(`{"error":{"message":"not supported","type":"not_supported_error"}}`))
	}))
	defer srv.Close()

	m, err := New(srv.URL, "").Metrics(context.Background(), "")
	if err != nil {
		t.Fatalf("expected nil err for 501, got %v", err)
	}
	if m.Available {
		t.Errorf("Available = true, want false on 501")
	}
}

func TestSlotsParses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[
			{"id":0,"id_task":42,"n_ctx":4096,"is_processing":true,"prompt":"summarize this text","next_token":{"has_next_token":true,"n_remain":80,"n_decoded":120}},
			{"id":1,"id_task":-1,"n_ctx":4096,"is_processing":false,"prompt":"","next_token":{"has_next_token":false,"n_remain":0,"n_decoded":0}}
		]`))
	}))
	defer srv.Close()

	slots, err := New(srv.URL, "").Slots(context.Background(), "")
	if err != nil {
		t.Fatalf("Slots: %v", err)
	}
	if len(slots) != 2 {
		t.Fatalf("len(slots) = %d, want 2", len(slots))
	}
	if slots[0].ID != 0 || !slots[0].IsProcessing || slots[0].Progress().NDecoded != 120 || slots[0].Progress().NRemain != 80 {
		t.Errorf("slot 0: %+v", slots[0])
	}
	if slots[0].Prompt != "summarize this text" {
		t.Errorf("slot 0 Prompt = %q, want %q", slots[0].Prompt, "summarize this text")
	}
	if slots[1].IsProcessing || slots[1].IDTask != -1 {
		t.Errorf("slot 1: %+v", slots[1])
	}
}

func TestSlotsDisabled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = w.Write([]byte(`{"error":{"type":"not_supported_error"}}`))
	}))
	defer srv.Close()

	_, err := New(srv.URL, "").Slots(context.Background(), "")
	if !errors.Is(err, ErrSlotsDisabled) {
		t.Errorf("expected ErrSlotsDisabled, got %v", err)
	}
}

func TestSlotsAcceptsArrayNextToken(t *testing.T) {
	// Newer llama.cpp builds emit next_token as a single-element array (used
	// by speculative decoding). Older builds emit a flat object. Our client
	// should accept both and surface the same SlotProgress.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[
			{"id":0,"id_task":7,"n_ctx":8192,"is_processing":true,"next_token":[{"has_next_token":true,"n_remain":50,"n_decoded":150}]},
			{"id":1,"id_task":-1,"n_ctx":8192,"is_processing":false,"next_token":{"has_next_token":false,"n_remain":0,"n_decoded":0}}
		]`))
	}))
	defer srv.Close()

	slots, err := New(srv.URL, "").Slots(context.Background(), "")
	if err != nil {
		t.Fatalf("Slots: %v", err)
	}
	if slots[0].Progress().NDecoded != 150 || slots[0].Progress().NRemain != 50 {
		t.Errorf("array shape not parsed: %+v", slots[0].Progress())
	}
	if slots[1].Progress().HasNextToken {
		t.Errorf("flat shape not parsed: %+v", slots[1].Progress())
	}
}

func TestSlotsAndMetricsAppendModelQueryParam(t *testing.T) {
	var seenSlots, seenMetrics string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/slots":
			seenSlots = r.URL.RawQuery
			_, _ = w.Write([]byte(`[]`))
		case "/metrics":
			seenMetrics = r.URL.RawQuery
			_, _ = w.Write([]byte(``))
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	_, _ = c.Slots(context.Background(), "unsloth/Qwen3.6-35B-A3B-GGUF:UD-Q6_K_XL")
	_, _ = c.Metrics(context.Background(), "unsloth/Qwen3.6-35B-A3B-GGUF:UD-Q6_K_XL")

	want := "model=unsloth%2FQwen3.6-35B-A3B-GGUF%3AUD-Q6_K_XL"
	if seenSlots != want {
		t.Errorf("slots query = %q, want %q", seenSlots, want)
	}
	if seenMetrics != want {
		t.Errorf("metrics query = %q, want %q", seenMetrics, want)
	}
}

func TestModelsAndRouterDetection(t *testing.T) {
	body := `{"data":[
		{"id":"a","object":"model","owned_by":"llamacpp","status":{"value":"loaded","args":["--alias","a"]}},
		{"id":"b","object":"model","owned_by":"llamacpp","status":{"value":"unloaded","exit_code":10,"failed":true}}
	]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	models, err := New(srv.URL, "").Models(context.Background())
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("len(models) = %d, want 2", len(models))
	}
	if !IsRouterMode(models) {
		t.Errorf("expected router-mode detection")
	}
	if models[1].Status == nil || !models[1].Status.Failed {
		t.Errorf("failed flag not preserved: %+v", models[1].Status)
	}
}

func TestSingleModelIsNotRouterMode(t *testing.T) {
	body := `{"data":[{"id":"single","object":"model","owned_by":"llamacpp"}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	models, _ := New(srv.URL, "").Models(context.Background())
	if IsRouterMode(models) {
		t.Errorf("single-model server flagged as router mode: %+v", models)
	}
}

func TestProbesSendBearerToken(t *testing.T) {
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// /health is intentionally unauthenticated upstream; our client must
		// preserve that so a probe still works even with a global API key.
		if !strings.HasPrefix(r.URL.Path, "/health") {
			seen = r.Header.Get("Authorization")
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "key123")
	_, _ = c.Health(context.Background())
	if seen != "" {
		t.Errorf("/health should not send auth, got %q", seen)
	}
	_, _ = c.Props(context.Background())
	if seen != "Bearer key123" {
		t.Errorf("/props auth = %q, want Bearer key123", seen)
	}
}

func TestEraseSlotPostsCorrectPath(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Method + " " + r.URL.Path + "?" + r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := New(srv.URL, "").EraseSlot(context.Background(), 0, ""); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got != "POST /slots/0?action=erase" {
		t.Errorf("got %q", got)
	}
}

func TestEraseSlotIncludesModelParam(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.RawQuery
	}))
	defer srv.Close()

	_ = New(srv.URL, "").EraseSlot(context.Background(), 2, "unsloth/Qwen3.6-35B-A3B-GGUF:UD-Q6_K_XL")
	if !strings.Contains(got, "action=erase") || !strings.Contains(got, "model=unsloth%2FQwen3.6-35B-A3B-GGUF") {
		t.Errorf("query = %q (missing action or model)", got)
	}
}

func TestEraseSlotSurfaces5xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("kv cache fault"))
	}))
	defer srv.Close()

	err := New(srv.URL, "").EraseSlot(context.Background(), 0, "")
	if err == nil || !strings.Contains(err.Error(), "kv cache fault") {
		t.Errorf("expected error containing upstream body, got: %v", err)
	}
}

func TestDrainEmptyQueueReturnsZero(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slots" {
			_, _ = w.Write([]byte(`[{"id":0,"id_task":0,"is_processing":false,"n_ctx":0,"speculative":false,"params":{},"next_token":[]}]`))
			return
		}
		t.Errorf("unexpected request to %s", r.URL.Path)
	}))
	defer srv.Close()

	res, err := New(srv.URL, "").Drain(context.Background(), "", DrainOptions{})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if res.Cancellations != 0 || res.Iterations != 1 {
		t.Errorf("got %+v, want 0 cancels in 1 iter", res)
	}
}

func TestDrainCancelsThenReturns(t *testing.T) {
	// Simulate a queue: first /slots call shows busy, then EraseSlot
	// hit, then second /slots call shows idle.
	state := 0
	var erases int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/slots":
			state++
			if state == 1 {
				_, _ = w.Write([]byte(`[{"id":0,"id_task":7148,"is_processing":true,"n_ctx":0,"speculative":false,"params":{},"next_token":[]}]`))
			} else {
				_, _ = w.Write([]byte(`[{"id":0,"id_task":7149,"is_processing":false,"n_ctx":0,"speculative":false,"params":{},"next_token":[]}]`))
			}
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/slots/"):
			erases++
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	res, err := New(srv.URL, "").Drain(context.Background(), "",
		DrainOptions{PollInterval: 1 * time.Millisecond})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if res.Cancellations != 1 || erases != 1 {
		t.Errorf("got cancels=%d erases=%d, want 1/1", res.Cancellations, erases)
	}
	if res.Iterations < 2 {
		t.Errorf("expected at least 2 iters, got %d", res.Iterations)
	}
}

func TestLoadModelPostsJSONBody(t *testing.T) {
	var gotPath, gotBody, gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotCT = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer srv.Close()

	if err := New(srv.URL, "").LoadModel(context.Background(), "user/Model:Q4_K_M"); err != nil {
		t.Fatalf("LoadModel: %v", err)
	}
	if gotPath != "/models/load" {
		t.Errorf("path = %q, want /models/load", gotPath)
	}
	if gotCT != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotCT)
	}
	if gotBody != `{"model":"user/Model:Q4_K_M"}` {
		t.Errorf("body = %q, want JSON with model field", gotBody)
	}
}

func TestUnloadModelPostsJSONBody(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer srv.Close()

	if err := New(srv.URL, "").UnloadModel(context.Background(), "foo"); err != nil {
		t.Fatalf("UnloadModel: %v", err)
	}
	if gotPath != "/models/unload" {
		t.Errorf("path = %q, want /models/unload", gotPath)
	}
}

func TestLoadModelSurfacesUpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"unknown model"}`))
	}))
	defer srv.Close()

	err := New(srv.URL, "").LoadModel(context.Background(), "ghost")
	if err == nil {
		t.Fatal("expected error on 404, got nil")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error should include status code, got: %v", err)
	}
}

func TestPropsCapsResponseBody(t *testing.T) {
	// A misbehaving upstream returns far more than maxRespBytes. We must read
	// at most the cap (so we can't OOM); the resulting truncated JSON then
	// fails to parse rather than being fully buffered.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Open a JSON object, then flood with bytes well past the cap.
		_, _ = io.WriteString(w, `{"model_path":"`)
		_, _ = io.Copy(w, io.LimitReader(neverEnding{}, maxRespBytes+(1<<20)))
	}))
	defer srv.Close()

	_, err := New(srv.URL, "").Props(context.Background())
	if err == nil {
		t.Fatal("expected parse error from truncated oversized body, got nil")
	}
}

// neverEnding is an io.Reader that yields an unbounded stream of 'a' bytes.
type neverEnding struct{}

func (neverEnding) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'a'
	}
	return len(p), nil
}

func TestDrainHonorsContextCancel(t *testing.T) {
	// /slots always reports busy — drain would loop forever without ctx cancel.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slots" {
			_, _ = w.Write([]byte(`[{"id":0,"id_task":1,"is_processing":true,"n_ctx":0,"speculative":false,"params":{},"next_token":[]}]`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := New(srv.URL, "").Drain(ctx, "",
		DrainOptions{PollInterval: 5 * time.Millisecond, Deadline: time.Hour})
	if err == nil {
		t.Errorf("expected ctx error, got nil")
	}
}
