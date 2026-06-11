package compose

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProcessesUnmarshalsAllFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{
			"name":"socat","namespace":"default","status":"Running","system_time":"1m30s",
			"is_ready":"Ready","has_ready_probe":true,"pid":42,"exit_code":0,"restarts":1,
			"cpu":3.5,"mem":1048576,"is_running":true
		}]}`))
	}))
	defer srv.Close()
	c := New("test", srv.URL, "")
	procs, err := c.Processes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(procs) != 1 {
		t.Fatalf("got %d procs, want 1", len(procs))
	}
	p := procs[0]
	if p.Name != "socat" || p.Namespace != "default" || p.Status != "Running" ||
		p.SystemTime != "1m30s" || p.Health != "Ready" || !p.HasHealthProbe ||
		p.Pid != 42 || p.Restarts != 1 || p.CPU != 3.5 || p.Mem != 1048576 || !p.IsRunning {
		t.Errorf("fields not unmarshalled correctly: %+v", p)
	}
}

func TestPing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/live" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()
	c := New("test", srv.URL, "")
	if !c.Ping(context.Background()) {
		t.Error("expected Ping to succeed")
	}
}

func TestPingFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()
	c := New("test", srv.URL, "")
	if c.Ping(context.Background()) {
		t.Error("Ping should return false on 500")
	}
}

func TestPingUnreachable(t *testing.T) {
	c := New("test", "http://127.0.0.1:1", "")
	if c.Ping(context.Background()) {
		t.Error("Ping to closed port should return false")
	}
}

func TestProcessesFlatArray(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"name":"foo","status":"Running","pid":42}]`))
	}))
	defer srv.Close()
	c := New("test", srv.URL, "")
	procs, err := c.Processes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(procs) != 1 || procs[0].Name != "foo" || procs[0].Pid != 42 {
		t.Errorf("got %+v", procs)
	}
}

func TestProcessesWrappedArray(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{"name":"bar","status":"Stopped"}]}`))
	}))
	defer srv.Close()
	c := New("test", srv.URL, "")
	procs, err := c.Processes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(procs) != 1 || procs[0].Name != "bar" {
		t.Errorf("wrapped form not parsed: %+v", procs)
	}
}

func TestProcessesAuthHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-PC-Token-Key") != "secret" {
			t.Errorf("missing/wrong auth header: %q", r.Header.Get("X-PC-Token-Key"))
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization header should not be set, got %q", got)
		}
		w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	c := New("test", srv.URL, "secret")
	if _, err := c.Processes(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestStartStopRestart(t *testing.T) {
	type call struct {
		method string
		path   string
	}
	var hits []call
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, call{r.Method, r.URL.Path})
		w.WriteHeader(200)
	}))
	defer srv.Close()
	c := New("test", srv.URL, "")
	ctx := context.Background()
	if err := c.Start(ctx, "foo"); err != nil {
		t.Fatal(err)
	}
	if err := c.Stop(ctx, "foo"); err != nil {
		t.Fatal(err)
	}
	if err := c.Restart(ctx, "foo"); err != nil {
		t.Fatal(err)
	}
	// process-compose uses verb-first URLs. Start/Restart are POST, Stop is
	// PATCH — that's how the upstream gin router registers them.
	want := []call{
		{http.MethodPost, "/process/start/foo"},
		{http.MethodPatch, "/process/stop/foo"},
		{http.MethodPost, "/process/restart/foo"},
	}
	if len(hits) != 3 {
		t.Fatalf("expected 3 requests, got %v", hits)
	}
	for i, w := range want {
		if hits[i] != w {
			t.Errorf("hit %d = %+v, want %+v", i, hits[i], w)
		}
	}
}

func TestStopNotRunningIsIdempotent(t *testing.T) {
	// Stopping an already-exited process is a no-op, not an error. process-compose
	// returns 500 {"error":"process p is not running"} when the process self-exited
	// before the stop landed; Stop must swallow that so the UI doesn't surface a
	// spurious error toast for what is really a no-op.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"process p is not running"}`))
	}))
	defer srv.Close()
	c := New("test", srv.URL, "")
	if err := c.Stop(context.Background(), "p"); err != nil {
		t.Errorf("Stop on a not-running process must be idempotent (nil), got: %v", err)
	}
}

func TestStopRealErrorStillBubbles(t *testing.T) {
	// A genuine failure (anything other than "is not running") must still surface.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := New("test", srv.URL, "")
	if err := c.Stop(context.Background(), "p"); err == nil {
		t.Error("a non-'not running' stop error must still bubble up")
	}
}

func TestReload(t *testing.T) {
	var hits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		hits = append(hits, r.URL.Path)
		w.WriteHeader(200)
	}))
	defer srv.Close()
	c := New("test", srv.URL, "")
	if err := c.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0] != "/project/configuration" {
		t.Errorf("expected one POST to /project/configuration, got %v", hits)
	}
}

func TestReloadSurfacesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "config invalid", http.StatusBadRequest)
	}))
	defer srv.Close()
	c := New("test", srv.URL, "")
	if err := c.Reload(context.Background()); err == nil {
		t.Fatal("expected error when reload returns 400")
	}
}

func TestLogsURLIsPathBased(t *testing.T) {
	var hit string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = r.URL.Path
		w.Write([]byte(`{"logs":[]}`))
	}))
	defer srv.Close()
	c := New("test", srv.URL, "")
	if _, err := c.Logs(context.Background(), "myproc", 200); err != nil {
		t.Fatal(err)
	}
	want := "/process/logs/myproc/0/200"
	if hit != want {
		t.Errorf("logs URL = %q, want %q (process-compose uses path params)", hit, want)
	}
}

func TestLogsStringArrayFormat(t *testing.T) {
	// This is what process-compose actually returns from
	// /process/logs/{name}/{endOffset}/{limit}.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"logs":["first line\r","second line\n","third line"]}`))
	}))
	defer srv.Close()
	c := New("test", srv.URL, "")
	logs, err := c.Logs(context.Background(), "p", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 3 {
		t.Fatalf("expected 3 lines, got %d: %+v", len(logs), logs)
	}
	if logs[0].Message != "first line" {
		t.Errorf("trim trailing CR: got %q", logs[0].Message)
	}
	if logs[1].Message != "second line" {
		t.Errorf("trim trailing LF: got %q", logs[1].Message)
	}
}

func TestLogsWrappedFormat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"logs":[{"time":"t1","message":"hello"}]}`))
	}))
	defer srv.Close()
	c := New("test", srv.URL, "")
	logs, err := c.Logs(context.Background(), "p", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 || logs[0].Message != "hello" {
		t.Errorf("got %+v", logs)
	}
}

func TestLogsPlainTextFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("line1\nline2\n  \nline3\n"))
	}))
	defer srv.Close()
	c := New("test", srv.URL, "")
	logs, err := c.Logs(context.Background(), "p", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 3 {
		t.Errorf("expected 3 lines (blank skipped), got %d: %+v", len(logs), logs)
	}
	if logs[0].Message != "line1" {
		t.Errorf("got %+v", logs)
	}
}

func TestLogsEmptyWrappedReturnsNoLines(t *testing.T) {
	// process-compose returns {"logs":[]} for a process with an empty buffer
	// (Disabled / never-started). This must yield ZERO lines so the template
	// renders its "no logs" empty state — NOT one line containing the literal
	// JSON, which is what the plain-text fallback would (wrongly) produce.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"logs":[]}`))
	}))
	defer srv.Close()
	c := New("test", srv.URL, "")
	logs, err := c.Logs(context.Background(), "p", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 0 {
		t.Errorf(`empty {"logs":[]} must yield 0 lines, got %d: %+v`, len(logs), logs)
	}
}

func TestErrorResponseBubblesUp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("boom"))
	}))
	defer srv.Close()
	c := New("test", srv.URL, "")
	if _, err := c.Processes(context.Background()); err == nil {
		t.Error("expected error from 500")
	}
}

func TestProcessesCapsResponseBody(t *testing.T) {
	// A misbehaving upstream returns far more than maxRespBytes. We must read
	// at most the cap (so we can't OOM); the resulting truncated JSON then
	// fails both unmarshal shapes rather than being fully buffered.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"name":"`))
		_, _ = io.Copy(w, io.LimitReader(neverEnding{}, maxRespBytes+(1<<20)))
	}))
	defer srv.Close()
	c := New("test", srv.URL, "")
	if _, err := c.Processes(context.Background()); err == nil {
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

func TestEndpointTrailingSlashTrimmed(t *testing.T) {
	c := New("test", "http://foo/", "")
	if c.Endpoint() != "http://foo" {
		t.Errorf("trailing slash not trimmed: %q", c.Endpoint())
	}
}
