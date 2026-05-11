// vendor-codemirror fetches the CodeMirror npm packages preview.js needs
// and bundles them into internal/web/static/vendor/codemirror.bundle.js via
// the esbuild Go API. Resolves docs/frontend-architecture.md §10 — drops
// the runtime esm.sh dependency that flaked in ident-browser testing.
//
// Run:   go run ./cmd/vendor-codemirror
// Net:   registry.npmjs.org (allowlisted) — no proxy needed
// Out:   internal/web/static/vendor/codemirror.bundle.js
// Also:  prints a package@version table to stdout for LICENSES.md updates.
//
// Idempotent in the sense that re-running produces the same bundle if
// upstream package versions haven't moved. The seedPackages list pins
// behavior; transitive deps follow each package.json's `dependencies` +
// `peerDependencies` fields (we always resolve to dist-tags.latest of the
// dep, which matches the CodeMirror release model where the @codemirror/*
// suite ships in lockstep).
//
// The production binary build (`make build`) is `go build ./cmd/infra-mngmt`
// — it does NOT include this package, so esbuild stays out of the deployed
// binary. `go build ./...` and `make check` will compile this file (and so
// keep esbuild on go.mod's required list), but no binary persists from
// that compile.
package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
)

// seedPackages are the immediate imports preview.js needs. Transitive
// dependencies are walked recursively from each package's package.json.
//
// Keep in lockstep with the re-export list in entryJS below: every
// `from "X"` clause in entryJS must have its containing package listed
// here.
var seedPackages = []string{
	"codemirror",
	"@codemirror/state",
	"@codemirror/lang-markdown",
}

// entryJS is the bundle's entry point. esbuild reads this as the root
// module and recursively pulls in everything reachable via `import`.
// The named exports here are what preview.js imports at runtime.
const entryJS = `
export { EditorView, basicSetup } from "codemirror";
export { EditorState } from "@codemirror/state";
export { markdown } from "@codemirror/lang-markdown";
`

const outputFile = "internal/web/static/vendor/codemirror.bundle.js"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "vendor-codemirror: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	tmp, err := os.MkdirTemp("", "cm-build-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	fmt.Printf("staging in %s\n", tmp)

	nodeModules := filepath.Join(tmp, "node_modules")
	if err := os.MkdirAll(nodeModules, 0o755); err != nil {
		return err
	}

	// Walk the dependency closure. fetched[pkg] = version once successfully
	// staged; doubles as both a visit-set and the manifest we print at the
	// end for LICENSES.md.
	fetched := map[string]string{}
	queue := append([]string{}, seedPackages...)
	for len(queue) > 0 {
		pkg := queue[0]
		queue = queue[1:]
		if _, ok := fetched[pkg]; ok {
			continue
		}
		ver, deps, err := fetchAndExtract(pkg, nodeModules)
		if err != nil {
			return fmt.Errorf("fetch %s: %w", pkg, err)
		}
		fetched[pkg] = ver
		for _, d := range deps {
			if _, ok := fetched[d]; !ok {
				queue = append(queue, d)
			}
		}
	}
	fmt.Printf("staged %d packages\n", len(fetched))

	// Write the entry module at the root of the staging dir so esbuild
	// resolves bare imports relative to the sibling node_modules tree.
	entryPath := filepath.Join(tmp, "entry.js")
	if err := os.WriteFile(entryPath, []byte(entryJS), 0o644); err != nil {
		return err
	}

	// Resolve absolute output path against the current working directory
	// (the tool expects to be invoked from the repo root).
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	outAbs := filepath.Join(cwd, outputFile)
	if err := os.MkdirAll(filepath.Dir(outAbs), 0o755); err != nil {
		return err
	}

	fmt.Printf("bundling → %s\n", outputFile)
	result := api.Build(api.BuildOptions{
		EntryPoints:       []string{entryPath},
		Bundle:            true,
		Format:            api.FormatESModule,
		Target:            api.ES2020,
		Platform:          api.PlatformBrowser,
		Outfile:           outAbs,
		Write:             true,
		LogLevel:          api.LogLevelWarning,
		AbsWorkingDir:     tmp,
		MinifyWhitespace:  false, // diffability over file size — the bundle is committed
		MinifyIdentifiers: false,
		MinifySyntax:      false,
		Banner: map[string]string{
			"js": "/* CodeMirror 6 bundle — see cmd/vendor-codemirror/. Regenerate with `go run ./cmd/vendor-codemirror`. */",
		},
	})
	for _, w := range result.Warnings {
		fmt.Fprintf(os.Stderr, "  warn: %s\n", w.Text)
	}
	if len(result.Errors) > 0 {
		for _, e := range result.Errors {
			fmt.Fprintf(os.Stderr, "  error: %s\n", e.Text)
		}
		return fmt.Errorf("esbuild reported %d errors", len(result.Errors))
	}

	info, err := os.Stat(outAbs)
	if err != nil {
		return err
	}
	fmt.Printf("wrote %s (%d bytes)\n\n", outputFile, info.Size())

	// Print a manifest sorted by package name. Paste into LICENSES.md.
	names := make([]string, 0, len(fetched))
	for n := range fetched {
		names = append(names, n)
	}
	sort.Strings(names)
	fmt.Println("vendored packages (paste into static/vendor/LICENSES.md):")
	for _, n := range names {
		fmt.Printf("  %s@%s\n", n, fetched[n])
	}
	return nil
}

// fetchAndExtract downloads the npm tarball for pkg at its `latest`
// dist-tag, extracts it into <nodeModules>/<pkg>/, and returns (version,
// deps). deps includes both `dependencies` and `peerDependencies` from
// package.json — esbuild needs both to resolve a complete bundle for
// packages like @codemirror/lang-markdown that declare @codemirror/state
// as a peer.
func fetchAndExtract(pkg, nodeModules string) (string, []string, error) {
	metaURL := "https://registry.npmjs.org/" + pkg
	metaResp, err := http.Get(metaURL)
	if err != nil {
		return "", nil, err
	}
	defer metaResp.Body.Close()
	if metaResp.StatusCode != 200 {
		return "", nil, fmt.Errorf("metadata %s: HTTP %d", pkg, metaResp.StatusCode)
	}

	// Only decode the fields we need. Versions is large for popular
	// packages; we just need the entry for the resolved version.
	var meta struct {
		DistTags struct {
			Latest string `json:"latest"`
		} `json:"dist-tags"`
		Versions map[string]struct {
			Dist struct {
				Tarball string `json:"tarball"`
			} `json:"dist"`
			Dependencies     map[string]string `json:"dependencies"`
			PeerDependencies map[string]string `json:"peerDependencies"`
		} `json:"versions"`
	}
	if err := json.NewDecoder(metaResp.Body).Decode(&meta); err != nil {
		return "", nil, fmt.Errorf("decode metadata: %w", err)
	}
	latest := meta.DistTags.Latest
	if latest == "" {
		return "", nil, fmt.Errorf("no dist-tags.latest")
	}
	v, ok := meta.Versions[latest]
	if !ok {
		return "", nil, fmt.Errorf("latest version %s not present in versions map", latest)
	}
	fmt.Printf("  %s@%s\n", pkg, latest)

	tarResp, err := http.Get(v.Dist.Tarball)
	if err != nil {
		return "", nil, err
	}
	defer tarResp.Body.Close()
	if tarResp.StatusCode != 200 {
		return "", nil, fmt.Errorf("tarball: HTTP %d", tarResp.StatusCode)
	}
	gz, err := gzip.NewReader(tarResp.Body)
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)

	// pkg may be scoped (@codemirror/state) — that's fine; filepath.Join
	// handles the slash. Extract into node_modules/<pkg>/.
	dest := filepath.Join(nodeModules, pkg)
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return "", nil, err
	}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", nil, err
		}
		// npm tarballs prefix every entry with "package/". Strip it.
		name := strings.TrimPrefix(h.Name, "package/")
		if name == "" || name == h.Name {
			continue
		}
		outPath := filepath.Join(dest, name)
		// Defend against tar entries trying to escape via "../" — the
		// resulting absolute path must remain inside dest.
		rel, err := filepath.Rel(dest, outPath)
		if err != nil || strings.HasPrefix(rel, "..") {
			return "", nil, fmt.Errorf("tar path escape: %s", h.Name)
		}
		if h.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(outPath, 0o755); err != nil {
				return "", nil, err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			return "", nil, err
		}
		f, err := os.Create(outPath)
		if err != nil {
			return "", nil, err
		}
		if _, err := io.Copy(f, tr); err != nil {
			_ = f.Close()
			return "", nil, err
		}
		if err := f.Close(); err != nil {
			return "", nil, err
		}
	}

	// Merge dependencies + peerDependencies. We don't resolve semver
	// ranges — we just use the latest of each dep. CodeMirror releases the
	// @codemirror/* suite in lockstep, so latest-of-each is consistent in
	// practice. If a future dep diverges, switch to a real semver resolver.
	deps := make([]string, 0, len(v.Dependencies)+len(v.PeerDependencies))
	seen := map[string]bool{}
	for d := range v.Dependencies {
		if !seen[d] {
			seen[d] = true
			deps = append(deps, d)
		}
	}
	for d := range v.PeerDependencies {
		if !seen[d] {
			seen[d] = true
			deps = append(deps, d)
		}
	}
	return latest, deps, nil
}
