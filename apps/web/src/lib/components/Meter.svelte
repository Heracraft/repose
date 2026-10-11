<!--
  One share of a plan's limit as a thin bar: what is used of what the plan
  holds (memory running now, disk the projects hold, egress this period). One
  series, so no legend; the numbers beside it carry the reading and the
  bar is a picture of them. The fill is the accent, which may draw a
  measure (I-659). Past the limit the bar's scale becomes the used figure:
  the share up to the limit stays blue, the overage is amber, and an ink
  tick marks where the limit falls, so "over" reads as an amount. The words
  say "over" too, so the state is never colour alone.
-->
<script lang="ts">
	import { share } from '$lib/format';

	let {
		label,
		used,
		limit,
		format,
		note,
		marker,
		markerLabel,
		compact = false,
		href
	}: {
		label: string;
		used: number;
		limit: number;
		/** Formats a figure for the "used of limit" text. */
		format: (n: number) => string;
		/** A line under the bar: the overage, or what the limit means. */
		note?: string;
		/** 0..1 of the limit: a faint tick, such as where the period's pace puts egress today. */
		marker?: number;
		/** What the marker means, read after the meter. */
		markerLabel?: string;
		/** The bar and the reading on one line, no label above: for a table cell. */
		compact?: boolean;
		/** The label is a link (already built with resolve()). */
		href?: string;
	} = $props();

	const markerId = $props.id();
	let over = $derived(limit > 0 && used > limit);
	// The bar's whole length: the limit, or the used figure past it.
	let scale = $derived(over ? used : limit);
	let fill = $derived(over ? share(limit, scale) : share(used, limit));
	let overage = $derived(over ? 1 - fill : 0);
	let reading = $derived(`${format(used)} of ${format(limit)}`);
	let markAt = $derived(
		marker === undefined || limit <= 0 ? undefined : share(marker * limit, scale)
	);
	let testid = $derived(`meter-${label.toLowerCase().replace(/[^a-z0-9]+/g, '-')}`);
</script>

{#snippet bar()}
	<!-- aria-valuenow stops at the limit, which is all a meter can hold, so
	     aria-valuetext gives the real reading, over the limit included. -->
	<div
		class="relative {compact ? 'w-24 shrink-0' : 'mt-2 w-full'}"
		role="meter"
		aria-label={label}
		aria-valuemin="0"
		aria-valuemax={limit}
		aria-valuenow={Math.min(used, limit)}
		aria-valuetext={over ? `${reading}, over the limit` : reading}
		aria-describedby={markAt !== undefined && markerLabel ? markerId : undefined}
	>
		<div class="meter-track flex h-1.5 w-full overflow-hidden rounded-xs border">
			<div class="meter-fill h-full" style="width: {fill * 100}%"></div>
			{#if over}
				<div class="meter-fill meter-fill--over h-full" style="width: {overage * 100}%"></div>
			{/if}
		</div>
		{#if over}
			<span
				class="meter-tick absolute -top-[3px] h-3 w-px bg-ink"
				style="left: calc({fill * 100}% - 0.5px)"
				aria-hidden="true"
			></span>
		{/if}
		{#if markAt !== undefined}
			<span
				class="meter-tick absolute -top-0.5 h-2.5 w-px bg-ink-muted"
				style="left: calc({markAt * 100}% - 0.5px)"
				aria-hidden="true"
			></span>
		{/if}
	</div>
	{#if markAt !== undefined && markerLabel}
		<span id={markerId} class="sr-only">{markerLabel}</span>
	{/if}
{/snippet}

{#snippet figures()}
	<span class="font-mono text-compact whitespace-nowrap text-ink-muted tabular-nums">
		{reading}{#if over}<span class="text-amber-800 dark:text-amber-300">&nbsp;· over</span>{/if}
	</span>
{/snippet}

{#if compact}
	<div class="meter flex items-center gap-3" data-testid={testid}>
		{@render bar()}
		{@render figures()}
	</div>
{:else}
	<div class="meter" data-testid={testid}>
		<div class="flex items-baseline justify-between gap-4 text-sm">
			{#if href}
				<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -- callers build href with resolve() -->
				<a {href} class="font-medium hover:underline">{label}</a>
			{:else}
				<span class="font-medium">{label}</span>
			{/if}
			{@render figures()}
		</div>
		{@render bar()}
		{#if note}
			<p class="mt-2 text-xs text-ink-muted">{note}</p>
		{/if}
	</div>
{/if}

<style>
	/* The track needs an edge you can see: on a card, a sunken fill alone
	   sits at 1.1:1 and the bar loses its whole length, so a meter at 10%
	   read as a stray line. The control edge is 3:1 in both schemes. */
	.meter-track {
		background-color: var(--sunken);
		border-color: var(--control-edge, var(--rule-strong));
	}
	.meter-fill {
		background-color: var(--accent);
	}
	.meter-fill--over {
		background-color: var(--color-amber-500);
	}
	@media (prefers-color-scheme: dark) {
		.meter-fill--over {
			background-color: var(--color-amber-400);
		}
	}
	/* Forced colours erase background fills, which would leave every meter
	   reading empty. The fills and ticks opt out and take system colours;
	   the track's border is kept by the browser as a system colour on its
	   own. */
	@media (forced-colors: active) {
		.meter-fill,
		.meter-tick {
			forced-color-adjust: none;
			background-color: CanvasText;
		}
		.meter-fill--over {
			background-color: Highlight;
		}
	}
</style>
