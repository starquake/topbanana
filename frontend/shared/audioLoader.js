// audioLoader buffers a question's audio clip before the question is revealed,
// so the solo client can show a brief loading beat and then start the audio
// from the top the moment the question paints (#1070). It exists because the
// loading screen, not a separate preload, is the single fetch of the bytes: it
// loads the clip once here, the real <audio> element plays it from cache.
//
// Resolves when the clip is buffered (canplaythrough), OR a short timeout
// elapses, OR it errors / the URL is missing / the DOM is unavailable, so the
// caller always proceeds to the question. The result reports why so the caller
// can leave the manual play control up on a failure / timeout.
//
// Best-effort and never rejects: a missing URL, a missing DOM, a load error, or
// a timeout all resolve so the question is never blocked behind a stuck clip.

// DEFAULT_TIMEOUT_MS caps the loading beat so a slow / unreachable clip still
// lets the question through after a short wait.
export const DEFAULT_TIMEOUT_MS = 5000;

// loadAudioClip warms the browser cache for a clip and resolves with
// { ok, reason } where reason is one of: 'ready' (buffered), 'timeout',
// 'error', or 'skipped' (no URL / no Audio support).
export function loadAudioClip(url, { timeoutMs = DEFAULT_TIMEOUT_MS } = {}) {
    if (!url || typeof window === 'undefined' || typeof Audio !== 'function') {
        return Promise.resolve({ ok: false, reason: 'skipped' });
    }
    return new Promise((resolve) => {
        const audio = new Audio();
        let settled = false;
        let timer = null;
        const finish = (reason) => {
            if (settled) return;
            settled = true;
            if (timer !== null) clearTimeout(timer);
            audio.oncanplaythrough = null;
            audio.onerror = null;
            resolve({ ok: reason === 'ready', reason });
        };
        audio.preload = 'auto';
        audio.oncanplaythrough = () => finish('ready');
        audio.onerror = () => finish('error');
        timer = setTimeout(() => finish('timeout'), timeoutMs);
        audio.src = url;
    });
}
