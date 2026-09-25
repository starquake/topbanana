// An expired session 303s a fetch or XHR to /login, which the browser follows
// to a 200 HTML page, so callers check the final URL instead of the status.
export const SESSION_EXPIRED_MESSAGE = 'Your session has expired. Sign in again.';

export function isLoginRedirect(url) {
    if (!url || typeof window === 'undefined') return false;
    try {
        return new URL(url, window.location.href).pathname === '/login';
    } catch {
        return false;
    }
}
