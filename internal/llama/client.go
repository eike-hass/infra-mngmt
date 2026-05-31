// Package llama is a minimal HTTP client for the llama.cpp `llama-server`
// observability surface: /health, /props, /metrics (Prometheus), /slots.
//
// Each probe is independent — a single dead/slow endpoint times out on its own
// budget, never the sum across endpoints. The intended caller (web layer)
// kicks all four probes off in parallel and renders whatever returned in
// time. Callers must NOT share a Client across goroutines if they care about
// per-call cancellation only — `context.WithTimeout` per call is the right
// pattern.
package llama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// maxRespBytes caps how much of any response body we read. These payloads are
// health/props/metrics/slots/models JSON (KBs); the cap is a guardrail so a
// misbehaving upstream can't OOM the dashboard.
const maxRespBytes = 8 << 20 // 8 MiB

// Client probes one llama-server endpoint. Endpoint is the base URL
// ("http://host:port"); paths are appended. APIKey, when non-empty, is sent as
// `Authorization: Bearer <key>` on every request except /health (which the
// upstream server explicitly leaves unauthenticated).
type Client struct {
	endpoint string
	apiKey   string
	http     *http.Client
}

// New returns a Client with a per-request timeout of 8s. Sized for the
// worst-observed case: a 35B-parameter model decoding on Vulkan, where
// /metrics and /slots have to wait for the inference mutex between decode
// steps. Per-step latency at this size can be 100-500ms; the lock is only
// released between steps. 8s gives enough headroom while keeping us under
// the 10s panel refresh cadence so probes don't overlap.
func New(endpoint, apiKey string) *Client {
	return &Client{
		endpoint: strings.TrimRight(endpoint, "/"),
		apiKey:   apiKey,
		http:     &http.Client{Timeout: 8 * time.Second},
	}
}

// Endpoint returns the base URL; surfaced for UI display.
func (c *Client) Endpoint() string { return c.endpoint }

// ── /health ───────────────────────────────────────────────────────────────────

// Health is the parsed body of GET /health.
type Health struct {
	OK     bool   // 200 → true, else false
	Status string // "ok", "loading model", etc. (best-effort)
}

// Health probes /health. Returns OK=false on timeout/non-2xx; the caller
// distinguishes via err.
func (c *Client) Health(ctx context.Context) (Health, error) {
	resp, err := c.do(ctx, http.MethodGet, "/health", false)
	if err != nil {
		return Health{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxRespBytes))
	h := Health{OK: resp.StatusCode == http.StatusOK}
	var parsed struct {
		Status string `json:"status"`
		Error  struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err == nil {
		if parsed.Status != "" {
			h.Status = parsed.Status
		} else if parsed.Error.Message != "" {
			h.Status = parsed.Error.Message
		}
	}
	if h.Status == "" {
		if h.OK {
			h.Status = "ok"
		} else {
			h.Status = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
	}
	return h, nil
}

// ── /props ────────────────────────────────────────────────────────────────────

// Props is a curated subset of GET /props relevant to a status panel. Upstream
// returns a much larger object — we deliberately lift only what the UI shows.
type Props struct {
	ModelPath  string `json:"model_path"`
	TotalSlots int    `json:"total_slots"`
	BuildInfo  string `json:"build_info"`
	IsSleeping bool   `json:"is_sleeping"`
	// NCtx is the per-slot ctx budget (default_generation_settings.n_ctx).
	NCtx int `json:"-"`
}

// Props probes /props.
func (c *Client) Props(ctx context.Context) (Props, error) {
	resp, err := c.do(ctx, http.MethodGet, "/props", true)
	if err != nil {
		return Props{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxRespBytes))
	if err != nil {
		return Props{}, err
	}
	var raw struct {
		ModelPath                 string `json:"model_path"`
		TotalSlots                int    `json:"total_slots"`
		BuildInfo                 string `json:"build_info"`
		IsSleeping                bool   `json:"is_sleeping"`
		DefaultGenerationSettings struct {
			NCtx int `json:"n_ctx"`
		} `json:"default_generation_settings"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return Props{}, fmt.Errorf("parse props: %w", err)
	}
	return Props{
		ModelPath:  raw.ModelPath,
		TotalSlots: raw.TotalSlots,
		BuildInfo:  raw.BuildInfo,
		IsSleeping: raw.IsSleeping,
		NCtx:       raw.DefaultGenerationSettings.NCtx,
	}, nil
}

// ── /metrics ──────────────────────────────────────────────────────────────────

// Metrics holds the `llamacpp:*` Prometheus series the upstream server
// actually emits as of build b9080. Available is false when the server
// returned 501 (the `--metrics` flag was not set); the caller may still
// display partial info from /slots.
//
// The upstream README's metric list is out of date — kv_cache_* series do
// NOT exist; the live set is what's mirrored below. Verified live against a
// router-mode b9080-9f5f0e689 server on 2026-05-10.
type Metrics struct {
	Available bool

	// Counters (monotonic, reset on restart).
	PromptTokensTotal           float64
	PromptSecondsTotal          float64
	TokensPredictedTotal        float64
	TokensPredictedSecondsTotal float64
	NDecodeTotal                float64
	NTokensMax                  float64

	// Gauges (instantaneous).
	PromptTokensPerSec  float64 // tok/s, avg prompt throughput
	PredictedPerSec     float64 // tok/s, avg generation throughput
	RequestsProcessing  float64 // in-flight requests
	RequestsDeferred    float64 // queued waiting for a free slot
	NBusySlotsPerDecode float64 // avg busy slots per llama_decode() call
}

// Metrics probes /metrics. A 501 response is treated as "metrics flag not
// set" and returns Available=false with no error — that's a configuration
// state, not a fault. modelID, when non-empty, is appended as `?model=<urlencoded>`
// for router-mode servers (`--models-dir` / `--models-preset`) which need to
// know which backend to scrape.
func (c *Client) Metrics(ctx context.Context, modelID string) (Metrics, error) {
	path := "/metrics"
	if modelID != "" {
		path += "?model=" + url.QueryEscape(modelID)
	}
	req, err := c.newReq(ctx, http.MethodGet, path, true)
	if err != nil {
		return Metrics{}, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return Metrics{}, fmt.Errorf("llama GET /metrics: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotImplemented {
		return Metrics{Available: false}, nil
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxRespBytes))
		return Metrics{}, fmt.Errorf("llama GET /metrics: %s", bytes.TrimSpace(body))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxRespBytes))
	if err != nil {
		return Metrics{}, err
	}
	m := Metrics{Available: true}
	parseLlamaPromMetrics(body, &m)
	return m, nil
}

// parseLlamaPromMetrics scans Prometheus text and extracts the documented
// `llamacpp:*` series. The format is:
//
//	# HELP <name> <text>
//	# TYPE <name> <type>
//	<name>{<labels>} <value> [<timestamp>]
//
// llama-server emits no labels, so we accept the simpler "<name> <value>"
// shape and ignore TYPE/HELP lines. A series may legitimately appear multiple
// times (one per backend/process — though llama-server is single-process); we
// keep the last occurrence, which matches Prometheus scrape semantics.
func parseLlamaPromMetrics(body []byte, m *Metrics) {
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 0, 4096), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		// Strip optional `{labels}` between name and value.
		if i := strings.IndexByte(line, '{'); i >= 0 {
			j := strings.IndexByte(line[i:], '}')
			if j < 0 {
				continue
			}
			line = line[:i] + line[i+j+1:]
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name, valStr := fields[0], fields[1]
		v, err := strconv.ParseFloat(valStr, 64)
		if err != nil {
			continue
		}
		switch name {
		case "llamacpp:prompt_tokens_total":
			m.PromptTokensTotal = v
		case "llamacpp:prompt_seconds_total":
			m.PromptSecondsTotal = v
		case "llamacpp:tokens_predicted_total":
			m.TokensPredictedTotal = v
		case "llamacpp:tokens_predicted_seconds_total":
			m.TokensPredictedSecondsTotal = v
		case "llamacpp:n_decode_total":
			m.NDecodeTotal = v
		case "llamacpp:n_tokens_max":
			m.NTokensMax = v
		case "llamacpp:prompt_tokens_seconds":
			m.PromptTokensPerSec = v
		case "llamacpp:predicted_tokens_seconds":
			m.PredictedPerSec = v
		case "llamacpp:requests_processing":
			m.RequestsProcessing = v
		case "llamacpp:requests_deferred":
			m.RequestsDeferred = v
		case "llamacpp:n_busy_slots_per_decode":
			m.NBusySlotsPerDecode = v
		}
	}
}

// ── /slots ────────────────────────────────────────────────────────────────────

// SlotProgress is the generation progress carried by /slots. Older builds
// returned this as a flat object (`next_token: {...}`); current builds (≥
// b9000-ish) return an array `next_token: [{...}]` with one entry per
// candidate (used by speculative decoding). Slot.NextToken below uses a
// custom unmarshaller that accepts either form.
type SlotProgress struct {
	HasNextToken bool `json:"has_next_token"`
	NRemain      int  `json:"n_remain"`
	NDecoded     int  `json:"n_decoded"`
}

// nextTokenField is the unmarshalling target for the `next_token` field. It
// flattens both shapes upstream emits to a single SlotProgress: the legacy
// object, and the current single-element array. Multi-element arrays are
// reduced to the first entry — speculative-decoding candidates beyond the
// accepted one aren't useful for a status panel.
type nextTokenField SlotProgress

func (n *nextTokenField) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	if data[0] == '[' {
		var arr []SlotProgress
		if err := json.Unmarshal(data, &arr); err != nil {
			return err
		}
		if len(arr) > 0 {
			*n = nextTokenField(arr[0])
		}
		return nil
	}
	var obj SlotProgress
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	*n = nextTokenField(obj)
	return nil
}

// SlotParams is the sampler+budget subset of /slots' nested `params{...}`.
// We deliberately omit `generation_prompt`, `samplers`, `lora`, and similar
// fields that either leak user prompts or aren't actionable in a status
// panel. The remaining fields characterize the *current task* on the slot
// — useful for verifying what's running but not for reconstructing it.
type SlotParams struct {
	Temperature     float64 `json:"temperature"`
	TopK            int     `json:"top_k"`
	TopP            float64 `json:"top_p"`
	MinP            float64 `json:"min_p"`
	NPredict        int     `json:"n_predict"`
	NKeep           int     `json:"n_keep"`
	ChatFormat      string  `json:"chat_format"`
	ReasoningFormat string  `json:"reasoning_format"`
	SpeculativeType string  `json:"speculative.type"`
}

// Slot is the per-slot subset the UI renders. Upstream returns the full
// sampler `params{...}` and a richer `next_token` — we lift the
// non-prompt-leaking parts plus the prompt itself. The panel sits behind
// trusted_networks so prompt exposure is acceptable for now; tighten this
// down (gate behind a config flag, truncate, redact) if the trust boundary
// ever widens.
type Slot struct {
	ID           int            `json:"id"`
	IDTask       int            `json:"id_task"`
	NCtx         int            `json:"n_ctx"`
	IsProcessing bool           `json:"is_processing"`
	Speculative  bool           `json:"speculative"`
	Prompt       string         `json:"prompt"`
	Params       SlotParams     `json:"params"`
	NextToken    nextTokenField `json:"next_token"`
}

// Progress returns the per-slot SlotProgress regardless of whether upstream
// emitted the legacy object or the newer array shape.
func (s Slot) Progress() SlotProgress { return SlotProgress(s.NextToken) }

// Slots probes /slots. A 501 response is mapped to a typed sentinel so the
// caller can render a "slots disabled (--no-slots)" hint instead of an error.
// modelID, when non-empty, targets a specific backend in router-mode servers
// (without it the router returns 400 "model name is missing from the request").
func (c *Client) Slots(ctx context.Context, modelID string) ([]Slot, error) {
	path := "/slots"
	if modelID != "" {
		path += "?model=" + url.QueryEscape(modelID)
	}
	req, err := c.newReq(ctx, http.MethodGet, path, true)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("llama GET /slots: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotImplemented {
		return nil, ErrSlotsDisabled
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxRespBytes))
		return nil, fmt.Errorf("llama GET /slots: %s", bytes.TrimSpace(body))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxRespBytes))
	if err != nil {
		return nil, err
	}
	var slots []Slot
	if err := json.Unmarshal(body, &slots); err != nil {
		return nil, fmt.Errorf("parse slots: %w", err)
	}
	return slots, nil
}

// ErrSlotsDisabled is returned when the server responds 501 to /slots — the
// `--no-slots` flag was set. Distinct from a transport error so the UI can
// surface the configuration state instead of a generic failure.
var ErrSlotsDisabled = fmt.Errorf("llama: /slots disabled on server (--no-slots)")

// EraseSlot aborts whatever task is currently running on the given slot and
// clears its KV cache. Weights and other slots are unaffected — this is the
// cheap-cancel primitive (microseconds), not a model unload. Returns nil on
// success even if the slot was already idle (the upstream server reports
// 200 in both cases).
func (c *Client) EraseSlot(ctx context.Context, slotID int, modelID string) error {
	path := "/slots/" + strconv.Itoa(slotID) + "?action=erase"
	if modelID != "" {
		path += "&model=" + url.QueryEscape(modelID)
	}
	req, err := c.newReq(ctx, http.MethodPost, path, true)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("llama POST %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxRespBytes))
		return fmt.Errorf("llama POST %s: HTTP %d: %s", path, resp.StatusCode, bytes.TrimSpace(body))
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// DrainResult summarizes what a Drain call did. Cancellations counts the
// number of distinct slot tasks the loop aborted (a single click can cancel
// in-flight + pop deferred tasks one by one until quiet). Iterations is the
// total /slots polls performed; useful for distinguishing "nothing was
// queued" (Iterations=1, Cancellations=0) from "we hit the safety cap"
// (Iterations==MaxIterations).
type DrainResult struct {
	Cancellations int
	Iterations    int
	Elapsed       time.Duration
	HitSafetyCap  bool
}

// DrainOptions tunes the loop. Zero values mean defaults: at most 50
// iterations, 100ms between polls, 10s overall budget. The defaults are
// generous for a one-click destructive action; in practice the loop almost
// always exits in 1-3 iterations.
type DrainOptions struct {
	MaxIterations int
	PollInterval  time.Duration
	Deadline      time.Duration
}

// Drain repeatedly erases active slots until /slots reports nothing
// processing. Designed for clearing a backlog of deferred tasks left
// behind by client-side timeouts (e.g. agent CLI gave up but the server
// kept generating, with more requests piling up behind it).
//
// Each iteration:
//  1. Fetch current slot state
//  2. If no slot is processing, return — done
//  3. Erase every processing slot
//  4. Sleep PollInterval, repeat
//
// The loop is bounded by MaxIterations + Deadline so a misbehaving server
// can't pin this goroutine.
func (c *Client) Drain(ctx context.Context, modelID string, opts DrainOptions) (DrainResult, error) {
	if opts.MaxIterations <= 0 {
		opts.MaxIterations = 50
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = 100 * time.Millisecond
	}
	if opts.Deadline <= 0 {
		opts.Deadline = 10 * time.Second
	}
	deadline := time.Now().Add(opts.Deadline)
	var res DrainResult
	for i := 0; i < opts.MaxIterations; i++ {
		res.Iterations++
		if time.Now().After(deadline) {
			res.HitSafetyCap = true
			break
		}
		slots, err := c.Slots(ctx, modelID)
		if err != nil {
			res.Elapsed = time.Since(deadline.Add(-opts.Deadline))
			return res, fmt.Errorf("drain iter %d: %w", i, err)
		}
		busy := 0
		for _, s := range slots {
			if !s.IsProcessing {
				continue
			}
			busy++
			if err := c.EraseSlot(ctx, s.ID, modelID); err != nil {
				// Don't fail the whole drain on a single erase error —
				// the slot may have just finished naturally between the
				// /slots fetch and our erase call.
				continue
			}
			res.Cancellations++
		}
		if busy == 0 {
			break
		}
		select {
		case <-ctx.Done():
			res.Elapsed = time.Since(deadline.Add(-opts.Deadline))
			return res, ctx.Err()
		case <-time.After(opts.PollInterval):
		}
	}
	if res.Iterations >= opts.MaxIterations {
		res.HitSafetyCap = true
	}
	res.Elapsed = time.Since(deadline.Add(-opts.Deadline))
	return res, nil
}

// ── /v1/models ─────────────────────────────────────────────────────────────

// Model is the per-model entry returned by /v1/models. In single-model mode
// the array has one element with Status.Value == "" and only `id` populated.
// In router mode (--models-dir / --models-preset) every preset shows up here
// with a non-empty Status.Value ∈ {unloaded, loading, loaded, sleeping}.
type Model struct {
	ID      string       `json:"id"`
	Aliases []string     `json:"aliases"`
	Object  string       `json:"object"`
	OwnedBy string       `json:"owned_by"`
	Created int64        `json:"created"`
	Status  *ModelStatus `json:"status,omitempty"`
}

// ModelStatus is the router-mode status block. Args and Preset are surfaced
// for diagnostics; the UI shows them on hover. Failed=true with ExitCode=10
// is the typical "this preset's args were rejected" signal.
type ModelStatus struct {
	Value    string   `json:"value"`
	Args     []string `json:"args"`
	Preset   string   `json:"preset"`
	ExitCode int      `json:"exit_code"`
	Failed   bool     `json:"failed"`
}

// IsRouterMode reports whether the response shape indicates router mode —
// i.e. at least one model entry carries a `status` block. Single-model
// servers omit the field entirely.
func IsRouterMode(models []Model) bool {
	for _, m := range models {
		if m.Status != nil && m.Status.Value != "" {
			return true
		}
	}
	return false
}

// Models probes /v1/models. Returns the unwrapped data list — callers use
// IsRouterMode to decide which downstream probes to fan out per model.
func (c *Client) Models(ctx context.Context) ([]Model, error) {
	resp, err := c.do(ctx, http.MethodGet, "/v1/models", true)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxRespBytes))
	if err != nil {
		return nil, err
	}
	var wrapped struct {
		Data []Model `json:"data"`
	}
	if err := json.Unmarshal(body, &wrapped); err != nil {
		return nil, fmt.Errorf("parse models: %w", err)
	}
	return wrapped.Data, nil
}

// LoadModel asks the router to load a preset. Async on the server side:
// the response is {"success": true} as soon as the load is queued, and the
// model's /v1/models status transitions unloaded → loading → loaded over
// the next ~seconds-to-minutes depending on model size. Under --models-max
// LRU eviction, this may unload another model first.
func (c *Client) LoadModel(ctx context.Context, modelID string) error {
	return c.modelLifecycle(ctx, "/models/load", modelID)
}

// UnloadModel evicts a loaded preset, freeing its VRAM. Synchronous: the
// response returns after the child process has shut down.
func (c *Client) UnloadModel(ctx context.Context, modelID string) error {
	return c.modelLifecycle(ctx, "/models/unload", modelID)
}

func (c *Client) modelLifecycle(ctx context.Context, path, modelID string) error {
	body, err := json.Marshal(map[string]string{"model": modelID})
	if err != nil {
		return err
	}
	req, err := c.newReq(ctx, http.MethodPost, path, true)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("llama POST %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		rbody, _ := io.ReadAll(io.LimitReader(resp.Body, maxRespBytes))
		return fmt.Errorf("llama POST %s (model=%s): HTTP %d: %s", path, modelID, resp.StatusCode, bytes.TrimSpace(rbody))
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// ── HTTP helpers ──────────────────────────────────────────────────────────────

func (c *Client) do(ctx context.Context, method, path string, auth bool) (*http.Response, error) {
	req, err := c.newReq(ctx, method, path, auth)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("llama %s %s: %w", method, path, err)
	}
	if resp.StatusCode >= 400 && path != "/health" {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxRespBytes))
		resp.Body.Close()
		return nil, fmt.Errorf("llama %s %s: %s", method, path, bytes.TrimSpace(body))
	}
	return resp, nil
}

func (c *Client) newReq(ctx context.Context, method, path string, auth bool) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint+path, nil)
	if err != nil {
		return nil, err
	}
	if auth && c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	return req, nil
}
