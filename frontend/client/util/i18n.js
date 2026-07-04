// i18n resolves the strings the SPA computes at runtime (verdict copy, share
// text, error banners) against the catalog the shell injects as window.__I18N__
// (#1115). Mirrors the server locale.Translate contract: a missing key falls
// back to the key itself so an untranslated string is visible, never blank.

// PLACEHOLDER matches {name} tokens for named interpolation.
const PLACEHOLDER = /\{(\w+)\}/g;

// catalog returns the injected message map, or an empty object when the global
// is absent (a unit test that never rendered the shell, or a very early call).
function catalog() {
    if (typeof window === 'undefined' || !window.__I18N__) return {};

    return window.__I18N__.messages || {};
}

// t looks up key in the injected catalog and, when params is given, replaces
// {name} placeholders with the matching param value. An unknown key returns the
// key itself; an unmatched placeholder is left as-is.
export function t(key, params) {
    const messages = catalog();
    let text = Object.prototype.hasOwnProperty.call(messages, key) ? messages[key] : key;
    if (params) {
        text = text.replace(PLACEHOLDER, (match, name) =>
            Object.prototype.hasOwnProperty.call(params, name) ? String(params[name]) : match);
    }

    return text;
}

// registerI18n exposes t to Alpine templates as the $t magic so shells can
// localize inline x-text expressions the same way JS calls t().
export function registerI18n(Alpine) {
    Alpine.magic('t', () => t);
}
