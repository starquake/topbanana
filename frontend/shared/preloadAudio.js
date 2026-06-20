// preloadAudio warms the browser cache for an audio URL so the subsequent
// <audio> element can start playing without stalling on a network round-trip.
// The solo client and the host big screen both call it during the per-question
// read beat, mirroring preloadImage for the question picture.
//
// Best-effort and fire-and-forget: a missing URL, a missing DOM, or a failed
// fetch all resolve without throwing so callers never need to guard. The real
// <audio> element still drives the visible playback path.
export function preloadAudio(url) {
    if (!url || typeof window === 'undefined' || typeof Audio !== 'function') {
        return Promise.resolve();
    }
    return new Promise((resolve) => {
        const audio = new Audio();
        audio.preload = 'auto';
        audio.oncanplaythrough = () => resolve();
        audio.onerror = () => resolve();
        audio.src = url;
    });
}
