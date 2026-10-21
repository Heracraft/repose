<script lang="ts">
	// A project's Snapshots tab (DECISIONS I-841): the list, Create
	// snapshot, and Restore… with both outcomes in one panel under its row.
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { toast } from 'svelte-sonner';
	import { createSnapshot, listSnapshots, restoreSnapshot } from '$lib/api/client';
	import { ApiError } from '$lib/api/errors';
	import { PollFailure, toastApiError } from '$lib/api/toast';
	import { pollWhileVisible } from '$lib/poll';
	import LoadState, { loadErrorText } from '$lib/components/LoadState.svelte';
	import SnapshotsTable from '$lib/components/project/SnapshotsTable.svelte';
	import { useProject } from '$lib/components/project/context.svelte';
	import type { Snapshot } from '$lib/api/types';

	const ctx = useProject();
	const id = ctx.id;

	let snapshots = $state<Snapshot[] | undefined>(undefined);
	let loadError = $state<string | undefined>(undefined);
	const failure = new PollFailure('Could not load snapshots.', ctx.pollFailures);

	async function refresh() {
		if (ctx.notFound) return;
		try {
			snapshots = await listSnapshots(id);
			loadError = undefined;
			failure.ok();
		} catch (err) {
			// Before the first answer the banner says why; after it, a toast.
			if (snapshots === undefined) loadError = loadErrorText(err, 'Could not load snapshots.');
			failure.fail(err, snapshots === undefined);
		}
	}

	onMount(() => pollWhileVisible(refresh));

	async function onCreate() {
		ctx.opBusy = 'snapshot';
		try {
			const { op_id } = await createSnapshot(id);
			void ctx.waitForOp(op_id, () => {
				ctx.opBusy = undefined;
				void refresh();
			});
		} catch (err) {
			ctx.opBusy = undefined;
			toastApiError(err, 'Could not take a snapshot.');
		}
	}

	/**
	 * Restores a snapshot over this project's disk, or into a new project
	 * when asNew names one. true once done; a string is why the api
	 * refused the new name.
	 */
	async function onRestore(snapshotId: string, asNew?: string): Promise<boolean | string> {
		ctx.opBusy = 'restore';
		try {
			const { op_id, project_id } = await restoreSnapshot(id, snapshotId, asNew);
			// An as-new restore's op belongs to the project it created, so it
			// is polled there.
			return await ctx.waitForOp(
				op_id,
				() => {
					ctx.opBusy = undefined;
					toast.success(
						asNew
							? `Created ${asNew} from the snapshot.`
							: `Restored ${ctx.project?.name ?? 'the project'}.`
					);
					// An as-new restore made a project elsewhere: go to it.
					if (asNew && project_id) {
						void goto(resolve('/projects/[id]', { id: project_id }));
						return;
					}
					void ctx.refresh();
					void refresh();
				},
				asNew && project_id ? project_id : id
			);
		} catch (err) {
			ctx.opBusy = undefined;
			// A refused name belongs under the field.
			if (asNew && err instanceof ApiError && (err.code === 'conflict' || err.code === 'invalid')) {
				return err.detail?.reason === 'name_taken'
					? `A project called ${asNew} already exists; pick another name.`
					: err.message;
			}
			toastApiError(err, 'Could not restore the snapshot.');
			return false;
		}
	}
</script>

{#if ctx.project && snapshots}
	<SnapshotsTable
		project={ctx.project}
		{snapshots}
		busy={!!ctx.opBusy}
		creating={ctx.opBusy === 'snapshot'}
		restoring={ctx.opBusy === 'restore'}
		oncreate={onCreate}
		onrestore={onRestore}
	/>
{:else}
	<div class="min-h-screen">
		<LoadState status={loadError ? 'failed' : 'loading'} error={loadError} onretry={refresh} />
	</div>
{/if}
