<script lang="ts">
	import { onMount, tick } from 'svelte';
	import { beforeNavigate, goto } from '$app/navigation';
	import { toast } from 'svelte-sonner';
	import { getMe, patchMe, notifyTest } from '$lib/api/client';
	import { ApiError } from '$lib/api/errors';
	import { toastApiError } from '$lib/api/toast';
	import PageShell from '$lib/components/PageShell.svelte';
	import LoadState, { loadErrorText } from '$lib/components/LoadState.svelte';
	import type { Me } from '$lib/api/types';

	// The timezone and the email toggle save the moment they change; the
	// ntfy URL is typed, so it has its own Save next to the field
	// (DECISIONS I-332).
	let me = $state<Me | undefined>(undefined);
	let tz = $state('');
	let savedTz = '';
	// The zone the account had when the page loaded.
	let loadedTz = $state('');
	let emailOn = $state(true);
	let ntfyUrl = $state('');
	let savedNtfyUrl = $state('');
	let savingNtfy = $state(false);
	let testAvailable = $state(true);
	let testing = $state(false);
	let loadError = $state<string | undefined>(undefined);
	// A link followed while the ntfy URL was unsaved: held here, and the
	// page asks about it in place instead of in a native confirm() box.
	let heldNavigation = $state<URL | undefined>(undefined);
	let leaveAnyway = false;
	let stayButton = $state<HTMLButtonElement | undefined>(undefined);
	// What had focus when the navigation was held (the link followed), so
	// Stay can give it back instead of dropping it to <body> (I-393).
	let heldFrom: HTMLElement | undefined;

	let ntfyDirty = $derived(ntfyUrl.trim() !== savedNtfyUrl);

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
		} catch (err) {
			loadError = loadErrorText(err, 'Could not load settings.');
			return;
		}
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
		if (!ntfyDirty || leaveAnyway) return;
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
		const back = heldFrom?.isConnected ? heldFrom : document.getElementById('ntfy-url');
		heldFrom = undefined;
		await tick();
		back?.focus();
	}

	async function leave() {
		const url = heldNavigation;
		heldNavigation = undefined;
		if (!url) return;
		leaveAnyway = true;
		try {
			// eslint-disable-next-line svelte/no-navigation-without-resolve -- the URL came from SvelteKit's own navigation, already resolved
			await goto(url);
		} finally {
			leaveAnyway = false;
		}
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

	async function sendTest() {
		testing = true;
		try {
			const result = await notifyTest();
			if (result.email === 'ok' || result.ntfy === 'ok') toast.success('Test notification sent.');
			else toast.error('The test notification failed on every channel.');
		} catch (err) {
			if (err instanceof ApiError && err.code === 'not_found') testAvailable = false;
			else toastApiError(err, 'Could not send a test notification.');
		} finally {
			testing = false;
		}
	}
</script>

<svelte:head>
	<title>Settings — repose</title>
</svelte:head>

<PageShell title="Settings" width="form">
	<LoadState
		status={me ? 'ready' : loadError ? 'failed' : 'loading'}
		error={loadError}
		onretry={load}
	>
		<!-- The first section draws no rule of its own (.form-section:first-child
		     in layout.css): the page title's rule is directly above it. -->
		<div class="form-section">
			<h2 class="text-xl font-semibold">Timezone</h2>
			{#if timezones.length}
				<select
					class="field mt-2 w-full sm:w-72"
					aria-label="Timezone"
					bind:value={tz}
					onchange={saveTz}
				>
					{#each timezones as z (z)}
						<option value={z}>{z}</option>
					{/each}
				</select>
			{:else}
				<input
					class="field mt-2 w-full sm:w-72"
					aria-label="Timezone"
					bind:value={tz}
					onchange={saveTz}
				/>
			{/if}
		</div>

		<div class="form-section">
			<h2 class="text-xl font-semibold">Notifications</h2>
			<label class="check-row mt-2">
				<input type="checkbox" bind:checked={emailOn} onchange={saveEmail} />
				Email notifications
			</label>
			<form class="mt-3" onsubmit={saveNtfy}>
				<label for="ntfy-url" class="mb-1 block text-sm font-medium text-ink">ntfy URL</label>
				<div class="flex gap-2">
					<input
						id="ntfy-url"
						class="field min-w-0 flex-1"
						placeholder="https://ntfy.sh/your-topic"
						bind:value={ntfyUrl}
					/>
					<button type="submit" class="btn shrink-0" disabled={savingNtfy || !ntfyDirty}>
						{savingNtfy ? 'Saving…' : 'Save'}
					</button>
				</div>
				{#if ntfyDirty && heldNavigation}
					<div
						class="banner banner--warn mt-3 mb-0 flex flex-wrap items-center justify-between gap-3"
						role="alert"
					>
						<p>The ntfy URL is not saved. Leave this page anyway?</p>
						<div class="flex items-center gap-2">
							<button type="button" class="btn-quiet btn--sm" bind:this={stayButton} onclick={stay}
								>Stay</button
							>
							<button type="button" class="btn-ghost" onclick={leave}>Leave</button>
						</div>
					</div>
				{:else if ntfyDirty}
					<p class="mt-1.5 text-sm text-ink-muted">Not saved yet.</p>
				{/if}
			</form>
			<div class="mt-3">
				{#if testAvailable}
					<button type="button" class="btn-ghost px-0" disabled={testing} onclick={sendTest}>
						{testing ? 'Sending…' : 'Send test'}
					</button>
				{:else}
					<span class="text-sm text-ink-faint">Test not available yet</span>
				{/if}
			</div>
		</div>

		<div class="form-section">
			<h2 class="text-xl font-semibold">Install</h2>
			<code class="codeblock mt-2 block px-3 py-2"
				>curl -fsSL https://repose.herakraft.co/install.sh | sh</code
			>
		</div>
	</LoadState>
</PageShell>
