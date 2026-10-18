<script lang="ts">
	// The wordmark: the name in the heading face, behind the mark. The mark
	// is a block at rest in the crook of an L (DECISIONS I-779): on a 100
	// grid, a wall and a floor 22 thick in the text's ink, and a block of 38
	// in the landing's blue, --sh-accent, the one --sh-* token off the
	// landing, sitting on the floor with 16 of daylight between it and the
	// wall. Two flat shapes, no stroke and no grey, and nothing in it
	// thinner than an arm, so the same drawing holds at 16px: the favicon is
	// this file's geometry, not a heavier cut of it. The viewBox is cropped
	// to the drawing.
	// word=false draws the mark alone, for a header row on a phone that has
	// no room for both; the link around it carries the name. The 28px cut's
	// word sits 1px under centre, so its baseline is the header links'
	// baseline rather than a hair above it (I-655).
	let {
		size = 'base',
		mark = false,
		word = true
	}: { size?: 'sm' | 'base'; mark?: boolean; word?: boolean } = $props();
</script>

<span
	class="inline-flex items-center gap-2 font-display font-semibold tracking-tight {size === 'sm'
		? 'text-base'
		: 'text-lg'}"
	>{#if mark}<svg
			viewBox="6 6 88 88"
			class="{size === 'sm' ? 'h-6' : 'h-7'} w-auto shrink-0"
			aria-hidden="true"
			><path d="M6 6h22v66h66v22H6Z" fill="currentColor" /><rect
				x="44"
				y="34"
				width="38"
				height="38"
				fill="var(--sh-accent)"
			/></svg
		>{/if}{#if word}<span class={size === 'sm' ? undefined : 'translate-y-px'}>repose</span
		>{/if}</span
>
