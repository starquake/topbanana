import { GameApp } from './components/GameApp.js';
import { claimNameForm } from './components/ClaimNameForm.js';

document.addEventListener('alpine:init', () => {
    Alpine.data('gameApp', () => new GameApp());
    // claimNameForm is a per-instance Alpine subcomponent. Each x-data
    // call gets its own input value / submitting / error state, which
    // is why we register it as a factory rather than constructing it
    // alongside gameApp.
    Alpine.data('claimNameForm', claimNameForm);
});

// Publish the visual-viewport height as a CSS custom property
// (--visual-viewport-height) so layout can react to the on-screen
// keyboard appearing on mobile. CSS units (vh/svh/dvh) on Mobile
// Firefox are tied to the layout viewport and do NOT shrink when
// the keyboard slides up; only window.visualViewport reports the
// reduced height. The claim-name modal's max-height reads this
// property to stay above the keyboard — see #196. Falls back to
// window.innerHeight when visualViewport is unavailable (very old
// browsers); on those the keyboard-avoidance does not kick in but
// the page is otherwise unaffected.
function updateVisualViewportHeight() {
    const vh = window.visualViewport
        ? window.visualViewport.height
        : window.innerHeight;
    document.documentElement.style.setProperty(
        '--visual-viewport-height', `${vh}px`,
    );
}
updateVisualViewportHeight();
if (window.visualViewport) {
    window.visualViewport.addEventListener('resize', updateVisualViewportHeight);
    window.visualViewport.addEventListener('scroll', updateVisualViewportHeight);
}
