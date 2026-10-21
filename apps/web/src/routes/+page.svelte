<script lang="ts">
	import './landing.css';
	import { onMount } from 'svelte';
	import { resolve } from '$app/paths';
	import { toast } from 'svelte-sonner';
	import { authState, signIn } from '$lib/auth.svelte';
	import { INSTALL_COMMAND } from '$lib/links';
	import { publicSeats } from '$lib/api/client';
	import type { PublicSeats } from '$lib/api/types';
	import Hero from '$lib/components/landing/Hero.svelte';
	import Gauge from '$lib/components/landing/Gauge.svelte';
	import Units from '$lib/components/landing/Units.svelte';
	import Shape, { type Kind, type Tone } from '$lib/components/landing/Shape.svelte';
	import SectionHead from '$lib/components/landing/SectionHead.svelte';
	import { landOnView } from '$lib/components/landing/inview';
	import OneCommand from '$lib/components/landing/OneCommand.svelte';
	import ComesBack from '$lib/components/landing/ComesBack.svelte';
	import Browser from '$lib/components/landing/Browser.svelte';
	import Localhost from '$lib/components/landing/Localhost.svelte';
	import Ready from '$lib/components/landing/Ready.svelte';
	import Editor from '$lib/components/landing/Editor.svelte';

	let signingIn = $state(false);
	/** The command whose Copy was pressed last, for 1.5s; null otherwise. */
	let copied = $state<string | null>(null);
	/** The page's one live region for the copy buttons (I-393): always in
	    the page, only its text changes, so a screen reader hears it. */
	let copyStatus = $state('');
	let copiedTimer: ReturnType<typeof setTimeout> | undefined;
	/** GET /public/seats (I-290): the launch's gauge; undefined until it answers, and if it never does. */
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

	async function onSignIn() {
		signingIn = true;
		try {
			await signIn();
		} catch {
			signingIn = false;
			toast.error('Sign-in was cancelled or failed; try again.');
		}
	}

	async function copyCommand(command: string, what: string) {
		try {
			await navigator.clipboard.writeText(command);
			copied = command;
			copyStatus = `Copied ${what}`;
			clearTimeout(copiedTimer);
			copiedTimer = setTimeout(() => {
				copied = null;
				copyStatus = '';
			}, 1500);
		} catch {
			toast.error('Could not copy. Select the command instead.');
		}
	}

	/** A command cut after each single "/" (not inside "//"), so a <wbr>
	    there lets a wrapped command break between path parts. */
	const breakable = (command: string) => command.split(/(?<=[^/]\/)(?!\/)/);

	// `what` finishes each Copy button's name for a screen reader ("Copy
	// the sign-in command"), so the three buttons are not three "Copy"s.
	// The checkout in the last one is named after its laptop folder (I-368),
	// the hero picture's your-project/. That path is an example, so its Copy
	// button copies `repose run` alone (`copy`): a pasted
	// `cd ~/code/your-project` fails on a visitor's laptop.
	const steps: { title: string; command: string; copy?: string; what: string }[] = [
		{ title: 'Install the CLI', command: INSTALL_COMMAND, what: 'the install command' },
		{ title: 'Sign in', command: 'repose login', what: 'the sign-in command' },
		{
			title: 'Run in any checkout',
			command: 'cd ~/code/your-project && repose run',
			copy: 'repose run',
			what: 'repose run, to run in your checkout'
		}
	];
	// docs/PRICING.md's three plans, with internal/billing/plans.go's
	// figures. The Units count is the plan's memory, one square per GB, so
	// the plans compare at a glance. The cards say "running at once" in words and
	// count no agents or machines: a count reads as a ceiling on what the
	// product does, and the memory is shared by whatever is running
	// (I-402). Solo shows its founding price with the list price struck
	// through beside it, and the terms in one short line (I-772: a
	// two-line sentence about the discount was the owner's "just do
	// strike through"); once GET /public/seats says no founding places
	// are left, Solo shows its list price alone, unstruck.
	const allPlans: {
		name: string;
		price: string;
		was?: string;
		then?: string;
		memory: number;
		disk: string;
		egress: string;
	}[] = [
		{
			name: 'Solo',
			price: '$39',
			was: '$49',
			then: 'First 12 months, first 30 subscribers',
			memory: 8,
			disk: '100 GB',
			egress: '250 GB'
		},
		{
			name: 'Plus',
			price: '$89',
			memory: 16,
			disk: '250 GB',
			egress: '500 GB'
		},
		{
			name: 'Pro',
			price: '$169',
			memory: 32,
			disk: '500 GB',
			egress: '1 TB'
		}
	];
	const plans = $derived(
		seats?.founding_left === 0
			? allPlans.map((p) =>
					p.name === 'Solo' ? { ...p, price: '$49', was: undefined, then: undefined } : p
				)
			: allPlans
	);
	// The sign-off row: every shape the page used, in the order it used
	// them, so the row reads as the page's own symbols and none appears
	// from nowhere. Mostly grey, a spot of blue, as the pictures are.
	const frieze: [Kind, Tone][] = [
		['pinwheel', 'accent'],
		['sphere', 'neutral'],
		['pill', 'neutral'],
		['halves', 'neutral'],
		['ring', 'neutral'],
		['arch', 'neutral'],
		['asterisk', 'neutral']
	];
</script>

<svelte:head>
	<title>repose: your dev environment, replicated in the cloud</title>
	<meta
		name="description"
		content="One command replicates your laptop's dev environment on a cloud machine: your code, tools and logins. For solo founders whose agents run with full permissions there, while the laptop stays out of reach."
	/>
</svelte:head>

{#snippet cellMark(kind: Kind)}
	<span class="cell-mark" aria-hidden="true"><Shape {kind} /></span>
{/snippet}

<!-- A sign-in button, or the dashboard link once signed in. The page is
     prerendered signed out and learns who you are after it mounts, so
     both labels share one grid cell and the box is as wide as the longer
     one from the first paint: the swap moves nothing (CLS 0). The hidden
     label is out of the accessibility tree. -->
{#snippet authLabel(shown: string, other: string)}
	<span class="swap"><span>{shown}</span><span aria-hidden="true">{other}</span></span>
{/snippet}

{#snippet authAction(klass: string, signedOut: string, signedIn: string)}
	{#if authState.authenticated}
		<a href={resolve('/projects')} class={klass}>{@render authLabel(signedIn, signedOut)}</a>
	{:else}
		<button type="button" class={klass} disabled={signingIn} onclick={onSignIn}
			>{@render authLabel(signedOut, signedIn)}</button
		>
	{/if}
{/snippet}

<!-- A command row with its Copy button: the hero's install command and
     each step's. Below md the command wraps instead of being cut off, so
     the whole of it can be read at 320px (WCAG 1.4.10), at a space or
     after a path's slash (breakable), not inside a filename. The button's name
     is its visible word and what it copies; the result is said in the
     page's one status region below. -->
{#snippet cmdRow(command: string, what: string, prompt: boolean, copy: string = command)}
	<div class="cmd">
		<span class="text"
			>{prompt ? '$ ' : ''}{#each breakable(command) as part, i (i)}{part}<wbr />{/each}</span
		>
		<button type="button" class="copy" onclick={() => copyCommand(copy, what)}>
			{@render copyIcon()}
			{copied === copy ? 'Copied' : 'Copy'}<span class="sr-only">{` ${what}`}</span>
		</button>
	</div>
{/snippet}

{#snippet copyIcon()}
	<svg
		viewBox="0 0 16 16"
		class="h-3.5 w-3.5"
		fill="none"
		stroke="currentColor"
		stroke-width="1.4"
		aria-hidden="true"
		><rect x="5.5" y="5.5" width="8" height="8" rx="1" /><path d="M10.5 3.5v-1h-8v8h1" /></svg
	>
{/snippet}

<div class="rails">
	<main id="main">
		<section class="sec">
			<div class="hero-grid land-inset">
				<p class="eyebrow">For solo founders and their agents</p>
				<h1 class="hero-h">
					<span class="line">Your dev environment,</span>
					<span class="line">replicated in the cloud</span>
				</h1>
				<p class="lead">
					Your code, tools and logins sit on a machine of its own, where agents run with full
					permissions and nobody approves commands one by one. Snapshot it as you go, and you can
					put a wrecked machine back in minutes.
				</p>
				<div class="hero-ctas">
					{@render authAction('btn btn--lg', 'Get started', 'Open the dashboard')}
					{@render cmdRow(INSTALL_COMMAND, 'the install command', false)}
				</div>
			</div>
			<div class="landing-stage hero-stage">
				<Hero />
			</div>
		</section>

		<section class="sec">
			<SectionHead shape="pill" title="Your laptop's setup, in one command">
				You spend an hour setting up a cloud box: install the CLIs, log in to GitHub, paste the
				<code>.env</code> files. <code>repose run</code> copies all of it from your laptop.
			</SectionHead>
			<div class="landing-stage">
				<OneCommand animated />
			</div>
		</section>

		<section class="sec">
			<SectionHead id="features" title="On every machine" />
			<ul class="land-cells">
				<li class="land-cell">
					<ComesBack />
					<h3>{@render cellMark('pinwheel')}Let it break the whole machine</h3>
					<p>
						repose snapshots the machine nightly, and you can take one yourself any time. Restoring
						it brings back the databases, logins and uncommitted work in minutes.
					</p>
				</li>
				<li class="land-cell">
					<Localhost />
					<h3>{@render cellMark('halves')}The machine's ports on your localhost</h3>
					<p>
						Ports from 1024 up, while <code>repose run</code> is open. Cookies and OAuth redirects behave
						as they do locally.
					</p>
				</li>
				<li class="land-cell">
					<Browser />
					<h3>{@render cellMark('ring')}Watch the agent's browser, and take over</h3>
					<p>
						<code>repose browser</code> shows the agent's Chromium on your laptop; click in it to take
						over.
					</p>
				</li>
				<li class="land-cell">
					<Editor />
					<h3>{@render cellMark('arch')}Open it in your editor</h3>
					<p>Every machine is an SSH host. Neovim on it, VS Code, Cursor or Zed over SSH.</p>
				</li>
			</ul>
		</section>

		<section class="sec">
			<SectionHead
				shape="asterisk"
				title="The agents you use, and the tools they need, on first boot"
			/>
			<div class="landing-stage">
				<Ready />
			</div>
		</section>

		<section class="sec">
			<SectionHead title="Start in three commands" />
			<ol class="steps" use:landOnView>
				{#each steps as step, i (step.title)}
					<li class="step">
						<div>
							<h3>
								<span class="step-mark land" style="--d: {i * 110}ms" aria-hidden="true"
									><Gauge
										fraction={(i + 1) / steps.length}
										tone={i === steps.length - 1 ? 'accent' : 'neutral'}
									/></span
								>{step.title}
							</h3>
						</div>
						{@render cmdRow(step.command, step.what, true, step.copy)}
					</li>
				{/each}
			</ol>
		</section>

		<section class="sec">
			<SectionHead id="pricing" title="Pricing">Launch prices. Seven days free.</SectionHead>
			<ul class="tiers" use:landOnView>
				{#each plans as t, i (t.name)}
					<li class="tier">
						<div class="tier-top">
							<h3 class="tier-name">{t.name}</h3>
							<span class="tier-units land" style="--d: {i * 110}ms"
								><Units count={t.memory} /></span
							>
						</div>
						<p class="tier-run">{t.memory} GB running at once</p>
						<p class="tier-spec">
							<span>{t.disk} disk ·</span> <span>{t.egress} egress</span>
						</p>
						<p class="tier-price">
							{#if t.was}<s class="was">{t.was}</s>{/if}
							<span class="n">{t.price}</span>
							<span class="per">a month</span>
						</p>
						{#if t.then}
							<p class="tier-then">{t.then}</p>
						{/if}
					</li>
				{/each}
			</ul>
			<div class="cta-row">
				{@render authAction('btn btn--lg', 'Start a free week', 'Open the dashboard')}
				{#if !authState.authenticated}
					{#if seats}
						<p class="seats" data-testid="seats-line">
							{#if seats.free > 0}
								{seats.free} {seats.free === 1 ? 'seat' : 'seats'} left
							{:else}
								Full for now. {seats.waiting} waiting; join the list and you're emailed when a seat frees.
							{/if}
						</p>
					{/if}
				{/if}
			</div>
		</section>
	</main>

	<!-- The sign-off: the page's shapes, one to a cell between the rails.
	     The site footer follows, under the rails. -->
	<div class="sec" aria-hidden="true">
		<ul class="frieze" use:landOnView>
			{#each frieze as [k, tone], i (i)}
				<li>
					<span class="land block h-full w-full" style="--d: {i * 60}ms"
						><Shape kind={k} {tone} meridians={k === 'sphere'} /></span
					>
				</li>
			{/each}
		</ul>
	</div>
</div>

<!-- The copy buttons' result, for a screen reader; drawn nowhere. -->
<p class="sr-only" role="status">{copyStatus}</p>
