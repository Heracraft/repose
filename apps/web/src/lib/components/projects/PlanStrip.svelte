<!--
  The plan's three limits over /projects (I-659, I-674): memory running
  now as the plan's squares filled by each running machine, then disk the
  projects hold and egress this period as a figure with a small ring. The
  band opens /billing. Memory is summed from the list itself (a size class's memory
  for each running machine), so it reads even with billing off or failing.
  With no usage or no limits from the api, the other cells are facts the list already
  holds: machines running, agents needing input, and the disk the guests
  use. A cell whose figures are missing is left out.
-->
<script lang="ts">
	import { resolve } from '$app/paths';
	import { classMemoryGB, gb, money, sizeGB } from '$lib/format';
	import MemoryBlocks from '$lib/components/MemoryBlocks.svelte';
	import UsageRing from '$lib/components/UsageRing.svelte';
	import type { Billing, Project } from '$lib/api/types';

	let {
		projects,
		billing
	}: {
		projects: Project[];
		/** GET /billing, or undefined when billing is off or did not load. */
		billing?: Billing;
	} = $props();

	/** One decimal at most: the api's held disk is a fraction of a GB. */
	const fmt = (n: number) => sizeGB(Math.round(n * 10) / 10);

	interface Cell {
		label: string;
		used: number;
		limit?: number;
		sub?: string;
		/** Formats used and limit; the plan's GB figures by default. */
		format?: (n: number) => string;
		/** Where the cell opens; a cell without one is not a link. */
		href?: string;
		/** Amber: an agent is waiting on you. */
		warn?: boolean;
		/** The meter is drawn only for a plan limit. */
		meter?: boolean;
	}

	const count = (n: number) => String(n);
	const billingHref = resolve('/billing');

	/** An agent waiting on you, as AgentChips marks it. */
	const waiting = (state: string) => state === 'needs_input' || state === 'question';

	let segments = $derived(
		projects
			.filter((p) => p.state === 'running')
			.map((p) => ({ name: p.name, gb: classMemoryGB(p.class) }))
	);
	let memoryRunning = $derived(
		projects.filter((p) => p.state === 'running').reduce((n, p) => n + classMemoryGB(p.class), 0)
	);

	/** Cells from the list alone, for when billing gives no usage. */
	function listCells(): Cell[] {
		const out: Cell[] = [];
		const running = projects.filter((p) => p.state === 'running').length;
		out.push({ label: 'Machines running', used: running, limit: projects.length, format: count });

		const asking = projects.filter((p) => p.signals?.agents.some((a) => waiting(a.state)));
		const agents = asking.reduce(
			(n, p) => n + (p.signals?.agents.filter((a) => waiting(a.state)).length ?? 0),
			0
		);
		out.push({
			label: 'Agents needing input',
			used: agents,
			format: count,
			warn: agents > 0,
			sub: asking.length > 0 ? asking.map((p) => p.name).join(', ') : undefined,
			href: asking.length === 1 ? resolve('/projects/[id]', { id: asking[0].id }) : undefined
		});

		// Only guests that reported their root filesystem count, on both sides.
		const sampled = projects.filter((p) => p.root_used_bytes !== undefined);
		if (sampled.length > 0) {
			const used = sampled.reduce((n, p) => n + (p.root_used_bytes ?? 0), 0);
			const size = sampled.reduce((n, p) => n + (p.root_size_bytes ?? p.volume_bytes), 0);
			out.push({ label: 'Disk in use', used, limit: size > 0 ? size : undefined, format: gb });
		}
		return out;
	}

	let cells = $derived.by((): Cell[] => {
		const u = billing?.usage;
		const memoryLimit = u && u.memory_gb > 0 ? u.memory_gb : undefined;
		const memory: Cell = {
			label: 'Memory running',
			used: memoryRunning,
			limit: memoryLimit,
			href: billingHref,
			meter: memoryLimit !== undefined
		};
		if (!u) return [memory, ...listCells()];
		const out: Cell[] = [memory];
		if (u.disk_gb > 0) {
			out.push({
				label: 'Disk held',
				used: u.disk_held_gb ?? u.disk_allocated_gb,
				limit: u.disk_gb,
				href: billingHref,
				meter: true
			});
		}
		if (u.egress_included_gb > 0) {
			out.push({
				label: 'Egress this period',
				used: u.egress_gb,
				limit: u.egress_included_gb,
				sub: u.overage_cents > 0 ? `${money(u.overage_cents)} overage so far` : undefined,
				href: billingHref,
				meter: true
			});
		}
		// A plan with no limits yet (waitlist, no subscription) answers with
		// zeros: the list's own facts fill the strip instead.
		return out.length === 1 && !memory.meter ? [memory, ...listCells()] : out;
	});
	let m = $derived(cells[0]);
	let mOver = $derived(m.limit !== undefined && m.used > m.limit);
	let rest = $derived(cells.slice(1));
</script>

<div class="band" data-testid="plan-strip">
	<svelte:element
		this={m.href ? 'a' : 'div'}
		href={m.href}
		class="mem"
		aria-label="{m.label}, {fmt(m.used)}{m.limit !== undefined ? ` of ${fmt(m.limit)}` : ''}{mOver
			? ', over'
			: ''}"
	>
		<span class="stat-label">{m.label}</span>
		<span class="stat-value" class:plan-over={mOver}
			>{fmt(m.used)}{m.limit !== undefined ? ' ' : ''}{#if m.limit !== undefined}
				<span class="plan-of">
					of {fmt(m.limit)}{#if mOver}&nbsp;· over{/if}</span
				>{/if}</span
		>
		{#if m.meter && m.limit !== undefined}
			<span class="mem-blocks"><MemoryBlocks label={m.label} {segments} limit={m.limit} /></span>
		{/if}
	</svelte:element>
	{#if rest.length}
		<div class="side">
			{#each rest as c (c.label)}
				{@const f = c.format ?? fmt}
				{@const over = c.meter === true && c.limit !== undefined && c.used > c.limit}
				<svelte:element
					this={c.href ? 'a' : 'div'}
					href={c.href}
					class="row"
					class:row--link={!!c.href}
					class:row--warn={c.warn}
				>
					{#if c.meter && c.limit !== undefined}
						<UsageRing label={c.label} used={c.used} limit={c.limit} format={f} />
					{/if}
					<span class="row-label">{c.label}</span>
					<span class="row-value" class:plan-over={over}
						>{f(c.used)}{c.limit !== undefined ? ' ' : ''}{#if c.limit !== undefined}
							<span class="plan-of">
								of {f(c.limit)}{#if over}&nbsp;· over{/if}</span
							>{/if}</span
					>
					{#if c.sub}<span class="row-sub">{c.sub}</span>{/if}
				</svelte:element>
			{/each}
		</div>
	{/if}
</div>

<style>
	/* One card: memory, the limit that decides what may start, on the
	   left with its squares; the other figures in quiet rows beside it. */
	.band {
		display: grid;
		border: 1px solid var(--rule);
		border-radius: var(--radius-md);
		background: var(--surface);
	}
	.mem {
		display: block;
		padding: 1.25rem 1.5rem;
		color: inherit;
	}
	.mem-blocks {
		display: block;
		margin-top: 0.875rem;
	}
	.side {
		display: grid;
		align-content: center;
		border-top: 1px solid var(--rule);
	}
	.row {
		display: grid;
		grid-template-columns: auto 1fr auto;
		align-items: center;
		column-gap: 0.75rem;
		padding: 0.875rem 1.5rem;
		color: inherit;
	}
	.row:not(:has(svg)) {
		grid-template-columns: 1fr auto;
	}
	.row + .row {
		border-top: 1px solid var(--rule);
	}
	.mem,
	.row--link {
		transition: background-color 150ms cubic-bezier(0.4, 0, 0.2, 1);
	}
	.mem:is(a):hover,
	.row--link:hover {
		background-color: color-mix(in srgb, var(--sunken) 70%, transparent);
	}
	.row-label {
		font-size: var(--text-sm);
		color: var(--ink-muted);
	}
	.row-value {
		font-size: 0.9375rem;
		font-variant-numeric: tabular-nums;
		white-space: nowrap;
	}
	.row-sub {
		grid-column: 1 / -1;
		margin-top: 0.125rem;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		font-size: var(--text-xs);
		color: var(--ink-muted);
	}
	.plan-of {
		margin-left: 0;
		font-family: var(--font-sans);
		font-size: var(--text-sm);
		font-weight: 400;
		letter-spacing: 0;
		color: var(--ink-muted);
	}
	.row-value .plan-of {
		margin-left: 0;
	}
	.plan-over .plan-of,
	.row--warn .row-value {
		color: var(--color-amber-800);
	}
	@media (prefers-color-scheme: dark) {
		.plan-over .plan-of,
		.row--warn .row-value {
			color: var(--color-amber-300);
		}
	}
	@media (min-width: 48rem) {
		.band {
			grid-template-columns: minmax(0, 1fr) minmax(0, 22rem);
		}
		.side {
			border-top: 0;
			border-left: 1px solid var(--rule);
		}
	}
</style>
