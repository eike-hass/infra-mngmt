package web

import (
	"bytes"
	"strings"
	"testing"

	"github.com/eike-hass/infra-mngmt/internal/compose"
)

// renderServicesSections renders every section template into one buffer
// using the given data. Used by tests that assert on cross-cutting concerns
// (shared col-name / col-actions classes, button label harmonization,
// icon coverage across cards) — concerns that the old monolithic template
// surfaced in one execute, but now live in separate section templates.
func renderServicesSections(t *testing.T, data servicesPageData) string {
	t.Helper()
	tmpl := parseTemplate("svc", "templates/services.html.tmpl")
	var buf bytes.Buffer
	for _, section := range []string{"bridges-section", "containers-section", "vaults-section"} {
		if err := tmpl.ExecuteTemplate(&buf, section, data); err != nil {
			t.Fatalf("ExecuteTemplate %s: %v", section, err)
		}
	}
	for _, iv := range data.Instances {
		instData := struct{ Instance instanceView }{Instance: iv}
		if err := tmpl.ExecuteTemplate(&buf, "instance-section", instData); err != nil {
			t.Fatalf("ExecuteTemplate instance-section: %v", err)
		}
	}
	for _, p := range data.OpenDesignProjects {
		cardData := struct{ Card containerProjectView }{Card: p}
		if err := tmpl.ExecuteTemplate(&buf, "open-design-card", cardData); err != nil {
			t.Fatalf("ExecuteTemplate open-design-card: %v", err)
		}
	}
	return buf.String()
}

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

	out := renderServicesSections(t, servicesPageData{Instances: views})
	if !strings.Contains(out, "windows") {
		t.Errorf("output does not contain 'windows':\n%s", out)
	}
	if !strings.Contains(out, "offline") {
		t.Errorf("output does not contain 'offline':\n%s", out)
	}
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
	out := renderServicesSections(t, servicesPageData{Instances: views})
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
	out := renderServicesSections(t, data)
	for _, want := range []string{
		"network bridges",
		"producer-pal",
		"172.18.0.1:3350",
		"Producer Pal MCP",
		`status-pill running`,
		`/bridge/apply?name=producer-pal`,
		// reset now routes through the blast-radius confirm modal
		`/partials/blast-radius?action=bridge-reset&name=producer-pal`,
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
	out := renderServicesSections(t, data)
	for _, want := range []string{
		`class="svc-icon" title="network bridges">⇄<`,
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
	out := renderServicesSections(t, data)
	// 3 tables × (1 th + 1 td) = 6 col-name occurrences.
	if got := strings.Count(out, `class="col-name"`); got < 6 {
		t.Errorf("expected at least 6 col-name usages (1 th + 1 td per table), got %d", got)
	}
	// Button-label harmonization: no legacy glyphs (intent color carries the
	// signal now), and every re-read-yaml button uses the same `reload` label
	// across bridges, containers, and process-compose.
	for _, glyph := range []string{`↻`, `>⟳ `, `>▶ `} {
		if strings.Contains(out, glyph) {
			t.Errorf("found legacy header-button glyph %q; intent class should carry the signal", glyph)
		}
	}
	if strings.Contains(out, `>refresh<`) {
		t.Errorf("found legacy 'refresh' label; should be 'reload' for consistency across cards")
	}
	// In the bridges header, `apply all` must appear BEFORE `reload` so the
	// strong (mutating) action sits left of the safe (read-only) reload.
	applyIdx := strings.Index(out, `>apply all<`)
	reloadIdx := strings.Index(out, `>reload<`)
	if applyIdx < 0 || reloadIdx < 0 || applyIdx > reloadIdx {
		t.Errorf("bridges header order wrong: apply-all=%d reload=%d (apply-all should come first)", applyIdx, reloadIdx)
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
	out := renderServicesSections(t, data)

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
	// Shell-level smoke test: when HasBridges is false the shell omits the
	// #bridges-section placeholder entirely, so the "network bridges"
	// chrome label never reaches the DOM. (Each section's body is fetched
	// from its own endpoint; absence of a placeholder means the section
	// never loads.)
	data := servicesShellData{InstanceNames: []string{"wsl"}}
	tmpl := parseTemplate("svc", "templates/services.html.tmpl")
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "shell", data); err != nil {
		t.Fatalf("template.Execute: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, `id="bridges-section"`) {
		t.Errorf("shell should omit bridges-section placeholder when HasBridges is false; got:\n%s", out)
	}
	if strings.Contains(out, "network bridges") {
		t.Errorf("'network bridges' chrome must not appear in the shell (it lives in bridges-section)")
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
			if err := tmpl.ExecuteTemplate(&buf, "containers-section", data); err != nil {
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
	if err := tmpl.ExecuteTemplate(&buf, "containers-section", data); err != nil {
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
	if err := tmpl.ExecuteTemplate(&buf, "containers-section", data); err != nil {
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
