<!--
  One measure of a machine over a window, as a thin line over a faint area
  (DECISIONS I-492, I-837). One series, so no legend: the label names it
  and the reading beside it ("now 12%, peak 100%") carries the numbers, as
  a Meter's does. The line is the accent over its wash; nothing rides on
  colour. The dashed lines are
  the top and the middle of the scale, each labelled above itself. A gap
  is a time the machine was stopped or did not report.
  Hover, or focus and the arrow keys, show one bucket's time and value.
-->
<script lang="ts">
	import { segments, paths, peak, latest, nearest, type Pt } from '$lib/usage';

	let {
		label,
		points,
		max,
		stepS,
		start,
		end,
		format,
		maxLabel
	}: {
		label: string;
		points: Pt[];
		/** The top of the y axis: 1 for a share, the class's memory for memory. */
		max: number;
		stepS: number;
		/** The window, in ms since the epoch, so the x axis is the window and not the data. */
		start: number;
		end: number;
		format: (v: number) => string;
		/** The words at the top gridline ("100%", "8 GB"). */
		maxLabel: string;
	} = $props();

	// The box is 80px whatever the data, so a poll swaps path data and
	// nothing around the chart moves. TOP leaves room for the top line's
	// label above it.
	const H = 80;
	const TOP = 15;
	const mid = (H + TOP) / 2;
	let width = $state(0);
	let hover = $state<number | undefined>(undefined);

	let x = $derived((t: number) => (end > start ? ((t - start) / (end - start)) * width : 0));
	let y = $derived((v: number) => TOP + (1 - Math.min(Math.max(v / max, 0), 1)) * (H - TOP));
	let runs = $derived(segments(points, stepS).map((r) => ({ r, ...paths(r, x, y, H) })));
	let reported = $derived(points.filter((p) => p.v !== null));
	let top = $derived(peak(points));
	let now = $derived(latest(points));
	let reading = $derived(
		now === undefined ? 'no data' : `now ${format(now)}, peak ${format(top ?? now)}`
	);
	let hp = $derived(hover !== undefined ? reported[hover] : undefined);

	function onMove(e: PointerEvent) {
		const box = (e.currentTarget as SVGElement).getBoundingClientRect();
		if (reported.length === 0 || box.width === 0) return;
		const t = start + ((e.clientX - box.left) / box.width) * (end - start);
		hover = nearest(reported, t);
	}

	function onKey(e: KeyboardEvent) {
		if (reported.length === 0) return;
		if (e.key === 'ArrowLeft' || e.key === 'ArrowRight') {
			e.preventDefault();
			const step = e.key === 'ArrowLeft' ? -1 : 1;
			const from = hover ?? (step < 0 ? reported.length : -1);
			hover = Math.min(Math.max(from + step, 0), reported.length - 1);
		} else if (e.key === 'Escape') {
			hover = undefined;
		}
	}

	/** The left end of the x axis: "1 hour ago", "24 hours ago", "7 days ago". */
	let ago = $derived.by(() => {
		const h = Math.round((end - start) / 3600_000);
		if (h >= 48) return `${Math.round(h / 24)} days ago`;
		return h === 1 ? '1 hour ago' : `${h} hours ago`;
	});

	function when(t: number): string {
		const d = new Date(t);
		const span = end - start;
		const time = d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });
		return span > 26 * 3600_000
			? `${d.toLocaleDateString(undefined, { weekday: 'short' })} ${time}`
			: time;
	}
</script>

<figure class="usage-chart" data-testid="chart-{label.toLowerCase().replace(/[^a-z0-9]+/g, '-')}">
	<figcaption class="cap">
		<span class="cap-label">{label}</span>
		<span class="cap-reading">{reading}</span>
	</figcaption>
	<div class="plot" bind:clientWidth={width}>
		<!-- The pointer and the arrow keys move a readout of one point. -->
		<!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions -->
		<svg
			width={width || '100%'}
			height={H}
			class="svg"
			role="img"
			aria-label="{label}: {reading}. Arrow keys read one point at a time."
			tabindex={reported.length > 0 ? 0 : -1}
			onpointermove={onMove}
			onpointerleave={() => (hover = undefined)}
			onkeydown={onKey}
			onblur={() => (hover = undefined)}
		>
			<line class="grid" x1="0" x2={width} y1={TOP + 0.5} y2={TOP + 0.5} />
			<line class="grid" x1="0" x2={width} y1={mid + 0.5} y2={mid + 0.5} />
			<line class="base" x1="0" x2={width} y1={H - 0.5} y2={H - 0.5} />
			<text class="axis" x="0" y={TOP - 3}>{maxLabel}</text>
			<text class="axis" x="0" y={mid - 3}>{format(max / 2)}</text>
			{#if width > 0}
				{#each runs as run, i (i)}
					{#if run.r.length === 1}
						<circle cx={x(run.r[0].t)} cy={y(run.r[0].v)} r="2" class="pt" />
					{:else}
						<path d={run.area} class="area" />
						<path d={run.line} class="line" />
					{/if}
				{/each}
				{#if hp && hp.v !== null}
					<line class="cursor" x1={x(hp.t)} x2={x(hp.t)} y1={TOP} y2={H} />
					<circle class="mark" cx={x(hp.t)} cy={y(hp.v)} r="3.5" />
				{/if}
			{/if}
		</svg>
		{#if hp && hp.v !== null}
			<div
				class="tip"
				style="left: {Math.min(Math.max(x(hp.t), 60), Math.max(width - 60, 60))}px"
				aria-live="polite"
			>
				<span class="tip-when">{when(hp.t)}</span>
				<span class="tip-v">{format(hp.v)}</span>
			</div>
		{/if}
	</div>
	<div class="ends" aria-hidden="true">
		<span>{ago}</span>
		<span>now</span>
	</div>
</figure>

<style>
	.usage-chart {
		min-width: 0;
		margin: 0;
	}
	.cap {
		display: flex;
		flex-wrap: wrap;
		align-items: baseline;
		justify-content: space-between;
		gap: 0 0.75rem;
		font-size: 0.8125rem;
	}
	.cap-label {
		font-weight: 500;
		color: var(--hk-text);
	}
	.cap-reading {
		font-size: 0.75rem;
		color: var(--hk-text-3);
		font-variant-numeric: tabular-nums;
		white-space: nowrap;
	}
	.plot {
		position: relative;
		height: 80px;
		margin-top: 0.5rem;
	}
	.svg {
		display: block;
		overflow: visible;
	}
	.svg:focus-visible {
		outline: 2px solid var(--hk-accent);
		outline-offset: 2px;
		border-radius: var(--hk-radius-xs);
	}
	.grid {
		stroke: var(--hk-hairline);
		stroke-width: 1;
		stroke-dasharray: 2 3;
	}
	.base {
		stroke: var(--hk-border-strong);
		stroke-width: 1;
	}
	.axis {
		fill: var(--hk-text-3);
		font-size: 11px;
		font-variant-numeric: tabular-nums;
	}
	.area {
		fill: var(--hk-accent-wash);
	}
	.line {
		fill: none;
		stroke: var(--hk-accent);
		stroke-width: 1.5;
		stroke-linejoin: round;
		stroke-linecap: round;
	}
	.pt {
		fill: var(--hk-accent);
	}
	.cursor {
		stroke: var(--hk-border-strong);
		stroke-width: 1;
	}
	.mark {
		fill: var(--hk-accent);
		stroke: var(--hk-bg);
		stroke-width: 2;
	}
	.tip {
		position: absolute;
		top: 0;
		display: flex;
		gap: 0.375rem;
		padding: 0.25rem 0.5rem;
		transform: translate(-50%, 2px);
		pointer-events: none;
		white-space: nowrap;
		border-radius: var(--hk-radius);
		background: var(--hk-surface);
		box-shadow: var(--hk-shadow-pop);
		font-size: 0.75rem;
		font-variant-numeric: tabular-nums;
	}
	.tip-when {
		color: var(--hk-text-3);
	}
	.tip-v {
		font-weight: 500;
	}
	.ends {
		display: flex;
		justify-content: space-between;
		margin-top: 0.25rem;
		font-size: 0.6875rem;
		color: var(--hk-text-3);
		font-variant-numeric: tabular-nums;
	}
	@media (forced-colors: active) {
		.line,
		.mark {
			stroke: CanvasText;
		}
		.area,
		.pt,
		.mark {
			fill: CanvasText;
		}
	}
</style>
