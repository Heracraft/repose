<!--
  04 Unlimited agents (ENG-290): asymmetric 2-column band.
  Left: section label, two-tone heading, and one-line explanation.
  Right: compact, sharp-cornered host list with taller rows so the real
  dashboard Frost component (Frost.svelte + lib/hk/frost.css) breathes.
-->
<script lang="ts">
	import Frost from '$lib/components/Frost.svelte';
	import Snowflake from '$lib/components/Snowflake.svelte';

	const ROWS = [
		{ name: 'api-rewrite', state: 'active', action: 'working', mem: 68 },
		{ name: 'docs-refresh', state: 'paused', action: 'resumes in ms', mem: 36 },
		{ name: 'cve-triage', state: 'frozen', action: 'thaws on connect', mem: 0 },
		{ name: 'billing-v2', state: 'frozen', action: 'thaws on connect', mem: 0 },
		{ name: 'mobile-sync', state: 'frozen', action: 'thaws on connect', mem: 0 }
	] as const;

	const FROZEN_SINCE = '2026-01-01T00:00:00Z';
</script>

<div class="it-grid" data-testid="idle-tiers">
	<div class="it-copy">
		<p class="label num"><span class="n">03 </span>Unlimited agents</p>
		<h2 class="hk-h2" id="idle-h">
			Keep as many agents <span class="muted">as you want</span>
		</h2>
		<p class="copy">
			Idle sandboxes pause, then freeze to disk — freeing memory for other work without losing
			state.
		</p>
	</div>

	<div class="it-pane" aria-hidden="true">
		<div class="it-pane-head">
			<span>host-01 · 5 sandboxes</span>
			<span class="it-pane-sum">1 active · 1 paused · 3 frozen</span>
		</div>
		<ul class="it-list">
			{#each ROWS as r, idx (r.name)}
				<li
					class="it-row it-row--{r.state}"
					data-frost-on={r.state === 'frozen' ? 'row' : undefined}
				>
					{#if r.state === 'frozen'}
						<Frost kind="row" since={FROZEN_SINCE} n={idx} />
					{/if}
					<span class="it-name">{r.name}</span>
					<span class="it-badge it-badge--{r.state}">
						{#if r.state === 'frozen'}
							<Snowflake size={12} />
						{:else}
							<i class="it-dot it-dot--{r.state}"></i>
						{/if}
						{r.state}
					</span>
					<span class="it-mem">
						{#if r.mem > 0}
							<span class="it-mem-fill it-mem-fill--{r.state}" style:width="{r.mem}%"></span>
						{:else}
							<span class="it-mem-empty">memory freed</span>
						{/if}
					</span>
					<span class="it-act">{r.action}</span>
				</li>
			{/each}
		</ul>
	</div>
</div>

<style>
	.it-grid {
		display: grid;
		grid-template-columns: minmax(0, 1fr);
		gap: 1.75rem;
		padding-bottom: 2.5rem;
	}
	@media (min-width: 960px) {
		.it-grid {
			grid-template-columns: minmax(0, 0.72fr) minmax(0, 1.28fr);
			align-items: start;
			gap: 3rem;
		}
	}
	.it-copy {
		display: flex;
		flex-direction: column;
		gap: 0.75rem;
	}
	.it-copy :global(.copy) {
		margin-top: 0.25rem;
	}

	/* Sharp-cornered host pane with 1px header bar (Zed) & 2px active accent rail (Warp) */
	.it-pane {
		border: 1px solid var(--hk-border);
		border-radius: 0;
		background: var(--hk-surface);
	}
	.it-pane-head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.75rem;
		padding: 0.6rem 1.25rem;
		border-bottom: 1px solid var(--hk-border);
		background: var(--hk-bg);
		font-family: var(--hk-font-mono);
		font-size: 0.6875rem;
		color: var(--hk-text-3);
	}
	.it-pane-sum {
		white-space: nowrap;
	}
	.it-list {
		--hk-row-bleed: 0px;
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.it-row {
		display: grid;
		grid-template-columns: 7.75rem 5.5rem minmax(0, 1fr) auto;
		align-items: center;
		gap: 1rem;
		min-height: 4.25rem;
		padding: 1.25rem 1.25rem;
		border-bottom: 1px solid var(--hk-hairline);
		font-size: 0.8125rem;
	}
	.it-row--active {
		box-shadow: inset 2px 0 0 var(--pic-accent);
	}
	.it-row:last-child {
		border-bottom: 0;
	}
	@media (max-width: 639.98px) {
		.it-row {
			grid-template-columns: minmax(0, 1fr) auto;
			min-height: 3.75rem;
			padding: 1rem;
		}
		.it-mem,
		.it-act {
			display: none;
		}
	}
	.it-name {
		font-family: var(--hk-font-mono);
		color: var(--hk-text);
		white-space: nowrap;
		overflow: hidden;
		text-overflow: ellipsis;
	}
	.it-badge {
		display: inline-flex;
		align-items: center;
		gap: 0.375rem;
		font-family: var(--hk-font-mono);
		font-size: 0.75rem;
	}
	.it-badge--active {
		color: var(--hk-text);
	}
	.it-badge--paused {
		color: var(--hk-text-2);
	}
	.it-badge--frozen {
		color: var(--hk-ice-ink, #456b86);
	}
	.it-dot {
		display: inline-block;
		width: 7px;
		height: 7px;
		border-radius: 50%;
		flex: none;
	}
	.it-dot--active {
		background: var(--pic-accent);
	}
	.it-dot--paused {
		background: var(--hk-text-3);
	}
	.it-mem {
		display: flex;
		align-items: center;
		height: 0.45rem;
		border-radius: 1px;
		background: var(--hk-surface-2);
		overflow: hidden;
	}
	.it-mem-fill {
		display: block;
		height: 100%;
		border-radius: 1px;
	}
	.it-mem-fill--active {
		background: var(--pic-accent);
	}
	.it-mem-fill--paused {
		background: var(--hk-text-3);
		opacity: 0.55;
	}
	.it-mem-empty {
		font-family: var(--hk-font-mono);
		font-size: 0.6875rem;
		color: var(--hk-ice-ink, #456b86);
		white-space: nowrap;
	}
	.it-row--frozen .it-mem {
		height: auto;
		background: transparent;
		overflow: visible;
	}
	.it-act {
		font-family: var(--hk-font-mono);
		font-size: 0.6875rem;
		color: var(--hk-text-3);
		white-space: nowrap;
	}
</style>
