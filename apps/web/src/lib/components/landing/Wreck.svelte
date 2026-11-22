<!--
  The safety figure (section 02): the agent wrecks the sandbox, a snapshot
  puts it back. Two windows and one wire, nothing else (landing council,
  2026-11-22: the laptop, the globe, the `repose run` and `snapshot` chips,
  the copied tables and the snapshot cards inside a card are gone; the hero
  already shows the laptop).

  Left, your sandbox: the repo with three files, the app's own state that
  git knows nothing about (postgres and its rows, node), and Claude Code in
  its bypass permissions mode. Right, its snapshots as a plain list, oldest
  first: the nightly at 03:00, yours at 21:25, and 21:31, the snapshot the
  stop took of the wreck. Between them the one accent chip, `restore 21:25`,
  on a wire that points back at the sandbox.

  The still frame, which is also what reduced motion, no script and the
  first paint show, is the story's end: the sandbox restored, 21:31 marked
  wrecked in red, 21:25 lit as restored, and the chip on the wire. With
  motion the loop runs from that frame: the chip and 21:31 go and the
  sandbox works (3.5s); the agent turns red, the files are struck, the
  database reads 0 rows, node is not found and the stop snapshot 21:31
  lands (3s); then the chip comes, 21:25 lights and the sandbox is back,
  which is the still frame again (held 5.5s). Only CSS transitions move,
  on a phase the script steps through, so pausing holds the phase.

  Provenance. The machine's own state is real on the recruiting machine
  (2026-09-26): 3,532 rows in its postgres, node v24.20.0. The snapshot
  times are illustrative, on one clock: nightly snapshots and one on every
  stop are /docs/lifecycle. Claude Code runs in bypassPermissions mode on
  every machine (/docs/agents).
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
		'Your sandbox holds your repo, the app’s Postgres database with 3,532 rows, and node, with ' +
		'Claude Code running in bypass permissions mode. Its snapshots are a nightly one from 03:00 and ' +
		'yours from 21:25. The agent wrecks the machine: its edits are gone, the database is empty and ' +
		'node is not found. Stopping it takes one more snapshot, 21:31, of the wreck. Restoring 21:25 ' +
		'brings the edits, all 3,532 rows and node back.';

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

{#snippet dbIcon()}
	<svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true" class="fi">
		<g fill="none" stroke="currentColor" stroke-width="1.1" stroke-linejoin="round">
			<ellipse cx="8" cy="3.8" rx="5" ry="1.9" />
			<path
				d="M3 3.8v8.4c0 1.05 2.24 1.9 5 1.9s5-.85 5-1.9V3.8M3 8c0 1.05 2.24 1.9 5 1.9s5-.85 5-1.9"
			/>
		</g>
	</svg>
{/snippet}

{#snippet toolIcon()}
	<svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true" class="fi">
		<g fill="none" stroke="currentColor" stroke-width="1.1" stroke-linejoin="round">
			<rect x="1.8" y="2.5" width="12.4" height="11" />
			<path d="M4.5 6.2l2.2 1.8-2.2 1.8M8.3 10.3h3.2" stroke-linecap="round" />
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

		<div class="win box" aria-hidden="true">
			<WinHead who="machine" dot />
			<ul class="rows">
				<li class="fr repo">{@render repoIcon()}<span>your-project/</span></li>
				{#each files as f (f)}
					<li class="fr kid">{@render fileIcon()}<span class="fname">{f}</span></li>
				{/each}
				<li class="fr sys first">
					{@render dbIcon()}<span>postgres</span>
					<span class="val"><span class="ok">3,532 rows</span><span class="bad">0 rows</span></span>
				</li>
				<li class="fr sys">
					{@render toolIcon()}<span>node</span>
					<span class="val"><span class="ok">v24.20.0</span><span class="bad">not found</span></span
					>
				</li>
			</ul>
			<div class="agentrow">
				<svg viewBox="0 0 24 24" width="20" height="20" class="agent">
					{#each agent.paths as d (d)}<path {d} fill-rule="evenodd" />{/each}
				</svg>
				<span class="mode">⏵⏵ bypass permissions on</span>
			</div>
		</div>

		<div class="wire" aria-hidden="true">
			<span class="pic-chip restore">restore 21:25</span>
			<i class="line"></i>
		</div>

		<div class="win snaps" aria-hidden="true">
			<WinHead who="snapshots" />
			<ol class="list">
				<li class="snap night"><span class="t">03:00</span><span class="why">nightly</span></li>
				<li class="snap mine">
					<span class="t">21:25</span><span class="why"><span>restored</span></span>
				</li>
				<li class="snap stop"><span class="t">21:31</span><span class="why">wrecked</span></li>
			</ol>
		</div>
	</div>
</div>

<style>
	/* ---------- Layout: sandbox, wire, snapshots ---------- */
	.wk {
		--ease: cubic-bezier(0.33, 1, 0.68, 1);
		display: grid;
		grid-template-columns: minmax(0, 27rem) minmax(9rem, 12rem) minmax(0, 17rem);
		justify-content: center;
		align-items: center;
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
		padding: 8px 0;
		list-style: none;
	}
	.fr {
		display: flex;
		align-items: center;
		gap: 8px;
		height: 28px;
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
	/* The strike across a lost file, drawn from the left. */
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
	.sys.first {
		margin-top: 8px;
		border-top: 1px solid var(--rule);
		height: 37px;
		padding-top: 8px;
	}
	.val {
		display: grid;
		margin-left: auto;
		text-align: right;
	}
	.val > span {
		grid-area: 1 / 1;
		transition: opacity 0.3s var(--ease);
	}
	.val .bad {
		opacity: 0;
		color: var(--pic-stop);
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

	/* ---------- The wire: the one chip, pointing back at the sandbox ---------- */
	.wire {
		position: relative;
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 8px;
		padding: 0 14px;
	}
	.line {
		position: relative;
		display: block;
		width: 100%;
		border-top: 1.5px solid var(--pic-accent);
		transform-origin: right;
		transition:
			transform 0.45s var(--ease),
			opacity 0.3s var(--ease);
	}
	.line::before {
		content: '';
		position: absolute;
		left: -1px;
		top: -5.5px;
		border: 4.5px solid transparent;
		border-right: 6px solid var(--pic-accent);
		border-left: 0;
	}
	.restore {
		transition: opacity 0.3s var(--ease);
	}

	/* ---------- The snapshots: a plain list, oldest first ---------- */
	.list {
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.snap {
		display: flex;
		align-items: baseline;
		gap: 12px;
		height: 40px;
		padding: 10px 14px 0;
		transition: opacity 0.35s var(--ease);
	}
	.snap + .snap {
		border-top: 1px solid var(--rule);
	}
	.t {
		font-weight: 500;
		transition: color 0.3s var(--ease);
	}
	.why {
		margin-left: auto;
		font-family: var(--font-sans);
		font-size: 12px;
		color: var(--pic-dim);
	}
	.why > span {
		transition: opacity 0.3s var(--ease);
	}
	.stop .why {
		color: var(--pic-stop);
	}

	/* ---------- The still frame (restored) ---------- */
	.mine .t,
	.mine .why {
		color: var(--pic-accent);
	}

	/* ---------- Work: the sandbox runs, nothing to restore yet ---------- */
	.wk[data-phase='work'] :is(.restore, .stop),
	.wk[data-phase='wreck'] .restore,
	.wk:not([data-phase='restored']) .mine .why > span {
		opacity: 0;
	}
	.wk:not([data-phase='restored']) .line {
		transform: scaleX(0);
		opacity: 0;
	}
	.wk:not([data-phase='restored']) .mine .t {
		color: var(--pic-ink);
	}

	/* ---------- Wreck: the agent turns red and takes what git can't bring back ---------- */
	.wk[data-phase='wreck'] .agent {
		fill: var(--pic-stop);
	}
	.wk[data-phase='wreck'] .fname {
		color: var(--pic-faint);
	}
	.wk[data-phase='wreck'] .fname::after {
		transform: scaleX(1);
	}
	.wk[data-phase='wreck'] .kid:nth-child(3) .fname::after {
		transition-delay: 0.12s;
	}
	.wk[data-phase='wreck'] .kid:nth-child(4) .fname::after {
		transition-delay: 0.24s;
	}
	.wk[data-phase='wreck'] .val .ok {
		opacity: 0;
	}
	.wk[data-phase='wreck'] .val .bad {
		opacity: 1;
		transition-delay: 0.4s;
	}
	.wk[data-phase='wreck'] .stop {
		transition-delay: 1.2s;
	}
	.wk[data-phase='wreck'] :global(.win-dot) {
		background: var(--rule-strong);
	}

	/* ---------- Phone and tablet: stacked, the wire points up ---------- */
	@media (max-width: 1023.98px) {
		.wk {
			grid-template-columns: minmax(0, 30rem);
			align-items: stretch;
		}
		.wire {
			flex-direction: row-reverse;
			justify-content: center;
			gap: 12px;
			height: 64px;
			padding: 0;
		}
		.line {
			width: 0;
			height: 100%;
			border-top: 0;
			border-left: 1.5px solid var(--pic-accent);
			transform-origin: bottom;
		}
		.line::before {
			left: -5.25px;
			top: -1px;
			border: 4.5px solid transparent;
			border-bottom: 6px solid var(--pic-accent);
			border-top: 0;
		}
		.wk:not([data-phase='restored']) .line {
			transform: scaleY(0);
		}
	}

	@media (prefers-reduced-motion: reduce) {
		.wk *,
		.wk *::after {
			transition: none !important;
		}
	}
</style>
