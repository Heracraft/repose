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
		src: { text: string; glyph: 'laptop' | 'clock' | 'hook'; mono: boolean };
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
			src: { text: 'your laptop', glyph: 'laptop', mono: false },
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
			src: { text: '07:00', glyph: 'clock', mono: true },
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
			src: { text: 'role.created', glyph: 'hook', mono: true },
			running: false,
			mark: mark('Claude Code'),
			gb: 8,
			layers: {
				agent: { v: 'Claude Code', note: 'asks at a captcha' },
				trigger: { v: 'a webhook', note: 'from the job board' },
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
			pic.querySelectorAll('.src.fire').forEach((el) => el.classList.remove('fire'));
			pic.querySelectorAll<HTMLElement>('.pulse').forEach((el) => el.removeAttribute('style'));
			uses.forEach((u, k) => {
				win(k).classList.toggle('on', u.running);
				win(k).querySelector('.trigger')?.classList.remove('fire');
			});
		}

		function start() {
			const me = ++run;
			import('animejs').then(({ createTimeline }) => {
				if (dead || me !== run) return;
				const pulse = (k: number) => pic.querySelector<HTMLElement>(`.col[data-u="${k}"] .pulse`)!;
				const src = (k: number) => pic.querySelector<HTMLElement>(`.col[data-u="${k}"] .src`)!;
				const set = (k: number, cls: string, on: boolean, sel?: string) => () => {
					const el = sel ? win(k).querySelector(sel) : win(k);
					el?.classList.toggle(cls, on);
				};
				function cycle() {
					if (dead || me !== run) return;
					const t = createTimeline({ autoplay: false, onComplete: () => cycle() });
					// The schedule wakes the shopping agent, then the webhook the
					// job agent; both freeze again before the seam.
					t.call(() => show(1), 3000)
						.call(() => src(1).classList.add('fire'), 3000)
						.add(pulse(1), { y: ['-100%', '200%'], duration: 520, ease: 'inOutCubic' }, 3000)
						.call(set(1, 'fire', true, '.trigger'), 3400)
						.call(set(1, 'on', true), 3800)
						.call(set(1, 'fire', false, '.trigger'), 6400)
						.call(() => src(1).classList.remove('fire'), 6400)
						.call(() => show(2), 6800)
						.call(() => src(2).classList.add('fire'), 6800)
						.add(pulse(2), { y: ['-100%', '200%'], duration: 520, ease: 'inOutCubic' }, 6800)
						.call(set(2, 'fire', true, '.trigger'), 7200)
						.call(set(2, 'on', true), 7600)
						.call(set(2, 'fire', false, '.trigger'), 10200)
						.call(() => src(2).classList.remove('fire'), 10200)
						.call(set(1, 'on', false), 12800)
						.call(set(2, 'on', false), 13400)
						.call(() => show(0), 14200)
						.call(() => {}, 15000);
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
	<svg viewBox="0 0 24 24" width="20" height="20" fill="currentColor" aria-hidden="true"
		>{#each m.paths as d (d)}<path
				{d}
				fill-rule={m.evenodd ? 'evenodd' : 'nonzero'}
				clip-rule={m.evenodd ? 'evenodd' : 'nonzero'}
			/>{/each}</svg
	>
{/snippet}

{#snippet glyph(kind: 'laptop' | 'clock' | 'hook')}
	<svg
		viewBox="0 0 16 16"
		width="14"
		height="14"
		fill="none"
		stroke="currentColor"
		stroke-width="1.25"
		stroke-linejoin="round"
		stroke-linecap="round"
		aria-hidden="true"
	>
		{#if kind === 'laptop'}
			<rect x="3" y="3.5" width="10" height="7" rx="0.75" /><path d="M1.5 12.5h13" />
		{:else if kind === 'clock'}
			<circle cx="8" cy="8" r="5.5" /><path d="M8 5v3l2 1.5" />
		{:else}
			<circle cx="8" cy="8" r="5.5" /><path
				d="M2.5 8h11M8 2.5c-1.8 1.6-2.6 3.4-2.6 5.5s.8 3.9 2.6 5.5M8 2.5c1.8 1.6 2.6 3.4 2.6 5.5s-.8 3.9-2.6 5.5"
			/>
		{/if}
	</svg>
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
			<div class="col" class:cur={k === 0} data-u={k} aria-hidden="true">
				<span class="src" class:mono={u.src.mono}>{@render glyph(u.src.glyph)}{u.src.text}</span>
				<i class="wire"><i class="pulse"></i></i>
				<div class="win" class:on={u.running} data-u={k}>
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
								{#if l === 'agent'}
									<span class="val"
										>{@render markSvg(u.mark)}<span class="ag"
											><span class="tx">{c.v}</span><span class="ag-n">{c.note}</span></span
										></span
									>
								{:else}
									<span class="val" class:mono={c.mono}><span class="tx">{c.v}</span></span>
								{/if}
								{#if l === 'machine'}
									<span class="meter" aria-hidden="true"
										>{#each { length: 8 }, i (i)}<i class:f={i < u.gb / 2}></i>{/each}</span
									>
								{:else if c.note && l !== 'agent'}
									<span class="note">{c.note}</span>
								{/if}
							{/if}
						</div>
					{/each}
				</div>
			</div>
		{/each}
	</div>
	<PauseButton {picture} name="agents" />
</div>

<style>
	.cmp {
		--row-h: 44px;
		display: grid;
		grid-template-columns: minmax(0, 1fr);
		gap: 16px;
		max-width: 26rem;
		margin-inline: auto;
		color: var(--pic-ink);
		font-family: var(--font-sans);
		font-size: 14px;
		line-height: 18px;
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
	.col {
		display: flex;
		flex-direction: column;
		align-items: center;
		min-width: 0;
	}
	/* Where each agent's sessions come from, on a short wire into its
	   machine's head: your laptop, the schedule's time, the webhook's
	   event. A trigger firing fills its chip and runs a pulse down the
	   wire. */
	.src {
		display: inline-flex;
		align-items: center;
		gap: 6px;
		height: 24px;
		padding: 0 8px;
		border: 1px solid var(--rule-strong);
		border-radius: 6px;
		background: var(--surface);
		font-size: 12px;
		color: var(--pic-dim);
		transition:
			background-color 180ms ease,
			border-color 180ms ease,
			color 180ms ease;
	}
	.src.mono {
		font-family: var(--font-mono);
	}
	.src:global(.fire) {
		border-color: var(--pic-accent);
		background: var(--pic-accent);
		color: var(--surface);
	}
	.wire {
		position: relative;
		width: 1px;
		height: 28px;
		overflow: hidden;
		background: var(--rule-strong);
	}
	.pulse {
		position: absolute;
		inset: 0 -0.5px auto;
		height: 50%;
		background: var(--pic-accent);
		transform: translateY(-100%);
	}
	.win {
		align-self: stretch;
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
		.cmp > .col {
			grid-area: 2 / 1;
			opacity: 0;
			transition:
				opacity 200ms ease-in,
				border-color 300ms ease;
		}
		.cmp > .col.cur {
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
		height: 40px;
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
	.ag {
		display: flex;
		flex-direction: column;
		gap: 2px;
		min-width: 0;
	}
	.ag-n {
		overflow: hidden;
		font-size: 13px;
		font-weight: 400;
		color: var(--pic-dim);
		text-overflow: ellipsis;
	}
	.agent {
		min-height: calc(var(--row-h) + 20px);
		font-size: 15px;
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
		width: 7px;
		height: 14px;
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
		.cmp > .col,
		.src,
		.val,
		.who,
		.meter > i,
		.pick > span {
			transition: none !important;
		}
	}
</style>
