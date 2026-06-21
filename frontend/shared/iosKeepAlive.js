// iosKeepAlive routes Web Audio through the iOS media channel so the question
// audio keeps playing even when the iPhone ring/silent switch is set to silent
// (a deliberate product decision: a quiz host wants the room to hear the clips),
// AND keeps the output path "hot" so iOS does not re-suspend it between clips.
//
// The mechanism is a looping, silent <audio> element started inside the Start
// gesture: once an <audio> element is playing media, iOS treats the page as an
// active media session and lets the Web Audio AudioContext play through the same
// (un-muted, hardware) channel. We use a REAL served file, not a `data:` URI:
// iOS Safari is flaky with data-URI <audio> sources (the suspected reason an
// earlier data-URI keep-alive did not help), so the loop must point at a real
// gapless asset (#1088).
//
// iOS-only by design: it is a no-op on every other platform, where Web Audio
// plays fine without the media-channel trick. iPadOS reports a desktop
// "Macintosh" UA, so it is detected via the touch-points heuristic that Apple's
// own docs recommend for telling an iPad apart from a Mac.

// SILENCE_SRC is the gapless silent loop. A real served WAV, not a data: URI.
const SILENCE_SRC = '/static/audio/silence.wav';

// isIOS reports whether the current device is an iPhone / iPad / iPod. iPadOS 13+
// masquerades as "Macintosh", so a Mac UA with a touch screen is treated as an
// iPad; a real Mac (no touch) is not.
function isIOS() {
    if (typeof navigator === 'undefined') return false;
    const ua = navigator.userAgent || '';
    if (/iPad|iPhone|iPod/.test(ua)) return true;
    return /Macintosh/.test(ua) && (navigator.maxTouchPoints || 0) > 1;
}

// createIOSKeepAlive returns a keep-alive controller. start() spins up the
// looping silent <audio> (call it from a user gesture so the play is allowed);
// stop() tears it down. On a non-iOS device every method is a no-op.
export function createIOSKeepAlive() {
    const enabled = isIOS();
    let el = null;
    let onVisibility = null;

    function start() {
        if (!enabled || el || typeof document === 'undefined') return;
        el = document.createElement('audio');
        el.src = SILENCE_SRC;
        el.loop = true;
        // playsinline keeps iOS from hijacking the element into a fullscreen
        // player; the element is silent and off-screen.
        el.setAttribute('playsinline', '');
        el.setAttribute('aria-hidden', 'true');
        el.style.display = 'none';
        document.body.appendChild(el);
        // Best-effort: the play is inside a gesture, but swallow a rejection so a
        // stricter policy never throws out of the Start handler.
        const playback = el.play();
        if (playback && typeof playback.catch === 'function') {
            playback.catch(() => {});
        }
        // Pause while the tab is hidden so iOS does not flag a backgrounded media
        // session, then resume when it comes back so the channel stays hot.
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
