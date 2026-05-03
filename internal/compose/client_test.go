package compose

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProcessStateIsRunning(t *testing.T) {
	cases := map[string]bool{
		"Running": true, "running": true, "Launched": true, "starting": true, "Restarting": true,
		"Stopped": false, "Error": false, "Completed": false, "Disabled": false, "": false,
	}
	for status, want := range cases {
		if got := (ProcessState{Status: status}).IsRunning(); got != want {
			t.Errorf("IsRunning(%q) = %v, want %v", status, got, want)
		}
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
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("missing/wrong auth header: %q", r.Header.Get("Authorization"))
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
	want := []string{"/process/foo/start", "/process/foo/stop", "/process/foo/restart"}
	if len(hits) != 3 {
		t.Fatalf("expected 3 requests, got %v", hits)
	}
	for i, p := range want {
		if hits[i] != p {
			t.Errorf("hit %d = %q, want %q", i, hits[i], p)
		}
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

func TestEndpointTrailingSlashTrimmed(t *testing.T) {
	c := New("test", "http://foo/", "")
	if c.Endpoint() != "http://foo" {
		t.Errorf("trailing slash not trimmed: %q", c.Endpoint())
	}
}
