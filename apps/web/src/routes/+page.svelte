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
	import type { PublicSeats } from '$lib/api/types';
	import { PLANS } from '$lib/plans';
	import PlanCells from '$lib/components/PlanCells.svelte';
	import CodeCopy from '$lib/components/CodeCopy.svelte';
	import CopyButton from '$lib/hk/CopyButton.svelte';
	import Hero from '$lib/components/landing/Hero.svelte';
	import OneCommand from '$lib/components/landing/OneCommand.svelte';
	import ComesBack from '$lib/components/landing/ComesBack.svelte';
	import Localhost from '$lib/components/landing/Localhost.svelte';
	import Browser from '$lib/components/landing/Browser.svelte';
	import Editor from '$lib/components/landing/Editor.svelte';
	import Ready from '$lib/components/landing/Ready.svelte';

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
	<title>repose: your dev environment, replicated in the cloud</title>
	<meta
		name="description"
		content="One command replicates your laptop's dev environment on a cloud machine: your code, tools and logins. For solo founders whose agents run with full permissions there, while the laptop stays out of reach."
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

{#snippet plus()}
	<svg class="plus" viewBox="0 0 16 16" fill="none" stroke="currentColor" aria-hidden="true">
		<path d="M8 3v10M3 8h10" />
	</svg>
{/snippet}

<main id="main" tabindex="-1" class="frame landing">
	<section class="band band--pic hero">
		<p class="label">For solo founders and their agents</p>
		<h1 class="hk-h1">Your dev environment, replicated in the cloud</h1>
		<div class="cols hero-row">
			<p class="hk-lead">
				Your code, tools and logins sit on a machine of its own, where agents run with full
				permissions and nobody approves commands one by one. Snapshot it as you go, and you can put
				a wrecked machine back in minutes.
			</p>
			<div class="hero-act">
				<div class="ctas">
					{@render authAction('Get started', 'Open the dashboard')}
					<CopyButton text={INSTALL_COMMAND} label="Copy install command" size="lg" tone="page" />
				</div>
				<p class="install">{INSTALL_COMMAND}</p>
			</div>
		</div>
		<div class="stage hero-stage">
			<Hero />
		</div>
	</section>

	<section class="band band--pic">
		<div class="sec-head">
			<h2 class="hk-h2">Your laptop's setup, in one command</h2>
			<p class="copy">
				You spend an hour setting up a cloud box: install the CLIs, log in to GitHub, paste the
				<code>.env</code> files. <code>repose run</code> copies all of it from your laptop.
			</p>
		</div>
		<div class="stage">
			<OneCommand animated />
		</div>
	</section>

	<section class="band" id="features">
		<div class="sec-head">
			<h2 class="hk-h2">A cloud machine that feels like your own box</h2>
		</div>
		<ul class="cells">
			<li>
				<ComesBack />
				<h3 class="item-title">Let it break the whole machine</h3>
				<p class="copy">
					repose snapshots the machine nightly, and you can take one yourself any time.
				</p>
				<p class="cmd"><span aria-hidden="true">$</span> repose snapshots create</p>
			</li>
			<li>
				<Localhost />
				<h3 class="item-title">Use its ports on your localhost</h3>
				<p class="copy">
					Ports from 1024 up appear on your localhost while you're attached, so cookies and OAuth
					redirects behave as they do locally. Detached, you forward one by hand.
				</p>
				<p class="cmd"><span aria-hidden="true">$</span> repose open 5173</p>
			</li>
			<li>
				<Browser />
				<h3 class="item-title">Watch the agent's browser, and take over</h3>
				<p class="copy">Step in for a captcha or a passkey in the browser the agent is driving.</p>
				<p class="cmd"><span aria-hidden="true">$</span> repose browser</p>
			</li>
			<li>
				<Editor />
				<h3 class="item-title">Open it in your editor</h3>
				<p class="copy">
					Each machine is an SSH host, so VS Code, Cursor and Zed open it over SSH.
				</p>
				<p class="cmd"><span aria-hidden="true">$</span> repose code your-project</p>
			</li>
		</ul>
	</section>

	<section class="band band--pic">
		<div class="sec-head">
			<h2 class="hk-h2">The agents you use, and the tools they need, on first boot</h2>
			<p class="links">
				<a href={resolve('/docs/[slug]', { slug: 'agents' })}>All agents</a>
				<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -- resolve() with a fragment appended -->
				<a href={resolve('/docs/[slug]', { slug: 'machine' }) + '#whats-installed'}
					>Everything installed</a
				>
			</p>
		</div>
		<div class="stage">
			<Ready />
		</div>
	</section>

	<section class="band band--ruled">
		<div class="sec-head">
			<h2 class="hk-h2">Start in three commands</h2>
		</div>
		<ol class="ruled steps">
			{#each steps as step, i (step.title)}
				<li class="cols">
					<h3 class="item-title step-title">
						<span class="label" aria-hidden="true">{String(i + 1).padStart(2, '0')}</span
						>{step.title}
					</h3>
					<CodeCopy command={step.command} what={step.what} labelFrom={1200} />
				</li>
			{/each}
		</ol>
	</section>

	<section class="band band--ruled" id="pricing">
		<div class="sec-head">
			<h2 class="hk-h2">Pricing</h2>
			<div class="price-act">
				<p class="copy">Launch prices.</p>
				<div class="cta-row">
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
		</div>
		<PlanCells plans={PLANS} {introEligible} variant="public" level={3} />
	</section>

	<section class="band band--ruled">
		<div class="cols faq-cols">
			<h2 class="hk-h2">Questions</h2>
			<div class="ruled faq">
				<details>
					<summary class="item-title"
						>Why not skip permissions on my laptop?{@render plus()}</summary
					>
					<p class="copy">
						There the agent can read whatever you can: your SSH keys, your other repositories, the
						sessions in your browser. On repose it gets a machine holding one project and the logins
						you chose to copy. Nothing on that machine can open a connection to your laptop, and
						your other projects are separate machines it can't reach.
					</p>
				</details>
				<details>
					<summary class="item-title"
						>Can I close my laptop while the agent works?{@render plus()}</summary
					>
					<p class="copy">
						Yes. Detach and close it; the agent keeps working on the machine. When it finishes, or
						wants a decision from you, you get an email, a push on your phone through ntfy, or a
						Google Chat card, and you can answer its question from there.
					</p>
				</details>
				<details>
					<summary class="item-title">What if the agent wrecks the machine?{@render plus()}</summary
					>
					<p class="copy">
						You restore a snapshot. repose takes one every night and at every stop, and
						<code>repose snapshots create</code> takes one before something risky. A restore brings back
						the disk whole: code, databases, installed tools and logins made on the machine.
					</p>
				</details>
				<details>
					<summary class="item-title"
						>Can three agents try three approaches at once?{@render plus()}</summary
					>
					<p class="copy">
						<code>repose fork your-project -n 3</code> copies the machine's disk, uncommitted
						changes and installed dependencies included, into three new machines, and
						<code>--prompt</code> starts an agent in each. Fetch the copy that won with git, and remove
						the rest.
					</p>
				</details>
				<details>
					<summary class="item-title"
						>Why not rent a server and set it up myself?{@render plus()}</summary
					>
					<p class="copy">
						You can, and you redo its setup for each project: the CLIs, the GitHub login, the
						<code>.env</code> files. The nightly snapshots and the port forwards are yours to build as
						well.
					</p>
				</details>
			</div>
		</div>
	</section>
</main>
