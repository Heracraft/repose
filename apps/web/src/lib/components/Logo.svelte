<script lang="ts">
	// The wordmark: the mark, then "repose" in Noto Serif 600 (I-837). The
	// mark is a cell dividing (DECISIONS I-779, which supersedes I-363's
	// traced cross): two lobes on a 45-degree axis, the mother 23 in the
	// text's ink and the daughter 21 in the accent, drawn with a cleavage
	// furrow of radius 18 tangent to both, which pinches the outline inward
	// to a bridge of 7. The furrow is what makes it a division: two circles
	// unioned bulge where they meet, a dividing cell pulls in. The daughter
	// is the smaller one because the division has not finished. Geometry
	// from ops/dev/logo-mitosis.py, which prints these two paths. The
	// viewBox is cropped to the drawing. The daughter is the kit's accent,
	// the one brand use of the blue (I-837); I-779 drew it in the landing's
	// --sh-accent, which is the same blue before the kit named it.
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
	>{#if mark}<svg viewBox="4 4 92 92" class="mark" class:divide aria-hidden="true"
			><path
				class="mother"
				d="M32.5 49.5A23 23 0 1 1 49.5 32.5A18 18 0 0 0 54.3 49.4L49.4 54.3A18 18 0 0 0 32.5 49.5Z"
				fill="currentColor"
			/><path
				class="daughter"
				d="M70.6 54.3A21 21 0 1 1 54.3 70.6A18 18 0 0 0 49.4 54.3L54.3 49.4A18 18 0 0 0 70.6 54.3Z"
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
