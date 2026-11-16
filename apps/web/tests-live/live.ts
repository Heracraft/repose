// Shared configuration for the live suite: the tests that run against a
// deployed dashboard (https://repose.run) rather than against
// internal/fakes/api. `tests/` is the fake-backed suite and stays the one CI
// runs; this one closes the rows of docs/workstreams/08-dashboard.md §9 that
// say "the real Logto" or need production, which a fake cannot close by
// construction.
import fs from 'node:fs';
import path from 'node:path';

export const LIVE_URL = (process.env.REPOSE_LIVE_URL ?? 'https://repose.run').replace(/\/$/, '');

/**
 * Where `pnpm run live:auth` writes the signed-in browser state. It holds a
 * Logto refresh token in localStorage, so it is a credential: the directory
 * is git-ignored and the file is written 0600.
 */
export const AUTH_DIR = path.resolve(import.meta.dirname, '.auth');
export const AUTH_STATE = path.join(AUTH_DIR, 'state.json');

export function haveAuthState(): boolean {
	return fs.existsSync(AUTH_STATE);
}

/** The reason an account test is skipped, phrased as the way to fix it. */
export const NO_AUTH_REASON =
	`no saved sign-in at ${AUTH_STATE}: run \`pnpm --filter web run live:auth\` once ` +
	`(it opens a browser, you sign in, it saves the session).`;
