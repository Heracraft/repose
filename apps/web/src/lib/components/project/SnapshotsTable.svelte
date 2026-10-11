<!--
  A project's snapshots, newest first: when each was taken, its size, why
  (stop, a schedule, by hand), and when it expires, amber under three
  days. Each restores over this disk, behind the same typed confirm as
  Destroy, or into a new project under a name. One snapshot at a time has
  its panel open, under its row; the panel takes focus and Cancel gives it
  back to the button that opened it. In a wide card the rows line up as a
  table under a header; in the side column each row is two lines.
-->
<script lang="ts">
	import ConfirmType from '$lib/components/ConfirmType.svelte';
	import RestoreNameForm from '$lib/components/RestoreNameForm.svelte';
	import { focusAfterRender } from '$lib/focus';
	import { dateTime, gb, timeUntil } from '$lib/format';
	import type { Project, Snapshot } from '$lib/api/types';

	let {
		project,
		snapshots,
		busy = false,
		creating = false,
		restoring = false,
		oncreate,
		onrestore
	}: {
		project: Project;
		snapshots: Snapshot[];
		/** Another operation runs: every action waits. */
		busy?: boolean;
		creating?: boolean;
		restoring?: boolean;
		oncreate: () => void;
		/** true once restored; a string is why the api refused the new name; false otherwise. */
		onrestore: (snapshotId: string, asNew?: string) => Promise<boolean | string>;
	} = $props();

	const THREE_DAYS = 3 * 86_400_000;

	let panel = $state<{ snapshotId: string; kind: 'replace' | 'new' } | undefined>(undefined);
	let newName = $state('');
	let newError = $state<string | undefined>(undefined);

	function open(snapshotId: string, kind: 'replace' | 'new') {
		panel = { snapshotId, kind };
		newName = '';
		newError = undefined;
	}

	/** Close a restore panel and give focus back to the button that opened it. */
	function close() {
		const was = panel;
		panel = undefined;
		if (was) void focusAfterRender(`restore-${was.kind}-${was.snapshotId}`);
	}

	async function restore(snapshotId: string, asNew?: string) {
		newError = undefined;
		const got = await onrestore(snapshotId, asNew);
		if (got === true) {
			panel = undefined;
			newName = '';
		} else if (typeof got === 'string') {
			newError = got;
		}
	}

	function expiry(s: Snapshot): { text: string; soon: boolean } {
		if (!s.expires_at) return { text: 'kept', soon: false };
		const left = timeUntil(s.expires_at);
		const soon = new Date(s.expires_at).getTime() - Date.now() < THREE_DAYS;
		return { text: left ? `in ${left}` : 'expired', soon };
	}
</script>

<section class="snapshots card" aria-labelledby="snapshots-title" data-testid="snapshots-card">
	<div class="flex items-center justify-between gap-4">
		<h2 id="snapshots-title" class="text-xl font-semibold">Snapshots</h2>
		<button type="button" class="btn-quiet btn--sm" disabled={busy} onclick={oncreate}>
			{creating ? 'Snapshotting…' : 'Create snapshot'}
		</button>
	</div>
	{#if snapshots.length === 0}
		<p class="mt-3 border-t pt-3 text-sm text-ink-muted border-rule">No snapshots yet.</p>
	{:else}
		<div class="mt-3 @container">
			<div class="head hidden text-xs font-medium text-ink-muted @lg:grid" aria-hidden="true">
				<span>Taken</span><span class="text-right">Size</span><span>Reason</span><span>Expires</span
				><span></span>
			</div>
			<ul>
				{#each snapshots as s (s.id)}
					{@const which = panel?.snapshotId === s.id ? panel.kind : undefined}
					{@const exp = expiry(s)}
					<li class="snap border-t py-2.5 text-sm border-rule" data-testid="snapshot-row">
						<div class="cells">
							<span class="taken tabular-nums">{dateTime(s.created_at)}</span>
							<span class="size text-ink-muted tabular-nums">{gb(s.bytes)}</span>
							<span class="reason"><span class="badge">{s.reason}</span></span>
							<span
								class="expires text-xs tabular-nums {exp.soon
									? 'text-amber-800 dark:text-amber-300'
									: 'text-ink-muted'}"
								title={s.expires_at ? dateTime(s.expires_at) : undefined}
								>{#if s.expires_at}<span class="@lg:sr-only">expires&nbsp;</span
									>{/if}{exp.text}</span
							>
							{#if !which}
								<!-- -mx-2 takes back the ghost buttons' own padding, so their
								     words line up with the card's edge. -->
								<span class="actions -mx-2 flex flex-wrap items-center @lg:justify-end">
									<button
										type="button"
										id={`restore-replace-${s.id}`}
										class="btn-ghost-danger"
										disabled={busy}
										onclick={() => open(s.id, 'replace')}>Restore…</button
									>
									<button
										type="button"
										id={`restore-new-${s.id}`}
										class="btn-ghost"
										disabled={busy}
										onclick={() => open(s.id, 'new')}>Restore as new…</button
									>
								</span>
							{/if}
						</div>
						{#if which === 'replace'}
							<div class="mt-3 rounded-sm border p-4 border-rule" data-testid="restore-confirm">
								<p>
									Restoring replaces the disk of {project.slug} with this snapshot. Anything written since
									{dateTime(s.created_at)} is lost.
								</p>
								{#if project.state !== 'stopped'}
									<p class="mt-2 text-ink-muted">
										Stop {project.slug} first, or restore the snapshot as a new project.
									</p>
								{/if}
								<div class="mt-3">
									<ConfirmType
										word={project.slug}
										label="Restore"
										busyLabel="Restoring…"
										busy={restoring}
										disabled={busy || project.state !== 'stopped'}
										onconfirm={() => restore(s.id)}
										autofocus
										oncancel={close}
									/>
								</div>
							</div>
						{:else if which === 'new'}
							<RestoreNameForm
								id={s.id}
								bind:value={newName}
								error={newError}
								busy={restoring}
								submitLabel="Restore as new"
								onsubmit={(n) => void restore(s.id, n)}
								oncancel={close}
							/>
						{/if}
					</li>
				{/each}
			</ul>
		</div>
	{/if}
</section>

<style>
	/* Narrow: the time, size and reason on the first line with the expiry
	   at its end; the actions under them. */
	.cells {
		display: grid;
		grid-template-columns: auto auto minmax(0, 1fr) auto;
		grid-template-areas:
			'taken size reason expires'
			'actions actions actions actions';
		column-gap: 0.75rem;
		row-gap: 0.25rem;
		align-items: baseline;
	}
	.taken {
		grid-area: taken;
	}
	.size {
		grid-area: size;
	}
	.reason {
		grid-area: reason;
	}
	.expires {
		grid-area: expires;
	}
	.actions {
		grid-area: actions;
	}
	/* Wide (the @lg step, 32rem): one row per snapshot under the header,
	   as a table. */
	@container (width >= 32rem) {
		.head,
		.cells {
			grid-template-columns: 9rem 4rem 5.5rem minmax(0, 1fr) auto;
			column-gap: 0.75rem;
		}
		.head {
			padding-bottom: 0.5rem;
		}
		.cells {
			grid-template-areas: 'taken size reason expires actions';
		}
		.snap:first-child {
			border-color: var(--rule-strong);
		}
	}
</style>
