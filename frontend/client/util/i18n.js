// i18n is the player SPA's client-side translation lookup (#1115). The server
// renders the static shell text through its own {{t}} func, but the strings the
// JS computes at runtime (verdict copy, share text, error banners, ternary
// labels) can't go through that, so they resolve here against the catalog the
// shell injects as window.__I18N__ (the resolved locale plus the full merged
// message map for it). It mirrors the server locale.Translate contract: a
// missing key falls back to the key itself, so an untranslated string is
// visible but never blank. There is deliberately no i18n library.

// PLACEHOLDER matches {name} tokens for simple named interpolation.
const PLACEHOLDER = /\{(\w+)\}/g;

// catalog returns the injected message map, or an empty object when the global
// is absent (a unit test that never rendered the shell, or a very early call).
function catalog() {
    if (typeof window === 'undefined' || !window.__I18N__) return {};

    return window.__I18N__.messages || {};
}

// t looks up key in the injected catalog and, when params is given, replaces
// {name} placeholders with the matching param value. An unknown key returns the
// key itself; an unmatched placeholder is left as-is. The injected map is the
// English catalog overlaid with the active locale, so every known key already
// resolves to a value without a per-lookup English fallback here.
export function t(key, params) {
    const messages = catalog();
    let text = Object.prototype.hasOwnProperty.call(messages, key) ? messages[key] : key;
    if (params) {
        text = text.replace(PLACEHOLDER, (match, name) =>
            Object.prototype.hasOwnProperty.call(params, name) ? String(params[name]) : match);
    }

    return text;
}

// registerI18n exposes t to Alpine templates as the $t magic, so the shells can
// localize inline x-text expressions (e.g. ternary verdict labels) the same way
// components call t() in JS. Registering it in one place keeps every surface
// reading window.__I18N__ through this module rather than reaching into the
// global ad hoc.
export function registerI18n(Alpine) {
    Alpine.magic('t', () => t);
}
