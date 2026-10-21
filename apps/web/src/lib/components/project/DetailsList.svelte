<!--
  What a project is built from and when things happened to it (I-837), as
  read-back rows: its base (and whether base updates are held), the
  config revision it runs and whether that built, the agent a bare
  `repose run` opens, when it was created, when a temporary one is
  destroyed (with Keep, as `repose keep` does), and when it last took a
  snapshot. A fact the project does not have is left out.
-->
<script lang="ts">
	import { resolve } from '$app/paths';
	import { dateTime, relativeTime } from '$lib/format';
	import type { Project, Revision } from '$lib/api/types';

	let {
		project,
		revision,
		/** Keeps a temporary machine; absent where the section is read-only. */
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

<section class="hk-section" aria-labelledby="details-title" data-testid="details-card">
	<div class="hk-section-head">
		<h2 id="details-title" class="hk-h2-app">Details</h2>
	</div>
	<dl class="hk-facts details">
		<div>
			<dt>Base</dt>
			<dd>
				<span class="hk-mono">{project.base_version}</span>{#if project.hold_base_updates}<span
						class="aside">&nbsp;· held</span
					>{/if}
			</dd>
		</div>
		<div>
			<dt>Config</dt>
			<dd class="config">
				{#if revision}
					<span class="status"
						>{#if revision.status === 'failed'}<span class="hk-dot hk-dot--err" aria-hidden="true"
							></span>{/if}{revision.status}</span
					>
					<span class="aside" title={dateTime(revision.created_at)}
						>{relativeTime(revision.created_at)}</span
					>
				{/if}
				<a href={resolve('/projects/[id]/config', { id: project.id })} class="hk-link view-link"
					>View</a
				>
			</dd>
		</div>
		{#if project.agent_default}
			<div>
				<dt>Default agent</dt>
				<dd class="hk-mono">{project.agent_default}</dd>
			</div>
		{/if}
		<div>
			<dt>Created</dt>
			<dd class="num">{dateTime(project.created_at)}</dd>
		</div>
		{#if project.expires_at}
			<!-- A temporary machine is destroyed with no snapshot when this
			     passes (I-347); Keep is the way off that timer, here as in
			     the CLI. -->
			<div data-testid="details-expires">
				<dt>Expires</dt>
				<dd class="expires num">
					<span>{dateTime(project.expires_at)}</span>
					{#if onkeep}
						<button
							type="button"
							class="hk-btn-ghost hk-btn-sm keep"
							aria-disabled={keeping ? 'true' : undefined}
							onclick={keep}>{keeping ? 'Keeping…' : 'Keep'}</button
						>
					{/if}
				</dd>
			</div>
		{/if}
		{#if project.last_snapshot_at}
			<div>
				<dt>Last snapshot</dt>
				<dd class="num" title={dateTime(project.last_snapshot_at)}>
					{relativeTime(project.last_snapshot_at)}
				</dd>
			</div>
		{/if}
	</dl>
</section>

<style>
	/* A 21rem column: the label column narrows to its words. */
	.details > :global(div) {
		grid-template-columns: 7.5rem minmax(0, 1fr);
	}
	@media (max-width: 639.98px) {
		.details > :global(div) {
			grid-template-columns: minmax(0, 1fr);
		}
	}
	.num {
		font-variant-numeric: tabular-nums;
	}
	.aside {
		color: var(--hk-text-3);
		font-size: 0.8125rem;
	}
	.config {
		display: flex;
		flex-wrap: wrap;
		align-items: baseline;
		gap: 0 0.5rem;
	}
	.status {
		display: inline-flex;
		align-items: center;
		gap: 0.375rem;
		color: var(--hk-text-2);
	}
	.expires {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		justify-content: space-between;
		gap: 0 0.5rem;
	}
	.keep {
		margin-block: -0.375rem;
		margin-right: -0.625rem;
	}
	/* A thumb's 44px, the row's height unchanged. */
	@media (pointer: coarse), (max-width: 639.98px) {
		.view-link {
			display: inline-flex;
			align-items: center;
			min-height: 2.75rem;
			margin-block: -0.6875rem;
		}
	}
	/* Forced colours draw a ghost's border: keep it inside the frame. */
	@media (forced-colors: active) {
		.keep {
			margin-inline: 0;
		}
	}
</style>
