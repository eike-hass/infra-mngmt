package compose

import (
	"reflect"
	"testing"
)

func TestBootstrapArgs(t *testing.T) {
	cases := []struct {
		name        string
		composeFile string
		port        string
		tokenFile   string
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
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := bootstrapArgs(tc.composeFile, tc.port, tc.tokenFile)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("bootstrapArgs(%q,%q,%q) = %v, want %v",
					tc.composeFile, tc.port, tc.tokenFile, got, tc.want)
			}
		})
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
