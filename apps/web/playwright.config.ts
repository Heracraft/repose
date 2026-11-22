import type { PlaywrightTestConfig } from '@playwright/test';
import { BASE_URL } from './tests/fixtures';

// No `webServer` entry: tests/global-setup.ts starts cmd/fakeapi, cmd/fake-
// logto and the already-built dashboard (`node build`) itself, since the
// dashboard's PUBLIC_API_URL/PUBLIC_LOGTO_ENDPOINT are only known once the
// two Go fixtures have picked their (random) ports. tests/fixtures.ts never
// reuses an existing server (`reuseExistingServer: false` semantics): it
// picks an ephemeral loopback port when REPOSE_WEB_TEST_PORT is unset, fails
// loudly if the target port is already occupied, and kills all spawned
// process groups on teardown and signals. Run `pnpm build` before
// `pnpm test:integration`.
const config: PlaywrightTestConfig = {
	testDir: 'tests',
	testMatch: /(.+\.)?spec\.ts/,
	globalSetup: './tests/global-setup.ts',
	use: {
		baseURL: BASE_URL,
		trace: 'retain-on-failure',
		// Playwright's own downloaded Chromium is unusable on this Nix dev
		// box (glibc mismatch); PLAYWRIGHT_CHROMIUM_PATH points at a working
		// one there (`nix shell nixpkgs#chromium`). CI and a normal machine
		// leave this unset and get Playwright's bundled browser as usual.
		launchOptions: process.env.PLAYWRIGHT_CHROMIUM_PATH
			? { executablePath: process.env.PLAYWRIGHT_CHROMIUM_PATH, args: ['--no-sandbox'] }
			: undefined
	},
	// One worker: every spec file shares the one fake api/fake Logto process
	// (one simulated account), so two files running at once would step on
	// each other's projects and settings. fullyParallel is redundant with
	// workers: 1 but documents the same intent at the file level.
	fullyParallel: false,
	workers: 1
};

export default config;
