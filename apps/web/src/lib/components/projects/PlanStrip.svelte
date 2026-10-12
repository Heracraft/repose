<!--
  The plan's three limits over /projects (I-659, I-674): memory running
  now as the plan's squares filled by each running machine, then disk the
  projects hold and egress this period as a figure with a small ring. The
  band opens /billing. Memory is summed from the list itself (a size class's memory
  for each running machine), so it reads even with billing off or failing.
  With no usage or no limits from the api, the other cells are facts the list already
  holds: machines running, agents needing input, and the disk the guests
  use, as one row of equal cells. A cell whose figures are missing is
  left out.
-->
<script lang="ts">
	import { resolve } from '$app/paths';
	import { classMemoryGB, gb, money, sizeGB } from '$lib/format';
	import MemoryBlocks from '$lib/components/MemoryBlocks.svelte';
	import UsageRing from '$lib/components/UsageRing.svelte';
	import type { Billing, Project, Question } from '$lib/api/types';

	let {
		projects,
		billing,
		questions = [],
		flat = false
	}: {
		projects: Project[];
		/** GET /billing, or undefined when billing is off or did not load. */
		billing?: Billing;
		/** Pending questions: an agent asking one needs input too. */
		questions?: Question[];
		/** No box of its own: it sits in a card that draws one. */
		flat?: boolean;
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

		// One agent once, whether its signal, its question or both say so.
		const signalled = projects.flatMap((p) =>
			(p.signals?.agents ?? [])
				.filter((a) => waiting(a.state))
				.map((a) => ({ id: p.id, agent: a.agent }))
		);
		const waiters = [...signalled, ...questions.map((q) => ({ id: q.project_id, agent: q.agent }))];
		const agentKeys = new Set(waiters.map((w) => `${w.id}\u0000${w.agent}`));
		const askingIds = new Set(waiters.map((w) => w.id));
		const agents = agentKeys.size;
		const asking = [...askingIds].map(
			(id) =>
				projects.find((p) => p.id === id) ?? {
					id,
					name: questions.find((q) => q.project_id === id)?.project ?? id
				}
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

	/** Over a plan limit: drawn amber, and the row's sub line says by how much. */
	const isOver = (c: Cell) => c.meter === true && c.limit !== undefined && c.used > c.limit;
	function subOf(c: Cell): string | undefined {
		const f = c.format ?? fmt;
		const by = isOver(c) && c.limit !== undefined ? `over by ${f(c.used - c.limit)}` : undefined;
		return [by, c.sub].filter(Boolean).join(' · ') || undefined;
	}
</script>

<!-- "700 GB of 500 GB": it wraps before "of", never inside a figure. -->
{#snippet figure(c: Cell)}
	{@const f = c.format ?? fmt}
	<span class="nowrap">{f(c.used)}</span>
	{#if c.limit !== undefined}<span class="plan-of nowrap">of {f(c.limit)}</span>{/if}
{/snippet}

<!-- Laid out by its own width, not the viewport's: it also sits in a
     22rem column beside an empty list. -->
<div class="strip" class:strip--flat={flat}>
	{#if m.meter}
		<div class="band" data-testid="plan-strip">
			<svelte:element
				this={m.href ? 'a' : 'div'}
				href={m.href}
				class="mem"
				aria-label="{m.label}, {fmt(m.used)}{m.limit !== undefined
					? ` of ${fmt(m.limit)}`
					: ''}{mOver ? ', over' : ''}"
			>
				<span class="stat-label">{m.label}</span>
				<span class="stat-value" class:plan-over={mOver}>{@render figure(m)}</span>
				{#if mOver && m.limit !== undefined}
					<span class="stat-sub plan-over-sub">over by {fmt(m.used - m.limit)}</span>
				{/if}
				{#if m.limit !== undefined}
					<span class="mem-blocks"><MemoryBlocks label={m.label} {segments} limit={m.limit} /></span
					>
				{/if}
			</svelte:element>
			{#if rest.length}
				<div class="side">
					{#each rest as c (c.label)}
						{@const f = c.format ?? fmt}
						{@const over = isOver(c)}
						{@const sub = subOf(c)}
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
							<span class="row-value" class:plan-over={over}>{@render figure(c)}</span>
							{#if sub}<span class="row-sub" class:plan-over-sub={over}>{sub}</span>{/if}
						</svelte:element>
					{/each}
				</div>
			{/if}
		</div>
	{:else}
		<!-- No plan limit to draw against: the list's own facts, one row of
	     equal cells across the band. -->
		<div class="band band--stats" data-testid="plan-strip">
			{#each cells as c (c.label)}
				<svelte:element
					this={c.href ? 'a' : 'div'}
					href={c.href}
					class="cell"
					class:row--link={!!c.href}
					class:row--warn={c.warn}
					aria-label={c.href
						? `${c.label}, ${(c.format ?? fmt)(c.used)}${c.limit !== undefined ? ` of ${(c.format ?? fmt)(c.limit)}` : ''}`
						: undefined}
				>
					<span class="stat-label">{c.label}</span>
					<span class="stat-value cell-value">{@render figure(c)}</span>
					{#if c.sub}<span class="row-sub">{c.sub}</span>{/if}
				</svelte:element>
			{/each}
		</div>
	{/if}
</div>

<style>
	.strip {
		container-type: inline-size;
	}
	.strip--flat .band {
		border: 0;
		border-radius: 0;
	}
	.strip--flat .band:not(.band--stats) {
		background: transparent;
	}
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
		grid-template-columns: auto minmax(0, 1fr) auto;
		align-items: center;
		column-gap: 0.75rem;
		padding: 0.875rem 1.5rem;
		color: inherit;
	}
	.row:not(:has(svg)) {
		grid-template-columns: minmax(0, 1fr) auto;
	}
	.row + .row {
		border-top: 1px solid var(--rule);
	}
	.mem,
	.cell,
	.row--link {
		transition: background-color 150ms cubic-bezier(0.4, 0, 0.2, 1);
	}
	.mem:is(a):hover,
	.row--link:hover {
		background-color: color-mix(in srgb, var(--sunken) 70%, transparent);
	}
	.row-label {
		min-width: 0;
		font-size: var(--text-sm);
		color: var(--ink-muted);
	}
	.row-value {
		font-size: 0.9375rem;
		font-variant-numeric: tabular-nums;
		text-align: right;
	}
	.nowrap {
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
	.plan-over,
	.plan-over .plan-of,
	.plan-over-sub,
	.row--warn .row-value,
	.row--warn .cell-value {
		color: var(--color-amber-800);
	}
	@media (prefers-color-scheme: dark) {
		.plan-over,
		.plan-over .plan-of,
		.plan-over-sub,
		.row--warn .row-value,
		.row--warn .cell-value {
			color: var(--color-amber-300);
		}
	}
	/* The list's facts with no plan limit: equal cells split by hairlines,
	   two to a row on a phone, one row from md. The band's rule colour
	   shows through the 1px gaps as the hairlines. */
	.band--stats {
		grid-template-columns: repeat(2, minmax(0, 1fr));
		gap: 1px;
		overflow: hidden;
		background: var(--rule);
	}
	.cell {
		display: block;
		min-width: 0;
		padding: 1rem 1.25rem;
		background: var(--surface);
		color: inherit;
	}
	.cell:last-child:nth-child(odd) {
		grid-column: 1 / -1;
	}
	.cell.row--link:hover {
		background-color: color-mix(in srgb, var(--sunken) 70%, var(--surface));
	}
	.cell .row-sub {
		display: block;
		margin-top: 0.25rem;
	}
	@container (width >= 44rem) {
		.band {
			grid-template-columns: minmax(0, 1fr) minmax(0, 22rem);
		}
		.side {
			border-top: 0;
			border-left: 1px solid var(--rule);
		}
		.band--stats {
			grid-template-columns: none;
			grid-auto-flow: column;
			grid-auto-columns: minmax(0, 1fr);
		}
		.cell {
			padding: 1.25rem 1.5rem;
		}
		.cell:last-child:nth-child(odd) {
			grid-column: auto;
		}
	}
</style>
