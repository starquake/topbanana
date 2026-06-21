// audioLoader buffers a question's audio clip before the question is revealed,
// so the play surfaces can show a brief loading beat and then start the audio
// from the top the moment the question paints (#1070). It loads the clip through
// a throwaway Howler `Howl` (#1088) so the loading beat warms the same Web Audio
// decode path the question controller's Howl uses, then unloads it.
//
// Resolves when the clip is loaded (Howler 'load'), OR a short timeout elapses,
// OR it errors / the URL is missing / Howler is unavailable, so the caller
// always proceeds to the question. The result reports why so the caller can
// leave the manual play control up on a failure / timeout.
//
// Best-effort and never rejects: a missing URL, a missing Howl global, a load
// error, or a timeout all resolve so the question is never blocked behind a
// stuck clip.
import { AUDIO_FORMATS } from '@shared/audioFormats.js';

// DEFAULT_TIMEOUT_MS caps the loading beat so a slow / unreachable clip still
// lets the question through after a short wait.
export const DEFAULT_TIMEOUT_MS = 5000;

// loadAudioClip warms the Web Audio decode for a clip and resolves with
// { ok, reason } where reason is one of: 'ready' (loaded), 'timeout',
// 'error', or 'skipped' (no URL / no Howl support).
export function loadAudioClip(url, { timeoutMs = DEFAULT_TIMEOUT_MS } = {}) {
    const Howl = typeof window !== 'undefined' ? window.Howl : null;
    if (!url || !Howl) {
        return Promise.resolve({ ok: false, reason: 'skipped' });
    }
    return new Promise((resolve) => {
        let settled = false;
        let timer = null;
        let probe = null;
        const finish = (reason) => {
            if (settled) return;
            settled = true;
            if (timer !== null) clearTimeout(timer);
            // Unload the throwaway probe so it does not hold a second decode of
            // the same clip; the controller builds its own Howl to play it.
            if (probe) probe.unload();
            resolve({ ok: reason === 'ready', reason });
        };
        timer = setTimeout(() => finish('timeout'), timeoutMs);
        probe = new Howl({
            src: [url],
            // /media/{id} has no extension, so declare the accepted formats for
            // Howler's codec check (mirrors questionAudio.js); see audioFormats.
            format: AUDIO_FORMATS,
            preload: true,
            onload: () => finish('ready'),
            onloaderror: () => finish('error'),
        });
    });
}
