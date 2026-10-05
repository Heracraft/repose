<script lang="ts">
	import { onMount } from 'svelte';
	import { toast } from 'svelte-sonner';
	import { getMe, deleteMe } from '$lib/api/client';
	import { toastApiError } from '$lib/api/toast';
	import { signOut } from '$lib/auth.svelte';
	import PageShell from '$lib/components/PageShell.svelte';
	import ConfirmType from '$lib/components/ConfirmType.svelte';
	import MachineNix from '$lib/components/MachineNix.svelte';
	import LoadState, { loadErrorText } from '$lib/components/LoadState.svelte';
	import type { Me } from '$lib/api/types';

	let me = $state<Me | undefined>(undefined);
	let deleting = $state(false);
	let loadError = $state<string | undefined>(undefined);

	// loadError is cleared only by a load that worked: cleared first, it
	// flipped LoadState back to "Loading…" mid-retry, which unmounted the
	// Retry button under the click and dropped focus to <body>.
	async function load() {
		try {
			me = await getMe();
			loadError = undefined;
		} catch (err) {
			loadError = loadErrorText(err, 'Could not load your account.');
		}
	}

	onMount(load);

	async function onDelete() {
		deleting = true;
		try {
			await deleteMe();
			toast.success('Account cancellation started. Everything is deleted in 30 days.');
			await signOut();
		} catch (err) {
			deleting = false;
			toastApiError(err, 'Could not start account deletion.');
		}
	}
</script>

<svelte:head>
	<title>Account — repose</title>
</svelte:head>

<PageShell title="Account" width="form">
	<LoadState
		status={me ? 'ready' : loadError ? 'failed' : 'loading'}
		error={loadError}
		onretry={load}
	>
		{#if me}
			<dl class="space-y-2 text-sm">
				<div class="flex justify-between">
					<dt class="text-ink-muted">Handle</dt>
					<dd class="font-mono">{me.handle}</dd>
				</div>
				<div class="flex justify-between">
					<dt class="text-ink-muted">Email</dt>
					<dd>{me.email}</dd>
				</div>
				<div class="flex justify-between">
					<dt class="text-ink-muted">GitHub</dt>
					<dd class={me.github_login ? '' : 'text-ink-muted'}>
						{me.github_login ?? 'Not linked'}
					</dd>
				</div>
			</dl>

			<button type="button" class="btn-ghost mt-4 -ml-2" onclick={() => signOut()}>Sign out</button>

			<MachineNix />

			<div class="form-section">
				<h2 class="text-xl font-semibold text-red-700 dark:text-red-400">Delete account</h2>
				<p class="mt-1 text-sm text-ink-muted">
					Stops every environment at once. Everything, including snapshots, is deleted 30 days
					later.
				</p>
				<div class="mt-3">
					<ConfirmType
						word={me.handle}
						label="Delete account"
						disabled={deleting}
						onconfirm={onDelete}
					/>
				</div>
			</div>
		{/if}
	</LoadState>
</PageShell>
