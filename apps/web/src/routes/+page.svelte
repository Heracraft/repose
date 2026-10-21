<script lang="ts">
	import './landing.css';
	import '$lib/components/landing/pictures.css';
	import { onMount } from 'svelte';
	import { resolve } from '$app/paths';
	import { toast } from 'svelte-sonner';
	import { authState, signIn, type ReturnPath } from '$lib/auth.svelte';
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
	import { RESTART } from '$lib/components/landing/session';

	let signingIn = $state(false);
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
			toast.error('Sign-in was cancelled or failed; try again.');
			history.replaceState(null, '', location.pathname);
		}
	});

	async function onSignIn(returnTo?: ReturnPath) {
		if (signingIn) return;
		signingIn = true;
		try {
			await signIn(returnTo);
		} catch {
			signingIn = false;
			toast.error('Sign-in was cancelled or failed; try again.');
		}
	}

	// On a phone the install line breaks at its spaces, never inside the URL
	// and never between the pipe and its target.
	const install = INSTALL_COMMAND.split(/(https:\/\/\S+)/);

	// Each row shows exactly what its Copy puts on the clipboard, and that
	// runs on a visitor's laptop: the row's title names the folder.
	const steps: { title: string; command: string; what: string }[] = [
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
		<button
			type="button"
			class="hk-btn hk-btn-lg hk-btn--touch"
			aria-disabled={signingIn ? 'true' : undefined}
			onclick={() => onSignIn(returnTo)}>{@render authLabel(signedOut, signedIn)}</button
		>
	{/if}
{/snippet}

{#snippet plus()}
	<svg class="plus" viewBox="0 0 16 16" fill="none" stroke="currentColor" aria-hidden="true">
		<path d="M8 3v10M3 8h10" />
	</svg>
{/snippet}

<main id="main" tabindex="-1" class="frame landing">
	<section class="hero">
		<div class="hero-copy">
			<p class="hk-eyebrow">For solo founders and their agents</p>
			<h1 class="hk-h1">Your dev environment, replicated in the cloud</h1>
			<p class="hk-lead">
				Your code, tools and logins sit on a machine of its own, where agents run with full
				permissions and nobody approves commands one by one. Snapshot it as you go, and you can put
				a wrecked machine back in minutes.
			</p>
			<div class="ctas">
				{@render authAction('Get started', 'Open the dashboard')}
				<CopyButton text={INSTALL_COMMAND} label="Copy install command" size="lg" tone="page" />
			</div>
			<p class="install">
				{install[0]}<span class="nowrap">{install[1]}</span>
				<span class="nowrap">{install[2].trimStart()}</span>
			</p>
		</div>
		<div class="stage hero-stage">
			<Hero />
		</div>
	</section>

	<section class="sec">
		<div class="sec-head">
			<h2 class="hk-h2">Your laptop's setup, in one command</h2>
			<p class="side-text">
				You spend an hour setting up a cloud box: install the CLIs, log in to GitHub, paste the
				<code>.env</code> files. <code>repose run</code> copies all of it from your laptop.
			</p>
		</div>
		<div class="stage">
			<OneCommand animated />
		</div>
	</section>

	<section class="sec" id="features">
		<div class="sec-head">
			<h2 class="hk-h2">On every machine</h2>
		</div>
		<ul class="cells">
			<li>
				<ComesBack />
				<h3 class="hk-h3">Let it break the whole machine</h3>
				<p>
					repose snapshots the machine nightly, and you can take one yourself any time. Restoring it
					brings back the databases, logins and uncommitted work in minutes.
				</p>
				<p class="cell-cmd"><span aria-hidden="true">$</span> repose snapshots create</p>
			</li>
			<li>
				<Localhost />
				<h3 class="hk-h3">The machine's ports on your localhost</h3>
				<p>
					Ports from 1024 up appear on your localhost while you're attached. When you're not,
					forward one by hand. Cookies and OAuth redirects behave as they do locally.
				</p>
				<p class="cell-cmd"><span aria-hidden="true">$</span> repose open 3000</p>
			</li>
			<li>
				<Browser />
				<h3 class="hk-h3">Watch the agent's browser, and take over</h3>
				<p>The agent's Chromium, on your laptop. Click in it to take over.</p>
				<p class="cell-cmd"><span aria-hidden="true">$</span> repose browser</p>
			</li>
			<li>
				<Editor />
				<h3 class="hk-h3">Open it in your editor</h3>
				<p>Every machine is an SSH host. Neovim on it, VS Code, Cursor or Zed over SSH.</p>
				<p class="cell-cmd"><span aria-hidden="true">$</span> repose code your-project</p>
			</li>
		</ul>
	</section>

	<section class="sec">
		<div class="sec-head">
			<h2 class="hk-h2">The agents you use, and the tools they need, on first boot</h2>
			<p class="more">
				<a class="hk-more" href={resolve('/docs/[slug]', { slug: 'agents' })}>All agents</a>
				<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -- resolve() with a fragment appended -->
				<a class="hk-more" href={resolve('/docs/[slug]', { slug: 'machine' }) + '#whats-installed'}
					>Everything installed</a
				>
			</p>
		</div>
		<div class="stage">
			<Ready />
		</div>
	</section>

	<section class="sec split">
		<div class="side">
			<h2 class="hk-h2">Sign in, then run</h2>
		</div>
		<ol class="steps">
			{#each steps as step (step.title)}
				<li>
					<p class="step-title">{step.title}</p>
					<CodeCopy command={step.command} what={step.what} prompt />
				</li>
			{/each}
		</ol>
	</section>

	<section class="sec" id="pricing">
		<div class="pricing-head">
			<h2 class="hk-h2">Pricing</h2>
			<p>Launch prices. Seven days free.</p>
		</div>
		<PlanCells plans={PLANS} {introEligible} variant="public" level={3} />
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
	</section>

	<section class="sec split">
		<div class="side">
			<h2 class="hk-h2">Questions</h2>
		</div>
		<div class="faq">
			<details>
				<summary>Do my files survive a stop?{@render plus()}</summary>
				<p>
					Yes. The next <code>repose run</code> or <code>repose attach</code> starts the machine
					again in {RESTART} with your files where you left them.
				</p>
			</details>
			<details>
				<summary>Is my Claude Code login copied from my laptop?{@render plus()}</summary>
				<p>
					No. You log into Claude Code once, on the machine itself. The Codex, opencode and GitHub
					CLI logins your laptop has are copied.
				</p>
			</details>
			<details>
				<summary>Does the CLI run on Windows?{@render plus()}</summary>
				<p>Yes, through WSL. Linux and macOS run it directly.</p>
			</details>
		</div>
	</section>
</main>
