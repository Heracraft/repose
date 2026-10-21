// The account's machine.nix on the dashboard (DECISIONS I-490): edit and
// save it on Settings' machine.nix tab (I-841), a copy pushed from the
// laptop since is not overwritten, and each project's Config tab has the
// opt-out switch.
import { test, expect } from '@playwright/test';
import { signIn, createProject, apiURLFromEnv } from './helpers';

async function putPersonal(fragment: string) {
	const res = await fetch(`${apiURLFromEnv()}/me/config`, {
		method: 'PUT',
		headers: { Authorization: 'Bearer playwright', 'Content-Type': 'application/json' },
		body: JSON.stringify({ fragment, source: 'cli' })
	});
	if (!res.ok) throw new Error(`putPersonal: ${res.status} ${await res.text()}`);
}

test.beforeEach(async ({ page }) => {
	await putPersonal('');
	await signIn(page);
});

test.afterAll(async () => {
	await putPersonal('');
});

test('machine.nix is edited and saved in Settings', async ({ page }) => {
	await page.goto('/settings/machine-nix');
	await expect(page).toHaveTitle(/^(\(\d+\) )?machine\.nix · Settings · repose$/);
	const section = page.locator('#machine-nix');
	await expect(section.getByText('You have none yet.')).toBeVisible();
	// Empty, the editor shows the shape of a module as a placeholder, which
	// is not content: nothing to save.
	await expect(section.locator('.cm-placeholder')).toContainText('home.packages');
	// Save is the page's one primary, waiting until there is a change.
	const save = section.getByRole('button', { name: 'Save', exact: true });
	await expect(save).toHaveClass(/(^|\s)hk-btn(\s|$)/);
	await expect(save).toHaveAttribute('aria-disabled', 'true');
	await section.locator('.cm-content').click();
	await page.keyboard.type('{ pkgs, ... }: { home.packages = [ pkgs.ripgrep ]; }');
	await expect(section.getByText('Unsaved changes')).toBeVisible();
	await expect(save).not.toHaveAttribute('aria-disabled', 'true');
	await save.click();
	await expect(section.getByRole('status')).toContainText('Saved.');
	await page.reload();
	await expect(section.locator('.cm-content')).toContainText('pkgs.ripgrep');
	await expect(section.getByText(/on the dashboard$/)).toBeVisible();
});

test('a copy pushed from the laptop after the page loaded is not overwritten', async ({ page }) => {
	await putPersonal('{ }\n');
	await page.goto('/settings/machine-nix');
	const section = page.locator('#machine-nix');
	await expect(section.getByText(/from your laptop$/)).toBeVisible();
	await putPersonal('{ home.packages = [ ]; }\n');
	await section.locator('.cm-content').click();
	await page.keyboard.press('End');
	await page.keyboard.type(' ');
	await section.getByRole('button', { name: 'Save', exact: true }).click();
	await expect(section.getByRole('alert')).toContainText('changed after this page loaded');
	await section.getByRole('button', { name: "Load the account's copy" }).click();
	await expect(section.locator('.cm-content')).toContainText('home.packages');
});

test("the Config page's switch keeps machine.nix off one machine", async ({ page }) => {
	const p = await createProject(apiURLFromEnv(), {
		name: 'personal-app',
		remote_url: 'github.com/heracraft/personal-app'
	});
	await page.goto(`/projects/${p.id}/config`);
	const box = page.getByRole('checkbox', { name: 'Use your machine.nix on this machine' });
	await expect(box).toBeChecked();
	await box.uncheck();
	await expect(page.getByText('machine.nix is off for this machine')).toBeVisible();
	await page.reload();
	await expect(box).not.toBeChecked();
	await page.goto(`/projects/${p.id}/config`);
	await page.getByRole('link', { name: /Edit machine\.nix/ }).click();
	await expect(page).toHaveURL('/settings/machine-nix');
	await expect(page.locator('#machine-nix').getByText(`Off on ${p.slug}.`)).toBeVisible();
	await expect(page.locator('#machine-nix')).toBeInViewport();
});

// The links and bookmarks of before I-841 land on the tab.
test('/settings#machine-nix and /account#machine-nix open the machine.nix tab', async ({
	page
}) => {
	for (const url of ['/settings#machine-nix', '/account#machine-nix']) {
		await page.goto(url);
		await expect(page).toHaveURL('/settings/machine-nix');
		await expect(page.locator('#machine-nix .nix-frame')).toBeVisible();
	}
});

// On its own tab the editor is the page's subject: at least 28rem tall
// from a 640px column and 20rem on a phone, growing with its text to 24
// lines and scrolling inside itself past that.
test('the machine.nix editor has room to write in, up to a cap', async ({ page }) => {
	await page.setViewportSize({ width: 1440, height: 900 });
	await page.goto('/settings/machine-nix');
	const section = page.locator('#machine-nix');
	const scroller = section.locator('.cm-scroller');
	await expect(scroller).toBeVisible();
	const empty = (await scroller.boundingBox())!.height;
	expect(empty).toBeGreaterThanOrEqual(28 * 16);
	await section.locator('.cm-content').click();
	for (let i = 0; i < 50; i++) await page.keyboard.press('Enter');
	const capped = (await scroller.boundingBox())!.height;
	expect(capped).toBeLessThan(24 * 20 + 20);
	expect(await scroller.evaluate((el) => el.scrollHeight > el.clientHeight)).toBe(true);
	// The foot with Save stays in view under the editor at 1440x900.
	await expect(section.getByRole('button', { name: 'Save', exact: true })).toBeInViewport();

	await page.setViewportSize({ width: 390, height: 844 });
	await page.goto('/settings/machine-nix');
	const phone = (await scroller.boundingBox())!.height;
	expect(phone).toBeGreaterThanOrEqual(20 * 16);
	expect(phone).toBeLessThan(28 * 16);
});
