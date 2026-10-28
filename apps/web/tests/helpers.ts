import type { Locator, Page } from '@playwright/test';
import { BASE_URL } from './fixtures';

/** Drives the whole fake-Logto authorization-code round trip. */
export async function signIn(page: Page): Promise<void> {
	await page.goto('/');
	// A link since I-878; hydrated, its click handler runs the browser flow.
	await page.getByRole('link', { name: 'Get started' }).click();
	await page.waitForURL(/\/oidc\/auth/);
	await page.getByRole('button', { name: 'Continue as heracraft' }).click();
	await page.waitForURL(/\/projects/);
}

/**
 * Creates a project directly against the fake api (no CLI in this suite).
 * The name and remote get a random suffix so a retried test, or a test
 * file that runs more than once in the same fixture process, never hits
 * the (user, name) uniqueness conflict.
 */
export async function createProject(
	apiURL: string,
	body: { name: string; remote_url: string; class?: string }
): Promise<{ id: string; slug: string; name: string }> {
	const suffix = Math.random().toString(36).slice(2, 8);
	const res = await fetch(`${apiURL}/projects`, {
		method: 'POST',
		headers: { Authorization: 'Bearer playwright', 'Content-Type': 'application/json' },
		body: JSON.stringify({
			class: 'large',
			...body,
			name: `${body.name}-${suffix}`,
			remote_url: `${body.remote_url}-${suffix}`
		})
	});
	if (!res.ok) throw new Error(`createProject: ${res.status} ${await res.text()}`);
	return res.json();
}

export function apiURLFromEnv(): string {
	const url = process.env.PUBLIC_API_URL;
	if (!url) throw new Error('PUBLIC_API_URL not set — run tests through global-setup.ts');
	return url;
}

/** Drives the fake api's error switch (cmd/fakeapi's admin listener). */
async function adminCall(route: string, body: { method: string; path: string; code?: string }) {
	const adminURL = process.env.FAKEAPI_ADMIN_URL;
	if (!adminURL) throw new Error('FAKEAPI_ADMIN_URL not set — run tests through global-setup.ts');
	const res = await fetch(`${adminURL}${route}`, {
		method: 'POST',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify(body)
	});
	if (!res.ok) throw new Error(`${route}: ${res.status}`);
}

export const failNext = (method: string, path: string, code: string) =>
	adminCall('/fail-next', { method, path, code });
export const fail = (method: string, path: string, code: string) =>
	adminCall('/fail', { method, path, code });
export const unfail = (method: string, path: string) => adminCall('/unfail', { method, path });

/** The fake's billing knobs (internal/fakes/api.BillingState, cmd/fakeapi POST /billing). */
export interface BillingState {
	mode?: 'off' | 'none' | 'trial' | 'active' | 'past_due' | 'suspended' | 'exempt';
	plan?: 'solo' | 'plus' | 'pro';
	scheduled_plan?: 'solo' | 'plus' | 'pro' | '';
	cancelled?: boolean;
	seats?: { total: number; held: number; waiting: number };
	waitlist?: { position: number; invited?: boolean; hold_hours?: number };
	egress_gb?: number;
	/** What the projects hold in all (I-585); negative clears the override. */
	disk_held_gb?: number;
	invoices?: unknown[];
	/** The account had a subscription before: no founding price (I-497, I-672). */
	intro_used?: boolean;
	/** GET /public/seats's founding_left (I-672); negative serves null, no offer. */
	founding_left?: number;
}

function adminURL(): string {
	const url = process.env.FAKEAPI_ADMIN_URL;
	if (!url) throw new Error('FAKEAPI_ADMIN_URL not set — run tests through global-setup.ts');
	return url;
}

/** Sets the fake's billing state: mode, plan, seats, waitlist, usage, invoices. */
export async function setBilling(state: BillingState): Promise<void> {
	const res = await fetch(`${adminURL()}/billing`, {
		method: 'POST',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify(state)
	});
	if (!res.ok) throw new Error(`/billing: ${res.status} ${await res.text()}`);
}

/** The default: billing off, the account exempt, seats 18 of 30 free, nobody waiting, 30 founding places. */
export async function resetBilling(): Promise<void> {
	await setBilling({
		mode: 'off',
		seats: { total: 30, held: 12, waiting: 0 },
		waitlist: { position: 0 },
		egress_gb: 0,
		disk_held_gb: -1,
		scheduled_plan: '',
		cancelled: false,
		intro_used: false,
		founding_left: 30
	});
}

export { BASE_URL };

/** Has a project's guest ask a question, as repose-ask would (the fake's admin POST /question). */
export async function addQuestion(
	projectId: string,
	q: { agent?: string; text: string; options?: string[]; timeout_s?: number }
): Promise<{ id: string }> {
	const adminURL = process.env.FAKEAPI_ADMIN_URL;
	if (!adminURL) throw new Error('FAKEAPI_ADMIN_URL not set — run tests through global-setup.ts');
	const res = await fetch(`${adminURL}/question`, {
		method: 'POST',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify({ project_id: projectId, ...q })
	});
	if (!res.ok) throw new Error(`/question: ${res.status} ${await res.text()}`);
	return res.json();
}

/** Closes a question from the guest's side (cmd/fakeapi POST /question/close, I-690). */
export async function closeQuestion(
	id: string,
	reason: 'terminal' | 'withdrawn' | 'interrupted' | 'asker_gone' | '' = '',
	state: 'cancelled' | 'expired' = 'cancelled'
): Promise<void> {
	const adminURL = process.env.FAKEAPI_ADMIN_URL;
	if (!adminURL) throw new Error('FAKEAPI_ADMIN_URL not set — run tests through global-setup.ts');
	const res = await fetch(`${adminURL}/question/close`, {
		method: 'POST',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify({ question_id: id, state, reason })
	});
	if (!res.ok) throw new Error(`/question/close: ${res.status} ${await res.text()}`);
}

/**
 * count events on a project, spacingMinutes apart (default 1) ending now,
 * e000 oldest (cmd/fakeapi POST /events). kinds, agents, windows and
 * summaries are cycled when given; the project's creation moves to before
 * the first of them, so it stays the oldest event.
 */
export async function addEvents(
	projectId: string,
	count: number,
	opts: {
		kinds?: string[];
		agents?: string[];
		windows?: string[];
		summaries?: string[];
		spacingMinutes?: number;
	} = {}
): Promise<void> {
	const res = await fetch(`${adminURL()}/events`, {
		method: 'POST',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify({
			project_id: projectId,
			count,
			kinds: opts.kinds,
			agents: opts.agents,
			windows: opts.windows,
			summaries: opts.summaries,
			spacing_minutes: opts.spacingMinutes
		})
	});
	if (!res.ok) throw new Error(`/events: ${res.status} ${await res.text()}`);
}

/** Sets the agents a running project's signals report, and its sessions (cmd/fakeapi POST /agents). */
export async function setAgents(
	projectId: string,
	agents: { agent: string; window: string; state: string }[],
	sessions: { ssh_sessions?: number; tmux_clients?: number } = {}
): Promise<void> {
	const res = await fetch(`${adminURL()}/agents`, {
		method: 'POST',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify({ project_id: projectId, agents, ...sessions })
	});
	if (!res.ok) throw new Error(`/agents: ${res.status} ${await res.text()}`);
}

/**
 * A fake clock held still from the page's first script: installed at 0 and
 * paused at 1000 before the page loads, so nothing on the page (a loop,
 * a timer) moves until the test calls `page.clock.runFor`. Pausing after
 * the load at the page's `Date.now()` plus a margin raced a loaded machine
 * two ways: past the margin, "Cannot fast-forward to the past"; inside it,
 * a loop that had already run for an unknown time before the pause.
 */
export async function holdClock(page: Page): Promise<void> {
	await page.clock.install({ time: 0 });
	await page.clock.pauseAt(1000);
}

/** Freezes a project `minutes` ago, or clears it with a negative number
 * (cmd/fakeapi POST /resting: the mock of DECISIONS I-683). */
export async function setResting(projectID: string, minutes: number): Promise<void> {
	const res = await fetch(`${adminURL()}/resting`, {
		method: 'POST',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify({ project_id: projectID, minutes })
	});
	if (!res.ok) throw new Error(`setResting: ${res.status}`);
}

/** Stops a project straight against the fake api (no CLI in this suite). */
export async function stopProject(apiURL: string, id: string): Promise<void> {
	const res = await fetch(`${apiURL}/projects/${id}/stop`, {
		method: 'POST',
		headers: { Authorization: 'Bearer playwright', 'Content-Type': 'application/json' },
		body: JSON.stringify({ snapshot: false })
	});
	if (!res.ok) throw new Error(`stopProject: ${res.status} ${await res.text()}`);
}

/** Destroys a project, so a test that makes many leaves the shared
 *  account under its 100-project cap for the files that run after it. */
export async function destroyProject(apiURL: string, id: string): Promise<void> {
	const res = await fetch(`${apiURL}/projects/${id}`, {
		method: 'DELETE',
		headers: { Authorization: 'Bearer playwright', 'Content-Type': 'application/json' },
		body: JSON.stringify({ snapshot: false })
	});
	if (!res.ok && res.status !== 404) throw new Error(`destroyProject: ${res.status}`);
}

/**
 * Destroys every project the fake holds. The suite shares one account, and
 * the files before a project-hungry one leave theirs behind: by
 * projects-list.spec the full run reached the fake's limit of 100 and
 * createProject answered `project_limit` (ui-revamp-ia integration).
 */
export async function pruneProjects(apiURL: string): Promise<void> {
	const res = await fetch(`${apiURL}/projects`, {
		headers: { Authorization: 'Bearer playwright' }
	});
	if (!res.ok) throw new Error(`pruneProjects: ${res.status}`);
	const list = (await res.json()) as { id: string }[];
	// One mid-step may refuse; the rest still free the room.
	for (const p of list) await destroyProject(apiURL, p.id).catch(() => {});
}

/**
 * A locator's box once it has stopped moving: two reads a frame apart that
 * agree. `scrollTo` is fire-and-forget — nothing in it waits for the scroll
 * to land, and sticky layout resolves against the new position a frame
 * later — so a `boundingBox()` read straight after a scroll can measure an
 * element mid-flight. No user asserts on a position mid-scroll, so waiting
 * for the steady state hides nothing; it is the state the tests that use
 * this are named for (I-841's rail, the hero's agent row).
 */
export async function settledBox(
	page: Page,
	locator: Locator
): Promise<{ x: number; y: number; width: number; height: number }> {
	let prev = (await locator.boundingBox())!;
	for (let i = 0; i < 20; i++) {
		await page.waitForTimeout(50);
		const box = (await locator.boundingBox())!;
		if (box.y === prev.y && box.height === prev.height) return box;
		prev = box;
	}
	return prev;
}
