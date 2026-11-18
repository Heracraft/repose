<script lang="ts">
	// Settings, General (DECISIONS I-578, I-841): the account, its
	// timezone, the install line, Revoke certificates and Delete account.
	// Notifications, Secrets and machine.nix are the other tabs.
	import { onMount, tick } from 'svelte';
	import { toast } from 'svelte-sonner';
	import { getMe, patchMe, deleteMe, revokeAllCerts, listAccountSecrets } from '$lib/api/client';
	import { toastApiError } from '$lib/api/toast';
	import { signOut } from '$lib/auth.svelte';
	import { billingStore } from '$lib/billing.svelte';
	import ConfirmType from '$lib/components/ConfirmType.svelte';
	import CodeCopy from '$lib/components/CodeCopy.svelte';
	import { INSTALL_COMMAND } from '$lib/links';
	import { dateOnly, relativeTime } from '$lib/format';
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
	let revoking = $state(false);
	let loadError = $state<string | undefined>(undefined);

	// GitHub (DECISIONS I-1340, I-1344). Two facts and one switch: whether
	// the account holds a GH_TOKEN and when it expires, and whether a
	// laptop may copy its gh login. The switch can only subtract -- a
	// laptop that has turned the copy off in its own config.toml stays
	// off -- so the note says where it lands and when.
	let ghToken = $state<'yes' | 'no' | 'unknown'>('unknown');
	let loginCopy = $state(true);
	let savingCopy = $state(false);
	// A week before the expiry, and after it, the line names what to do
	// (I-1342); the CLI's `repose github status` says the same.
	const GH_WARN_MS = 7 * 24 * 3600 * 1000;
	let ghExpiresSoon = $derived(
		me?.github?.token_expires_at
			? new Date(me.github.token_expires_at).getTime() - Date.now() <= GH_WARN_MS
			: false
	);
	/** The expiry as ApiTokens writes one: a date ahead, what ended it behind. */
	function ghEnds(iso: string): string {
		return new Date(iso).getTime() <= Date.now()
			? `It expired ${relativeTime(iso)}`
			: `It expires ${dateOnly(iso)}`;
	}

	// Delete account opens in place of its button (I-386): the typed
	// handle, then the danger button; Keep it gives the focus back.
	let confirmDelete = $state(false);
	// Revoke certificates, the same way: the one act a lost laptop leaves
	// no other door to, since the CLI went with it (I-1220).
	let confirmRevoke = $state(false);

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
	// The account's secrets say whether a token is there; its value never
	// leaves the api, and the name is all this needs. Read before `me` is
	// assigned, so the section paints once instead of growing a line.
	async function readGhToken() {
		try {
			ghToken = (await listAccountSecrets()).some((s) => s.name === 'GH_TOKEN') ? 'yes' : 'no';
		} catch {
			ghToken = 'unknown';
		}
	}

	async function load() {
		try {
			const [fresh] = await Promise.all([getMe(), readGhToken()]);
			me = fresh;
			billingStore.setMe(me);
			loadError = undefined;
			tz = me.tz || Intl.DateTimeFormat().resolvedOptions().timeZone;
			savedTz = tz;
			loadedTz = tz;
		} catch (err) {
			loadError = loadErrorText(err, 'Could not load settings.');
			return;
		}
		loginCopy = me.github?.login_copy ?? true;
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

	async function saveLoginCopy() {
		const next = loginCopy;
		savingCopy = true;
		try {
			me = await patchMe({ github: { login_copy: next } });
			billingStore.setMe(me);
			toast.success(
				next
					? 'Your laptops may copy their gh login again.'
					: 'Your laptops stop copying their gh login.'
			);
		} catch (err) {
			loginCopy = !next;
			toastApiError(err, 'Could not save the GitHub setting.');
		} finally {
			savingCopy = false;
		}
	}

	async function onRevoke() {
		revoking = true;
		try {
			const { revoked } = await revokeAllCerts();
			confirmRevoke = false;
			toast.success(revoked === 1 ? '1 certificate revoked.' : `${revoked} certificates revoked.`);
			void focusAfterRender('revoke-certs-open');
		} catch (err) {
			toastApiError(err, 'Could not revoke the certificates.');
		} finally {
			revoking = false;
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

		<section class="set-row" id="github">
			<div class="set-head">
				<h2 id="github-h" class="hk-h2-app">GitHub</h2>
				<p class="set-note">For every laptop on this account</p>
			</div>
			<div class="set-body">
				{#if ghToken === 'yes'}
					<p class="consequence">
						A token of yours, the <code>GH_TOKEN</code> secret, for the repositories you picked.
					</p>
					{#if me.github?.token_expires_at}
						<p class="consequence">
							{ghEnds(me.github.token_expires_at)}.
							{#if ghExpiresSoon}
								<code>repose github setup</code> on a laptop stores a new one.
							{/if}
						</p>
					{/if}
				{:else if ghToken === 'no'}
					<p class="consequence">
						No token. <code>repose github setup</code> on a laptop makes one for the repositories you
						pick.
					</p>
				{:else}
					<p class="consequence">Could not read whether your account holds a token.</p>
				{/if}
				<label class="hk-check after">
					<input
						type="checkbox"
						bind:checked={loginCopy}
						onchange={saveLoginCopy}
						disabled={savingCopy}
					/>
					Laptops may copy their gh login
				</label>
				<p class="field-note">
					Turned off, each laptop stops within an hour. Turning it back on lets a laptop copy again
					only where that laptop still has <code>gh</code> on its own list.
				</p>
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
	<section class="set-row" id="revoke-certs">
		<div class="set-head">
			<h2 id="revoke-h" class="hk-h2-app">Revoke certificates</h2>
		</div>
		<div class="set-body">
			<p class="consequence">
				The connections your certificates opened close within 30 seconds, on any device.
			</p>
			{#if confirmRevoke}
				<div class="after">
					<ConfirmType
						word={me.handle}
						label="Revoke certificates"
						busy={revoking}
						busyLabel="Revoking…"
						onconfirm={onRevoke}
						oncancel={() => {
							confirmRevoke = false;
							void focusAfterRender('revoke-certs-open');
						}}
						cancelLabel="Keep them"
						autofocus
					/>
				</div>
			{:else}
				<button
					type="button"
					id="revoke-certs-open"
					class="hk-btn-danger hk-btn-sm after"
					onclick={() => (confirmRevoke = true)}>Revoke certificates…</button
				>
			{/if}
		</div>
	</section>

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
	/* The same note as the Notifications tab's, to the pixel: the body's
	   smallest rank must not outrank the head's own note. */
	.field-note {
		margin: 0.375rem 0 0;
		font-size: 0.8125rem;
		line-height: 1.25rem;
		color: var(--hk-text-3);
		text-wrap: pretty;
	}
</style>
