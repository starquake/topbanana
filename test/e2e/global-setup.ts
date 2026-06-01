import { execFileSync } from 'node:child_process';
import { writeFileSync } from 'node:fs';
import { join } from 'node:path';

import { SESSION_KEY, SESSION_COOKIE, adminStatePath } from './e2e-auth';

// globalSetup mints one session cookie for the migration-seeded admin and
// writes it into a Playwright storageState file the admin specs reuse. The
// cookie is signed with SESSION_KEY (the same key every per-worker server
// runs with) and scoped to domain 127.0.0.1 with no port, so it
// authenticates on every worker regardless of which port that worker's
// server listens on. This replaces the per-spec register -> verify -> login
// dance with a single cookie mint.
export default function globalSetup(): void {
  const value = execFileSync('go', ['run', './cmd/e2e-admin-session'], {
    cwd: join(__dirname, '..', '..'),
    env: { ...process.env, SESSION_KEY },
    encoding: 'utf8',
  }).trim();

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
