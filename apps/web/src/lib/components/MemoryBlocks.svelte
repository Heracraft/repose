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
		segments: { name: string; gb: number }[];
		limit: number;
		perRow?: number;
	} = $props();

	let used = $derived(segments.reduce((n, s) => n + s.gb, 0));
	let total = $derived(Math.max(limit, used));
	/** Each square's run: its index in segments, or -1 when free. */
	let squares = $derived.by(() => {
		const out: { seg: number; over: boolean; name?: string }[] = [];
		segments.forEach((s, i) => {
			for (let k = 0; k < s.gb; k++) out.push({ seg: i, over: out.length >= limit, name: s.name });
		});
		while (out.length < total) out.push({ seg: -1, over: false });
		return out;
	});
	let reading = $derived(
		`${used} GB of ${limit} GB${used > limit ? ', over' : ''}` +
			(segments.length ? `: ${segments.map((s) => `${s.name} ${s.gb} GB`).join(', ')}` : '')
	);
</script>

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
			title={q.name ? `${q.name}` : undefined}
		></i>
	{/each}
</div>

<style>
	.blocks {
		display: grid;
		grid-template-columns: repeat(var(--cols), 10px);
		gap: 3px;
		width: max-content;
	}
	/* A phone takes sixteen to a row: 32 squares fit 413px, not 350. */
	@media (width < 40rem) {
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
