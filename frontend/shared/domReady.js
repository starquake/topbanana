// onDomReady runs fn once the document is parsed: immediately when the DOM has
// already loaded, otherwise on DOMContentLoaded. The standalone admin/auth page
// scripts (cooldown, copy-prompt, password-length, quiz-reorder, the upload
// queues) all need the same "wire up after parse, but cope with a late-loaded
// module" guard, so it lives here once rather than being re-typed per entry.
// A no-op when document is absent (SSR / a non-browser import).
export function onDomReady(fn) {
    if (typeof document === 'undefined') return;
    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', fn, { once: true });
    } else {
        fn();
    }
}
