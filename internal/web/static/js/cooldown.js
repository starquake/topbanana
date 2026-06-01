// cooldown.js re-enables the rate-limit "Wait Ns" submit buttons on the auth
// pages (forgot-password, verify-email request/pending) by ticking the
// server-rendered countdown down to zero client-side, so a visitor who waits
// out the lockout can submit without reloading.
//
// The server-rendered disabled/"Wait Ns" state stays the source of truth on
// first paint and if this module never runs; server-side enforcement stays
// authoritative (a submit right at expiry the server still rejects just
// re-renders the cooldown page). Each button carries its remaining seconds in
// data-cooldown and active label in data-cooldown-label, so this is generic
// across the three pages.

function startCooldown(button) {
    let remaining = Number.parseInt(button.dataset.cooldown ?? '', 10);
    // No countdown to run: not in cooldown (0 / empty) or a malformed
    // value. Leave whatever the server rendered untouched.
    if (!Number.isFinite(remaining) || remaining <= 0) return;

    const activeLabel = button.dataset.cooldownLabel ?? '';

    const tick = () => {
        remaining -= 1;
        if (remaining > 0) {
            button.textContent = `Wait ${remaining}s`;

            return;
        }
        clearInterval(timer);
        button.textContent = activeLabel;
        button.removeAttribute('disabled');
        button.removeAttribute('aria-disabled');
    };

    const timer = setInterval(tick, 1000);
}

if (typeof document !== 'undefined') {
    const run = () => document.querySelectorAll('[data-cooldown]').forEach(startCooldown);
    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', run);
    } else {
        run();
    }
}
