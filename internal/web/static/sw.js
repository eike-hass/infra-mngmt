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
      // Best-effort pre-cache. Promise.allSettled (vs. cache.addAll's atomic
      // semantics) ensures that a single unreachable shell URL — typically
      // because WSL was idled out at the exact moment the new SW tried to
      // install — does not abort the install and leave us pinned on the
      // previous version. The network-first fetch handler self-heals stale
      // entries on the next successful page load, so the only cost of a
      // partial pre-cache is one extra round-trip to the network when
      // WSL comes back.
      Promise.allSettled(SHELL_PATHS.map((path) => cache.add(path))),
    ),
  );
  self.skipWaiting();
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    (async () => {
      const cache = await caches.open(CACHE_NAME);
      const keys = await caches.keys();
      const oldKeys = keys.filter(
        (k) => k.startsWith("infra-mngmt-shell-") && k !== CACHE_NAME,
      );
      // Fallback: copy any shell entries still missing from the new cache
      // out of previous-version caches before deleting them. Without this,
      // if the install above couldn't fetch (WSL down at the moment of the
      // SW bump), activation would leave the new cache empty, and the next
      // page load while WSL was still down would render a browser error
      // instead of the cached shell. Stale headers carried in by the copy
      // are short-lived — the network-first fetch handler refreshes them
      // on the first page load that reaches a live backend.
      for (const oldKey of oldKeys) {
        const oldCache = await caches.open(oldKey);
        for (const path of SHELL_PATHS) {
          if (!(await cache.match(path))) {
            const oldResp = await oldCache.match(path);
            if (oldResp) await cache.put(path, oldResp.clone());
          }
        }
        await caches.delete(oldKey);
      }
      await self.clients.claim();
    })(),
  );
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
