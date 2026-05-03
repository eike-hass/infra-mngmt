package web

import (
	"bytes"
	"html/template"
	"strings"
	"testing"

	"github.com/eike-hass/infra-mngmt/internal/compose"
)

func TestServicesTemplateRendersOfflineCard(t *testing.T) {
	views := []instanceView{
		{
			Name:      "wsl",
			Endpoint:  "http://localhost:9998",
			Online:    true,
			CanBoot:   true,
			Processes: []compose.ProcessState{{Name: "socat-llama", Status: "Running", Pid: 123, CPU: 0.1, Mem: 4096, SystemTime: "1m0s", IsRunning: true}},
		},
		{
			Name:     "windows",
			Endpoint: "http://10.255.255.254:9999",
			Online:   false,
			CanBoot:  true,
		},
	}

	tmpl := template.Must(template.New("svc").Funcs(tmplFuncs).Parse(servicesHTML))
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, views); err != nil {
		t.Fatalf("template.Execute error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "windows") {
		t.Errorf("output does not contain 'windows':\n%s", out)
	}
	if !strings.Contains(out, "offline") {
		t.Errorf("output does not contain 'offline':\n%s", out)
	}
	t.Logf("rendered %d bytes", len(out))
	t.Logf("output:\n%s", out)
}

// TestServicesTemplateRendersAPIBadges verifies that the conditional UI bits
// driven by process-compose API fields — health pill, exit code, namespace
// label, system_time — actually render when the corresponding fields are set.
func TestServicesTemplateRendersAPIBadges(t *testing.T) {
	views := []instanceView{{
		Name:     "wsl",
		Endpoint: "http://localhost:9998",
		Online:   true,
		Processes: []compose.ProcessState{
			// Healthy probe-backed process in a non-default namespace
			{Name: "api", Namespace: "web", Status: "Running", IsRunning: true, Pid: 7, SystemTime: "5m12s", Health: "Ready", HasHealthProbe: true},
			// Crashed process — exit code badge should appear
			{Name: "worker", Namespace: "default", Status: "Error", IsRunning: false, ExitCode: 137, SystemTime: "2s"},
		},
	}}
	tmpl := template.Must(template.New("svc").Funcs(tmplFuncs).Parse(servicesHTML))
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, views); err != nil {
		t.Fatalf("template.Execute error: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		`health-pill ready`, `>Ready<`, // health badge for api
		`exit-code`, `>137<`, // exit code badge for worker
		`proc-ns">web<`, // namespace label for api
		`>5m12s<`,       // system_time rendered directly
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	}
	// Default namespace should NOT render the proc-ns label
	if strings.Contains(out, `proc-ns">default<`) {
		t.Errorf("default namespace should be hidden")
	}
}
