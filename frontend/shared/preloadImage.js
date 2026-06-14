// preloadImage fetches and decodes an image URL into the browser cache so
// the subsequent <img> render paints from cache instead of stalling on a
// network round-trip. Both the solo client and the host big screen call it
// during the per-question read beat: by the time options paint, the image
// is usually already decoded.
//
// Best-effort and fire-and-forget: a missing URL, a missing DOM, or a
// failed fetch all resolve without throwing so callers never need to
// guard. The real <img> element still drives the visible error path via
// its own @error handler.
export function preloadImage(url) {
    if (!url || typeof window === 'undefined' || typeof Image !== 'function') {
        return Promise.resolve();
    }
    return new Promise((resolve) => {
        const img = new Image();
        img.onload = () => resolve();
        img.onerror = () => resolve();
        img.src = url;
    });
}
