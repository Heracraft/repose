// Lighthouse accessibility audits of every page a visitor or a signed-in
// user can reach, in both colour schemes and at two widths.
//
// 08 §9 names /projects and /projects/[id]/config and a score of 90. A score
// is an average, though, and one failed color-contrast audit still scores
// above 90, which is how muted text under 4.5:1 shipped past this gate. So a
// run fails on the aggregate under 90 and also on any binary audit that
// scores 0, unless KNOWN_FAILURES names that audit on that page with a
// reason.
//
// The signed-in pages need a session, and Lighthouse cannot sign in, so this
// runs Lighthouse against the Chromium instance Playwright has already
// signed in: the browser is launched with a CDP port, the test signs in, and
// the Lighthouse CLI attaches to that same browser with --port.
// tests-a11y/fixtures.ts explains why that browser is a persistent context.
// Each report's final URL is checked against the audited one, so an audit
// that was bounced to the landing page fails instead of scoring the wrong
// page.
//
// The landing is audited under prefers-reduced-motion: reduce, in a browser
// of its own (auditLanding below), so every run scores the same still frame
// instead of whichever moment of the pictures' loops Lighthouse happened to
// sample. Playwright applies a context's reducedMotion the way it applies
// colorScheme: with Emulation.setEmulatedMedia on every tab of the context,
// the one Lighthouse opens with --port included, before that tab runs.
//
// It audits the same bundle production serves (`pnpm build` output, run by
// tests/fixtures.ts), against internal/fakes/api rather than the deployed
// api. Accessibility is a property of the markup, which the api does not
// change; what a run against production would add is the real content's
// text lengths and colours, which the fake's fixtures already mirror.
import { execFile } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { promisify } from 'node:util';
import type { Page } from '@playwright/test';
import { test, expect } from './fixtures';
import {
	signIn,
	createProject,
	apiURLFromEnv,
	setBilling,
	resetBilling,
	BASE_URL
} from '../tests/helpers';
import { CDP_PORT } from './cdp';

const run = promisify(execFile);

const MINIMUM = 90;
const OUT_DIR = path.resolve(import.meta.dirname, '../test-results/lighthouse');

/**
 * Binary audits allowed to fail, by Lighthouse audit id, each with the pages
 * it may fail on (paths as in PAGES, or '*'), the widths it may fail at
 * (all when absent) and why it is tolerated. An entry is a debt with a
 * reason, never a way to quiet a run: delete it when the fix lands. Empty
 * means every binary audit must pass everywhere.
 */
const KNOWN_FAILURES: Record<
	string,
	{ pages: string[]; widths?: (keyof typeof WIDTHS)[]; reason: string }
> = {};

/** The two widths docs/LANDING.md judges a page at. */
const WIDTHS = {
	desktop: [
		'--form-factor=desktop',
		'--screenEmulation.mobile=false',
		'--screenEmulation.width=1440',
		'--screenEmulation.height=900',
		'--screenEmulation.deviceScaleFactor=1'
	],
	mobile: [
		'--form-factor=mobile',
		'--screenEmulation.mobile',
		'--screenEmulation.width=390',
		'--screenEmulation.height=844',
		'--screenEmulation.deviceScaleFactor=3'
	]
} as const;

interface Target {
	/** The path as reported, with the project id left as [id]. */
	name: string;
	signedIn: boolean;
	url: () => string;
	/** Waits in the test's own tab until the page has rendered its content. */
	ready: (page: Page) => Promise<void>;
	/** Puts the fake api into the state the audit should see. */
	setup?: () => Promise<void>;
	teardown?: () => Promise<void>;
}

let projectId = '';

const heading = (page: Page) => expect(page.locator('main h1').first()).toBeVisible();

// Public pages come first: each worker starts signed out (a fresh profile),
// and these are audited as a visitor sees them. The landing page renders
// differently once signed in.
const PAGES: Target[] = [
	{
		name: '/',
		signedIn: false,
		url: () => '/',
		ready: (page) => expect(page.getByRole('button', { name: 'Get started' })).toBeVisible()
	},
	{ name: '/docs', signedIn: false, url: () => '/docs', ready: heading },
	// A docs page with prompts, output and a copy button in its code blocks.
	{ name: '/docs/lifecycle', signedIn: false, url: () => '/docs/lifecycle', ready: heading },
	// The command tables, the widest in the docs at 390.
	{ name: '/docs/cli', signedIn: false, url: () => '/docs/cli', ready: heading },
	{ name: '/privacy', signedIn: false, url: () => '/privacy', ready: heading },
	{ name: '/terms', signedIn: false, url: () => '/terms', ready: heading },
	{ name: '/refunds', signedIn: false, url: () => '/refunds', ready: heading },
	{
		name: '/projects',
		signedIn: true,
		url: () => '/projects',
		ready: (page) => expect(page.locator('table')).toBeVisible()
	},
	{
		name: '/projects/[id]',
		signedIn: true,
		url: () => `/projects/${projectId}`,
		ready: (page) =>
			expect(page.getByRole('heading', { level: 1, name: /^a11y-app-/ })).toBeVisible()
	},
	{
		name: '/projects/[id]/config',
		signedIn: true,
		url: () => `/projects/${projectId}/config`,
		ready: (page) => expect(page.getByRole('tab', { name: 'Menu' })).toBeVisible()
	},
	{
		name: '/projects/[id]/secrets',
		signedIn: true,
		url: () => `/projects/${projectId}/secrets`,
		ready: (page) => expect(page.getByRole('heading', { level: 1, name: 'Secrets' })).toBeVisible()
	},
	{
		name: '/settings',
		signedIn: true,
		url: () => '/settings',
		ready: (page) => expect(page.getByRole('heading', { level: 1, name: 'Settings' })).toBeVisible()
	},
	// With no plan the page shows the three plan cards, its densest state;
	// the default fixture state (billing off) is a single sentence.
	{
		name: '/billing',
		signedIn: true,
		url: () => '/billing',
		ready: (page) => expect(page.getByRole('heading', { level: 2, name: 'Solo' })).toBeVisible(),
		setup: () => setBilling({ mode: 'none' }),
		teardown: resetBilling
	}
];

interface Audit {
	id: string;
	title: string;
	score: number | null;
	scoreDisplayMode: string;
	details?: { items?: { node?: { selector?: string; snippet?: string } }[] };
}

interface Result {
	score: number;
	/** Binary audits that scored 0, as "id: title (first offending nodes)". */
	failed: { id: string; line: string }[];
	finalUrl: string;
}

/**
 * Runs the Lighthouse CLI against the already-open browser and returns the
 * accessibility score out of 100 and the failed binary audits, leaving the
 * HTML and JSON reports on disk as the evidence 08 §9 asks for.
 */
async function audit(
	file: string,
	url: string,
	width: keyof typeof WIDTHS,
	port = CDP_PORT
): Promise<Result> {
	const out = path.join(OUT_DIR, file);
	await run(
		'lighthouse',
		[
			url,
			`--port=${port}`,
			'--only-categories=accessibility',
			'--output=json',
			'--output=html',
			`--output-path=${out}`,
			'--quiet',
			'--disable-full-page-screenshot',
			// The session lives in this origin's localStorage; Lighthouse's
			// default storage reset would sign the browser out between runs.
			'--disable-storage-reset',
			// Accessibility audits read the DOM, not timings, so simulated
			// throttling only makes the run slower.
			'--throttling-method=provided',
			...WIDTHS[width]
		],
		{ timeout: 180_000, maxBuffer: 32 * 1024 * 1024 }
	);
	const report = JSON.parse(fs.readFileSync(`${out}.report.json`, 'utf8'));
	const audits = Object.values(report.audits as Record<string, Audit>);
	const failed = audits
		.filter((a) => a.scoreDisplayMode === 'binary' && a.score === 0)
		.map((a) => {
			const nodes = (a.details?.items ?? [])
				.slice(0, 3)
				.map((i) => i.node?.selector ?? i.node?.snippet)
				.filter(Boolean);
			return {
				id: a.id,
				line: `${a.id}: ${a.title}${nodes.length ? ` (${nodes.join('; ')})` : ''}`
			};
		});
	return {
		score: Math.round(report.categories.accessibility.score * 100),
		failed,
		finalUrl: report.finalDisplayedUrl
	};
}

const signedInContexts = new WeakSet<object>();

async function ensureSignedIn(page: Page): Promise<void> {
	if (signedInContexts.has(page.context())) return;
	await signIn(page);
	signedInContexts.add(page.context());
}

test.beforeAll(async () => {
	fs.mkdirSync(OUT_DIR, { recursive: true });
	const p = await createProject(apiURLFromEnv(), {
		name: 'a11y-app',
		remote_url: 'github.com/heracraft/a11y-app'
	});
	projectId = p.id;
});

/** Fails the test on a bounce, a low score or a binary audit KNOWN_FAILURES does not name. */
function judge(target: Target, pathname: string, width: keyof typeof WIDTHS, result: Result): void {
	// A bounce to the landing page (signed out) would otherwise be scored as
	// if it were this page.
	expect(new URL(result.finalUrl).pathname, 'the page Lighthouse audited').toBe(pathname);
	expect(result.score).toBeGreaterThanOrEqual(MINIMUM);

	const unexpected = result.failed.filter((f) => {
		const known = KNOWN_FAILURES[f.id];
		if (!known || !(known.widths ?? [width]).includes(width)) return true;
		return !(known.pages.includes('*') || known.pages.includes(target.name));
	});
	expect(
		unexpected.map((f) => f.line),
		'failed binary audits'
	).toEqual([]);
}

/** The landing's browser listens here, beside the shared one on CDP_PORT. */
const LANDING_CDP_PORT = CDP_PORT + 1;

const slugOf = (target: Target) =>
	target.name.replace(/[^a-z0-9]+/gi, '-').replace(/^-|-$/g, '') || 'landing';

for (const target of PAGES) {
	for (const width of Object.keys(WIDTHS) as (keyof typeof WIDTHS)[]) {
		const title = `${target.name} at ${width} width passes every accessibility audit`;

		if (target.name === '/') {
			// The landing's pictures loop, so an audit with motion on samples
			// a different frame each run: a row mid-fade has a different
			// contrast from the same row at rest, and the result changed from
			// one run to the next. Under reduced motion every picture draws
			// its final frame and stops (LANDING.md, "Motion"), so each run
			// scores the same page. The shared browser's context was made
			// without reducedMotion and Playwright sets it per context, so
			// the landing gets a browser of its own, signed out on a fresh
			// profile as a visitor is, on a port of its own.
			test(title, async ({ playwright, launchOptions, scheme }) => {
				const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'repose-a11y-landing-'));
				const context = await playwright.chromium.launchPersistentContext(dir, {
					...launchOptions,
					args: (launchOptions.args ?? []).map((a) =>
						a.startsWith('--remote-debugging-port=')
							? `--remote-debugging-port=${LANDING_CDP_PORT}`
							: a
					),
					baseURL: BASE_URL,
					colorScheme: scheme,
					reducedMotion: 'reduce',
					viewport: null
				});
				// What the tab Lighthouse opens reports for the media query:
				// the evidence that the emulation reached it, not only this
				// test's own tab.
				const seen: boolean[] = [];
				context.on('page', async (tab) => {
					for (let i = 0; i < 50 && !tab.isClosed(); i++) {
						try {
							seen.push(
								await tab.evaluate(() => matchMedia('(prefers-reduced-motion: reduce)').matches)
							);
							return;
						} catch {
							// Mid-navigation: the document went away under the call.
							await new Promise((r) => setTimeout(r, 100));
						}
					}
				});
				try {
					const page = context.pages()[0] ?? (await context.newPage());
					const pathname = target.url();
					await page.goto(pathname);
					await target.ready(page);
					expect(
						await page.evaluate(() => matchMedia('(prefers-reduced-motion: reduce)').matches),
						'reduced motion in the test tab'
					).toBe(true);

					const result = await audit(
						`${slugOf(target)}-${scheme}-${width}`,
						`${BASE_URL}${pathname}`,
						width,
						LANDING_CDP_PORT
					);
					console.log(`${target.name} ${scheme} ${width}: ${result.score}`);
					// One tab, so one reading: [true] (a probe run printed exactly that).
					expect(seen, 'reduced motion in the tab Lighthouse audited').toEqual([true]);
					judge(target, pathname, width, result);
				} finally {
					await context.close();
					fs.rmSync(dir, { recursive: true, force: true });
				}
			});
			continue;
		}

		test(title, async ({ page, scheme }) => {
			if (target.signedIn) await ensureSignedIn(page);
			await target.setup?.();
			try {
				const pathname = target.url();
				await page.goto(pathname);
				await target.ready(page);

				const result = await audit(
					`${slugOf(target)}-${scheme}-${width}`,
					`${BASE_URL}${pathname}`,
					width
				);
				console.log(`${target.name} ${scheme} ${width}: ${result.score}`);
				judge(target, pathname, width, result);
			} finally {
				await target.teardown?.();
			}
		});
	}
}
