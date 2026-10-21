<!--
  The plan's memory as its own squares (I-674, I-837): one square per GB,
  filled by what holds memory now, one run per machine. One machine fills
  in the accent; two in the accent and the secondary ink; three or more
  in the series colours, then the secondary ink. A frozen machine's GB
  are hatched in the ice ink, and free GB are an empty 3:1 outline. GB past the plan
  keep their machine's fill and take an amber ring, so every legend
  swatch still has its squares (review r1 R1-A6, sys-3). The fills are not the only carrier: the reading ("28 GB of 32
  GB, over") is the meter's value text, and with two machines or more the
  legend names each run.
-->
<script lang="ts">
	let {
		label,
		segments,
		limit,
		perRow = 32,
		size = 10,
		meter = true,
		legend = false
	}: {
		label: string;
		/** What runs, in order: one run of squares each. */
		segments: { name: string; gb: number; frozen?: boolean }[];
		limit: number;
		perRow?: number;
		/** The square's side in px: 10 on /billing, 8 in the projects' plan row. */
		size?: 8 | 10;
		/**
		 * The squares carry role=meter themselves. False when a Meter wraps
		 * them as its figure and is the meter (one meter, one name).
		 */
		meter?: boolean;
		/** A key under the squares naming each machine's run, when two or more run. */
		legend?: boolean;
	} = $props();

	let used = $derived(segments.reduce((n, s) => n + s.gb, 0));
	let total = $derived(Math.max(limit, used));

	/** The fill of the i-th machine's run, by how many runs there are. */
	function fill(i: number, n: number): string {
		if (n <= 1) return 'var(--hk-accent)';
		if (n === 2) return i === 0 ? 'var(--hk-accent)' : 'var(--hk-text-2)';
		return i < 3 ? `var(--hk-series-${i + 1})` : 'var(--hk-text-2)';
	}

	let squares = $derived.by(() => {
		const out: { seg: number; over: boolean; name?: string; frozen?: boolean }[] = [];
		segments.forEach((s, i) => {
			for (let k = 0; k < s.gb; k++)
				out.push({ seg: i, over: out.length >= limit, name: s.name, frozen: s.frozen });
		});
		while (out.length < total) out.push({ seg: -1, over: false });
		return out;
	});
	let reading = $derived(
		`${used} GB of ${limit} GB${used > limit ? ', over' : ''}` +
			(segments.length
				? `: ${segments.map((s) => `${s.name} ${s.gb} GB${s.frozen ? ' frozen' : ''}`).join(', ')}`
				: '')
	);
	let cols = $derived(Math.min(total, perRow));
	// Half a row where 32 squares do not fit their box.
	let narrow = $derived(Math.min(total, Math.ceil(perRow / 2)));
</script>

<div class="fit" class:fit--8={size === 8} style="--sq: {size}px">
	<div
		class="blocks"
		style="--cols: {cols}; --cols-narrow: {narrow}"
		role={meter ? 'meter' : undefined}
		aria-label={meter ? label : undefined}
		aria-valuemin={meter ? 0 : undefined}
		aria-valuemax={meter ? limit : undefined}
		aria-valuenow={meter ? Math.min(used, limit) : undefined}
		aria-valuetext={meter ? reading : undefined}
		aria-hidden={meter ? undefined : 'true'}
	>
		{#each squares as q, i (i)}
			<i
				class:on={q.seg >= 0 && !q.frozen}
				class:ice={q.seg >= 0 && q.frozen}
				class:over={q.over}
				style={q.seg >= 0 && !q.frozen ? `--fill: ${fill(q.seg, segments.length)}` : undefined}
				title={q.name}
			></i>
		{/each}
	</div>
	{#if legend && segments.length > 1}
		<ul class="legend" aria-label="{label} by machine">
			{#each segments as s, i (`${s.name}-${i}`)}
				<li>
					<i
						class:on={!s.frozen}
						class:ice={s.frozen}
						style={s.frozen ? undefined : `--fill: ${fill(i, segments.length)}`}
						aria-hidden="true"
					></i>{s.name}<span class="gb">{s.gb} GB{s.frozen ? ', frozen' : ''}</span>
				</li>
			{/each}
			{#if used > limit}
				<li>
					<i class="over" aria-hidden="true"></i>Over the plan<span class="gb"
						>{used - limit} GB</span
					>
				</li>
			{/if}
		</ul>
	{/if}
</div>

<style>
	/* The squares lay out by the box they sit in, not the viewport. */
	.fit {
		container-type: inline-size;
	}
	.blocks {
		display: grid;
		grid-template-columns: repeat(var(--cols), var(--sq));
		gap: 2px;
		width: max-content;
	}
	/* 32 squares of 10px take 382px, of 8px 318px. */
	@container (width < 382px) {
		.fit:not(.fit--8) .blocks {
			grid-template-columns: repeat(var(--cols-narrow), var(--sq));
		}
	}
	@container (width < 318px) {
		.fit--8 .blocks {
			grid-template-columns: repeat(var(--cols-narrow), var(--sq));
		}
	}
	i {
		display: block;
		width: var(--sq);
		height: var(--sq);
		box-shadow: inset 0 0 0 1px var(--hk-control-edge);
	}
	.on {
		box-shadow: none;
		background: var(--fill);
	}
	/* A frozen machine's memory (I-683) still counts against the plan:
	   hatched in the ice ink, so at 8px it reads as held and frozen, never
	   as a free square. */
	.ice {
		box-shadow: inset 0 0 0 1px var(--hk-ice-ink);
		background: repeating-linear-gradient(135deg, var(--hk-ice-ink) 0 1px, transparent 1px 3px);
	}
	/* Past the plan: the machine's own fill, ringed in amber. The ring
	   sits in the 2px gap, so a run past the line reads as one amber
	   frame. */
	.over {
		box-shadow:
			0 0 0 1px var(--hk-warn-dot),
			inset 0 0 0 1px var(--hk-warn-dot);
	}
	.over:not(.on, .ice) {
		background: transparent;
	}
	.legend {
		display: flex;
		flex-wrap: wrap;
		gap: 0.25rem 1rem;
		margin: 0.625rem 0 0;
		padding: 0;
		list-style: none;
		font-size: 0.75rem;
		color: var(--hk-text-2);
	}
	.legend li {
		display: inline-flex;
		align-items: center;
		gap: 0.375rem;
	}
	.legend i {
		--sq: 8px;
	}
	.gb {
		color: var(--hk-text-3);
		font-variant-numeric: tabular-nums;
	}
	@media (forced-colors: active) {
		i {
			box-shadow: none;
			border: 1px solid GrayText;
		}
		.on,
		.over {
			forced-color-adjust: none;
			background: CanvasText;
		}
		/* Held but frozen: grey fill, between the running ink and a free outline. */
		.ice {
			forced-color-adjust: none;
			border-color: GrayText;
			background: GrayText;
		}
	}
</style>
