// share.js wires the "Share" affordance used on the public home page and
// the player client (start screen + finish screen). One module covers
// both surfaces so the deep-link patterns, copy-fallback behaviour, and
// dialog chrome stay in lockstep.
//
// Two entry points:
//
//   - Declarative: any element with [data-share-trigger] is auto-wired
//     at DOMContentLoaded. The trigger reads data-share-path,
//     data-share-title, and an optional data-share-text and opens the
//     dialog on click. Used by the server-rendered home page so the
//     template stays free of inline handlers.
//
//   - Programmatic: openShareDialog({title, text, url}) — used by the
//     Alpine.js player client, which composes the text dynamically
//     (score, quiz title) and calls in with absolute fields.
//
// The dialog is built on the native <dialog> element so ESC-to-close
// and the modal backdrop come for free. Tailwind utility classes are
// emitted in the markup; share.js is scanned by Tailwind via the
// @source directive in _tailwind.css so the classes survive
// tree-shaking.

// Brand SVG paths sourced from Simple Icons (CC0). Each path is the
// inner d= value for a 24×24 viewBox; the button template wraps it
// with the appropriate <svg> + brand-colour background. Keeping the
// paths here as data means a future brand redesign only touches this
// file, not the dialog HTML.
const ICON_WHATSAPP = 'M17.472 14.382c-.297-.149-1.758-.867-2.03-.967-.273-.099-.471-.148-.67.15-.197.297-.767.966-.94 1.164-.173.199-.347.223-.644.075-.297-.15-1.255-.463-2.39-1.475-.883-.788-1.48-1.761-1.653-2.059-.173-.297-.018-.458.13-.606.134-.133.298-.347.446-.52.149-.174.198-.298.298-.497.099-.198.05-.371-.025-.52-.075-.149-.669-1.612-.916-2.207-.242-.579-.487-.5-.669-.51-.173-.008-.371-.01-.57-.01-.198 0-.52.074-.792.372-.272.297-1.04 1.016-1.04 2.479 0 1.462 1.065 2.875 1.213 3.074.149.198 2.096 3.2 5.077 4.487.709.306 1.262.489 1.694.625.712.227 1.36.195 1.871.118.571-.085 1.758-.719 2.006-1.413.248-.694.248-1.289.173-1.413-.074-.124-.272-.198-.57-.347m-5.421 7.403h-.004a9.87 9.87 0 01-5.031-1.378l-.361-.214-3.741.982.998-3.648-.235-.374a9.86 9.86 0 01-1.51-5.26c.001-5.45 4.436-9.884 9.888-9.884 2.64 0 5.122 1.03 6.988 2.898a9.825 9.825 0 012.893 6.994c-.003 5.45-4.437 9.884-9.885 9.884m8.413-18.297A11.815 11.815 0 0012.05 0C5.495 0 .16 5.335.157 11.892c0 2.096.547 4.142 1.588 5.945L.057 24l6.305-1.654a11.882 11.882 0 005.683 1.448h.005c6.554 0 11.89-5.335 11.893-11.893a11.821 11.821 0 00-3.48-8.413Z';
const ICON_TELEGRAM = 'M11.944 0A12 12 0 0 0 0 12a12 12 0 0 0 12 12 12 12 0 0 0 12-12A12 12 0 0 0 12 0a12 12 0 0 0-.056 0zm4.962 7.224c.1-.002.321.023.465.14a.506.506 0 0 1 .171.325c.016.093.036.306.02.472-.18 1.898-.962 6.502-1.36 8.627-.168.9-.499 1.201-.82 1.23-.696.065-1.225-.46-1.9-.902-1.056-.693-1.653-1.124-2.678-1.8-1.185-.78-.417-1.21.258-1.91.177-.184 3.247-2.977 3.307-3.23.007-.032.014-.15-.056-.212s-.174-.041-.249-.024c-.106.024-1.793 1.14-5.061 3.345-.48.33-.913.49-1.302.48-.428-.008-1.252-.241-1.865-.44-.752-.245-1.349-.374-1.297-.789.027-.216.325-.437.893-.663 3.498-1.524 5.83-2.529 6.998-3.014 3.332-1.386 4.025-1.627 4.476-1.635z';
const ICON_REDDIT = 'M12 0A12 12 0 0 0 0 12a12 12 0 0 0 12 12 12 12 0 0 0 12-12A12 12 0 0 0 12 0zm5.01 4.744c.688 0 1.25.561 1.25 1.249a1.25 1.25 0 0 1-2.498.056l-2.597-.547-.8 3.747c1.824.07 3.48.632 4.674 1.488.308-.309.73-.491 1.207-.491.968 0 1.754.786 1.754 1.754 0 .716-.435 1.333-1.01 1.614a3.111 3.111 0 0 1 .042.52c0 2.694-3.13 4.87-7.004 4.87-3.874 0-7.004-2.176-7.004-4.87 0-.183.015-.366.043-.534A1.748 1.748 0 0 1 4.028 12c0-.968.786-1.754 1.754-1.754.463 0 .898.196 1.207.49 1.207-.883 2.878-1.43 4.744-1.487l.885-4.182a.342.342 0 0 1 .14-.197.35.35 0 0 1 .238-.042l2.906.617a1.214 1.214 0 0 1 1.108-.701zM9.25 12C8.561 12 8 12.562 8 13.25c0 .687.561 1.248 1.25 1.248.687 0 1.248-.561 1.248-1.249 0-.688-.561-1.249-1.249-1.249zm5.5 0c-.687 0-1.248.561-1.248 1.25 0 .687.561 1.248 1.249 1.248.688 0 1.249-.561 1.249-1.249 0-.687-.562-1.249-1.25-1.249zm-5.466 3.99a.327.327 0 0 0-.231.094.33.33 0 0 0 0 .463c.842.842 2.484.913 2.961.913.477 0 2.105-.056 2.961-.913a.361.361 0 0 0 .029-.463.33.33 0 0 0-.464 0c-.547.533-1.684.73-2.512.73-.828 0-1.979-.196-2.512-.73a.326.326 0 0 0-.232-.095z';
const ICON_X = 'M18.901 1.153h3.68l-8.04 9.19L24 22.846h-7.406l-5.8-7.584-6.638 7.584H.474l8.6-9.83L0 1.154h7.594l5.243 6.932ZM17.61 20.644h2.039L6.486 3.24H4.298Z';

const NETWORKS = [
    {
        key: 'whatsapp',
        label: 'WhatsApp',
        bg: '#25D366',
        icon: ICON_WHATSAPP,
        href: ({ text, url }) => `https://wa.me/?text=${encodeURIComponent(joinTextAndURL(text, url))}`,
    },
    {
        key: 'telegram',
        label: 'Telegram',
        bg: '#229ED9',
        icon: ICON_TELEGRAM,
        href: ({ text, url }) =>
            `https://t.me/share/url?url=${encodeURIComponent(url)}&text=${encodeURIComponent(text)}`,
    },
    {
        key: 'reddit',
        label: 'Reddit',
        bg: '#FF4500',
        icon: ICON_REDDIT,
        href: ({ text, url }) =>
            `https://reddit.com/submit?url=${encodeURIComponent(url)}&title=${encodeURIComponent(text)}`,
    },
    {
        key: 'x',
        label: 'X',
        bg: '#000000',
        icon: ICON_X,
        href: ({ text, url }) =>
            `https://twitter.com/intent/tweet?text=${encodeURIComponent(text)}&url=${encodeURIComponent(url)}`,
    },
];

// joinTextAndURL composes the single-string payload for channels that
// cannot carry the URL separately from the body — the WhatsApp
// pre-fill (wa.me has no url field) and the Copy fallback (one
// clipboard string, link last so chat clients still auto-unfurl).
function joinTextAndURL(text, url) {
    if (!text) return url;

    return `${text}\n${url}`;
}

// openShareDialog mounts a modal <dialog> with the share controls,
// shows it, and returns when the user closes it. The dialog is
// rebuilt on every call (rather than reused) so multiple share
// triggers on the same page never share state — closing one dialog
// can't blank the contents of the next.
//
// Inputs:
//   - title: short label rendered as the dialog heading. Also passed
//     to navigator.share's title field (some OS share sheets surface
//     it; most ignore it).
//   - text:  pre-composed message body (e.g. "Play this quiz: Foo"
//     or "I scored 3500 on Foo — beat me?").
//   - url:   absolute play URL.
export function openShareDialog({ title, text, url }) {
    const dialog = buildDialog({ title, text, url });
    document.body.appendChild(dialog);
    // Use the close event so any path back out of the dialog (ESC,
    // backdrop click, Close button) tears down the DOM exactly once.
    dialog.addEventListener('close', () => dialog.remove(), { once: true });
    dialog.showModal();
}

// canUseNativeShare reports whether navigator.share is available on
// this device. The check is wrapped in a try because some browsers
// expose navigator.share but throw on the actual call when called
// from a non-secure context — testing for the function alone is
// enough at click time; the call itself is wrapped below.
function canUseNativeShare() {
    return typeof navigator !== 'undefined' && typeof navigator.share === 'function';
}

function buildDialog({ title, text, url }) {
    const dialog = document.createElement('dialog');
    // Tailwind preflight zeroes margins on every element including
    // <dialog>, which breaks the browser-default `margin: auto`
    // centering on modal dialogs. Switching to the translate
    // technique (fixed + top/left 50% + -translate -50%) makes the
    // centering independent of margin, so the dialog ignores
    // preflight's reset entirely.
    dialog.className =
        'share-dialog fixed top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2 max-w-[600px] w-[calc(100%-2rem)] bg-surface text-text border border-accent-line rounded-lg shadow-2xl p-0 backdrop:bg-bg/80 backdrop:backdrop-blur-sm';

    dialog.innerHTML = `
        <header class="flex items-center justify-between px-6 py-5 border-b border-border-soft">
            <p class="font-display text-sm font-semibold uppercase tracking-[0.14em]">Share</p>
            <button type="button"
                    aria-label="close"
                    data-share-close
                    class="w-6 h-6 inline-flex items-center justify-center rounded-full bg-border-soft text-text-dim border-0 hover:bg-border hover:text-text cursor-pointer">&times;</button>
        </header>
        <section class="px-6 py-5 text-[0.95rem]">
            <p class="font-semibold mb-5 break-all text-cyan text-sm" data-share-link></p>
            <div class="grid grid-cols-3 gap-3 sm:grid-cols-6">
                ${networkButtonsHTML()}
                <button type="button" data-share-copy aria-label="Copy link"
                        class="group flex flex-col items-center gap-2 bg-transparent border-0 p-0 cursor-pointer focus-visible:outline-none">
                    <span class="w-12 h-12 inline-flex items-center justify-center rounded-full bg-border border border-border-soft text-text transition-transform group-hover:scale-110 group-focus-visible:scale-110 group-focus-visible:shadow-focus">
                        <svg viewBox="0 0 16 16" class="w-5 h-5" fill="currentColor" aria-hidden="true"><path d="M4 1.5H3a2 2 0 0 0-2 2V14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V3.5a2 2 0 0 0-2-2h-1v1h1a1 1 0 0 1 1 1V14a1 1 0 0 1-1 1H3a1 1 0 0 1-1-1V3.5a1 1 0 0 1 1-1h1z"/><path d="M9.5 1a.5.5 0 0 1 .5.5v1a.5.5 0 0 1-.5.5h-3a.5.5 0 0 1-.5-.5v-1a.5.5 0 0 1 .5-.5zm-3-1A1.5 1.5 0 0 0 5 1.5v1A1.5 1.5 0 0 0 6.5 4h3A1.5 1.5 0 0 0 11 2.5v-1A1.5 1.5 0 0 0 9.5 0z"/></svg>
                    </span>
                    <span class="text-[0.7rem] uppercase tracking-[0.1em] text-text-dim group-hover:text-text">Copy</span>
                </button>
                ${nativeShareButtonHTML()}
            </div>
            <p class="hidden mt-4 text-xs text-text-dim text-center" data-share-feedback></p>
        </section>
        <footer class="flex justify-end gap-2 px-6 py-4 border-t border-border-soft">
            <button type="button" data-share-close
                    class="inline-flex items-center justify-center min-h-[36px] px-3 py-2 border border-border rounded-sm bg-transparent text-text-dim text-xs uppercase font-semibold tracking-[0.14em] transition-colors hover:border-accent hover:text-text cursor-pointer">Close</button>
        </footer>
    `;

    wireDialog(dialog, { title, text, url });

    return dialog;
}

function networkButtonsHTML() {
    return NETWORKS.map((n) => `
        <a data-share-network="${n.key}"
           target="_blank" rel="noopener noreferrer"
           aria-label="Share on ${n.label}"
           class="group flex flex-col items-center gap-2 no-underline focus-visible:outline-none">
            <span class="w-12 h-12 inline-flex items-center justify-center rounded-full text-white transition-transform group-hover:scale-110 group-focus-visible:scale-110 group-focus-visible:shadow-focus"
                  style="background-color: ${n.bg};">
                <svg viewBox="0 0 24 24" class="w-6 h-6" fill="currentColor" aria-hidden="true"><path d="${n.icon}"/></svg>
            </span>
            <span class="text-[0.7rem] uppercase tracking-[0.1em] text-text-dim group-hover:text-text">${n.label}</span>
        </a>
    `).join('');
}

function nativeShareButtonHTML() {
    if (!canUseNativeShare()) return '';

    return `
        <button type="button" data-share-native aria-label="More share options"
                class="group flex flex-col items-center gap-2 bg-transparent border-0 p-0 cursor-pointer focus-visible:outline-none">
            <span class="w-12 h-12 inline-flex items-center justify-center rounded-full bg-accent text-bg transition-transform group-hover:scale-110 group-focus-visible:scale-110 group-focus-visible:shadow-focus">
                <svg viewBox="0 0 16 16" class="w-5 h-5" fill="currentColor" aria-hidden="true"><path d="M13.5 1a1.5 1.5 0 1 0 0 3 1.5 1.5 0 0 0 0-3zM11 2.5a2.5 2.5 0 1 1 .603 1.628l-6.718 3.12a2.5 2.5 0 0 1 0 1.504l6.718 3.12a2.5 2.5 0 1 1-.488.876l-6.718-3.12a2.5 2.5 0 1 1 0-3.256l6.718-3.12A2.5 2.5 0 0 1 11 2.5zm-8.5 4a1.5 1.5 0 1 0 0 3 1.5 1.5 0 0 0 0-3zm11 5.5a1.5 1.5 0 1 0 0 3 1.5 1.5 0 0 0 0-3z"/></svg>
            </span>
            <span class="text-[0.7rem] uppercase tracking-[0.1em] text-text-dim group-hover:text-text">More</span>
        </button>
    `;
}

function wireDialog(dialog, { title, text, url }) {
    dialog.querySelector('[data-share-link]').textContent = url;

    dialog.querySelectorAll('[data-share-close]').forEach((el) => {
        el.addEventListener('click', () => dialog.close());
    });

    // Backdrop click closes the dialog. We can't add the listener to
    // a separate backdrop element (the ::backdrop pseudo isn't a real
    // DOM node), so we filter clicks whose target IS the dialog
    // element — that's the click that landed on the backdrop rather
    // than on any of the dialog's children.
    dialog.addEventListener('click', (ev) => {
        if (ev.target === dialog) dialog.close();
    });

    dialog.querySelectorAll('[data-share-network]').forEach((link) => {
        const network = NETWORKS.find((n) => n.key === link.dataset.shareNetwork);
        if (network) link.href = network.href({ text, url });
    });

    const copyBtn = dialog.querySelector('[data-share-copy]');
    if (copyBtn) {
        copyBtn.addEventListener('click', async () => {
            try {
                await navigator.clipboard.writeText(joinTextAndURL(text, url));
                flashFeedback(dialog, 'Link copied to clipboard.');
            } catch (_err) {
                flashFeedback(dialog, 'Could not copy — select the link above and copy manually.');
            }
        });
    }

    const nativeBtn = dialog.querySelector('[data-share-native]');
    if (nativeBtn) {
        nativeBtn.addEventListener('click', async () => {
            try {
                await navigator.share({ title, text, url });
                dialog.close();
            } catch (err) {
                // AbortError = user dismissed the share sheet. Don't
                // surface anything; the dialog stays open so they can
                // pick another option.
                if (err && err.name !== 'AbortError') {
                    flashFeedback(dialog, 'Native share unavailable — pick a network or copy the link.');
                }
            }
        });
    }
}

function flashFeedback(dialog, msg) {
    const el = dialog.querySelector('[data-share-feedback]');
    if (!el) return;
    el.textContent = msg;
    el.classList.remove('hidden');
    setTimeout(() => el.classList.add('hidden'), 2500);
}

// autowire scans the document for [data-share-trigger] buttons and
// attaches a click handler that opens the dialog. The trigger element
// carries the message inputs as data-* attributes so the template
// stays declarative.
//
// Re-exported because the player client (Alpine) mounts content after
// initial parse and needs to call this to pick up newly-rendered
// triggers. The home page just relies on the DOMContentLoaded run.
export function autowireShareTriggers(root = document) {
    root.querySelectorAll('[data-share-trigger]:not([data-share-bound])').forEach((btn) => {
        btn.dataset.shareBound = 'true';
        btn.addEventListener('click', () => {
            const path = btn.dataset.sharePath;
            const url = new URL(path, window.location.origin).href;
            openShareDialog({
                title: btn.dataset.shareTitle || 'Share',
                text: btn.dataset.shareText || btn.dataset.shareTitle || '',
                url,
            });
        });
    });
}

if (typeof document !== 'undefined') {
    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', () => autowireShareTriggers());
    } else {
        autowireShareTriggers();
    }
}
