---
name: infra-mngmt-design-iteration
description: Use when a Design handoff (HTML/JSX prototype) needs to be applied to the infra-mngmt frontend, or when the user asks to "match the design / prototype / mockup", audit drift against the prototype, or attaches a `*-handoff.zip`. Triggers on phrases like "the X view looks off vs the design", "apply the prototype", "compare to the handoff". Do NOT trigger for generic UI work without a prototype to match.
---

# infra-mngmt design iteration

Design handoffs are single-page React/JSX prototypes meant to be re-implemented — not copied — in the target codebase. infra-mngmt is Go templates + HTMX, so each pass is a translation. The work splits naturally into two phases: **looking** (find every drift) and **doing** (fix them). Each phase has a dedicated subagent so the main session stays a clean orchestrator.

The goal is **thoroughness, not minimal token use**. Past iterations failed by stopping at "looks roughly OK" and then chasing residual drifts in 4+ back-and-forth rounds with the user. The agents below are wired to look harder than feels necessary on the first pass, so the iteration converges instead of dragging.

## Setup

Already in place:

- `make proto` — Python `http.server` on `0.0.0.0:7843` inside the devcontainer, serving `$PROTO_DIR` (default `external/handoff/infra-mngmt/project` — bind-mounted, survives container restarts).
- `.devcontainer/devcontainer.json` — `appPort: ["7843:7843"]` publishes the port up to the WSL host on all interfaces; ident-browser reaches it via the docker-bridge gateway at `172.17.0.1:7843`.
- `.gitignore` — `*-handoff.zip` and `/external/handoff/` are excluded; handoffs are ephemeral.

**URLs (reachable from the devcontainer and from ident-browser):**
- Live: `http://172.17.0.1:7842/`
- Prototype: `http://172.17.0.1:7843/index.html` (once `make proto` is running)

## Workflow

1. **Stage the handoff.**
   ```bash
   mkdir -p external/handoff
   unzip -o "<bundle>.zip" -d external/handoff/    # or any path; pass via PROTO_DIR
   make proto &                                    # serves external/handoff/infra-mngmt/project by default
   curl -sI http://127.0.0.1:7843/index.html | head -1   # expect 200 OK
   ```

   The `external/handoff/` tree is gitignored, so the prototype isn't committed but persists across devcontainer restarts (don't use `/tmp` for this — it's wiped at restart and the bundle won't necessarily be available).

2. **Compare.** Spawn the `design-comparator` subagent. It renders both pages via ident-browser, walks every view × sub-state, screenshots, and writes an exhaustive drift list to `/tmp/design-drift-<timestamp>.md`.
   ```
   Agent(
     subagent_type: "design-comparator",
     description: "drift audit for new prototype",
     prompt: "Compare the live infra-mngmt UI against the prototype and produce an exhaustive drift catalogue. Pay extra attention to <any view the user flagged>."
   )
   ```
   Read the executive summary it returns. Open the findings file at `/tmp/design-drift-*.md` to scan the full list — if the user flagged something specific, confirm it's covered. If the comparator returned findings inline without writing them to disk (Sonnet sometimes elides the Write call), save them yourself before invoking the implementer.

3. **Apply.** Spawn the `design-implementer` subagent with the findings path and a scope. Recommended first batch: high-impact drifts only. Follow-up batches handle medium/low after re-comparison.
   ```
   Agent(
     subagent_type: "design-implementer",
     description: "apply high-impact prototype drifts",
     prompt: "Apply all high-impact drifts from /tmp/design-drift-<timestamp>.md. Run make check, deploy, report back with the new build_epoch."
   )
   ```
   The implementer makes the edits, runs `make check`, deploys via the redeploy endpoint, and reports the new `build_epoch` plus a list of files changed.

4. **Re-compare** (loop until clean). Spawn `design-comparator` again to confirm the fixes landed and catch residuals. If the executive summary is short and matches what the user cares about, stop. Otherwise → step 3 with the residuals.

5. **Commit.** Once you and the user are satisfied, commit the design changes (`internal/web/` modifications + any new helpers/tests). The infrastructure changes (`Makefile`, `devcontainer.json`, `.gitignore`, the two agents, this skill) are a separate concern — commit them once, not per iteration.

## Notes for the orchestrator (you)

- **You don't screenshot, edit, or build directly.** Your job is to spawn the right agent at the right time, read its summary, and decide what to do next.
- **Trust the comparator's findings.** If it flags a drift you think is fine, look at the screenshots it described before second-guessing. The premise of the workflow is that the comparator looks harder than reactive judgment.
- **Don't let the implementer skip `make check`.** If it reports a failing build, push back — investigate before deploying.
- **Stop conditions.** Convergence usually happens in 1–2 implementer rounds. If a third round still surfaces high-impact drifts, something's structurally off — surface it to the user instead of looping.

## Reference (mapping)

Prototype filenames evolve between revisions — the view files are often suffixed with a revision tag (`app-services-v3.jsx`, `app-services-v4.jsx`, etc.). Match by intent, not by exact filename.

| Concept | Prototype (typical filename pattern) | Live |
| --- | --- | --- |
| Design tokens | `:root` in `index.html` | `:root` in `internal/web/static/css/app.css` |
| Atoms (Pill, Disclosure, Chevron, …) | `app-common.jsx` | inline CSS (`.llama-pill`, `.status-pill`, `.bare`, …) |
| Config view | `app-config.jsx` | `templates/index.html.tmpl` + `entity_list.html.tmpl` + `preview.html.tmpl` |
| Services view | `app-services*.jsx` | `templates/services.html.tmpl` |
| Llama view | `app-llama*.jsx` | `templates/llama.html.tmpl` |
| Promote modal | `app-promote.jsx` | `templates/promote_picker.html.tmpl` + `promote_result.html.tmpl` |
| Shell (CardShell, WakePill, BuildChip) | `app-main*.jsx` | `templates/index.html.tmpl` + global CSS |
