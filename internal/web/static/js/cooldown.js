// cooldown.js ticks the rate-limit "Wait Ns" submit buttons on the auth
// pages (forgot-password, verify-email request, verify-email pending)
// down to zero and re-enables them, so a visitor who waits out the
// lockout can submit again without reloading the page.
//
// The server renders the initial disabled state and the wait count
// (e.g. `Wait 60s`, `disabled`) at page load; that server-rendered
// markup stays the source of truth on first paint and works unchanged
// if this module never runs. All this module does is let the UI catch
// up to the cooldown the server already enforces: server-side
// enforcement remains authoritative, so a submit fired right at expiry
// that the server still considers too early simply re-renders the
// cooldown page again, which is fine.
//
// Generic across all three pages: each button carries the remaining
// seconds in data-cooldown and its active label in data-cooldown-label,
// so the page templates differ only in those attribute values. The
// module is scanned by Tailwind via the @source "../js" directive, but
// it emits no class names of its own.

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
