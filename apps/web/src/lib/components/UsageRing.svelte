<!--
  A share of a limit as a small pie beside its figure (I-674, I-679): the
  figure carries the reading, the wedge is a glance at how full it is. A
  filled wedge, not a stroked arc: a short arc on a ring reads as a
  loading spinner. The quarter-circle is the landing's own shape. Accent
  up to the limit; past it the disc fills in amber, and the words say
  "over" too, so the state is never colour alone.
-->
<script lang="ts">
	import { share } from '$lib/format';

	let {
		label,
		used,
		limit,
		format,
		size = 18
	}: {
		label: string;
		used: number;
		limit: number;
		/** Formats a figure for the meter's spoken reading. */
		format: (n: number) => string;
		size?: number;
	} = $props();

	/** The wedge from twelve o'clock, clockwise, for a share 0..1 of the disc. */
	function wedge(f: number): string {
		const a = f * 2 * Math.PI;
		const x = 9 + 8 * Math.sin(a);
		const y = 9 - 8 * Math.cos(a);
		return `M9 9 L9 1 A8 8 0 ${f > 0.5 ? 1 : 0} 1 ${x.toFixed(3)} ${y.toFixed(3)} Z`;
	}
	let over = $derived(limit > 0 && used > limit);
	// A sliver stays visible for any use at all, so 0.4% is not read as none.
	let frac = $derived(over ? 1 : used > 0 ? Math.max(share(used, limit), 0.05) : 0);
	let reading = $derived(`${format(used)} of ${format(limit)}${over ? ', over' : ''}`);
</script>

<svg
	class="uring"
	class:uring--over={over}
	width={size}
	height={size}
	viewBox="0 0 18 18"
	role="meter"
	aria-label={label}
	aria-valuemin={0}
	aria-valuemax={limit}
	aria-valuenow={Math.min(used, limit)}
	aria-valuetext={reading}
>
	<circle class="track" cx="9" cy="9" r="8" />
	{#if frac >= 1}
		<circle class="wedge" cx="9" cy="9" r="8" />
	{:else if frac > 0}
		<path class="wedge" d={wedge(frac)} />
	{/if}
</svg>

<style>
	.uring {
		flex: none;
		display: block;
	}
	.track {
		fill: var(--sunken);
		stroke: var(--rule-strong);
		stroke-width: 1;
	}
	.wedge {
		fill: var(--accent);
	}
	.uring--over .wedge {
		fill: var(--color-amber-600);
	}
	@media (forced-colors: active) {
		.track {
			fill: Canvas;
			stroke: GrayText;
		}
		.wedge {
			fill: CanvasText;
		}
	}
</style>
