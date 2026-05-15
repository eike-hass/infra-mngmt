// Service worker for the wake feature.
//
// Solves the cold-tab case: when WSL has idled out (vmIdleTimeout), the
// infra-mngmt server is unreachable, and the browser would normally show
// "Site can't be reached" with no JS context to trigger a wake. With this
// SW registered (after at least one successful visit), the cached shell is
// served instead, the wake.js island runs in it, fires the wake POST to the
// Windows-side wake-proxy, and reloads when the upstream comes back.
//
// What we cache: the app shell (the index HTML + its CSS + the wake JS).
// We do NOT cache HTMX-fetched partials or any /api/* — those must always
// hit the network so the UI shows live data when WSL is up.
//
// Cache invalidation: bumping CACHE_VERSION evicts the old cache on the
// next activate. Bump when shell-level assets change in a way the user
// would notice on a stale load.

// v2: evicts v1 caches that hold pages served with Referrer-Policy: no-referrer,
// which caused Chromium to null the wake fetch's Origin header. The new shell
// is served with Referrer-Policy: same-origin so the wake-proxy accepts it.
const CACHE_VERSION = "v2";
const CACHE_NAME = "infra-mngmt-shell-" + CACHE_VERSION;

// Paths that are part of the shell — pre-cached on install so the cold-tab
// case has something to serve immediately. Wake.js plus the CSS for the
// pill is enough; the rest of the page is allowed to load empty since the
// user is just there to trigger the wake and reload.
const SHELL_PATHS = ["/", "/static/css/app.css", "/static/js/wake.js"];

self.addEventListener("install", (event) => {
  event.waitUntil(
    caches.open(CACHE_NAME).then((cache) =>
      // addAll fails atomically if any URL 4xx/5xxs — desirable here so a
      // partial cache doesn't survive a deploy that broke a shell asset.
      cache.addAll(SHELL_PATHS),
    ),
  );
  self.skipWaiting();
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches.keys().then((keys) =>
      Promise.all(
        keys
          .filter((k) => k.startsWith("infra-mngmt-shell-") && k !== CACHE_NAME)
          .map((k) => caches.delete(k)),
      ),
    ),
  );
  self.clients.claim();
});

self.addEventListener("fetch", (event) => {
  const req = event.request;
  if (req.method !== "GET") return;

  const url = new URL(req.url);

  // Same-origin only. Cross-origin (e.g. the wake-proxy POST) passes through
  // untouched — the browser handles it normally.
  if (url.origin !== self.location.origin) return;

  // Never cache APIs or partials. The wake mechanism specifically relies on
  // /api/version reaching the network so the page can tell the difference
  // between "WSL up" and "WSL down."
  if (url.pathname.startsWith("/api/") || url.pathname.startsWith("/partials/")) {
    return;
  }

  // Only cache the explicit shell paths. Everything else (entity pages, etc.)
  // falls through to the network; if the network fails on those, the browser
  // shows its native error, which is fine — those views can't function
  // without a live backend anyway.
  if (!SHELL_PATHS.includes(url.pathname)) return;

  // Network-first with cache fallback. We want the freshest shell when WSL is
  // up; the cache is a strict fallback for the cold-tab case.
  event.respondWith(
    fetch(req)
      .then((resp) => {
        if (resp && resp.ok) {
          const copy = resp.clone();
          caches.open(CACHE_NAME).then((cache) => cache.put(req, copy));
        }
        return resp;
      })
      .catch(() => caches.match(req).then((cached) => cached || Response.error())),
  );
});
