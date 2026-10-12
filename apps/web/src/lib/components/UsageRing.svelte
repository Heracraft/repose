<!--
  A share of a plan limit as a small ring beside its figure (I-674): the
  figure carries the reading, the ring is a glance at how full it is. It
  replaces the long bars where a page held several of them. Accent up to
  the limit; past it the ring closes in amber, and the words say "over"
  too, so the state is never colour alone.
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

	const R = 7;
	const C = 2 * Math.PI * R;
	let over = $derived(limit > 0 && used > limit);
	// A sliver stays visible for any use at all, so 0.4% is not read as none.
	let frac = $derived(over ? 1 : used > 0 ? Math.max(share(used, limit), 0.04) : 0);
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
	<circle class="track" cx="9" cy="9" r={R} />
	{#if frac > 0}
		<circle
			class="arc"
			cx="9"
			cy="9"
			r={R}
			stroke-dasharray="{frac * C} {C}"
			transform="rotate(-90 9 9)"
		/>
	{/if}
</svg>

<style>
	.uring {
		flex: none;
		display: block;
	}
	circle {
		fill: none;
		stroke-width: 2.5;
	}
	.track {
		stroke: var(--rule-strong);
	}
	.arc {
		stroke: var(--accent);
		stroke-linecap: round;
		transition: stroke-dasharray 400ms cubic-bezier(0.33, 1, 0.68, 1);
	}
	.uring--over .arc {
		stroke: var(--color-amber-600);
	}
	@media (prefers-reduced-motion: reduce) {
		.arc {
			transition: none;
		}
	}
	@media (forced-colors: active) {
		.track {
			stroke: GrayText;
		}
		.arc {
			stroke: CanvasText;
		}
	}
</style>
