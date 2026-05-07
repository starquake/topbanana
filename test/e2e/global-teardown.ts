import { rmSync } from 'fs';

export default function globalTeardown(): void {
  const dataDir = process.env.TOPBANANA_E2E_DATA_DIR;
  if (dataDir) {
    rmSync(dataDir, { recursive: true, force: true });
  }
}
