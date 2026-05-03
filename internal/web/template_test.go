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
			Processes: []compose.ProcessState{{Name: "socat-llama", Status: "Running", Pid: 123, CPU: 0.1, Mem: 4096, Age: 60}},
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
