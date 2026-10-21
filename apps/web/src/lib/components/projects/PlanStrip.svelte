<!--
  The plan's three limits under the projects table (I-659, I-674, I-837):
  one row between two hairlines, each cell the Meter anatomy (label left,
  value right, figure under). Memory running fills the plan's squares with
  each machine that holds memory and opens /billing; disk held and egress
  this period are 6px meters; the row ends with a link to Billing. Memory
  is summed from the list itself, so it reads even with billing off or
  failing. With no plan limits from the api, the cells are facts the list
  already holds, as text: memory running, machines running, agents
  needing input and the disk the guests use.
-->
<script lang="ts">
	import { resolve } from '$app/paths';
	import { classMemoryGB, gb, sizeGB } from '$lib/format';
	import { holdsMemory } from '$lib/plans';
	import MemoryBlocks from '$lib/components/MemoryBlocks.svelte';
	import Meter from '$lib/components/Meter.svelte';
	import { frozenSince } from '$lib/frozen';
	import type { Billing, Project, Question } from '$lib/api/types';

	let {
		projects,
		billing,
		questions = []
	}: {
		projects: Project[];
		/** GET /billing, or undefined when billing is off or did not load. */
		billing?: Billing;
		/** Pending questions: an agent asking one needs input too. */
		questions?: Question[];
	} = $props();

	/** One decimal at most: the api's held disk is a fraction of a GB. */
	const fmt = (n: number) => sizeGB(Math.round(n * 10) / 10);
	const billingHref = resolve('/billing');
	/** "5 GB of 500 GB", "0 GB of 1 TB", "28 GB of 32 GB · over": the plan's units, as /billing prints them. */
	const reading = (used: number, limit: number) =>
		`${fmt(used)} of ${fmt(limit)}${used > limit ? ' · over' : ''}`;

	/** An agent waiting on you, as AgentChips marks it. */
	const waiting = (state: string) => state === 'needs_input' || state === 'question';

	// The five states that hold the plan's memory, not `running` alone
	// (internal/billing/usage.go, pinned by plans.test.ts): counting one
	// state told the user they had room the api then refused.
	let segments = $derived(
		projects
			.filter(holdsMemory)
			.map((p) => ({ name: p.name, gb: classMemoryGB(p.class), frozen: !!frozenSince(p) }))
	);
	let memoryRunning = $derived(segments.reduce((n, s) => n + s.gb, 0));

	let usage = $derived(billing?.usage);
	let memoryLimit = $derived(usage && usage.memory_gb > 0 ? usage.memory_gb : undefined);
	let diskHeld = $derived(usage ? (usage.disk_held_gb ?? usage.disk_allocated_gb) : 0);
	let memoryOver = $derived(memoryLimit !== undefined && memoryRunning > memoryLimit);

	interface Fact {
		label: string;
		value: string;
		sub?: string;
		href?: string;
		/** The cell's link name, when it is a link. */
		name?: string;
	}

	/** The list's own facts, for a plan with no limits to draw against. */
	let facts = $derived.by((): Fact[] => {
		const out: Fact[] = [
			{
				label: 'Memory running',
				value: fmt(memoryRunning),
				href: billingHref,
				name: `Memory running, ${fmt(memoryRunning)}`
			}
		];
		// No denominator: a plan sells no project count (I-569).
		out.push({ label: 'Machines running', value: String(segments.length) });

		// One agent once, whether its signal, its question or both say so.
		const signalled = projects.flatMap((p) =>
			(p.signals?.agents ?? [])
				.filter((a) => waiting(a.state))
				.map((a) => ({ id: p.id, agent: a.agent }))
		);
		const waiters = [...signalled, ...questions.map((q) => ({ id: q.project_id, agent: q.agent }))];
		const agents = new Set(waiters.map((w) => `${w.id}\u0000${w.agent}`)).size;
		const askingIds = [...new Set(waiters.map((w) => w.id))];
		const names = askingIds.map(
			(id) =>
				projects.find((p) => p.id === id)?.name ??
				questions.find((q) => q.project_id === id)?.project ??
				id
		);
		out.push({
			label: 'Agents needing input',
			value: String(agents),
			sub: names.length > 0 ? names.join(', ') : undefined,
			href: askingIds.length === 1 ? resolve('/projects/[id]', { id: askingIds[0] }) : undefined
		});

		// Only guests that reported their root filesystem count, on both sides.
		const sampled = projects.filter((p) => p.root_used_bytes !== undefined);
		if (sampled.length > 0) {
			const used = sampled.reduce((n, p) => n + (p.root_used_bytes ?? 0), 0);
			const size = sampled.reduce((n, p) => n + (p.root_size_bytes ?? p.volume_bytes), 0);
			out.push({
				label: 'Disk in use',
				value: size > 0 ? `${gb(used)} of ${gb(size)}` : gb(used)
			});
		}
		return out;
	});
</script>

<section
	class="plan"
	class:plan--facts={memoryLimit === undefined}
	aria-label="Plan"
	data-testid="plan-strip"
>
	{#if memoryLimit !== undefined && usage}
		<a
			href={billingHref}
			class="cell cell--link"
			aria-label="Memory running, {fmt(memoryRunning)} of {fmt(memoryLimit)}{memoryOver
				? ', over'
				: ''}"
		>
			<Meter
				label="Memory running"
				used={memoryRunning}
				limit={memoryLimit}
				valueText={reading(memoryRunning, memoryLimit)}
			>
				{#snippet track()}
					<MemoryBlocks
						label="Memory running"
						{segments}
						limit={memoryLimit!}
						size={8}
						meter={false}
						legend
					/>
				{/snippet}
			</Meter>
		</a>
		{#if usage.disk_gb > 0}
			<div class="cell">
				<Meter
					label="Disk held"
					used={diskHeld}
					limit={usage.disk_gb}
					valueText={reading(diskHeld, usage.disk_gb)}
				/>
			</div>
		{/if}
		{#if usage.egress_included_gb > 0}
			<div class="cell">
				<Meter
					label="Egress this period"
					used={usage.egress_gb}
					limit={usage.egress_included_gb}
					valueText={reading(usage.egress_gb, usage.egress_included_gb)}
				/>
			</div>
		{/if}
		<a href={billingHref} class="hk-more billing">Billing</a>
	{:else}
		{#each facts as f (f.label)}
			<!-- Spans only inside a cell: the label and its figure read as
			     one line, and the cell is the innermost box holding both. -->
			<svelte:element
				this={f.href ? 'a' : 'div'}
				href={f.href}
				class="cell fact"
				class:cell--link={!!f.href}
				aria-label={f.name}
			>
				<span class="fact-label">{f.label}</span>
				<span class="fact-value">{f.value}</span>
				{#if f.sub}<span class="fact-sub">{f.sub}</span>{/if}
			</svelte:element>
		{/each}
	{/if}
</section>

<style>
	/* One row between two hairlines; one column below 900px. */
	.plan {
		display: grid;
		gap: 0.75rem;
		padding: 1rem 0;
		border-top: 1px solid var(--hk-hairline);
		border-bottom: 1px solid var(--hk-hairline);
	}
	@media (min-width: 900px) {
		.plan {
			grid-template-columns: repeat(3, minmax(0, 1fr)) auto;
			align-items: start;
			gap: 2.5rem;
		}
		.plan--facts {
			grid-template-columns: repeat(4, minmax(0, 1fr));
		}
	}
	.cell {
		display: block;
		min-width: 0;
		color: inherit;
		text-decoration: none;
	}
	.cell--link {
		border-radius: var(--hk-radius-sm);
		outline-offset: 4px;
	}
	.cell--link:hover :global(.hk-meter-label),
	.cell--link:hover .fact-label {
		color: var(--hk-text);
	}
	.billing {
		margin-top: 0;
		align-self: center;
		white-space: nowrap;
	}
	/* The list's facts: label left, figure right, a sub line under. */
	.fact {
		display: grid;
		grid-template-columns: minmax(0, 1fr) auto;
		align-items: baseline;
		column-gap: 0.75rem;
	}
	.fact-label {
		font-size: 0.8125rem;
		color: var(--hk-text-3);
		white-space: nowrap;
		transition: color var(--hk-dur-fast) var(--hk-ease);
	}
	.fact-value {
		font-size: 0.8125rem;
		font-weight: 500;
		font-variant-numeric: tabular-nums;
		color: var(--hk-text);
		white-space: nowrap;
	}
	.fact-sub {
		grid-column: 1 / -1;
		margin-top: 0.25rem;
		font-size: 0.75rem;
		color: var(--hk-text-3);
	}
</style>
