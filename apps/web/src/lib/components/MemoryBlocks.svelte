<!--
  The plan's memory as its own squares (I-674): one square per GB, as the
  plan cells draw a plan (Units.svelte), filled by what runs now. Each
  running project fills a run of squares, the runs told apart by two
  steps of the accent, so the picture says both how much is running and
  which machine holds it. Empty squares are what is free. Past the limit
  the extra squares are amber. The figure beside it carries the reading.
-->
<script lang="ts">
	let {
		label,
		segments,
		limit,
		perRow = 32
	}: {
		label: string;
		/** What runs, in order: one run of squares each. */
		segments: { name: string; gb: number; frozen?: boolean }[];
		limit: number;
		perRow?: number;
	} = $props();

	let used = $derived(segments.reduce((n, s) => n + s.gb, 0));
	let total = $derived(Math.max(limit, used));
	/** Each square's run: its index in segments, or -1 when free. */
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
</script>

<div class="fit">
	<div
		class="blocks"
		style="--cols: {Math.min(total, perRow)}; --cols-narrow: {Math.min(total, 16)}"
		role="meter"
		aria-label={label}
		aria-valuemin={0}
		aria-valuemax={limit}
		aria-valuenow={Math.min(used, limit)}
		aria-valuetext={reading}
	>
		{#each squares as q, i (i)}
			<i
				class:on={q.seg >= 0}
				class:alt={q.seg % 2 === 1}
				class:over={q.over}
				class:ice={q.frozen}
				title={q.name ? `${q.name}` : undefined}
			></i>
		{/each}
	</div>
</div>

<style>
	/* The squares lay out by the box they sit in, not the viewport: a
	   narrow cell beside a side column took 32 to a row past its edge. */
	.fit {
		container-type: inline-size;
	}
	.blocks {
		display: grid;
		grid-template-columns: repeat(var(--cols), 10px);
		gap: 3px;
		width: max-content;
	}
	/* Sixteen to a row in a box under 413px, what 32 squares take. */
	@container (width < 413px) {
		.blocks {
			grid-template-columns: repeat(var(--cols-narrow), 10px);
		}
	}
	i {
		display: block;
		width: 10px;
		height: 10px;
		border-radius: var(--radius-xs);
		box-shadow: inset 0 0 0 1px var(--rule-strong);
	}
	.on {
		box-shadow: none;
		background: var(--accent);
	}
	.on.alt {
		background: color-mix(in srgb, var(--accent) 55%, var(--surface));
	}
	/* A frozen machine's memory (I-683): its squares turn to ice, an edge
	   of frost over a pale fill; they still count against the plan. */
	.on.ice {
		background: color-mix(in srgb, var(--frost) 22%, var(--surface));
		box-shadow: inset 0 0 0 1px var(--frost);
	}
	.on.over {
		background: var(--color-amber-600);
	}
	@media (forced-colors: active) {
		i {
			border: 1px solid GrayText;
		}
		.on {
			background: CanvasText;
		}
	}
</style>
