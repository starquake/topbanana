import { createHmac } from 'node:crypto';
import { writeFileSync } from 'node:fs';

import { SESSION_KEY, SESSION_COOKIE, SEED_ADMIN_ID, adminStatePath } from './e2e-auth';

// globalSetup mints one session cookie for the migration-seeded admin and
// writes it into a Playwright storageState file the admin specs reuse,
// replacing the per-spec register -> verify -> login dance. The cookie is
// signed with SESSION_KEY (the same key every per-worker server runs with)
// and scoped to domain 127.0.0.1 with no port, so it authenticates on every
// worker regardless of which port that worker's server listens on.
//
// Cookie format mirrors internal/session (session.go is the contract):
//   base64url(playerID|sessionVersion|issuedAt) + "." +
//     base64url(hmac_sha256(SESSION_KEY, payload))
// integerBase is 10 and the encoding is Go's RawURLEncoding (unpadded),
// which Node's 'base64url' matches. Reproduced here rather than shelling
// out to Go so the suite stays in one language; if session.go ever changes
// the scheme, the session unit tests fail and the e2e admin specs 303 to
// /login, surfacing the drift.
export default function globalSetup(): void {
  const issuedAt = Math.floor(Date.now() / 1000);
  const payload = `${SEED_ADMIN_ID}|0|${issuedAt}`;
  const payloadPart = Buffer.from(payload).toString('base64url');
  const macPart = createHmac('sha256', SESSION_KEY).update(payload).digest('base64url');
  const value = `${payloadPart}.${macPart}`;

  const state = {
    cookies: [
      {
        name: SESSION_COOKIE,
        value,
        domain: '127.0.0.1',
        path: '/',
        expires: -1,
        httpOnly: true,
        secure: false,
        sameSite: 'Lax' as const,
      },
    ],
    origins: [],
  };

  writeFileSync(adminStatePath(), JSON.stringify(state));
}
