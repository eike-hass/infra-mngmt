package compose

import (
	"context"
	"os"
	"reflect"
	"runtime"
	"testing"
)

func TestBootstrapArgs(t *testing.T) {
	cases := []struct {
		name        string
		composeFile string
		port        string
		tokenFile   string
		extraFiles  []string
		want        []string
	}{
		{
			name:        "no port no token",
			composeFile: "/etc/pc.yaml",
			want:        []string{"-f", "/etc/pc.yaml", "--tui=false", "--keep-project"},
		},
		{
			name:        "with port",
			composeFile: "/etc/pc.yaml",
			port:        "9998",
			want:        []string{"-f", "/etc/pc.yaml", "--tui=false", "--keep-project", "--port", "9998"},
		},
		{
			name:        "with token file",
			composeFile: "/etc/pc.yaml",
			port:        "9999",
			tokenFile:   "/etc/pc.token",
			want:        []string{"-f", "/etc/pc.yaml", "--tui=false", "--keep-project", "--port", "9999", "--token-file", "/etc/pc.token"},
		},
		{
			name:        "with extra files",
			composeFile: "/etc/pc.yaml",
			port:        "9999",
			extraFiles:  []string{"/etc/pc.bridges.yaml", "/etc/pc.override.yaml"},
			want: []string{
				"-f", "/etc/pc.yaml",
				"-f", "/etc/pc.bridges.yaml",
				"-f", "/etc/pc.override.yaml",
				"--tui=false", "--keep-project", "--port", "9999",
			},
		},
		{
			name:        "skips empty extra entries",
			composeFile: "/etc/pc.yaml",
			extraFiles:  []string{"", "/etc/pc.frag.yaml", ""},
			want: []string{
				"-f", "/etc/pc.yaml",
				"-f", "/etc/pc.frag.yaml",
				"--tui=false", "--keep-project",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := bootstrapArgs(tc.composeFile, tc.port, tc.tokenFile, tc.extraFiles)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("bootstrapArgs(%q,%q,%q,%v) = %v, want %v",
					tc.composeFile, tc.port, tc.tokenFile, tc.extraFiles, got, tc.want)
			}
		})
	}
}

func TestFilterExistingDropsMissing(t *testing.T) {
	dir := t.TempDir()
	exists := dir + "/exists.yaml"
	if err := os.WriteFile(exists, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := filterExisting([]string{exists, dir + "/missing.yaml"})
	if len(got) != 1 || got[0] != exists {
		t.Errorf("filterExisting: got %v, want [%s]", got, exists)
	}
}

func TestFilterExistingNilOnEmptyInput(t *testing.T) {
	if got := filterExisting(nil); got != nil {
		t.Errorf("filterExisting(nil) = %v, want nil", got)
	}
}

func TestBuildSpawnCmdLinuxDetachesFromCtx(t *testing.T) {
	// On Linux, spawning a non-.exe binary must NOT bind the child to ctx —
	// otherwise the spawned process-compose dies a few hundred ms after the
	// HTTP request handler returns, taking the WSL-tier supervisor down
	// every time the user clicks "▶ start process-compose". Verified by
	// asserting cmd.Cancel is nil (exec.Command leaves it nil; only
	// exec.CommandContext sets it).
	if runtime.GOOS != "linux" {
		t.Skip("detach behavior is Linux-specific")
	}
	cmd, err := buildSpawnCmd(context.Background(), "/usr/local/bin/process-compose", []string{"-f", "/tmp/x.yaml"})
	if err != nil {
		t.Fatalf("buildSpawnCmd: %v", err)
	}
	if cmd.Cancel != nil {
		t.Error("Linux spawn cmd should not be ctx-bound: cmd.Cancel must be nil so request-ctx cancel doesn't SIGKILL the child")
	}
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid {
		t.Errorf("Linux spawn cmd must have SysProcAttr.Setsid=true to detach the child into its own session; got %+v", cmd.SysProcAttr)
	}
}

func TestBuildSpawnCmdLinuxNonNilSysProcAttr(t *testing.T) {
	// Cross-check against future maintainers who might switch to Setpgid
	// or a different attribute: the child MUST start in a new session/pgid,
	// otherwise SIGINT to infra-mngmt cascades to PC.
	if runtime.GOOS != "linux" {
		t.Skip("detach behavior is Linux-specific")
	}
	cmd, _ := buildSpawnCmd(context.Background(), "/usr/local/bin/process-compose", nil)
	if cmd.SysProcAttr == nil {
		t.Fatal("SysProcAttr must be set to detach from parent process group")
	}
}

func TestIsWindowsBinary(t *testing.T) {
	cases := map[string]bool{
		"/c/Windows/System32/foo.exe":      true,
		"/mnt/c/tools/process-compose.exe": true,
		"/usr/local/bin/process-compose":   false,
		"/some/path/foo.EXE":               true,
		"":                                 false,
	}
	for in, want := range cases {
		if got := isWindowsBinary(in); got != want {
			t.Errorf("isWindowsBinary(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestPortFromEndpoint(t *testing.T) {
	cases := map[string]string{
		"http://localhost:9998":   "9998",
		"http://wsl-windows:9999": "9999",
		"https://example.com:443": "443",
		"http://example.com":      "",
		"":                        "",
	}
	for in, want := range cases {
		if got := portFromEndpoint(in); got != want {
			t.Errorf("portFromEndpoint(%q) = %q, want %q", in, got, want)
		}
	}
}
