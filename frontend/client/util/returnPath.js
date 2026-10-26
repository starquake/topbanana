// setReturnPath replaces the page URL with path and updates the switcher links' next to match.
export function setReturnPath(path) {
    window.history.replaceState(window.history.state, '', path);
    for (const link of document.querySelectorAll('a[data-lang-link]')) {
        const url = new URL(link.href, window.location.origin);
        url.searchParams.set('next', path);
        link.href = url.pathname + url.search;
    }
}
