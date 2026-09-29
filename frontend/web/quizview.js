// Admin quiz view page script: strips the post-upload counts from the URL,
// wires the copy-link pills and the share modal, and exposes the image library
// viewer that the thumbnails call from onclick.

import { onDomReady } from '@shared/domReady.js';

const UPLOAD_PARAMS = ['uploaded', 'failed', 'cancelled'];

// A refresh must not repaint a stale post-upload banner (#951).
function stripUploadParams() {
    if (!document.querySelector('[data-testid="upload-banner"]')) return;
    if (!window.history || !window.history.replaceState) return;
    const url = new URL(window.location.href);
    if (!UPLOAD_PARAMS.some((name) => url.searchParams.has(name))) return;
    for (const name of UPLOAD_PARAMS) url.searchParams.delete(name);
    window.history.replaceState(window.history.state, '', url.pathname + url.search + url.hash);
}

function wireCopyPills() {
    document.querySelectorAll('[data-copy-path]').forEach((btn) => {
        btn.addEventListener('click', async () => {
            const url = window.location.origin + btn.dataset.copyPath;
            const original = btn.textContent;
            try {
                await navigator.clipboard.writeText(url);
                btn.textContent = 'Copied!';
            } catch {
                btn.textContent = 'Press ⌘C';
            }
            setTimeout(() => { btn.textContent = original; }, 1500);
        });
    });
}

// The absolute URL is derived from window.location.origin so the server does
// not need to know its public hostname.
function wireShareModal(body) {
    const title = body.dataset.shareTitle;
    const url = window.location.origin + body.dataset.sharePath;
    const linkEl = body.querySelector('.share-link');
    const feedback = body.querySelector('.share-feedback');
    if (linkEl) linkEl.textContent = url;

    const flash = (msg) => {
        if (!feedback) return;
        feedback.textContent = msg;
        feedback.classList.remove('hidden');
        setTimeout(() => { feedback.classList.add('hidden'); }, 2500);
    };

    const copyBtn = body.querySelector('.share-copy');
    if (copyBtn) {
        copyBtn.addEventListener('click', async () => {
            try {
                await navigator.clipboard.writeText(url);
                flash('Link copied to clipboard.');
            } catch {
                flash('Could not copy automatically — select the link above and copy it manually.');
            }
        });
    }

    const whatsappBtn = body.querySelector('.share-whatsapp');
    if (whatsappBtn) {
        whatsappBtn.addEventListener('click', () => {
            window.open(`https://wa.me/?text=${encodeURIComponent(`${title}: ${url}`)}`, '_blank', 'noopener');
        });
    }

    const signalBtn = body.querySelector('.share-signal');
    if (signalBtn) {
        signalBtn.addEventListener('click', async () => {
            // Signal has no public web share URL: use the Web Share API where
            // it exists and fall back to the clipboard.
            if (navigator.share) {
                try {
                    await navigator.share({ title, url });

                    return;
                } catch (err) {
                    if (err && err.name === 'AbortError') return;
                }
            }
            try {
                await navigator.clipboard.writeText(url);
                flash('Link copied — paste into Signal.');
            } catch {
                flash('Could not share — copy the link above manually.');
            }
        });
    }
}

// openImageViewer points the shared lightbox <img> at the full-size URL (#950),
// hiding it behind the loading indicator until it loads (#993) and swapping in
// the error panel on a failed load.
export function openImageViewer(src) {
    const img = document.getElementById('image-modal-viewer-img');
    const errorEl = document.getElementById('image-modal-viewer-error');
    const loadingEl = document.getElementById('image-modal-viewer-loading');
    if (img) {
        img.classList.add('invisible');
        img.removeAttribute('src');
        if (errorEl) errorEl.hidden = true;
        if (loadingEl) loadingEl.hidden = false;
        const onLoad = () => {
            img.classList.remove('invisible');
            if (loadingEl) loadingEl.hidden = true;
            img.removeEventListener('error', onError);
        };
        const onError = () => {
            img.removeEventListener('load', onLoad);
            if (loadingEl) loadingEl.hidden = true;
            if (errorEl) errorEl.hidden = false;
        };
        img.addEventListener('load', onLoad, { once: true });
        img.addEventListener('error', onError, { once: true });
        img.src = src;
    }
    window.openModal('image-modal-viewer');
}

window.openImageViewer = openImageViewer;

onDomReady(() => {
    stripUploadParams();
    wireCopyPills();
    document.querySelectorAll('[data-share-path]').forEach(wireShareModal);
});
