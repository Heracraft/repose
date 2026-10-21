<script lang="ts">
	import { onMount, tick } from 'svelte';
	import { beforeNavigate } from '$app/navigation';
	import { toast } from 'svelte-sonner';
	import { leaveAnyway, leavingAnyway } from '$lib/leave';
	import { getMe, patchMe, notifyTest, deleteMe } from '$lib/api/client';
	import { ApiError } from '$lib/api/errors';
	import { toastApiError } from '$lib/api/toast';
	import { signOut } from '$lib/auth.svelte';
	import PageShell from '$lib/components/PageShell.svelte';
	import ConfirmType from '$lib/components/ConfirmType.svelte';
	import MachineNix from '$lib/components/MachineNix.svelte';
	import AccountSecrets from '$lib/components/AccountSecrets.svelte';
	import CodeCopy from '$lib/components/CodeCopy.svelte';
	import { INSTALL_COMMAND } from '$lib/links';
	import { dateOnly } from '$lib/format';
	import { focusAfterRender } from '$lib/focus';
	import LoadState, { loadErrorText } from '$lib/components/LoadState.svelte';
	import type { Me } from '$lib/api/types';

	// The timezone and the email toggle save the moment they change; the
	// ntfy URL and the Google Chat webhook are typed, so each has its own
	// Save next to the field (DECISIONS I-332, I-692).
	let me = $state<Me | undefined>(undefined);
	let tz = $state('');
	let savedTz = '';
	// The zone the account had when the page loaded.
	let loadedTz = $state('');
	let emailOn = $state(true);
	let ntfyUrl = $state('');
	let savedNtfyUrl = $state('');
	let savingNtfy = $state(false);
	// The Google Chat webhook is a credential (I-692): the api never sends
	// it back, so the field starts empty and holds only a new URL.
	let gchatUrl = $state('');
	let savingGChat = $state(false);
	let testAvailable = $state(true);
	let testing = $state(false);
	let deleting = $state(false);
	let loadError = $state<string | undefined>(undefined);
	// A link followed while the ntfy URL was unsaved: held here, and the
	// page asks about it in place instead of in a native confirm() box.
	let heldNavigation = $state<URL | undefined>(undefined);
	let stayButton = $state<HTMLButtonElement | undefined>(undefined);
	// What had focus when the navigation was held (the link followed), so
	// Stay can give it back instead of dropping it to <body> (I-393).
	let heldFrom: HTMLElement | undefined;

	let ntfyDirty = $derived(ntfyUrl.trim() !== savedNtfyUrl);
	let gchatDirty = $derived(gchatUrl.trim() !== '');
	let gchat = $derived(me?.notify?.gchat ?? null);

	// Delete account opens in place of its button (I-386): the typed
	// handle, then the danger button; Cancel gives the focus back.
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
	// banner and its button ("Retrying…") until the answer comes, as on
	// every other page; cleared first, it swapped in "Loading…" mid-click.
	async function load() {
		try {
			me = await getMe();
			loadError = undefined;
			tz = me.tz || Intl.DateTimeFormat().resolvedOptions().timeZone;
			savedTz = tz;
			loadedTz = tz;
			emailOn = me.notify?.email ?? true;
			ntfyUrl = me.notify?.ntfy_url ?? '';
			savedNtfyUrl = ntfyUrl;
			gchatUrl = '';
		} catch (err) {
			loadError = loadErrorText(err, 'Could not load settings.');
			return;
		}
		// The sections render only now, after the browser looked for the
		// fragment's target and found nothing: the Config page links to
		// #machine-nix, and /account#machine-nix arrives here with it.
		void tick().then(() => {
			const id = decodeURIComponent(location.hash.slice(1));
			if (id) document.getElementById(id)?.scrollIntoView();
		});
		// An account that never set a timezone shows this browser's; with
		// no Save button to press any more, store it so what the page shows
		// is what the account has. Quietly: nothing the user did.
		if (!me.tz && tz) {
			try {
				me = await patchMe({ tz });
			} catch {
				// Left unset; the next change of the select saves it.
			}
		}
	}

	onMount(load);

	beforeNavigate(({ cancel, type, to }) => {
		if ((!ntfyDirty && !gchatDirty) || leavingAnyway()) return;
		// A tab close or reload gets the browser's own prompt from cancel().
		// A link inside the dashboard is held, and the page asks beside the
		// field that is unsaved, so the question uses the house banner and
		// buttons rather than the browser's dialog.
		cancel();
		if (type === 'leave' || !to) return;
		const active = document.activeElement;
		heldFrom = active instanceof HTMLElement && active !== document.body ? active : undefined;
		heldNavigation = to.url;
		void tick().then(() => stayButton?.focus());
	});

	/**
	 * Stay closes the question and puts focus back on the control that
	 * asked to leave; a navigation with no such control (the browser's Back)
	 * returns it to the unsaved field.
	 */
	async function stay() {
		heldNavigation = undefined;
		const back = heldFrom?.isConnected
			? heldFrom
			: document.getElementById(ntfyDirty ? 'ntfy-url' : 'gchat-url');
		heldFrom = undefined;
		await tick();
		back?.focus();
	}

	async function leave() {
		const url = heldNavigation;
		heldNavigation = undefined;
		if (!url) return;
		await leaveAnyway(url);
	}

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

	async function saveEmail() {
		const next = emailOn;
		try {
			me = await patchMe({ notify: { email: next } });
			toast.success(next ? 'Email notifications on.' : 'Email notifications off.');
		} catch (err) {
			emailOn = !next;
			toastApiError(err, 'Could not change email notifications.');
		}
	}

	async function saveNtfy(e: SubmitEvent) {
		e.preventDefault();
		const next = ntfyUrl.trim();
		savingNtfy = true;
		try {
			me = await patchMe({ notify: { ntfy_url: next || null } });
			ntfyUrl = next;
			savedNtfyUrl = next;
			heldNavigation = undefined;
			toast.success(next ? 'ntfy URL saved.' : 'ntfy turned off.');
		} catch (err) {
			toastApiError(err, 'Could not save the ntfy URL.');
		} finally {
			savingNtfy = false;
		}
	}

	async function saveGChat(e: SubmitEvent) {
		e.preventDefault();
		const next = gchatUrl.trim();
		if (!next) return;
		savingGChat = true;
		try {
			me = await patchMe({ notify: { gchat_url: next } });
			gchatUrl = '';
			heldNavigation = undefined;
			toast.success('Google Chat webhook saved.');
		} catch (err) {
			toastApiError(err, 'Could not save the Google Chat webhook.');
		} finally {
			savingGChat = false;
		}
	}

	async function removeGChat() {
		savingGChat = true;
		try {
			me = await patchMe({ notify: { gchat_url: null } });
			gchatUrl = '';
			toast.success('Google Chat turned off.');
		} catch (err) {
			toastApiError(err, 'Could not turn Google Chat off.');
		} finally {
			savingGChat = false;
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

	async function sendTest() {
		testing = true;
		try {
			const result = await notifyTest();
			const failed = [
				result.ntfy === 'error' && `ntfy: ${result.ntfy_error ?? 'failed'}`,
				result.gchat === 'error' && `Google Chat: ${result.gchat_error ?? 'failed'}`
			].filter(Boolean);
			if (result.email === 'ok' || result.ntfy === 'ok' || result.gchat === 'ok') {
				if (failed.length) toast.error(`Test sent, but failed on ${failed.join('; ')}.`);
				else toast.success('Test notification sent.');
			} else toast.error('The test notification failed on every channel.');
			// A test that got through to Google Chat clears its broken mark,
			// and one Google refused sets it.
			if (result.gchat) me = await getMe();
		} catch (err) {
			if (err instanceof ApiError && err.code === 'not_found') testAvailable = false;
			else toastApiError(err, 'Could not send a test notification.');
		} finally {
			testing = false;
		}
	}
</script>

<svelte:head>
	<title>Settings · repose</title>
</svelte:head>

<PageShell title="Settings">
	<!-- Each section is a row of two tracks from 1024px (I-837): the h2
	     and at most one constraint in 15rem on the left, the controls in
	     40rem on the right, the same for every section. AccountSecrets and
	     MachineNix draw the same row (set-row). -->
	<div class="settings">
		<!-- Only what reads /v1/me waits on it: the install line, machine.nix
		     and the secrets have their own sources and show while it loads
		     or after it failed. -->
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
								<dd>{me.email}</dd>
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

				<section class="set-row">
					<div class="set-head">
						<h2 id="notify-h" class="hk-h2-app">Notifications</h2>
					</div>
					<div class="set-body hk-fields">
						<label class="hk-check">
							<input type="checkbox" bind:checked={emailOn} onchange={saveEmail} />
							Email notifications
						</label>
						<form onsubmit={saveNtfy}>
							<label for="ntfy-url" class="hk-field-label">ntfy URL</label>
							<div class="field-row">
								<input
									id="ntfy-url"
									class="hk-input mono"
									placeholder="https://ntfy.sh/your-topic"
									bind:value={ntfyUrl}
								/>
								<button
									type="submit"
									class="hk-btn-secondary hk-btn-sm"
									disabled={!ntfyDirty}
									aria-disabled={savingNtfy ? 'true' : undefined}
								>
									{savingNtfy ? 'Saving…' : 'Save'}
								</button>
							</div>
							{#if ntfyDirty && heldNavigation}
								<div class="hk-banner ask" role="alert">
									<p>The ntfy URL is not saved. Leave this page anyway?</p>
									<div class="ask-actions">
										<button
											type="button"
											class="hk-btn-secondary hk-btn-sm"
											bind:this={stayButton}
											onclick={stay}>Stay</button
										>
										<button type="button" class="hk-btn-ghost hk-btn-sm" onclick={leave}
											>Leave</button
										>
									</div>
								</div>
							{:else if ntfyDirty}
								<p class="field-note">Not saved yet.</p>
							{/if}
						</form>
						<form onsubmit={saveGChat}>
							<div class="label-row">
								<label for="gchat-url" class="hk-field-label">Google Chat webhook</label>
								{#if gchat}
									<!-- At phone width "space" is dropped so Remove stays on the label line. -->
									<span class="space"
										><span class="max-sm:hidden">space&nbsp;</span><span class="hk-mono"
											>{gchat.space}</span
										></span
									>
									{#if gchat.broken_at}
										<span class="hk-badge hk-badge--warn"
											>broken since {dateOnly(gchat.broken_at)}</span
										>
									{/if}
									<button
										type="button"
										class="hk-btn-ghost hk-btn-sm remove"
										aria-disabled={savingGChat ? 'true' : undefined}
										onclick={() => !savingGChat && removeGChat()}>Remove</button
									>
								{/if}
							</div>
							<div class="field-row">
								<input
									id="gchat-url"
									type="password"
									autocomplete="off"
									spellcheck="false"
									class="hk-input mono"
									placeholder={gchat
										? 'New webhook URL'
										: 'https://chat.googleapis.com/v1/spaces/...'}
									bind:value={gchatUrl}
								/>
								<button
									type="submit"
									class="hk-btn-secondary hk-btn-sm"
									disabled={!gchatDirty}
									aria-disabled={savingGChat ? 'true' : undefined}
								>
									{savingGChat ? 'Saving…' : 'Save'}
								</button>
							</div>
							{#if gchatDirty && !ntfyDirty && heldNavigation}
								<div class="hk-banner ask" role="alert">
									<p>The Google Chat webhook is not saved. Leave this page anyway?</p>
									<div class="ask-actions">
										<button
											type="button"
											class="hk-btn-secondary hk-btn-sm"
											bind:this={stayButton}
											onclick={stay}>Stay</button
										>
										<button type="button" class="hk-btn-ghost hk-btn-sm" onclick={leave}
											>Leave</button
										>
									</div>
								</div>
							{:else if gchatDirty}
								<p class="field-note">Not saved yet.</p>
							{/if}
						</form>
						<div>
							{#if testAvailable}
								<button
									type="button"
									class="hk-btn-secondary hk-btn-sm"
									aria-disabled={testing ? 'true' : undefined}
									onclick={() => !testing && sendTest()}
								>
									{testing ? 'Sending…' : 'Send test'}
								</button>
							{:else}
								<span class="field-note">Test not available yet</span>
							{/if}
						</div>
					</div>
				</section>

				<section class="set-row">
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

		<section class="set-row">
			<div class="set-head">
				<h2 id="install-h" class="hk-h2-app">Install</h2>
				<p class="set-note">Runs on macOS and Linux.</p>
			</div>
			<div class="set-body">
				<CodeCopy command={INSTALL_COMMAND} what="install command" />
			</div>
		</section>

		<MachineNix />

		<AccountSecrets />

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
	</div>
</PageShell>

<style>
	/* The rows, here and in MachineNix and AccountSecrets. */
	.settings :global(.set-row) {
		display: grid;
		grid-template-columns: minmax(0, 1fr);
		row-gap: 1rem;
	}
	.settings :global(.set-row:not(:first-child)) {
		margin-top: 2.75rem;
		padding-top: 2rem;
		border-top: 1px solid var(--hk-hairline);
	}
	.settings :global(.set-head) {
		min-width: 0;
	}
	.settings :global(.set-note) {
		margin: 0.375rem 0 0;
		font-size: 0.8125rem;
		line-height: 1.25rem;
		color: var(--hk-text-3);
		text-wrap: pretty;
	}
	.settings :global(.set-body) {
		min-width: 0;
	}
	@media (min-width: 1024px) {
		.settings :global(.set-row) {
			grid-template-columns: 15rem minmax(0, 40rem);
			column-gap: 3rem;
		}
	}
	/* Inside a row the first fact or table row drops its own top rule:
	   the row's hairline is directly above it. */
	.settings :global(.set-body > .hk-facts > div:first-child),
	.settings :global(.set-body > .hk-table thead th) {
		border-top: 0;
	}
	.settings :global(.set-body > .hk-facts > div:first-child) {
		padding-top: 0;
	}

	.quiet {
		color: var(--hk-text-3);
	}
	.after {
		margin-top: 1rem;
	}
	.mono {
		font-family: var(--hk-font-mono);
	}
	.field-row {
		display: flex;
		align-items: center;
		gap: 0.5rem;
	}
	.field-row .hk-input {
		flex: 1 1 auto;
		min-width: 0;
	}
	.field-row button {
		flex: none;
	}
	.label-row {
		display: flex;
		flex-wrap: wrap;
		align-items: baseline;
		gap: 0.25rem 0.75rem;
		margin-bottom: 0.375rem;
	}
	.label-row .hk-field-label {
		margin-bottom: 0;
	}
	.space {
		font-size: 0.8125rem;
		color: var(--hk-text-3);
	}
	.remove {
		margin-left: auto;
		margin-right: -0.625rem;
	}
	.field-note {
		margin: 0.375rem 0 0;
		font-size: 0.8125rem;
		color: var(--hk-text-3);
	}
	.ask {
		margin: 0.75rem 0 0;
		justify-content: space-between;
	}
	.ask-actions {
		display: flex;
		align-items: center;
		gap: 0.5rem;
	}
	.tz {
		max-width: 20rem;
	}
	.consequence {
		margin: 0;
		font-size: 0.9375rem;
		color: var(--hk-text-2);
	}
</style>
