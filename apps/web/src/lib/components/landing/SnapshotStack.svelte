<!--
  A machine's snapshots, newest first: the newest as a whole tile with its
  miniature (`mini`), each older one a head-only stub under it, 4px apart
  (F14, DECISIONS I-666). Pictures that move tiles between these places
  (the hero, ComesBack) use SnapshotTile directly and keep this order.
-->
<script lang="ts">
	import type { Snippet } from 'svelte';
	import SnapshotTile, { type SnapshotCause } from './SnapshotTile.svelte';

	let {
		snapshots,
		lit = false,
		mini
	}: {
		snapshots: { time: string; cause?: SnapshotCause }[];
		lit?: boolean;
		mini?: Snippet;
	} = $props();
</script>

<div class="snaps">
	{#each snapshots as s, i (s.time)}
		<SnapshotTile time={s.time} cause={s.cause} stub={i > 0} lit={lit && i === 0} children={mini} />
	{/each}
</div>

<style>
	.snaps {
		display: flex;
		flex-direction: column;
		gap: 4px;
		min-width: 0;
	}
</style>
