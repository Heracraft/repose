// The account's machine.nix on the dashboard (DECISIONS I-490): edit and
// save it in Settings (the Account page until I-578), a copy pushed from the laptop since is not
// overwritten, and each project's Config page has the opt-out switch.
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
	await page.goto('/settings');
	const section = page.locator('#machine-nix');
	await expect(section.getByText('You have none yet.')).toBeVisible();
	const save = section.getByRole('button', { name: 'Save', exact: true });
	await expect(save).toBeDisabled();
	await section.locator('.cm-content').click();
	await page.keyboard.type('{ pkgs, ... }: { home.packages = [ pkgs.ripgrep ]; }');
	await expect(section.getByText('Not saved yet.')).toBeVisible();
	await save.click();
	await expect(section.getByRole('status')).toContainText('Saved.');
	await page.reload();
	await expect(section.locator('.cm-content')).toContainText('pkgs.ripgrep');
	await expect(section.getByText(/on the dashboard\./)).toBeVisible();
});

test('a copy pushed from the laptop after the page loaded is not overwritten', async ({ page }) => {
	await putPersonal('{ }\n');
	await page.goto('/settings');
	const section = page.locator('#machine-nix');
	await expect(section.getByText(/from your laptop\./)).toBeVisible();
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
	await page.getByRole('link', { name: 'Edit machine.nix on your account' }).click();
	await expect(page).toHaveURL('/settings#machine-nix');
	await expect(page.locator('#machine-nix').getByText(`Off on ${p.slug}.`)).toBeVisible();
	await expect(page.getByRole('heading', { name: 'machine.nix' })).toBeInViewport();
});
