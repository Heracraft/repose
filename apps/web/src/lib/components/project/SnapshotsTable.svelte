<!--
  A project's snapshots, newest first (I-837): when each was taken, its
  size, why (stop, a schedule, by hand) and when it expires. One action a
  row, Restore…, which opens one panel under that row offering both
  outcomes: over this disk, behind the same typed confirm as Destroy, or
  as a new project under a name, which then opens. The panel takes focus
  and Cancel gives it back to the row's Restore…. The newest 8 show, and
  the rest wait behind one toggle (I-680). Below 640px each row is two
  lines.
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

	const SNAPSHOTS_SHOWN = 8;

	let all = $state(false);
	/** The snapshot whose restore panel is open. */
	let openId = $state<string | undefined>(undefined);
	let newName = $state('');
	let newError = $state<string | undefined>(undefined);
	// The row whose panel is open stays, even past the first 8.
	let shown = $derived(
		all ? snapshots : snapshots.filter((s, i) => i < SNAPSHOTS_SHOWN || s.id === openId)
	);

	function open(snapshotId: string) {
		openId = snapshotId;
		newName = '';
		newError = undefined;
	}

	/** Close the restore panel and give focus back to the row's Restore…. */
	function close() {
		const was = openId;
		openId = undefined;
		if (was) void focusAfterRender(`restore-${was}`);
	}

	/** Which of the panel's two outcomes is under way. */
	let kind = $state<'over' | 'new' | undefined>(undefined);

	async function restore(snapshotId: string, asNew?: string) {
		newError = undefined;
		kind = asNew ? 'new' : 'over';
		const got = await onrestore(snapshotId, asNew);
		if (got === true) {
			openId = undefined;
			newName = '';
		} else if (typeof got === 'string') {
			newError = got;
		}
	}

	function expiry(s: Snapshot): string {
		if (!s.expires_at) return 'kept';
		const left = timeUntil(s.expires_at);
		return left ? `in ${left}` : 'expired';
	}
</script>

{#snippet create()}
	<button
		type="button"
		class="hk-btn-secondary hk-btn-sm"
		aria-disabled={busy ? 'true' : undefined}
		onclick={() => !busy && oncreate()}
	>
		{creating ? 'Snapshotting…' : 'Create snapshot'}
	</button>
{/snippet}

<section class="hk-section" aria-labelledby="snapshots-title" data-testid="snapshots-card">
	<div class="hk-section-head">
		<h2 id="snapshots-title" class="hk-h2-app">Snapshots</h2>
		{#if snapshots.length > 0}{@render create()}{/if}
	</div>
	{#if snapshots.length === 0}
		<div class="hk-empty-row">
			<span>No snapshots yet.</span>
			{@render create()}
		</div>
	{:else}
		<table class="hk-table snaps">
			<thead>
				<tr>
					<th scope="col">Taken</th>
					<th scope="col" class="hk-num-col hidden sm:table-cell">Size</th>
					<th scope="col" class="hidden sm:table-cell">Reason</th>
					<th scope="col" class="hidden sm:table-cell">Expires</th>
					<th scope="col" class="act"><span class="hk-sr">Restore</span></th>
				</tr>
			</thead>
			{#each shown as s (s.id)}
				{@const isOpen = openId === s.id}
				<tbody data-testid="snapshot-row">
					<tr class:open={isOpen}>
						<td class="taken">
							<span class="num">{dateTime(s.created_at)}</span>
							<!-- Below 640px the other columns go under the time. -->
							<span class="sub block sm:hidden"
								>{gb(s.bytes)} · {s.reason} ·
								<span title={s.expires_at ? dateTime(s.expires_at) : undefined}
									>{s.expires_at ? `expires ${expiry(s)}` : expiry(s)}</span
								></span
							>
						</td>
						<td class="hk-num-col num hidden whitespace-nowrap sm:table-cell">{gb(s.bytes)}</td>
						<td class="muted hidden sm:table-cell">{s.reason}</td>
						<td class="muted num hidden whitespace-nowrap sm:table-cell">
							<span title={s.expires_at ? dateTime(s.expires_at) : undefined}>{expiry(s)}</span>
						</td>
						<td class="act">
							{#if !isOpen}
								<button
									type="button"
									id={`restore-${s.id}`}
									class="hk-btn-ghost hk-btn-sm"
									disabled={busy}
									onclick={() => open(s.id)}>Restore…</button
								>
							{/if}
						</td>
					</tr>
					{#if isOpen}
						<tr>
							<td colspan="5" class="panel-cell">
								<div class="panel" data-testid="restore-confirm">
									<p class="panel-say">
										Restore this snapshot over {project.slug}, or as a new project. Anything written
										since <span class="whitespace-nowrap">{dateTime(s.created_at)}</span>
										is lost on {project.slug}.
									</p>
									{#if project.state !== 'stopped'}
										<p class="panel-note">Stop {project.slug} first to restore over it.</p>
									{/if}
									<ConfirmType
										word={project.slug}
										label="Restore"
										busyLabel="Restoring…"
										busy={restoring && kind === 'over'}
										disabled={busy || project.state !== 'stopped'}
										onconfirm={() => restore(s.id)}
										autofocus
									/>
									<div class="panel-rule"></div>
									<RestoreNameForm
										id={s.id}
										bind:value={newName}
										error={newError}
										busy={restoring && kind === 'new'}
										autofocus={false}
										submitLabel="Restore as new"
										onsubmit={(n) => void restore(s.id, n)}
										oncancel={close}
									/>
								</div>
							</td>
						</tr>
					{/if}
				</tbody>
			{/each}
		</table>
		{#if snapshots.length > SNAPSHOTS_SHOWN}
			<button
				type="button"
				class="hk-btn-ghost hk-btn-sm toggle"
				aria-expanded={all}
				data-testid="snapshots-toggle"
				onclick={() => (all = !all)}>{all ? 'Show fewer' : `Show all ${snapshots.length}`}</button
			>
		{/if}
	{/if}
</section>

<style>
	.snaps {
		border-bottom: 1px solid var(--hk-hairline);
	}
	.snaps tbody + tbody tr:first-child td {
		border-top: 1px solid var(--hk-hairline);
	}
	.snaps td {
		font-size: 0.875rem;
	}
	.num {
		font-variant-numeric: tabular-nums;
	}
	.muted {
		color: var(--hk-text-3);
		font-size: 0.8125rem;
	}
	.sub {
		margin-top: 0.125rem;
		font-size: 0.75rem;
		color: var(--hk-text-3);
	}
	.snaps .act {
		width: 1px;
		text-align: right;
		white-space: nowrap;
	}
	.snaps td.act {
		padding-block: 0.375rem;
	}
	.act .hk-btn-ghost {
		margin-right: -0.625rem;
	}
	.snaps tr.open td {
		padding-bottom: 0.5rem;
	}
	.snaps td.panel-cell {
		padding: 0 0 0.875rem;
		border-top: 0;
	}
	/* One panel, both outcomes: the sentence, the typed confirm, a rule,
	   the name for a new project and Cancel. */
	.panel {
		padding: 0.875rem 1rem;
		border-radius: var(--hk-radius);
		background: var(--hk-surface-2);
	}
	.panel-say {
		margin: 0 0 0.875rem;
		max-width: 44rem;
		color: var(--hk-text);
	}
	.panel-note {
		margin: -0.5rem 0 0.875rem;
		color: var(--hk-text-2);
	}
	.panel-rule {
		margin: 1rem 0;
		border-top: 1px solid var(--hk-border);
	}
	.toggle {
		margin: 0.5rem 0 0 -0.625rem;
	}
</style>
