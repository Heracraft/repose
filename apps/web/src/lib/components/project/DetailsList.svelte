<!--
  What a project is built from and when things happened to it: its base
  (and whether base updates are held), the config revision it runs and
  whether that built, the agent a bare `repose run` opens, when it was
  created, when a temporary one is destroyed, and when it last took a
  snapshot.
-->
<script lang="ts">
	import { resolve } from '$app/paths';
	import { dateTime, relativeTime } from '$lib/format';
	import type { Project, Revision } from '$lib/api/types';

	let {
		project,
		revision,
		/** Keeps a temporary machine; absent where the card is read-only. */
		onkeep
	}: { project: Project; revision?: Revision; onkeep?: () => Promise<void> } = $props();

	let keeping = $state(false);

	async function keep() {
		if (keeping || !onkeep) return;
		keeping = true;
		try {
			await onkeep();
		} finally {
			keeping = false;
		}
	}
</script>

<section class="card" aria-labelledby="details-title" data-testid="details-card">
	<h2 id="details-title" class="text-xl font-semibold">Details</h2>
	<dl class="mt-2 text-sm">
		<div class="row flex items-baseline justify-between gap-4 py-2">
			<dt class="text-ink-muted">Base</dt>
			<dd class="flex items-baseline gap-2">
				<span class="font-mono text-compact">{project.base_version}</span>
				{#if project.hold_base_updates}<span class="badge">held</span>{/if}
			</dd>
		</div>
		<div class="row flex items-baseline justify-between gap-4 py-2">
			<dt class="text-ink-muted">Config</dt>
			<dd class="flex min-w-0 items-baseline gap-2">
				{#if revision}
					<span
						class="badge"
						class:badge--new={revision.status === 'applied'}
						class:badge--error={revision.status === 'failed'}>{revision.status}</span
					>
					<span class="text-xs text-ink-muted tabular-nums" title={dateTime(revision.created_at)}
						>{relativeTime(revision.created_at)}</span
					>
				{/if}
				<a href={resolve('/projects/[id]/config', { id: project.id })} class="link">View</a>
			</dd>
		</div>
		<div class="row flex items-baseline justify-between gap-4 py-2">
			<dt class="text-ink-muted">Default agent</dt>
			<dd class="font-mono text-compact">{project.agent_default || '—'}</dd>
		</div>
		<div class="row flex items-baseline justify-between gap-4 py-2">
			<dt class="text-ink-muted">Created</dt>
			<dd class="whitespace-nowrap tabular-nums">{dateTime(project.created_at)}</dd>
		</div>
		{#if project.expires_at}
			<!-- A temporary machine is destroyed with no snapshot when this
			     passes (I-347). Keep is the only escape from that timer, and
			     until now it was in the CLI alone — including for the user
			     who taps the hour-left notification on a phone. -->
			<div class="row flex items-baseline justify-between gap-4 py-2" data-testid="details-expires">
				<dt class="text-ink-muted">Expires</dt>
				<dd class="flex items-baseline gap-3 whitespace-nowrap tabular-nums">
					{dateTime(project.expires_at)}
					{#if onkeep}
						<button
							type="button"
							class="btn-ghost hit"
							aria-disabled={keeping ? 'true' : undefined}
							onclick={keep}>{keeping ? 'Keeping…' : 'Keep'}</button
						>
					{/if}
				</dd>
			</div>
		{/if}
		<div class="row flex items-baseline justify-between gap-4 py-2">
			<dt class="text-ink-muted">Last snapshot</dt>
			<dd class="tabular-nums">
				{#if project.last_snapshot_at}
					<span title={dateTime(project.last_snapshot_at)}
						>{relativeTime(project.last_snapshot_at)}</span
					>
				{:else}
					<span class="text-ink-muted">none</span>
				{/if}
			</dd>
		</div>
	</dl>
</section>
