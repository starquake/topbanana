import { execFileSync } from 'node:child_process';

// execSqlite runs a SQL string against a worker DB through the sqlite3 CLI.
// `-cmd '.timeout 5000'` runs the .timeout dot-command (busy_timeout = 5s)
// BEFORE the positional SQL, so a write that hits a lock the open server
// holds waits up to 5s rather than failing instantly with SQLITE_BUSY -
// the CLI carries no busy timeout of its own (#745). The -cmd flag must
// precede dbFile. Returns trimmed stdout.
//
// Lives in its own module so both helpers.ts and the worker-scoped
// fixtures.ts can use it: helpers.ts imports from fixtures.ts, so defining
// it there and importing back would be a cycle.
export function execSqlite(dbFile: string, sql: string): string {
  return execFileSync('sqlite3', ['-cmd', '.timeout 5000', dbFile, sql], {
    encoding: 'utf8',
  }).trim();
}
