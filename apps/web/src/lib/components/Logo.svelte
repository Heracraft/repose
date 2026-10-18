<script lang="ts">
	// The wordmark: the name in the heading face, behind the mark. The mark
	// is a copy being made, caught just before it finishes (DECISIONS
	// I-779): on a 100 grid a disc of 34 at 38,38 in the text's ink is the
	// original, and the accent shape is what has so far cleared it of a
	// second disc of 33 at 61,61 — the copy, a little smaller because it is
	// not finished, holding a seam of 5 all the way round. --sh-accent is
	// the one --sh-* token off the landing, and the disc is the landing's
	// sphere. Two flat shapes, no stroke and no grey, and the crescent is
	// 20 wide at its widest, so the same drawing holds at 16px: the favicon
	// is this file's geometry, not a heavier cut. The viewBox is cropped to
	// the drawing. The curve is the one the mark is allowed; the rest of
	// the foundation is rectangles (I-779).
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
			viewBox="4 4 91 91"
			class="{size === 'sm' ? 'h-6' : 'h-7'} w-auto shrink-0"
			aria-hidden="true"
			><circle cx="38" cy="38" r="34" fill="currentColor" /><path
				d="M34.1 76.8A32 32 0 0 0 76.8 34.1A39 39 0 0 1 34.1 76.8Z"
				fill="var(--sh-accent)"
			/></svg
		>{/if}{#if word}<span class={size === 'sm' ? undefined : 'translate-y-px'}>repose</span
		>{/if}</span
>
