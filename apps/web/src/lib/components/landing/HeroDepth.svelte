<!--
  HeroDepth: the hero's picture as a product shot (hero loop, depth
  direction, 2026-11-12). Three always-on agents as three real windows,
  set in perspective on a shallow arc: the on-call responder in front and
  in focus, edged in the accent; your dev environment and the job agent
  turned toward it on either side, quieter. Each window says in plain
  words what its agent is doing, and its foot names its machine.

  The loop: a job is posted and the job agent's frozen window wakes; the
  responder moves from reading your logs to asking you; the job agent
  reaches the captcha and asks you; then the job agent freezes and the
  responder is back at its logs, the still frame. It moves by class
  changes only (colour and opacity through CSS transitions), timed by
  anime.js; the 3D arrangement itself never moves.

  Below 1200px the arc flattens to a stack, the responder first.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { watchReducedMotion } from './inview';
	import { Picture } from './motion.svelte';
	import { agentMarks, type Mark } from '$lib/components/illustrations/marks';

	type Win = {
		key: string;
		agent: Mark;
		use: string;
		running: boolean;
		/** What it is doing: done, doing now, next. */
		lines: string[];
		/** The still frame's step (index of the line it is on). */
		step: number;
		/** Shown in place of the lines while it is frozen. */
		frozen?: string[];
		machine: string;
	};
	const mark = (n: string) => agentMarks.find((m) => m.name === n)!;

	const wins: Win[] = [
		{
			key: 'dev',
			agent: mark('opencode'),
			use: 'your dev environment',
			running: true,
			lines: [
				'Your code, tools and logins are here',
				'Working on your branch',
				'Keeps going with your laptop shut'
			],
			step: 1,
			machine: 'its own machine, 16 GB'
		},
		{
			key: 'call',
			agent: mark('Claude Code'),
			use: 'on call',
			running: true,
			lines: [
				'An alert went off at 3:12 am',
				'Reading your logs to find the cause',
				'Asking you before it fixes anything'
			],
			step: 1,
			machine: 'its own machine, 2 GB, always on'
		},
		{
			key: 'jobs',
			agent: mark('Codex'),
			use: 'applying for jobs',
			running: false,
			lines: ['A new job was posted', 'Filling in your application', 'Asking you at a captcha'],
			step: 0,
			frozen: [
				'Waiting for a new job to be posted',
				'Frozen while there is nothing to do',
				'Wakes in a second or two'
			],
			machine: 'its own machine, 8 GB'
		}
	];

	let pic: HTMLDivElement;
	const picture = new Picture();

	onMount(() => {
		let dead = false;
		let run = 0;
		let tl: { pause(): unknown; play(): unknown } | null = null;
		const win = (k: string) => pic.querySelector<HTMLElement>(`.win[data-k="${k}"]`)!;
		const at = (k: string, s: number) => {
			const w = win(k);
			w.querySelectorAll<HTMLElement>('.ln').forEach((li, i) => {
				li.classList.toggle('done', i < s);
				li.classList.toggle('now', i === s);
			});
		};
		const on = (k: string, v: boolean) => win(k).classList.toggle('on', v);

		function rest() {
			tl?.pause();
			tl = null;
			for (const w of wins) {
				on(w.key, w.running);
				at(w.key, w.step);
			}
		}

		function start() {
			const me = ++run;
			import('animejs').then(({ createTimeline }) => {
				if (dead || me !== run) return;
				function cycle() {
					if (dead || me !== run) return;
					const t = createTimeline({ autoplay: false, onComplete: () => cycle() });
					t.call(() => {
						at('jobs', 0);
						on('jobs', true);
					}, 1600)
						.call(() => at('jobs', 1), 3400)
						.call(() => at('call', 2), 5200)
						.call(() => at('jobs', 2), 7600)
						.call(() => on('jobs', false), 11000)
						.call(() => at('call', 1), 11400)
						.call(() => at('jobs', 0), 11600)
						.call(() => {}, 13400);
					tl = t;
					t.play();
				}
				picture.start(pic, (m) => {
					if (m === 'rest') rest();
					else if (m === 'hold') tl?.pause();
					else if (tl) tl.play();
					else cycle();
				});
			});
		}

		const unwatch = watchReducedMotion((reduce) => {
			if (reduce) {
				run++;
				picture.stop();
				rest();
			} else start();
		});
		return () => {
			dead = true;
			unwatch();
			picture.stop();
			tl?.pause();
		};
	});
</script>

{#snippet markSvg(m: Mark)}
	<svg viewBox="0 0 24 24" width="18" height="18" fill="currentColor" aria-hidden="true"
		>{#each m.paths as d (d)}<path
				{d}
				fill-rule={m.evenodd ? 'evenodd' : 'nonzero'}
				clip-rule={m.evenodd ? 'evenodd' : 'nonzero'}
			/>{/each}</svg
	>
{/snippet}

{#snippet tick()}
	<svg
		viewBox="0 0 12 12"
		width="12"
		height="12"
		fill="none"
		stroke="currentColor"
		stroke-width="1.5"
		stroke-linecap="round"
		stroke-linejoin="round"
		aria-hidden="true"><path d="M2.5 6.25 5 8.5l4.5-5" /></svg
	>
{/snippet}

<div class="dp-frame" use:picture.frame>
	<div
		class="dp"
		bind:this={pic}
		role="img"
		aria-label="Three always-on agents running at once, each in a window of its own machine. In front, Claude Code is on call: an alert went off at 3:12 am, it is reading your logs to find the cause, and it asks you before it fixes anything; its machine has 2 GB and is always on. To one side, opencode works on your branch in your dev environment, with your code, tools and logins, on a 16 GB machine, and keeps going with your laptop shut. To the other, Codex applies for jobs on an 8 GB machine: frozen while there is nothing to do, it wakes in a second when a job is posted, fills in your application and asks you at a captcha."
	>
		<div class="row" aria-hidden="true">
			{#each wins as w (w.key)}
				<div class="win" class:on={w.running} class:front={w.key === 'call'} data-k={w.key}>
					<div class="head">
						<span class="ag">{@render markSvg(w.agent)}</span>
						<span class="who"><b>{w.agent.name}</b><span class="use">{w.use}</span></span>
						<span class="state"
							><i class="dot"></i><span class="sw"
								><span class="s-run">running</span><span class="s-frz">frozen</span></span
							></span
						>
					</div>
					<div class="body">
						<ol class="lines">
							{#each w.lines as l, i (l)}
								<li class="ln" class:done={i < w.step} class:now={i === w.step}>
									<span class="mk"><i class="pt"></i>{@render tick()}</span>{l}
								</li>
							{/each}
						</ol>
						{#if w.frozen}
							<ol class="lines frz">
								{#each w.frozen as l (l)}<li class="ln">
										<span class="mk"><i class="pt"></i></span>{l}
									</li>{/each}
							</ol>
						{/if}
					</div>
					<div class="foot">{w.machine}</div>
				</div>
			{/each}
		</div>
	</div>
</div>

<style>
	.dp {
		color: var(--pic-ink);
		font-family: var(--font-sans);
		font-size: 15px;
		line-height: 22px;
	}

	/* The arc: one camera for the row, looking down a little; the side
	   windows turn toward the middle about their inner edges and sit back,
	   the front one comes forward. Nothing here moves. */
	.row {
		display: grid;
		grid-template-columns: minmax(0, 1.08fr) minmax(0, 1fr) minmax(0, 1.08fr);
		align-items: center;
		gap: 32px;
		max-width: 1200px;
		margin-inline: auto;
		padding: 36px 0 40px;
		perspective: 1500px;
		perspective-origin: 50% 20%;
	}
	.win {
		display: flex;
		flex-direction: column;
		min-width: 0;
		border: 1px solid var(--rule-strong);
		border-radius: 6px;
		background: var(--surface);
		overflow: hidden;
		opacity: 0.62;
		transition:
			opacity 400ms ease,
			border-color 300ms ease;
	}
	.win[data-k='dev'] {
		transform-origin: 100% 50%;
		transform: rotateX(4deg) rotateY(-20deg);
	}
	.win[data-k='jobs'] {
		transform-origin: 0% 50%;
		transform: rotateX(4deg) rotateY(20deg);
	}
	.win.front {
		transform: rotateX(4deg) translateZ(90px);
		opacity: 1;
		border-color: var(--pic-accent);
	}
	.win:not(.front):global(.on) {
		opacity: 0.8;
	}
	/* The side windows turn away, so their type is set a step larger to
	   read at the same size as the front one's at their far edge. */
	.win:not(.front) {
		font-size: 16px;
	}
	.win:not(.front) .who b {
		font-size: 17px;
	}
	.win:not(.front) .use {
		font-size: 15px;
	}
	.win:not(.front) .state,
	.win:not(.front) .foot {
		font-size: 14px;
	}

	.head {
		display: flex;
		align-items: center;
		gap: 12px;
		min-width: 0;
		padding: 14px 18px;
		border-bottom: 1px solid var(--rule);
	}
	.ag {
		display: grid;
		flex: none;
		place-items: center;
		width: 34px;
		height: 34px;
		border: 1px solid var(--rule);
		border-radius: 6px;
		background: var(--sunken);
		color: var(--pic-ink);
	}
	.front .ag {
		color: var(--pic-accent);
	}
	.who {
		display: flex;
		flex-direction: column;
		min-width: 0;
		line-height: 18px;
	}
	.who b {
		font-size: 15px;
		font-weight: 600;
	}
	.front .who b {
		color: var(--pic-accent);
	}
	.use {
		color: var(--pic-dim);
		font-size: 14px;
	}
	.state {
		display: flex;
		flex: none;
		align-items: center;
		gap: 6px;
		margin-left: auto;
		font-family: var(--font-mono);
		font-size: 13px;
		color: var(--pic-dim);
	}
	.win:global(.on) .state {
		color: var(--pic-ink);
	}
	.dot {
		width: 7px;
		height: 7px;
		border-radius: 50%;
		background: var(--pic-faint);
		transition: background-color 300ms ease;
	}
	.win:global(.on) .dot {
		background: var(--color-emerald-500);
	}
	.sw {
		display: inline-grid;
	}
	.sw > span {
		grid-area: 1 / 1;
		text-align: right;
		transition: opacity 200ms ease;
	}
	.win:global(.on) .s-run,
	.win:not(:global(.on)) .s-frz {
		transition-delay: 200ms;
	}
	.s-run,
	.win:global(.on) .s-frz {
		opacity: 0;
	}
	.win:global(.on) .s-run {
		opacity: 1;
	}

	/* What it is doing: done lines ticked, the current one in ink with the
	   accent point, the next one faint. A frozen window shows its frozen
	   lines in the same place. */
	.body {
		display: grid;
		padding: 16px 18px 20px;
	}
	.lines {
		display: grid;
		grid-area: 1 / 1;
		align-content: start;
		gap: 10px;
		margin: 0;
		padding: 0;
		list-style: none;
		transition: opacity 300ms ease;
	}
	/* Waking or freezing, the outgoing lines leave before the incoming
	   ones arrive, so the two sets never show at once. */
	.lines.frz {
		opacity: 0;
		transition: opacity 250ms ease;
	}
	.win:global(.on) .lines:not(.frz) {
		transition: opacity 300ms ease 250ms;
	}
	.win[data-k='jobs']:not(:global(.on)) .lines:not(.frz) {
		opacity: 0;
		transition: opacity 250ms ease;
	}
	.win[data-k='jobs']:not(:global(.on)) .lines.frz {
		opacity: 1;
		transition: opacity 300ms ease 250ms;
	}
	.ln {
		display: flex;
		align-items: center;
		gap: 10px;
		min-width: 0;
		color: var(--pic-faint);
		transition: color 300ms ease;
	}
	.ln:global(.done) {
		color: var(--pic-dim);
	}
	.ln:global(.now) {
		color: var(--pic-ink);
	}
	.frz .ln {
		color: var(--pic-dim);
	}
	.mk {
		display: inline-grid;
		flex: none;
		place-items: center;
		width: 12px;
		height: 12px;
	}
	.mk > * {
		grid-area: 1 / 1;
		transition: opacity 300ms ease;
	}
	.pt {
		width: 5px;
		height: 5px;
		border-radius: 50%;
		background: currentColor;
	}
	.mk > svg {
		opacity: 0;
	}
	.ln:global(.done) .mk > svg {
		opacity: 1;
	}
	.ln:global(.done) .pt {
		opacity: 0;
	}
	.ln:global(.now) .pt {
		background: var(--pic-accent);
	}
	.win:not(:global(.on)) .ln:global(.now) .pt {
		background: currentColor;
	}

	.foot {
		margin-top: auto;
		padding: 10px 18px;
		border-top: 1px solid var(--rule);
		background: var(--sunken);
		font-family: var(--font-mono);
		font-size: 13px;
		color: var(--pic-dim);
	}

	/* Below 1200px the arc flattens into a stack, the one in front first. */
	@media (max-width: 1199.98px) {
		.row {
			grid-template-columns: minmax(0, 1fr);
			gap: 12px;
			max-width: 30rem;
			padding: 0;
			perspective: none;
		}
		.win,
		.win[data-k='dev'],
		.win[data-k='jobs'],
		.win.front {
			transform: none;
			opacity: 1;
		}
		.win.front {
			order: -1;
		}
		.win:not(.front) {
			font-size: inherit;
		}
		.win:not(.front) .who b {
			font-size: 15px;
		}
		.win:not(.front) .use {
			font-size: 14px;
		}
		.win:not(.front) .state,
		.win:not(.front) .foot {
			font-size: 13px;
		}
	}
	@media (max-width: 479.98px) {
		.head {
			padding: 10px 14px;
		}
		.body {
			padding: 12px 14px 14px;
		}
		.lines {
			gap: 6px;
		}
		.foot {
			padding: 8px 14px;
		}
	}
	@media (prefers-reduced-motion: reduce) {
		.win,
		.dot,
		.sw > span,
		.lines,
		.ln,
		.mk > * {
			transition: none !important;
		}
	}
</style>
