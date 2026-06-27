// Focus management for the player-client modals. A dialog that sets
// aria-modal="true" promises assistive tech that focus stays inside it; this
// keeps that promise: on open it moves focus into the dialog, keeps Tab /
// Shift+Tab cycling within it, and on close restores focus to the element
// that had focus before the modal opened (the opener).

const FOCUSABLE_SELECTOR = [
    'a[href]',
    'button:not([disabled])',
    'input:not([disabled])',
    'select:not([disabled])',
    'textarea:not([disabled])',
    '[tabindex]:not([tabindex="-1"])',
].join(',');

// focusableWithin returns the tabbable elements inside container in DOM order,
// dropping ones that are not rendered (display:none, detached) so a hidden
// control never becomes a Tab dead-end.
function focusableWithin(container) {
    return Array.from(container.querySelectorAll(FOCUSABLE_SELECTOR))
        .filter((el) => el.getClientRects().length > 0);
}

// createFocusTrap wires Tab containment plus focus restore for a single dialog
// element. It is DOM-only (no Alpine dependency) so it stays simple to reason
// about and reuse. activate() records the opener and moves focus in;
// deactivate() removes the handler and returns focus to the opener.
export function createFocusTrap(container) {
    let opener = null;

    function onKeydown(event) {
        if (event.key !== 'Tab') return;
        const items = focusableWithin(container);
        if (items.length === 0) {
            event.preventDefault();
            return;
        }
        const first = items[0];
        const last = items[items.length - 1];
        const active = document.activeElement;
        if (event.shiftKey) {
            if (active === first || !container.contains(active)) {
                event.preventDefault();
                last.focus();
            }
        } else if (active === last || !container.contains(active)) {
            event.preventDefault();
            first.focus();
        }
    }

    return {
        activate() {
            opener = document.activeElement;
            container.addEventListener('keydown', onKeydown);
            // A [data-autofocus] element wins so a modal can land focus on its
            // primary control (a name input, a non-destructive Cancel) rather
            // than whatever happens to be first in the DOM.
            const target = container.querySelector('[data-autofocus]')
                || focusableWithin(container)[0];
            if (target) target.focus();
        },
        deactivate() {
            container.removeEventListener('keydown', onKeydown);
            if (opener && document.contains(opener) && typeof opener.focus === 'function') {
                opener.focus();
            }
            opener = null;
        },
    };
}

// registerFocusTrap installs the x-focus-trap Alpine directive. Bind it on a
// dialog element with a boolean expression that mirrors the modal's open state
// (e.g. x-focus-trap="claimModalOpen"): the trap activates when the expression
// becomes truthy and tears down (restoring focus) when it becomes falsy or the
// element is removed. This covers both x-if (element created/destroyed) and
// x-show (element kept, display toggled) modal wiring.
export function registerFocusTrap(Alpine) {
    Alpine.directive('focus-trap', (el, { expression }, { effect, evaluateLater, cleanup }) => {
        const trap = createFocusTrap(el);
        const isOpen = evaluateLater(expression);
        let active = false;

        effect(() => {
            isOpen((open) => {
                if (open && !active) {
                    active = true;
                    // Defer to the next frame so the open update has been laid
                    // out before focus moves in: an x-show display toggle is
                    // applied in Alpine's microtask flush, and focus() no-ops
                    // on a still-hidden element (Firefox), so wait for layout.
                    requestAnimationFrame(() => {
                        if (active) trap.activate();
                    });
                } else if (!open && active) {
                    active = false;
                    trap.deactivate();
                }
            });
        });

        cleanup(() => {
            if (active) {
                active = false;
                trap.deactivate();
            }
        });
    });
}
