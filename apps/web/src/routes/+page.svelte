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

	const stepCount = $derived(['', '', '', 'Three', 'Four'][steps.length] ?? String(steps.length));

	const introEligible = $derived(seats?.founding_left !== 0);
</script>

<svelte:head>
	<title>repose: sandboxes for your coding agents</title>
	<meta
		name="description"
		content="Run Claude Code, Codex, and other coding agents with full permissions in isolated Linux VMs with whole-disk undo. On our cloud, or your own Mac mini or Linux server."
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
				<h1 class="hk-h1" id="hero-h">Sandboxes for your coding agents</h1>
				<p class="hk-lead">
					A Linux VM per repo, set up like your laptop, on our cloud or your Mac mini or Linux
					server. Agents get full permissions; a snapshot undoes what they break.
				</p>
				<div class="hero-act">
					<div class="hero-btns">
						{@render authAction('Start a free week', 'Open the dashboard', '/billing')}
						<a class="hk-more hero-docs" href={resolve('/docs')}>Read docs</a>
					</div>
				</div>
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
			<h2 class="hk-h2" id="safety-h">Let it break the whole machine</h2>
		</div>
		<div class="fig">
			<div class="stage">
				<Wreck />
			</div>
			<PauseButton />
		</div>
	</section>

	<section class="band" id="pricing" aria-labelledby="pricing-h">
		<div class="head head--pricing">
			{@render label('03', 'Pricing')}
			<div class="pricing-title">
				<h2 class="hk-h2" id="pricing-h">Launch prices</h2>
				<p class="copy pricing-lead">Stopped sandboxes don't count.</p>
			</div>
			{#if !authState.authenticated && seats}
				<p class="seats side" data-testid="seats-line">
					{#if seats.free > 0}
						{seats.free} cloud {seats.free === 1 ? 'seat' : 'seats'} left
					{:else}
						Cloud full for now, {seats.waiting} waiting. Your machine has no waitlist.
					{/if}
				</p>
			{/if}
		</div>
		<PlanCells plans={PLANS} {introEligible} variant="public" level={3} />
		<div class="hw" id="parts" data-testid="plan-own">
			<h3 class="hw-name">Your Mac mini or Linux server</h3>
			<p class="hw-price">
				<span class="n">{price(OWN_HARDWARE.priceCents)}</span>
				<span class="per">a month</span>
			</p>
			<p class="hw-what">
				{OWN_HARDWARE.hosts === 1 ? 'One machine.' : `${OWN_HARDWARE.hosts} machines.`} Run as many sandboxes
				as its memory and disk hold.
			</p>
		</div>
	</section>

	<section class="band install-band" id="install" aria-labelledby="install-h">
		<div class="head">
			{@render label('04', 'Start')}
			<h2 class="hk-h2" id="install-h">{stepCount} commands</h2>
			<div class="cta-row side start-actions">
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
				{@render authAction('Start a free week', 'Open the dashboard', '/billing')}
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
	</section>
</main>
