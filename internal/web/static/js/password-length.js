// password-length.js shows a live "too short" hint under a new-password
// input as the visitor types, so they learn the minimum before submitting
// rather than after a server round-trip. Server-side validation stays the
// source of truth; this is a hint only.
//
// The threshold comes from the input's DOM minLength property (rendered from
// auth.MinPasswordLength), so there is no second copy of the number here. The
// paired message element is named in data-password-length; an unset/0
// minLength or a missing message element is a no-op.

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
