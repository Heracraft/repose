<!--
  The dashboard's places (DECISIONS I-841, Guide B 11.6 with a named
  exception): one `nav aria-label="Dashboard"` drawn two ways.

  From 1024px a 12.5rem rail beside the page, sticky under the header and
  as tall as the window: Projects, Questions (with the pending count),
  Billing and Settings; under a hairline the projects by name, one dot each
  (amber while an agent waits on you); and at its foot the plan, one state
  line per billing mode, left off /billing. Below 1024px the same four
  places are a sticky strip under the header (57 + 44 = 101px, the docs
  sub-bar's line), and in trouble (payment failed, suspended, no plan, the
  waitlist, a held seat) one plan line under it.

  The places paint in the first frame; the project rows fill a group that
  sits above a footer pinned to the rail's foot, so rows that arrive late
  move nothing (I-664).
-->
<script lang="ts">
	import { onMount, tick } from 'svelte';
	import { MediaQuery } from 'svelte/reactivity';
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { afterNavigate } from '$app/navigation';
	import { authState } from '$lib/auth.svelte';
	import { pollWhileVisible } from '$lib/poll';
	import { pendingQuestions } from '$lib/questions.svelte';
	import { projectsStore } from '$lib/projects.svelte';
	import { billingStore } from '$lib/billing.svelte';
	import { footerLines, planLine } from '$lib/billingMode';
	import { orderProjects } from '$lib/projectList';
	import { classMemoryGB } from '$lib/format';
	import { holdsMemory } from '$lib/plans';
	import { frozenSince } from '$lib/frozen';
	import type { GuestState, Project } from '$lib/api/types';
	import Snowflake from './Snowflake.svelte';
	import { notFound } from '$lib/notFound.svelte';

	/** Rows the group shows before "All N". */
	const ROWS = 8;

	type Place = 'projects' | 'questions' | 'billing' | 'settings';
	const places: { id: Place; href: string; label: string }[] = [
		{ id: 'projects', href: resolve('/projects'), label: 'Projects' },
		{ id: 'questions', href: resolve('/questions'), label: 'Questions' },
		{ id: 'billing', href: resolve('/billing'), label: 'Billing' },
		{ id: 'settings', href: resolve('/settings'), label: 'Settings' }
	];

	const TONE: Record<GuestState, 'on' | 'busy' | 'idle' | 'err'> = {
		running: 'on',
		stopped: 'idle',
		creating: 'busy',
		building: 'busy',
		starting: 'busy',
		stopping: 'busy',
		restoring: 'busy',
		destroying: 'busy',
		destroyed: 'idle',
		error: 'err'
	};

	const path = $derived(page.url.pathname);
	const under = (p: string, href: string) => p === href || p.startsWith(`${href}/`);

	let waiting = $derived(pendingQuestions.count);
	let all = $derived(
		orderProjects(projectsStore.list ?? [], (p) => pendingQuestions.waiting(p.id))
	);
	let rows = $derived(all.slice(0, ROWS));

	/** Whether the rail is drawn: the strip below 1024px draws no project rows. */
	const wide = new MediaQuery('min-width: 1024px');

	/** The project a /projects/<id>/… path is under, if its row is drawn. */
	let projectId = $derived(/^\/projects\/([^/]+)/.exec(path)?.[1]);
	let rowCurrent = $derived(
		!page.error &&
			!notFound.on &&
			wide.current &&
			!!projectId &&
			rows.some((p) => p.id === projectId)
	);

	/** One current item per dashboard page; none on an error page (a signed-in 404). */
	function current(id: Place): boolean {
		if (page.error || notFound.on) return false;
		if (id === 'projects') return path === places[0].href || (!!projectId && !rowCurrent);
		return under(path, places.find((p) => p.id === id)!.href);
	}

	function rowName(p: Project): string {
		const asks = pendingQuestions.waiting(p.id);
		const state = frozenSince(p) ? 'frozen' : p.state;
		return `${p.name}, ${state}${asks ? ', waiting on you' : ''}`;
	}

	// The plan. Memory running is summed from the projects list when the
	// rail has one, so a start shows here before the next billing read.
	let runningGB = $derived(
		projectsStore.list
			? projectsStore.list.filter(holdsMemory).reduce((n, p) => n + classMemoryGB(p.class), 0)
			: Math.round((billingStore.info?.usage.running_gb ?? 0) * 10) / 10
	);
	let facts = $derived({ me: billingStore.me, billing: billingStore.info, off: billingStore.off });
	let onBilling = $derived(under(path, resolve('/billing')));
	let foot = $derived(footerLines(billingStore.mode, facts, runningGB));
	let line = $derived(onBilling ? undefined : planLine(billingStore.mode, facts));

	// The shell's reads, once signed in: the pending questions every 30s
	// while the tab is in view (pollWhileVisible ticks every 10s, two of
	// three pass), the projects and billing every 60s while in view, and
	// both again on a navigation that finds them older than 30s. A page
	// that reads any of them itself hands the answer to the store.
	let signedIn = $derived(authState.authenticated === true);
	$effect(() => {
		if (!signedIn) return;
		return pollWhileVisible(() => pendingQuestions.refresh(29_000));
	});
	$effect(() => {
		if (!signedIn) return;
		void projectsStore.refresh(30_000);
		void billingStore.refresh({ maxAge: 30_000 });
		const timer = setInterval(() => {
			if (document.visibilityState !== 'visible') return;
			void projectsStore.refresh(55_000);
			void billingStore.refresh({ maxAge: 55_000 });
		}, 60_000);
		return () => clearInterval(timer);
	});
	afterNavigate((nav) => {
		if (nav.type === 'enter' || !signedIn) return;
		void pendingQuestions.refresh();
		void projectsStore.refresh(30_000);
		void billingStore.refresh({ maxAge: 30_000 });
	});

	// The strip below 1024px: sideways scroll with an edge fade only while
	// places lie past that edge, and the current place centred by
	// scrollLeft (scrollIntoView would scroll the page too).
	let list: HTMLElement | undefined = $state();
	let fadeStart = $state(false);
	let fadeEnd = $state(false);
	function edges() {
		if (!list) return;
		const max = list.scrollWidth - list.clientWidth;
		fadeStart = max > 1 && list.scrollLeft > 1;
		fadeEnd = max > 1 && list.scrollLeft < max - 1;
	}
	async function centre() {
		await tick();
		if (!list) return;
		const cur = list.querySelector<HTMLElement>('[aria-current="page"]');
		if (cur && list.scrollWidth > list.clientWidth) {
			list.scrollLeft = cur.offsetLeft - (list.clientWidth - cur.offsetWidth) / 2;
		}
		edges();
	}
	onMount(() => {
		void centre();
		const ro = new ResizeObserver(edges);
		if (list) ro.observe(list);
		return () => ro.disconnect();
	});
	afterNavigate(() => void centre());
</script>

<div class="rail" data-dash-strip>
	<nav aria-label="Dashboard" class="rail-nav">
		<ul
			class="places"
			class:fade-start={fadeStart}
			class:fade-end={fadeEnd}
			bind:this={list}
			onscroll={edges}
		>
			{#each places as place (place.id)}
				<li>
					<!-- eslint-disable svelte/no-navigation-without-resolve -- place.href is built with resolve() above -->
					<a
						href={place.href}
						class="place"
						aria-current={current(place.id) ? 'page' : undefined}
						aria-label={place.id === 'questions' && waiting > 0
							? `Questions, ${waiting} waiting`
							: undefined}
					>
						<!-- eslint-enable svelte/no-navigation-without-resolve -->
						<svg
							class="icon"
							viewBox="0 0 16 16"
							fill="none"
							stroke="currentColor"
							stroke-width="1.4"
							stroke-linecap="round"
							stroke-linejoin="round"
							aria-hidden="true"
						>
							{#if place.id === 'projects'}
								<rect x="2.5" y="2.5" width="4.5" height="4.5" rx="1" />
								<rect x="9" y="2.5" width="4.5" height="4.5" rx="1" />
								<rect x="2.5" y="9" width="4.5" height="4.5" rx="1" />
								<rect x="9" y="9" width="4.5" height="4.5" rx="1" />
							{:else if place.id === 'questions'}
								<path
									d="M2.5 4a1.5 1.5 0 0 1 1.5-1.5h8A1.5 1.5 0 0 1 13.5 4v5.5A1.5 1.5 0 0 1 12 11H7l-3 2.5V11a1.5 1.5 0 0 1-1.5-1.5Z"
								/>
								<path d="M6.6 5.4a1.45 1.45 0 0 1 2.8.5c0 1-1.4 1.2-1.4 2" />
								<path d="M8 9.3v.01" />
							{:else if place.id === 'billing'}
								<rect x="1.75" y="3.5" width="12.5" height="9" rx="1.5" />
								<path d="M1.75 6.5h12.5M4.5 10h2" />
							{:else}
								<path d="M2.5 4.5h6M11.5 4.5h2M2.5 11.5h2M7.5 11.5h6" />
								<circle cx="10" cy="4.5" r="1.5" />
								<circle cx="6" cy="11.5" r="1.5" />
							{/if}
						</svg>
						<span class="label">{place.label}</span>
						{#if place.id === 'questions' && waiting > 0}
							<span
								class="hk-badge hk-badge--warn hk-num qbadge"
								data-testid="pending-questions"
								aria-hidden="true">{waiting}</span
							>
						{/if}
					</a>
				</li>
			{/each}
		</ul>

		{#if rows.length > 0}
			<div class="group">
				<p class="group-head" id="rail-projects">
					Projects <span class="hk-num">{all.length}</span>
				</p>
				<ul aria-labelledby="rail-projects">
					{#each rows as p (p.id)}
						{@const asks = pendingQuestions.waiting(p.id)}
						<li>
							<a
								href={resolve('/projects/[id]', { id: p.id })}
								class="prow"
								aria-current={rowCurrent && projectId === p.id ? 'page' : undefined}
								aria-label={rowName(p)}
							>
								<span class="mark" aria-hidden="true">
									{#if asks}
										<span class="hk-dot hk-dot--warn" data-tone="warn"></span>
									{:else if frozenSince(p)}
										<Snowflake size={12} />
									{:else}
										<span class="hk-dot hk-dot--{TONE[p.state]}" data-tone={TONE[p.state]}></span>
									{/if}
								</span>
								<span class="slug">{p.name}</span>
							</a>
						</li>
					{/each}
					{#if all.length > ROWS}
						<li>
							<a href={resolve('/projects')} class="prow more">All {all.length}</a>
						</li>
					{/if}
				</ul>
			</div>
		{/if}
	</nav>

	{#if !onBilling && billingStore.mode !== 'off'}
		<section class="foot" aria-label="Plan" data-testid="plan-strip">
			<!-- The three lines are there before billing answers, a bar in
			     each, so the foot's height does not change when they fill. -->
			<p class="foot-label">
				{#if foot}{foot.label}{:else}<span class="hk-skel skel-label"></span>{/if}
			</p>
			{#if !foot || foot.name}
				<p class="foot-name">
					{#if foot}{foot.name}{:else}<span class="hk-skel skel-name"></span>{/if}
				</p>
			{/if}
			<p class="foot-state" class:foot-state--quiet={!foot?.link}>
				{#if !foot}
					<span class="hk-skel skel-state"></span>
				{:else}
					{#if foot.dot}<span class="hk-dot hk-dot--{foot.dot}" aria-hidden="true"></span>{/if}
					{#if foot.link}
						<a href={resolve('/billing')}>{foot.state}</a>
					{:else}
						{foot.state}
					{/if}
				{/if}
			</p>
		</section>
	{/if}
</div>

<div class="plan-line" class:plan-line--reserve={!onBilling && billingStore.reserve}>
	{#if line}
		<p>
			{#if line.dot}<span class="hk-dot hk-dot--{line.dot}" aria-hidden="true"></span>{/if}
			<span>{line.text}</span>
			<a href={resolve('/billing')}>{line.link}</a>
		</p>
	{/if}
</div>

<style>
	/* ---------- Strip, below 1024px ---------- */
	.rail {
		position: sticky;
		top: var(--header-h);
		z-index: 25;
		margin-inline: calc(-1 * var(--frame-x));
		padding-inline: var(--frame-x);
		box-shadow: inset 0 -1px 0 var(--hk-hairline);
		background: color-mix(in srgb, var(--hk-bg) 92%, transparent);
		backdrop-filter: saturate(1.4) blur(10px);
		-webkit-backdrop-filter: saturate(1.4) blur(10px);
	}
	.places {
		display: flex;
		gap: 0.25rem;
		height: 2.75rem;
		margin: 0 calc(-1 * var(--frame-x));
		padding: 0 var(--frame-x);
		list-style: none;
		overflow-x: auto;
		scrollbar-width: none;
	}
	.places::-webkit-scrollbar {
		display: none;
	}
	.places.fade-start {
		mask-image: linear-gradient(to right, transparent, black 2.5rem);
	}
	.places.fade-end {
		mask-image: linear-gradient(to left, transparent, black 2.5rem);
	}
	.places.fade-start.fade-end {
		mask-image: linear-gradient(
			to right,
			transparent,
			black 2.5rem,
			black calc(100% - 2.5rem),
			transparent
		);
	}
	.places li {
		display: flex;
		flex: none;
	}
	/* A thumb's 44px row; the 32px pill is drawn by ::before. */
	.place {
		position: relative;
		isolation: isolate;
		display: flex;
		align-items: center;
		gap: 0.5rem;
		height: 2.75rem;
		padding: 0 0.625rem;
		font-size: 0.8125rem;
		color: var(--hk-text-2);
		text-decoration: none;
		white-space: nowrap;
		transition: color var(--hk-dur-fast) var(--hk-ease);
	}
	/* The first place lines up with the page's left edge. */
	.places li:first-child .place {
		margin-left: -0.625rem;
	}
	.place::before {
		content: '';
		position: absolute;
		inset: 0.375rem 0;
		z-index: -1;
		border-radius: var(--hk-radius-7);
		transition: background-color var(--hk-dur-fast) var(--hk-ease);
	}
	.place:hover {
		color: var(--hk-text);
	}
	.place:hover::before {
		background: var(--hk-hover);
	}
	.place:focus-visible {
		outline: none;
	}
	.place:focus-visible::before {
		outline: 2px solid var(--hk-accent);
		outline-offset: 0;
	}
	.place[aria-current='page'] {
		color: var(--hk-text);
	}
	.place[aria-current='page']::before {
		background: var(--hk-surface-2);
		box-shadow: inset 0 0 0 1px var(--hk-hairline);
	}
	.icon {
		flex: none;
		width: 1rem;
		height: 1rem;
		color: var(--hk-text-3);
	}
	.place[aria-current='page'] .icon {
		color: var(--hk-accent);
	}
	.qbadge {
		margin-left: auto;
	}
	.group,
	.foot {
		display: none;
	}
	@media (max-width: 479.98px) {
		.icon {
			display: none;
		}
		/* The last pill's trailing padding hangs into the gutter, as the
		   first's leading padding does, so the four fit at 360 with no
		   sideways drag. */
		.places li:last-child .place {
			margin-right: -0.625rem;
		}
	}

	/* The plan line: phones and tablets, trouble only; room is kept for
	   it when the last mode seen was trouble, so it moves nothing in. */
	.plan-line p {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0 0.5rem;
		margin: 0;
		padding-top: 0.75rem;
		font-size: 0.8125rem;
		color: var(--hk-text-2);
	}
	.plan-line--reserve {
		min-height: 2.125rem;
	}
	.plan-line a {
		color: var(--hk-accent);
	}
	@media (pointer: coarse), (max-width: 639.98px) {
		.plan-line a {
			display: inline-flex;
			align-items: center;
			min-height: 2.75rem;
			margin-block: -0.6875rem;
		}
	}

	/* ---------- Rail, from 1024px ---------- */
	@media (min-width: 1024px) {
		.rail {
			display: flex;
			flex-direction: column;
			align-self: start;
			top: var(--header-h);
			/* The window under the header less the compact footer: the grid
			   is at least this tall, so the rail never runs out of row, never
			   slides under the header at a page's foot, and its foot sits at
			   one height on every page (I-841, review IA). While the outage
			   bar shows above the header its height comes off too, so a
			   short page still ends at the window's foot (review r4). */
			height: calc(100dvh - var(--header-h) - var(--dash-foot-h) - var(--outage-h, 0px));
			margin: 0;
			padding: 2.25rem 0 1.5rem;
			box-shadow: none;
			background: none;
			backdrop-filter: none;
			-webkit-backdrop-filter: none;
			overflow-y: auto;
			scrollbar-width: none;
		}
		.rail-nav {
			display: flex;
			flex-direction: column;
			min-height: 0;
		}
		.places {
			flex-direction: column;
			gap: 0.125rem;
			height: auto;
			margin: 0;
			padding: 0;
			overflow: visible;
			mask-image: none !important;
		}
		.place {
			height: 2rem;
		}
		.place::before {
			inset: 0;
		}
		.places li:first-child .place {
			margin-left: 0;
		}
		.place,
		.places li {
			width: 100%;
		}
		.group {
			display: block;
			margin-top: 1.25rem;
			padding-top: 1.25rem;
			border-top: 1px solid var(--hk-hairline);
		}
		.group-head {
			display: flex;
			justify-content: space-between;
			margin: 0 0 0.375rem;
			padding: 0 0.625rem;
			font-size: 0.6875rem;
			font-weight: 500;
			letter-spacing: 0.04em;
			text-transform: uppercase;
			color: var(--hk-text-3);
		}
		.group ul {
			display: flex;
			flex-direction: column;
			gap: 0.125rem;
			margin: 0;
			padding: 0;
			list-style: none;
		}
		.prow {
			display: flex;
			align-items: center;
			gap: 0.625rem;
			height: 1.875rem;
			padding: 0 0.625rem;
			border-radius: var(--hk-radius-7);
			font-size: 0.8125rem;
			color: var(--hk-text-2);
			text-decoration: none;
			transition:
				color var(--hk-dur-fast) var(--hk-ease),
				background-color var(--hk-dur-fast) var(--hk-ease);
		}
		.prow:hover {
			color: var(--hk-text);
			background: var(--hk-hover);
		}
		.prow:focus-visible {
			outline-offset: -2px;
		}
		.prow[aria-current='page'] {
			color: var(--hk-text);
			background: var(--hk-surface-2);
			box-shadow: inset 0 0 0 1px var(--hk-hairline);
		}
		.mark {
			display: inline-flex;
			flex: none;
			align-items: center;
			justify-content: center;
			width: 0.75rem;
			color: var(--hk-text-2);
		}
		.mark .hk-dot {
			width: 6px;
			height: 6px;
		}
		.slug {
			min-width: 0;
			overflow: hidden;
			text-overflow: ellipsis;
			white-space: nowrap;
		}
		.more {
			color: var(--hk-text-3);
			padding-left: 2rem;
		}
		.foot {
			display: block;
			flex: none;
			margin-top: auto;
			padding: 1.25rem 0.625rem 0;
			border-top: 1px solid var(--hk-hairline);
		}
		/* The page above the rail's foot keeps its own space. */
		.rail-nav {
			padding-bottom: 2rem;
		}
		.foot p {
			margin: 0;
		}
		.foot-label {
			font-size: 0.75rem;
			color: var(--hk-text-3);
		}
		.foot-name {
			margin-top: 0.125rem !important;
			font-family: var(--hk-font-display);
			font-size: 1.25rem;
			line-height: 1.75rem;
			color: var(--hk-text);
		}
		.foot-state {
			display: flex;
			align-items: center;
			gap: 0.5rem;
			margin-top: 0.25rem !important;
			font-size: 0.8125rem;
		}
		.foot-state--quiet {
			color: var(--hk-text-2);
		}
		.foot-state a {
			color: var(--hk-accent);
			text-decoration: none;
		}
		.foot-state a:hover {
			text-decoration: underline;
		}
		.foot .hk-skel {
			display: inline-block;
			vertical-align: middle;
			height: 0.75rem;
			border-radius: var(--hk-radius-xs);
		}
		.skel-label {
			width: 2.5rem;
		}
		.foot .skel-name {
			width: 4rem;
			height: 1.25rem;
		}
		.skel-state {
			width: 9rem;
		}
		.foot-label,
		.foot-state {
			min-height: 1.25rem;
		}
		.plan-line {
			display: none;
		}
	}
	/* Forced colours: the pill's fill is gone, so the current item takes
	   layout.css's nav underline, which the text-decoration above beat. */
	@media (forced-colors: active) {
		.place[aria-current='page'],
		.prow[aria-current='page'] {
			text-decoration: underline;
			text-decoration-thickness: 2px;
			text-underline-offset: 0.35em;
		}
	}
</style>
