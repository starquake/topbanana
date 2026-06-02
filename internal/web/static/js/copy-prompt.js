// copy-prompt.js wires the Copy button on the quiz-import page: clicking
// [data-copy-target] copies the referenced element's text to the clipboard
// and flips the button label to "Copied" for a moment. Loaded on every admin
// page, so it self-noops where no [data-copy-target] button exists. The async
// Clipboard API is unavailable in insecure contexts (plain http) and in some
// browsers without a user gesture, so a selection + execCommand fallback keeps
// the button working there; only a total failure leaves the label unchanged.

const COPIED_RESET_MS = 2000;

function selectElementText(element) {
    const selection = window.getSelection?.();
    if (!selection) return false;
    const range = document.createRange();
    range.selectNodeContents(element);
    selection.removeAllRanges();
    selection.addRange(range);
    return true;
}

async function copyText(target, text) {
    try {
        await navigator.clipboard.writeText(text);
        return true;
    } catch {
        // Fall through to the legacy selection-based copy below.
    }

    if (!selectElementText(target)) return false;
    try {
        return document.execCommand('copy');
    } catch {
        return false;
    }
}

function wire(button) {
    const targetSelector = button.dataset.copyTarget;
    const target = targetSelector ? document.querySelector(targetSelector) : null;
    if (!target) return;

    const label = button.querySelector('[data-copy-label]') ?? button;
    const original = label.textContent;
    let resetTimer = null;

    button.addEventListener('click', async () => {
        const copied = await copyText(target, target.textContent ?? '');
        if (!copied) return;

        label.textContent = 'Copied';
        button.dataset.copied = 'true';
        if (resetTimer) clearTimeout(resetTimer);
        resetTimer = setTimeout(() => {
            label.textContent = original;
            delete button.dataset.copied;
        }, COPIED_RESET_MS);
    });
}

if (typeof document !== 'undefined') {
    const run = () => document.querySelectorAll('[data-copy-target]').forEach(wire);
    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', run);
    } else {
        run();
    }
}
