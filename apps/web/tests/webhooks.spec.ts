// A webhook route that forwards to the app, on a project's Triggers tab
// (DECISIONS I-1110, I-1360): the form's "Forward to your app", the row
// beside the triggers, Rotate and Remove asking in the row, and Edit.
// I-1110 put these under Webhooks on Config; I-1360 moved them here,
// because a route and a webhook trigger are one door with two receivers.
import { test, expect, type Page } from '@playwright/test';
import { signIn, createProject, apiURLFromEnv, cleanSlate } from './helpers';

cleanSlate();

/** The viewport at 1440 and 390 in both schemes, as triggers.spec.ts shoots. */
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

test('Config has no Webhooks section; the Triggers tab makes a forwarding route', async ({
	page
}) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'hooks-app',
		remote_url: 'github.com/heracraft/hooks-app'
	});
	await page.goto(`/projects/${p.id}/config`);
	await expect(page.getByRole('heading', { name: 'Webhooks' })).toHaveCount(0);
	await expect(page.getByRole('button', { name: 'Add route' })).toHaveCount(0);

	await page.goto(`/projects/${p.id}/triggers`);
	const section = page.locator('#triggers');
	await section.getByRole('button', { name: 'Add trigger' }).click();
	const form = section.getByTestId('trigger-form');
	await form.getByLabel('Name').fill('stripe');
	await form.getByRole('button', { name: 'Forward to your app' }).click();
	// The agent's half of the form goes away: no prompt, no settings.
	await expect(form.getByText('Prompt')).toHaveCount(0);
	await expect(form.getByRole('button', { name: 'More settings' })).toHaveCount(0);
	await form.getByLabel('Port and path in the machine').fill('3000/api/webhooks/stripe');
	await shots(page, 'triggers-forward-form');
	await form.getByRole('button', { name: 'Add trigger' }).click();

	await expect(section.getByRole('heading', { name: '1 trigger' })).toBeVisible();
	await expect(section.getByRole('cell', { name: 'webhook', exact: true })).toBeVisible();
	await expect(
		section.getByRole('cell', { name: 'your app :3000/api/webhooks/stripe', exact: true })
	).toBeVisible();
	// The row opened on its URL, which is never printed; Copy puts it on
	// the clipboard.
	await expect(
		section.getByRole('button', { name: 'Copy the URL of stripe' }).first()
	).toBeVisible();
	await expect(section.getByText('Forwards to :3000/api/webhooks/stripe')).toBeVisible();
	await expect(page.locator('body')).not.toContainText('hooks.repose');
	// The api holds a route with no trigger, as `repose webhooks add` makes.
	const routes = await fetch(`${apiURLFromEnv()}/projects/${p.id}/webhooks`, {
		headers: { Authorization: 'Bearer playwright' }
	}).then((r) => r.json());
	expect(routes).toHaveLength(1);
	expect(routes[0].port).toBe(3000);
	expect(routes[0].trigger).toBeUndefined();
});

test('a bad name or target is refused in the form, before anything is sent', async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'hooks-bad-app',
		remote_url: 'github.com/heracraft/hooks-bad-app'
	});
	await page.goto(`/projects/${p.id}/triggers`);
	const section = page.locator('#triggers');
	await section.getByRole('button', { name: 'Add trigger' }).click();
	const form = section.getByTestId('trigger-form');
	await form.getByRole('button', { name: 'Forward to your app' }).click();
	await form.getByLabel('Name').fill('Stripe');
	await form.getByLabel('Port and path in the machine').fill('3000');
	await form.getByRole('button', { name: 'Add trigger' }).click();
	await expect(form.getByTestId('trigger-form-error')).toContainText('A name is 1 to 32 of a-z');

	await form.getByLabel('Name').fill('ok');
	await form.getByLabel('Port and path in the machine').fill('nope');
	await form.getByRole('button', { name: 'Add trigger' }).click();
	await expect(form.getByTestId('trigger-form-error')).toContainText(
		'A target is a port, or a port and a path'
	);
});

test('Edit, Rotate and Remove work on a forwarding route in its row', async ({ page }) => {
	const api = apiURLFromEnv();
	const p = await createProject(api, {
		name: 'hooks-rm-app',
		remote_url: 'github.com/heracraft/hooks-rm-app'
	});
	await fetch(`${api}/projects/${p.id}/webhooks`, {
		method: 'POST',
		headers: { Authorization: 'Bearer playwright', 'Content-Type': 'application/json' },
		body: JSON.stringify({ name: 'github', target: '3000', allow_start: false })
	});
	await page.goto(`/projects/${p.id}/triggers`);
	const section = page.locator('#triggers');
	await expect(section.getByRole('heading', { name: '1 trigger' })).toBeVisible();
	// A forwarding route has no Run and no Off: a provider delivers or
	// it does not. Its four are Copy, Edit, Rotate and Remove.
	await expect(section.getByRole('button', { name: 'Run github now' })).toHaveCount(0);
	await expect(section.getByRole('button', { name: 'Turn github off' })).toHaveCount(0);

	// Edit opens on the target and changes it; the token is kept, so the
	// URL in a provider's dashboard goes on working.
	await section.getByRole('button', { name: 'Change github' }).click();
	const form = section.getByTestId('trigger-form');
	await expect(form.getByLabel('Name')).toBeDisabled();
	await expect(form.getByLabel('Port and path in the machine')).toHaveValue('3000');
	await form.getByLabel('Port and path in the machine').fill('3000/api/webhooks/github');
	await form.getByRole('button', { name: 'Save', exact: true }).click();
	await expect(
		section.getByRole('cell', { name: 'your app :3000/api/webhooks/github', exact: true })
	).toBeVisible();

	await section.getByRole('button', { name: 'Rotate the token of github' }).click();
	await expect(page.getByTestId('confirm-rotate-webhook')).toContainText('answers 410 for 30 days');
	await page.getByRole('button', { name: 'Keep' }).click();
	await expect(page.getByTestId('confirm-rotate-webhook')).toHaveCount(0);

	await section.getByRole('button', { name: 'Remove github' }).click();
	await expect(page.getByTestId('confirm-remove-webhook')).toContainText('then 404');
	await page.getByRole('button', { name: 'Remove', exact: true }).click();
	await expect(section.getByText('No triggers yet')).toBeVisible();
});

test('a trigger and a forwarding route share the list and the count', async ({ page }) => {
	const api = apiURLFromEnv();
	const p = await createProject(api, {
		name: 'hooks-mixed-app',
		remote_url: 'github.com/heracraft/hooks-mixed-app'
	});
	const headers = { Authorization: 'Bearer playwright', 'Content-Type': 'application/json' };
	await fetch(`${api}/projects/${p.id}/triggers`, {
		method: 'POST',
		headers,
		body: JSON.stringify({ name: 'apply', kind: 'route', route: 'apply' })
	});
	await fetch(`${api}/projects/${p.id}/webhooks`, {
		method: 'POST',
		headers,
		body: JSON.stringify({ name: 'stripe', target: '3000/api/webhooks/stripe' })
	});
	await page.goto(`/projects/${p.id}/triggers`);
	const section = page.locator('#triggers');
	await expect(section.getByRole('heading', { name: '2 triggers' })).toBeVisible();
	// The route a trigger owns is the trigger's row, not a second one.
	await expect(section.getByRole('button', { name: 'apply', exact: true })).toHaveCount(1);
	await expect(section.getByRole('button', { name: 'stripe', exact: true })).toHaveCount(1);
	await expect(section.getByRole('cell', { name: /^an agent/ })).toHaveCount(1);
	await expect(section.getByRole('cell', { name: /^your app/ })).toHaveCount(1);
	await shots(page, 'triggers-mixed');
});

// ENG-56: a route's row opens on its last 20 deliveries, as `repose
// webhooks url --json` lists them, not the one the row shows.
test('a forwarding route opens on its last deliveries', async ({ page }) => {
	const api = apiURLFromEnv();
	const p = await createProject(api, {
		name: 'hooks-deliveries',
		remote_url: 'github.com/heracraft/hooks-deliveries'
	});
	await fetch(`${api}/projects/${p.id}/webhooks`, {
		method: 'POST',
		headers: { Authorization: 'Bearer playwright', 'Content-Type': 'application/json' },
		body: JSON.stringify({ name: 'stripe', target: '4242', allow_start: false })
	});
	const admin = process.env.FAKEAPI_ADMIN_URL!;
	await fetch(`${admin}/webhook-deliveries`, {
		method: 'POST',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify({
			project_id: p.id,
			name: 'stripe',
			deliveries: Array.from({ length: 22 }, (_, i) => ({
				status: i === 21 ? 502 : 200,
				outcome: i === 21 ? 'upstream_error' : 'delivered',
				minutes_ago: 60 - i
			}))
		})
	});
	await page.goto(`/projects/${p.id}/triggers`);
	const section = page.locator('#triggers');
	await section.getByRole('button', { name: 'stripe', exact: true }).click();
	const list = section.getByTestId('deliveries');
	await expect(list.locator('li')).toHaveCount(20);
	await expect(list.locator('li').first()).toContainText('502');
	await expect(list.locator('li').first()).toContainText('upstream_error');
});
