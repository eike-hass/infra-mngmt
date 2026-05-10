package web

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/eike-hass/infra-mngmt/internal/compose"
	"github.com/eike-hass/infra-mngmt/internal/llama"
)

// llamaLogLines is how many recent log lines we tail into each llama card.
// 150 is large enough to span at least one router-boot block + recent
// activity; the body has a max-height so it scrolls instead of pushing
// downstream cards off-screen.
const llamaLogLines = 150

// llamaProbeBudget caps each parallel probe. Sized to leave a small buffer
// over the HTTP client's 8s timeout so the context cancels cleanly *after*
// the HTTP layer fails — and stays under the 10s panel refresh cadence so
// in-flight probes always complete before the next refresh fires.
const llamaProbeBudget = 8500 * time.Millisecond

// llamaModelView is one router-mode model entry rendered inside a server card.
// In single-model mode the surrounding card has zero models and renders the
// flat metrics+slots block instead.
type llamaModelView struct {
	ID            string
	Status        string // "loaded" | "loading" | "unloaded" | "sleeping"
	Failed        bool
	ExitCode      int
	Args          []string
	Synthetic     bool   // true for the router's auto-injected fallback (id="default", no --model)
	ModelPath     string // value of --model from Args; empty for synthetic / mmproj-only presets
	Metrics       llama.Metrics
	MetricsErr    string
	Slots         []llama.Slot
	SlotsErr      string
	SlotsDisabled bool
}

// extractModelPath pulls the value of `--model PATH` from a preset's args
// list. Returns empty string when the preset has no --model — typical for
// the router's synthetic [default] entry.
func extractModelPath(args []string) string {
	for i, a := range args {
		if (a == "--model" || a == "-m") && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// isSyntheticDefault reports whether a /v1/models entry is the router's
// auto-injected fallback rather than a user-defined preset. Detection: the
// id is "default" AND the args have no --model (so it can never launch).
// The router emits this whenever the INI lacks a [default] section, used
// for requests that arrive without ?model=.
func isSyntheticDefault(id string, modelPath string) bool {
	return id == "default" && modelPath == ""
}

// llamaServerView is one row in the standalone llama view — one card per
// configured llama_servers entry.
type llamaServerView struct {
	Instance string
	Process  string
	Endpoint string

	Health    llama.Health
	HealthErr string

	Props    llama.Props
	HasProps bool
	PropsErr string

	Router    bool
	Models    []llamaModelView
	ModelsErr string

	// Single-model fallback: populated only when Router == false.
	Metrics       llama.Metrics
	MetricsErr    string
	Slots         []llama.Slot
	SlotsErr      string
	SlotsDisabled bool

	// Tail of process-compose log buffer for the underlying process — fetched
	// in the same poll cycle as the rest of the card so it tracks live state.
	// LogsErr non-empty when the compose instance is unreachable or the
	// process name doesn't exist there.
	Logs    []compose.LogLine
	LogsErr string
}

type llamaPageData struct {
	Servers []llamaServerView
}

// handleLlamaAll renders the standalone "llama" view body — one card per
// configured llama-server. Auto-refreshes every 5s via HTMX.
func (s *Server) handleLlamaAll(w http.ResponseWriter, r *http.Request) {
	servers := s.llamaServers
	if len(servers) == 0 {
		s.renderLlamaPage(w, llamaPageData{})
		return
	}

	out := make([]llamaServerView, len(servers))
	var wg sync.WaitGroup
	for i, e := range servers {
		i, e := i, e
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = s.probeLlamaServer(r.Context(), e)
		}()
	}
	wg.Wait()

	sort.Slice(out, func(i, j int) bool {
		if out[i].Instance != out[j].Instance {
			return out[i].Instance < out[j].Instance
		}
		return out[i].Process < out[j].Process
	})
	s.renderLlamaPage(w, llamaPageData{Servers: out})
}

// probeLlamaServer fans out the four canonical probes (health, props, models,
// then per-model slots/metrics in router mode) for one server. Single-model
// servers go straight to flat /metrics + /slots without ?model=.
func (s *Server) probeLlamaServer(ctx context.Context, e LlamaEntry) llamaServerView {
	c := s.llamaClients[llamaKey(e.Instance, e.Process)]
	v := llamaServerView{Instance: e.Instance, Process: e.Process}
	if c == nil {
		// SetLlamaServers wasn't called for this entry — defensive only.
		return v
	}
	v.Endpoint = c.Endpoint()

	var (
		mu    sync.Mutex
		wg    sync.WaitGroup
		probe = func(fn func(context.Context)) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				cctx, cancel := context.WithTimeout(ctx, llamaProbeBudget)
				defer cancel()
				fn(cctx)
			}()
		}
		models    []llama.Model
		modelsErr error
	)

	probe(func(cctx context.Context) {
		h, err := c.Health(cctx)
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			v.HealthErr = err.Error()
			return
		}
		v.Health = h
	})
	probe(func(cctx context.Context) {
		p, err := c.Props(cctx)
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			v.PropsErr = err.Error()
			return
		}
		v.Props = p
		v.HasProps = true
	})
	probe(func(cctx context.Context) {
		m, err := c.Models(cctx)
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			modelsErr = err
			return
		}
		models = m
	})
	// Tail the process-compose log buffer for the underlying process. Runs in
	// parallel with the llama-server probes since it hits a different
	// endpoint (PC's REST, not llama's HTTP) and can fail independently.
	probe(func(cctx context.Context) {
		pc := s.findComposeClient(e.Instance)
		if pc == nil {
			mu.Lock()
			defer mu.Unlock()
			v.LogsErr = "compose instance " + e.Instance + " not configured"
			return
		}
		lines, err := pc.Logs(cctx, e.Process, llamaLogLines)
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			v.LogsErr = err.Error()
			return
		}
		v.Logs = lines
	})

	wg.Wait()

	switch {
	case modelsErr != nil:
		v.ModelsErr = modelsErr.Error()
	case llama.IsRouterMode(models):
		v.Router = true
		v.Models = s.probeRouterModels(ctx, c, models)
	default:
		// Single-model server: flat metrics + slots.
		flat := s.probeFlatLlama(ctx, c)
		v.Metrics = flat.Metrics
		v.MetricsErr = flat.MetricsErr
		v.Slots = flat.Slots
		v.SlotsErr = flat.SlotsErr
		v.SlotsDisabled = flat.SlotsDisabled
	}
	return v
}

// flatLlamaProbe is the metrics+slots subset for a single backend, used both
// for non-router servers (no ?model=) and per loaded model in router mode.
type flatLlamaProbe struct {
	Metrics       llama.Metrics
	MetricsErr    string
	Slots         []llama.Slot
	SlotsErr      string
	SlotsDisabled bool
}

func (s *Server) probeFlatLlama(ctx context.Context, c *llama.Client) flatLlamaProbe {
	return s.probeFlatLlamaForModel(ctx, c, "")
}

func (s *Server) probeFlatLlamaForModel(ctx context.Context, c *llama.Client, modelID string) flatLlamaProbe {
	var (
		out flatLlamaProbe
		mu  sync.Mutex
		wg  sync.WaitGroup
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		cctx, cancel := context.WithTimeout(ctx, llamaProbeBudget)
		defer cancel()
		m, err := c.Metrics(cctx, modelID)
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			out.MetricsErr = err.Error()
			return
		}
		out.Metrics = m
	}()
	go func() {
		defer wg.Done()
		cctx, cancel := context.WithTimeout(ctx, llamaProbeBudget)
		defer cancel()
		sl, err := c.Slots(cctx, modelID)
		mu.Lock()
		defer mu.Unlock()
		if errors.Is(err, llama.ErrSlotsDisabled) {
			out.SlotsDisabled = true
			return
		}
		if err != nil {
			out.SlotsErr = err.Error()
			return
		}
		out.Slots = sl
	}()
	wg.Wait()
	return out
}

// probeRouterModels iterates the model list. Loaded models get a per-model
// probe in parallel; unloaded/sleeping/failed models render with status only.
func (s *Server) probeRouterModels(ctx context.Context, c *llama.Client, models []llama.Model) []llamaModelView {
	out := make([]llamaModelView, len(models))
	var wg sync.WaitGroup
	for i, m := range models {
		i, m := i, m
		out[i] = llamaModelView{ID: m.ID}
		if m.Status != nil {
			out[i].Status = m.Status.Value
			out[i].Failed = m.Status.Failed
			out[i].ExitCode = m.Status.ExitCode
			out[i].Args = m.Status.Args
			out[i].ModelPath = extractModelPath(m.Status.Args)
			out[i].Synthetic = isSyntheticDefault(m.ID, out[i].ModelPath)
		}
		// Only probe live backends. Unloaded/failed have nothing to scrape.
		if out[i].Status != "loaded" && out[i].Status != "sleeping" {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			flat := s.probeFlatLlamaForModel(ctx, c, m.ID)
			out[i].Metrics = flat.Metrics
			out[i].MetricsErr = flat.MetricsErr
			out[i].Slots = flat.Slots
			out[i].SlotsErr = flat.SlotsErr
			out[i].SlotsDisabled = flat.SlotsDisabled
		}()
	}
	wg.Wait()
	// Surface loaded models first so the eye doesn't have to scan past
	// unloaded presets to find what's actually serving.
	sort.SliceStable(out, func(i, j int) bool {
		return modelStatusRank(out[i].Status) < modelStatusRank(out[j].Status)
	})
	return out
}

// modelStatusRank orders router-mode model entries: loaded → sleeping → loading
// → unloaded → failed. Lower rank renders first.
func modelStatusRank(status string) int {
	switch status {
	case "loaded":
		return 0
	case "sleeping":
		return 1
	case "loading":
		return 2
	case "unloaded":
		return 3
	default:
		return 4
	}
}

func (s *Server) renderLlamaPage(w http.ResponseWriter, data llamaPageData) {
	tmpl := template.Must(template.New("llama").Funcs(tmplFuncs).Parse(llamaPageHTML))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.Execute(w, data); err != nil {
		log.Printf("llama page render: %v", err)
	}
}

// pctClass maps a 0..1 ratio to a CSS class for color coding.
func pctClass(ratio float64) string {
	switch {
	case ratio >= 0.9:
		return "high"
	case ratio >= 0.6:
		return "med"
	default:
		return "low"
	}
}

// pctWidth converts a 0..1 ratio to an integer percent for CSS width:.
func pctWidth(ratio float64) int {
	if ratio < 0 {
		return 0
	}
	if ratio > 1 {
		return 100
	}
	return int(ratio * 100)
}

// formatTokensPerSec is the gauge label. llama-server reports throughput in
// tokens/second already; we round to one decimal for display.
func formatTokensPerSec(v float64) string {
	if v == 0 {
		return "—"
	}
	return fmt.Sprintf("%.1f tok/s", v)
}

// exitCodeHelp returns a short human-readable interpretation of a router
// child's exit code. Source: upstream router writes the literal child
// exit code (no sentinels) per server-models.cpp:849; subprocess.h:1043-1050
// uses 99 for Windows TerminateProcess and 1 for Unix SIGKILL via
// subprocess_terminate. Other small codes are llama-server's own non-clean
// shutdown returning N != 0 from clean_up()/destructors. Upstream conflates
// 'evicted with bad exit' and 'failed to launch' under the same failed
// flag — the exit code is the only available discriminator without
// session-history tracking, and even that is heuristic, so we just label
// each code with its known platform meaning and let the operator decide.
func exitCodeHelp(code int) string {
	switch code {
	case 99:
		return "Windows force-kill (subprocess_terminate after --stop-timeout). Likely an LRU eviction whose child didn't shut down in time."
	case 1:
		return "Unix SIGKILL via subprocess_terminate (timeout) or generic child failure."
	case 0:
		return "clean exit — should not appear with failed=true."
	default:
		return "child llama-server returned a non-zero exit code from its own shutdown path (clean_up / destructors). Could be an unclean eviction or a real launch failure — upstream JSON does not distinguish."
	}
}

// statusHelp returns the long-form explanation of a router-mode model's
// lifecycle state, used as the title= tooltip on the status pill. Mirrors
// the values in /v1/models[].status.value documented in upstream
// tools/server/server-models.cpp.
func statusHelp(status string) string {
	switch status {
	case "loaded":
		return "model is in VRAM and serving requests"
	case "sleeping":
		return "model unloaded from VRAM by --sleep-idle-seconds; KV cache lost. First request after this state pays a re-load cost."
	case "loading":
		return "preset is being launched / weights mmap'd into VRAM"
	case "unloaded":
		return "preset is registered but no child process is running. Either it failed to launch (check failed pill) or --models-max kept it from loading."
	default:
		return "lifecycle state unknown — likely a build that doesn't expose status.value"
	}
}

// formatCount formats a cumulative counter compactly (k/M suffix). Used for
// the "decoded" tile — total tokens predicted since process start.
func formatCount(v float64) string {
	switch {
	case v == 0:
		return "—"
	case v < 1000:
		return fmt.Sprintf("%.0f", v)
	case v < 1_000_000:
		return fmt.Sprintf("%.1fk", v/1000)
	default:
		return fmt.Sprintf("%.1fM", v/1_000_000)
	}
}

// slotProgress returns the per-slot progress percent (0..100) given the
// generation counters from /slots' next_token{}. Returns 0 when no work is
// scheduled (n_decoded + n_remain == 0) so the bar is empty rather than NaN.
func slotProgress(decoded, remain int) int {
	total := decoded + remain
	if total <= 0 {
		return 0
	}
	pct := decoded * 100 / total
	if pct > 100 {
		return 100
	}
	if pct < 0 {
		return 0
	}
	return pct
}

// addInts is a tiny template helper for "{{add x y}}".
func addInts(a, b int) int { return a + b }
