import type { PlaywrightTestConfig } from '@playwright/test';
import { LIVE_URL, AUTH_STATE, haveAuthState } from './tests-live/live';

// The live suite. Unlike playwright.config.ts it starts nothing: the site
// under test is already deployed, and REPOSE_LIVE_URL points at it
// (https://repose.run by default, a staging deploy otherwise).
//
// Two projects, because one half needs a real GitHub sign-in and the other
// does not:
//
//   public   — everything a signed-out visitor sees, plus the sign-in
//              redirect into the real Logto. Always runs.
//   account  — the signed-in pages, using the browser state saved by
//              `pnpm run live:auth`. Skipped with a message when that state
//              is absent, rather than failing, so `pnpm run live` is
//              useful to anyone without the owner's account.
//
// Nothing here mutates production: the account project reads pages and
// asserts on controls (the delete-account button's disabled state, for
// one), and never presses a destructive one.
const config: PlaywrightTestConfig = {
	testDir: 'tests-live',
	testMatch: /(.+\.)?spec\.ts/,
	reporter: process.env.CI ? [['list'], ['html', { open: 'never' }]] : [['list']],
	use: {
		baseURL: LIVE_URL,
		trace: 'retain-on-failure',
		// Same dev-box escape hatch as the fake-backed config: Playwright's
		// downloaded Chromium does not run on this Nix box.
		launchOptions: process.env.PLAYWRIGHT_CHROMIUM_PATH
			? { executablePath: process.env.PLAYWRIGHT_CHROMIUM_PATH, args: ['--no-sandbox'] }
			: undefined
	},
	projects: [
		{ name: 'public', testMatch: /public\.spec\.ts/ },
		{
			name: 'account',
			testMatch: /account\.spec\.ts/,
			use: haveAuthState() ? { storageState: AUTH_STATE } : {}
		}
	],
	// One account, so two signed-in specs at once would fight over settings.
	fullyParallel: false,
	workers: 1
};

export default config;
