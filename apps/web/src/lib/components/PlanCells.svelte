<!--
  The three plans from one source (lib/plans.ts, DECISIONS I-663, I-837):
  the landing's pricing, /docs/billing and /billing's plan picker draw this,
  so the three cannot disagree. One card per plan, a hairline edge on the
  page colour; the plan the account is on takes the stronger edge, the
  surface and a "Current" badge, and nothing else marks a card.

  Each card is the same stack: the name; the price, with the list price
  struck while an introductory price applies (public) and the line that
  says when it ends; a hairline; the memory in words; disk and egress; a
  line for a figure the account already passes (id over-{plan id}, for the
  action's aria-describedby); the action the page gives.
  From 900px the cards share their rows (a subgrid), so each line runs
  level across the three whatever one card holds that the others do not.

  variant 'public' (landing, docs): no action, disk and egress on one line,
  the founding terms under the price. variant 'app' (/billing): disk and
  egress as two rows, the introductory offer as the line under the price.
-->
<script lang="ts">
	import type { Snippet } from 'svelte';
	import { price, gbs } from '$lib/format';
	import { memoryLine, planSize, type PlanFigures, type PlanId } from '$lib/plans';

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
		/** The plan the account is on: the stronger edge and a "Current" badge. */
		current?: PlanId;
		/** The card's action (a button, or a line saying why there is none), at its foot. */
		action?: Snippet<[PlanFigures]>;
		/** What the account uses now: a figure past a plan's limit is said under its specs. */
		compare?: { runningGB: number; diskHeldGB: number };
		/** The introductory price applies to this visitor's checkout (I-497). */
		introEligible?: boolean;
		/** 'public' on the landing and in the docs; 'app' on /billing. */
		variant?: 'public' | 'app';
		/** The names' heading level: 3 under a section title, 2 under /billing's h1. */
		level?: 2 | 3;
		label?: string;
	} = $props();

	const offer = (p: PlanFigures) => (introEligible ? p.intro : undefined);
</script>

<ul class="plans plans--{variant}" aria-label={label}>
	{#each plans as p (p.id)}
		{@const intro = offer(p)}
		{@const overRun = !!compare && compare.runningGB > p.memoryGB}
		{@const overDisk = !!compare && compare.diskHeldGB > p.diskGB}
		<li class="plan" class:current={current === p.id} data-testid="plan-{p.id}">
			<div class="p-top">
				<svelte:element this={`h${level}`} class="p-name">{p.name}</svelte:element>
				{#if current === p.id}<span class="hk-badge hk-badge--outline">Current</span>{/if}
			</div>
			<p class="p-price">
				{#if intro && variant === 'public'}<s class="was">{price(p.priceCents)}</s>{/if}
				<span class="n">{price(intro ? intro.priceCents : p.priceCents)}</span>
				<span class="per">a month</span>
			</p>
			{#if intro}
				<p class="p-then">
					{#if variant === 'public'}
						First {intro.months} months{#if intro.limit}, first {intro.limit} subscribers{/if}
					{:else}
						<span data-testid="intro-{p.id}"
							>{price(intro.priceCents)} for {intro.months} months</span
						>, then {price(p.priceCents)}
						{#if intro.egressGB !== p.egressGB}and <span class="nw">{planSize(p.egressGB)}</span>
							egress{/if}
						from <span class="nw">month {intro.months + 1}</span>
					{/if}
				</p>
			{/if}
			<p class="p-run">{memoryLine(p.memoryGB)}</p>
			{#if variant === 'public'}
				<p class="p-specs p-specs--line">
					{planSize(p.diskGB)} disk · {planSize(intro ? intro.egressGB : p.egressGB)} egress
				</p>
			{:else}
				<dl class="p-specs">
					<div>
						<dt>Disk</dt>
						<dd>{planSize(p.diskGB)}</dd>
					</div>
					<div>
						<dt>Egress a month</dt>
						<dd>{planSize(intro ? intro.egressGB : p.egressGB)}</dd>
					</div>
				</dl>
			{/if}
			{#if compare && (overRun || overDisk)}
				<!-- over-{id}: a refused action names this line as its reason (aria-describedby). -->
				<div class="p-over" id="over-{p.id}">
					{#if overRun}
						<p>
							<span class="hk-dot hk-dot--warn" aria-hidden="true"></span>Running
							<span class="nw">{gbs(compare.runningGB)}</span>, holds
							<span class="nw">{gbs(p.memoryGB)}</span>
						</p>
					{/if}
					{#if overDisk}
						<p>
							<span class="hk-dot hk-dot--warn" aria-hidden="true"></span>Projects hold
							<span class="nw">{gbs(compare.diskHeldGB)}</span>, allows
							<span class="nw">{planSize(p.diskGB)}</span>
						</p>
					{/if}
				</div>
			{/if}
			{#if action && variant === 'app'}
				<div class="p-act">{@render action(p)}</div>
			{/if}
		</li>
	{/each}
</ul>

<style>
	.plans {
		display: grid;
		gap: 1rem;
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.plan {
		display: grid;
		grid-template-columns: minmax(0, 1fr);
		align-content: start;
		min-width: 0;
		padding: 1.5rem;
		border: 1px solid var(--hk-border);
		border-radius: var(--hk-radius-lg);
	}
	.plan.current {
		border-color: var(--hk-border-strong);
		background: var(--hk-surface);
	}

	.p-top {
		grid-row: 1;
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.375rem 0.625rem;
		min-height: 1.25rem;
	}
	/* A heading element for the tests and the outline (billing.spec reads
	   the names as headings), drawn as the kit's small caps label: sans,
	   never the heading serif. */
	.p-name {
		margin: 0;
		font-family: var(--hk-font-sans);
		font-size: 0.6875rem;
		font-weight: 600;
		line-height: 1.25rem;
		letter-spacing: 0.16em;
		text-transform: uppercase;
		color: var(--hk-text-3);
	}

	.p-price {
		grid-row: 2;
		display: flex;
		flex-wrap: wrap;
		align-items: baseline;
		gap: 0.375rem;
		margin: 0;
		padding-top: 0.75rem;
	}
	.p-price .was {
		font-size: 0.9375rem;
		color: var(--hk-text-3);
		font-variant-numeric: tabular-nums;
	}
	.p-price .n {
		font-family: var(--hk-font-display);
		font-size: 2.25rem;
		font-weight: 400;
		line-height: 2.5rem;
		letter-spacing: -0.02em;
		font-variant-numeric: tabular-nums;
		color: var(--hk-text);
	}
	.p-price .per {
		font-size: 0.875rem;
		color: var(--hk-text-3);
		white-space: nowrap;
	}
	.p-then {
		grid-row: 3;
		margin: 0;
		padding-top: 0.25rem;
		font-size: 0.8125rem;
		line-height: 1.25rem;
		color: var(--hk-text-3);
		text-wrap: pretty;
	}
	/* A figure never splits between its number and its unit. */
	.nw {
		white-space: nowrap;
	}

	.p-run {
		grid-row: 4;
		margin: 1.25rem 0 0;
		padding-top: 1.25rem;
		border-top: 1px solid var(--hk-hairline);
		font-size: 0.9375rem;
		font-weight: 500;
		color: var(--hk-text);
	}
	.p-specs {
		grid-row: 5;
		margin: 0;
		padding-top: 0.25rem;
	}
	.p-specs--line {
		font-size: 0.8125rem;
		color: var(--hk-text-2);
	}
	.p-specs > div {
		display: flex;
		align-items: baseline;
		justify-content: space-between;
		gap: 1rem;
		padding-block: 0.5rem;
		border-bottom: 1px solid var(--hk-hairline);
		font-size: 0.8125rem;
	}
	.p-specs dt {
		color: var(--hk-text-3);
	}
	.p-specs dd {
		margin: 0;
		color: var(--hk-text);
		font-variant-numeric: tabular-nums;
		white-space: nowrap;
	}

	.p-over {
		grid-row: 6;
		padding-top: 0.75rem;
		font-size: 0.8125rem;
		color: var(--hk-text-2);
	}
	.p-over p {
		margin: 0;
	}
	.p-over p + p {
		margin-top: 0.25rem;
	}
	.p-over .hk-dot {
		margin-right: 0.5rem;
		vertical-align: 0.0625rem;
	}

	.p-act {
		grid-row: 7;
		align-self: end;
		padding-top: 1.25rem;
	}
	/* The page's button fills the card, so the three line up. */
	.p-act :global(:is(a, button)) {
		width: 100%;
	}

	/* From 900px three across, the cards sharing their seven rows. */
	@media (min-width: 900px) {
		.plans {
			grid-template-columns: repeat(3, minmax(0, 1fr));
			grid-template-rows: repeat(7, auto);
			row-gap: 0;
		}
		.plan {
			grid-row: span 7;
			grid-template-rows: subgrid;
		}
	}
	@media (forced-colors: active) {
		.plan.current {
			border-width: 2px;
		}
	}
</style>
