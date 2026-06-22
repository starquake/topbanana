// iosKeepAlive plays a looping silent <audio> so iOS treats the page as an active
// media session and routes Web Audio through the hardware channel -- so quiz audio
// is heard even with the ring/silent switch on (a deliberate product choice). A
// real served file, not a data: URI (iOS Safari is flaky with data-URI <audio>).
// iOS-only; a no-op elsewhere, where Web Audio plays fine without it (#1088).
const SILENCE_SRC = '/static/audio/silence.wav';

// iPadOS 13+ reports a "Macintosh" UA, so a Mac with a touch screen is taken for
// an iPad; a real Mac (no touch) is not.
function isIOS() {
    if (typeof navigator === 'undefined') return false;
    const ua = navigator.userAgent || '';
    if (/iPad|iPhone|iPod/.test(ua)) return true;
    return /Macintosh/.test(ua) && (navigator.maxTouchPoints || 0) > 1;
}

// start() must be called from a user gesture so the play is allowed; stop() tears
// it down. Every method is a no-op off iOS.
export function createIOSKeepAlive() {
    const enabled = isIOS();
    let el = null;
    let onVisibility = null;

    function start() {
        if (!enabled || el || typeof document === 'undefined') return;
        el = document.createElement('audio');
        el.src = SILENCE_SRC;
        el.loop = true;
        // playsinline so iOS does not open a fullscreen player.
        el.setAttribute('playsinline', '');
        el.setAttribute('aria-hidden', 'true');
        el.style.display = 'none';
        document.body.appendChild(el);
        // In a gesture, but swallow a rejection so a stricter policy can't throw
        // out of the Start handler.
        const playback = el.play();
        if (playback && typeof playback.catch === 'function') {
            playback.catch(() => {});
        }
        // Pause while hidden so iOS doesn't flag a backgrounded session; resume on return.
        onVisibility = () => {
            if (!el) return;
            if (document.visibilityState === 'hidden') {
                el.pause();
            } else {
                const resumed = el.play();
                if (resumed && typeof resumed.catch === 'function') resumed.catch(() => {});
            }
        };
        document.addEventListener('visibilitychange', onVisibility);
    }

    function stop() {
        if (onVisibility) {
            document.removeEventListener('visibilitychange', onVisibility);
            onVisibility = null;
        }
        if (el) {
            el.pause();
            el.remove();
            el = null;
        }
    }

    return { start, stop };
}
