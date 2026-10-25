<script lang="ts">
	// A project's Usage tab (DECISIONS I-492, I-841): the api's minute
	// samples over an hour, a day or a week, as four charts, and the
	// busiest processes folded under them. Samples change once a minute,
	// so the poll fetches them at most that often; a window switch fetches
	// at once.
	import { onMount } from 'svelte';
	import { getSamples } from '$lib/api/client';
	import { pollWhileVisible } from '$lib/poll';
	import { bytes, gb } from '$lib/format';
	import { pct, cpuTime, type Pt } from '$lib/usage';
	import UsageChart from '$lib/components/UsageChart.svelte';
	import { useProject } from '$lib/components/project/context.svelte';
	import type { Samples, SampleWindow } from '$lib/api/types';

	const ctx = useProject();
	const id = ctx.id;

	const WINDOWS: [SampleWindow, string, number][] = [
		['1h', 'Hour', 3600_000],
		['24h', 'Day', 86400_000],
		['7d', 'Week', 7 * 86400_000]
	];
	let samplesWindow = $state<SampleWindow>('1h');
	let samples = $state<Samples | undefined>(undefined);
	let samplesAt = $state(0);
	let samplesFailed = $state(false);

	async function refresh(force = false) {
		if (ctx.notFound) return;
		if (!force && samples?.window === samplesWindow && Date.now() - samplesAt < 55_000) return;
		const want = samplesWindow;
		try {
			const got = await getSamples(id, want);
			if (want !== samplesWindow) return; // switched again while this was in flight
			samples = got;
			samplesAt = Date.now();
			samplesFailed = false;
		} catch {
			// The section says it could not load; the header stands.
			samplesFailed = true;
		}
	}

	function chooseWindow(w: SampleWindow) {
		if (w === samplesWindow) return;
		samplesWindow = w;
		void refresh(true);
	}

	onMount(() => pollWhileVisible(() => refresh()));

	let span = $derived(WINDOWS.find(([w]) => w === samples?.window)?.[2] ?? 3600_000);
	let series = $derived.by(() => {
		const pts = samples?.points ?? [];
		const at = (f: (p: (typeof pts)[number]) => number | null): Pt[] =>
			pts.map((p) => ({ t: Date.parse(p.ts), v: f(p) }));
		return {
			cpu: at((p) => p.cpu),
			mem: at((p) => p.mem_used_bytes),
			pressure: at((p) => p.cpu_pressure),
			wait: at((p) => p.host_cpu_wait)
		};
	});
</script>

<section class="hk-section" data-testid="usage-card" aria-labelledby="usage-title">
	<div class="hk-section-head uhead">
		<!-- The tab says Usage; the heading names the region for a screen reader. -->
		<h2 id="usage-title" class="hk-sr">Usage</h2>
		<div class="hk-seg--buttons hk-seg--sm" role="group" aria-label="Window">
			{#each WINDOWS as [w, name] (w)}
				<button type="button" aria-pressed={samplesWindow === w} onclick={() => chooseWindow(w)}
					>{name}</button
				>
			{/each}
		</div>
	</div>
	{#if !samples}
		<div class="hk-empty-row">
			<span>{samplesFailed ? 'Could not load usage. It tries again in a minute.' : 'Loading…'}</span
			>
		</div>
	{:else if samples.points.length === 0}
		<div class="hk-empty-row">
			<span
				>{ctx.project?.state === 'running'
					? 'No usage reported in this window yet.'
					: 'No samples in this window: the machine was not running.'}</span
			>
		</div>
	{:else}
		<div class="charts">
			<UsageChart
				label="CPU"
				points={series.cpu}
				max={1}
				stepS={samples.step_s}
				start={samplesAt - span}
				end={samplesAt}
				format={pct}
				maxLabel="100% of {samples.vcpus} {samples.vcpus === 1 ? 'core' : 'cores'}"
			/>
			<UsageChart
				label="Memory"
				points={series.mem}
				max={samples.memory_bytes}
				stepS={samples.step_s}
				start={samplesAt - span}
				end={samplesAt}
				format={bytes}
				maxLabel={gb(samples.memory_bytes)}
			/>
			<UsageChart
				label="Waiting for a core"
				points={series.pressure}
				max={1}
				stepS={samples.step_s}
				start={samplesAt - span}
				end={samplesAt}
				format={pct}
				maxLabel="100%"
			/>
			<UsageChart
				label="Waiting for the server"
				points={series.wait}
				max={1}
				stepS={samples.step_s}
				start={samplesAt - span}
				end={samplesAt}
				format={pct}
				maxLabel="100%"
			/>
		</div>
		{#if samples.procs.length > 0}
			<!-- Folded: the charts answer most visits, and the table is there
			     when a chart's peak needs a name (I-674). -->
			<details class="procs" data-testid="busiest-processes">
				<summary>
					<span class="procs-chev" aria-hidden="true"></span>
					Busiest processes
					<span class="procs-n">{samples.procs.length}</span>
				</summary>
				<table class="hk-table procs-table">
					<thead>
						<tr>
							<th scope="col">Process</th>
							<th scope="col" class="hk-num-col">CPU time</th>
							<th scope="col" class="hk-num-col">Peak memory</th>
						</tr>
					</thead>
					<tbody>
						{#each samples.procs as p (p.comm)}
							<tr>
								<td class="hk-mono">{p.comm}</td>
								<td class="hk-num-col">{cpuTime(p.cpu_s)}</td>
								<td class="hk-num-col">{bytes(p.rss_max_bytes)}</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</details>
		{/if}
	{/if}
</section>

<style>
	.uhead {
		justify-content: flex-start;
	}
	/* The plots line up at their foot whatever their captions wrap to;
	   two by two from a 640px column. */
	.charts {
		display: grid;
		align-items: end;
		gap: 2rem 2.5rem;
	}
	@container dash (min-width: 640px) {
		.charts {
			grid-template-columns: repeat(2, minmax(0, 1fr));
		}
	}
	.procs {
		margin-top: 2rem;
		border-top: 1px solid var(--hk-hairline);
	}
	.procs summary {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		min-height: 2.75rem;
		cursor: pointer;
		list-style: none;
		font-size: 0.875rem;
		font-weight: 500;
	}
	.procs summary::-webkit-details-marker {
		display: none;
	}
	/* The CSS chevron of Guide B 12.17. */
	.procs-chev {
		width: 6px;
		height: 6px;
		margin: 0 0.25rem 0 0.125rem;
		border-right: 1.5px solid var(--hk-text-3);
		border-bottom: 1.5px solid var(--hk-text-3);
		transform: rotate(-45deg);
		transition: transform var(--hk-dur-fast) var(--hk-ease);
	}
	.procs[open] .procs-chev {
		transform: rotate(45deg);
	}
	.procs-n {
		font-weight: 400;
		font-size: 0.8125rem;
		color: var(--hk-text-3);
		font-variant-numeric: tabular-nums;
	}
	.procs-table {
		margin-bottom: 0.75rem;
	}
	@media (prefers-reduced-motion: reduce) {
		.procs-chev {
			transition: none;
		}
	}
</style>
