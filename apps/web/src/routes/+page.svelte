<script lang="ts">
	import './landing.css';
	import '$lib/components/landing/pictures.css';
	import { onMount } from 'svelte';
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
	// import HeroVisual from '$lib/components/landing/HeroVisual.svelte';
	import Wreck from '$lib/components/landing/Wreck.svelte';
	import PauseButton from '$lib/components/landing/PauseButton.svelte';
	import UseCasesStack from '$lib/components/landing/UseCasesStack.svelte';
	import YourHardware from '$lib/components/landing/YourHardware.svelte';
	import IdleTiers from '$lib/components/landing/IdleTiers.svelte';
	import AttachMix from '$lib/components/landing/AttachMix.svelte';
	// import { loadHero } from '$lib/components/landing/heroes';

	// /** Optional `?hero=` gallery candidate override; defaults to HeroVisual. */
	// let HeroAlt = $state<Component | null>(null);
	// onMount(async () => {
	// 	const name = new URLSearchParams(window.location.search).get('hero');
	// 	if (!name) return;
	// 	const alt = await loadHero(name);
	// 	if (alt) HeroAlt = alt;
	// });

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
		content="Run coding agents with full permissions in isolated Linux VMs with whole-disk undo. On our cloud, or your Linux server or Mac mini."
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
		<div class="hero-top">
			<div class="hero-top-copy">
				<h1 class="hk-h1" id="hero-h">
					A sandbox per repo, <span class="muted">with undo</span>
				</h1>
				<p class="hk-lead">
					Give coding agents full permissions in isolated Linux VMs that keep going after you close
					your laptop — on our cloud, or your Linux server or Mac mini.
				</p>
			</div>
			<div class="hero-top-act">
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
					<span class="nw">{solo.trialDays} days free on cloud</span>
					<span class="sep" aria-hidden="true">·</span>
					<a class="meta-link nw" href="#pricing">Free on your own host</a>
					<span class="sep" aria-hidden="true">·</span>
					<span class="nw">Apache 2.0</span>
				</p>
			</div>
		</div>

		<!-- Desktop: full interactive Wreck -> Restore visual above the fold -->
		<div class="fig hero-wreck-desktop">
			<div class="stage">
				<Wreck />
			</div>
			<PauseButton />
		</div>

		<!-- Mobile: compact single-card summary (avoids the 8-row vertical column) -->
		<div class="hero-wreck-mobile" aria-hidden="true">
			<div class="hwm-card">
				<div class="hwm-row">
					<span class="hwm-dot hwm-dot--ok"></span>
					<span class="hwm-k">Your laptop</span>
					<span class="hwm-v">Untouched</span>
				</div>
				<div class="hwm-row hwm-row--mid">
					<span class="hwm-dot hwm-dot--err"></span>
					<span class="hwm-k">Isolated sandbox</span>
					<span class="hwm-v">Agent breaks OS &amp; deps</span>
				</div>
				<div class="hwm-row hwm-row--undo">
					<span class="hwm-dot hwm-dot--acc"></span>
					<span class="hwm-k">Whole-disk undo</span>
					<span class="hwm-v">Restored in seconds</span>
				</div>
			</div>
		</div>
	</section>

	<section class="band" id="agents" aria-labelledby="agents-h">
		<div class="head">
			{@render label('01', 'Always on')}
			<h2 class="hk-h2" id="agents-h">
				One machine, <span class="muted">any kind of agent</span>
			</h2>
			<p class="copy side">
				Sandboxes stay up after you close your laptop, run on schedules, and wake up on webhooks.
			</p>
		</div>
		<UseCasesStack />
	</section>

	<section class="band" id="hardware" aria-labelledby="hardware-h">
		<div class="head">
			{@render label('02', 'Your hardware')}
			<h2 class="hk-h2" id="hardware-h">
				Run it on hardware <span class="muted">you already own</span>
			</h2>
			<div class="links side">
				<a class="hk-more" href={resolve('/docs/[slug]', { slug: 'own-hardware' })}>What it needs</a
				>
			</div>
		</div>
		<YourHardware />
	</section>

	<section class="band" id="idle" aria-labelledby="idle-h">
		<IdleTiers />
	</section>

	<section class="band" id="attach" aria-labelledby="attach-h">
		<div class="head">
			{@render label('04', 'No lock-in')}
			<h2 class="hk-h2" id="attach-h">
				One command to attach. <span class="muted">Bring anything.</span>
			</h2>
			<p class="copy side">
				Connect from Claude Code Desktop, your terminal, or any frontend. Mix agents and
				subscriptions with full control of the machine.
			</p>
		</div>
		<AttachMix />
	</section>

	<section class="band" id="pricing" aria-labelledby="pricing-h">
		<div class="head head--pricing">
			{@render label('05', 'Pricing')}
			<div class="pricing-title">
				<h2 class="hk-h2" id="pricing-h">Pay for what runs at once</h2>
				<p class="copy pricing-lead">Stopped sandboxes don't count against your memory.</p>
			</div>
			<div class="cta-row side">
				{@render authAction('Start a free week', 'Open the dashboard', '/billing')}
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
				<span class="n">Free</span>
				<span class="per">for your first host</span>
			</p>
			<p class="hw-what">
				Bring your own Linux server or Mac mini — run as many sandboxes as its memory and disk hold.
				Add unlimited hosts for {price(OWN_HARDWARE.priceCents)}/mo, or included with any cloud plan.
			</p>
		</div>
	</section>

	<section class="band install-band" id="install" aria-labelledby="install-h">
		<div class="head">
			{@render label('06', 'Start')}
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
