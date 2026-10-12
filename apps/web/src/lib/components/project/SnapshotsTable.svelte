<!--
  A project's snapshots, newest first: when each was taken, its size, why
  (stop, a schedule, by hand), and when it expires, amber under three
  days. Each restores over this disk, behind the same typed confirm as
  Destroy, or into a new project under a name. One snapshot at a time has
  its panel open, under its row; the panel takes focus and Cancel gives it
  back to the button that opened it. In a wide card the rows line up as a
  table under a header; below 32rem each row is two lines. The newest 8
  show, and the rest wait behind one toggle, as the config page's
  revisions do (I-680).
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
	const SNAPSHOTS_SHOWN = 8;

	let all = $state(false);

	let panel = $state<{ snapshotId: string; kind: 'replace' | 'new' } | undefined>(undefined);
	let newName = $state('');
	let newError = $state<string | undefined>(undefined);
	// The row whose panel is open stays, even past the first 8.
	let shown = $derived(
		all ? snapshots : snapshots.filter((s, i) => i < SNAPSHOTS_SHOWN || s.id === panel?.snapshotId)
	);

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
	<div class="flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
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
				{#each shown as s (s.id)}
					{@const which = panel?.snapshotId === s.id ? panel.kind : undefined}
					{@const exp = expiry(s)}
					<li class="snap border-t py-2.5 text-sm border-rule" data-testid="snapshot-row">
						<div class="cells">
							<span class="taken whitespace-nowrap tabular-nums">{dateTime(s.created_at)}</span>
							<span class="size whitespace-nowrap text-ink-muted tabular-nums">{gb(s.bytes)}</span>
							<span class="reason"><span class="badge">{s.reason}</span></span>
							<span class="line2">
								<span
									class="expires text-xs tabular-nums {exp.soon
										? 'text-amber-800 dark:text-amber-300'
										: 'text-ink-muted'}"
									title={s.expires_at ? dateTime(s.expires_at) : undefined}
									>{#if s.expires_at}<span class="@lg:sr-only">expires&nbsp;</span
										>{/if}{exp.text}</span
								>
								{#if !which}
									<!-- The negative right margin takes back the ghost buttons'
									     own padding, so their words line up with the card's edge. -->
									<span class="actions flex flex-wrap items-center whitespace-nowrap">
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
							</span>
						</div>
						{#if which === 'replace'}
							<!-- From 40rem the warning sits beside the form, as in Destroy;
							     narrower, the form goes under it. -->
							<div
								class="mt-3 @container rounded-sm border p-4 border-rule"
								data-testid="restore-confirm"
							>
								<div class="split">
									<div class="min-w-0">
										<p>
											Restoring replaces the disk of {project.slug} with this snapshot. Anything written
											since <span class="whitespace-nowrap">{dateTime(s.created_at)}</span> is lost.
										</p>
										{#if project.state !== 'stopped'}
											<p class="mt-2 text-ink-muted">
												Stop {project.slug} first, or restore the snapshot as a new project.
											</p>
										{/if}
									</div>
									<div class="form min-w-0">
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
			{#if snapshots.length > SNAPSHOTS_SHOWN}
				<button
					type="button"
					class="btn-ghost mt-1 -ml-2"
					aria-expanded={all}
					data-testid="snapshots-toggle"
					onclick={() => (all = !all)}>{all ? 'Show fewer' : `Show all ${snapshots.length}`}</button
				>
			{/if}
		</div>
	{/if}
</section>

<style>
	/* Narrow: the time, size and reason on the first line; the expiry
	   and the actions on the second, the actions wrapping under the
	   expiry whole when the line is too short. */
	.cells {
		display: grid;
		grid-template-columns: auto auto minmax(0, 1fr);
		grid-template-areas:
			'taken size reason'
			'line2 line2 line2';
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
		min-width: 0;
	}
	.line2 {
		grid-area: line2;
		display: flex;
		flex-wrap: wrap;
		align-items: baseline;
		column-gap: 0.75rem;
	}
	.expires {
		grid-area: expires;
	}
	.actions {
		grid-area: actions;
		justify-content: flex-end;
		margin-left: auto;
		margin-right: -0.5rem;
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
		.line2 {
			display: contents;
		}
		.snap:first-child {
			border-color: var(--rule-strong);
		}
	}
	/* The restore panel: stacked, then from 40rem the warning on the left
	   and the form in a 24rem column on the right (Destroy's split). */
	.split {
		display: flex;
		flex-direction: column;
		gap: 0.75rem;
	}
	@container (width >= 40rem) {
		.split {
			flex-direction: row;
			align-items: flex-start;
			justify-content: space-between;
			gap: 2rem;
		}
		.split .form {
			flex: 0 0 24rem;
		}
	}
</style>
