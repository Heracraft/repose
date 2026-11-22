<!--
  The Own hardware plan (I-1364), beside the three plan cards rather than
  among them: it sells one host the user owns and none of repose's
  memory, disk or egress, so its figures are not the cards' figures and a
  fourth card would hold three blank rows where they are.

  One row on the page's hairline: the name, the price, what it is in one
  line, and the page's action at the end. The three cards are untouched.

  variant 'public' (the landing, the docs): no action, a link instead.
  variant 'app' (/billing): the action the page gives.
-->
<script lang="ts">
	import type { Snippet } from 'svelte';
	import { price } from '$lib/format';
	import type { PlanFigures } from '$lib/plans';

	let {
		plan,
		current = false,
		action,
		meta,
		variant = 'app',
		level = 2
	}: {
		plan: PlanFigures;
		/** The account is on this plan: the stronger edge and a badge. */
		current?: boolean;
		action?: Snippet<[PlanFigures]>;
		/** Optional extra note rendered under the summary line (e.g. hardware requirements). */
		meta?: Snippet;
		variant?: 'public' | 'app';
		level?: 2 | 3;
	} = $props();
</script>

<div class="own own--{variant}" class:current data-testid="plan-{plan.id}">
	<div class="o-top">
		<svelte:element this={`h${level}`} class="o-name">{plan.name}</svelte:element>
		{#if current}<span class="hk-badge hk-badge--outline">Current</span>{/if}
	</div>
	<p class="o-price">
		<span class="o-n">{price(plan.priceCents)}</span>
		<span class="o-per">a month</span>
	</p>
	<div class="o-what">
		<p>
			Connect {plan.hosts === 1 ? 'one host you own' : `${plan.hosts} hosts you own`}. Your RAM and
			disk are the limits.
		</p>
		{#if meta}
			<p class="o-meta">{@render meta()}</p>
		{/if}
	</div>
	{#if action}
		<div class="o-act">{@render action(plan)}</div>
	{/if}
</div>

<style>
	.own {
		display: grid;
		grid-template-columns: minmax(0, 1fr);
		gap: 0.25rem;
		align-items: start;
		margin-top: 1rem;
		padding: 1.5rem;
		border: 1px solid var(--hk-border);
		border-radius: var(--hk-radius-lg);
	}
	.own.current {
		border-color: var(--hk-border-strong);
		background: var(--hk-surface);
	}
	.o-top {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.375rem 0.625rem;
		min-height: 1.25rem;
	}
	/* The cards' name: a heading element for the outline, drawn as the
	   kit's small-caps label, so the row reads as one of them. */
	.o-name {
		margin: 0;
		font-family: var(--hk-font-sans);
		font-size: 0.6875rem;
		font-weight: 600;
		line-height: 1.25rem;
		letter-spacing: 0.16em;
		text-transform: uppercase;
		color: var(--hk-text-3);
	}
	.o-price {
		display: flex;
		flex-wrap: wrap;
		align-items: baseline;
		gap: 0.375rem;
		margin: 0;
		padding-top: 0.5rem;
	}
	.o-price .o-n {
		font-family: var(--hk-font-display);
		font-size: 1.75rem;
		font-weight: 400;
		line-height: 2rem;
		letter-spacing: -0.02em;
		font-variant-numeric: tabular-nums;
		color: var(--hk-text);
	}
	.o-price .o-per {
		font-size: 0.875rem;
		color: var(--hk-text-3);
		white-space: nowrap;
	}
	.o-what {
		margin: 0;
		font-size: 0.875rem;
		line-height: 1.45;
		color: var(--hk-text-2);
		text-wrap: pretty;
	}
	.o-what p {
		margin: 0;
	}
	.o-meta {
		margin-top: 0.25rem !important;
		font-size: 0.8125rem;
		color: var(--hk-text-3);
	}
	.o-act {
		padding-top: 1rem;
	}
	/* From 900px the row reads as a row: the name and price at the left,
	   the line beside them, the action at the right, which is what keeps
	   it from looking like a fourth card. */
	@media (min-width: 900px) {
		.own {
			grid-template-columns: 12rem minmax(0, 1fr) auto;
			column-gap: 2rem;
			align-items: center;
		}
		.o-top {
			grid-column: 1;
			grid-row: 1;
		}
		.o-price {
			grid-column: 1;
			grid-row: 2;
			padding-top: 0.25rem;
		}
		.o-what {
			grid-column: 2;
			grid-row: 1 / span 2;
		}
		.o-act {
			grid-column: 3;
			grid-row: 1 / span 2;
			padding-top: 0;
		}
	}
	@media (forced-colors: active) {
		.own.current {
			border-width: 2px;
		}
	}
</style>
