<script lang="ts">
	// The wordmark: the name in the heading face, behind the mark. The mark
	// is a cell dividing (DECISIONS I-779): two lobes on a 45-degree axis,
	// the mother 23 in the text's ink and the daughter 21 in the landing's
	// blue, --sh-accent, drawn with a cleavage furrow of radius 18 tangent
	// to both, which pinches the outline inward to a bridge of 7. The
	// furrow is what makes it a division: two circles unioned bulge where
	// they meet, a dividing cell pulls in. The daughter is the smaller one
	// because the division has not finished. Geometry from
	// ops/dev/logo-mitosis.py, which prints these two paths.
	// The viewBox is cropped to the drawing.
	// word=false draws the mark alone, for a header row on a phone that has
	// no room for both; the link around it carries the name. The 28px cut's
	// word sits 1px under centre, so its baseline is the header links'
	// baseline rather than a hair above it (I-655).
	// divide plays the division once, for the header of a page a visitor
	// lands on; a pointer or keyboard focus on the link around the mark
	// plays it again. Both are off under prefers-reduced-motion, and a
	// browser without the CSS d property draws the resting state.
	let {
		size = 'base',
		mark = false,
		word = true,
		divide = false
	}: { size?: 'sm' | 'base'; mark?: boolean; word?: boolean; divide?: boolean } = $props();
</script>

<span
	class="inline-flex items-center gap-2 font-display font-semibold tracking-tight {size === 'sm'
		? 'text-base'
		: 'text-lg'}"
	>{#if mark}<svg
			viewBox="4 4 92 92"
			class="mark {size === 'sm' ? 'h-6' : 'h-7'} w-auto shrink-0"
			class:divide
			aria-hidden="true"
			><path
				class="mother"
				d="M32.5 49.5A23 23 0 1 1 49.5 32.5A18 18 0 0 0 54.3 49.4L49.4 54.3A18 18 0 0 0 32.5 49.5Z"
				fill="currentColor"
			/><path
				class="daughter"
				d="M70.6 54.3A21 21 0 1 1 54.3 70.6A18 18 0 0 0 49.4 54.3L54.3 49.4A18 18 0 0 0 70.6 54.3Z"
				fill="var(--sh-accent)"
			/></svg
		>{/if}{#if word}<span class={size === 'sm' ? undefined : 'translate-y-px'}>repose</span
		>{/if}</span
>

<style>
	/* The division, as a morph of the two paths from one body to two. Both
	   stages come out of ops/dev/logo-mitosis.py with the same command
	   order, which is what lets the d property interpolate; the lobes are
	   centred on the resting drawing, so they travel apart rather than the
	   whole mark sliding. 900ms with an ease in and out, inside the
	   landing's range of 120ms to 1.1s (LANDING.md, "The reference").
	   A browser without the CSS d property ignores all of this and draws
	   the attribute, the resting state. */
	@keyframes divide-mother {
		from {
			d: path(
				'M32.0 59.7A23 23 0 1 1 59.7 32.0A18 18 0 0 0 64.5 40.4L40.4 64.5A18 18 0 0 0 32.0 59.7Z'
			);
		}
	}
	@keyframes divide-daughter {
		from {
			d: path(
				'M71.4 44.8A21 21 0 1 1 44.8 71.4A18 18 0 0 0 40.4 64.5L64.5 40.4A18 18 0 0 0 71.4 44.8Z'
			);
		}
	}
	/* The same two stages again under a second name, so a pointer can
	   replay the division after the load animation has finished. */
	@keyframes redivide-mother {
		from {
			d: path(
				'M32.0 59.7A23 23 0 1 1 59.7 32.0A18 18 0 0 0 64.5 40.4L40.4 64.5A18 18 0 0 0 32.0 59.7Z'
			);
		}
	}
	@keyframes redivide-daughter {
		from {
			d: path(
				'M71.4 44.8A21 21 0 1 1 44.8 71.4A18 18 0 0 0 40.4 64.5L64.5 40.4A18 18 0 0 0 71.4 44.8Z'
			);
		}
	}

	.divide .mother {
		animation: divide-mother 900ms cubic-bezier(0.65, 0, 0.35, 1) both;
	}
	.divide .daughter {
		animation: divide-daughter 900ms cubic-bezier(0.65, 0, 0.35, 1) both;
	}

	:global(a:hover) .mark .mother,
	:global(a:focus-visible) .mark .mother {
		animation: redivide-mother 900ms cubic-bezier(0.65, 0, 0.35, 1);
	}
	:global(a:hover) .mark .daughter,
	:global(a:focus-visible) .mark .daughter {
		animation: redivide-daughter 900ms cubic-bezier(0.65, 0, 0.35, 1);
	}

	@media (prefers-reduced-motion: reduce) {
		.mark .mother,
		.mark .daughter,
		:global(a:hover) .mark .mother,
		:global(a:hover) .mark .daughter,
		:global(a:focus-visible) .mark .mother,
		:global(a:focus-visible) .mark .daughter {
			animation: none;
		}
	}
</style>
