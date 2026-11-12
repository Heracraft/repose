<!--
  Compose: the hero's picture (hero loop, round 1). Three always-on
  agents, each a machine window built from the same five layers, top down:
  the agent (the one lit part), its trigger, your setup, its secrets
  and the machine. The rows line up across the three windows, so a
  layer reads across and an agent reads down; a layer an agent leaves out
  reads "none" in the faint grey. The machine row carries a meter of its memory
  in 2 GB steps, so a tiny machine and a large one differ at a glance.

  The loop shows them always on: the shopping agent's schedule fires at 7
  and wakes its frozen machine, then the job board's webhook wakes the
  job agent; each goes back to frozen once it is done. It moves only by
  class changes (colour and opacity, CSS transitions), timed by anime.js.

  Still frame (reduced motion, no script, and the loop's seam): the dev
  environment running, the other two frozen. Every string is a docs fact
  or one of the two lines I-1169 writes ahead (a schedule or a webhook
  starting a session, a 2 GB machine).
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { watchReducedMotion } from './inview';
	import { Picture } from './motion.svelte';
	import PauseButton from './PauseButton.svelte';
	import { agentMarks, type Mark } from '$lib/components/illustrations/marks';

	type Cell = { v: string; note?: string; mono?: boolean } | null;
	type Use = {
		name: string;
		short: string;
		running: boolean;
		mark: Mark;
		gb: number;
		layers: { agent: Cell; trigger: Cell; secrets: Cell; setup: Cell; machine: Cell };
	};
	const mark = (n: string) => agentMarks.find((m) => m.name === n)!;

	const layers = ['agent', 'trigger', 'setup', 'secrets', 'machine'] as const;
	const names: Record<(typeof layers)[number], string> = {
		agent: 'Agent',
		trigger: 'Trigger',
		secrets: 'Secrets',
		setup: 'Setup',
		machine: 'Machine'
	};

	const uses: Use[] = [
		{
			name: 'A dev environment',
			short: 'dev',
			running: true,
			mark: mark('opencode'),
			gb: 16,
			layers: {
				agent: { v: 'opencode', note: 'on your branch' },
				trigger: null,
				secrets: { v: 'STRIPE_SECRET_KEY', mono: true },
				setup: { v: 'repose run', note: 'code, tools, logins', mono: true },
				machine: { v: '4 cores, 16 GB' }
			}
		},
		{
			name: 'A shopping agent',
			short: 'shopping',
			running: false,
			mark: mark('Codex'),
			gb: 2,
			layers: {
				agent: { v: 'Codex', note: 'asks before it pays' },
				trigger: { v: 'every morning at 7' },
				secrets: { v: 'STORE_API_KEY', mono: true },
				setup: null,
				machine: { v: '2 GB, on all day' }
			}
		},
		{
			name: 'An agent that applies for jobs',
			short: 'jobs',
			running: false,
			mark: mark('Claude Code'),
			gb: 8,
			layers: {
				agent: { v: 'Claude Code', note: 'asks at a captcha' },
				trigger: { v: 'role.created', note: 'webhook', mono: true },
				secrets: { v: 'JOB_BOARD_KEY', mono: true },
				setup: { v: 'resumes/', note: 'from your laptop', mono: true },
				machine: { v: '2 cores, 8 GB' }
			}
		}
	];

	let pic: HTMLDivElement;
	const picture = new Picture();

	onMount(() => {
		let dead = false;
		let run = 0;
		let tl: { pause(): unknown; play(): unknown } | null = null;
		const win = (k: number) => pic.querySelector<HTMLElement>(`.win[data-u="${k}"]`)!;

		// On a phone one window shows at a time: the one the loop is on.
		function show(k: number) {
			pic.querySelectorAll('.cur').forEach((el) => el.classList.remove('cur'));
			pic.querySelectorAll(`[data-u="${k}"]`).forEach((el) => el.classList.add('cur'));
		}
		function rest() {
			tl?.pause();
			tl = null;
			show(0);
			uses.forEach((u, k) => {
				win(k).classList.toggle('on', u.running);
				win(k).querySelector('.trigger')?.classList.remove('fire');
			});
		}

		function start() {
			const me = ++run;
			import('animejs').then(({ createTimeline }) => {
				if (dead || me !== run) return;
				const set = (k: number, cls: string, on: boolean, sel?: string) => () => {
					const el = sel ? win(k).querySelector(sel) : win(k);
					el?.classList.toggle(cls, on);
				};
				function cycle() {
					if (dead || me !== run) return;
					const t = createTimeline({ autoplay: false, onComplete: () => cycle() });
					// The schedule wakes the shopping agent, then the webhook the
					// job agent; both freeze again before the seam.
					t.call(() => show(1), 800)
						.call(set(1, 'fire', true, '.trigger'), 1200)
						.call(set(1, 'on', true), 1600)
						.call(set(1, 'fire', false, '.trigger'), 4200)
						.call(() => show(2), 4600)
						.call(set(2, 'fire', true, '.trigger'), 5000)
						.call(set(2, 'on', true), 5400)
						.call(set(2, 'fire', false, '.trigger'), 8000)
						.call(set(1, 'on', false), 10600)
						.call(set(2, 'on', false), 11200)
						.call(() => show(0), 12000)
						.call(() => {}, 12800);
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
	<svg viewBox="0 0 24 24" width="16" height="16" fill="currentColor" aria-hidden="true"
		>{#each m.paths as d (d)}<path
				{d}
				fill-rule={m.evenodd ? 'evenodd' : 'nonzero'}
				clip-rule={m.evenodd ? 'evenodd' : 'nonzero'}
			/>{/each}</svg
	>
{/snippet}

<div class="cmp-frame" use:picture.frame>
	<div
		class="cmp"
		bind:this={pic}
		role="img"
		aria-label="Three always-on agents, each on a machine of its own, built from the same layers: agent, trigger, setup, secrets and machine. A dev environment: opencode on your branch, no trigger, your code, tools and logins copied by repose run, STRIPE_SECRET_KEY, 4 cores and 16 GB. A shopping agent: Codex, which asks before it pays, every morning at 7, no setup, STORE_API_KEY, 2 GB on all day. An agent that applies for jobs: Claude Code, which asks at a captcha, a role.created webhook, your resumes, JOB_BOARD_KEY, 2 cores and 8 GB. The schedule and the webhook wake their frozen machines."
	>
		<p class="pick" aria-hidden="true">
			{#each uses as u, k (u.name)}<span class:cur={k === 0} data-u={k}>{u.short}</span>{/each}
		</p>
		{#each uses as u, k (u.name)}
			<div class="win" class:on={u.running} class:cur={k === 0} data-u={k} aria-hidden="true">
				<div class="head">
					<span class="who">{u.name}</span>
					<span class="state"
						><i class="dot"></i><span class="sw"
							><span class="s-run">running</span><span class="s-frz">frozen</span></span
						></span
					>
				</div>
				{#each layers as l (l)}
					{@const c = u.layers[l]}
					<div class="row {l}" class:out={c === null}>
						<span class="nm">{names[l]}</span>
						{#if c === null}
							<span class="val none">none</span>
						{:else}
							<span class="val" class:mono={c.mono}
								>{#if l === 'agent'}{@render markSvg(u.mark)}{/if}<span class="tx">{c.v}</span></span
							>
							{#if l === 'machine'}
								<span class="meter" aria-hidden="true"
									>{#each { length: 8 }, i (i)}<i class:f={i < u.gb / 2}></i>{/each}</span
								>
							{:else if c.note}
								<span class="note">{c.note}</span>
							{/if}
						{/if}
					</div>
				{/each}
			</div>
		{/each}
	</div>
	<PauseButton {picture} name="agents" />
</div>

<style>
	.cmp {
		--row-h: 40px;
		display: grid;
		grid-template-columns: minmax(0, 1fr);
		gap: 16px;
		max-width: 26rem;
		margin-inline: auto;
		color: var(--pic-ink);
		font-family: var(--font-sans);
		font-size: 13px;
		line-height: 16px;
	}
	@media (min-width: 1024px) {
		.cmp {
			grid-template-columns: repeat(3, minmax(0, 1fr));
			gap: var(--col-gap, 32px);
			max-width: none;
		}
	}
	@media (max-width: 639.98px) {
		.cmp {
			--row-h: 34px;
			font-size: 12px;
		}
	}
	/* A frozen machine is drawn quiet; a running one in full ink, its
	   edge a step stronger. A trigger waking one brings it up. */
	.win {
		--v: var(--pic-ink);
		--lit: var(--pic-accent);
		min-width: 0;
		border: 1px solid var(--rule-strong);
		border-radius: 6px;
		background: var(--surface);
		overflow: hidden;
		transition: border-color 300ms ease;
	}
	.win.on {
		border-color: var(--control-edge);
	}
	.val,
	.who {
		color: var(--v);
		transition: color 300ms ease;
	}
	.on .state {
		color: var(--pic-ink);
	}
	.pick {
		display: none;
	}
	/* On a phone the three windows share one place and the loop shows
	   one at a time (out 200ms, then in 300ms), named in the strip under
	   it. */
	@media (max-width: 639.98px) {
		.cmp > .win {
			grid-area: 2 / 1;
			opacity: 0;
			transition:
				opacity 200ms ease-in,
				border-color 300ms ease;
		}
		.cmp > .win.cur {
			opacity: 1;
			transition:
				opacity 300ms ease-out 200ms,
				border-color 300ms ease;
		}
		.pick {
			order: -1;
			grid-area: 1 / 1;
			align-self: start;
			display: flex;
			justify-content: center;
			gap: 16px;
			margin: 0;
			font-family: var(--font-mono);
			font-size: 11px;
			letter-spacing: 0.06em;
			text-transform: uppercase;
			color: var(--pic-faint);
		}
		.pick > span {
			transition: color 300ms ease;
		}
		.pick > .cur {
			color: var(--pic-ink);
		}
	}
	.head {
		display: flex;
		align-items: center;
		gap: 8px;
		height: 36px;
		padding: 0 12px;
		border-bottom: 1px solid var(--rule);
		background: var(--sunken);
	}
	.who {
		min-width: 0;
		overflow: hidden;
		font-weight: 600;
		white-space: nowrap;
		text-overflow: ellipsis;
	}
	.state {
		display: flex;
		align-items: center;
		gap: 6px;
		margin-left: auto;
		font-family: var(--font-mono);
		font-size: 11px;
		color: var(--pic-dim);
	}
	.dot {
		width: 6px;
		height: 6px;
		border-radius: 50%;
		background: var(--pic-faint);
		transition: background-color 300ms ease;
	}
	.on .dot {
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
	.s-run,
	.on .s-frz {
		opacity: 0;
	}
	.on .s-run {
		opacity: 1;
	}

	.row {
		display: flex;
		align-items: center;
		gap: 10px;
		min-height: var(--row-h);
		padding: 0 12px;
		border-top: 1px solid var(--rule);
		transition:
			background-color 300ms ease,
			box-shadow 300ms ease;
	}
	.head + .row {
		border-top: 0;
	}
	.nm {
		flex: none;
		width: 4.75rem;
		font-family: var(--font-mono);
		font-size: 10.5px;
		letter-spacing: 0.06em;
		text-transform: uppercase;
		color: var(--pic-faint);
		transition: color 300ms ease;
	}
	.val {
		display: flex;
		align-items: center;
		gap: 8px;
		min-width: 0;
		white-space: nowrap;
	}
	.tx {
		overflow: hidden;
		text-overflow: ellipsis;
	}
	.mono {
		font-family: var(--font-mono);
		font-size: 0.923em;
	}
	.note {
		min-width: 0;
		margin-left: auto;
		overflow: hidden;
		color: var(--pic-dim);
		white-space: nowrap;
		text-overflow: ellipsis;
	}
	/* A layer left out: an empty slot, dashed. */
	.val.none {
		color: var(--pic-faint);
	}
	/* The one lit part: the agent. */
	.agent {
		box-shadow: inset 2px 0 0 var(--lit);
		transition: box-shadow 300ms ease;
	}
	.agent .nm,
	.agent .val {
		color: var(--lit);
	}
	.agent .val {
		font-weight: 600;
	}
	/* A trigger firing: its row lights in the accent while it wakes the machine. */
	.trigger:global(.fire) {
		background: color-mix(in srgb, var(--pic-accent) 9%, var(--surface));
		box-shadow: inset 2px 0 0 var(--pic-accent);
	}
	.trigger:global(.fire) .nm,
	.trigger:global(.fire) .val {
		color: var(--pic-accent);
	}
	.meter {
		display: flex;
		gap: 2px;
		margin-left: auto;
	}
	.meter > i {
		width: 6px;
		height: 12px;
		border-radius: 1px;
		background: var(--rule);
	}
	.meter > i.f {
		background: var(--pic-dim);
		transition: background-color 300ms ease;
	}
	@media (prefers-reduced-motion: reduce) {
		.dot,
		.sw > span,
		.row,
		.nm,
		.win,
		.cmp > .win,
		.val,
		.who,
		.meter > i,
		.pick > span {
			transition: none !important;
		}
	}
</style>
