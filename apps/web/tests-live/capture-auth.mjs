// Captures a real signed-in browser session for the live suite.
//
//   pnpm --filter web run live:auth
//
// Opens a browser at the deployed dashboard, waits while you sign in with
// GitHub through the real Logto, and saves the resulting browser state to
// tests-live/.auth/state.json. `pnpm --filter web run live` then runs the
// signed-in half of the suite with it.
//
// That file holds a Logto refresh token. It is git-ignored, written 0600,
// and revoked by signing out in the dashboard (or by deleting the session
// in Logto). Treat it as a password; do not copy it anywhere.
//
// Plain JavaScript, not TypeScript, so it runs with bare `node` — this is a
// one-off operator script, not part of the test run.
import fs from 'node:fs';
import path from 'node:path';
import { chromium } from '@playwright/test';

const LIVE_URL = (process.env.REPOSE_LIVE_URL ?? 'https://repose.run').replace(/\/$/, '');
const AUTH_DIR = path.resolve(import.meta.dirname, '.auth');
const AUTH_STATE = path.join(AUTH_DIR, 'state.json');
const TIMEOUT_MS = Number(process.env.REPOSE_LIVE_AUTH_TIMEOUT_MS ?? 5 * 60 * 1000);

const launchOptions = process.env.PLAYWRIGHT_CHROMIUM_PATH
	? { executablePath: process.env.PLAYWRIGHT_CHROMIUM_PATH, args: ['--no-sandbox'] }
	: {};

const browser = await chromium.launch({ headless: false, ...launchOptions });
const context = await browser.newContext();
const page = await context.newPage();

console.log(`Opening ${LIVE_URL} — sign in with GitHub in the window that just opened.`);
await page.goto(LIVE_URL);

try {
	await page.waitForURL(`${LIVE_URL}/projects`, { timeout: TIMEOUT_MS });
} catch {
	console.error(
		`Did not reach ${LIVE_URL}/projects within ${Math.round(TIMEOUT_MS / 1000)}s; nothing saved.`
	);
	await browser.close();
	process.exit(1);
}

// The Logto SDK writes its tokens after the callback resolves; give the
// storage write a moment rather than racing it.
await page.waitForTimeout(1500);

fs.mkdirSync(AUTH_DIR, { recursive: true, mode: 0o700 });
await context.storageState({ path: AUTH_STATE });
fs.chmodSync(AUTH_STATE, 0o600);
await browser.close();

console.log(`Saved ${AUTH_STATE} (0600). Run: pnpm --filter web run live`);
