import { test, expect } from './fixtures';

// #466 - the home page links a web app manifest and a root-scoped
// service worker. The browser only honours the install prompt when
// both are reachable and the SW activates without scripting errors,
// so we pin that contract here. The console-error check guards against
// regressions where the SW throws during install (e.g. one of the
// precache URLs 404s).
test('home page exposes the manifest and registers the service worker', async ({ page, browserName }) => {
  // Firefox in Playwright (>=1.46) supports the Service Worker API but
  // skipWaiting + clients.claim behaviour is more permissive than
  // Chromium's. We still assert ready resolves on both engines; the
  // registration object itself is what we care about.
  const consoleErrors: string[] = [];
  page.on('console', (msg) => {
    if (msg.type() === 'error') {
      consoleErrors.push(msg.text());
    }
  });
  page.on('pageerror', (err) => {
    consoleErrors.push(err.message);
  });

  await page.goto('/');

  // Non-production deploys prefix title + manifest name with the env
  // label (e.g. "[development] Top Banana!"); CI runs with
  // APP_ENV=development.
  await expect(page).toHaveTitle(/Top Banana!$/);

  const manifestHref = await page.locator('link[rel="manifest"]').getAttribute('href');
  expect(manifestHref).toBe('/manifest.webmanifest');

  const manifestResp = await page.request.get('/manifest.webmanifest');
  expect(manifestResp.status()).toBe(200);
  const manifest = await manifestResp.json();
  expect(manifest.name).toMatch(/Top Banana!$/);
  expect(manifest.start_url).toBe('/');
  expect(manifest.display).toBe('standalone');
  expect(Array.isArray(manifest.icons)).toBeTruthy();
  expect(manifest.icons.length).toBeGreaterThanOrEqual(3);
  const hasMaskable = manifest.icons.some((i: { purpose?: string }) => (i.purpose ?? '').includes('maskable'));
  expect(hasMaskable).toBeTruthy();

  const swReadyState = await page.evaluate(async () => {
    if (!('serviceWorker' in navigator)) {
      return 'unsupported';
    }
    const reg = await navigator.serviceWorker.ready;
    const worker = reg.active ?? reg.installing ?? reg.waiting;
    return worker ? worker.state : 'no-worker';
  });
  // 'activated' is the goal; on Firefox the first navigation may still
  // see 'activating' if clients.claim has just landed. Both prove
  // install succeeded without throwing.
  expect(['activated', 'activating']).toContain(swReadyState);

  // SW install errors surface either as page errors (the install handler
  // throws) or as console errors emitted by the browser when an
  // addAll() URL fails. Exclude unrelated chatter (e.g. third-party
  // font CSS warnings) by matching on SW-only signals.
  const swErrors = consoleErrors.filter((m) => /service ?worker|sw\.js|cache/i.test(m));
  expect(swErrors, `unexpected SW console errors: ${swErrors.join(' | ')}`).toHaveLength(0);

  // Avoid leaving an active SW behind in the per-worker browser
  // context — the next spec that hits / on the same browser context
  // would inherit it and the cache-first handler could mask intended
  // server responses. Firefox lets you unregister; chromium too.
  await page.evaluate(async () => {
    if (!('serviceWorker' in navigator)) return;
    const regs = await navigator.serviceWorker.getRegistrations();
    await Promise.all(regs.map((r) => r.unregister()));
  });

  // Suppress unused-var lint on browserName: kept in scope so the test
  // signature mirrors the rest of the suite and engine-specific logic
  // can land here without a parameter rename.
  void browserName;
});
