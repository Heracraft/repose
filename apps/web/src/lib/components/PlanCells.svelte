<!--
  The three plans, side by side, from one source (lib/plans.ts, DECISIONS
  I-663): the landing's #pricing and /billing's plan picker draw this, so
  the two cannot disagree. Cells cut by hairlines, never three cards. Each
  cell is the same stack, and from md the cells share their rows (a
  subgrid), so the names, the spec lines, the price rules and the buttons
  each sit on one line across, whatever one cell holds that the others do
  not:
    the name, and the memory as squares (Units, one per GB, top-aligned
      in a box four rows high, so the three pictures share a top line);
    the memory in words, in ink: it is what the plan sells;
    disk and egress, label and figure on hairlines;
    a slot for the introductory offer, reserved on every cell;
    the price on the blue rule, and under it the price it becomes;
    the action, which the page supplies.
  Below md a cell is compact: the name over its squares with the price
  beside them, then the memory, then disk and egress side by side.
-->
<script lang="ts">
	import type { Snippet } from 'svelte';
	import { price, gbs } from '$lib/format';
	import { memoryLine, planSize, type PlanFigures, type PlanId } from '$lib/plans';
	import Units from './landing/Units.svelte';

	let {
		plans,
		current,
		action,
		compare,
		introEligible = false,
		variant = 'app',
		level = 2,
		label = 'Plans'
	}: {
		plans: PlanFigures[];
		/** The plan the account is on: marked Current, with an ink top edge. */
		current?: PlanId;
		/** The cell's action (a button or a link), pinned to its foot. */
		action?: Snippet<[PlanFigures]>;
		/** What the account uses now: a figure past a plan's limit is said in amber. */
		compare?: { runningGB: number; diskHeldGB: number };
		/** The introductory price applies to this visitor's checkout (I-497). */
		introEligible?: boolean;
		/** 'landing' sits between the rails in the landing's tokens; 'app' is a box on the dashboard. */
		variant?: 'landing' | 'app';
		/** The names' heading level: 3 under the landing's section title, 2 under /billing's h1. */
		level?: 2 | 3;
		label?: string;
	} = $props();

	const offer = (p: PlanFigures) => (introEligible ? p.intro : undefined);
</script>

<ul class="plans plans--{variant}" aria-label={label}>
	{#each plans as p, i (p.id)}
		{@const intro = offer(p)}
		{@const overRun = compare && compare.runningGB > p.memoryGB}
		{@const overDisk = compare && compare.diskHeldGB > p.diskGB}
		<li class="plan" class:current={current === p.id} data-testid="plan-{p.id}">
			<div class="p-top">
				<svelte:element this={`h${level}`} class="p-name">{p.name}</svelte:element>
				{#if current === p.id}<span class="badge p-current">Current</span>{/if}
			</div>
			<span class="p-units" class:land={variant === 'landing'} style="--d: {i * 110}ms"
				><Units count={p.memoryGB} /></span
			>
			<div class="p-run">
				<p>{memoryLine(p.memoryGB)}</p>
				{#if overRun && compare}
					<p class="p-over">
						Running <span class="nw">{gbs(compare.runningGB)}</span>, holds
						<span class="nw">{gbs(p.memoryGB)}</span>
					</p>
				{/if}
			</div>
			<div class="p-specs">
				<dl class="p-spec">
					<div>
						<dt>Disk</dt>
						<dd>{planSize(p.diskGB)}</dd>
					</div>
					<div>
						<dt>Egress a month</dt>
						<dd>{planSize(intro ? intro.egressGB : p.egressGB)}</dd>
					</div>
				</dl>
				{#if overDisk && compare}
					<p class="p-over">Projects hold <span class="nw">{gbs(compare.diskHeldGB)}</span></p>
				{/if}
			</div>
			<div class="p-intro">
				{#if intro}
					<span class="badge" data-testid="intro-{p.id}"
						>{price(intro.priceCents)} for {intro.months} months</span
					>
				{/if}
			</div>
			<p class="p-price">
				<span class="n">{price(intro ? intro.priceCents : p.priceCents)}</span>
				<span class="per">/ month</span>
			</p>
			<p class="p-then">
				{#if intro}
					then {price(p.priceCents)}
					{#if intro.egressGB !== p.egressGB}and <span class="nw">{planSize(p.egressGB)}</span>
						egress{/if}
					from <span class="nw">month {intro.months + 1}</span>
				{/if}
			</p>
			{#if action}
				<div class="p-act">{@render action(p)}</div>
			{/if}
		</li>
	{/each}
</ul>

<style>
	.plans {
		/* The landing sets its own inset and blue (--land-x, --sh-accent);
		   the dashboard keeps the Foundation's. */
		--p-x: 1.5rem;
		--p-rule: var(--accent);
		--p-name: 1.5rem;
		--p-price: 2.5rem;
		display: grid;
		list-style: none;
		margin: 0;
		padding: 0;
	}
	.plans--landing {
		--p-x: var(--land-x, 1.25rem);
		--p-rule: var(--sh-accent);
		--p-name: 2.25rem;
		--p-price: 3rem;
	}
	/* On the dashboard the three are one box on --surface, cut by
	   hairlines, as a stat strip is; on the landing the rails are the box. */
	.plans--app {
		border: 1px solid var(--rule);
		border-radius: var(--radius-sm);
		background: var(--surface);
	}

	.plan {
		position: relative;
		display: grid;
		grid-template-columns: minmax(0, 1fr) auto;
		grid-template-areas:
			'top price'
			'units price'
			'run run'
			'spec spec'
			'intro intro'
			'then then'
			'act act';
		align-items: start;
		column-gap: 0.75rem;
		min-width: 0;
		padding: 1.5rem var(--p-x) 1.75rem;
	}
	.plans--landing .plan {
		border-top: 1px solid var(--rule);
	}
	.plans--app .plan + .plan {
		border-top: 1px solid var(--rule);
	}
	/* The plan the account is on: an ink edge along its top. */
	.plan.current::before {
		content: '';
		position: absolute;
		inset: -1px 0 auto;
		height: 2px;
		background: var(--ink);
	}

	/* In a narrow cell the Current badge drops under the name rather
	   than run into the squares beside it. */
	.p-top {
		grid-area: top;
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.375rem 0.625rem;
		min-width: 0;
	}
	/* A figure never splits between its number and its unit. */
	.nw {
		white-space: nowrap;
	}
	.p-name {
		font-family: var(--font-display);
		font-size: calc(var(--p-name) * 0.8);
		font-weight: 700;
		line-height: 1;
		letter-spacing: -0.01em;
		text-transform: uppercase;
		color: var(--ink);
	}
	.p-units {
		grid-area: units;
		display: flex;
		align-items: flex-start;
		line-height: 0;
		/* Under the name on a phone; from md level with its top, Pro's
		   four rows hanging down beside it. */
		align-self: start;
		padding-top: 0.625rem;
	}
	.p-run {
		grid-area: run;
		margin-top: 0.875rem;
		font-size: 0.9375rem;
		font-weight: 500;
		color: var(--ink);
	}
	.p-over {
		margin-top: 0.25rem;
		font-size: 0.8125rem;
		font-weight: 400;
		color: var(--color-amber-800);
	}
	@media (prefers-color-scheme: dark) {
		.p-over {
			color: var(--color-amber-300);
		}
	}

	/* Below md the two figures stand side by side, a label over a mono
	   figure; the sentence above already says the memory. */
	.p-specs {
		grid-area: spec;
		min-width: 0;
	}
	.p-spec {
		display: grid;
		grid-template-columns: repeat(2, minmax(0, 1fr));
		gap: 0.75rem;
		margin-top: 1rem;
		padding-top: 0.875rem;
		border-top: 1px solid var(--rule);
	}
	.p-spec > div {
		min-width: 0;
	}
	.p-spec dt {
		font-size: 0.75rem;
		color: var(--ink-muted);
	}
	.p-spec dd {
		margin-top: 0.125rem;
		font-family: var(--font-mono);
		font-size: var(--text-compact);
		font-variant-numeric: tabular-nums;
		color: var(--ink);
		white-space: nowrap;
	}
	.p-specs .p-over {
		margin-top: 0.5rem;
	}

	.p-intro {
		grid-area: intro;
	}
	.p-intro:has(.badge) {
		margin-top: 1rem;
	}

	/* The price on the blue rule. Below md it stands in the top row, the
	   rule as wide as the figure. */
	.p-price {
		grid-area: price;
		display: flex;
		align-items: baseline;
		gap: 0.375rem;
		justify-self: end;
		padding-top: 0.5rem;
		background: linear-gradient(var(--p-rule), var(--p-rule)) no-repeat 0 0 / 100% 3px;
	}
	.p-price .n {
		font-family: var(--font-display);
		font-size: calc(var(--p-price) * 0.66);
		font-weight: 700;
		line-height: 1;
		letter-spacing: -0.02em;
		font-variant-numeric: tabular-nums;
		color: var(--ink);
	}
	.p-price .per {
		font-size: 0.8125rem;
		color: var(--ink-muted);
		white-space: nowrap;
	}
	.p-then {
		grid-area: then;
		font-size: 0.8125rem;
		color: var(--ink-muted);
		text-wrap: balance;
	}
	.p-then:not(:empty) {
		margin-top: 0.5rem;
	}
	.p-act {
		grid-area: act;
		margin-top: 1.25rem;
	}
	/* The page's button fills the cell, so the three line up. */
	.p-act :global(:is(a, button)) {
		width: 100%;
	}

	@media (min-width: 768px) {
		/* The cells share their rows: a subgrid seven rows deep, so each
		   row is as tall as its tallest cell's and the lines run across. */
		.plans {
			grid-template-columns: repeat(3, minmax(0, 1fr));
			grid-template-rows: repeat(7, auto);
		}
		.plan {
			grid-row: span 7;
			grid-template-rows: subgrid;
			grid-template-columns: minmax(0, 1fr) auto;
			grid-template-areas:
				'top units'
				'run run'
				'spec spec'
				'intro intro'
				'price price'
				'then then'
				'act act';
			padding: 1.75rem var(--p-x) 2rem;
		}
		.plans--app .plan + .plan,
		.plans--landing .plan + .plan {
			border-left: 1px solid var(--rule);
		}
		.plans--app .plan + .plan {
			border-top: 0;
		}
		.p-name {
			font-size: var(--p-name);
		}
		/* A box four rows of squares high (45px), so a one-row Solo and a
		   four-row Pro start on the same top line. */
		.p-units {
			min-height: 45px;
			padding-top: 0;
		}
		.p-run {
			margin-top: 0.75rem;
		}
		.p-spec {
			display: block;
			margin-top: 1.125rem;
			padding-top: 0;
			border-top: 0;
		}
		.p-spec > div {
			display: flex;
			align-items: baseline;
			justify-content: space-between;
			gap: 1rem;
			padding-block: 0.5rem;
			border-top: 1px solid var(--rule);
		}
		.p-spec > div:last-child {
			border-bottom: 1px solid var(--rule);
		}
		.p-spec dt {
			font-size: 0.875rem;
		}
		.p-spec dd {
			margin-top: 0;
			font-family: var(--font-sans);
			font-size: 0.875rem;
		}
		/* The offer's row is as tall as the one cell that has it, so the
		   others keep the room and their rules stay level; with no offer
		   on any cell (a plan change) it takes none. */
		.p-intro {
			display: flex;
			align-items: center;
		}
		.p-intro:has(.badge) {
			margin-top: 1.25rem;
		}
		.p-price {
			justify-self: stretch;
			margin-top: 1.25rem;
			padding-top: 1rem;
		}
		.p-price .n {
			font-size: var(--p-price);
		}
		.p-act {
			margin-top: 1.5rem;
		}
	}
	@media (forced-colors: active) {
		.p-price {
			border-top: 3px solid CanvasText;
		}
	}
</style>
