<!--
  Compose: the hero's picture (hero loop, round 8). Three always-on
  agents, each a lane that reads left to right as a sentence: what starts
  it, the agent on a machine of its own (its setup, secrets and size
  inside the window, the layers it is built from), and what it does for
  you, in words a reader who does not code follows (owner, 2026-11-12).
  The lanes: you from your laptop and opencode in your dev
  environment; an alert and an on-call responder that asks before it
  changes anything; a new job posting and an agent that applies. The
  owner's uses (2026-11-12): a dev environment, the summit deck's
  responder, the job agent.

  The loop shows them always on: the alert wakes the responder's frozen
  machine, then the posting wakes the job agent's; each start fills its
  chip, runs a pulse along the wire, and its machine turns running, then
  a pulse runs out and what it does for you lights; each freezes again
  before the seam. It moves by class changes (colour, CSS transitions)
  and the pulses' translate, timed by anime.js.

  Still frame (reduced motion, no script, and the loop's seam): the dev
  environment running, the other two frozen, every lane drawn. Every
  string is a docs fact or one of the two lines I-1169 writes ahead (a
  webhook starting a session, a 2 GB machine).
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { watchReducedMotion } from './inview';
	import { Picture } from './motion.svelte';
	import { agentMarks, type Mark } from '$lib/components/illustrations/marks';

	type Glyph = 'laptop' | 'bell' | 'globe' | 'phone';
	type Part = { label: string; v: string; mono?: boolean; gb?: number };
	type Lane = {
		short: string;
		use: string;
		agent: Mark;
		running: boolean;
		from: { text: string; glyph: Glyph };
		parts: Part[];
		to: string;
	};
	const mark = (n: string) => agentMarks.find((m) => m.name === n)!;

	const lanes: Lane[] = [
		{
			short: 'dev',
			use: 'your dev environment',
			agent: mark('opencode'),
			running: true,
			from: { text: 'you start it from your laptop', glyph: 'laptop' },
			parts: [
				{ label: 'Your setup', v: 'your code, tools, logins' },
				{ label: 'Secrets', v: 'your Stripe key' },
				{ label: 'Machine', v: '4 cores, 16 GB', gb: 16 }
			],
			to: 'keeps working after you close your laptop'
		},
		{
			short: 'on call',
			use: 'an on-call responder',
			agent: mark('Claude Code'),
			running: false,
			from: { text: 'an alert goes off', glyph: 'bell' },
			parts: [
				{ label: 'Your setup', v: "your app's code" },
				{ label: 'Secrets', v: 'your metrics login' },
				{ label: 'Machine', v: '2 GB, always on', gb: 2 }
			],
			to: 'finds the cause, asks before it fixes anything'
		},
		{
			short: 'jobs',
			use: 'an agent that applies for jobs',
			agent: mark('Codex'),
			running: false,
			from: { text: 'a new job is posted', glyph: 'globe' },
			parts: [
				{ label: 'Your setup', v: 'your resumes' },
				{ label: 'Secrets', v: "the job board's key" },
				{ label: 'Machine', v: '2 cores, 8 GB', gb: 8 }
			],
			to: 'applies for you, asks you at a captcha'
		}
	];

	let pic: HTMLDivElement;
	const picture = new Picture();

	onMount(() => {
		let dead = false;
		let run = 0;
		let tl: { pause(): unknown; play(): unknown } | null = null;
		const lane = (k: number) => pic.querySelector<HTMLElement>(`.lane[data-u="${k}"]`)!;

		// Below 1024px one lane shows at a time: the one the loop is on.
		function show(k: number) {
			pic.querySelectorAll('.cur').forEach((el) => el.classList.remove('cur'));
			pic.querySelectorAll(`[data-u="${k}"]`).forEach((el) => el.classList.add('cur'));
		}
		function rest() {
			tl?.pause();
			tl = null;
			show(0);
			pic.querySelectorAll<HTMLElement>('.pulse').forEach((el) => el.removeAttribute('style'));
			lanes.forEach((l, k) => {
				lane(k).classList.toggle('on', l.running);
				lane(k).classList.remove('fire', 'done');
			});
		}

		function start() {
			const me = ++run;
			import('animejs').then(({ createTimeline }) => {
				if (dead || me !== run) return;
				const cls = (k: number, c: string, on: boolean) => () => lane(k).classList.toggle(c, on);
				const pulse = (k: number, i: number) =>
					pic.querySelector<HTMLElement>(`.lane[data-u="${k}"] .w${i} .pulse`)!;
				const across = () => matchMedia('(min-width: 1024px)').matches;
				type TL = ReturnType<typeof createTimeline>;
				// A start: the chip fills, a pulse runs into the machine and it
				// turns running; then a pulse runs out to what it does for you.
				function wake(t: TL, k: number, at: number) {
					const run = (i: number, when: number) =>
						t.add(
							pulse(k, i),
							across()
								? { x: ['-100%', '100%'], duration: 620, ease: 'inOutCubic' }
								: { y: ['-100%', '100%'], duration: 620, ease: 'inOutCubic' },
							when
						);
					t.call(() => show(k), at - 600).call(cls(k, 'fire', true), at);
					run(1, at);
					t.call(cls(k, 'on', true), at + 560);
					run(2, at + 1400);
					t.call(cls(k, 'done', true), at + 1960).call(cls(k, 'fire', false), at + 3200);
				}
				function cycle() {
					if (dead || me !== run) return;
					const t = createTimeline({ autoplay: false, onComplete: () => cycle() });
					wake(t, 1, 2400);
					wake(t, 2, 7200);
					t.call(cls(1, 'on', false), 12000)
						.call(cls(1, 'done', false), 12000)
						.call(cls(2, 'on', false), 12400)
						.call(cls(2, 'done', false), 12400)
						.call(() => show(0), 12800)
						.call(() => {}, 14000);
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

{#snippet glyph(kind: Glyph)}
	<svg
		class="gl"
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
		{:else if kind === 'bell'}
			<path d="M4 11V7.5a4 4 0 0 1 8 0V11l1 1.5H3zM6.5 14h3" />
		{:else if kind === 'globe'}
			<circle cx="8" cy="8" r="5.5" /><path
				d="M2.5 8h11M8 2.5c-1.8 1.6-2.6 3.4-2.6 5.5s.8 3.9 2.6 5.5M8 2.5c1.8 1.6 2.6 3.4 2.6 5.5s-.8 3.9-2.6 5.5"
			/>
		{:else}
			<rect x="4.5" y="1.5" width="7" height="13" rx="1.25" /><path d="M7 12.25h2" />
		{/if}
	</svg>
{/snippet}

{#snippet wire(i: number)}
	<span class="wire w{i}"><i class="line"><i class="pulse"></i></i><i class="tip"></i></span>
{/snippet}

<div class="cmp-frame" use:picture.frame>
	<div
		class="cmp"
		bind:this={pic}
		role="img"
		aria-label="Three always-on agents, each on a machine of its own. You start it from your laptop, and opencode works in your dev environment, with your code, tools and logins, your Stripe key, 4 cores and 16 GB, and keeps working after you close your laptop. An alert goes off, and Claude Code, an on-call responder with your app's code, your metrics login and 2 GB, always on,, finds the cause and asks before it fixes anything. A new job is posted, and Codex, an agent that applies for jobs with your resumes, the job board's key and 2 cores and 8 GB, applies for you and asks you at a captcha."
	>
		<p class="pick" aria-hidden="true">
			{#each lanes as l, k (l.short)}<span class:cur={k === 0} data-u={k}>{l.short}</span>{/each}
		</p>
		{#each lanes as l, k (l.short)}
			<div class="lane" class:on={l.running} class:cur={k === 0} data-u={k} aria-hidden="true">
				<span class="from">{@render glyph(l.from.glyph)}{l.from.text}</span>
				{@render wire(1)}
				<div class="win">
					<div class="head">
						<span class="ag">{@render markSvg(l.agent)}<span class="bd">{l.agent.name}</span></span>
						<span class="use">{l.use}</span>
						<span class="state"
							><i class="dot"></i><span class="sw"
								><span class="s-run">running</span><span class="s-frz">frozen</span></span
							></span
						>
					</div>
					<div class="parts">
						{#each l.parts as p (p.label)}
							<span class="part"
								><span class="nm">{p.label}</span><span class="v" class:mono={p.mono}
									><span class="tx">{p.v}</span>{#if p.gb}<span class="meter"
											>{#each { length: 8 }, i (i)}<i class:f={i < p.gb / 2}></i>{/each}</span
										>{/if}</span
								></span
							>
						{/each}
					</div>
				</div>
				{@render wire(2)}
				<span class="to">{@render glyph('phone')}<span>{l.to}</span></span>
			</div>
		{/each}
	</div>
</div>

<style>
	.cmp {
		display: grid;
		gap: 20px;
		color: var(--pic-ink);
		font-family: var(--font-sans);
		font-size: 13px;
		line-height: 18px;
	}
	/* A lane reads left to right: where it starts, the wire in, the
	   machine, the wire out, what it does for you. The five tracks are one
	   template, so the three lanes' parts stand in columns. */
	.lane {
		display: grid;
		grid-template-columns: 11rem minmax(1.5rem, 1fr) minmax(0, 37rem) minmax(1.5rem, 1fr) 14rem;
		align-items: center;
		min-width: 0;
	}
	.pick {
		display: none;
	}

	.from,
	.to {
		display: flex;
		align-items: center;
		gap: 8px;
		min-width: 0;
		padding: 8px 10px;
		border: 1px solid var(--rule-strong);
		border-radius: 6px;
		background: var(--surface);
		color: var(--pic-dim);
		transition:
			background-color 180ms ease,
			border-color 180ms ease,
			color 180ms ease;
	}
	.from {
		font-weight: 500;
		color: var(--pic-ink);
	}
	.gl {
		flex: none;
	}
	/* A start: the chip fills in the accent while it wakes the machine. */
	.lane:global(.fire) .from {
		border-color: var(--pic-accent);
		background: var(--pic-accent);
		color: var(--surface);
	}
	/* What the agent does for you, lit once it has done it. */
	.lane:global(.done) .to {
		border-color: var(--pic-accent);
		color: var(--pic-accent);
	}

	.wire {
		display: flex;
		align-items: center;
		height: 10px;
		min-width: 0;
	}
	.line {
		position: relative;
		flex: 1;
		height: 1px;
		overflow: hidden;
		background: var(--rule-strong);
	}
	.pulse {
		position: absolute;
		inset: 0;
		background: var(--pic-accent);
		transform: translateX(-100%);
	}
	.tip {
		flex: none;
		width: 0;
		height: 0;
		border: 4px solid transparent;
		border-left: 6px solid var(--rule-strong);
		border-right: 0;
	}

	.win {
		min-width: 0;
		border: 1px solid var(--rule-strong);
		border-radius: 6px;
		background: var(--surface);
		overflow: hidden;
		transition: border-color 300ms ease;
	}
	.lane:global(.on) .win {
		border-color: var(--control-edge);
	}
	.head {
		display: flex;
		align-items: center;
		gap: 10px;
		min-width: 0;
		height: 44px;
		padding: 0 14px;
		border-bottom: 1px solid var(--rule);
		box-shadow: inset 2px 0 0 var(--pic-accent);
	}
	.ag {
		display: flex;
		flex: none;
		align-items: center;
		gap: 8px;
		color: var(--pic-accent);
		font-size: 15px;
	}
	.ag .bd {
		font-weight: 600;
	}
	.use {
		min-width: 0;
		overflow: hidden;
		color: var(--pic-dim);
		white-space: nowrap;
		text-overflow: ellipsis;
	}
	.state {
		display: flex;
		flex: none;
		align-items: center;
		gap: 6px;
		margin-left: auto;
		font-family: var(--font-mono);
		font-size: 11px;
		color: var(--pic-dim);
	}
	.lane:global(.on) .state {
		color: var(--pic-ink);
	}
	.dot {
		width: 6px;
		height: 6px;
		border-radius: 50%;
		background: var(--pic-faint);
		transition: background-color 300ms ease;
	}
	.lane:global(.on) .dot {
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
	.lane:global(.on) .s-frz {
		opacity: 0;
	}
	.lane:global(.on) .s-run {
		opacity: 1;
	}

	/* The layers it is built from, side by side under the head. */
	.parts {
		display: grid;
		grid-template-columns: 1.2fr 1fr 1.3fr;
		background: var(--sunken);
	}
	.part {
		display: flex;
		flex-direction: column;
		gap: 3px;
		min-width: 0;
		padding: 9px 14px 10px;
	}
	.part + .part {
		border-left: 1px solid var(--rule);
	}
	.nm {
		font-family: var(--font-mono);
		font-size: 10.5px;
		letter-spacing: 0.06em;
		text-transform: uppercase;
		color: var(--pic-faint);
	}
	.v {
		display: flex;
		align-items: center;
		gap: 8px;
		min-width: 0;
	}
	.tx {
		min-width: 0;
		overflow: hidden;
		white-space: nowrap;
		text-overflow: ellipsis;
	}
	.mono {
		font-family: var(--font-mono);
		font-size: 12px;
	}
	.meter {
		display: flex;
		flex: none;
		gap: 2px;
		margin-left: auto;
	}
	.meter > i {
		width: 5px;
		height: 11px;
		border-radius: 1px;
		background: var(--rule);
	}
	.meter > i.f {
		background: var(--pic-dim);
	}

	/* Below 1024px a lane runs down: the start, the machine, what it does,
	   each wire turned down; the three lanes share one place and the loop
	   shows one at a time (out 200ms, then in 300ms), named in the strip
	   over it. */
	@media (max-width: 1023.98px) {
		.cmp {
			max-width: 28rem;
			margin-inline: auto;
			gap: 16px;
		}
		.lane {
			grid-area: 2 / 1;
			grid-template-columns: minmax(0, 1fr);
			justify-items: center;
			opacity: 0;
			transition: opacity 200ms ease-in;
		}
		.lane.cur {
			opacity: 1;
			transition: opacity 300ms ease-out 200ms;
		}
		.win,
		.to {
			justify-self: stretch;
		}
		.wire {
			flex-direction: column;
			width: 10px;
			height: 28px;
		}
		.line {
			width: 1px;
			height: auto;
		}
		.pulse {
			transform: translateY(-100%);
		}
		.tip {
			border: 4px solid transparent;
			border-top: 6px solid var(--rule-strong);
			border-bottom: 0;
		}
		.pick {
			display: flex;
			grid-area: 1 / 1;
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
	@media (max-width: 479.98px) {
		.head {
			flex-wrap: wrap;
			row-gap: 2px;
			height: auto;
			padding: 10px 14px;
		}
		.use {
			order: 3;
			flex-basis: 100%;
			white-space: normal;
		}
		.parts {
			grid-template-columns: minmax(0, 1fr);
		}
		.part + .part {
			border-left: 0;
			border-top: 1px solid var(--rule);
		}
		.part {
			flex-direction: row;
			align-items: baseline;
			gap: 12px;
		}
		.nm {
			flex: none;
			width: 4.5rem;
		}
		.v {
			flex: 1;
		}
	}
	@media (prefers-reduced-motion: reduce) {
		.from,
		.to,
		.win,
		.dot,
		.sw > span,
		.lane,
		.lane.cur,
		.pick > span {
			transition: none !important;
		}
	}
</style>
