package web

// Mechanical enforcement of the rules in docs/frontend-architecture.md that
// can be checked by walking the source tree. These tests run as part of
// `make check` and exist so the doc's rules don't decay into folklore.
//
// Each test names the §section of the doc it enforces and emits one error
// line per violation with `<file>:<line>: <reason>` so failures are actionable.
//
// New exceptions: if you need to add a documented allowlist entry, update
// BOTH this file (so the test passes) AND the corresponding section of the
// architecture doc (so the exception is durable). Adding only the allowlist
// here defeats the purpose.

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ─── helpers ────────────────────────────────────────────────────────────────

// walkLines invokes fn for every line of every file under root whose path
// matches pred. Skips _test.go to avoid feedback loops on test fixtures.
func walkLines(t *testing.T, root string, pred func(path string) bool, fn func(path string, lineno int, line string)) {
	t.Helper()
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if !pred(path) {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		s := bufio.NewScanner(f)
		s.Buffer(make([]byte, 1<<16), 1<<22) // tolerate long lines (minified vendor files)
		lineno := 0
		for s.Scan() {
			lineno++
			fn(path, lineno, s.Text())
		}
		return s.Err()
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
}

func hasExt(exts ...string) func(string) bool {
	return func(p string) bool {
		for _, e := range exts {
			if strings.HasSuffix(p, e) {
				return true
			}
		}
		return false
	}
}

// ─── §3.3: layout — no HTML/CSS/JS in .go files ─────────────────────────────

// TestGuideline_NoTemplateStringConstantsInGo enforces the rule that HTML
// template content does not live in Go raw-string constants. Pre-M4 the web
// package had ~2300 lines of HTML in template.go; M4 moved every constant
// into templates/*.html.tmpl. This guard prevents regression.
//
// Doc reference: docs/frontend-architecture.md §3.3 + §17 anti-patterns.
//
// Allowed: SVG icon constants (e.g. claudeIconSVG, opencodeIconSVG, faviconSVG)
// — small, content-addressable, and the architecture doc explicitly carves
// them out as exceptions in §4.3. The allowlist is the *name suffix* "SVG"
// or "Icon"; a new icon constant follows the same naming and passes.
var goTemplateConstRe = regexp.MustCompile(`^const\s+([A-Za-z_][A-Za-z0-9_]*)\s*=\s*` + "`")

func TestGuideline_NoTemplateStringConstantsInGo(t *testing.T) {
	walkLines(t, "../web", hasExt(".go"), func(path string, lineno int, line string) {
		m := goTemplateConstRe.FindStringSubmatch(line)
		if m == nil {
			return
		}
		name := m[1]
		// Allowlist: SVG icon constants are explicitly carved out by §4.3.
		// Match suffix-only so the test fails for anything called fooHTML
		// or barTemplate but passes for fooSVG / fooIcon.
		if strings.HasSuffix(name, "SVG") || strings.HasSuffix(name, "Icon") {
			return
		}
		t.Errorf("%s:%d: HTML/template raw-string constant %q lives in .go — see docs/frontend-architecture.md §3.3 (move to templates/*.html.tmpl)", path, lineno, name)
	})
}

// ─── §4 + §6: templates — no inline <style> or <script> bodies ──────────────

// TestGuideline_TemplatesHaveNoInlineCSSOrJS guards M2 and M3: page-level
// CSS lives in static/css/app.css, JS lives in static/js/<page>.js. The
// architecture doc allows a small number of documented exceptions:
//
//   - login.html.tmpl has its own minimal <style> block. It is intentionally
//     standalone (no app.css dependency) so the auth flow does not depend on
//     the rest of the asset pipeline. Documented in §3.2.
//   - promote_picker.html.tmpl and promote_result.html.tmpl have small
//     modal-scoped <script> blocks for keyboard handling (ESC, click-outside).
//     M3 explicitly excluded these from the extraction; they will move when
//     a future cleanup addresses promote modal interactions holistically.
//
// Doc reference: docs/frontend-architecture.md §3.3, §17 anti-patterns.
func TestGuideline_TemplatesHaveNoInlineCSSOrJS(t *testing.T) {
	// File-level allowlist with the *reason* embedded so a reader of a
	// failure understands what was deliberately let through.
	allowed := map[string]string{
		"login.html.tmpl":          "login is a standalone page with no app.css dependency (§3.2)",
		"promote_picker.html.tmpl": "modal-scoped JS for ESC/click-outside; deferred from M3",
		"promote_result.html.tmpl": "modal-scoped JS for ESC/click-outside; deferred from M3",
	}
	openTagRe := regexp.MustCompile(`<(style|script)(\s|>)`)
	srcAttrRe := regexp.MustCompile(`<script[^>]*\bsrc=`)

	walkLines(t, "../web/templates", hasExt(".html.tmpl"), func(path string, lineno int, line string) {
		base := filepath.Base(path)
		if _, ok := allowed[base]; ok {
			return
		}
		m := openTagRe.FindStringSubmatchIndex(line)
		if m == nil {
			return
		}
		// `<script src="...">` is fine — it loads an external module.
		// Only inline <script> bodies are forbidden.
		tag := line[m[2]:m[3]]
		if tag == "script" && srcAttrRe.MatchString(line) {
			return
		}
		t.Errorf("%s:%d: inline <%s> block in template — see docs/frontend-architecture.md §3.3 (move to static/css/ or static/js/)", path, lineno, tag)
	})
}

// ─── §10: no CDN URLs, except documented exceptions ─────────────────────────

// TestGuideline_NoCDNURLsInServedAssets blocks regressions on the M1 work.
// Every third-party JS/CSS file must be vendored under static/vendor/ with
// an entry in LICENSES.md. The one documented exception is CodeMirror, which
// is still loaded from esm.sh because vendoring requires bundling its
// module graph — tracked in §10's "CodeMirror is the outstanding exception"
// subsection. The exception is granted ONLY to static/js/preview.js.
//
// Doc reference: docs/frontend-architecture.md §10, §17 anti-patterns.
func TestGuideline_NoCDNURLsInServedAssets(t *testing.T) {
	bannedHosts := []string{"unpkg.com", "cdn.jsdelivr.net", "esm.sh"}
	for _, root := range []string{"../web/templates", "../web/static"} {
		walkLines(t, root, hasExt(".html.tmpl", ".js", ".css"), func(path string, lineno int, line string) {
			// Skip the vendor directory — vendored files may carry their
			// own upstream comments referencing CDN URLs.
			if strings.Contains(path, "/static/vendor/") {
				return
			}
			for _, host := range bannedHosts {
				if !strings.Contains(line, host) {
					continue
				}
				// CodeMirror via esm.sh is the documented exception.
				if host == "esm.sh" && filepath.Base(path) == "preview.js" {
					continue
				}
				t.Errorf("%s:%d: references CDN %q — vendor it under static/vendor/ per docs/frontend-architecture.md §10\n\t> %s", path, lineno, host, strings.TrimSpace(line))
			}
		})
	}
}

// ─── §6.2: JS modules don't import from URLs (with one exception) ───────────

// TestGuideline_NoURLImportsInJS asserts the rule that production JS only
// uses browser APIs and same-origin module imports. The one carved-out
// exception is CodeMirror in preview.js — see §10. New page modules MUST
// import only from same-origin paths (e.g., "./shared.js" if shared.js
// gets introduced).
//
// Doc reference: docs/frontend-architecture.md §6.2.
var urlImportRe = regexp.MustCompile(`^\s*import\s+.*from\s+"(https?://|//)`)

func TestGuideline_NoURLImportsInJS(t *testing.T) {
	walkLines(t, "../web/static/js", hasExt(".js"), func(path string, lineno int, line string) {
		if !urlImportRe.MatchString(line) {
			return
		}
		// preview.js may import CodeMirror from esm.sh until that is vendored.
		if filepath.Base(path) == "preview.js" && strings.Contains(line, "esm.sh") {
			return
		}
		t.Errorf("%s:%d: cross-origin import in production JS — see docs/frontend-architecture.md §6.2\n\t> %s", path, lineno, strings.TrimSpace(line))
	})
}

// ─── §10: every vendored file is recorded in LICENSES.md ────────────────────

// TestGuideline_VendoredFilesHaveLicenseEntry guards the audit-trail rule:
// every binary blob in static/vendor/ must be traceable to an upstream
// URL and version in LICENSES.md, so upgrades and audits don't have to
// reverse-engineer where each file came from.
//
// Doc reference: docs/frontend-architecture.md §10.
func TestGuideline_VendoredFilesHaveLicenseEntry(t *testing.T) {
	vendorDir := "../web/static/vendor"
	licData, err := os.ReadFile(filepath.Join(vendorDir, "LICENSES.md"))
	if err != nil {
		t.Fatalf("read LICENSES.md: %v", err)
	}
	licStr := string(licData)
	entries, err := os.ReadDir(vendorDir)
	if err != nil {
		t.Fatalf("read vendor dir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || name == "LICENSES.md" {
			continue
		}
		if !strings.Contains(licStr, name) {
			t.Errorf("%s/%s: not referenced in LICENSES.md — record upstream URL + version per docs/frontend-architecture.md §10", vendorDir, name)
		}
	}
}

// ─── §7.4: polling intervals — minimum 5 s for HTMX partials ────────────────

// TestGuideline_HTMXPollingCadenceFloor enforces the rule that polling
// triggers do not fire more than once per 5 seconds. Anything tighter
// than that should be modeled as SSE (see §7.2). The check parses
// `hx-trigger="...every Ns..."` attributes across template files.
//
// Doc reference: docs/frontend-architecture.md §7.4, §9, §17 anti-patterns.
var hxTriggerEveryRe = regexp.MustCompile(`hx-trigger="[^"]*every\s+(\d+)s`)

func TestGuideline_HTMXPollingCadenceFloor(t *testing.T) {
	const minSeconds = 5
	walkLines(t, "../web/templates", hasExt(".html.tmpl"), func(path string, lineno int, line string) {
		ms := hxTriggerEveryRe.FindAllStringSubmatch(line, -1)
		for _, m := range ms {
			secs := 0
			for _, c := range m[1] {
				secs = secs*10 + int(c-'0')
			}
			if secs < minSeconds {
				t.Errorf("%s:%d: hx-trigger polls every %ds — minimum is %ds; if tighter cadence is needed, use SSE (see docs/frontend-architecture.md §7.2)", path, lineno, secs, minSeconds)
			}
		}
	})
}

// ─── §6.2: no eval, no Function constructor ─────────────────────────────────

// TestGuideline_NoEvalOrFunctionConstructor enforces the rule that JS
// modules never execute string-evaluated code paths. Both are dynamic-code
// holes that defeat module scoping and CSP hardening.
//
// Doc reference: docs/frontend-architecture.md §6.2.
var dynamicEvalRe = regexp.MustCompile(`\beval\s*\(|\bnew\s+Function\s*\(`)

func TestGuideline_NoEvalOrFunctionConstructor(t *testing.T) {
	walkLines(t, "../web/static/js", hasExt(".js"), func(path string, lineno int, line string) {
		// Strip line comments + simple string contents to reduce false
		// positives (e.g. a comment that says "// don't use eval here").
		stripped := line
		if i := strings.Index(stripped, "//"); i >= 0 {
			stripped = stripped[:i]
		}
		if !dynamicEvalRe.MatchString(stripped) {
			return
		}
		t.Errorf("%s:%d: dynamic code evaluation (eval / new Function) — see docs/frontend-architecture.md §6.2\n\t> %s", path, lineno, strings.TrimSpace(line))
	})
}

// ─── §4.3: template.HTML only for vetted SVG icon constants ─────────────────

// TestGuideline_TemplateHTMLOnlyForIcons enforces the auto-escape rule.
// `template.HTML(s)` is a deliberate escape bypass; any new use must
// promise that `s` is content-addressable and not derived from user input.
// Today the only allowed callers wrap SVG icon constants (claudeIconSVG,
// opencodeIconSVG). Other uses must be reviewed against §4.3.
//
// Doc reference: docs/frontend-architecture.md §4.3.
var tmplHTMLCallRe = regexp.MustCompile(`template\.HTML\(\s*([A-Za-z_][A-Za-z0-9_]*)\s*\)`)

func TestGuideline_TemplateHTMLOnlyForIcons(t *testing.T) {
	walkLines(t, "../web", hasExt(".go"), func(path string, lineno int, line string) {
		// Skip comments at the head of the line.
		stripped := line
		if i := strings.Index(stripped, "//"); i >= 0 {
			stripped = stripped[:i]
		}
		m := tmplHTMLCallRe.FindStringSubmatch(stripped)
		if m == nil {
			return
		}
		arg := m[1]
		// Same allowlist heuristic as the const test: identifier ends in
		// "SVG" or "Icon" → it's a vetted icon constant.
		if strings.HasSuffix(arg, "SVG") || strings.HasSuffix(arg, "Icon") {
			return
		}
		t.Errorf("%s:%d: template.HTML(%s) escapes auto-escaping for non-icon content — see docs/frontend-architecture.md §4.3", path, lineno, arg)
	})
}
