package docker

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// fakeRunner records calls and returns a fixed result.
type fakeRunner struct {
	calls  [][]string // per call: {name, args...}
	out    []byte
	err    error
	onCall func(name string, args ...string) ([]byte, error)
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	rec := append([]string{name}, args...)
	f.calls = append(f.calls, rec)
	if f.onCall != nil {
		return f.onCall(name, args...)
	}
	return f.out, f.err
}

func TestComposeArgsBuildsLeadingFlags(t *testing.T) {
	c := &Compose{File: "/tmp/foo.yaml", Project: "myproj"}
	got := c.args("up", "-d")
	want := []string{"compose", "-f", "/tmp/foo.yaml", "-p", "myproj", "up", "-d"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("args() = %v, want %v", got, want)
	}
}

func TestComposeArgsOmitsProjectWhenEmpty(t *testing.T) {
	c := &Compose{File: "/tmp/foo.yaml"}
	got := c.args("ps")
	want := []string{"compose", "-f", "/tmp/foo.yaml", "ps"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("args() = %v, want %v", got, want)
	}
}

func TestComposeUpInvokesDocker(t *testing.T) {
	r := &fakeRunner{out: []byte("Container foo  Started\n")}
	c := &Compose{File: "f.yaml", Project: "p", Runner: r}
	if _, err := c.Up(context.Background()); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(r.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(r.calls))
	}
	want := []string{"docker", "compose", "-f", "f.yaml", "-p", "p", "up", "-d", "--no-recreate"}
	if !reflect.DeepEqual(r.calls[0], want) {
		t.Errorf("call = %v, want %v", r.calls[0], want)
	}
}

func TestComposeDownInvokesDocker(t *testing.T) {
	r := &fakeRunner{out: []byte("")}
	c := &Compose{File: "f.yaml", Runner: r}
	if _, err := c.Down(context.Background()); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	want := []string{"docker", "compose", "-f", "f.yaml", "down"}
	if !reflect.DeepEqual(r.calls[0], want) {
		t.Errorf("call = %v, want %v", r.calls[0], want)
	}
}

func TestComposeUpSurfacesError(t *testing.T) {
	r := &fakeRunner{
		out: []byte("error response from daemon: pull access denied"),
		err: errors.New("exit status 1"),
	}
	c := &Compose{File: "f.yaml", Runner: r}
	_, err := c.Up(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !contains(err.Error(), "exit status 1") {
		t.Errorf("error %q should mention underlying exit status", err.Error())
	}
	if !contains(err.Error(), "pull access denied") {
		t.Errorf("error %q should include trimmed combined output", err.Error())
	}
}

func TestComposePSParsesJSONLines(t *testing.T) {
	r := &fakeRunner{out: []byte(
		`{"Name":"vault-mcp-fs-1","Image":"mcp-fs:latest","Service":"mcp-fs","State":"running","Status":"Up 1 minute"}` + "\n" +
			`{"Name":"vault-sidecar-1","Image":"alpine","Service":"sidecar","State":"exited","Status":"Exited (0)"}` + "\n",
	)}
	c := &Compose{File: "f.yaml", Runner: r}
	got, err := c.PS(context.Background())
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 entries, got %d", len(got))
	}
	if got[0].Name != "vault-mcp-fs-1" || got[0].Service != "mcp-fs" || got[0].State != "running" {
		t.Errorf("entry 0 wrong: %+v", got[0])
	}
	if got[1].Service != "sidecar" || got[1].State != "exited" {
		t.Errorf("entry 1 wrong: %+v", got[1])
	}
}

func TestComposePSSkipsMalformedLines(t *testing.T) {
	r := &fakeRunner{out: []byte(
		"\n" +
			`{"Name":"ok","Image":"x","Service":"s","State":"running","Status":""}` + "\n" +
			"this line is not json\n" +
			"   \n" +
			`{"Name":"ok2","Image":"y","Service":"t","State":"running","Status":""}` + "\n",
	)}
	c := &Compose{File: "f.yaml", Runner: r}
	got, err := c.PS(context.Background())
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("want 2 valid entries, got %d (%+v)", len(got), got)
	}
}

func TestComposePSEmptyOutputReturnsEmpty(t *testing.T) {
	r := &fakeRunner{out: []byte{}}
	c := &Compose{File: "f.yaml", Runner: r}
	got, err := c.PS(context.Background())
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want empty, got %v", got)
	}
}

func TestTrimOutputSingleLine(t *testing.T) {
	if got := trimOutput([]byte("short and tidy")); got != "short and tidy" {
		t.Errorf("got %q", got)
	}
}

func TestTrimOutputMultiline(t *testing.T) {
	if got := trimOutput([]byte("first line\nsecond line\nthird")); got != "first line …" {
		t.Errorf("got %q", got)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || (len(sub) > 0 && indexOf(s, sub) >= 0))
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
