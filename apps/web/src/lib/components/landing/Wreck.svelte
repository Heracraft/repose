<!--
  The safety figure (section 02): the agent wrecks the sandbox, a snapshot
  puts it back. One window and one unboxed timeline (landing council,
  round 2: no status rows to decipher, no boxed list, no chip on a wire).

  Left, your sandbox: the repo with three files, and Claude Code in its
  bypass permissions mode. Right, its snapshots on one rail, oldest first:
  the nightly at 03:00, yours at 21:25, and 21:31, the snapshot the stop
  took of the wreck, as a red dot. An accent arc, "restore 21:25", runs
  from 21:31 back to 21:25.

  The still frame, which is also what reduced motion, no script and the
  first paint show, is the story's end: the sandbox whole, 21:31 red, 21:25
  lit and the arc drawn. With motion the loop runs from that frame: the
  arc and 21:31 go and the sandbox works (3.5s); the agent turns red, the
  repo and its files are struck and the stop snapshot 21:31 lands (3s);
  then the arc comes, 21:25 lights and the sandbox is back, which is the
  still frame again (held 5.5s). Only CSS transitions move, on a phase the
  script steps through, so pausing holds the phase.

  Provenance. The snapshot times are illustrative, on one clock: nightly
  snapshots and one on every stop are /docs/lifecycle. Claude Code runs in
  bypassPermissions mode on every machine (/docs/agents).
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { agentMarks } from '$lib/components/illustrations/marks';
	import { watchReducedMotion } from './inview';
	import { Picture } from './motion.svelte';
	import WinHead from './WinHead.svelte';

	const agent = agentMarks[0];
	const files = ['.env.example', 'poller.ts', 'timeout.test.ts'];

	type Phase = 'work' | 'wreck' | 'restored';
	// How long each phase holds before the next one.
	const HOLD: Record<Phase, number> = { work: 3500, wreck: 3000, restored: 5500 };
	const NEXT: Record<Phase, Phase> = { work: 'wreck', wreck: 'restored', restored: 'work' };

	let phase: Phase = $state('restored');
	let pic: HTMLDivElement;
	const picture = new Picture();

	const label = 'An agent wrecks your sandbox; a snapshot restores it';
	const story =
		'Your sandbox holds your repo, with Claude Code running in bypass permissions mode. Its ' +
		'snapshots are a nightly one from 03:00 and yours from 21:25. The agent wrecks the machine ' +
		'and the repo is gone. Stopping it takes one more snapshot, 21:31, of the wreck. Restoring ' +
		'21:25 brings the whole machine back as it was.';

	onMount(() => {
		let timer: ReturnType<typeof setTimeout> | undefined;
		// When the current phase is due to end, and what was left of it at a hold.
		let due = 0;
		let left = 0;
		let running = false;

		function arm(ms: number) {
			clearTimeout(timer);
			due = performance.now() + ms;
			timer = setTimeout(step, ms);
		}
		function step() {
			phase = NEXT[phase];
			// The restored frame is the still frame: the loop's seam.
			if (phase === 'restored') picture.looped();
			arm(HOLD[phase]);
		}
		function hold() {
			if (timer === undefined) return;
			clearTimeout(timer);
			timer = undefined;
			left = Math.max(0, due - performance.now());
		}
		function start() {
			running = true;
			left = 0;
			picture.start(
				pic,
				(m, fromStill) => {
					if (m !== 'play') return hold();
					// From the still frame the loop leaves after a short beat.
					if (fromStill || !left) arm(fromStill ? 1200 : HOLD[phase]);
					else arm(left);
				},
				0.35
			);
		}
		function stop() {
			running = false;
			hold();
			picture.stop();
			phase = 'restored';
		}

		const unwatch = watchReducedMotion((reduce) => {
			if (reduce) {
				if (running) stop();
			} else if (!running) start();
		});

		return () => {
			clearTimeout(timer);
			picture.stop();
			unwatch();
		};
	});
</script>

{#snippet fileIcon()}
	<svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true" class="fi">
		<path
			d="M4 1.5h5.2L12.5 4.8v9.7H4z M9 1.5v3.5h3.5"
			fill="none"
			stroke="currentColor"
			stroke-width="1.1"
			stroke-linejoin="round"
		/>
	</svg>
{/snippet}

{#snippet repoIcon()}
	<svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true" class="fi">
		<g fill="none" stroke="currentColor" stroke-width="1.1" stroke-linejoin="round">
			<path d="M1.5 3.5h4.3l1.4 1.6h7.3v8.4h-13z" />
			<circle cx="6" cy="10.8" r="1.1" />
			<circle cx="10.5" cy="8.4" r="1.1" />
			<path d="M6 9.7V7.6M10.5 9.5c0 1.2-4.5 0.4-4.5 1.3" />
		</g>
	</svg>
{/snippet}

<div class="hero-frame" use:picture.frame>
	<div
		class="hero-pic wk"
		data-phase={phase}
		role="img"
		aria-label={label}
		aria-describedby="wreck-story"
		bind:this={pic}
	>
		<p id="wreck-story" hidden>{story}</p>

		<div class="win box side laptop" aria-hidden="true">
			<WinHead who="machine" dot />
			<ul class="rows">
				<li class="fr repo">{@render repoIcon()}<span class="fname">your-project/</span></li>
				{#each files as f (f)}
					<li class="fr kid">{@render fileIcon()}<span class="fname">{f}</span></li>
				{/each}
			</ul>
			<div class="agentrow">
				<svg viewBox="0 0 24 24" width="20" height="20" class="agent">
					{#each agent.paths as d (d)}<path {d} fill-rule="evenodd" />{/each}
				</svg>
				<span class="mode">⏵⏵ bypass permissions on</span>
			</div>
		</div>

		<div class="tl side right" aria-hidden="true">
			<p class="tl-h">
				<svg viewBox="0 0 16 16" width="14" height="14">
					<path d="M8 8V0A8 8 0 0 0 0 8Z" fill="currentColor" />
					<path d="M8 8V16A8 8 0 0 0 16 8Z" fill="currentColor" opacity="0.45" />
				</svg>
				Snapshots
			</p>
			<div class="tl-arc">
				<span class="restore">restore 21:25</span>
				<svg viewBox="0 0 100 24" preserveAspectRatio="none">
					<path d="M83.33 23 C 83.33 1, 50 1, 50 20" fill="none" />
				</svg>
				<i class="tip"></i>
			</div>
			<ol class="tl-rail">
				<li class="snap night"><i></i><span class="t">03:00</span></li>
				<li class="snap mine"><i></i><span class="t">21:25</span></li>
				<li class="snap stop"><i></i><span class="t">21:31</span></li>
			</ol>
		</div>
	</div>
</div>

<style>
	/* ---------- Layout: the sandbox, then its snapshots, top-aligned ---------- */
	.wk {
		--ease: cubic-bezier(0.33, 1, 0.68, 1);
		display: grid;
		grid-template-columns: minmax(0, 26rem) minmax(0, 21rem);
		column-gap: 6rem;
		justify-content: center;
		align-items: start;
		color: var(--pic-ink);
		font-family: var(--font-mono);
		font-size: 13px;
		line-height: 20px;
	}
	.win {
		min-width: 0;
		border: 1px solid var(--rule);
		border-radius: 6px;
		background: var(--surface);
		overflow: hidden;
	}

	/* ---------- The sandbox ---------- */
	.rows {
		margin: 0;
		padding: 10px 0;
		list-style: none;
	}
	.fr {
		display: flex;
		align-items: center;
		gap: 8px;
		height: 30px;
		padding: 0 14px;
		white-space: nowrap;
	}
	.fr :global(.fi) {
		flex: none;
		color: var(--pic-dim);
	}
	.kid {
		padding-left: 36px;
	}
	.fname {
		position: relative;
		transition: color 0.3s var(--ease);
	}
	/* The strike across what the agent took, drawn from the left. */
	.fname::after {
		content: '';
		position: absolute;
		left: -2px;
		right: -2px;
		top: 50%;
		height: 1.5px;
		background: var(--pic-stop);
		transform: scaleX(0);
		transform-origin: left;
		transition: transform 0.35s var(--ease);
	}
	.agentrow {
		display: flex;
		align-items: center;
		gap: 10px;
		height: 44px;
		padding: 0 14px;
		border-top: 1px solid var(--rule);
	}
	.agent {
		flex: none;
		fill: var(--pic-ink);
		transition: fill 0.3s var(--ease);
	}
	.mode {
		font-size: 12px;
		color: var(--pic-dim);
	}

	/* ---------- The snapshots: one rail, no box ---------- */
	.tl {
		min-width: 0;
	}
	.tl-h {
		display: flex;
		align-items: center;
		gap: 8px;
		height: 28px;
		margin: 0;
		font-family: var(--font-sans);
		font-size: 12px;
		font-weight: 600;
		color: var(--pic-ink);
	}
	.tl-h svg {
		flex: none;
	}
	.tl-arc {
		position: relative;
		height: 34px;
		margin-top: 40px;
		transition: opacity 0.35s var(--ease);
	}
	.tl-arc svg {
		position: absolute;
		inset: 0;
		width: 100%;
		height: 100%;
		overflow: visible;
	}
	.tl-arc path {
		stroke: var(--pic-accent);
		stroke-width: 1.5;
		stroke-linecap: round;
		vector-effect: non-scaling-stroke;
	}
	/* The arrowhead lands on 21:25, the middle dot. */
	.tip {
		position: absolute;
		left: 50%;
		bottom: -4px;
		border: 4.5px solid transparent;
		border-top: 6px solid var(--pic-accent);
		border-bottom: 0;
		transform: translateX(-50%);
	}
	.restore {
		position: absolute;
		left: 66.67%;
		top: -24px;
		transform: translateX(-50%);
		font-size: 12px;
		font-weight: 500;
		color: var(--pic-accent);
		white-space: nowrap;
	}
	.tl-rail {
		position: relative;
		display: grid;
		grid-template-columns: repeat(3, 1fr);
		margin: 8px 0 0;
		padding: 0;
		list-style: none;
	}
	.tl-rail::before {
		content: '';
		position: absolute;
		left: 16.67%;
		right: 16.67%;
		top: 5px;
		border-top: 1px solid var(--rule-strong);
	}
	.snap {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 10px;
		transition: opacity 0.35s var(--ease);
	}
	.snap i {
		position: relative;
		width: 11px;
		height: 11px;
		border: 1.5px solid var(--rule-strong);
		border-radius: 50%;
		background: var(--surface);
		transition:
			background-color 0.3s var(--ease),
			border-color 0.3s var(--ease);
	}
	.t {
		font-weight: 500;
		color: var(--pic-dim);
		transition: color 0.3s var(--ease);
	}
	.stop i {
		border-color: var(--pic-stop);
		background: var(--pic-stop);
	}
	.stop .t {
		color: var(--pic-stop);
	}

	/* ---------- The still frame (restored) ---------- */
	.mine i {
		border-color: var(--pic-accent);
		background: var(--pic-accent);
	}
	.mine .t {
		color: var(--pic-accent);
	}

	/* ---------- Work: the sandbox runs, nothing to restore yet ---------- */
	.wk[data-phase='work'] .stop,
	.wk:not([data-phase='restored']) .tl-arc {
		opacity: 0;
	}
	.wk:not([data-phase='restored']) .mine i {
		border-color: var(--rule-strong);
		background: var(--surface);
	}
	.wk:not([data-phase='restored']) .mine .t {
		color: var(--pic-dim);
	}

	/* ---------- Wreck: the agent turns red and takes the repo ---------- */
	.wk[data-phase='wreck'] .agent {
		fill: var(--pic-stop);
	}
	.wk[data-phase='wreck'] .fname {
		color: var(--pic-faint);
	}
	.wk[data-phase='wreck'] .fname::after {
		transform: scaleX(1);
	}
	.wk[data-phase='wreck'] .kid:nth-child(2) .fname::after {
		transition-delay: 0.1s;
	}
	.wk[data-phase='wreck'] .kid:nth-child(3) .fname::after {
		transition-delay: 0.2s;
	}
	.wk[data-phase='wreck'] .kid:nth-child(4) .fname::after {
		transition-delay: 0.3s;
	}
	.wk[data-phase='wreck'] .stop {
		transition-delay: 1.2s;
	}
	.wk[data-phase='wreck'] :global(.win-dot) {
		background: var(--rule-strong);
	}

	/* ---------- Phone and tablet: stacked, the snapshots under the sandbox ---------- */
	@media (max-width: 1023.98px) {
		.wk {
			grid-template-columns: minmax(0, 30rem);
			row-gap: 24px;
			align-items: stretch;
		}
		.tl-arc {
			margin-top: 28px;
		}
	}

	@media (prefers-reduced-motion: reduce) {
		.wk *,
		.wk *::after {
			transition: none !important;
		}
	}
</style>
