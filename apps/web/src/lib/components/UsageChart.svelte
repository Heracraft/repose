<!--
  One measure of a machine over a window, as a thin line over a faint area
  (DECISIONS I-492). One series, so no legend: the label names it and the
  reading beside it ("now 12%, peak 100%") carries the numbers, as a
  Meter's does. The line is ink, like a meter's fill; nothing rides on
  colour. A gap is a time the machine was stopped or did not report.
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

	const H = 88;
	const TOP = 4;
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

<figure class="usage-chart min-w-0" data-testid="chart-{label.toLowerCase().replace(/[^a-z0-9]+/g, '-')}">
	<figcaption class="flex items-baseline justify-between gap-4 text-sm">
		<span class="font-medium">{label}</span>
		<span class="font-mono text-compact text-ink-muted tabular-nums">{reading}</span>
	</figcaption>
	<div class="relative mt-1.5" bind:clientWidth={width}>
		<!-- The pointer and the arrow keys move a readout of one point. -->
		<!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions -->
		<svg
			width={width || '100%'}
			height={H}
			class="block overflow-visible text-ink focus-visible:outline-2 focus-visible:outline-offset-2"
			role="img"
			aria-label="{label}: {reading}. Arrow keys read one point at a time."
			tabindex={reported.length > 0 ? 0 : -1}
			onpointermove={onMove}
			onpointerleave={() => (hover = undefined)}
			onkeydown={onKey}
			onblur={() => (hover = undefined)}
		>
			<line class="grid" x1="0" x2={width} y1={TOP} y2={TOP} />
			<line class="grid" x1="0" x2={width} y1={(H + TOP) / 2} y2={(H + TOP) / 2} />
			<line class="base" x1="0" x2={width} y1={H - 0.5} y2={H - 0.5} />
			<text class="axis" x="2" y={TOP + 11}>{maxLabel}</text>
			{#if width > 0}
				{#each runs as run, i (i)}
					{#if run.r.length === 1}
						<circle cx={x(run.r[0].t)} cy={y(run.r[0].v)} r="2" fill="currentColor" />
					{:else}
						<path d={run.area} class="area" />
						<path d={run.line} class="line" />
					{/if}
				{/each}
				{#if hp && hp.v !== null}
					<line class="cursor" x1={x(hp.t)} x2={x(hp.t)} y1={TOP} y2={H} />
					<circle class="mark" cx={x(hp.t)} cy={y(hp.v)} r="4" />
				{/if}
			{/if}
		</svg>
		{#if hp && hp.v !== null}
			<div
				class="tip pointer-events-none absolute top-0 rounded-xs border px-2 py-1 text-xs tabular-nums"
				style="left: {Math.min(Math.max(x(hp.t), 60), Math.max(width - 60, 60))}px"
				aria-live="polite"
			>
				<span class="text-ink-muted">{when(hp.t)}</span>
				<span class="font-medium">{format(hp.v)}</span>
			</div>
		{/if}
	</div>
	<div class="mt-1 flex justify-between text-2xs text-ink-faint tabular-nums" aria-hidden="true">
		<span>{ago}</span>
		<span>now</span>
	</div>
</figure>

<style>
	.grid {
		stroke: var(--rule);
		stroke-width: 1;
		stroke-dasharray: 2 3;
	}
	.base {
		stroke: var(--rule-strong);
		stroke-width: 1;
	}
	.axis {
		fill: var(--ink-faint);
		font-size: 11px;
	}
	.area {
		fill: currentColor;
		opacity: 0.12;
	}
	.line {
		fill: none;
		stroke: currentColor;
		stroke-width: 2;
		stroke-linejoin: round;
		stroke-linecap: round;
	}
	.cursor {
		stroke: var(--ink-muted);
		stroke-width: 1;
	}
	.mark {
		fill: currentColor;
		stroke: var(--surface);
		stroke-width: 2;
	}
	.tip {
		transform: translate(-50%, 2px);
		white-space: nowrap;
		background: var(--surface);
		border-color: var(--rule-strong);
	}
	@media (forced-colors: active) {
		.line,
		.mark {
			stroke: CanvasText;
		}
		.area {
			fill: CanvasText;
		}
	}
</style>
