// home.js wires the home-page Alpine islands. Currently it registers the
// resume-session CTA (#888) that flips the public "Join a live game" button
// to a "Resume session" deep link when the player has a remembered session
// from a previous join (the localStorage entry JoinApp writes on a successful
// join). With no remembered entry the default CTA shows.
//
// The key string and parse shape live in the shared rememberedSession module so
// the home page reads exactly what the player client (JoinApp) wrote, from one
// place (#1005). This module is folded into the combined admin.js bundle
// (#1071), which the home layout loads.

import {
    readRememberedSession,
    forgetRememberedSession,
} from '@shared/rememberedSession.js';

// resumeSessionCta is the Alpine component registered against the home page's
// CTA island. It exposes resumeCode (the remembered join code or empty) and
// exit() which clears the entry, fires a best-effort leave to the live
// session, and stays on the page so the default "Join a live game" surface
// renders without a reload (Alpine re-evaluates x-if as soon as resumeCode
// flips back to empty).
function resumeSessionCta() {
    return {
        resumeCode: '',
        init() {
            const remembered = readRememberedSession();
            this.resumeCode = remembered ? remembered.code : '';
        },
        exit() {
            const code = this.resumeCode;
            this.resumeCode = '';
            forgetRememberedSession();
            if (!code) return;
            try {
                fetch(`/api/sessions/${encodeURIComponent(code)}/leave`, {
                    method: 'POST',
                    keepalive: true,
                }).catch(() => {});
            } catch {
                // Network is offline / fetch unavailable; the player still
                // ages out of the active window server-side, so nothing to
                // retry.
            }
        },
    };
}

document.addEventListener('alpine:init', () => {
    window.Alpine.data('resumeSessionCta', resumeSessionCta);
});
