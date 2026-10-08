<!--
  One share of a plan's limit as a thin bar: what is used of what the plan
  holds (memory running now, disk the projects hold, egress this period). One
  series, so no legend; the numbers beside it carry the reading and the
  bar is a picture of them. The fill is ink; past the limit it turns amber
  and the words say "over", so the state is never colour alone.
-->
<script lang="ts">
	import { share } from '$lib/format';

	let {
		label,
		used,
		limit,
		format,
		note
	}: {
		label: string;
		used: number;
		limit: number;
		/** Formats a figure for the "used of limit" text. */
		format: (n: number) => string;
		/** A line under the bar: the overage, or what the limit means. */
		note?: string;
	} = $props();

	let fraction = $derived(share(used, limit));
	let over = $derived(limit > 0 && used > limit);
	let reading = $derived(`${format(used)} of ${format(limit)}`);
</script>

<div class="meter" data-testid="meter-{label.toLowerCase().replace(/[^a-z0-9]+/g, '-')}">
	<div class="flex items-baseline justify-between gap-4 text-sm">
		<span class="font-medium">{label}</span>
		<span class="font-mono text-compact text-ink-muted tabular-nums">
			{reading}{#if over}<span class="text-amber-700 dark:text-amber-400">&nbsp;· over</span>{/if}
		</span>
	</div>
	<!-- aria-valuenow stops at the limit, which is all a meter can hold, so
	     aria-valuetext gives the real reading, over the limit included. -->
	<div
		class="meter-track mt-1.5 h-2 w-full overflow-hidden rounded-xs border"
		role="meter"
		aria-label={label}
		aria-valuemin="0"
		aria-valuemax={limit}
		aria-valuenow={Math.min(used, limit)}
		aria-valuetext={over ? `${reading}, over the limit` : reading}
	>
		<div
			class="meter-fill h-full {over
				? 'meter-fill--over bg-amber-600 dark:bg-amber-400'
				: 'bg-zinc-800 dark:bg-zinc-200'}"
			style="width: {fraction * 100}%"
		></div>
	</div>
	{#if note}
		<p class="mt-1.5 text-xs text-ink-muted">{note}</p>
	{/if}
</div>

<style>
	/* The track needs an edge you can see: on a card, a sunken fill alone
	   sits at 1.1:1 and the bar loses its whole length, so a meter at 10%
	   read as a stray line. The control edge is 3:1 in both schemes. */
	.meter-track {
		background-color: var(--sunken);
		border-color: var(--control-edge, var(--rule-strong));
	}
	/* Forced colours erase background fills, which would leave every meter
	   reading empty. The fill opts out and takes a system colour; the
	   track's border is kept by the browser as a system colour on its own. */
	@media (forced-colors: active) {
		.meter-fill {
			forced-color-adjust: none;
			background-color: CanvasText;
		}
		.meter-fill--over {
			background-color: Highlight;
		}
	}
</style>
