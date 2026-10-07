<script lang="ts">
	// The account's machine.nix (DECISIONS I-490): a home-manager module every
	// machine of the account gets beside its project's configuration. The
	// save names the revision the editor started from, so a copy pushed from
	// the laptop since is never overwritten here without asking.
	import { onMount, tick } from 'svelte';
	import { beforeNavigate } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { toast } from 'svelte-sonner';
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
			toast.success('machine.nix saved.');
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

<div class="form-section" id="machine-nix">
	<h2 class="text-xl font-semibold">machine.nix</h2>
	<p class="mt-1 text-sm text-ink-muted">
		A home-manager module every machine of your account gets, beside each project's own
		configuration: packages, shell aliases, prompt, dotfiles. Your laptop keeps a copy at
		<code class="font-mono">~/.config/repose/machine.nix</code>.
		<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -- resolve() with a fragment appended -->
		<a class="link" href={resolve('/docs/[slug]', { slug: 'config' }) + '#your-machine-nix'}
			>How it works</a
		>
	</p>
	<LoadState
		status={personal ? 'ready' : loadError ? 'failed' : 'loading'}
		error={loadError}
		onretry={load}
	>
		{#if personal}
			{#if personal.created_at}
				<p class="mt-2 text-sm text-ink-muted">
					Saved {dateTime(personal.created_at)}
					{personal.source === 'cli' ? 'from your laptop' : 'on the dashboard'}.
				</p>
			{:else}
				<p class="mt-2 text-sm text-ink-muted">You have none yet.</p>
			{/if}
			{#if personal.opted_out && personal.opted_out.length > 0}
				<p class="mt-1 text-sm text-ink-muted">Off on {personal.opted_out.join(', ')}.</p>
			{/if}
			<div class="mt-3">
				<NixEditor bind:value={text} {errorLine} label="machine.nix" />
			</div>
			{#if conflict}
				<div class="banner banner--warn mt-3" role="alert">
					<p>
						machine.nix on your account changed after this page loaded, probably pushed from your
						laptop. Load that copy to edit it; your changes here are dropped.
					</p>
					<button type="button" class="btn-ghost mt-2 -ml-2" onclick={load}
						>Load the account's copy</button
					>
				</div>
			{/if}
			{#if error}
				<div class="banner banner--error mt-3" role="alert">
					<p class="font-mono text-compact whitespace-pre-wrap">{error}</p>
				</div>
			{/if}
			{#if heldNavigation}
				<div class="banner banner--warn mt-3" role="alert">
					<p>machine.nix is not saved.</p>
					<div class="mt-2 flex gap-2">
						<button
							type="button"
							class="btn-ghost -ml-2"
							bind:this={stayButton}
							onclick={() => (heldNavigation = undefined)}>Stay</button
						>
						<button type="button" class="btn-ghost" onclick={leave}>Leave anyway</button>
					</div>
				</div>
			{/if}
			<div class="mt-3 flex flex-wrap items-center gap-x-3 gap-y-1">
				<button type="button" class="btn" disabled={saving || !dirty || conflict} onclick={save}>
					{saving ? 'Saving…' : 'Save'}
				</button>
				{#if dirty}
					<span class="text-sm text-ink-muted">Not saved yet.</span>
				{:else if saved}
					<span class="text-sm text-ink-muted" role="status">{saved}</span>
				{/if}
			</div>
		{/if}
	</LoadState>
</div>
