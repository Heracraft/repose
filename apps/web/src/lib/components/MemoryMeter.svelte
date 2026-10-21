<!--
  Memory running against the plan (I-674, I-837): one meter with the
  anatomy of Meter.svelte (label and reading over a 6px bar), so it reads
  as a set with Disk held and Egress beside it. The owner's call
  (2026-10-21): what is used against the plan, not who uses it; the split
  per project lives on /projects.
    running  the accent, the colour of the Disk held and Egress fills
    frozen   the ice ink, as on every frozen machine
    left     the empty track
    over     past a tick at the plan's limit, in amber
  The bar holds the plan, or what runs when that is more. Colour is never
  the only carrier: the line under the bar says each figure in words, and
  the meter's value text reads the same.
-->
<script lang="ts">
	import type { Snippet } from 'svelte';
	import { FROZEN_LOOK } from '$lib/flags';
	import { gbs } from '$lib/format';

	let {
		label = 'Memory running',
		running,
		frozen = 0,
		limit,
		testid = 'meter-running-now',
		children
	}: {
		label?: string;
		/** GB that machines running now hold, frozen ones left out. */
		running: number;
		/** GB that frozen machines hold. */
		frozen?: number;
		/** The plan's memory in GB. */
		limit: number;
		testid?: string;
		/** A note under the line, inside the meter's cell. */
		children?: Snippet;
	} = $props();

	const r1 = (n: number) => Math.round(n * 10) / 10;
	let used = $derived(r1(running + frozen));
	let over = $derived(r1(Math.max(0, used - limit)));
	let left = $derived(r1(Math.max(0, limit - used)));
	let scale = $derived(Math.max(limit, used, 1));
	/** Frozen is named only where the dashboard draws frozen machines. */
	let showFrozen = $derived(FROZEN_LOOK || frozen > 0);

	// Running from the left, frozen after it, both cut at the plan's limit;
	// what lies past the limit is one amber span after the tick. A 2px gap
	// parts the spans, so they stay apart in forced colours too.
	let spans = $derived.by(() => {
		const out: { kind: 'run' | 'ice' | 'over'; start: number; gb: number }[] = [];
		const inPlan = (start: number, gb: number) => Math.max(0, Math.min(start + gb, limit) - start);
		const run = inPlan(0, running);
		if (run > 0) out.push({ kind: 'run', start: 0, gb: run });
		const ice = inPlan(run, frozen);
		if (ice > 0) out.push({ kind: 'ice', start: run, gb: ice });
		if (over > 0)
			out.push({ kind: 'over', start: Math.max(limit, 0), gb: used - Math.max(limit, 0) });
		return out;
	});
	const pct = (gb: number) => `${(gb / scale) * 100}%`;

	let reading = $derived(`${gbs(used)} of ${gbs(limit)}${over ? ' · over' : ''}`);
	let parts = $derived(
		[
			`${gbs(r1(running))} running`,
			showFrozen ? `${gbs(r1(frozen))} frozen` : '',
			over ? `${gbs(over)} over` : `${gbs(left)} left`
		].filter(Boolean)
	);
	let valueText = $derived(
		`${gbs(used)} of ${gbs(limit)}${over ? ', over' : ''}: ${parts.join(', ')}`
	);
</script>

<div class="hk-meter mm" class:hk-meter--neutral={used === 0} data-testid={testid}>
	<div class="hk-meter-top">
		<span class="hk-meter-label">{label}</span>
		<span class="hk-meter-value">{reading}</span>
	</div>
	<!-- aria-valuenow stops at the limit, which is all a meter can hold, so
	     aria-valuetext gives the real reading, over and its split included. -->
	<div
		class="bar"
		role="meter"
		aria-label={label}
		aria-valuemin={0}
		aria-valuemax={limit}
		aria-valuenow={Math.min(used, limit)}
		aria-valuetext={valueText}
	>
		<div class="clip">
			{#each spans as s (s.kind)}
				<i
					class="seg seg--{s.kind}"
					style:left={pct(s.start)}
					style:width={s.start + s.gb >= scale ? pct(s.gb) : `calc(${pct(s.gb)} - 2px)`}
				></i>
			{/each}
		</div>
		{#if over}
			<i class="tick" style:left="calc({pct(limit)} - 1.5px)"></i>
		{/if}
	</div>
	<p class="split" data-testid="memory-split">
		<span><i class="sw sw--run" aria-hidden="true"></i>{gbs(r1(running))} running</span>
		{#if showFrozen}
			<span><i class="sw sw--ice" aria-hidden="true"></i>{gbs(r1(frozen))} frozen</span>
		{/if}
		{#if over}
			<span class="is-over"><i class="sw sw--over" aria-hidden="true"></i>{gbs(over)} over</span>
		{:else}
			<span><i class="sw sw--left" aria-hidden="true"></i>{gbs(left)} left</span>
		{/if}
	</p>
	{@render children?.()}
</div>

<style>
	.mm {
		/* Amber past the plan: between the dot and the warn text, 3.2:1 on the
		   light page (the dot alone is 2.06:1); brighter still at night. */
		--over: color-mix(in srgb, var(--hk-warn-dot) 60%, var(--hk-warn));
		/* The kit's track: the accent's tint, or grey with nothing running
		   (hk-meter--neutral, as Egress at 0). Never red or amber: the spans
		   carry the state, and amber past the tick says over. */
		--free: color-mix(in srgb, var(--m) 16%, transparent);
	}

	/* The kit's meter track: 6px, a pill; the tick stands 3px proud. */
	.bar {
		position: relative;
		height: 6px;
		margin-block: 3px;
	}
	.clip {
		position: absolute;
		inset: 0;
		overflow: hidden;
		border-radius: var(--hk-radius-pill);
		background: var(--free);
	}
	.seg {
		position: absolute;
		top: 0;
		bottom: 0;
		transition:
			left var(--hk-dur-meter) var(--hk-ease),
			width var(--hk-dur-meter) var(--hk-ease);
	}
	.seg--run {
		background: var(--hk-accent);
	}
	.seg--ice {
		background: var(--hk-ice-ink);
	}
	.seg--over {
		background: var(--over);
	}
	.tick {
		position: absolute;
		top: -3px;
		bottom: -3px;
		width: 1px;
		background: var(--hk-text);
	}

	/* The split in words, each with its key. */
	.split {
		display: flex;
		flex-wrap: wrap;
		gap: 0.25rem 0.875rem;
		margin: 0.75rem 0 0;
		font-size: 0.8125rem;
		line-height: 1.25rem;
		color: var(--hk-text-3);
		font-variant-numeric: tabular-nums;
	}
	.split span {
		display: inline-flex;
		align-items: center;
		gap: 0.375rem;
		white-space: nowrap;
	}
	.split .is-over {
		color: var(--hk-warn);
	}
	.sw {
		flex: none;
		width: 8px;
		height: 8px;
		border-radius: 2px;
	}
	.sw--run {
		background: var(--hk-accent);
	}
	.sw--ice {
		background: var(--hk-ice-ink);
	}
	.sw--over {
		background: var(--over);
	}
	/* Left is the empty track: its key is the track with an edge. */
	.sw--left {
		background: var(--free);
		box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--m) 45%, transparent);
	}

	@media (prefers-reduced-motion: reduce) {
		.seg {
			transition: none;
		}
	}

	/* Forced colours: running is CanvasText, frozen GrayText, over
	   Highlight; the 2px gaps keep the spans apart and the words under
	   the bar carry the rest. */
	@media (forced-colors: active) {
		.clip,
		.seg,
		.sw,
		.tick {
			forced-color-adjust: none;
		}
		.clip {
			background: Canvas;
			box-shadow: inset 0 0 0 1px GrayText;
		}
		.seg--run,
		.sw--run,
		.tick {
			background: CanvasText;
		}
		.seg--ice,
		.sw--ice {
			background: GrayText;
		}
		.seg--over,
		.sw--over {
			background: Highlight;
		}
		.sw--left {
			background: Canvas;
			box-shadow: inset 0 0 0 1px GrayText;
		}
	}
</style>
