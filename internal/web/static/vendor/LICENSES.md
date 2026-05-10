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

## CodeMirror 6 — NOT YET VENDORED

The CodeMirror runtime is currently loaded from `https://esm.sh/codemirror@6`
(plus its `@codemirror/lang-markdown` and `@codemirror/state` peers) at runtime.
Vendoring requires bundling the module graph; that is deferred to migration
step M5 in `docs/frontend-architecture.md` §16. Until then this is the one
remaining external dependency at page load.
