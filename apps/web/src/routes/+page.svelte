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
	import { tabStopWhenScrolls } from '$lib/scroller';
	import type { PublicSeats } from '$lib/api/types';
	import { OWN_HARDWARE, PLANS } from '$lib/plans';
	import PlanCells from '$lib/components/PlanCells.svelte';
	import OwnHardwarePlan from '$lib/components/OwnHardwarePlan.svelte';
	import CodeCopy from '$lib/components/CodeCopy.svelte';
	import CopyButton from '$lib/hk/CopyButton.svelte';
	import Terminal from '$lib/components/landing/Terminal.svelte';
	import PauseButton from '$lib/components/landing/PauseButton.svelte';
	import { loadHero } from '$lib/components/landing/heroes';
	import Wreck from '$lib/components/landing/Wreck.svelte';
	import OneCommand from '$lib/components/landing/OneCommand.svelte';
	import ComesBack from '$lib/components/landing/ComesBack.svelte';
	import Localhost from '$lib/components/landing/Localhost.svelte';
	import Browser from '$lib/components/landing/Browser.svelte';
	import Editor from '$lib/components/landing/Editor.svelte';
	import Ready from '$lib/components/landing/Ready.svelte';

	/** The hero picture: the Orbit, or the candidate ?hero= names, loaded after mount. */
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

	/** The hero's install line, scrolled to its end, shows its last characters unfaded. */
	function markEnd(e: Event) {
		const el = e.currentTarget as HTMLElement;
		el.dataset.end = String(el.scrollLeft + el.clientWidth >= el.scrollWidth - 1);
	}

	// Each row shows exactly what its Copy puts on the clipboard, and that
	// runs on a visitor's laptop: the row's title names the folder.
	const steps: { title: string; command: string; what: string }[] = [
		{ title: 'Install the CLI', command: INSTALL_COMMAND, what: 'the install command' },
		{ title: 'Sign in', command: 'repose login', what: 'the sign-in command' },
		{ title: 'Run in any checkout', command: 'repose run', what: 'repose run' }
	];

	// The founding price shows while GET /public/seats has places left, and
	// before it answers (the prerendered page); once it says none, Solo
	// shows its list price alone.
	const introEligible = $derived(seats?.founding_left !== 0);
</script>

<svelte:head>
	<title>repose: a machine of its own for your coding agents</title>
	<meta
		name="description"
		content="repose run copies your checkout, tools and logins to a machine where your coding agents keep working after you close your laptop. On our servers, or on a Mac mini you own for $10 a month."
	/>
</svelte:head>

<!-- A sign-in button, or the dashboard link once signed in. The page is
     prerendered signed out and learns who you are after it mounts, so
     both labels share one grid cell and the box is as wide as the longer
     one from the first paint: the swap moves nothing (CLS 0). The hidden
     label is out of the accessibility tree. -->
{#snippet authLabel(shown: string, other: string)}
	<span class="swap"><span>{shown}</span><span aria-hidden="true">{other}</span></span>
{/snippet}

{#snippet authAction(signedOut: string, signedIn: string, returnTo?: ReturnPath)}
	{#if authState.authenticated}
		<a href={resolve('/projects')} class="hk-btn hk-btn-lg hk-btn--touch"
			>{@render authLabel(signedIn, signedOut)}</a
		>
	{:else}
		<!-- A link, not a button: this page is prerendered, and a press
		     that lands before the bundle has hydrated used to do nothing at
		     all, with no feedback (DECISIONS I-864, I-878). The href starts
		     the flow at `GET /signin`; once hydrated, a plain left click
		     takes the browser flow instead. -->
		<!-- eslint-disable svelte/no-navigation-without-resolve -- signinHref() calls resolve('/signin') and adds the return path (lib/signin) -->
		<a
			href={signinHref(returnTo)}
			class="hk-btn hk-btn-lg hk-btn--touch"
			onclick={(e) => onSignInLinkClick(e, returnTo)}>{@render authLabel(signedOut, signedIn)}</a
		>
		<!-- eslint-enable svelte/no-navigation-without-resolve -->
	{/if}
{/snippet}

<!-- The page's one numbering: a band's number and name, and a cell's
     (2.1 to 2.4), in the mono label, the number at a fixed width. The
     space after the number is text, so a screen reader and a copy read
     "01 Setup"; it hangs at the span's end inside the fixed width. -->
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
				<code>repose run</code> copies your checkout, tools and logins to a machine where the agents
				keep working after you close your laptop. On our servers, or on a Mac mini you own.
			</p>
			<div class="hero-act">
				<div class="ctas">
					{@render authAction('Get started', 'Open the dashboard')}
					<CopyButton text={INSTALL_COMMAND} label="Copy install command" size="lg" tone="page" />
				</div>
				<pre
					class="install"
					use:tabStopWhenScrolls={'Install command'}
					onscroll={markEnd}>{INSTALL_COMMAND}</pre>
			</div>
		</div>
		<div class="fig fig--hero field-stage">
			<div class="stage field-stage hero-stage">
				<!-- The hero is the Quickstart's real `repose run` session
				     (Terminal.svelte, I-837): the page leads with the product
				     running, and the Orbit it replaces drew the always-on pitch
				     the headline no longer makes (I-1446). `?hero=<name>` still
				     draws any candidate in its place, in the browser only
				     (heroes.ts), the Orbit among them. -->
				<Hero />
			</div>
			<!-- The page's one control, in the hero's field (I-666): it holds
			     EVERY looping picture, not the hero's, and shows itself when
			     any loop runs (`motion.live > 0` in PauseButton). It stays
			     when the hero does not loop, because the wreck, the setup and
			     the grid still do. -->
			<PauseButton />
		</div>
	</section>

	<section class="band" id="parts" aria-labelledby="parts-h">
		<div class="head">
			{@render label('01', 'Where it runs')}
			<h2 class="hk-h2" id="parts-h">Where it runs</h2>
			<p class="links side">
				<a class="hk-more" href={resolve('/docs/[slug]', { slug: 'own-hardware' })}
					>Your own hardware</a
				>
				<a class="hk-more" href={resolve('/docs/[slug]', { slug: 'billing' })}>Pricing</a>
			</p>
		</div>
		<!-- Two homes, one band (I-1446). No start time for either while the
		     Mac's is too slow to print (ENG-70), and no memory figure until
		     what a size means on an owned host is settled (ENG-91): two cells
		     under one heading, one with a number and one without, is an
		     implication rather than an omission. -->
		<ul class="cols cols--halves">
			<li>
				{@render label('1.1', 'On our servers')}
				<h3 class="item-title">We run the machine</h3>
				<p class="copy">
					Your plan's memory is what runs at once, around the clock, however many hours the
					machines run. Snapshots go to cloud storage, so a machine comes back even when the
					server under it does not.
				</p>
				<p class="cmd">repose run</p>
			</li>
			<li>
				{@render label('1.2', 'On a Mac mini you own')}
				<h3 class="item-title">Or the Mac on your desk does</h3>
				<p class="copy">
					An M3 or newer on macOS 15 with 16 GB, and $10 a month. One command puts a single
					virtual machine on it and changes nothing else; your projects go there and nobody
					else's, and their snapshots stay on its disk.
				</p>
				<p class="cmd">repose host add</p>
			</li>
		</ul>
	</section>

	<section class="band" aria-labelledby="wreck-h">
		<div class="head">
			{@render label('02', 'Permissions')}
			<h2 class="hk-h2" id="wreck-h">Let it run with full permissions</h2>
			<p class="copy side">
				You don't approve its commands one by one. It can touch its machine and anything the logins
				on it can, and restoring a snapshot brings the machine back as it was.
			</p>
		</div>
		<div class="fig fig--wreck">
			<div class="stage">
				<Wreck />
			</div>
		</div>
	</section>

	<section class="band" aria-labelledby="setup-h">
		<div class="head">
			{@render label('03', 'Setup')}
			<h2 class="hk-h2" id="setup-h">Your laptop's setup, in one command</h2>
			<p class="copy side">
				You spend an hour setting up a cloud machine: install the CLIs, log in to GitHub, paste the
				<code>.env</code> files. <code>repose run</code> copies all of it from your laptop.
			</p>
		</div>
		<div class="fig">
			<div class="stage">
				<OneCommand animated />
			</div>
		</div>
	</section>

	<section class="band" id="features" aria-labelledby="features-h">
		<div class="head">
			{@render label('04', 'Machine')}
			<h2 class="hk-h2" id="features-h">Work on the machine as you would on your laptop</h2>
			<p class="copy side">
				Each project gets a machine of its own, and you reach it from the tools already on your
				laptop.
			</p>
		</div>
		<ul class="cells">
			<li class="fig">
				<ComesBack />
				<div class="cell-copy">
					{@render label('4.1', 'Snapshots')}
					<h3 class="item-title">Restore the whole disk</h3>
					<p class="copy">
						A snapshot holds the checkout, the installed tools and the logins made on the machine. A
						restore puts it back on the stopped machine, or on a new one with <code>--as</code>.
					</p>
					<p class="cmd">repose snapshots restore</p>
				</div>
			</li>
			<li class="fig">
				<Localhost />
				<div class="cell-copy">
					{@render label('4.2', 'Ports')}
					<h3 class="item-title">Use its ports on your localhost</h3>
					<p class="copy">
						Ports your servers listen on, from 1024 up, appear on your localhost while you're
						attached, so cookies and OAuth redirects behave as they do locally.
					</p>
					<p class="cmd">repose attach</p>
				</div>
			</li>
			<li class="fig">
				<Browser />
				<div class="cell-copy">
					{@render label('4.3', 'Browser')}
					<h3 class="item-title">Watch the agent's browser, and take over</h3>
					<p class="copy">
						The agents' browser runs on the machine. Open it on your laptop to watch, or to do a
						login or a captcha for them.
					</p>
					<p class="cmd">repose browser</p>
				</div>
			</li>
			<li class="fig">
				<Editor />
				<div class="cell-copy">
					{@render label('4.4', 'Editor')}
					<h3 class="item-title">Open it in your editor</h3>
					<p class="copy">
						Each machine is an SSH host. Neovim runs on it, and VS Code, Cursor and Zed open it from
						your laptop.
					</p>
					<p class="cmd">ssh your-project.repose</p>
				</div>
			</li>
		</ul>
	</section>

	<section class="band" aria-labelledby="agents-h">
		<div class="head">
			{@render label('05', 'Agents')}
			<h2 class="hk-h2" id="agents-h">
				The agents you use, and the tools they need, on first boot
			</h2>
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

	<section class="band field-stage band--dark" aria-labelledby="install-h">
		<div class="head">
			{@render label('06', 'Install')}
			<h2 class="hk-h2" id="install-h">Start in three commands</h2>
			<p class="links side">
				<a class="hk-more" href={resolve('/docs/[slug]', { slug: 'install' })}
					>Install and sign in</a
				>
				<a class="hk-more" href={resolve('/docs/[slug]', { slug: 'run-and-attach' })}
					>Run and attach</a
				>
			</p>
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
	</section>

	<section class="band" id="pricing" aria-labelledby="pricing-h">
		<div class="head">
			{@render label('07', 'Pricing')}
			<h2 class="hk-h2" id="pricing-h">Launch prices</h2>
			<div class="cta-row side">
				<!-- Signing up here comes back to Billing, where the plans are. -->
				{@render authAction('Start a free week', 'Open the dashboard', '/billing')}
				{#if !authState.authenticated && seats}
					<p class="seats" data-testid="seats-line">
						{#if seats.free > 0}
							{seats.free}
							{seats.free === 1 ? 'seat' : 'seats'} left
						{:else}
							Full for now. {seats.waiting} waiting; join the list and you're emailed when a seat frees.
						{/if}
					</p>
				{/if}
			</div>
		</div>
		<PlanCells plans={PLANS} {introEligible} variant="public" level={3} />
		<!-- The fourth thing for sale, and not a fourth card: it sells one
		     host the visitor owns and none of the three figures above
		     (I-1364). -->
		<OwnHardwarePlan plan={OWN_HARDWARE} variant="public" level={3} />
	</section>

	<section class="band faq-band" aria-labelledby="faq-h">
		<div class="head">
			{@render label('08', 'Questions')}
			<h2 class="hk-h2" id="faq-h">Before you try it</h2>
			<p class="links side">
				<a class="hk-more" href={resolve('/docs/[slug]', { slug: 'limits' })}>Limits</a>
				<a class="hk-more" href={resolve('/docs/[slug]', { slug: 'billing' })}>Billing</a>
			</p>
		</div>
		<div class="ruled faq">
			<details>
				<summary class="item-title"
					>Can I run it on a Mac I already have?{@render plus()}</summary
				>
				<p class="copy">
					An M3 or newer on macOS 15 with 16 GB of memory, for $10 a month. <code
						>repose host add</code
					> puts one virtual machine on it, keeps the Mac awake while your machines run, and
					<code>repose host rm</code> takes it off again with its disk.
				</p>
			</details>
			<details>
				<summary class="item-title"
					>What happens when my Mac sleeps or reboots?{@render plus()}</summary
				>
				<p class="copy">
					Your machines are out of reach until it answers again, and what they wrote to disk is
					kept. A restart starts them fresh: their disks are as they were, and what they held in
					memory is gone.
				</p>
			</details>
			<details>
				<summary class="item-title">Can I close my laptop while it works?{@render plus()}</summary>
				<p class="copy">
					The agent keeps working on its machine, and you hear by email or on your phone when it's
					done or needs you. Attach again, or open the machine in your editor, to see where it got
					to.
				</p>
			</details>
			<details>
				<summary class="item-title">What can the agent reach?{@render plus()}</summary>
				<p class="copy">
					It runs as <code>dev</code> with sudo on its own machine, with the logins you copied and the
					secrets you set. Your other projects are separate machines it can't reach, and it connects to
					your laptop only through a port or a Chrome you lend it.
				</p>
			</details>
			<details>
				<summary class="item-title">What happens when it breaks something?{@render plus()}</summary>
				<p class="copy">
					You restore a snapshot and the whole disk comes back: the checkout, the tools and the
					logins made on the machine, Claude Code's aside. repose takes one each night and at every
					stop, or you run <code>repose snapshots create</code> first; they're kept seven days, and the
					newest stays.
				</p>
			</details>
			<details>
				<summary class="item-title">What does an agent cost while it waits?{@render plus()}</summary
				>
				<p class="copy">
					A machine nobody is using freezes after about 15 minutes of quiet, and a webhook, a
					scheduled job or your next command wakes it in a second or two. You pay for the memory
					your plan runs at once, so a waiting agent costs nothing on top.
				</p>
			</details>
		</div>
	</section>
</main>
