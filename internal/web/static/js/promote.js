// Promote modal lifecycle — backdrop-click + Escape-key dismissal +
// auto-focus on the rename input. Extracted from the modal templates
// (templates/promote_picker.html.tmpl, templates/promote_result.html.tmpl)
// to drop the inline <script> bodies; once onclick= attributes also leave
// the modal templates, CSP can drop `'unsafe-inline'` from `script-src`
// entirely (see docs/frontend-architecture.md §18.3).
//
// The modal appears via HTMX swap into #promote-slot, and disappears the
// same way (the close button GET's /partials/promote-clear which returns
// an empty body). So this module wires up on `htmx:afterSwap` whenever
// #promote-slot is the target, and tears down on the same event when the
// new content lacks #promote-modal.

let _activeKeydownListener = null;

function teardown() {
  if (_activeKeydownListener) {
    document.removeEventListener('keydown', _activeKeydownListener);
    _activeKeydownListener = null;
  }
}

function initPromoteModal() {
  // Always tear down any prior listener; the modal may have been replaced
  // with a fresh picker or result, or with empty content.
  teardown();

  const modal = document.getElementById('promote-modal');
  if (!modal) return; // slot cleared — nothing to wire up

  // Backdrop-click dismiss. The handler is bound to the modal element
  // itself; e.target === modal means the click landed on the backdrop,
  // not on the card inside.
  modal.addEventListener('click', e => {
    if (e.target === modal) {
      document.getElementById('promote-close-btn')?.click();
    }
  });

  // Escape-key dismiss. Self-removing on trigger so we don't accumulate
  // listeners across modal open/close cycles. teardown() also removes it
  // when a later swap drops the modal entirely.
  const onKey = e => {
    if (e.key === 'Escape') {
      e.preventDefault();
      document.removeEventListener('keydown', onKey);
      _activeKeydownListener = null;
      document.getElementById('promote-close-btn')?.click();
    }
  };
  document.addEventListener('keydown', onKey);
  _activeKeydownListener = onKey;

  // Auto-focus the rename input so power users can edit immediately.
  // Only present in the picker, not the result fragment — querying with
  // optional chaining handles both cases.
  const renameInput = document.getElementById('promote-rename-input');
  if (renameInput) {
    renameInput.focus();
    renameInput.select();
  }
}

document.body.addEventListener('htmx:afterSwap', e => {
  if (e.detail?.target?.id === 'promote-slot') initPromoteModal();
});
