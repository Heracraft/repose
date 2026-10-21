<script lang="ts">
	// The wordmark: the mark, then "repose" in Noto Serif 600 (I-363, I-837).
	// The mark is the owner's notebook sketch, traced: a thin cross in the
	// text's ink, its crossing left of centre and low, and three blocks
	// hugging the crossing. The big square below-right is the accent, the
	// one brand use of the blue; the two small ones are greys that hold 3:1
	// on the page in both themes (--hk-text-3 and the control edge). Flat,
	// no stroke; the favicon is this same drawing. word=false draws the mark
	// alone, for a phone's header row; the link around it carries the name.
	let {
		size = 'base',
		mark = false,
		word = true
	}: { size?: 'sm' | 'base'; mark?: boolean; word?: boolean } = $props();
</script>

<span class="logo logo--{size}"
	>{#if mark}<svg viewBox="13 4 74 92" class="mark" aria-hidden="true"
			><rect x="36" y="18" width="7" height="32" class="grey" /><rect
				x="47"
				y="36"
				width="12"
				height="14"
				class="light"
			/><rect x="47" y="54" width="20" height="18" class="blue" /><rect
				x="43"
				y="4"
				width="4"
				height="92"
				fill="currentColor"
			/><rect x="13" y="50" width="74" height="4" fill="currentColor" /></svg
		>{/if}{#if word}<span class="word">repose</span>{/if}</span
>

<style>
	.logo {
		display: inline-flex;
		align-items: center;
		gap: 0.5rem;
		font-family: var(--hk-font-display);
		font-weight: 600;
		letter-spacing: -0.01em;
		color: var(--hk-text);
		line-height: 1;
	}
	.logo--base {
		font-size: 1.125rem;
	}
	.logo--sm {
		font-size: 1rem;
	}
	.mark {
		flex: none;
		width: auto;
		height: 1.625rem;
	}
	.logo--sm .mark {
		height: 1.375rem;
	}
	.grey {
		fill: var(--hk-text-3);
	}
	.light {
		fill: var(--hk-control-edge);
	}
	.blue {
		fill: var(--hk-accent);
	}
	/* The serif's cap height sits high in its line box; one pixel down puts
	   the word's baseline on the header links' baseline. */
	.logo--base .word {
		transform: translateY(1px);
	}
</style>
