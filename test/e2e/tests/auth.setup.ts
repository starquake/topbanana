import { test as setup, expect } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { join } from 'node:path';

import { adminStatePath, SEED_ADMIN_EMAIL, SEED_ADMIN_PASSWORD, SEED_ADMIN_PASSWORD_HASH } from '../e2e-auth';
import { csrfTokenPattern } from './helpers';

// Logs the seed admin (players.id = 1) in through the real /login flow and
// saves the cookie jar as the shared storageState the admin specs reuse, so the
// suite exercises real Go session issuance rather than minting a cookie.
//
// Uses the `request` API, not a browser page: CI installs only the one
// matrixed browser per job, so a page-based setup defaulting to chromium fails
// on the firefox job. Login is a plain form POST, so HTTP suffices.
//
// The seed admin ships passwordless on 'host' (roles migration #538), so first
// stamp a real bcrypt hash and promote it to admin. Runs against worker 0 (the
// default baseURL); the worker fixture does the same on the others.
setup('authenticate the shared admin', async ({ request }) => {
  const dataDir = process.env.TOPBANANA_E2E_DATA_DIR;
  if (!dataDir) {
    throw new Error('TOPBANANA_E2E_DATA_DIR is not set; cannot prepare the seed admin');
  }
  // SEED_ADMIN_PASSWORD_HASH is a fixed bcrypt constant (chars [./A-Za-z0-9$],
  // no quotes), so it is safe to interpolate directly - unlike the
  // user-derived values in markEmailVerified/setRole, it needs no escaping.
  execFileSync('sqlite3', [
    join(dataDir, 'e2e-0.db'),
    `UPDATE players SET role = 'admin', password_hash = '${SEED_ADMIN_PASSWORD_HASH}' WHERE id = 1;`,
  ]);

  // GET the login form to seed the CSRF cookie on the request jar and read the
  // matching hidden token.
  const formHtml = await (await request.get('/login')).text();
  const match = csrfTokenPattern.exec(formHtml);
  const csrfToken = match?.[1] ?? match?.[2];
  if (!csrfToken) {
    throw new Error(`no csrf_token found in /login response; body=${formHtml}`);
  }

  // A successful login 303-redirects to the role landing; maxRedirects 0 keeps
  // the 303 (and its Set-Cookie session) visible rather than following it.
  const resp = await request.post('/login', {
    form: { email: SEED_ADMIN_EMAIL, password: SEED_ADMIN_PASSWORD, csrf_token: csrfToken },
    maxRedirects: 0,
  });
  expect(resp.status(), `login failed: ${resp.status()} ${await resp.text()}`).toBe(303);

  await request.storageState({ path: adminStatePath() });
});
