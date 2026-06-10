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
