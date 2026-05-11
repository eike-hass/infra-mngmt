# Vendored asset licenses

Vendoring policy: `docs/frontend-architecture.md` §10. Each entry below records
the upstream source URL and the version pinned at the time of vendoring. To
upgrade, replace the file and update both the version and the upstream URL.

## htmx 2.0.4

- File: `htmx.min.js`
- Upstream: https://registry.npmjs.org/htmx.org/-/htmx.org-2.0.4.tgz (`package/dist/htmx.min.js`)
- License: 0BSD (Zero-Clause BSD)
- Project: https://htmx.org

## fuse.js 7.0.0

- File: `fuse.min.js`
- Upstream: https://registry.npmjs.org/fuse.js/-/fuse.js-7.0.0.tgz (`package/dist/fuse.min.js`)
- License: Apache-2.0
- Project: https://www.fusejs.io

## marked 12.0.2

- File: `marked.min.js`
- Upstream: https://registry.npmjs.org/marked/-/marked-12.0.2.tgz (`package/marked.min.js`)
- License: MIT
- Project: https://marked.js.org

## CodeMirror 6 (bundled)

- File: `codemirror.bundle.js`
- Build: `make vendor-codemirror` (Go tool at `cmd/vendor-codemirror/`) — fetches
  the packages below from `registry.npmjs.org`, populates a temp
  `node_modules/`, and bundles via the esbuild Go API into a single ESM file.
- License: MIT (all packages below)
- Project: https://codemirror.net

Versions at last regeneration (pinned by `dist-tags.latest` at build time;
regenerate to bump):

| Package | Version |
|---|---|
| codemirror | 6.0.2 |
| @codemirror/autocomplete | 6.20.2 |
| @codemirror/commands | 6.10.3 |
| @codemirror/lang-css | 6.3.1 |
| @codemirror/lang-html | 6.4.11 |
| @codemirror/lang-javascript | 6.2.5 |
| @codemirror/lang-markdown | 6.5.0 |
| @codemirror/language | 6.12.3 |
| @codemirror/lint | 6.9.6 |
| @codemirror/search | 6.7.0 |
| @codemirror/state | 6.6.0 |
| @codemirror/view | 6.42.1 |
| @lezer/common | 1.5.2 |
| @lezer/css | 1.3.3 |
| @lezer/highlight | 1.2.3 |
| @lezer/html | 1.3.13 |
| @lezer/javascript | 1.5.4 |
| @lezer/lr | 1.4.10 |
| @lezer/markdown | 1.6.3 |
| @marijn/find-cluster-break | 1.0.2 |
| crelt | 1.0.6 |
| style-mod | 4.1.3 |
| w3c-keyname | 2.2.8 |
