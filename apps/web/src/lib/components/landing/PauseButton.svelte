<!--
  The pause button of a looping picture (WCAG 2.2.2, DECISIONS I-666). It
  stands in the picture's figure bar, at its right end (I-673, I-961):
  every picture on the landing sits under a --bar-h bar holding its
  number, and the frame (the element with `use:picture.frame`) gives up
  its positioning there, so the button is placed from the figure
  (landing.css, .fig), centred in the bar, its chip's right edge on the
  text line (--pause-r, else --edge). It is never over a head or a
  picture's content. It is 28px with an invisible 44px
  hit area, shows on hover or when anything in the frame (the element
  with `use:picture.frame`) has focus, and is in the tab order throughout.
  aria-pressed is true while the picture is paused. It is rendered only
  while the loop runs, so there is none with reduced motion or without
  scripts.
-->
<script lang="ts">
	import type { Picture } from './motion.svelte';

	// `name` says which picture the button pauses, so five buttons in a
	// screen reader's list or the tab order are told apart:
	// "Pause the ports picture". aria-pressed carries the state.
	let { picture, name }: { picture: Picture; name: string } = $props();
</script>

{#if picture.moving}
	<button
		type="button"
		class="pic-pause"
		aria-label="Pause the {name} picture"
		aria-pressed={picture.paused}
		onclick={() => picture.toggle()}
	>
		<svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true">
			{#if picture.paused}
				<path
					d="M5 3.5v9l7.5-4.5z"
					fill="currentColor"
					stroke="currentColor"
					stroke-width="1"
					stroke-linejoin="round"
				/>
			{:else}
				<path d="M5.5 3.5v9M10.5 3.5v9" fill="none" stroke="currentColor" stroke-width="2" />
			{/if}
		</svg>
	</button>
{/if}

<style>
	:global(.pic-frame) {
		position: relative;
	}
	/* A picture control: the --rule-strong hairline and surface of the
	   pictures' tags, with the kit's smallest radius as a button,
	   --pic-dim at rest. In the figure bar over the picture, centred in
	   the bar's height, its chip's right edge on the text line. */
	.pic-pause {
		--pp: 28px;
		position: absolute;
		top: calc((var(--bar-h, 40px) - var(--pp)) / 2);
		right: calc(var(--pause-r, var(--edge, 0px)) - (var(--pp) - 28px) / 2);
		z-index: 5;
		display: grid;
		place-items: center;
		width: var(--pp);
		height: var(--pp);
		padding: 0;
		border: 1px solid var(--rule-strong);
		border-radius: var(--hk-radius-xs);
		background: var(--surface);
		color: var(--pic-dim);
		cursor: pointer;
		opacity: 0;
		transition:
			opacity 0.15s,
			color 0.15s,
			border-color 0.15s;
	}
	/* 44px to a finger or a pointer, drawn nowhere; positioned, so it
	   moves nothing. */
	.pic-pause::after {
		content: '';
		position: absolute;
		inset: -9px;
	}
	:global(:hover) > .pic-pause,
	:global(:focus-within) > .pic-pause,
	.pic-pause[aria-pressed='true'] {
		opacity: 1;
	}
	.pic-pause:hover {
		color: var(--pic-ink);
		border-color: var(--pic-dim);
	}
	.pic-pause[aria-pressed='true'] {
		color: var(--pic-accent);
		border-color: var(--pic-accent);
	}
	/* No hover on a touch screen: the button is always shown there. */
	@media (hover: none) {
		.pic-pause {
			opacity: 1;
		}
	}
	/* To a finger the button itself is 44px, so it counts as a 44px target;
	   the chip stays 28px, drawn by ::before in the middle of it. */
	@media (pointer: coarse) {
		.pic-pause {
			--pp: 44px;
			isolation: isolate;
			border-color: transparent;
			background: none;
		}
		.pic-pause::before {
			content: '';
			position: absolute;
			inset: 8px;
			z-index: -1;
			border: 1px solid var(--rule-strong);
			border-radius: var(--hk-radius-xs);
			background: var(--surface);
		}
		.pic-pause::after {
			inset: 0;
		}
		.pic-pause:hover::before {
			border-color: var(--pic-dim);
		}
		.pic-pause[aria-pressed='true']::before {
			border-color: var(--pic-accent);
		}
	}
	@media (prefers-reduced-motion: reduce) {
		.pic-pause {
			display: none;
		}
	}
</style>
