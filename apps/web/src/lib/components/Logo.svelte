<script lang="ts">
	// The wordmark: the name in the heading face, behind the mark. The mark
	// is the owner's notebook sketch, traced (DECISIONS I-363): a thin cross
	// in the text's ink, its crossing left of centre and low, and three
	// blocks hugging the crossing: a skinny one in grey above-left, a middle
	// square in a fainter grey above-right on the arm (both the mark's own
	// greys, see the style block), and the big square in the landing's blue,
	// --sh-accent, below-right: the one --sh-* token off the landing. Flat, no stroke;
	// the favicon is this same drawing. The viewBox is cropped to the
	// drawing so the thin stems get every pixel the header gives the mark.
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
			viewBox="13 4 74 92"
			class="{size === 'sm' ? 'h-6' : 'h-7'} w-auto shrink-0"
			aria-hidden="true"
			><rect x="36" y="18" width="7" height="32" class="mark-grey" /><rect
				x="47"
				y="36"
				width="12"
				height="14"
				class="mark-light"
			/><rect x="47" y="54" width="20" height="18" fill="var(--sh-accent)" /><rect
				x="43"
				y="4"
				width="4"
				height="92"
				fill="currentColor"
			/><rect x="13" y="50" width="74" height="4" fill="currentColor" /></svg
		>{/if}{#if word}<span class={size === 'sm' ? undefined : 'translate-y-px'}>repose</span
		>{/if}</span
>

<style>
	/* The two grey blocks are the mark's own greys, not the landing's shape
	   greys: at header size the blocks are a few pixels wide, and the shape
	   greys that read on a 200px drawing vanish there. In the light the
	   shapes' zinc-300 sat at 1.5:1 on --page and zinc-400 at 2.5:1; the mark
	   takes zinc-500 for the skinny block (4.8:1) and --control-edge for the
	   middle one (3.4:1), so both hold 3:1 and the middle block stays the
	   fainter of the two. In the dark, --sh-light (zinc-600) sat at 2.7:1;
	   the skinny block steps to zinc-400 (7.4:1) and the middle one stays
	   --control-edge, whose dark value holds 3.7:1. The token, not its hex,
	   so the mark follows the floor I-370 keeps on it (I-393). The
	   landing's large shapes keep their own greys. */
	.mark-grey {
		fill: var(--color-zinc-500);
	}
	.mark-light {
		fill: var(--control-edge);
	}
	@media (prefers-color-scheme: dark) {
		.mark-grey {
			fill: var(--color-zinc-400);
		}
	}
</style>
