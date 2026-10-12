<!--
  A six-armed snowflake, drawn in the current colour: the frozen mark
  (DECISIONS I-683). Round ends, thin strokes, so it stays a flake at 13px.
  Decorative: the word beside it says "frozen".
-->
<script lang="ts">
	let { size = 14 }: { size?: number } = $props();
</script>

<svg
	viewBox="0 0 16 16"
	width={size}
	height={size}
	fill="none"
	stroke="currentColor"
	stroke-width="1.25"
	stroke-linecap="round"
	stroke-linejoin="round"
	aria-hidden="true"
	class="shrink-0"
>
	<!-- Six arms, each with one fork near its tip: at 12 to 14px a second
	     fork closer in filled the middle and read as a blot. -->
	{#each [0, 60, 120, 180, 240, 300] as a (a)}
		<g transform="rotate({a} 8 8)">
			<path d="M8 8V1.25" />
			<path d="M6.4 2.6 8 4.1l1.6-1.5" />
		</g>
	{/each}
</svg>
