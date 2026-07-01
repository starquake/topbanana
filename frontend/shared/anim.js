// Shared anime.js wrapper for every animated surface: the solo game, the
// join / lobby / in-game player surface, and the host big screen. anime.js is vendored
// and self-hosted; this module keeps one reduced-motion + missing-global
// contract instead of a copy per component. esbuild inlines it into each
// tree's bundle, so there is no cross-tree runtime fetch.

// reducedMotion returns true when the OS-level prefers-reduced-motion
// preference is set, so animation calls short-circuit to their final state
// for affected users.
function reducedMotion() {
    return typeof window !== 'undefined'
        && typeof window.matchMedia === 'function'
        && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
}

// runAnim wraps anime.js (window.anime) so a missing global or a
// reduced-motion preference never leaves a half-rendered frame. On the skip
// path it invokes params.onComplete when the caller supplied one, so callers
// whose targets start in a pre-state snap straight to their final value;
// callers whose targets already sit in their final state simply omit
// complete. Supports both the v4 animate(targets, params) form and the legacy
// callable form. targets may be a CSS selector, a DOM element, or a plain
// object to tween.
export function runAnim(targets, params) {
    if (reducedMotion() || typeof window === 'undefined' || !window.anime) {
        if (typeof params.onComplete === 'function') params.onComplete();
        return;
    }
    const a = window.anime;
    if (typeof a.animate === 'function') {
        a.animate(targets, params);
    } else if (typeof a === 'function') {
        a({ targets, ...params });
    } else if (typeof params.onComplete === 'function') {
        params.onComplete();
    }
}

// enterCard fades+rises a card Alpine mounts (x-if) in its resting state. It
// sets the hidden from-state on el.style SYNCHRONOUSLY so no frame paints the
// card visible before the tween (#1154); onComplete clears it, so runAnim's
// reduced-motion / missing-anime skip path still lands fully visible. Call from
// x-init (synchronous), not a deferred $nextTick/rAF, or the flash returns.
export function enterCard(el, { rise = 12, duration = 380, ease = 'outQuad' } = {}) {
    el.style.opacity = '0';
    el.style.transform = `translateY(${rise}px)`;
    runAnim(el, {
        opacity: [0, 1],
        translateY: [rise, 0],
        duration,
        ease,
        onComplete: () => {
            el.style.opacity = '';
            el.style.transform = '';
        },
    });
}
