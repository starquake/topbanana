// Shared HTTP helpers for the per-feature service classes.
//
// Before #287 the services all did `return await response.json()`
// without checking response.ok. Non-2xx bodies from the Go side are
// plain text (`http.Error` writes `text/plain`), so `response.json()`
// threw a `SyntaxError`. Some callers caught the throw and surfaced
// it (the retry banner in submitAnswer); others let it bubble as an
// unhandled rejection. Both shapes are wrong — the retry banner fired
// on 400/404 too, and `startGame` crashed silently on a 409 race.
//
// The new contract:
//   - 2xx → return the parsed JSON body.
//   - any other status → throw an [ApiError] with `.status` and `.body`
//     set so the caller can branch on the status.
// Callers that have a defined "absent" semantics (e.g. 404 on
// getNextQuestion meaning "no more questions") still short-circuit
// to `return null` BEFORE calling jsonOrThrow.

// ApiError carries the HTTP status and (best-effort) raw body from a
// non-2xx response so the caller can branch on `.status` instead of
// pattern-matching error strings.
export class ApiError extends Error {
    constructor(message, status, body) {
        super(message);
        this.name = 'ApiError';
        this.status = status;
        this.body = body;
    }
}

// jsonOrThrow returns response.json() on 2xx, otherwise throws an
// ApiError. The raw body is captured (best-effort — a network error
// during reading just leaves body=""), capped at 200 chars in the
// thrown message so a giant 500 page doesn't show up in console
// stacks.
export async function jsonOrThrow(response) {
    if (response.ok) {
        return await response.json();
    }
    let body = '';
    try {
        body = await response.text();
    } catch {
        // Leave body empty — the status is the load-bearing field for
        // callers; the body is just for logs.
    }
    const preview = body.slice(0, 200);
    throw new ApiError(`HTTP ${response.status}: ${preview}`, response.status, body);
}
