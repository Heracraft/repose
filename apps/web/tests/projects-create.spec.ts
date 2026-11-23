// New project on /projects: the dashboard makes a machine, which is the
// first of I-1266's gaps in the owner's order (DECISIONS I-1354). A
// project made here is an empty machine; Repository is the remote the
// laptop's `repose run` looks the project up by, in the CLI's normalized
// form, and never a clone.
import { test, expect, type Page } from '@playwright/test';
import {
	signIn,
	apiURLFromEnv,
	destroyProject,
	ownedHost,
	pruneProjects,
	resetBilling,
	setBilling
} from './helpers';

/**
 * The viewport at 1440 and 390 in both schemes, at 1x, for the review
 * CLAUDE.md asks for ("Judge visuals at real size"). Written only with
 * REPOSE_SHOTS_DIR set, as own-hardware.spec.ts does.
 */
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

const litter: string[] = [];

// The account is shared with every other file: start from no project, so
// a list left long by an earlier one cannot push a row of this file's own
// past the 25 the table draws.
test.beforeAll(async () => {
	await pruneProjects(apiURLFromEnv());
});

test.beforeEach(async ({ page }) => {
	await resetBilling();
	await signIn(page);
});

test.afterAll(async () => {
	const api = apiURLFromEnv();
	while (litter.length) await destroyProject(api, litter.pop()!);
	await resetBilling();
});

/** A name no other run of this file has used (the suite shares one account). */
const unique = (stem: string) => `${stem}-${Math.random().toString(36).slice(2, 8)}`;

/**
 * The create form's own locator. Every field is read through it: on a
 * page whose list is not empty, a bare getByLabel('Size') also matches a
 * row's "Stop resize-app" button, because getByLabel matches a substring.
 */
const form = (page: Page) => page.getByTestId('new-project-form');

async function openForm(page: Page) {
	await page.goto('/projects');
	await page.getByRole('button', { name: 'New project' }).click();
	await expect(form(page)).toBeVisible();
}

/** The project the fake api holds under this name, read straight from it. */
async function fromApi(name: string) {
	const res = await fetch(`${apiURLFromEnv()}/projects`, {
		headers: { Authorization: 'Bearer playwright' }
	});
	expect(res.ok).toBe(true);
	const list = (await res.json()) as {
		id: string;
		name: string;
		slug: string;
		class: string;
		remote_url: string;
		expires_at?: string;
		placement?: string;
	}[];
	return list.find((p) => p.name === name);
}

test('New project makes a machine, which opens on its page and is in the list', async ({
	page
}) => {
	const name = unique('dash-made');
	await openForm(page);
	await form(page).getByLabel('Name').fill(name);
	await form(page).getByLabel('Size').selectOption('small');
	// Typed as a user has it in their browser's address bar; what is stored
	// has to be the form `repose run` looks a project up by.
	await form(page).getByLabel('Repository').fill('https://GitHub.com/heracraft/Dash-Made.git');
	await form(page).getByRole('button', { name: 'Create' }).click();

	// The machine's own page, where Connect says how to reach it (I-1350).
	await page.waitForURL(/\/projects\/[^/]+$/);
	const made = await fromApi(name);
	expect(made).toBeTruthy();
	litter.push(made!.id);
	expect(page.url()).toContain(`/projects/${made!.id}`);
	await expect(page.locator('main h1')).toHaveText(name);
	await expect(
		page.getByRole('list', { name: 'Connect' }).getByRole('listitem').nth(0)
	).toContainText(`repose attach ${made!.slug}`);

	// The size asked for, and the remote in the CLI's normalized form:
	// host lowercased, scheme, `.git` and the trailing slash gone.
	expect(made!.class).toBe('small');
	expect(made!.remote_url).toBe('github.com/heracraft/Dash-Made');
	expect(made!.expires_at ?? null).toBeNull();

	// And in the list it came from, with the size it was made at.
	await page.goto('/projects');
	const row = page
		.locator('.hk-table-wrap')
		.locator('tr')
		.filter({ has: page.getByRole('link', { name, exact: true }) });
	await expect(row).toBeVisible();
	await expect(row).toContainText('small');
	await expect(row).toContainText('4 GB');
});

test('the api refuses the name and the form keeps what was typed', async ({ page }) => {
	await openForm(page);
	// A space is not in the api's name rule; the api holds that rule, so
	// its own sentence is what the field shows.
	await form(page).getByLabel('Name').fill('not a name');
	await form(page).getByRole('button', { name: 'Create' }).click();
	const error = form(page).getByTestId('new-project-error');
	await expect(error).toContainText('[A-Za-z0-9._-]');
	expect(page.url()).toContain('/projects');
	await expect(page.getByTestId('new-project-form')).toBeVisible();
	await expect(form(page).getByLabel('Name')).toHaveValue('not a name');

	// An empty name never reaches the api.
	await form(page).getByLabel('Name').fill('  ');
	await form(page).getByRole('button', { name: 'Create' }).click();
	await expect(error).toHaveText('Name is required.');

	// The same form goes through once the name is one the api takes.
	const name = unique('refused-then');
	await form(page).getByLabel('Name').fill(name);
	await form(page).getByRole('button', { name: 'Create' }).click();
	await page.waitForURL(/\/projects\/[^/]+$/);
	const made = await fromApi(name);
	expect(made).toBeTruthy();
	litter.push(made!.id);
	// No repository typed: the machine is the empty one, with no remote.
	expect(made!.remote_url).toBe('');
	// The api's default class, which is what `repose run` gets.
	expect(made!.class).toBe('large');
});

test('a name the account already has is refused in the api’s words', async ({ page }) => {
	const name = unique('twice');
	await openForm(page);
	await form(page).getByLabel('Name').fill(name);
	await form(page).getByRole('button', { name: 'Create' }).click();
	await page.waitForURL(/\/projects\/[^/]+$/);
	const made = await fromApi(name);
	litter.push(made!.id);

	await openForm(page);
	await form(page).getByLabel('Name').fill(name);
	await form(page).getByRole('button', { name: 'Create' }).click();
	await expect(form(page).getByTestId('new-project-error')).toContainText(name);
	await expect(form(page).getByTestId('new-project-error')).toContainText('exists');
});

// `repose run --temp` is the only other door to a temporary machine, and
// the api refuses a remote beside a lifetime, so the field gives way.
test('Keep it makes a temporary machine and takes the repository away', async ({ page }) => {
	const name = unique('dash-temp');
	await openForm(page);
	await form(page).getByLabel('Name').fill(name);
	await expect(form(page).getByLabel('Repository')).toBeVisible();
	await form(page).getByLabel('Keep it').selectOption('3600');
	await expect(form(page).getByLabel('Repository')).toHaveCount(0);
	await expect(form(page).getByTestId('np-temp-note')).toContainText('no snapshot');
	await form(page).getByRole('button', { name: 'Create' }).click();

	await page.waitForURL(/\/projects\/[^/]+$/);
	const made = await fromApi(name);
	expect(made).toBeTruthy();
	litter.push(made!.id);
	expect(made!.expires_at).toBeTruthy();
	expect(made!.remote_url).toBe('');

	await page.goto('/projects');
	const row = page
		.locator('.hk-table-wrap')
		.locator('tr')
		.filter({ has: page.getByRole('link', { name, exact: true }) });
	await expect(row).toContainText('temporary');
});

// A plan refusal is the api's sentence with this page's own link, as a
// row's Start draws it, not a toast that takes the sentence away.
test('an account with no plan is refused in place, with a link to the plan', async ({ page }) => {
	await setBilling({ mode: 'none' });
	await openForm(page);
	await form(page).getByLabel('Name').fill(unique('no-plan'));
	await form(page).getByRole('button', { name: 'Create' }).click();
	const error = form(page).getByTestId('new-project-error');
	await expect(error).toBeVisible();
	await expect(error.getByRole('link', { name: 'See your plan' })).toBeVisible();
	// The api's sentence, without the raw URL it ends with (refusal.ts).
	await expect(error).not.toContainText('http');
	expect(page.url()).toContain('/projects');
});

// A billing-exempt account past every plan's disk: the Billing page says
// nothing is refused, and Create makes the machine, so the page and the
// next action agree (ENG-34, I-1396). Before this the page said creating
// was refused, in red, and the dashboard created anyway.
test('a billing-exempt account past every plan\'s disk is told nothing is refused, and Create makes the machine', async ({
	page
}) => {
	await setBilling({ mode: 'exempt', disk_held_gb: 612.3 });
	await page.goto('/billing');
	await expect(page.getByText('This account is billing-exempt.')).toBeVisible();
	await expect(page.getByTestId('own-usage')).toContainText('612.3 GB');
	await expect(page.getByTestId('disk-over')).toHaveCount(0);
	const name = unique('exempt-made');
	await openForm(page);
	await form(page).getByLabel('Name').fill(name);
	await form(page).getByRole('button', { name: 'Create' }).click();
	await page.waitForURL(/\/projects\/[^/]+$/);
	const made = await fromApi(name);
	expect(made).toBeTruthy();
	litter.push(made!.id);
	await expect(page.locator('main h1')).toHaveText(name);
});

// The form and the machine it made, at the two widths and both schemes,
// at 1x, to be read at that size (CLAUDE.md "Judge visuals at real
// size"). As tokens.spec's "API tokens shots" does.
test('New project shots', async ({ page }, testInfo) => {
	const shot = (name: string) =>
		page.screenshot({ path: testInfo.outputPath(`${name}.png`), animations: 'disabled' });
	for (const width of [1440, 390]) {
		for (const scheme of ['light', 'dark'] as const) {
			const name = `shot-${width}-${scheme}`;
			await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
			await page.emulateMedia({ colorScheme: scheme });
			await openForm(page);
			await form(page).getByLabel('Name').fill(name);
			await form(page).getByLabel('Repository').fill('github.com/heracraft/todo-app');
			await shot(`np-form-${width}-${scheme}`);
			// An account holds one project per remote, and this test makes
			// four machines: the field is emptied after the shot so each
			// shot can show the same URL.
			await form(page).getByLabel('Repository').fill('');
			await form(page).getByRole('button', { name: 'Create' }).click();
			await page.waitForURL(/\/projects\/[^/]+$/);
			const made = await fromApi(name);
			litter.push(made!.id);
			await expect(page.getByRole('list', { name: 'Connect' })).toBeVisible();
			await shot(`np-made-${width}-${scheme}`);
			await page.goto('/projects');
			await expect(page.getByRole('link', { name, exact: true })).toBeVisible();
			await shot(`np-list-${width}-${scheme}`);
		}
	}
	await page.emulateMedia({ colorScheme: null });
});

test('Cancel closes the form and puts focus back on New project', async ({ page }) => {
	await openForm(page);
	await form(page).getByRole('button', { name: 'Cancel' }).click();
	await expect(page.getByTestId('new-project-form')).toHaveCount(0);
	await expect(page.getByRole('button', { name: 'New project' })).toBeFocused();
});

// Runs on (DECISIONS I-1390): the dashboard's `repose run --host`. The
// control shows only when the account owns a host; a host that is still
// waiting refuses the create in the api's own words and strands nothing,
// and repose's servers is the way out. The 2026-11-19 dashboard review
// measured the shape this replaces: 201, then `error` 8.0 s later, with
// Restart as the only control.
test('Runs on lets a customer whose host is waiting make the machine on repose instead', async ({
	page
}) => {
	const api = apiURLFromEnv();
	// Without a host there is nothing to choose, so no control.
	await openForm(page);
	await expect(form(page).getByLabel('Runs on')).toHaveCount(0);

	// The default plan: one host, and memory on repose's servers too, so
	// repose's servers is a way out. hosts.spec.ts sets a limit of its
	// own, which the fake reads as the Own hardware plan, and the files
	// run in name order whatever the command line says.
	await ownedHost({ limit: -1 });
	await ownedHost({ name: 'mac-mini' });
	await ownedHost({ state_host: 'mac-mini', state: 'registering' });
	const name = unique('on-mac');
	try {
		await openForm(page);
		const runsOn = form(page).getByLabel('Runs on');
		await expect(runsOn).toBeVisible();
		// Your host is the default, and its state is on the option.
		await expect(runsOn).toHaveValue('own');
		await expect(runsOn.locator('option[value="own"]')).toHaveText('mac-mini · waiting');
		await form(page).getByLabel('Name').fill(name);
		await form(page).getByRole('button', { name: 'Create' }).click();
		const error = form(page).getByTestId('new-project-error');
		// Hosts other files left behind make it the several-host form
		// (I-1582), which names mac-mini the same way.
		await expect(error).toContainText('mac-mini has not registered yet');
		// Nothing was made: the form is still here with the name typed,
		// and the api holds no such project.
		await expect(form(page).getByLabel('Name')).toHaveValue(name);
		expect(await fromApi(name)).toBeUndefined();
		await shots(page, 'new-project-runs-on-refused');

		await runsOn.selectOption('hosted');
		await form(page).getByRole('button', { name: 'Create' }).click();
		await page.waitForURL(/\/projects\/[^/]+$/);
		const made = await fromApi(name);
		expect(made).toBeTruthy();
		litter.push(made!.id);
		expect(made!.placement).toBe('hosted');
	} finally {
		// The host would refuse every other file's creates.
		const res = await fetch(`${api}/hosts`, { headers: { Authorization: 'Bearer playwright' } });
		for (const h of (await res.json()) as { id: string }[]) {
			await fetch(`${api}/hosts/${h.id}`, {
				method: 'DELETE',
				headers: { Authorization: 'Bearer playwright' }
			});
		}
	}
});

// A host of the customer's own that is full says which host and what it
// has against what the machine needs, as the CLI does (I-1586). The
// x86 pass measured "repose has no room for this machine" here, which
// named neither. The fake's Mac is 24 GB: 21 GB for machines, so after
// an xl (16 GB) a large (8 GB) does not fit.
test('a full host of your own names itself and the memory it has', async ({ page }) => {
	const api = apiURLFromEnv();
	await ownedHost({ limit: -1 });
	await ownedHost({ name: 'mac-full' });
	try {
		const res = await fetch(`${api}/projects`, {
			method: 'POST',
			headers: { Authorization: 'Bearer playwright', 'Content-Type': 'application/json' },
			body: JSON.stringify({ name: unique('big'), class: 'xl', placement: 'own' })
		});
		expect(res.status).toBe(201);
		litter.push(((await res.json()) as { id: string }).id);

		const name = unique('no-room');
		await openForm(page);
		await form(page).getByLabel('Name').fill(name);
		await form(page).getByLabel('Size').selectOption('large');
		await form(page).getByRole('button', { name: 'Create' }).click();
		await expect(form(page).getByTestId('new-project-error')).toContainText(
			'your host mac-full has 5 GB of memory free for machines, and a large machine needs 8 GB'
		);
		expect(await fromApi(name)).toBeUndefined();
	} finally {
		while (litter.length) await destroyProject(api, litter.pop()!);
		const res = await fetch(`${api}/hosts`, { headers: { Authorization: 'Bearer playwright' } });
		for (const h of (await res.json()) as { id: string }[]) {
			await fetch(`${api}/hosts/${h.id}`, {
				method: 'DELETE',
				headers: { Authorization: 'Bearer playwright' }
			});
		}
	}
});
