<script lang="ts">
	import './landing.css';
	import '$lib/components/landing/pictures.css';
	import { onMount, type Component } from 'svelte';
	import { resolve } from '$app/paths';
	import { toast } from 'svelte-sonner';
	import { SIGN_IN_FAILED, authState, onSignInLinkClick } from '$lib/auth.svelte';
	import { signinHref, type ReturnPath } from '$lib/signin';
	import { INSTALL_COMMAND } from '$lib/links';
	import { publicSeats } from '$lib/api/client';
	import { price } from '$lib/format';
	import type { PublicSeats } from '$lib/api/types';
	import { OWN_HARDWARE, PLANS } from '$lib/plans';
	import PlanCells from '$lib/components/PlanCells.svelte';
	import CodeCopy from '$lib/components/CodeCopy.svelte';
	import HeroVisual from '$lib/components/landing/HeroVisual.svelte';
	import ScrollWheel from '$lib/components/landing/ScrollWheel.svelte';
	import Wreck from '$lib/components/landing/Wreck.svelte';
	import PauseButton from '$lib/components/landing/PauseButton.svelte';
	import MacFigures from '$lib/components/landing/MacFigures.svelte';
	import YourHardware from '$lib/components/landing/YourHardware.svelte';
	import { MAC_DESIGN2_LIVE } from '$lib/data/mac-figures';
	import { loadHero } from '$lib/components/landing/heroes';

	/** Optional `?hero=` gallery candidate override; defaults to HeroVisual. */
	let HeroAlt = $state<Component | null>(null);
	onMount(async () => {
		const name = new URLSearchParams(window.location.search).get('hero');
		if (!name) return;
		const alt = await loadHero(name);
		if (alt) HeroAlt = alt;
	});

	/** GET /public/seats (I-290); undefined until it answers, and if it never does. */
	let seats = $state<PublicSeats | undefined>(undefined);

	onMount(() => {
		publicSeats()
			.then((s) => (seats = s))
			.catch(() => {
				// The plans stand on their own without the count.
			});
		const params = new URLSearchParams(location.search);
		if (params.get('error')) {
			toast.error(SIGN_IN_FAILED);
			history.replaceState(null, '', location.pathname);
		}
	});

	let startMode = $state<'cloud' | 'own'>('cloud');

	const install = { title: 'Install', command: INSTALL_COMMAND, what: 'the install command' };
	const login = { title: 'Sign in', command: 'repose login', what: 'the sign-in command' };
	const run = { title: 'Run in a repo', command: 'repose run', what: 'repose run' };
	const hostAdd = {
		title: 'Add your machine',
		command: 'repose host add',
		what: 'the host add command'
	};
	const steps = $derived(
		startMode === 'cloud' ? [install, login, run] : [install, login, hostAdd, run]
	);

	/* Sandboxes of the default `small` size (4 GB, docs/machine) that a
	   plan's memory runs at once: the bar under each plan draws them. */
	const SMALL_GB = 4;
	const COUNT = ['', 'One', 'Two', 'Three', 'Four', 'Five', 'Six', 'Seven', 'Eight'];
	const fits = (gb: number) => Math.floor(gb / SMALL_GB);
	const maxFit = Math.max(...PLANS.map((p) => fits(p.memoryGB)));

	const introEligible = $derived(seats?.founding_left !== 0);
	const solo = PLANS[0];
	const soloPrice = $derived(introEligible && solo.intro ? solo.intro.priceCents : solo.priceCents);
</script>

<svelte:head>
	<title>repose: sandboxes for your coding agents</title>
	<meta
		name="description"
		content="Run Claude Code, Codex, and other coding agents with full permissions in isolated Linux VMs with whole-disk undo. On our cloud, or your Mac mini or Linux server."
	/>
</svelte:head>

{#snippet authLabel(shown: string, other: string)}
	<span class="swap"><span>{shown}</span><span aria-hidden="true">{other}</span></span>
{/snippet}

{#snippet authAction(
	signedOut: string,
	signedIn: string,
	returnTo?: ReturnPath,
	variant: 'primary' | 'secondary' = 'primary'
)}
	{@const cls =
		variant === 'primary'
			? 'hk-btn hk-btn-lg hk-btn--touch'
			: 'hk-btn-secondary hk-btn-lg hk-btn--touch'}
	{#if authState.authenticated}
		<a href={resolve('/projects')} class={cls}>{@render authLabel(signedIn, signedOut)}</a>
	{:else}
		<!-- eslint-disable svelte/no-navigation-without-resolve -- signinHref() calls resolve('/signin') and adds the return path (lib/signin) -->
		<a href={signinHref(returnTo)} class={cls} onclick={(e) => onSignInLinkClick(e, returnTo)}
			>{@render authLabel(signedOut, signedIn)}</a
		>
		<!-- eslint-enable svelte/no-navigation-without-resolve -->
	{/if}
{/snippet}

{#snippet label(n: string, name: string)}
	<p class="label num"><span class="n">{n + ' '}</span>{name}</p>
{/snippet}

<main id="main" tabindex="-1" class="frame landing">
	<section class="band hero" aria-labelledby="hero-h">
		<div class="hero-grid">
			<div class="hero-copy">
				<h1 class="hk-h1" id="hero-h">A sandbox per repo, with undo</h1>
				<p class="hk-lead">
					Agents get full permissions in a Linux VM set up like your laptop, on our cloud or your
					Mac mini or Linux server.
				</p>
				<div class="hero-act">
					<div class="hero-btns">
						{@render authAction('Start a free week', 'Open the dashboard', '/billing')}
						<a class="hk-more hero-docs" href={resolve('/docs')}>Read docs</a>
					</div>
					<div class="hero-install">
						<CodeCopy
							command={INSTALL_COMMAND}
							what="the install command"
							prompt
							labelFrom={1200}
						/>
					</div>
				</div>
				<p class="hero-meta">
					<span class="nw">{solo.trialDays} days free</span>
					<span class="sep" aria-hidden="true">·</span>
					<a class="meta-link nw" href="#pricing">Cloud from {price(soloPrice)}/mo</a>
					<span class="sep" aria-hidden="true">·</span>
					<a class="meta-link nw" href="#pricing"
						>Your Mac mini or Linux server {price(OWN_HARDWARE.priceCents)}/mo</a
					>
				</p>
			</div>

			<div class="hero-pane hero-stage">
				<div class="hero-term">
					{#if HeroAlt}
						<HeroAlt />
					{:else}
						<HeroVisual />
					{/if}
				</div>
			</div>
		</div>
	</section>

	<section class="band band--wheel" id="agents" aria-labelledby="where-h">
		<ScrollWheel />
	</section>

	<section class="band" id="safety" aria-labelledby="safety-h">
		<div class="head">
			{@render label('02', 'Safety')}
			<h2 class="hk-h2" id="safety-h">Let the agent break the whole machine</h2>
		</div>
		<div class="fig">
			<div class="stage">
				<Wreck />
			</div>
			<PauseButton />
		</div>
	</section>

	<section class="band" id="hardware" aria-labelledby="hardware-h">
		<div class="head">
			{@render label('03', 'Your hardware')}
			<h2 class="hk-h2" id="hardware-h">Run it on hardware you own</h2>
			<div class="links side">
				<a class="hk-more" href={resolve('/docs/[slug]', { slug: 'own-hardware' })}>What it needs</a
				>
			</div>
		</div>
		<YourHardware />
		{#if MAC_DESIGN2_LIVE}<MacFigures />{/if}
	</section>

	<section class="band" id="pricing" aria-labelledby="pricing-h">
		<div class="head head--pricing">
			{@render label('04', 'Pricing')}
			<div class="pricing-title">
				<h2 class="hk-h2" id="pricing-h">Pay for what runs at once</h2>
				<p class="copy pricing-lead">Stopped sandboxes don't count against your memory.</p>
			</div>
			<div class="cta-row side">
				{@render authAction('Start a free week', 'Open the dashboard', '/billing')}
				{#if !authState.authenticated && seats}
					<p class="seats" data-testid="seats-line">
						{#if seats.free > 0}
							{seats.free} cloud {seats.free === 1 ? 'seat' : 'seats'} left
						{:else}
							Cloud full for now, {seats.waiting} waiting. Your machine has no waitlist.
						{/if}
					</p>
				{/if}
			</div>
		</div>
		<PlanCells plans={PLANS} {introEligible} variant="public" level={3} />
		<ul class="mem" aria-label="Sandboxes each plan runs at once">
			{#each PLANS as p (p.id)}
				{@const n = fits(p.memoryGB)}
				<li data-testid="mem-{p.id}">
					<span class="mem-p">{p.name}</span>
					<span class="mem-bar" aria-hidden="true">
						{#each Array.from({ length: maxFit }, (_v, i) => i) as i (i)}<i class:on={i < n}
							></i>{/each}
					</span>
					<span class="mem-n"
						>{COUNT[n] ?? n} {SMALL_GB} GB {n === 1 ? 'sandbox' : 'sandboxes'}</span
					>
				</li>
			{/each}
		</ul>
		<div class="hw" id="parts" data-testid="plan-own">
			<h3 class="hw-name">Your machine</h3>
			<p class="hw-price">
				<span class="n">{price(OWN_HARDWARE.priceCents)}</span>
				<span class="per">a month</span>
			</p>
			<p class="hw-what">
				Any number of hosts you own. Each runs as many sandboxes as its memory and disk hold.
			</p>
		</div>
	</section>

	<section class="band install-band" id="install" aria-labelledby="install-h">
		<div class="head">
			{@render label('05', 'Start')}
			<h2 class="hk-h2" id="install-h">Run it in your repo</h2>
		</div>
		<div class="start-tabs">
			<div class="hk-tablist" role="tablist" aria-label="Setup path">
				<button
					type="button"
					role="tab"
					aria-selected={startMode === 'cloud'}
					onclick={() => (startMode = 'cloud')}>Cloud</button
				>
				<button
					type="button"
					role="tab"
					aria-selected={startMode === 'own'}
					onclick={() => (startMode = 'own')}>Your machine</button
				>
			</div>
		</div>
		<ol class="ruled steps">
			{#each steps as step (step.title)}
				<li>
					<h3 class="step-title">{step.title}</h3>
					<CodeCopy command={step.command} what={step.what} prompt labelFrom={1200} />
				</li>
			{/each}
		</ol>
		<div class="start-cta">
			{@render authAction('Start a free week', 'Open the dashboard', '/billing')}
		</div>
	</section>
</main>
