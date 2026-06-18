// System tab island.
//
// Two small bits of behavior that pure HTMX can't express:
//   1. Expand/collapse of the docker/podman df category rows — the per-object
//      detail is rendered up front (hidden) and toggled client-side, so there's
//      no per-expand round-trip. Delegated so it survives card re-renders after
//      a prune (innerHTML swap into the section wrapper).
//   2. Surfacing the prune/remove result toast carried on the response's
//      HX-Trigger header (event name "sys-toast"). htmx dispatches the event;
//      we forward its detail to the shared toast stack.

// ── category disclosure (client-side show/hide) ──
document.body.addEventListener('click', e => {
  const btn = e.target.closest('.sys-disclosure');
  if (!btn) return;
  const id = btn.dataset.target;
  const row = id && document.getElementById(id);
  if (!row) return;
  const open = row.hasAttribute('hidden');
  if (open) row.removeAttribute('hidden');
  else row.setAttribute('hidden', '');
  btn.setAttribute('aria-expanded', String(open));
  btn.classList.toggle('open', open);
});

// ── prune/remove result toast (HX-Trigger: {"sys-toast": {...}}) ──
document.body.addEventListener('sys-toast', e => {
  const d = e.detail || {};
  if (window.showToast) window.showToast({ title: d.title || 'done', body: d.body || '', kind: d.kind || 'ok' });
});
