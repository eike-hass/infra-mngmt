package web

import (
	"bytes"
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

	tmpl := parseTemplate("svc", "templates/services.html.tmpl")
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, servicesPageData{Instances: views}); err != nil {
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
//
// Note on health pill: redundant in the running+ready case (the common happy
// path), so the template suppresses it there. We assert it shows only when
// it adds information — see the worker process below for a probe-backed
// process that's NOT ready, which should surface the pill.
func TestServicesTemplateRendersAPIBadges(t *testing.T) {
	views := []instanceView{{
		Name:     "wsl",
		Endpoint: "http://localhost:9998",
		Online:   true,
		Processes: []compose.ProcessState{
			// Healthy probe-backed process in a non-default namespace.
			// Health pill should be SUPPRESSED here — Running+Ready is implicit.
			{Name: "api", Namespace: "web", Status: "Running", IsRunning: true, Pid: 7, SystemTime: "5m12s", Health: "Ready", HasHealthProbe: true},
			// Probe-backed process that's running but probe is failing —
			// health pill MUST surface so the user sees the unhealthy state.
			{Name: "starting", Namespace: "default", Status: "Running", IsRunning: true, Pid: 8, Health: "Not Ready", HasHealthProbe: true},
			// Crashed process — exit code badge should appear
			{Name: "worker", Namespace: "default", Status: "Error", IsRunning: false, ExitCode: 137, SystemTime: "2s"},
		},
	}}
	tmpl := parseTemplate("svc", "templates/services.html.tmpl")
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, servicesPageData{Instances: views}); err != nil {
		t.Fatalf("template.Execute error: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		`health-pill`, `>Not Ready<`, // health badge for the unhealthy probe-backed process
		`exit-code`, `>137<`, // exit code badge for worker
		`proc-ns">web<`, // namespace label for api
		`>5m12s<`,       // system_time rendered directly
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	}
	// Running+Ready combination must NOT render a health pill — it's redundant.
	// (Other processes have a health-pill above; we look for the specific Ready text.)
	if strings.Contains(out, `>Ready<`) {
		t.Errorf("Running+Ready should suppress redundant health pill, but found `>Ready<`:\n%s", out)
	}
	// Default namespace should NOT render the proc-ns label
	if strings.Contains(out, `proc-ns">default<`) {
		t.Errorf("default namespace should be hidden")
	}
}

func TestServicesTemplateRendersBridges(t *testing.T) {
	data := servicesPageData{
		Bridges: []bridgeView{
			{
				Name:        "producer-pal",
				Tier:        "windows",
				Type:        "portproxy+firewall",
				Listen:      "172.18.0.1:3350",
				Connect:     "127.0.0.1:3350",
				State:       "active",
				StateClass:  "running",
				DisplayName: "Producer Pal MCP",
			},
			{
				Name:       "stale",
				Tier:       "windows",
				State:      "missing",
				StateClass: "stopped",
			},
		},
	}
	tmpl := parseTemplate("svc", "templates/services.html.tmpl")
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		t.Fatalf("template.Execute error: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"network bridges",
		"producer-pal",
		"172.18.0.1:3350",
		"Producer Pal MCP",
		`status-pill running`,
		`/bridge/apply?name=producer-pal`,
		`/bridge/reset?name=producer-pal`,
		`/bridges/apply`,
		`svc-running-count">1/2 active`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n", want)
		}
	}
}

// TestServicesTemplateCardIconsAndPidColumn verifies each card header carries
// its identifying icon (⇆ bridges, ⬢ containers, ⚙ compose) and the
// process-compose pid column has the col-pid width hint so wide PIDs don't
// squeeze the rest of the row.
func TestServicesTemplateCardIconsAndPidColumn(t *testing.T) {
	data := servicesPageData{
		Bridges:    []bridgeView{{Name: "b1", State: "active", StateClass: "running", Listen: "x", Connect: "y"}},
		Containers: []containerView{{Name: "c1", State: "running", StateClass: "running"}},
		Docker:     dockerHealthView{Configured: true, Online: true},
		Instances: []instanceView{{
			Name: "wsl", Endpoint: "http://x", Online: true,
			Processes: []compose.ProcessState{{Name: "p1", Status: "Running", IsRunning: true, Pid: 91760}},
		}},
	}
	tmpl := parseTemplate("svc", "templates/services.html.tmpl")
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		t.Fatalf("template.Execute: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		`class="svc-icon" title="network bridges">⇆<`,
		`class="svc-icon" title="docker containers">⬢<`,
		`class="svc-icon" title="process-compose instance">⚙<`,
		`class="col-pid">pid<`,           // header
		`<td class="col-pid">91760</td>`, // body
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output", want)
		}
	}
}

// TestServicesTemplateNameColumnConstrained verifies that all three tables
// share the same .col-name class on first-column header + body cells, so the
// state column starts at the same x-position in every card.
func TestServicesTemplateNameColumnConstrained(t *testing.T) {
	data := servicesPageData{
		Bridges: []bridgeView{{
			Name: "b1", Tier: "windows", State: "active", StateClass: "running",
			Listen: "1.2.3.4:80", Connect: "127.0.0.1:80",
		}},
		Containers: []containerView{{
			Name: "c1", State: "running", StateClass: "running", ID: "abc",
		}},
		Docker: dockerHealthView{Configured: true, Online: true},
		Instances: []instanceView{{
			Name: "wsl", Endpoint: "http://x", Online: true,
			Processes: []compose.ProcessState{{Name: "p1", Status: "Running", IsRunning: true, Pid: 1}},
		}},
	}
	tmpl := parseTemplate("svc", "templates/services.html.tmpl")
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		t.Fatalf("template.Execute: %v", err)
	}
	out := buf.String()
	// 3 tables × (1 th + 1 td) = 6 col-name occurrences.
	if got := strings.Count(out, `class="col-name"`); got < 6 {
		t.Errorf("expected at least 6 col-name usages (1 th + 1 td per table), got %d", got)
	}
	// Refresh-button harmonization: every card-level reload-style button must
	// use the same `⟳ refresh` label — no legacy ↻ glyph and no "reload"
	// label on process-compose.
	if strings.Contains(out, `↻`) {
		t.Errorf("found legacy ↻ glyph; refresh button should use ⟳:\n%s", out)
	}
	if strings.Contains(out, `>⟳ reload<`) {
		t.Errorf("process-compose still uses 'reload' label; should be 'refresh' for consistency")
	}
	// In the bridges header, ▶ apply all must appear BEFORE ⟳ refresh so the
	// strong (mutating) action sits left of the safe (read-only) refresh.
	applyIdx := strings.Index(out, `▶ apply all`)
	refreshIdx := strings.Index(out, `⟳ refresh`)
	if applyIdx < 0 || refreshIdx < 0 || applyIdx > refreshIdx {
		t.Errorf("bridges header order wrong: apply-all=%d refresh=%d (apply-all should come first)", applyIdx, refreshIdx)
	}
}

// TestServicesTemplateActionColumnRightAligned verifies that all three tables
// (bridges, containers, process-compose) tag their action cells with the
// shared col-actions class so the buttons line up flush right regardless of
// table column count.
func TestServicesTemplateActionColumnRightAligned(t *testing.T) {
	data := servicesPageData{
		Bridges: []bridgeView{{
			Name: "b1", Tier: "windows", State: "active", StateClass: "running",
			Listen: "1.2.3.4:80", Connect: "127.0.0.1:80",
		}},
		Containers: []containerView{{
			Name: "c1", State: "running", StateClass: "running", ID: "abc",
		}},
		Docker: dockerHealthView{Configured: true, Online: true, Endpoint: "unix:///x"},
		Instances: []instanceView{{
			Name: "wsl", Endpoint: "http://x", Online: true,
			Processes: []compose.ProcessState{{Name: "p1", Status: "Running", IsRunning: true, Pid: 1}},
		}},
	}
	tmpl := parseTemplate("svc", "templates/services.html.tmpl")
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		t.Fatalf("template.Execute: %v", err)
	}
	out := buf.String()

	// Three tables × (header + body cell) = 6 col-actions hits expected.
	got := strings.Count(out, `class="col-actions"`)
	if got < 6 {
		t.Errorf("expected at least 6 col-actions usages (one per table head + body), got %d\n%s", got, out)
	}
	// The shared <code> styling rule should appear exactly once in CSS, not
	// inline on each cell. Inline font-size:11px on <code> would be a
	// regression of the CSS consolidation.
	if strings.Contains(out, `<code style="font-size:11px"`) {
		t.Errorf("inline font-size on <code> should be replaced by .process-table code rule\n%s", out)
	}
}

func TestServicesTemplateOmitsBridgeSectionWhenEmpty(t *testing.T) {
	data := servicesPageData{
		Instances: []instanceView{{Name: "wsl", Endpoint: "x", Online: true}},
	}
	tmpl := parseTemplate("svc", "templates/services.html.tmpl")
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		t.Fatalf("template.Execute: %v", err)
	}
	if strings.Contains(buf.String(), "network bridges") {
		t.Errorf("bridges section should be hidden when no bridges declared")
	}
}

func TestServicesTemplateRendersDockerHealthLED(t *testing.T) {
	cases := []struct {
		name      string
		health    dockerHealthView
		wantClass string
		wantHost  string
	}{
		{
			name:      "online",
			health:    dockerHealthView{Configured: true, Online: true, Endpoint: "unix:///var/run/docker.sock"},
			wantClass: "online-dot online",
			wantHost:  "unix:///var/run/docker.sock",
		},
		{
			name:      "offline",
			health:    dockerHealthView{Configured: true, Online: false, Endpoint: "tcp://10.0.0.5:2375", Error: "connection refused"},
			wantClass: "online-dot offline",
			wantHost:  "tcp://10.0.0.5:2375",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := servicesPageData{
				Containers: []containerView{{Name: "alpha", State: "running", StateClass: "running"}},
				Docker:     tc.health,
			}
			tmpl := parseTemplate("svc", "templates/services.html.tmpl")
			var buf bytes.Buffer
			if err := tmpl.Execute(&buf, data); err != nil {
				t.Fatalf("template.Execute: %v", err)
			}
			out := buf.String()
			if !strings.Contains(out, tc.wantClass) {
				t.Errorf("missing %q in output:\n%s", tc.wantClass, out)
			}
			if !strings.Contains(out, tc.wantHost) {
				t.Errorf("missing endpoint %q in output:\n%s", tc.wantHost, out)
			}
		})
	}
}

func TestServicesTemplateRendersContainerRunningCount(t *testing.T) {
	data := servicesPageData{
		Containers: []containerView{
			{Name: "alpha", State: "running", StateClass: "running"},
			{Name: "beta", State: "stopped", StateClass: "stopped"},
			{Name: "gamma", State: "running", StateClass: "running"},
		},
		Docker: dockerHealthView{Configured: true, Online: true, Endpoint: "unix:///var/run/docker.sock"},
	}
	tmpl := parseTemplate("svc", "templates/services.html.tmpl")
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		t.Fatalf("template.Execute: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `svc-running-count">2/3 running`) {
		t.Errorf("expected '2/3 running' summary; got:\n%s", out)
	}
}

func TestServicesTemplateOmitsDockerLEDWhenUnconfigured(t *testing.T) {
	data := servicesPageData{
		Containers: []containerView{{Name: "alpha", State: "running", StateClass: "running"}},
		Docker:     dockerHealthView{}, // not configured
	}
	tmpl := parseTemplate("svc", "templates/services.html.tmpl")
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		t.Fatalf("template.Execute: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "online-dot online") || strings.Contains(out, "online-dot offline") {
		t.Errorf("LED should be omitted when Docker is unconfigured:\n%s", out)
	}
	if !strings.Contains(out, "docker: matched by name") {
		t.Errorf("expected fallback endpoint label when not configured")
	}
}
