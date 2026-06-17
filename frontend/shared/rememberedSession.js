// rememberedSession is the single source of truth for the remembered live-game
// join entry shared between the player client (JoinApp) and the home page CTA.
// The player client writes a { code } entry on a successful join so a reload or
// brief drop can resume; the home page reads the same entry to flip its
// "Join a live game" button to a "Resume session" deep link (#888). Keeping the
// key string and parse shape in one module means the two bundle trees stay in
// lockstep by construction rather than by a fragile mirrored comment (#1005).

// SESSION_STORAGE_KEY holds the remembered { code } the player joined (MP-10 /
// #687). One key; cleared when the lobby is gone or on an explicit leave.
export const SESSION_STORAGE_KEY = 'topbanana.session';

// forgetRememberedSession clears the remembered entry. Best-effort: a storage
// exception (private mode, unavailable storage) is swallowed, since the stale
// entry self-heals on the next failed resume.
export function forgetRememberedSession() {
    try {
        window.localStorage.removeItem(SESSION_STORAGE_KEY);
    } catch {
        // Storage is unavailable; nothing to recover from.
    }
}

// readRememberedSession returns the remembered { code }, or null when there is
// nothing usable stored. Guards against a malformed or partial entry, clearing
// it so a corrupt value self-heals rather than failing every read.
export function readRememberedSession() {
    let raw;
    try {
        raw = window.localStorage.getItem(SESSION_STORAGE_KEY);
    } catch {
        return null;
    }
    if (!raw) return null;
    try {
        const parsed = JSON.parse(raw);
        if (parsed && typeof parsed.code === 'string' && parsed.code !== '') {
            return { code: parsed.code };
        }
    } catch {
        // Fall through to the cleanup below.
    }
    forgetRememberedSession();
    return null;
}
