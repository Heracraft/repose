<!--
  The head of every window the landing draws (I-669, I-673): a sunken row
  --shot-head (28px) tall, 10px in from the window's sides, with the
  place's 16px mark and its name at 12px, the same in the hero, the
  setup section, the feature grid and Ready. `machine` is the cloud and
  "your cloud machine" with the green running dot; `laptop` is the laptop
  and "your laptop"; `snapshots` is the hero's snapshots panel, whose
  pinwheel the hero passes as `mark` so its loop can turn it. Whatever
  the caller puts in `children` follows the name (Localhost's address
  bar, OneCommand's ready time, which brings its own dot: `dot={false}`).
-->
<script lang="ts">
	import type { Snippet } from 'svelte';

	const NAME = {
		machine: 'your cloud machine',
		laptop: 'your laptop',
		snapshots: 'snapshots'
	} as const;

	let {
		who,
		mark,
		dot = who === 'machine',
		children
	}: {
		who: keyof typeof NAME;
		mark?: Snippet;
		dot?: boolean;
		children?: Snippet;
	} = $props();
</script>

<div class="shot-head">
	{#if mark}
		{@render mark()}
	{:else}
		<svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true">
			{#if who === 'machine'}
				<path
					d="M7 18.5h10.5a4 4 0 0 0 .6-7.96A5.5 5.5 0 0 0 7.4 9.1 4.7 4.7 0 0 0 7 18.5z"
					fill="none"
					stroke="currentColor"
					stroke-width="1.5"
					stroke-linejoin="round"
				/>
			{:else if who === 'laptop'}
				<g fill="none" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round">
					<rect x="4.5" y="5" width="15" height="10.5" rx="1" />
					<path d="M2 18.5h20" stroke-linecap="round" />
				</g>
			{:else}
				<g transform="translate(4 4)">
					<path d="M8 8V0A8 8 0 0 0 0 8Z" fill="currentColor" />
					<path d="M8 8V16A8 8 0 0 0 16 8Z" fill="currentColor" opacity="0.45" />
				</g>
			{/if}
		</svg>
	{/if}
	<span class="who">{NAME[who]}</span>
	{@render children?.()}
	{#if dot}<i class="win-dot"></i>{/if}
</div>

<style>
	.shot-head {
		display: flex;
		flex: none;
		align-items: center;
		gap: 8px;
		height: var(--shot-head, 28px);
		padding: 0 10px;
		border-bottom: 1px solid var(--rule);
		background: var(--sunken);
		font-size: 12px;
		line-height: 16px;
		color: var(--pic-ink);
	}
	.shot-head > :global(svg) {
		flex: none;
		width: 16px;
		height: 16px;
	}
	.who {
		flex: none;
		font-weight: 600;
		white-space: nowrap;
	}
	.win-dot {
		flex: none;
		width: 7px;
		height: 7px;
		margin-left: auto;
		border-radius: 50%;
		background: var(--color-emerald-500);
		transition: background-color 0.2s;
	}
</style>
