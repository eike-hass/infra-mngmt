// wake.js — visibility-driven WSL wake island.
//
// Why: the backend lives inside WSL. With vmIdleTimeout set, the WSL VM
// shuts down after a configurable idle window even though linger has the
// systemd-user services correctly auto-starting. From the browser's POV
// the API just stops responding. This module:
//
//   1. Pings /api/version on a slow cadence while the tab is visible.
//   2. If the ping fails, fires a fire-and-forget POST to the wake-proxy
//      (Windows-side, port-other-than-7842; renders into window.WAKE_URL
//      via the template).
//   3. Polls until /api/version recovers (typically 20–40s for cold boot),
//      shows a wake-chip status pill while doing so.
//   4. Goes silent when the tab is hidden, so an unused tab doesn't keep
//      the WSL VM warm — that's the whole point of the idle timeout.
//
// Service worker registration also lives here. On first successful visit,
// the SW pre-caches the app shell so the next cold-tab open can run this
// script even when WSL is down (and so trigger the wake).

const PING_PATH = "/api/version";
const PING_TIMEOUT_MS = 4000;
const POLL_INTERVAL_MS = 30_000;
const WAKE_POLL_INTERVAL_MS = 2_000;
const WAKE_TIMEOUT_MS = 90_000;

const chip = document.getElementById("wake-chip");
const elapsedEl = document.getElementById("wake-chip-elapsed");
// CSP-safe config: server renders the wake-proxy URL as a data-* attribute
// on the pill rather than as an inline script setting window.WAKE_URL.
const wakeURL = chip ? chip.getAttribute("data-wake-url") : "";

let pollTimer = null;
let wakeInProgress = false;

// Register the SW so future cold-tab loads can run this script from the
// cached shell. Best-effort: any failure is silent because the warm path
// works without the SW. Skip on insecure contexts where SW is unavailable.
if ("serviceWorker" in navigator) {
  navigator.serviceWorker.register("/sw.js").catch(() => {
    // Registration failed — usually a CSP misconfiguration or an old browser.
    // The wake-on-visible path still works for the (much more common) "I
    // left the tab open and it idled" case, so we don't surface this.
  });
}

if (chip && wakeURL) {
  chip.addEventListener("click", () => {
    // Retry on click from the failed state.
    if (chip.classList.contains("failed")) {
      void runWake();
    }
  });

  startPollLoop();
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "visible") startPollLoop();
    else stopPollLoop();
  });
}

function startPollLoop() {
  if (pollTimer) return;
  // Fire the first ping immediately so a backend that died while the tab
  // was hidden doesn't sit unfixed for 30s after focus returns.
  void pollOnce();
  pollTimer = setInterval(pollOnce, POLL_INTERVAL_MS);
}

function stopPollLoop() {
  if (!pollTimer) return;
  clearInterval(pollTimer);
  pollTimer = null;
}

async function pollOnce() {
  if (document.visibilityState !== "visible") return;
  if (wakeInProgress) return;
  if (await pingOk()) return;
  await runWake();
}

async function pingOk() {
  try {
    const ctrl = new AbortController();
    const t = setTimeout(() => ctrl.abort(), PING_TIMEOUT_MS);
    const resp = await fetch(PING_PATH, { cache: "no-store", signal: ctrl.signal });
    clearTimeout(t);
    return resp.ok;
  } catch {
    return false;
  }
}

async function runWake() {
  wakeInProgress = true;
  showActive();
  const start = Date.now();
  console.info("wake: backend unreachable, firing wake to", wakeURL);
  // mode: 'no-cors' = simple POST, no preflight required. We don't need to
  // read the response — wake-proxy will accept it and forward to PC, which
  // returns a JSON name response. Whether we can read it doesn't matter;
  // success is observed by /api/version coming back online.
  try {
    await fetch(wakeURL, { method: "POST", mode: "no-cors", cache: "no-store" });
  } catch (err) {
    // network-level failure — could be wake-proxy itself down. The poll
    // loop below will surface the failed state if it doesn't recover.
    console.warn("wake: fetch failed (continuing to poll for recovery):", err);
  }
  while (Date.now() - start < WAKE_TIMEOUT_MS) {
    updateElapsed(start);
    await sleep(WAKE_POLL_INTERVAL_MS);
    if (await pingOk()) {
      const elapsedS = Math.round((Date.now() - start) / 1000);
      console.info("wake: backend recovered after", elapsedS + "s, reloading");
      hideChip();
      wakeInProgress = false;
      // Backend is back — refresh so the UI shows live state. Soft reload
      // (no `?...` cache bust) — the page is meant to render normally.
      window.location.reload();
      return;
    }
  }
  console.warn("wake: timed out after", WAKE_TIMEOUT_MS / 1000 + "s; click pill to retry");
  showFailed();
  wakeInProgress = false;
}

function showActive() {
  chip.classList.remove("failed");
  chip.classList.add("active");
  updateElapsed(Date.now());
}

function showFailed() {
  chip.classList.remove("active");
  chip.classList.add("failed");
  if (elapsedEl) elapsedEl.textContent = "click to retry";
}

function hideChip() {
  chip.classList.remove("active");
  chip.classList.remove("failed");
}

function updateElapsed(start) {
  if (!elapsedEl) return;
  const secs = Math.floor((Date.now() - start) / 1000);
  elapsedEl.textContent = secs + "s";
}

function sleep(ms) {
  return new Promise((r) => setTimeout(r, ms));
}
