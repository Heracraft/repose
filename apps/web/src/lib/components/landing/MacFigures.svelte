<!--
  The Mac part of 03 Your hardware (DECISIONS I-1674), drawn only while
  MAC_DESIGN2_LIVE is true: four measured comparisons of the two ways
  repose has run sandboxes on one Mac, each sandbox its own VM (accent) and
  every sandbox inside one Linux VM (grey). Every length, value and sentence
  comes from lib/data/mac-figures.ts; nothing here is typed in. Static: no
  motion, so nothing to pause (docs/LANDING.md "Motion").
-->
<script lang="ts">
	import {
		LAYOUTS,
		MEASURED,
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
	<div class="mf-chart" role="img" aria-label={sentence(c)}>
		{#each c.rows as r (r.label)}
			<p class="mf-label" aria-hidden="true">{r.label}</p>
			{@render track(r.own, c.max, 'own')}
			{@render value(r.own, c.unit, r.ownSays, 'own')}
			{@render track(r.nested, c.max, 'nested')}
			{@render value(r.nested, c.unit, r.nestedSays, 'nested')}
		{/each}
		<span class="mf-ticks" aria-hidden="true">
			{#each ticks(c) as t (t)}
				<!-- Only the axis's ends are labelled; the inner ticks are marks. -->
				<span
					class="mf-tick"
					class:mf-minor={t !== 0 && t !== c.max}
					style:left="{(t / c.max) * 100}%">{t}{t === c.max ? ` ${c.unit}` : ''}</span
				>
			{/each}
		</span>
	</div>
	{#if c.note}<p class="mf-note">{c.note}</p>{/if}
{/snippet}

<div class="mf-head" data-testid="mac-figures">
	<h3 class="mf-h" id="mac-h">And on a Mac, each sandbox is its own VM</h3>
	<div class="mf-side">
		<ul class="mf-legend">
			<li><i class="mf-key mf-own" aria-hidden="true"></i>{LAYOUTS.own}</li>
			<li>
				<i class="mf-key mf-nested" aria-hidden="true"></i><span
					>{LAYOUTS.nested} <span class="mf-ver nw">({LAYOUTS.nestedVersion})</span></span
				>
			</li>
		</ul>
		<p class="mf-where">
			Measured on {MEASURED.machine}, {MEASURED.date}. A bar runs from the fastest run to the
			slowest.
		</p>
	</div>
</div>
<ul class="mf" aria-labelledby="mac-h">
	<li>
		<div class="mf-copy">
			<p class="label num"><span class="n">3.4 </span>Start</p>
			<h4 class="item-title">Start in seconds</h4>
		</div>
		<div class="mf-fig">{@render chart(START)}</div>
	</li>
	<li>
		<div class="mf-copy">
			<p class="label num"><span class="n">3.5 </span>Snapshots</p>
			<h4 class="item-title">Restore a snapshot in seconds</h4>
		</div>
		<div class="mf-fig">{@render chart(SNAPSHOT)}</div>
	</li>
	<li>
		<div class="mf-copy">
			<p class="label num"><span class="n">3.6 </span>Memory</p>
			<h4 class="item-title">Stopped sandboxes give memory back</h4>
			<p class="copy">
				An idle one is paused and keeps its memory; your next command wakes it in
				{seconds(REST.wake)}.
			</p>
		</div>
		<div class="mf-fig">{@render chart(REST.chart)}</div>
	</li>
	<li>
		<div class="mf-copy">
			<p class="label num"><span class="n">3.7 </span>Restart</p>
			<h4 class="item-title">Restart and keep memory, when there is room</h4>
			<p class="copy">
				<code>repose host restart</code> saves each sandbox's memory to disk, or refuses when the disk
				has no room for it; they are back once you log in.
			</p>
		</div>
		<div class="mf-fig">{@render chart(RESTART)}</div>
	</li>
</ul>

<style>
	/* The Mac's own head inside the band: a serif h3 at the left, the
	   legend and where it was measured on the right, like a band's head. */
	.mf-head {
		display: grid;
		grid-template-columns: minmax(0, 1fr);
		row-gap: 0.75rem;
		margin: 0 calc(-1 * var(--edge));
		padding: 2rem var(--edge) var(--head-gap);
		border-top: 1px solid var(--rule-in);
	}
	.mf-h {
		margin: 0;
		font-family: var(--hk-font-display);
		font-size: clamp(1.375rem, 1.1rem + 0.8vw, 1.75rem);
		font-weight: 400;
		line-height: 1.2;
		letter-spacing: -0.015em;
		color: var(--hk-text);
		text-wrap: balance;
	}
	.mf-side {
		display: flex;
		flex-direction: column;
		gap: 0.5rem;
	}
	.mf-legend {
		display: flex;
		flex-wrap: wrap;
		gap: 0.25rem 1.5rem;
		margin: 0;
		padding: 0;
		list-style: none;
		font-size: 0.875rem;
		line-height: 1.25rem;
		color: var(--hk-text-2);
	}
	.mf-legend li {
		display: inline-flex;
		align-items: baseline;
		gap: 0.5rem;
	}
	.mf-key {
		flex: none;
		align-self: center;
		width: 0.875rem;
		height: 0.625rem;
		border-radius: 1px;
	}
	.mf-ver,
	.mf-where {
		color: var(--hk-text-3);
	}
	.mf-where {
		margin: 0;
		font-size: 0.8125rem;
		line-height: 1.25rem;
	}
	@media (min-width: 1024px) {
		.mf-head {
			grid-template-columns: repeat(12, minmax(0, 1fr));
			column-gap: var(--col-gap);
			align-items: last baseline;
		}
		.mf-h {
			grid-column: 1 / 7;
		}
		.mf-side {
			grid-column: 7 / -1;
		}
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
	.mf-label {
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
		margin: 0.875rem 0 0;
		font-size: 0.75rem;
		line-height: 1.125rem;
		color: var(--hk-text-3);
		text-wrap: pretty;
	}
	/* The four charts: one column, two from 1024px, four from 1280px; the
	   cells of a row share their rows (subgrid), so the charts start on
	   one line whatever the copy's length. */
	.mf {
		display: grid;
		grid-template-columns: minmax(0, 1fr);
		margin: 0 calc(-1 * var(--edge));
		padding: 0;
		list-style: none;
		border-top: 1px solid var(--rule-in);
	}
	.mf > li {
		display: flex;
		flex-direction: column;
		min-width: 0;
		padding: 1.375rem var(--edge) 1.75rem;
	}
	.mf > li + li {
		border-top: 1px solid var(--rule-in);
	}
	.mf-copy > :global(.num) {
		margin-bottom: 0.375rem;
	}
	.mf-copy > :global(.item-title) {
		text-wrap: balance;
	}
	.mf-copy > :global(.copy) {
		margin-top: 0.25rem;
	}
	.mf-fig {
		padding: 1rem 0 0;
	}
	.mf-tick.mf-minor {
		font-size: 0;
		padding: 0;
	}
	@media (min-width: 1024px) {
		.mf {
			grid-template-columns: repeat(2, minmax(0, 1fr));
		}
		.mf > li {
			display: grid;
			grid-row: span 2;
			grid-template-rows: subgrid;
			padding: 1.5rem var(--in) 1.75rem;
		}
		.mf > li + li {
			border-top: 0;
		}
		.mf > li:nth-child(odd) {
			padding-left: var(--edge);
		}
		.mf > li:nth-child(even) {
			padding-right: var(--edge);
			border-left: 1px solid var(--rule-in);
		}
		.mf > li:nth-child(n + 3) {
			border-top: 1px solid var(--rule-in);
		}
	}
	@media (min-width: 1280px) {
		.mf {
			grid-template-columns: repeat(4, minmax(0, 1fr));
		}
		.mf > li:nth-child(n) {
			padding: 1.5rem var(--in) 1.75rem;
			border-top: 0;
		}
		.mf > li:first-child {
			padding-left: var(--edge);
		}
		.mf > li:last-child {
			padding-right: var(--edge);
		}
		.mf > li + li {
			border-left: 1px solid var(--rule-in);
		}
		.mf-chart {
			grid-template-columns: minmax(0, 1fr) 5.5rem;
			column-gap: 0.625rem;
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
	}
</style>
