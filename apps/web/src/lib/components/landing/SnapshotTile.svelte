<!--
  One snapshot, drawn the same way in every picture (F14, DECISIONS I-666):
  a head row with the pinwheel and the time, and under it, for the newest
  snapshot only, the machine in miniature (the `children` snippet: row
  shapes, never readable text). An older snapshot is its head alone
  (`stub`), stacked under the newest by the picture that draws it.

  `cause` adds a mark at the head's right end for why the snapshot was
  taken: `nightly` a moon, `user` a prompt (you asked, `repose snapshots
  create`), `stop` the wreck's cross (a stop snapshots first, even a
  wrecked machine). Without it, the head is the pinwheel and time alone.

  The tile sits inside a picture's role="img", so it carries no text for
  assistive tech of its own. Callers that animate the pinwheel set
  --turn on `.cam` (ComesBack's quarter turn), and `lit` turns the edge
  and the pinwheel blue, the restore's colour.
-->
<script lang="ts" module>
	export type SnapshotCause = 'nightly' | 'user' | 'stop';
</script>

<script lang="ts">
	import type { Snippet } from 'svelte';

	let {
		time,
		cause,
		stub = false,
		lit = false,
		class: cls = '',
		children
	}: {
		time: string;
		cause?: SnapshotCause;
		stub?: boolean;
		lit?: boolean;
		class?: string;
		children?: Snippet;
	} = $props();
</script>

<div class="snap {cls}" class:stub class:lit data-cause={cause}>
	<div class="s-head">
		<span class="cam"
			><svg viewBox="0 0 16 16" width="13" height="13" aria-hidden="true">
				<path d="M8 8V0A8 8 0 0 0 0 8Z" fill="currentColor" />
				<path d="M8 8V16A8 8 0 0 0 16 8Z" fill="currentColor" opacity="0.45" />
			</svg></span
		>
		<span class="s-time">{time}</span>
		{#if cause}
			<span class="cause">
				<svg viewBox="0 0 16 16" width="12" height="12" aria-hidden="true">
					{#if cause === 'nightly'}
						<path d="M10 2.2A6 6 0 1 0 14 10.4 4.9 4.9 0 0 1 10 2.2z" fill="currentColor" />
					{:else if cause === 'user'}
						<path
							d="M2.5 4l4 4-4 4M8.5 12.5h5"
							fill="none"
							stroke="currentColor"
							stroke-width="1.6"
							stroke-linecap="round"
							stroke-linejoin="round"
						/>
					{:else}
						<path d="M4 4l8 8M12 4l-8 8" fill="none" stroke="currentColor" stroke-width="1.8" />
					{/if}
				</svg>
			</span>
		{/if}
	</div>
	{#if !stub && children}
		<div class="s-mini">{@render children()}</div>
	{/if}
</div>

<style>
	/* The pictures' window: a --rule hairline, square corners,
	   the surface inside and a sunken head, as the machine panels are
	   drawn. The head is one height in every picture (I-673): with the
	   mini's top rule under it, it is a window head's --shot-head, so in
	   ComesBack the tile's head and the machine's sit on one line. */
	.snap {
		border: 1px solid var(--rule);
		background: var(--surface);
		overflow: hidden;
		color: var(--pic-ink);
		transition: border-color 0.3s;
	}
	.snap.lit {
		border-color: var(--pic-accent);
	}
	.s-head {
		display: flex;
		align-items: center;
		gap: 6px;
		height: calc(var(--shot-head, 28px) - 1px);
		padding: 0 8px;
		background: var(--sunken);
	}
	.cam {
		display: flex;
		color: var(--pic-dim);
		transition: color 0.2s;
	}
	.cam svg {
		transform: rotate(var(--turn, 0deg));
	}
	.lit .cam {
		color: var(--pic-accent);
	}
	.s-time {
		font-family: var(--font-mono);
		font-size: 12px;
		font-weight: 600;
		font-variant-numeric: tabular-nums;
	}
	.stub .s-time {
		color: var(--pic-dim);
	}
	.cause {
		display: flex;
		margin-left: auto;
		color: var(--pic-dim);
	}
	[data-cause='stop'] .cause {
		color: var(--pic-stop);
	}
	.s-mini {
		border-top: 1px solid var(--rule);
		overflow: hidden;
	}

	@media (max-width: 480px) {
		.s-head {
			padding: 0 6px;
			gap: 5px;
		}
		.s-time {
			font-size: 11px;
		}
	}
</style>
