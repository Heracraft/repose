<script lang="ts">
	// The wordmark: the mark, then "repose" in Noto Serif 600 (I-837). The
	// mark is a cell dividing (DECISIONS I-779, furrow narrowed in I-936):
	// two lobes on a 45-degree axis with their centres 52 apart, the mother
	// 23 in the text's ink and the daughter 21 in the accent, drawn with a
	// cleavage furrow of radius 5.6 tangent to both, which pinches the
	// outline inward to a bridge of 7. The furrow is what makes it a
	// division: two circles unioned bulge where they meet, a dividing cell
	// pulls in. A furrow this size creases the lobes over 19 degrees of
	// their circumference, so they stay round; I-779's radius of 18 took 32
	// and drew the mother out into a teardrop. The daughter is the smaller
	// one because the division has not finished. Geometry from
	// ops/dev/logo-mitosis.py, which prints the viewBox, these two paths
	// and the stage the keyframes morph from. The viewBox is cropped to the
	// drawing. The daughter is the kit's accent, the one brand use of the
	// blue (I-837); I-779 drew it in the landing's --sh-accent, which is
	// the same blue before the kit named it.
	// word=false draws the mark alone, for a header row on a phone that has
	// no room for both; the link around it carries the name.
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

<span class="logo logo--{size}"
	>{#if mark}<svg viewBox="9 9 82 82" class="mark" class:divide aria-hidden="true"
			><path
				class="mother"
				d="M42.7 53.3A23 23 0 1 1 53.3 42.7A5.6 5.6 0 0 0 54.3 49.2L49.2 54.3A5.6 5.6 0 0 0 42.7 53.3Z"
				fill="currentColor"
			/><path
				class="daughter"
				d="M60.6 50.3A21 21 0 1 1 50.3 60.6A5.6 5.6 0 0 0 49.2 54.3L54.3 49.2A5.6 5.6 0 0 0 60.6 50.3Z"
			/></svg
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
		height: 1.75rem;
	}
	.logo--sm .mark {
		height: 1.5rem;
	}
	.daughter {
		fill: var(--hk-accent);
	}
	/* The serif's cap height sits high in its line box; one pixel down puts
	   the word's baseline on the header links' baseline (I-655). */
	.logo--base .word {
		transform: translateY(1px);
	}

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
				'M37.0 62.5A23 23 0 1 1 62.5 37.0A5.6 5.6 0 0 0 64.1 40.3L40.3 64.1A5.6 5.6 0 0 0 37.0 62.5Z'
			);
		}
	}
	@keyframes divide-daughter {
		from {
			d: path(
				'M66.9 41.8A21 21 0 1 1 41.8 66.9A5.6 5.6 0 0 0 40.3 64.1L64.1 40.3A5.6 5.6 0 0 0 66.9 41.8Z'
			);
		}
	}
	/* The same two stages again under a second name, so a pointer can
	   replay the division after the load animation has finished. */
	@keyframes redivide-mother {
		from {
			d: path(
				'M37.0 62.5A23 23 0 1 1 62.5 37.0A5.6 5.6 0 0 0 64.1 40.3L40.3 64.1A5.6 5.6 0 0 0 37.0 62.5Z'
			);
		}
	}
	@keyframes redivide-daughter {
		from {
			d: path(
				'M66.9 41.8A21 21 0 1 1 41.8 66.9A5.6 5.6 0 0 0 40.3 64.1L64.1 40.3A5.6 5.6 0 0 0 66.9 41.8Z'
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
