import { jsonOrThrow } from './api.js';

// SessionService wraps the hosted live-session REST endpoints the player
// join + lobby surface consumes (MP-4 / #681). The session cookie carries
// the anonymous player identity (EnsurePlayer upgrades a cookieless visitor
// on the first call), so no token is threaded through here.
//
// The lobby is driven by the SSE event channel as a pure side-channel:
// every tick (and the initial frame on subscribe) means "re-GET state".
// getState is the authoritative read; the component owns the EventSource.
//
// 404 is a business signal on join/ready/state: an unknown room code, or a
// non-participant probing a code, both surface as 404. join maps it to a
// `notFound` result so the form can show "no game with that code"; ready
// and getState rethrow via jsonOrThrow for the caller to branch on.
export class SessionService {
    // join adds the caller to the session under displayName. On success it
    // returns { ok: true, displayName, isReady } - the displayName echoed
    // back is the one the player actually landed with, since a per-session
    // collision is resolved server-side with a petname fallback. A 404
    // (unknown code) maps to { ok: false, kind: 'notFound' }; a 409 (the
    // session already started, so the lobby has closed) to { ok: false,
    // kind: 'closed' }; a 403 (the player has already played this quiz) to
    // { ok: false, kind: 'alreadyPlayed' }; a 400 (empty name) to
    // { ok: false, kind: 'empty' }; anything else to { ok: false, kind:
    // 'error' }.
    async join(code, rawDisplayName) {
        const displayName = (rawDisplayName || '').trim();
        if (displayName === '') {
            return { ok: false, kind: 'empty', message: 'Please enter a name.' };
        }
        let response;
        try {
            response = await fetch(`/api/sessions/${encodeURIComponent(code)}/join`, {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ displayName }),
            });
        } catch {
            return { ok: false, kind: 'error', message: "Couldn't join the game - try again." };
        }
        if (response.status === 200) {
            const body = await response.json();
            return { ok: true, displayName: body.displayName, isReady: body.isReady };
        }
        if (response.status === 404) {
            return { ok: false, kind: 'notFound', message: 'No game found with that code.' };
        }
        if (response.status === 409) {
            return { ok: false, kind: 'closed', message: 'This game has already started.' };
        }
        if (response.status === 403) {
            return { ok: false, kind: 'alreadyPlayed', message: "You've already played this quiz." };
        }
        if (response.status === 400) {
            return { ok: false, kind: 'empty', message: 'Please enter a name.' };
        }
        return { ok: false, kind: 'error', message: "Couldn't join the game - try again." };
    }

    // setReady flips the caller's ready flag. The same endpoint marks ready
    // and un-ready via the body. Returns nothing on the 204; throws on any
    // other status so a transient failure surfaces as a retry banner rather
    // than silently dropping the toggle.
    async setReady(code, ready) {
        const response = await fetch(`/api/sessions/${encodeURIComponent(code)}/ready`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ ready }),
        });
        if (response.ok) return;
        await jsonOrThrow(response);
    }

    // answer records the caller's pick for the session's current question.
    // The server timestamps the pick on its own clock (the body carries only
    // the chosen option), so no client time is threaded through. Resolves to
    // { ok: true } on the 204, { ok: false, kind: 'closed' } on a 409 (the
    // answer window closed or no question is open - a benign race the UI
    // absorbs by holding the answered state anyway), and throws on any other
    // status so a transient failure surfaces as a retry banner rather than
    // silently dropping the pick.
    async answer(code, optionId) {
        const response = await fetch(`/api/sessions/${encodeURIComponent(code)}/answer`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ optionId }),
        });
        if (response.status === 204) {
            return { ok: true };
        }
        if (response.status === 409) {
            return { ok: false, kind: 'closed' };
        }
        await jsonOrThrow(response);
        return { ok: true };
    }

    // leave drops the caller from the session so their row falls out of the
    // roster, answered-order badges, and standings at once (MP-10). It fires
    // on tab close via navigator.sendBeacon, which the browser flushes during
    // unload where a fetch would be cancelled. Best-effort: sendBeacon returns
    // whether the request was queued, and a missed leave self-heals (the player
    // ages out of the active window), so there is nothing to await or recover.
    leave(code) {
        return navigator.sendBeacon(`/api/sessions/${encodeURIComponent(code)}/leave`);
    }

    // getState returns the authoritative lobby state, or null on a 404
    // (the session vanished, or the caller is no longer a participant). The
    // component treats null as "the lobby is gone" and surfaces it rather
    // than throwing on every poll.
    async getState(code) {
        const response = await fetch(`/api/sessions/${encodeURIComponent(code)}/state`);
        if (response.status === 404) {
            return null;
        }
        return jsonOrThrow(response);
    }
}

export const sessionService = new SessionService();
