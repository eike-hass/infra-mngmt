package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/eike-hass/infra-mngmt/internal/deps"
	"github.com/eike-hass/infra-mngmt/internal/entity"
)

// TestResolveMCPStatusesParallelProbes is a regression test for the
// "frontend appears down after reboot" incident: when one configured
// process-compose tier is unreachable, page loads must not stall waiting
// for it. The resolver probes every compose instance in parallel under
// per-endpoint timeouts, so total wall-clock for one healthy + one stuck
// instance must stay near a single composeProbeTimeout, not the sum.
func TestResolveMCPStatusesParallelProbes(t *testing.T) {
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/live":
			w.WriteHeader(http.StatusOK)
		case "/processes":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"name": "alpha", "status": "Running", "is_ready": "Ready"},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer healthy.Close()

	stuck := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer stuck.Close()

	srv := New(
		nil,
		[]ComposeEntry{
			{Name: "wsl", Endpoint: healthy.URL},
			{Name: "windows", Endpoint: stuck.URL},
		},
		"", nil, nil,
		[]deps.Rule{{
			Entity: "mcp:alpha",
			Scope:  "*",
			Needs:  []deps.Need{{Kind: "service", Name: "alpha"}},
		}},
		nil, nil, nil,
	)

	e := entity.Entity{
		ID:    "host:/x:mcp:alpha",
		Kind:  entity.KindMCPServer,
		Name:  "alpha",
		Scope: entity.GlobalScope(),
	}

	// Tight upper bound: a single probe budget plus generous slack for the
	// healthy roundtrip + goroutine scheduling. The pre-fix sequential
	// behavior would have taken ≥2 * composeProbeTimeout here, blowing past
	// this bound.
	deadline := composeProbeTimeout + 500*time.Millisecond

	start := time.Now()
	got := srv.resolveMCPStatuses(context.Background(), []entity.Entity{e})
	elapsed := time.Since(start)

	if elapsed > deadline {
		t.Errorf("resolveMCPStatuses took %v with one stuck endpoint; want ≤ %v", elapsed, deadline)
	}
	st := got[e.ID]
	if st == nil {
		t.Fatalf("no status for %s; got %+v", e.ID, got)
	}
	if st.State != "running" {
		t.Errorf("State = %q, want %q", st.State, "running")
	}
	if st.Process != "alpha" || st.Instance != "wsl" {
		t.Errorf("Process/Instance = %q/%q, want alpha/wsl", st.Process, st.Instance)
	}
}

// TestResolveMCPStatusesAllStuckBoundedByTimeout asserts the worst-case
// path: every compose tier is unreachable. Total wall-clock must still be
// bounded by composeProbeTimeout, not the sum across instances.
func TestResolveMCPStatusesAllStuckBoundedByTimeout(t *testing.T) {
	mkStuck := func() *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		}))
	}
	a, b := mkStuck(), mkStuck()
	defer a.Close()
	defer b.Close()

	srv := New(
		nil,
		[]ComposeEntry{
			{Name: "wsl", Endpoint: a.URL},
			{Name: "windows", Endpoint: b.URL},
		},
		"", nil, nil, nil, nil, nil, nil,
	)

	deadline := composeProbeTimeout + 500*time.Millisecond
	start := time.Now()
	_ = srv.resolveMCPStatuses(context.Background(), nil)
	elapsed := time.Since(start)

	if elapsed > deadline {
		t.Errorf("resolveMCPStatuses took %v with all endpoints stuck; want ≤ %v", elapsed, deadline)
	}
}
