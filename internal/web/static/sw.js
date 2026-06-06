// Top Banana service worker.
//
// Scope: site-wide (served from /sw.js so the registration's default
// scope is `/`). Strategy: precache the static shell on install,
// serve those URLs cache-first, and pass everything else through to
// the network untouched. No offline gameplay - scoring is
// server-authoritative.
//
// CACHE_VERSION is substituted by the Go handler at request time so
// each deploy invalidates the previous cache. The literal string
// "__CACHE_VERSION__" below is the placeholder.

const CACHE_VERSION = '__CACHE_VERSION__';
const CACHE_NAME = 'topbanana-shell-' + CACHE_VERSION;

const PRECACHE_URLS = [
    '/manifest.webmanifest',
    '/assets/css/app.css',
    '/assets/fonts/inter-latin.woff2',
    '/assets/fonts/inter-latin-ext.woff2',
    '/assets/fonts/orbitron-latin.woff2',
    '/assets/js/htmx.min.js',
    '/assets/js/dist/share.js',
    '/assets/js/vendor/alpine.min.js',
    '/assets/banana.svg',
    '/assets/banana-192.png',
    '/assets/banana-512.png',
    '/assets/banana-maskable-512.png',
    '/assets/og-image.png',
];

// Paths that must always hit the network: live API responses, the
// leaderboard SSE stream, admin templates rendered per-request, the
// player SPA (which embeds per-quiz Open Graph metadata at render
// time), and the auth flows. Matched as prefixes against the
// request URL's pathname.
const NETWORK_ONLY_PREFIXES = [
    '/api/',
    '/admin/',
    '/play/',
    '/client/',
    '/login',
    '/logout',
    '/register',
    '/profile',
    '/forgot-password',
    '/reset-password',
    '/verify-email',
    '/healthz',
];

// Precache each URL individually rather than via cache.addAll so a
// single 404 (e.g. an asset rename that misses PRECACHE_URLS) does
// not fail the whole install and leave the SW stuck in installing.
self.addEventListener('install', (event) => {
    event.waitUntil(
        caches.open(CACHE_NAME).then((cache) => Promise.all(
            PRECACHE_URLS.map((url) => cache.add(url).catch(() => undefined)),
        )),
    );
    self.skipWaiting();
});

self.addEventListener('activate', (event) => {
    event.waitUntil(
        caches.keys().then((keys) => Promise.all(
            keys
                .filter((k) => k.startsWith('topbanana-shell-') && k !== CACHE_NAME)
                .map((k) => caches.delete(k)),
        )).then(() => self.clients.claim()),
    );
});

function isNetworkOnly(url) {
    for (const prefix of NETWORK_ONLY_PREFIXES) {
        if (url.pathname.startsWith(prefix)) {
            return true;
        }
    }
    return false;
}

self.addEventListener('fetch', (event) => {
    const req = event.request;
    if (req.method !== 'GET') {
        return;
    }

    const url = new URL(req.url);
    if (url.origin !== self.location.origin) {
        return;
    }
    if (isNetworkOnly(url)) {
        return;
    }

    event.respondWith(
        caches.match(req).then((cached) => cached || fetch(req)),
    );
});
