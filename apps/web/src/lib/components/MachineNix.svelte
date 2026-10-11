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

<!-- One of Settings' two-track rows (its h2 and fact on the left, the
     control on the right from lg), drawn here with the same classes. The
     editor takes the whole right track: it holds code, and a long line
     wraps less in 790px than in 560. -->
<section
	class="form-section lg:grid lg:grid-cols-[280px_minmax(0,1fr)] lg:gap-x-12"
	id="machine-nix"
>
	<div>
		<h2 class="text-xl font-semibold">machine.nix</h2>
		<p class="mt-1 text-sm text-ink-muted">
			A home-manager module every machine of your account gets. Your laptop keeps it at
			<code class="font-mono text-compact whitespace-nowrap">~/.config/repose/machine.nix</code>.
		</p>
		<p class="mt-2 text-sm">
			<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -- resolve() with a fragment appended -->
			<a class="link" href={resolve('/docs/[slug]', { slug: 'config' }) + '#your-machine-nix'}
				>How it works</a
			>
		</p>
	</div>
	<div class="mt-4 min-w-0 lg:mt-0">
		<LoadState
			status={personal ? 'ready' : loadError ? 'failed' : 'loading'}
			error={loadError}
			onretry={load}
			shape="rows"
		>
			{#if personal}
				{#if personal.opted_out && personal.opted_out.length > 0}
					<p class="mb-3 text-sm text-ink-muted">Off on {personal.opted_out.join(', ')}.</p>
				{/if}
				<NixEditor bind:value={text} {errorLine} label="machine.nix">
					{#snippet footer()}
						<!-- The state on the left, said once as it changes; the
						     revision the account holds and Save on the right. -->
						<span role="status">
							{#if dirty}
								<span class="text-ink">Unsaved changes</span>
							{:else if saved}
								{saved}
							{:else if personal?.created_at}
								Saved
							{:else}
								You have none yet.
							{/if}
						</span>
						<span class="flex flex-wrap items-center gap-x-3 gap-y-1">
							{#if personal?.created_at}
								<span class="tabular-nums">
									{dateTime(personal.created_at)}
									{personal.source === 'cli' ? 'from your laptop' : 'on the dashboard'}
								</span>
							{/if}
							<button
								type="button"
								class="btn-quiet btn--sm"
								disabled={saving || !dirty || conflict}
								onclick={save}
							>
								{saving ? 'Saving…' : 'Save'}
							</button>
						</span>
					{/snippet}
				</NixEditor>
				{#if conflict}
					<div class="banner banner--warn mt-3 mb-0" role="alert">
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
					<div class="banner banner--error mt-3 mb-0" role="alert">
						<p class="font-mono text-compact whitespace-pre-wrap">{error}</p>
					</div>
				{/if}
				{#if heldNavigation}
					<div class="banner banner--warn mt-3 mb-0" role="alert">
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
			{/if}
		</LoadState>
	</div>
</section>
