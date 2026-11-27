// API tokens (DECISIONS I-1314..I-1322): Settings → API tokens lists them,
// makes one in a dialog that shows the token once, and revokes with the
// two-step the Secrets tab uses. The token reaches the DOM once, in the
// dialog, and never in the list.
import { test, expect, type Page } from '@playwright/test';
import { signIn, createProject, apiURLFromEnv, failNext, cleanSlate } from './helpers';

cleanSlate();

async function shots(page: Page, name: string): Promise<void> {
	const dir = process.env.REPOSE_SHOTS_DIR;
	if (!dir) return;
	for (const width of [1440, 390]) {
		for (const scheme of ['light', 'dark'] as const) {
			await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
			await page.emulateMedia({ colorScheme: scheme });
			await page.screenshot({ path: `${dir}/${name}-${width}-${scheme}.png` });
		}
	}
	await page.setViewportSize({ width: 1280, height: 720 });
	await page.emulateMedia({ colorScheme: null });
}

test.beforeEach(async ({ page }) => {
	await signIn(page);
});

const created: string[] = [];
test.afterAll(async () => {
	for (const id of created) {
		await fetch(`${apiURLFromEnv()}/projects/${id}`, {
			method: 'DELETE',
			headers: { Authorization: 'Bearer playwright' }
		});
	}
});

test('Settings makes, lists and revokes an API token', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'tokens-app',
		remote_url: 'github.com/heracraft/tokens-app'
	});
	created.push(p.id);

	await page.goto('/settings/tokens');
	const section = page.locator('#tokens');
	await expect(section.getByRole('heading', { name: /^(API tokens|\d+ tokens?)$/ })).toBeVisible();
	const dialog = page.getByTestId('token-dialog');
	await expect(dialog.getByLabel('Name', { exact: true })).toHaveCount(0);

	// The form: a name, the six scopes with read on, the project, the end.
	await section.getByRole('button', { name: 'Create token' }).click();
	await expect(dialog.getByLabel('Name', { exact: true })).toBeFocused();
	await expect(dialog.getByRole('checkbox', { name: /^read / })).toBeChecked();
	await expect(dialog.getByRole('checkbox')).toHaveCount(6);
	// Each box is centred on its row's first line of text, a wrapping row
	// included (it hung 4px above it under baseline alignment, 2026-11-18).
	for (const width of [1440, 390]) {
		await page.setViewportSize({ width, height: 1000 });
		const off = await dialog.locator('label.scope').evaluateAll((els) =>
			els.map((el) => {
				const box = el.querySelector('input')!.getBoundingClientRect();
				const range = document.createRange();
				range.selectNodeContents(el.querySelector('.scope-name')!);
				const line = range.getClientRects()[0];
				return box.top + box.height / 2 - (line.top + line.height / 2);
			})
		);
		for (const d of off) expect(Math.abs(d), `at ${width}`).toBeLessThan(1);
	}
	await page.setViewportSize({ width: 1280, height: 720 });
	// An empty name is refused in place.
	await dialog.getByRole('button', { name: 'Create' }).click();
	await expect(dialog.getByText('1 to 32 of a-z')).toBeVisible();
	await dialog.getByLabel('Name', { exact: true }).fill('ci');
	await dialog.getByRole('checkbox', { name: /^triggers / }).check();
	await dialog.getByLabel('Project', { exact: true }).selectOption({ label: p.slug });
	await dialog.getByLabel('Expires in', { exact: true }).selectOption({ label: '90 days' });
	await dialog.getByRole('button', { name: 'Create' }).click();

	// The one view of the token.
	const value = dialog.getByTestId('token-value').locator('pre.line');
	await expect(value).toBeVisible();
	const token = (await value.textContent()) ?? '';
	expect(token).toMatch(/^rpt_[A-Za-z0-9_-]{43}$/);
	await expect(dialog.getByText('Copy it now. It is not shown again.')).toBeVisible();
	await expect(dialog.getByText(`read, triggers · ${p.slug} · until`)).toBeVisible();
	await expect(dialog.getByRole('button', { name: 'Done' })).toBeFocused();
	await dialog.getByRole('button', { name: 'Done' }).click();
	await expect(dialog).not.toBeVisible();
	await expect(section.getByRole('button', { name: 'Create token' })).toBeFocused();

	// The list: name, prefix, scopes, project, end, last use; never the token.
	const table = page.getByTestId('api-tokens');
	await expect(section.getByRole('heading', { name: '1 token' })).toBeVisible();
	const row = table.getByRole('row', { name: /ci/ });
	await expect(row).toContainText(token.slice(0, 12) + '…');
	await expect(row).toContainText('read, triggers');
	await expect(row).toContainText(p.slug);
	await expect(row).toContainText('Never');
	expect(await page.content()).not.toContain(token);

	// A second with the same name is refused under the field.
	await section.getByRole('button', { name: 'Create token' }).click();
	await dialog.getByLabel('Name', { exact: true }).fill('ci');
	await dialog.getByRole('button', { name: 'Create' }).click();
	await expect(dialog.getByText('You already have a token named ci.')).toBeVisible();
	await page.keyboard.press('Escape');
	await expect(dialog).not.toBeVisible();

	// The token works against the api, and its use shows.
	const me = await fetch(`${apiURLFromEnv()}/me`, {
		headers: { Authorization: `Bearer ${token}` }
	});
	expect(me.status).toBe(200);
	expect(((await me.json()) as { token: { name: string } }).token.name).toBe('ci');
	await page.reload();
	await expect(table.getByRole('row', { name: /ci/ })).not.toContainText('Never');

	// Revoke: two steps, Keep it goes back, Revoke ends it, the row says so.
	await table.getByRole('button', { name: 'Revoke ci…' }).click();
	const confirm = page.getByTestId('confirm-revoke-token');
	await expect(confirm.getByRole('button', { name: 'Keep it' })).toBeFocused();
	await confirm.getByRole('button', { name: 'Keep it' }).click();
	await expect(confirm).toHaveCount(0);
	await expect(table.getByRole('button', { name: 'Revoke ci…' })).toBeFocused();
	await table.getByRole('button', { name: 'Revoke ci…' }).click();
	await confirm.getByRole('button', { name: 'Revoke ci now' }).click();
	await expect(confirm).toHaveCount(0);
	await expect(table.getByRole('row', { name: /ci/ })).toContainText('Revoked');
	await expect(table.getByRole('button', { name: 'Revoke ci…' })).toHaveCount(0);
	await expect(section.getByRole('heading', { name: 'API tokens' })).toBeVisible();
	const after = await fetch(`${apiURLFromEnv()}/me`, {
		headers: { Authorization: `Bearer ${token}` }
	});
	expect(after.status).toBe(401);
});

// The viewport at 1440 and 390 in both schemes, at 1x, for the review
// (CLAUDE.md "Judge visuals at real size").
test('API tokens shots', async ({ page }, testInfo) => {
	// One live token, so the list has a row.
	const res = await fetch(`${apiURLFromEnv()}/me/tokens`, {
		method: 'POST',
		headers: { Authorization: 'Bearer playwright', 'Content-Type': 'application/json' },
		body: JSON.stringify({ name: 'nightly', scopes: ['read', 'triggers'], expires_in_days: 90 })
	});
	expect(res.status).toBe(201);
	await page.goto('/settings/tokens');
	const shot = (name: string) =>
		page.screenshot({ path: testInfo.outputPath(`${name}.png`), animations: 'disabled' });
	for (const width of [1440, 390]) {
		for (const scheme of ['light', 'dark'] as const) {
			await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
			await page.emulateMedia({ colorScheme: scheme });
			await expect(page.getByTestId('api-tokens')).toBeVisible();
			await shot(`tokens-${width}-${scheme}`);
			await page.locator('#tokens').getByRole('button', { name: 'Create token' }).click();
			const dialog = page.getByTestId('token-dialog');
			await expect(dialog.getByLabel('Name', { exact: true })).toBeVisible();
			await shot(`tokens-dialog-${width}-${scheme}`);
			await dialog.getByLabel('Name', { exact: true }).fill(`shot-${width}-${scheme}`);
			await dialog.getByRole('button', { name: 'Create' }).click();
			await expect(dialog.getByTestId('token-value')).toBeVisible();
			await shot(`tokens-made-${width}-${scheme}`);
			await dialog.getByRole('button', { name: 'Done' }).click();
		}
	}
	await page.emulateMedia({ colorScheme: null });
});

// Five tabs and the API page fit a phone without sideways scroll
// (I-1322, I-1324; the design spec's rule for every docs page).
for (const width of [390, 360]) {
	test(`the Settings tabs and /docs/api fit at ${width}`, async ({ page }) => {
		await page.setViewportSize({ width, height: 800 });
		await page.goto('/settings/tokens');
		await expect(page.locator('#tokens')).toBeVisible();
		expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(
			width
		);
		const tabs = page.getByRole('navigation', { name: 'Settings' });
		await expect(tabs.getByRole('link', { name: 'API tokens' })).toBeInViewport({ ratio: 1 });
		await page.goto('/docs/api');
		await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
		expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(
			width
		);
	});
}

// A refusal raised while the Create token modal is open (both the 20-token
// limit and a server error) renders INSIDE the dialog on top of the modal,
// never as a toast hidden behind the top-layer backdrop (ENG-136, I-1450).
test('Create token shows API refusals inside the modal dialog, never behind it', async ({
	page
}) => {
	const auth = { Authorization: 'Bearer playwright', 'Content-Type': 'application/json' };
	// Clean any existing live tokens, then create 20 to hit the account limit.
	const existing = (await (
		await fetch(`${apiURLFromEnv()}/me/tokens`, { headers: auth })
	).json()) as {
		name: string;
		revoked_at: string | null;
	}[];
	for (const t of existing) {
		if (!t.revoked_at) {
			await fetch(`${apiURLFromEnv()}/me/tokens/${t.name}`, { method: 'DELETE', headers: auth });
		}
	}

	await page.goto('/settings/tokens');
	const section = page.locator('#tokens');
	const dialog = page.getByTestId('token-dialog');

	// First: a 500 internal error renders in-dialog, above the backdrop.
	await failNext('POST', '/v1/me/tokens', 'internal');
	await section.getByRole('button', { name: 'Create token' }).click();
	await dialog.getByLabel('Name', { exact: true }).fill('err-check');
	await dialog.getByRole('button', { name: 'Create' }).click();
	const refusal = dialog.getByTestId('token-create-error');
	await expect(refusal).toContainText('Could not make the token.');
	const errOnTop = await refusal.evaluate((el) => {
		const b = el.getBoundingClientRect();
		const hit = document.elementFromPoint(b.left + b.width / 2, b.top + b.height / 2);
		return el.contains(hit) || hit === el;
	});
	expect(errOnTop, 'internal refusal is covered by the modal backdrop').toBe(true);
	await dialog.getByRole('button', { name: 'Cancel' }).click();

	// Second: fill the 20-token limit and verify the limit sentence is shown
	// inside the dialog on top of the backdrop.
	const madeNames: string[] = [];
	try {
		for (let i = 1; i <= 20; i++) {
			const name = `cap-${i}`;
			const res = await fetch(`${apiURLFromEnv()}/me/tokens`, {
				method: 'POST',
				headers: auth,
				body: JSON.stringify({ name, scopes: ['read'], expires_in_days: 7 })
			});
			expect(res.status).toBe(201);
			madeNames.push(name);
		}
		await page.reload();
		await section.getByRole('button', { name: 'Create token' }).click();
		await dialog.getByLabel('Name', { exact: true }).fill('cap-21');
		await dialog.getByRole('button', { name: 'Create' }).click();
		await expect(refusal).toContainText('20 tokens at most');
		const limitOnTop = await refusal.evaluate((el) => {
			const b = el.getBoundingClientRect();
			const hit = document.elementFromPoint(b.left + b.width / 2, b.top + b.height / 2);
			return el.contains(hit) || hit === el;
		});
		expect(limitOnTop, 'token_limit refusal is covered by the modal backdrop').toBe(true);
		await shots(page, 'tokens-refused');
		await dialog.getByRole('button', { name: 'Cancel' }).click();
	} finally {
		for (const name of madeNames) {
			await fetch(`${apiURLFromEnv()}/me/tokens/${name}`, { method: 'DELETE', headers: auth });
		}
	}
});
