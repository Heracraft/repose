<!--
  The plan's three limits over /projects (I-659): memory running now, disk
  the projects hold, egress this period, each a .stat cell that opens
  /billing. Memory is summed from the list itself (a size class's memory
  for each running machine), so it reads even with billing off or failing.
  With no usage or no limits from the api, the other cells are facts the list already
  holds: machines running, agents needing input, and the disk the guests
  use. A cell whose figures are missing is left out.
-->
<script lang="ts">
	import { resolve } from '$app/paths';
	import { classMemoryGB, gb, money, sizeGB } from '$lib/format';
	import Meter from '$lib/components/Meter.svelte';
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
</script>

<div class="stat-row plan-strip" data-testid="plan-strip">
	{#each cells as c (c.label)}
		{@const f = c.format ?? fmt}
		{@const over = c.meter === true && c.limit !== undefined && c.used > c.limit}
		<svelte:element
			this={c.href ? 'a' : 'div'}
			href={c.href}
			class="stat plan-cell"
			class:plan-cell--link={!!c.href}
			class:plan-cell--warn={c.warn}
			aria-label={c.href
				? `${c.label}, ${f(c.used)}${c.limit !== undefined ? ` of ${f(c.limit)}${over ? ', over' : ''}` : ''}`
				: undefined}
		>
			<span class="stat-label">{c.label}</span>
			<span class="stat-value" class:plan-over={over}
				>{f(c.used)}{#if c.limit !== undefined}<span class="plan-of">
						of {f(c.limit)}{#if over}&nbsp;· over{/if}</span
					>{/if}</span
			>
			{#if c.meter && c.limit !== undefined}
				<Meter label={c.label} used={c.used} limit={c.limit} format={f} />
			{/if}
			{#if c.sub}<span class="stat-sub plan-sub">{c.sub}</span>{/if}
		</svelte:element>
	{/each}
</div>

<style>
	.plan-cell {
		display: block;
		color: inherit;
		transition: background-color 150ms cubic-bezier(0.4, 0, 0.2, 1);
	}
	.plan-cell--link:hover {
		background-color: color-mix(in srgb, var(--sunken) 70%, transparent);
	}
	/* The cell's value says the reading in large type, so the meter's own
	   label and figures row is not drawn here; the bar keeps its
	   aria-valuetext. */
	.plan-cell :global(.meter > div:first-child) {
		display: none;
	}
	.plan-cell :global(.meter) {
		margin-top: 0.75rem;
	}
	.plan-of {
		margin-left: 0.375rem;
		font-family: var(--font-sans);
		font-size: var(--text-sm);
		font-weight: 400;
		letter-spacing: 0;
		color: var(--ink-muted);
	}
	.plan-over .plan-of,
	.plan-cell--warn .stat-value {
		color: var(--color-amber-800);
	}
	@media (prefers-color-scheme: dark) {
		.plan-over .plan-of,
		.plan-cell--warn .stat-value {
			color: var(--color-amber-300);
		}
	}
	.plan-sub {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	/* On a phone the three cells stack: label and figure on one line keeps
	   the strip to a third of the height. */
	@media (width < 40rem) {
		.plan-cell {
			display: grid;
			grid-template-columns: 1fr auto;
			align-items: baseline;
			column-gap: 1rem;
			padding: 0.75rem 1rem;
		}
		.plan-cell .stat-value {
			margin-top: 0;
			font-size: var(--text-lg);
		}
		.plan-cell :global(.meter),
		.plan-cell .stat-sub {
			grid-column: 1 / -1;
		}
		.plan-cell :global(.meter) {
			margin-top: 0.25rem;
		}
	}
</style>
