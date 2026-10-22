<!--
  A circle filled to a fraction, clockwise from twelve o'clock: the pie of
  the shape set used as a measure, the way Isotype lets a shape's fill carry
  a quantity: the steps count up with it (a third, two thirds, done). Grey
  by default; the page gives the blue accent only to the one that says
  "done". A
  ring in --sh-light marks the whole, so a third reads as a third; the
  ring is part of the shape, so it takes the shapes' palette and not the
  page's rules (LANDING.md, "Shape language"). Always
  decorative: the number it shows is also in the text beside it.
-->
<script lang="ts">
	let { fraction, tone = 'neutral' }: { fraction: number; tone?: 'accent' | 'neutral' } = $props();

	const FILL = {
		accent: 'var(--sh-accent)',
		neutral: 'var(--sh-grey)'
	};

	// The wedge from twelve o'clock to the fraction, as an SVG path.
	let wedge = $derived.by(() => {
		const f = Math.min(Math.max(fraction, 0), 1);
		if (f >= 1) return '';
		const a = f * 2 * Math.PI;
		const x = 50 + 46 * Math.sin(a);
		const y = 50 - 46 * Math.cos(a);
		return `M50 50 V4 A46 46 0 ${f > 0.5 ? 1 : 0} 1 ${x.toFixed(2)} ${y.toFixed(2)} Z`;
	});
</script>

<svg viewBox="0 0 100 100" class="block h-full w-full" aria-hidden="true">
	<circle cx="50" cy="50" r="48" fill="none" stroke="var(--sh-light)" stroke-width="3" />
	{#if fraction >= 1}
		<circle cx="50" cy="50" r="46" fill={FILL[tone]} />
	{:else if fraction > 0}
		<path d={wedge} fill={FILL[tone]} />
	{/if}
</svg>
