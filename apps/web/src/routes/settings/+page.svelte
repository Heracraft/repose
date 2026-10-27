<script lang="ts">
	// Settings, General (DECISIONS I-578, I-841): the account, its
	// timezone, the install line and Delete account. Notifications,
	// Secrets and machine.nix are the other tabs.
	import { onMount, tick } from 'svelte';
	import { toast } from 'svelte-sonner';
	import { getMe, patchMe, deleteMe } from '$lib/api/client';
	import { toastApiError } from '$lib/api/toast';
	import { signOut } from '$lib/auth.svelte';
	import { billingStore } from '$lib/billing.svelte';
	import ConfirmType from '$lib/components/ConfirmType.svelte';
	import CodeCopy from '$lib/components/CodeCopy.svelte';
	import { INSTALL_COMMAND } from '$lib/links';
	import { dateOnly } from '$lib/format';
	import { focusAfterRender } from '$lib/focus';
	import { title } from '$lib/title';
	import LoadState, { loadErrorText } from '$lib/components/LoadState.svelte';
	import type { Me } from '$lib/api/types';

	// The timezone saves the moment it changes (DECISIONS I-332).
	// The shell's /me read, when it has one, so a visit after the first
	// paints at once; load() reads it again.
	const seen = billingStore.me;
	const seenTz = seen ? seen.tz || Intl.DateTimeFormat().resolvedOptions().timeZone : '';
	let me = $state<Me | undefined>(seen);
	let tz = $state(seenTz);
	let savedTz = seenTz;
	// The zone the account had when the page loaded.
	let loadedTz = $state(seenTz);
	let deleting = $state(false);
	let loadError = $state<string | undefined>(undefined);

	// Delete account opens in place of its button (I-386): the typed
	// handle, then the danger button; Keep it gives the focus back.
	let confirmDelete = $state(false);

	const supported =
		typeof Intl.supportedValuesOf === 'function' ? Intl.supportedValuesOf('timeZone') : [];
	// The browser's list leaves out some names the api accepts, UTC among
	// them; the account's own zone is always an option, or the select shows
	// blank and the next change would save a zone the user never picked.
	let timezones = $derived(
		supported.length && loadedTz && !supported.includes(loadedTz)
			? [loadedTz, ...supported]
			: supported
	);

	// loadError is cleared only by a load that worked, so a Retry keeps the
	// banner and its button ("Retrying…") until the answer comes.
	async function load() {
		try {
			me = await getMe();
			billingStore.setMe(me);
			loadError = undefined;
			tz = me.tz || Intl.DateTimeFormat().resolvedOptions().timeZone;
			savedTz = tz;
			loadedTz = tz;
		} catch (err) {
			loadError = loadErrorText(err, 'Could not load settings.');
			return;
		}
		// The sections render only now, after the browser looked for the
		// fragment's target and found nothing: /settings#delete-account and
		// /account#account arrive here with one.
		void tick().then(() => {
			const id = decodeURIComponent(location.hash.slice(1));
			if (id) document.getElementById(id)?.scrollIntoView();
		});
		// An account that never set a timezone shows this browser's; with
		// no Save button to press, store it so what the page shows is what
		// the account has. Quietly: nothing the user did.
		if (!me.tz && tz) {
			try {
				me = await patchMe({ tz });
			} catch {
				// Left unset; the next change of the select saves it.
			}
		}
	}

	onMount(load);

	async function saveTz() {
		const next = tz;
		try {
			me = await patchMe({ tz: next });
			savedTz = next;
			toast.success(`Timezone set to ${next}.`);
		} catch (err) {
			tz = savedTz;
			toastApiError(err, 'Could not save the timezone.');
		}
	}

	async function onDelete() {
		deleting = true;
		try {
			await deleteMe();
			toast.success('Account deletion started. Everything is deleted in 30 days.');
			await signOut();
		} catch (err) {
			deleting = false;
			toastApiError(err, 'Could not start account deletion.');
		}
	}
</script>

<svelte:head>
	<title>{title('Settings')}</title>
</svelte:head>

<LoadState
	status={me ? 'ready' : loadError ? 'failed' : 'loading'}
	error={loadError}
	onretry={load}
	shape="rows"
>
	{#if me}
		<!-- The account was its own page until I-578; /account redirects here. -->
		<section class="set-row" id="account">
			<div class="set-head">
				<h2 id="account-h" class="hk-h2-app">Account</h2>
			</div>
			<div class="set-body">
				<dl class="hk-facts">
					<div>
						<dt>Handle</dt>
						<dd class="hk-mono">{me.handle}</dd>
					</div>
					<div>
						<dt>Email</dt>
						<dd class="email">{me.email}</dd>
					</div>
					<div>
						<dt>GitHub</dt>
						<dd class:quiet={!me.github_login}>{me.github_login ?? 'Not linked'}</dd>
					</div>
					<div>
						<dt>Created</dt>
						<dd>{dateOnly(me.created_at)}</dd>
					</div>
				</dl>
				<button type="button" class="hk-btn-secondary hk-btn-sm after" onclick={() => signOut()}
					>Sign out</button
				>
			</div>
		</section>

		<section class="set-row" id="timezone">
			<div class="set-head">
				<h2 id="tz-h" class="hk-h2-app">Timezone</h2>
				<p class="set-note"><code>repose run</code> replaces it with your laptop's.</p>
			</div>
			<div class="set-body">
				{#if timezones.length}
					<select class="hk-select tz" aria-label="Timezone" bind:value={tz} onchange={saveTz}>
						{#each timezones as z (z)}
							<option value={z}>{z}</option>
						{/each}
					</select>
				{:else}
					<input class="hk-input tz" aria-label="Timezone" bind:value={tz} onchange={saveTz} />
				{/if}
			</div>
		</section>
	{/if}
</LoadState>

<!-- Install waits with the rest, so the account's rows arriving above it
     do not push it down (I-664); after a failed read it shows alone. -->
{#if me || loadError}
	<section class="set-row" id="install">
		<div class="set-head">
			<h2 id="install-h" class="hk-h2-app">Install</h2>
			<p class="set-note">Runs on macOS and Linux.</p>
		</div>
		<div class="set-body">
			<CodeCopy command={INSTALL_COMMAND} what="install command" />
		</div>
	</section>
{/if}

{#if me}
	<!-- The one irreversible action, last on the page. -->
	<section class="set-row" id="delete-account">
		<div class="set-head">
			<h2 id="delete-h" class="hk-h2-app">Delete account</h2>
		</div>
		<div class="set-body">
			<p class="consequence">
				Stops every machine at once. Everything, including snapshots, is deleted 30 days later.
			</p>
			{#if confirmDelete}
				<div class="after">
					<ConfirmType
						word={me.handle}
						label="Delete account"
						busy={deleting}
						busyLabel="Deleting…"
						onconfirm={onDelete}
						oncancel={() => {
							confirmDelete = false;
							void focusAfterRender('delete-account-open');
						}}
						cancelLabel="Keep it"
						autofocus
					/>
				</div>
			{:else}
				<button
					type="button"
					id="delete-account-open"
					class="hk-btn-danger hk-btn-sm after"
					onclick={() => (confirmDelete = true)}>Delete account…</button
				>
			{/if}
		</div>
	</section>
{/if}

<style>
	.quiet {
		color: var(--hk-text-3);
	}
	.email {
		overflow-wrap: anywhere;
	}
	.after {
		margin-top: 1rem;
	}
	.tz {
		max-width: 20rem;
	}
	.consequence {
		margin: 0;
		font-size: 0.9375rem;
		color: var(--hk-text-2);
		text-wrap: pretty;
	}
</style>
