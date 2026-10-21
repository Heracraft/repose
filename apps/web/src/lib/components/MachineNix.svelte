<script lang="ts">
	// The account's machine.nix (DECISIONS I-490): a home-manager module every
	// machine of the account gets beside its project's configuration. The
	// save names the revision the editor started from, so a copy pushed from
	// the laptop since is never overwritten here without asking.
	import { onMount, tick } from 'svelte';
	import { beforeNavigate } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { leaveAnyway, leavingAnyway } from '$lib/leave';
	import { getPersonal, putPersonal } from '$lib/api/client';
	import { ApiError } from '$lib/api/errors';
	import { toastApiError } from '$lib/api/toast';
	import { dateTime } from '$lib/format';
	import { machineNixLine, rebuildSummary } from '$lib/personal';
	import NixEditor from '$lib/components/NixEditor.svelte';
	import LoadState, { loadErrorText } from '$lib/components/LoadState.svelte';
	import type { PersonalConfig } from '$lib/api/types';

	let personal = $state<PersonalConfig | undefined>(undefined);
	let loadError = $state<string | undefined>(undefined);
	let text = $state('');
	let saving = $state(false);
	let saved = $state<string | undefined>(undefined);
	let error = $state<string | undefined>(undefined);
	let errorLine = $state<number | undefined>(undefined);
	/** The account's copy changed since the editor loaded it. */
	let conflict = $state(false);
	let dirty = $derived(personal !== undefined && text !== personal.fragment);

	let heldNavigation = $state<URL | undefined>(undefined);
	let stayButton = $state<HTMLButtonElement | undefined>(undefined);

	async function load() {
		try {
			personal = await getPersonal();
			text = personal.fragment;
			loadError = undefined;
			conflict = false;
		} catch (err) {
			loadError = loadErrorText(err, 'Could not load machine.nix.');
		}
	}

	onMount(load);

	beforeNavigate(({ cancel, type, to }) => {
		if (!dirty || leavingAnyway()) return;
		cancel();
		if (type === 'leave' || !to) return;
		heldNavigation = to.url;
		void tick().then(() => stayButton?.focus());
	});

	async function leave() {
		const url = heldNavigation;
		heldNavigation = undefined;
		if (!url) return;
		await leaveAnyway(url);
	}

	async function save() {
		if (!personal) return;
		saving = true;
		saved = error = undefined;
		errorLine = undefined;
		try {
			const res = await putPersonal(text, personal.revision_id);
			personal = res;
			text = res.fragment;
			if (res.unchanged) {
				saved = 'Nothing changed.';
			} else if (res.fragment === '') {
				saved = `Removed. ${rebuildSummary(res.projects)}`.trim();
			} else {
				saved = `Saved. ${rebuildSummary(res.projects)}`.trim();
			}
		} catch (err) {
			if (err instanceof ApiError && err.code === 'conflict') {
				conflict = true;
			} else if (err instanceof ApiError && err.code === 'invalid') {
				error = err.message;
				const line = err.detail?.personal_line;
				errorLine = typeof line === 'number' ? line : machineNixLine(err.message);
			} else {
				toastApiError(err, 'Could not save machine.nix.');
			}
		} finally {
			saving = false;
		}
	}
</script>

<!-- Settings → machine.nix (I-841): the head with How it works, the
     editor in the full column with its state and Save in its foot bar,
     the projects that keep it off under it. -->
<section class="hk-section" id="machine-nix" aria-labelledby="machine-nix-h">
	<div class="hk-section-head">
		<h2 id="machine-nix-h" class="hk-h2-app">machine.nix</h2>
	</div>
	<p class="lede">
		Your laptop keeps it at <code class="path">~/.config/repose/machine.nix</code>.
		<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -- resolve() with a fragment appended -->
		<a class="hk-link how" href={resolve('/docs/[slug]', { slug: 'config' }) + '#your-machine-nix'}
			>How it works</a
		>
	</p>
	<LoadState
		status={personal ? 'ready' : loadError ? 'failed' : 'loading'}
		error={loadError}
		onretry={load}
		shape="rows"
	>
		{#if personal}
			<div class="editor">
				<NixEditor bind:value={text} {errorLine} label="machine.nix" floor="var(--mn-floor)" />
			</div>
			<div class="foot">
				<!-- The state, said once as it changes, and the revision the
				     account holds; Save on the right, the page's one primary,
				     waiting until there is a change. -->
				<span class="left">
					<span role="status" class="state">
						{#if dirty}
							<span class="dirty">Unsaved changes</span>
						{:else if saved}
							{saved}
						{:else if personal?.created_at}
							Saved
						{:else}
							You have none yet.
						{/if}
					</span>
					{#if personal?.created_at}
						<span class="when">
							{dateTime(personal.created_at)}
							{personal.source === 'cli' ? 'from your laptop' : 'on the dashboard'}
						</span>
					{/if}
				</span>
				<button
					type="button"
					class="hk-btn"
					aria-disabled={!dirty || conflict || saving ? 'true' : undefined}
					onclick={() => dirty && !conflict && !saving && save()}
				>
					{saving ? 'Saving…' : 'Save'}
				</button>
			</div>
			{#if conflict}
				<div class="hk-banner after" role="alert">
					<p>machine.nix changed after this page loaded. Loading it drops your changes here.</p>
					<button type="button" class="hk-banner-action" onclick={load}
						>Load the account's copy</button
					>
				</div>
			{/if}
			{#if error}
				<div class="hk-banner hk-banner--err after" role="alert">
					<p class="build-error">{error}</p>
				</div>
			{/if}
			{#if heldNavigation}
				<div class="hk-banner after" role="alert">
					<p>machine.nix is not saved. Leave this page anyway?</p>
					<div class="ask">
						<button
							type="button"
							class="hk-btn-secondary hk-btn-sm"
							bind:this={stayButton}
							onclick={() => (heldNavigation = undefined)}>Stay</button
						>
						<button type="button" class="hk-btn-ghost hk-btn-sm" onclick={leave}>Leave</button>
					</div>
				</div>
			{/if}
			{#if personal.opted_out && personal.opted_out.length > 0}
				<p class="off">Off on {personal.opted_out.join(', ')}.</p>
			{/if}
		{/if}
	</LoadState>
</section>

<style>
	.how {
		margin-left: 0.25rem;
	}
	.lede {
		margin: -0.25rem 0 1.25rem;
		font-size: 0.875rem;
		color: var(--hk-text-3);
		text-wrap: pretty;
	}
	.path {
		white-space: nowrap;
	}
	/* The editor is the page's subject: room to write in. */
	.editor {
		--mn-floor: 20rem;
	}
	@container dash (min-width: 640px) {
		.editor {
			--mn-floor: 28rem;
		}
	}
	.off {
		margin: 1rem 0 0;
		font-size: 0.8125rem;
		color: var(--hk-text-3);
	}
	.left {
		display: flex;
		flex-wrap: wrap;
		align-items: baseline;
		gap: 0 0.75rem;
		min-width: 0;
		flex: 1 1 0;
	}
	.foot {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		justify-content: space-between;
		gap: 0.5rem 1rem;
		margin-top: 0.75rem;
		font-size: 0.8125rem;
		color: var(--hk-text-3);
	}
	.dirty {
		color: var(--hk-text);
	}
	.when {
		font-variant-numeric: tabular-nums;
	}
	.after {
		margin: 0.75rem 0 0;
		justify-content: space-between;
	}
	.build-error {
		font-family: var(--hk-font-mono);
		font-size: 0.8125rem;
		white-space: pre-wrap;
	}
	.ask {
		display: flex;
		align-items: center;
		gap: 0.5rem;
	}
	/* A thumb's 44px for the lone link in the head. */
	@media (pointer: coarse), (max-width: 639.98px) {
		.how {
			display: inline-flex;
			align-items: center;
			min-height: 2.75rem;
			margin-block: -0.6875rem;
		}
	}
</style>
