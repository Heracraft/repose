import type { Page } from '@playwright/test';
import { BASE_URL } from './fixtures';

/** Drives the whole fake-Logto authorization-code round trip. */
export async function signIn(page: Page): Promise<void> {
	await page.goto('/');
	await page.getByRole('button', { name: 'Get started' }).click();
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
	/** The account had a subscription before: no introductory price (I-497). */
	intro_used?: boolean;
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

/** The default: billing off, the account exempt, seats 18 of 30 free, nobody waiting. */
export async function resetBilling(): Promise<void> {
	await setBilling({
		mode: 'off',
		seats: { total: 30, held: 12, waiting: 0 },
		waitlist: { position: 0 },
		egress_gb: 0,
		disk_held_gb: -1,
		scheduled_plan: '',
		cancelled: false,
		intro_used: false
	});
}

/**
 * Stands in for Paddle's overlay (src/lib/paddle.ts): with the fake's
 * environment "fake" the page calls window.__reposePaddleStub instead of
 * loading Paddle.js, and the stub plays the webhook by completing the
 * transaction through the fake's admin listener, then reports
 * checkout.completed the way the overlay would.
 */
export async function installPaddleStub(page: Page): Promise<void> {
	await page.addInitScript((admin: string) => {
		window.__reposePaddleStub = {
			async open({ transactionId, onCompleted }) {
				window.__reposePaddleOpened = transactionId;
				await fetch(`${admin}/paddle/complete`, {
					method: 'POST',
					headers: { 'Content-Type': 'application/json' },
					body: JSON.stringify({ transaction_id: transactionId })
				});
				onCompleted();
			}
		};
	}, adminURL());
}

declare global {
	interface Window {
		__reposePaddleOpened?: string;
	}
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

/** count events on a project, one minute apart, e000 oldest (cmd/fakeapi POST /events). */
export async function addEvents(projectId: string, count: number): Promise<void> {
	const adminURL = process.env.FAKEAPI_ADMIN_URL;
	if (!adminURL) throw new Error('FAKEAPI_ADMIN_URL not set — run tests through global-setup.ts');
	const res = await fetch(`${adminURL}/events`, {
		method: 'POST',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify({ project_id: projectId, count })
	});
	if (!res.ok) throw new Error(`/events: ${res.status} ${await res.text()}`);
}
