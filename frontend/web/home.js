// home.js wires the home-page Alpine islands. Currently it registers the
// resume-session CTA (#888) that flips the public "Join a live game" button
// to a "Resume session" deep link when the player has a remembered session
// from a previous join (the localStorage entry JoinApp writes on a successful
// join). With no remembered entry the default CTA shows.
//
// The remembered-session shape mirrors JoinApp.SESSION_STORAGE_KEY exactly so
// the home page reads what the client surface wrote. Keeping the read-side
// code here means home doesn't pull in the player-client bundle to look at
// localStorage. esbuild bundles this to dist/home.js, served at
// /assets/js/home.js.

const SESSION_STORAGE_KEY = 'topbanana.session';

function readRememberedCode() {
    let raw;
    try {
        raw = window.localStorage.getItem(SESSION_STORAGE_KEY);
    } catch {
        return '';
    }
    if (!raw) return '';
    try {
        const parsed = JSON.parse(raw);
        if (parsed && typeof parsed.code === 'string' && parsed.code !== '') {
            return parsed.code;
        }
    } catch {
        // Malformed entry; ignore.
    }
    return '';
}

function forgetRememberedSession() {
    try {
        window.localStorage.removeItem(SESSION_STORAGE_KEY);
    } catch {
        // Storage is unavailable; the entry will self-heal on a later
        // failed resume.
    }
}

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
            this.resumeCode = readRememberedCode();
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
