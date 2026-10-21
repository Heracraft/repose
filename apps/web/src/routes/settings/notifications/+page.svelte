<script lang="ts">
	// Settings → Notifications (DECISIONS I-332, I-691, I-692, I-841): the
	// three channels and Send test. The email toggle saves the moment it
	// changes; the ntfy URL and the Google Chat webhook are typed, so each
	// has its own Save, and a link followed while one is unsaved asks first.
	import { onMount, tick } from 'svelte';
	import { beforeNavigate } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { toast } from 'svelte-sonner';
	import { leaveAnyway, leavingAnyway } from '$lib/leave';
	import { getMe, patchMe, notifyTest } from '$lib/api/client';
	import { ApiError } from '$lib/api/errors';
	import { toastApiError } from '$lib/api/toast';
	import { billingStore } from '$lib/billing.svelte';
	import { dateOnly } from '$lib/format';
	import { title } from '$lib/title';
	import { testReport, type TestReport } from '$lib/notifyTest';
	import LoadState, { loadErrorText } from '$lib/components/LoadState.svelte';
	import type { Me } from '$lib/api/types';

	let me = $state<Me | undefined>(undefined);
	let loadError = $state<string | undefined>(undefined);
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
	/** The last test's outcome, under Send test until the next one. */
	let report = $state<TestReport | undefined>(undefined);
	// A link followed while a field was unsaved: held here, and the page
	// asks about it in place instead of in a native confirm() box.
	let heldNavigation = $state<URL | undefined>(undefined);
	let stayButton = $state<HTMLButtonElement | undefined>(undefined);
	// What had focus when the navigation was held (the link followed), so
	// Stay can give it back instead of dropping it to <body> (I-393).
	let heldFrom: HTMLElement | undefined;

	let ntfyDirty = $derived(ntfyUrl.trim() !== savedNtfyUrl);
	let gchatDirty = $derived(gchatUrl.trim() !== '');
	let gchat = $derived(me?.notify?.gchat ?? null);

	async function load() {
		try {
			me = await getMe();
			billingStore.setMe(me);
			loadError = undefined;
			emailOn = me.notify?.email ?? true;
			ntfyUrl = me.notify?.ntfy_url ?? '';
			savedNtfyUrl = ntfyUrl;
			gchatUrl = '';
		} catch (err) {
			loadError = loadErrorText(err, 'Could not load notifications.');
		}
	}

	onMount(load);

	beforeNavigate(({ cancel, type, to }) => {
		if ((!ntfyDirty && !gchatDirty) || leavingAnyway()) return;
		// A tab close or reload gets the browser's own prompt from cancel().
		// A link inside the dashboard is held, and the page asks beside the
		// field that is unsaved.
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

	async function sendTest() {
		testing = true;
		report = undefined;
		try {
			const result = await notifyTest();
			report = testReport(result);
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
	<title>{title('Notifications · Settings')}</title>
</svelte:head>

{#snippet ask(what: string)}
	<div class="hk-banner ask" role="alert">
		<p>{what} is not saved. Leave this page anyway?</p>
		<div class="ask-actions">
			<button type="button" class="hk-btn-secondary hk-btn-sm" bind:this={stayButton} onclick={stay}
				>Stay</button
			>
			<button type="button" class="hk-btn-ghost hk-btn-sm" onclick={leave}>Leave</button>
		</div>
	</div>
{/snippet}

<LoadState
	status={me ? 'ready' : loadError ? 'failed' : 'loading'}
	error={loadError}
	onretry={load}
	shape="rows"
>
	{#if me}
		<section class="set-row" id="email">
			<div class="set-head">
				<h2 id="email-h" class="hk-h2-app">Email</h2>
				<p class="set-note">To {me.email}</p>
			</div>
			<div class="set-body">
				<label class="hk-check">
					<input type="checkbox" bind:checked={emailOn} onchange={saveEmail} />
					Email notifications
				</label>
			</div>
		</section>

		<section class="set-row" id="ntfy">
			<div class="set-head">
				<h2 id="ntfy-h" class="hk-h2-app">ntfy</h2>
				<p class="set-note">
					<!-- eslint-disable svelte/no-navigation-without-resolve -- resolve() with a fragment appended -->
					<a
						class="hk-link hk-tap"
						href={resolve('/docs/[slug]', { slug: 'notifications' }) + '#your-phone-with-ntfy'}
						>Set up a topic</a
					>
					<!-- eslint-enable svelte/no-navigation-without-resolve -->
				</p>
			</div>
			<form class="set-body" onsubmit={saveNtfy}>
				<label for="ntfy-url" class="hk-field-label">ntfy URL</label>
				<div class="field-row">
					<input
						id="ntfy-url"
						class="hk-input mono"
						placeholder="https://ntfy.sh/your-topic"
						autocomplete="off"
						spellcheck="false"
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
					{@render ask('The ntfy URL')}
				{:else if ntfyDirty}
					<p class="field-note">Not saved yet.</p>
				{/if}
			</form>
		</section>

		<section class="set-row" id="gchat">
			<div class="set-head">
				<h2 id="gchat-h" class="hk-h2-app">Google Chat</h2>
				{#if gchat}
					<p class="set-note">Space <span class="hk-mono">{gchat.space}</span></p>
				{:else}
					<p class="set-note">
						<!-- eslint-disable svelte/no-navigation-without-resolve -- resolve() with a fragment appended -->
						<a
							class="hk-link hk-tap"
							href={resolve('/docs/[slug]', { slug: 'notifications' }) + '#google-chat'}
							>Make a webhook</a
						>
						<!-- eslint-enable svelte/no-navigation-without-resolve -->
					</p>
				{/if}
			</div>
			<form class="set-body" onsubmit={saveGChat}>
				<div class="label-row">
					<label for="gchat-url" class="hk-field-label">Google Chat webhook</label>
					{#if gchat?.broken_at}
						<span class="hk-badge hk-badge--warn">broken since {dateOnly(gchat.broken_at)}</span>
					{/if}
					{#if gchat}
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
						placeholder={gchat ? 'New webhook URL' : 'https://chat.googleapis.com/v1/spaces/...'}
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
					{@render ask('The Google Chat webhook')}
				{:else if gchatDirty}
					<p class="field-note">Not saved yet.</p>
				{/if}
			</form>
		</section>

		<section class="set-row" id="test">
			<div class="set-head">
				<h2 id="test-h" class="hk-h2-app">Test</h2>
				<p class="set-note">One notification to each channel that is on.</p>
			</div>
			<div class="set-body">
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
					<p class="field-note">Test not available yet</p>
				{/if}
				<!-- Mounted while the test is available, so the outcome is
				     announced when it arrives. -->
				<div role="status" class="report-slot">
					{#if report}
						<div
							class="hk-banner report"
							class:hk-banner--ok={report.tone === 'ok'}
							class:hk-banner--err={report.tone === 'failed'}
							data-testid="test-report"
						>
							<p>{report.text}</p>
						</div>
					{/if}
				</div>
			</div>
		</section>
	{/if}
</LoadState>

<style>
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
	/* Save stands as tall as the 36px field beside it; on touch both are
	   44px already (review r1 R1-A3). */
	@media (min-width: 640px) and (not (pointer: coarse)) {
		.field-row button {
			height: 2.25rem;
		}
	}
	.label-row {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.25rem 0.75rem;
		margin-bottom: 0.375rem;
		min-height: 1.5rem;
	}
	.label-row .hk-field-label {
		margin-bottom: 0;
	}
	.remove {
		margin-block: -0.5rem;
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
	.report {
		margin: 0.75rem 0 0;
	}
	.report p {
		overflow-wrap: anywhere;
	}
</style>
