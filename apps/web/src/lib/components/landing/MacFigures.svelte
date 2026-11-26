<!--
  03 Your Mac (DECISIONS I-1674): four measured comparisons of the two ways
  repose has run sandboxes on one Mac, each sandbox its own VM (accent) and
  every sandbox inside one Linux VM (grey). Every length, value and sentence
  comes from lib/data/mac-figures.ts; nothing here is typed in. Static: no
  motion, so nothing to pause (docs/LANDING.md "Motion").
-->
<script lang="ts">
	import {
		LAYOUTS,
		REST,
		RESTART,
		SNAPSHOT,
		START,
		bar,
		figure,
		seconds,
		ticks,
		type Chart,
		type Range
	} from '$lib/data/mac-figures';

	/** "Each sandbox its own VM" as it reads inside a sentence. */
	const own = LAYOUTS.own[0].toLowerCase() + LAYOUTS.own.slice(1);
	const nested = LAYOUTS.nested[0].toLowerCase() + LAYOUTS.nested.slice(1);

	const said = (r: Range, unit: Chart['unit'], words?: string) =>
		words ? `${figure(r, unit)}, ${words}` : figure(r, unit);

	/** One sentence per chart for a screen reader; the bars are hidden from it. */
	function sentence(c: Chart): string {
		return c.rows
			.map(
				(r) =>
					`${r.label}: ${own} ${said(r.own, c.unit, r.ownSays)}; ${nested} ${said(r.nested, c.unit, r.nestedSays)}.`
			)
			.join(' ');
	}
</script>

{#snippet track(r: Range, max: number, kind: 'own' | 'nested')}
	{@const b = bar(r, max)}
	<span class="mf-track" aria-hidden="true">
		<span class="mf-bar mf-{kind}" style:width="{b.lo}%"></span>
		{#if b.hi > b.lo}
			<span class="mf-bar mf-{kind} mf-span" style:left="{b.lo}%" style:width="{b.hi - b.lo}%"
			></span>
		{/if}
	</span>
{/snippet}

{#snippet value(r: Range, unit: Chart['unit'], words: string | undefined, kind: 'own' | 'nested')}
	<span class="mf-val" class:mf-val-own={kind === 'own'} aria-hidden="true"
		>{figure(r, unit)}{#if words}<span class="mf-says">{words}</span>{/if}</span
	>
{/snippet}

{#snippet chart(c: Chart)}
	<figure class="mf-chart" role="img" aria-label={sentence(c)}>
		{#each c.rows as r (r.label)}
			<p class="mf-label" aria-hidden="true">{r.label}</p>
			{@render track(r.own, c.max, 'own')}
			{@render value(r.own, c.unit, r.ownSays, 'own')}
			{@render track(r.nested, c.max, 'nested')}
			{@render value(r.nested, c.unit, r.nestedSays, 'nested')}
		{/each}
		<span class="mf-ticks" aria-hidden="true">
			{#each ticks(c) as t (t)}
				<!-- On a phone only the axis's ends stand. -->
				<span
					class="mf-tick"
					class:mf-minor={t !== 0 && t !== c.max}
					style:left="{(t / c.max) * 100}%">{t}{t === c.max ? ` ${c.unit}` : ''}</span
				>
			{/each}
		</span>
		{#if c.note}<figcaption class="mf-note">{c.note}</figcaption>{/if}
	</figure>
{/snippet}

<ul class="cells mf" data-testid="mac-figures">
	<li>
		<div class="cell-copy">
			<p class="label num"><span class="n">3.1 </span>Start</p>
			<h3 class="item-title">Start a sandbox in seconds</h3>
			<p class="copy">
				A new one arrives with your checkout copied in; a stopped one keeps its disk.
			</p>
		</div>
		<div class="mf-fig">{@render chart(START)}</div>
	</li>
	<li>
		<div class="cell-copy">
			<p class="label num"><span class="n">3.2 </span>Snapshots</p>
			<h3 class="item-title">Snapshot in under a second</h3>
			<p class="copy">A snapshot is an APFS clone: it copies nothing until the sandbox writes.</p>
		</div>
		<div class="mf-fig">{@render chart(SNAPSHOT)}</div>
	</li>
	<li>
		<div class="cell-copy">
			<p class="label num"><span class="n">3.3 </span>Memory</p>
			<h3 class="item-title">Idle sandboxes give their memory back</h3>
			<p class="copy">
				{#if REST.unlocked}While your Mac is unlocked, an{:else}An{/if} idle sandbox is saved to disk
				in {seconds(REST.save)} and quits. Your next command wakes it in {seconds(REST.wake)}.
			</p>
		</div>
		<div class="mf-fig">{@render chart(REST.chart)}</div>
	</li>
	<li>
		<div class="cell-copy">
			<p class="label num"><span class="n">3.4 </span>Restart</p>
			<h3 class="item-title">Restart through repose and keep what is running</h3>
			<p class="copy">
				<code>repose host restart</code> saves each sandbox's memory first. They come back once you log
				in.
			</p>
		</div>
		<div class="mf-fig">{@render chart(RESTART)}</div>
	</li>
</ul>

<style>
	.mf-fig {
		padding: 0.5rem var(--shot-r) 1.75rem var(--shot-l);
	}
	/* One grid per chart: the bars' column, and the values' column sized to
	   its longest value, so a value never runs past the rule. */
	.mf-chart {
		display: grid;
		grid-template-columns: minmax(0, 1fr) 7.5rem;
		align-items: start;
		column-gap: 1rem;
		row-gap: 0.375rem;
		margin: 0;
	}
	.mf-label,
	.mf-note {
		grid-column: 1 / -1;
	}
	.mf-label {
		margin: 0 0 0.125rem;
		font-size: 0.8125rem;
		line-height: 1.25rem;
		color: var(--hk-text-2);
	}
	.mf-label:not(:first-child) {
		margin-top: 0.875rem;
	}
	/* A bar stands centred on its value's first line. */
	.mf-track {
		position: relative;
		display: block;
		height: 0.625rem;
		margin-top: 0.1875rem;
	}
	.mf-bar {
		position: absolute;
		top: 0;
		bottom: 0;
		left: 0;
		min-width: 2px;
		border-radius: 1px;
	}
	.mf-own {
		background: var(--pic-accent);
	}
	.mf-nested {
		background: var(--hk-text-4);
	}
	.mf-span {
		opacity: 0.5;
	}
	.mf-val {
		font-family: var(--hk-font-mono);
		font-size: 0.75rem;
		line-height: 1rem;
		font-variant-numeric: tabular-nums;
		color: var(--hk-text-3);
		white-space: nowrap;
	}
	.mf-val-own {
		color: var(--hk-text);
	}
	.mf-says {
		display: block;
		color: var(--hk-text-3);
	}
	/* The axis: a hairline under the bars' column, its ticks in mono. */
	.mf-ticks {
		position: relative;
		display: block;
		grid-column: 1;
		height: 1.5rem;
		margin-top: 0.375rem;
		border-top: 1px solid var(--hk-hairline);
	}
	.mf-tick {
		position: absolute;
		top: 0;
		padding-top: 0.375rem;
		font-family: var(--hk-font-mono);
		font-size: 0.6875rem;
		line-height: 1rem;
		color: var(--hk-text-3);
		white-space: nowrap;
		transform: translateX(-50%);
	}
	.mf-tick::before {
		content: '';
		position: absolute;
		top: -1px;
		left: 50%;
		width: 1px;
		height: 0.3125rem;
		background: var(--hk-border-strong);
	}
	.mf-tick:first-child {
		transform: none;
	}
	.mf-tick:first-child::before {
		left: 0;
	}
	.mf-tick:last-child {
		transform: translateX(-100%);
	}
	.mf-tick:last-child::before {
		left: auto;
		right: 0;
	}
	.mf-note {
		margin-top: 0.5rem;
		font-size: 0.75rem;
		line-height: 1.125rem;
		color: var(--hk-text-3);
		text-wrap: pretty;
	}
	/* From 1024px the two cells of a row share their rows, so the charts of
	   a row start on one line whatever their copy's length. */
	@media (min-width: 1024px) {
		.mf > li {
			display: grid;
			grid-row: span 2;
			grid-template-rows: subgrid;
		}
		.mf > li > :global(.cell-copy) {
			order: 0;
		}
	}
	@media (max-width: 639.98px) {
		.mf-chart {
			grid-template-columns: minmax(0, 1fr) 5.75rem;
			column-gap: 0.625rem;
		}
		.mf-val {
			font-size: 0.6875rem;
		}
		.mf-minor {
			display: none;
		}
	}
</style>
