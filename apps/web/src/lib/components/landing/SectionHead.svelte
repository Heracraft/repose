<!--
  The head every landing section shares: the bold serif title and one
  sentence, inset from the rails. A section about one feature carries that
  feature's shape before its title; the shape then stands in the footer's
  row (docs/LANDING.md, "Shape language"). An aside is a reading level
  with the title at its right (pricing's seats left), under it on a phone.
-->
<script lang="ts">
	import type { Snippet } from 'svelte';
	import Shape, { type Kind } from './Shape.svelte';

	let {
		title,
		id,
		shape,
		aside,
		children
	}: { title: string; id?: string; shape?: Kind; aside?: Snippet; children?: Snippet } = $props();
</script>

<header class="sec-head inset">
	<div class="sec-title">
		<h2 {id} class="landing-h">
			{#if shape}<span class="head-mark" aria-hidden="true"><Shape kind={shape} /></span
				>{/if}{title}
		</h2>
		{#if aside}{@render aside()}{/if}
	</div>
	{#if children}
		<p class="lead">{@render children()}</p>
	{/if}
</header>
