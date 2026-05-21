// PlayerService wraps the /api/players/me endpoints used by the
// "claim your display name" flow. The same anonymous row is returned
// across requests because EnsurePlayer middleware keeps the cookie
// stable, so the component can re-fetch /me any time it wants the
// latest username/isAnonymous/hasCustomName triple.

// readClaimNameError parses a PATCH /api/players/me error body. The
// server emits `{code, message}` JSON for the documented error paths
// (#289). Older / unexpected bodies (plain text from a proxy, an empty
// body) fall through to {} so the caller's status-based branching
// still works.
async function readClaimNameError(response) {
    try {
        return await response.clone().json();
    } catch {
        return {};
    }
}

export class PlayerService {
    // getMe returns {id, username, isAnonymous, hasCustomName}, or null
    // if the server somehow rejects the call (401 from a misconfigured
    // route, network failure, etc.). The component treats null as "skip
    // claim UI" so a broken auth wiring degrades to the previous
    // experience instead of throwing on page load.
    //
    // hasCustomName is what the frontend gates the claim affordances on
    // (#165): a registered or already-renamed visitor has it set, so the
    // modal does not re-open on a subsequent finished quiz. isAnonymous
    // remains for callers that care about the credential-level distinction.
    async getMe() {
        try {
            const response = await fetch('/api/players/me');
            if (!response.ok) return null;
            return await response.json();
        } catch {
            return null;
        }
    }

    // claimName PATCHes /api/players/me with a trimmed username and
    // returns a discriminated result the component can branch on
    // without inspecting raw status codes. The shape is:
    //   { ok: true,  player: {...} }                                       on 200
    //   { ok: false, status, kind: 'taken'|'already_claimed'|'empty'|'error', message }
    //
    // The two distinct 409 cases (#289) — "name in use by another row"
    // versus "this account is already non-anonymous" — surface as
    // 'taken' and 'already_claimed' respectively. The latter is a
    // state-drift signal: the client thought the player was anonymous
    // but the server disagrees, so the component should re-fetch /me
    // and dismiss the modal rather than show "name is taken".
    async claimName(rawUsername) {
        const username = (rawUsername || '').trim();
        if (username === '') {
            return { ok: false, status: 400, kind: 'empty', message: 'Please enter a name.' };
        }
        let response;
        try {
            response = await fetch('/api/players/me', {
                method: 'PATCH',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ username })
            });
        } catch {
            return { ok: false, status: 0, kind: 'error', message: "Couldn't save your name — try again later." };
        }
        if (response.status === 200) {
            const player = await response.json();
            return { ok: true, player };
        }
        if (response.status === 409) {
            const { code, message } = await readClaimNameError(response);
            if (code === 'already_claimed') {
                return {
                    ok: false, status: 409, kind: 'already_claimed',
                    message: message || 'This account already has a name.',
                };
            }
            return { ok: false, status: 409, kind: 'taken', message: 'That name is taken.' };
        }
        if (response.status === 400) {
            return { ok: false, status: 400, kind: 'empty', message: 'Please enter a name.' };
        }
        return { ok: false, status: response.status, kind: 'error', message: "Couldn't save your name — try again later." };
    }
}

export const playerService = new PlayerService();
