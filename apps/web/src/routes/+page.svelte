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
	import OwnHardwarePlan from '$lib/components/OwnHardwarePlan.svelte';
	import CodeCopy from '$lib/components/CodeCopy.svelte';
	import Terminal from '$lib/components/landing/Terminal.svelte';
	import PauseButton from '$lib/components/landing/PauseButton.svelte';
	import { loadHero } from '$lib/components/landing/heroes';
	import Localhost from '$lib/components/landing/Localhost.svelte';
	import Browser from '$lib/components/landing/Browser.svelte';
	import Ready from '$lib/components/landing/Ready.svelte';

	/** The hero picture: the real `repose run` terminal, or a `?hero=` gallery candidate. */
	let Hero = $state<Component>(Terminal);
	onMount(async () => {
		const name = new URLSearchParams(window.location.search).get('hero');
		if (!name) return;
		const alt = await loadHero(name);
		if (alt) Hero = alt;
	});

	/** GET /public/seats (I-290); undefined until it answers, and if it never does. */
	let seats = $state<PublicSeats | undefined>(undefined);

	onMount(() => {
		publicSeats()
			.then((s) => (seats = s))
			.catch(() => {
				// The plans stand on their own without the count.
			});
		// Logto sends the user back here (not /callback) when sign-in itself
		// was cancelled or failed before a code was issued.
		const params = new URLSearchParams(location.search);
		if (params.get('error')) {
			toast.error(SIGN_IN_FAILED);
			history.replaceState(null, '', location.pathname);
		}
	});

	// Each row shows exactly what its Copy puts on the clipboard, and runs
	// on a visitor's laptop.
	const steps: { title: string; command: string; what: string }[] = [
		{ title: 'Install the CLI', command: INSTALL_COMMAND, what: 'the install command' },
		{ title: 'Sign in', command: 'repose login', what: 'the sign-in command' },
		{ title: 'Run in any checkout', command: 'repose run', what: 'repose run' }
	];

	// The founding price shows while GET /public/seats has places left, and
	// before it answers (the prerendered page); once it says none, Solo
	// shows its list price alone.
	const introEligible = $derived(seats?.founding_left !== 0);
	const solo = PLANS[0];
	const soloPrice = $derived(
		introEligible && solo.intro ? solo.intro.priceCents : solo.priceCents
	);
</script>

<svelte:head>
	<title>repose: isolated Linux machines for your coding agents</title>
	<meta
		name="description"
		content="repose run gives your repo an isolated Linux VM where Claude Code, Codex and other agents keep working after you close your laptop. On our servers, or on a Mac mini or x86 box you own."
	/>
</svelte:head>

<!-- A sign-in button, or the dashboard link once signed in. Both labels
     share one grid cell so the swap after mount moves nothing (CLS 0). -->
{#snippet authLabel(shown: string, other: string)}
	<span class="swap"><span>{shown}</span><span aria-hidden="true">{other}</span></span>
{/snippet}

{#snippet authAction(signedOut: string, signedIn: string, returnTo?: ReturnPath)}
	{#if authState.authenticated}
		<a href={resolve('/projects')} class="hk-btn hk-btn-lg hk-btn--touch"
			>{@render authLabel(signedIn, signedOut)}</a
		>
	{:else}
		<!-- eslint-disable svelte/no-navigation-without-resolve -- signinHref() calls resolve('/signin') and adds the return path (lib/signin) -->
		<a
			href={signinHref(returnTo)}
			class="hk-btn hk-btn-lg hk-btn--touch"
			onclick={(e) => onSignInLinkClick(e, returnTo)}>{@render authLabel(signedOut, signedIn)}</a
		>
		<!-- eslint-enable svelte/no-navigation-without-resolve -->
	{/if}
{/snippet}

{#snippet label(n: string, name: string)}
	<p class="label num"><span class="n">{n + ' '}</span>{name}</p>
{/snippet}

{#snippet plus()}
	<svg class="plus" viewBox="0 0 16 16" fill="none" stroke="currentColor" aria-hidden="true">
		<path d="M8 3v10M3 8h10" />
	</svg>
{/snippet}

<main id="main" tabindex="-1" class="frame landing">
	<section class="band hero" aria-labelledby="hero-h">
		<div class="hero-copy">
			<h1 class="hk-h1" id="hero-h">Your coding agents, on a machine of their own</h1>
			<p class="hk-lead">
				<code>repose run</code> gives your repo an isolated Linux VM where Claude Code, Codex and
				other agents keep working after you close your laptop. On our servers, or on a Mac mini or
				x86 box you own.
			</p>
			<div class="hero-act">
				{@render authAction('Start a free week', 'Open the dashboard', '/billing')}
				<div class="hero-install">
					<CodeCopy command={INSTALL_COMMAND} what="the install command" prompt labelFrom={640} />
				</div>
			</div>
			<p class="hero-meta">
				{solo.trialDays} days free · Bring your own agent subscriptions or API keys ·
				<a class="meta-link" href="#pricing"
					>Hosted from {price(soloPrice)}/mo or own hardware {price(OWN_HARDWARE.priceCents)}/mo</a
				>
			</p>
		</div>
		<div class="fig fig--hero">
			<div class="stage hero-stage">
				<div class="hero-term">
					<Hero />
				</div>
			</div>
			<!-- Shown only when a looping `?hero=` gallery candidate is active. -->
			<PauseButton />
		</div>
	</section>

	<section class="band" id="how" aria-labelledby="how-h">
		<div class="head">
			{@render label('01', 'How it works')}
			<h2 class="hk-h2" id="how-h">What repose handles for you</h2>
			<p class="copy side">One command replaces manual server setup and ad-hoc SSH tunnels.</p>
		</div>
		<ul class="cols">
			<li>
				{@render label('1.1', 'One-command sync')}
				<h3 class="item-title">Sync your state in one command</h3>
				<p class="copy">
					<code>repose run</code> copies your branch, uncommitted edits, untracked files,
					<code>.env</code> and CLI logins to a fresh Linux VM while skipping
					<code>node_modules</code>. Pull the agent's commits back with
					<code>git fetch repose</code>.
				</p>
				<p class="cmd">repose run</p>
			</li>
			<li>
				{@render label('1.2', 'Full permissions')}
				<h3 class="item-title">Run unattended off your laptop</h3>
				<p class="copy">
					Each project runs in its own virtual machine as <code>dev</code> with <code>sudo</code>
					and full permissions. The agent can touch that machine and the logins on it, while your
					laptop and other repos stay walled off.
				</p>
				<p class="cmd">repose run -p "..."</p>
			</li>
			<li>
				{@render label('1.3', 'Snapshots')}
				<h3 class="item-title">Roll back the whole disk, or fork it</h3>
				<p class="copy">
					repose snapshots the disk nightly and at every stop, keeping seven days plus the newest.
					Restore a broken database or checkout in place, or clone a snapshot to a new machine with
					<code>--as</code>.
				</p>
				<p class="cmd">repose snapshots restore</p>
			</li>
		</ul>
	</section>

	<section class="band" id="features" aria-labelledby="features-h">
		<div class="head">
			{@render label('02', 'Workflow')}
			<h2 class="hk-h2" id="features-h">Feels like localhost, runs while you're away</h2>
			<p class="copy side">
				Use the tools already on your laptop while attached, and close the lid once the agent is
				underway.
			</p>
		</div>
		<ul class="cells">
			<li class="fig">
				<Localhost animated={false} />
				<div class="cell-copy">
					{@render label('2.1', 'Ports')}
					<h3 class="item-title">Use its ports on your localhost</h3>
					<p class="copy">
						Ports your servers listen on, from 1024 up, forward to your laptop's localhost while
						you're attached, so cookies and OAuth redirects behave as they do locally.
					</p>
					<p class="cmd">repose attach</p>
				</div>
			</li>
			<li class="fig">
				<Browser animated={false} />
				<div class="cell-copy">
					{@render label('2.2', 'Browser')}
					<h3 class="item-title">Watch the agent's browser, and take over</h3>
					<p class="copy">
						Every agent gets Playwright and Chrome DevTools wired to Chromium on the machine. Open
						the view on your laptop to watch, log in or clear a captcha yourself.
					</p>
					<p class="cmd">repose browser</p>
				</div>
			</li>
			<li class="cell--text">
				<div class="cell-copy">
					{@render label('2.3', 'Editor')}
					<h3 class="item-title">Open it in your editor</h3>
					<p class="copy">
						Each machine is an SSH host in your config. Open it from your laptop in VS Code, Cursor
						or Zed, or run Neovim directly on the machine.
					</p>
					<p class="cmd">ssh your-project.repose</p>
				</div>
			</li>
			<li class="cell--text">
				<div class="cell-copy">
					{@render label('2.4', 'Away & idle')}
					<h3 class="item-title">Close your laptop and get notified</h3>
					<p class="copy">
						Agents keep working after you disconnect and notify you by email or phone when they
						finish or ask a question. On hosted plans, quiet machines freeze after 15 minutes and
						wake when a command or webhook arrives.
					</p>
					<p class="cmd">repose ls</p>
				</div>
			</li>
		</ul>
	</section>

	<section class="band" id="agents" aria-labelledby="agents-h">
		<div class="head">
			{@render label('03', 'Agents & tools')}
			<h2 class="hk-h2" id="agents-h">Preinstalled on first boot</h2>
			<p class="links side">
				<a class="hk-more" href={resolve('/docs/[slug]', { slug: 'agents' })}>All agents</a>
				<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -- resolve() with a fragment appended -->
				<a class="hk-more" href={resolve('/docs/[slug]', { slug: 'machine' }) + '#whats-installed'}
					>Everything installed</a
				>
			</p>
		</div>
		<div class="fig">
			<Ready />
		</div>
	</section>

	<section class="band" id="pricing" aria-labelledby="pricing-h">
		<div class="head">
			{@render label('04', 'Pricing')}
			<h2 class="hk-h2" id="pricing-h">Launch prices</h2>
			<div class="cta-row side">
				{@render authAction('Start a free week', 'Open the dashboard', '/billing')}
				{#if !authState.authenticated && seats}
					<p class="seats" data-testid="seats-line">
						{#if seats.free > 0}
							{seats.free}
							{seats.free === 1 ? 'seat' : 'seats'} left
						{:else}
							Full for now. {seats.waiting} waiting; join the list and you're emailed when a seat
							frees.
						{/if}
					</p>
				{/if}
			</div>
		</div>
		<p class="pricing-sub">
			{solo.trialDays} days free on every plan. You pay for memory running at once and bring your own
			agent subscriptions or API keys.
		</p>
		<PlanCells plans={PLANS} {introEligible} variant="public" level={3} />
		<p class="pricing-note">
			Mix <code>tiny</code> (2 GB), <code>small</code> (4 GB), <code>large</code> (8 GB) and
			<code>xl</code> (16 GB) machines up to your plan's running memory; stopped projects count only
			toward disk. Up to 100 projects per account, with snapshots in cloud storage.
		</p>
		<OwnHardwarePlan plan={OWN_HARDWARE} variant="public" level={3} />
		<!-- No start time for either home while the Mac's is on the board (ENG-70),
		     and no Mac memory sizing claims until ENG-91 lands. -->
		<div class="pricing-note pricing-note--split" id="parts">
			<p>
				Needs an M3 or newer Mac on macOS 15 with 16 GB, or an x86 box with Linux and KVM. Snapshots
				stay on its disk.
			</p>
			<p class="links">
				<a class="hk-more" href={resolve('/docs/[slug]', { slug: 'own-hardware' })}
					>Your own hardware</a
				>
				<a class="hk-more" href={resolve('/docs/[slug]', { slug: 'billing' })}>Billing</a>
			</p>
		</div>
	</section>

	<section class="band faq-band" id="questions" aria-labelledby="faq-h">
		<div class="head">
			{@render label('05', 'Questions')}
			<h2 class="hk-h2" id="faq-h">Before you try it</h2>
			<p class="links side">
				<a class="hk-more" href={resolve('/docs/[slug]', { slug: 'limits' })}>Limits</a>
				<a class="hk-more" href={resolve('/docs/[slug]', { slug: 'billing' })}>Billing</a>
			</p>
		</div>
		<div class="ruled faq">
			<details>
				<summary class="item-title"
					>Do I bring my own Claude, Codex or Gemini account?{@render plus()}</summary
				>
				<p class="copy">
					Yes. repose gives your agents their machine and tools, and you use your own agent
					subscriptions or API keys. Claude Code logs in once inside a machine; Codex, opencode and
					<code>gh</code> logins copy from your laptop unless you opt out.
				</p>
			</details>
			<details>
				<summary class="item-title"
					>Can I run it on a Mac mini or x86 box I already own?{@render plus()}</summary
				>
				<p class="copy">
					Yes, on the Own hardware plan for {price(OWN_HARDWARE.priceCents)} a month. You need an M3
					or newer Mac on macOS 15 with 16 GB, or an x86 box with Linux and KVM.
					<code>repose host add</code> puts one virtual machine on a Mac and leaves the rest untouched,
					and <code>repose host rm</code> takes it off again with its disk.
				</p>
			</details>
			<details>
				<summary class="item-title"
					>What happens when my Mac sleeps or restarts?{@render plus()}</summary
				>
				<p class="copy">
					Your machines are out of reach until the Mac answers again, and what they wrote to disk is
					kept. A restart starts them fresh: their disks stay as they were, and what they held in
					memory is gone.
				</p>
			</details>
			<details>
				<summary class="item-title">What can an agent reach on the machine?{@render plus()}</summary
				>
				<p class="copy">
					It runs as <code>dev</code> with <code>sudo</code> on its own machine, with the logins you
					copied and the secrets you set. Other projects live on separate machines it cannot reach,
					and it connects to your laptop only through a port or a Chrome window you lend it.
				</p>
			</details>
			<details>
				<summary class="item-title">What does an agent cost while it waits?{@render plus()}</summary>
				<p class="copy">
					On hosted plans, a quiet machine freezes after about 15 minutes and wakes in a second or
					two when a command or webhook arrives. You pay for the memory your plan runs at once, so a
					waiting or stopped project adds no extra charge.
				</p>
			</details>
		</div>
	</section>

	<section class="band install-band" id="install" aria-labelledby="install-h">
		<div class="head">
			{@render label('06', 'Start')}
			<h2 class="hk-h2" id="install-h">Start in three commands</h2>
			<div class="cta-row side">
				{@render authAction('Start a free week', 'Open the dashboard', '/billing')}
			</div>
		</div>
		<ol class="ruled steps">
			{#each steps as step, i (step.title)}
				<li>
					<h3 class="item-title step-title">
						<span class="label" aria-hidden="true">6.{i + 1}</span>{step.title}
					</h3>
					<CodeCopy command={step.command} what={step.what} labelFrom={1200} />
				</li>
			{/each}
		</ol>
		<div class="steps-foot">
			<p class="links">
				<a class="hk-more" href={resolve('/docs/[slug]', { slug: 'install' })}
					>Install and sign in</a
				>
				<a class="hk-more" href={resolve('/docs/[slug]', { slug: 'run-and-attach' })}
					>Run and attach</a
				>
			</p>
		</div>
	</section>
</main>
