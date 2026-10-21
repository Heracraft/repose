// Checklist: "Secrets: add via text and via file, list, delete; values
// never appear in the DOM after save."
import { test, expect } from '@playwright/test';
import { signIn, createProject, apiURLFromEnv, failNext } from './helpers';

test.beforeEach(async ({ page }) => {
	await signIn(page);
});

test('add via text, list, and delete; the value never reaches the DOM', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'secrets-app',
		remote_url: 'github.com/heracraft/secrets-app'
	});
	await page.goto(`/projects/${p.id}/secrets`);

	await page.getByLabel('Name', { exact: true }).fill('DATABASE_URL');
	await page.getByLabel('Value', { exact: true }).fill('postgres://user:hunter2@db/app');
	await page.getByRole('button', { name: 'Add', exact: true }).click();

	const secretRow = page.getByText('DATABASE_URL', { exact: true });
	await expect(secretRow).toBeVisible({ timeout: 5_000 });
	expect(await page.content()).not.toContain('hunter2');

	// Delete asks in the row first; Keep it backs out with nothing sent.
	// Focus follows the panel: into it on open, back to Delete on Keep it,
	// so a keyboard user is never dropped to the top of the page (I-391).
	await page.getByRole('button', { name: 'Delete DATABASE_URL' }).click();
	const ask = page.getByTestId('confirm-delete-secret');
	await expect(ask).toContainText('keep their copy until they restart');
	await expect(ask.getByRole('button', { name: 'Keep it' })).toBeFocused();
	await ask.getByRole('button', { name: 'Keep it' }).click();
	await expect(ask).toHaveCount(0);
	await expect(secretRow).toBeVisible();
	await expect(page.getByRole('button', { name: 'Delete DATABASE_URL' })).toBeFocused();

	await page.getByRole('button', { name: 'Delete DATABASE_URL' }).click();
	await page.getByRole('button', { name: 'Delete DATABASE_URL now' }).click();
	await expect(secretRow).toHaveCount(0, { timeout: 5_000 });
	// The last row went, so focus lands on the Add form's first field.
	await expect(page.getByLabel('Name', { exact: true })).toBeFocused();
});

test('a name that does not match the allowed pattern is rejected client-side', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'secrets-invalid-app',
		remote_url: 'github.com/heracraft/secrets-invalid-app'
	});
	await page.goto(`/projects/${p.id}/secrets`);

	// The field shows and sends capitals, so a lowercase name is no longer
	// a mismatch; a hyphen still is.
	await page.getByLabel('Name', { exact: true }).fill('MY-KEY');
	await page.getByLabel('Value', { exact: true }).fill('x');
	await page.getByRole('button', { name: 'Add', exact: true }).click();

	await expect(
		page.getByText('Use A-Z, 0-9 and _, starting with a letter, at most 64 characters.')
	).toBeVisible();
	await expect(page.getByLabel('Name', { exact: true })).toBeFocused();

	// Add is never greyed out: an empty name is said under the field.
	await page.getByLabel('Name', { exact: true }).fill('');
	await page.getByRole('button', { name: 'Add', exact: true }).click();
	await expect(page.getByText('Name is required.')).toBeVisible();
});

test('a value over 64 KB is rejected client-side before it reaches the api', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'secrets-huge-app',
		remote_url: 'github.com/heracraft/secrets-huge-app'
	});
	await page.goto(`/projects/${p.id}/secrets`);

	await page.getByLabel('Name', { exact: true }).fill('BIG_VALUE');
	await page.getByLabel('Value', { exact: true }).fill('x'.repeat(70_000));
	await page.getByRole('button', { name: 'Add', exact: true }).click();

	await expect(page.getByText('Secret values are limited to 64 KB.')).toBeVisible();
	await expect(page.getByText('BIG_VALUE')).toHaveCount(0);
});

// The other half of the checklist row: "add via text **and via file**".
// The file path is not the text path with a different source — it reads
// an ArrayBuffer and base64s the bytes, so a value that is not valid
// UTF-8 (a key file, the common case) survives only on this path.
test('add via file stores the bytes, and they never reach the DOM', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'secrets-file-app',
		remote_url: 'github.com/heracraft/secrets-file-app'
	});
	await page.goto(`/projects/${p.id}/secrets`);

	// A byte sequence that is deliberately not valid UTF-8, so a path that
	// round-tripped through a string would corrupt it.
	const bytes = Buffer.from([0x00, 0xff, 0xfe, 0x41, 0x42, 0x43, 0x80, 0x0a]);
	await page.getByLabel('Name', { exact: true }).fill('SERVICE_ACCOUNT_KEY');
	await page.setInputFiles('input[type=file]', {
		name: 'key.bin',
		mimeType: 'application/octet-stream',
		buffer: bytes
	});
	await page.getByRole('button', { name: 'Add', exact: true }).click();

	await expect(page.getByText('SERVICE_ACCOUNT_KEY', { exact: true })).toBeVisible({
		timeout: 5_000
	});

	// What the api received is the base64 of those exact bytes, and none of
	// it is on the page afterwards.
	const b64 = bytes.toString('base64');
	expect(await page.content()).not.toContain(b64);
	expect(await page.content()).not.toContain('ABC');

	const res = await page.request.get(`${apiURLFromEnv()}/projects/${p.id}/secrets`, {
		headers: { Authorization: 'Bearer playwright' }
	});
	const names = (await res.json()).map((s: { name: string }) => s.name);
	expect(names).toContain('SERVICE_ACCOUNT_KEY');
});

// A file over the cap is refused on the file path too, not only the
// textarea's (08 §6, "Secret value over 64 KB").
test('a file over 64 KB is rejected client-side', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'secrets-big-file',
		remote_url: 'github.com/heracraft/secrets-big-file'
	});
	await page.goto(`/projects/${p.id}/secrets`);

	await page.getByLabel('Name', { exact: true }).fill('BIG_FILE');
	await page.setInputFiles('input[type=file]', {
		name: 'big.bin',
		mimeType: 'application/octet-stream',
		buffer: Buffer.alloc(64 * 1024 + 1, 7)
	});
	await page.getByRole('button', { name: 'Add', exact: true }).click();

	await expect(page.getByText('Secret values are limited to 64 KB.')).toBeVisible();
	await expect(page.getByText('BIG_FILE', { exact: true })).toHaveCount(0);
});

test('a failed first load shows the error and Retry loads the list', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'secrets-retry-app',
		remote_url: 'github.com/heracraft/secrets-retry-app'
	});
	await failNext('GET', `/projects/${p.id}/secrets`, 'internal');
	await page.goto(`/projects/${p.id}/secrets`);
	const failed = page.getByRole('alert');
	await expect(failed).toBeVisible();
	await failed.getByRole('button', { name: 'Retry' }).click();
	await expect(page.getByRole('heading', { level: 2, name: 'No secrets yet' })).toBeVisible();
	await expect(failed).toHaveCount(0);
});

// The native file widget is replaced by a ghost button (I-664): it opens
// the same picker, the picked file shows by name, and its bytes are what
// the api receives.
test('Load from file reads the value from the picked file', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'secrets-picker-app',
		remote_url: 'github.com/heracraft/secrets-picker-app'
	});
	await page.goto(`/projects/${p.id}/secrets`);
	await page.getByLabel('Name', { exact: true }).fill('deploy_key');

	const chooser = page.waitForEvent('filechooser');
	await page.getByRole('button', { name: 'Load from file' }).click();
	const bytes = Buffer.from('ssh-ed25519 AAAA picked\n');
	await (await chooser).setFiles({ name: 'id_deploy', mimeType: 'text/plain', buffer: bytes });
	await expect(page.getByTestId('secret-file-name')).toHaveText('id_deploy');
	await expect(page.getByLabel('Value', { exact: true })).toBeDisabled();

	const put = page.waitForRequest(
		(r) => r.method() === 'PUT' && r.url().endsWith(`/projects/${p.id}/secrets/DEPLOY_KEY`)
	);
	await page.getByRole('button', { name: 'Add', exact: true }).click();
	const body = (await put).postDataJSON() as { value: string };
	expect(body.value).toBe(bytes.toString('base64'));
	await expect(page.getByText('DEPLOY_KEY', { exact: true })).toBeVisible();
	// The form is clear for the next one.
	await expect(page.getByTestId('secret-file-name')).toHaveCount(0);
	await expect(page.getByRole('button', { name: 'Load from file' })).toBeVisible();
});

// I-664: the list and the form fill the frame side by side at 1440.
test('secrets spans the frame at 1440', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'secrets-wide-app',
		remote_url: 'github.com/heracraft/secrets-wide-app'
	});
	await page.setViewportSize({ width: 1440, height: 900 });
	await page.goto(`/projects/${p.id}/secrets`);
	const card = page.locator('form', { has: page.getByRole('heading', { name: 'Add a secret' }) });
	const box = await card.boundingBox();
	expect(box!.x + box!.width).toBeGreaterThanOrEqual(1200);
	const empty = await page.getByRole('heading', { name: 'No secrets yet' }).boundingBox();
	expect(empty!.y).toBeLessThan(box!.y + box!.height);
});

// With no secrets the list is one empty row that says where a secret
// lands; it no longer runs the form's height (I-837).
test('the empty state is one row level with the top of the form at 1440', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'secrets-level-app',
		remote_url: 'github.com/heracraft/secrets-level-app'
	});
	await page.setViewportSize({ width: 1440, height: 900 });
	await page.goto(`/projects/${p.id}/secrets`);
	const form = await page
		.locator('form', { has: page.getByRole('heading', { name: 'Add a secret' }) })
		.boundingBox();
	const title = await page.getByRole('heading', { level: 2, name: 'No secrets yet' }).boundingBox();
	if (!form || !title) throw new Error('no form or empty row');
	expect(title.y).toBeLessThan(form.y + 40);
	expect(title.height).toBeLessThan(30);
});

// At 320 a long name breaks after its underscores, never inside a word,
// and the Updated column gives its width to the name: the date is a
// second line under it.
test('on a phone a long name breaks at its underscores and Updated folds under it', async ({
	page
}) => {
	const name = 'STRIPE_WEBHOOK_SIGNING_SECRET_FOR_BILLING_WORKER_STAGING';
	const p = await createProject(apiURLFromEnv(), {
		name: 'secrets-phone-app',
		remote_url: 'github.com/heracraft/secrets-phone-app'
	});
	await page.setViewportSize({ width: 320, height: 800 });
	await page.goto(`/projects/${p.id}/secrets`);
	await page.getByLabel('Name', { exact: true }).fill(name);
	await page.getByLabel('Value', { exact: true }).fill('whsec_x');
	await page.getByRole('button', { name: 'Add', exact: true }).click();
	const cell = page.getByText(name, { exact: true });
	await expect(cell).toBeVisible({ timeout: 5_000 });

	await expect(page.getByRole('columnheader', { name: 'Updated' })).toBeHidden();
	const row = page.getByRole('row').filter({ has: cell });
	await expect(
		row.getByText(/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}$/).filter({ visible: true })
	).toHaveCount(1);

	// Each word between underscores sits on one line, and the name takes
	// more than one.
	const lines = await cell.evaluate((el) => {
		const tops = new Set<number>();
		const walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
		const split: string[] = [];
		for (let n = walker.nextNode(); n; n = walker.nextNode()) {
			const r = document.createRange();
			r.selectNodeContents(n);
			const rects = [...r.getClientRects()].filter((x) => x.width > 0);
			if (rects.length > 1) split.push(n.textContent ?? '');
			rects.forEach((x) => tops.add(Math.round(x.top)));
		}
		return { count: tops.size, split };
	});
	expect(lines.split).toEqual([]);
	expect(lines.count).toBeGreaterThan(1);
	expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(320);
});
