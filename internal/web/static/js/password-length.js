// password-length.js shows a live "too short" hint under a new-password input
// as the visitor types, so they learn the minimum before submitting; the
// server stays the source of truth (this is a hint only). The threshold comes
// from the input's DOM minLength (rendered from auth.MinPasswordLength), so
// there is no second copy of the number here; a missing message element (named
// in data-password-length) or minLength <= 0 is a no-op.

function wire(input) {
    const messageId = input.dataset.passwordLength;
    const message = messageId ? document.getElementById(messageId) : null;
    if (!message) return;

    const update = () => {
        const min = input.minLength;
        if (min > 0 && input.value.length > 0 && input.value.length < min) {
            message.textContent = `Must be at least ${min} characters.`;
        } else {
            message.textContent = '';
        }
    };

    input.addEventListener('input', update);
}

if (typeof document !== 'undefined') {
    const run = () => document.querySelectorAll('[data-password-length]').forEach(wire);
    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', run);
    } else {
        run();
    }
}
