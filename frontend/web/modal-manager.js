// Shared modal behaviour for the admin and host surfaces (#364, #889): focus
// capture/restore, Tab cycling, and Esc-to-close. Templates only declare the
// wrapper id="modal-...", role="dialog" + aria-modal + aria-labelledby, and the
// title element; the markup calls the window.openModal / window.closeModal
// globals from onclick and hx-on attributes.

const modalState = new Map();

function focusableInModal(modal) {
    const sel = 'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]),'
        + ' textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

    return Array.from(modal.querySelectorAll(sel))
        .filter((el) => !el.closest('[aria-hidden="true"]'));
}

export function openModal(id) {
    const modal = document.getElementById(id);
    if (!modal) return;
    // A second open() without an intervening close() would leak the prior
    // keydown listener; closeModal-first guarantees one listener per id.
    if (modalState.has(id)) closeModal(id);
    modal.classList.remove('hidden');

    // A previous failed action's error banner must not carry over into a fresh attempt.
    modal.querySelectorAll('[data-testid$="-error"]').forEach((el) => { el.hidden = true; });

    const previouslyFocused = document.activeElement;
    const focusables = focusableInModal(modal);
    if (focusables.length > 0) {
        focusables[0].focus();
    }

    const onKey = (e) => {
        if (e.key === 'Escape') {
            e.preventDefault();
            closeModal(id);

            return;
        }
        if (e.key !== 'Tab' || focusables.length === 0) return;
        const first = focusables[0];
        const last = focusables[focusables.length - 1];
        if (e.shiftKey && document.activeElement === first) {
            e.preventDefault();
            last.focus();
        } else if (!e.shiftKey && document.activeElement === last) {
            e.preventDefault();
            first.focus();
        }
    };
    document.addEventListener('keydown', onKey);
    modalState.set(id, { onKey, previouslyFocused });
}

export function closeModal(id) {
    // The modal may already be gone (an htmx outerHTML swap removed it, #951);
    // keydown + focus cleanup must run regardless.
    const modal = document.getElementById(id);
    if (modal) modal.classList.add('hidden');

    const state = modalState.get(id);
    if (!state) return;
    document.removeEventListener('keydown', state.onKey);
    if (state.previouslyFocused
        && typeof state.previouslyFocused.focus === 'function'
        && document.contains(state.previouslyFocused)) {
        state.previouslyFocused.focus();
    } else if (document.body && typeof document.body.focus === 'function') {
        // The trigger was detached (htmx swap); send focus to body so keyboard
        // users restart from a defined landmark.
        document.body.focus();
    }
    modalState.delete(id);
}

window.openModal = openModal;
window.closeModal = closeModal;
