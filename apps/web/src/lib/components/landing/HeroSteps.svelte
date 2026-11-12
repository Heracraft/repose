<!--
  HeroSteps: the hero's picture as one job's window, drawn five times along
  one line, each time in a different state (hero loop, 2026-11-12; round
  11: show, don't tell; round 12: one story; round 13: one object; round
  14: the picture alone tells it). Arrows run from window to window, so the
  eye follows one thing left to right (top to bottom below 1180px):

  1. Ready: Claude Code in the "Failed payments" window, any agent beside
     it; under it your laptop, shut.
  2. Asleep: the same window, dim, a moon.
  3. Woken: an alert drops in; the window lights, "Checking 3 failed
     payments", one line of code under it for the developer.
  4. Asking: the window's question on your phone, Yes / No.
  5. Done: a tick, "$420 recovered".

  The loop walks a token along the line from state to state, and each
  window does its one thing as the token reaches it. It moves by class
  changes and anime.js translate, opacity and scale.

  Still frame (reduced motion, no script, and between loops): the five
  states drawn whole.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { watchReducedMotion } from './inview';
	import { Picture } from './motion.svelte';
	import { agentMarks, type Mark } from '$lib/components/illustrations/marks';

	type Glyph = 'bell' | 'moon' | 'phone' | 'tick';
	const mark = (n: string) => agentMarks.find((m) => m.name === n)!;
	const claude = mark('Claude Code');
	const JOB = 'Payments agent';
	const titles = [
		'Its own cloud computer',
		'Asleep until needed',
		'A failed payment wakes it',
		'It asks you first',
		'Then it gets it done'
	];

	let pic: HTMLDivElement;
	const picture = new Picture();
	const STEP = 2600;

	onMount(() => {
		let dead = false;
		let run = 0;
		let tl: { pause(): unknown; play(): unknown } | null = null;
		const q = (s: string) => Array.from(pic.querySelectorAll<HTMLElement | SVGElement>(s));
		const scene = (k: number) => pic.querySelector<HTMLElement>(`.scene[data-k="${k}"]`)!;

		function light(k: number) {
			q('.scene').forEach((el, i) => el.classList.toggle('lit', i === k));
		}
		function rest() {
			tl?.pause();
			tl = null;
			light(-1);
			q('[data-a]').forEach((el) => el.removeAttribute('style'));
			q('.hi').forEach((el) => el.classList.remove('hi'));
			q('.off').forEach((el) => el.classList.remove('off'));
		}

		function start() {
			const me = ++run;
			import('animejs').then(({ createTimeline }) => {
				if (dead || me !== run) return;
				type TL = ReturnType<typeof createTimeline>;
				const cls = (el: Element, c: string, on: boolean) => () => el.classList.toggle(c, on);

				// The token runs down scene k's stretch of the line to the next window.
				function travel(t: TL, k: number, at: number) {
					const th = scene(k).querySelector<HTMLElement>('.thread');
					const tok = th?.querySelector<HTMLElement>('.tok');
					if (!th || !tok) return;
					const across = th.offsetWidth >= th.offsetHeight;
					const len = (across ? th.offsetWidth : th.offsetHeight) - 8;
					const p = across ? 'x' : 'y';
					t.add(tok, { opacity: [0, 1], [p]: [0, 0], duration: 160 }, at);
					t.add(tok, { [p]: [0, len], duration: 620, ease: 'inOutCubic' }, at + 160);
					t.add(tok, { opacity: [1, 0], duration: 160 }, at + 780);
				}
				// 1: the agent steps into the window.
				function ready(t: TL, at: number) {
					const m = q('.s1 .inmark')[0];
					t.add(m, { opacity: [0, 0], y: [-18, -18], duration: 1 }, at);
					t.add(m, { opacity: [0, 1], y: [-18, 0], duration: 560, ease: 'outCubic' }, at + 200);
				}
				// 2: the window dims and its moon comes up.
				function sleeps(t: TL, at: number) {
					const s = scene(1);
					t.call(cls(s, 'off', true), at);
					t.call(cls(s, 'off', false), at + 500);
				}
				// 3: the alert drops in and the window lights.
				function wakes(t: TL, at: number) {
					const s = scene(2);
					const fall = s.querySelector<HTMLElement>('.fall')!;
					t.call(cls(s, 'off', true), at);
					t.add(fall, { scaleY: [0, 0], duration: 1 }, at);
					t.add(fall, { scaleY: [0, 1], duration: 380, ease: 'inOutCubic' }, at + 250);
					t.call(cls(s, 'off', false), at + 650);
					q('.s3 .say').forEach((el, i) => {
						t.add(el, { opacity: [0, 0], duration: 1 }, at);
						t.add(el, { opacity: [0, 1], duration: 360 }, at + 750 + i * 300);
					});
				}
				// 4: the question comes in, and Yes is pressed.
				function asks(t: TL, at: number) {
					const card = q('.s4 .win')[0];
					const yes = q('.s4 .yes')[0];
					t.add(card, { opacity: [0, 0], y: [-10, -10], duration: 1 }, at);
					t.add(card, { opacity: [0, 1], y: [-10, 0], duration: 480, ease: 'outCubic' }, at + 150);
					t.add(yes, { scale: [1, 0.9, 1], duration: 360, ease: 'inOutQuad' }, at + 1000);
					t.call(cls(yes, 'hi', true), at + 1000).call(cls(yes, 'hi', false), at + 1800);
				}
				// 5: the result comes in.
				function done(t: TL, at: number) {
					const r = q('.s5 .got')[0];
					t.add(r, { opacity: [0, 0], scale: [0.92, 0.92], duration: 1 }, at);
					t.add(
						r,
						{ opacity: [0, 1], scale: [0.92, 1], duration: 480, ease: 'outCubic' },
						at + 150
					);
				}

				function cycle() {
					if (dead || me !== run) return;
					const t = createTimeline({ autoplay: false, onComplete: () => cycle() });
					[ready, sleeps, wakes, asks, done].forEach((f, k) => {
						t.call(() => light(k), k * STEP);
						f(t, k * STEP);
						if (k < 4) travel(t, k, k * STEP + STEP - 900);
					});
					t.call(() => light(-1), 5 * STEP).call(() => {}, 5 * STEP + 1400);
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

{#snippet markSvg(m: Mark, size: number)}
	<svg viewBox="0 0 24 24" width={size} height={size} fill="currentColor" aria-hidden="true"
		>{#each m.paths as d (d)}<path
				{d}
				fill-rule={m.evenodd ? 'evenodd' : 'nonzero'}
				clip-rule={m.evenodd ? 'evenodd' : 'nonzero'}
			/>{/each}</svg
	>
{/snippet}

{#snippet glyph(kind: Glyph, size: number)}
	<svg
		class="gl"
		viewBox="0 0 16 16"
		width={size}
		height={size}
		fill="none"
		stroke="currentColor"
		stroke-width={kind === 'tick' ? 1.75 : 1.25}
		stroke-linejoin="round"
		stroke-linecap="round"
		aria-hidden="true"
	>
		{#if kind === 'bell'}
			<path d="M4 11V7.5a4 4 0 0 1 8 0V11l1 1.5H3zM6.5 14h3" />
		{:else if kind === 'phone'}
			<rect x="4.25" y="1.75" width="7.5" height="12.5" rx="1.5" /><path d="M7 12h2" />
		{:else if kind === 'tick'}
			<path d="M3.5 8.5l3 3 6-7" />
		{:else}
			<path d="M12.5 10.2A5.25 5.25 0 0 1 5.8 3.5a5.25 5.25 0 1 0 6.7 6.7z" />
		{/if}
	</svg>
{/snippet}

<!-- The window's head, the same in every state: what leads it, the job, the state. -->
{#snippet head(lead: Glyph | 'agent', end: 'dot' | 'grey' | 'now' | 'none')}
	<div class="head">
		<span class="lead lead-{lead}"
			>{#if lead === 'agent'}{@render markSvg(claude, 14)}{:else}{@render glyph(
					lead,
					14
				)}{/if}</span
		>
		<span class="job">{JOB}</span>
		{#if end === 'now'}<span class="now">now</span>{:else if end !== 'none'}<i
				class="dot"
				class:grey={end === 'grey'}
			></i>{/if}
	</div>
{/snippet}

<div class="steps-frame" use:picture.frame>
	<div
		class="steps"
		bind:this={pic}
		role="img"
		aria-label="One payments agent, followed through five states. Claude Code, or any agent, runs on its own computer in the cloud with one instruction: when a payment fails, retry it, ask me first. Your laptop is shut. The machine sleeps until it is needed. A failed payment, sent by a Stripe webhook, wakes it (or a set time such as 8:00) and it starts checking 3 failed payments. It asks you on your phone, retry 3 declined cards, yes or no. Then it gets it done: 420 dollars recovered."
	>
		{#each titles as title, k (title)}
			<div class="scene s{k + 1}" data-k={k} aria-hidden="true">
				{#if k < 4}<i class="thread"><i class="tok" data-a></i></i>{/if}
				<span class="ttl">{title}</span>
				<div class="draw">
					{#if k === 0}
						<div class="win">
							{@render head('agent', 'dot')}
							<div class="body agent">
								<span class="inmark" data-a>{@render markSvg(claude, 20)}</span>
								<span class="who2"
									><span class="big">Claude Code</span><span class="any">or any agent</span></span
								>
							</div>
							<p class="job-say">“When a payment fails, retry it. Ask me first.”</p>
							<div class="lap">
								<svg viewBox="0 0 64 24" width="42" height="16" aria-hidden="true">
									<path class="lid" d="M10.5 0.5h43l9 17h-61z" />
									<path class="base" d="M1.5 17.5h61v3a2 2 0 0 1-2 2h-57a2 2 0 0 1-2-2z" />
									<path class="moon" d="M35.2 11.4A4.2 4.2 0 0 1 29.6 5.8a4.2 4.2 0 1 0 5.6 5.6z" />
								</svg>
								your laptop, shut
							</div>
						</div>
					{:else if k === 1}
						<div class="win sleeping">
							{@render head('moon', 'grey')}
							<div class="body zz">
								{@render glyph('moon', 18)}<span>asleep</span>
							</div>
						</div>
					{:else if k === 2}
						<span class="above"
							><span class="bell">{@render glyph('bell', 14)}</span>a Stripe webhook<span
								class="alt">or 8:00</span
							><i class="fall" data-a></i></span
						>
						<div class="win woke">
							{@render head('bell', 'dot')}
							<div class="body">
								<span class="say" data-a>Checking 3 failed payments…</span>
							</div>
						</div>
					{:else if k === 3}
						<span class="phone"><i class="notch"></i></span>
						<div class="win card" data-a>
							{@render head('phone', 'now')}
							<div class="body">
								<p class="ask">Retry 3 declined cards?</p>
								<div class="btns">
									<span class="yes" data-a>Yes</span><span class="no">No</span>
								</div>
							</div>
						</div>
					{:else}
						<div class="win finished">
							{@render head('tick', 'none')}
							<div class="body got" data-a>
								<span class="sum">$420 recovered</span>
								<span class="sub">3 cards charged</span>
							</div>
						</div>
					{/if}
				</div>
			</div>
		{/each}
	</div>
</div>

<style>
	.steps {
		/* The title block, the room above a window, a window's head, a
		   drawing's width. The line runs through the heads. */
		--th: 40px;
		--above: 34px;
		--hh: 30px;
		--dw: 188px;
		display: grid;
		grid-template-columns: repeat(5, minmax(0, 1fr));
		padding: 0 16px;
		color: var(--pic-ink);
		font-family: var(--font-sans);
		font-size: 13px;
		line-height: 18px;
	}

	/* A state: its short title, its window, the arrow on to the next. */
	.scene {
		position: relative;
		display: grid;
		grid-template-rows: var(--th) auto;
		min-width: 0;
	}
	.ttl {
		margin-right: 12px;
		font-size: 16px;
		line-height: 22px;
		font-weight: 600;
		letter-spacing: -0.01em;
	}
	.thread {
		position: absolute;
		left: calc(var(--dw) + 2px);
		right: 2px;
		top: calc(var(--th) + var(--above) + var(--hh) / 2 - 0.5px);
		height: 1px;
		background: var(--pic-dim);
		transition: background-color 300ms ease;
	}
	.thread::after {
		content: '';
		position: absolute;
		right: 0;
		top: -3px;
		width: 6px;
		height: 6px;
		border: solid var(--pic-dim);
		border-width: 1px 1px 0 0;
		transform: rotate(45deg);
		transition: border-color 300ms ease;
	}
	.scene:global(.lit) .thread {
		background: var(--pic-accent);
	}
	.scene:global(.lit) .thread::after {
		border-color: var(--pic-accent);
	}
	.tok {
		position: absolute;
		left: 0;
		top: -3.5px;
		width: 8px;
		height: 8px;
		border-radius: 50%;
		background: var(--pic-accent);
		opacity: 0;
	}

	.draw {
		position: relative;
		width: var(--dw);
		height: 186px;
	}

	.dot {
		flex: none;
		width: 7px;
		height: 7px;
		border-radius: 50%;
		background: var(--color-emerald-500);
	}
	.dot.grey {
		background: var(--pic-faint);
	}

	/* The window, the same in every state. */
	.win {
		position: absolute;
		left: 0;
		right: 0;
		top: var(--above);
		display: flex;
		flex-direction: column;
		border: 1px solid var(--control-edge);
		border-radius: 6px;
		background: var(--sunken);
		transition:
			border-color 300ms ease,
			opacity 300ms ease;
	}
	.head {
		display: flex;
		flex: none;
		align-items: center;
		gap: 7px;
		height: calc(var(--hh) - 1px);
		padding: 0 10px;
		border-bottom: 1px solid var(--rule);
		border-radius: 5px 5px 0 0;
		background: var(--surface);
		color: var(--pic-ink);
		font-size: 12px;
		font-weight: 600;
		white-space: nowrap;
	}
	.head > :last-child:not(.job) {
		margin-left: auto;
	}
	.lead {
		display: flex;
		color: var(--pic-accent);
	}
	.lead-tick {
		color: var(--color-emerald-500);
	}
	.now {
		color: var(--pic-faint);
		font-weight: 400;
	}
	.body {
		display: grid;
		align-content: start;
		gap: 4px;
		padding: 10px;
	}

	/* 1: ready, the agent in it; your laptop shut, apart. */
	.body.agent {
		display: flex;
		align-items: center;
		gap: 9px;
		padding: 8px 10px 4px;
	}
	.inmark {
		display: grid;
		place-items: center;
		color: var(--pic-accent);
	}
	.big {
		font-size: 14px;
		font-weight: 600;
	}
	.who2 {
		display: grid;
		white-space: nowrap;
	}
	.big {
		line-height: 18px;
	}
	.any {
		color: var(--pic-faint);
		font-size: 12px;
		line-height: 15px;
	}
	.job-say {
		margin: 0;
		padding: 2px 10px 9px;
		font-size: 13px;
		line-height: 18px;
	}
	/* The laptop, attached to the machine's foot: shut, apart from it. */
	.lap {
		display: flex;
		align-items: center;
		gap: 8px;
		height: 26px;
		padding: 0 10px;
		border-top: 1px solid var(--rule);
		color: var(--pic-ink);
		font-size: 13px;
		white-space: nowrap;
	}
	.lid,
	.base {
		fill: var(--surface);
		stroke: var(--pic-dim);
		stroke-linejoin: round;
	}
	.base {
		fill: var(--sunken);
	}
	.moon {
		fill: none;
		stroke: var(--pic-dim);
		stroke-width: 1.1;
		stroke-linejoin: round;
	}

	/* 2: asleep. */
	.sleeping {
		border-color: var(--rule);
		opacity: 0.62;
	}
	.sleeping .head {
		color: var(--pic-dim);
	}
	.sleeping .lead {
		color: var(--pic-dim);
	}
	.zz {
		display: flex;
		align-items: center;
		gap: 8px;
		padding: 14px 10px;
		color: var(--pic-dim);
	}
	.s2:global(.off) .sleeping {
		opacity: 1;
		border-color: var(--control-edge);
	}

	/* 3: woken by an alert. */
	.above {
		position: absolute;
		left: 0;
		top: 0;
		display: flex;
		align-items: center;
		gap: 6px;
		height: 24px;
		font-size: 12px;
		font-weight: 600;
	}
	.bell {
		display: grid;
		place-items: center;
		width: 24px;
		height: 24px;
		border: 1px solid var(--pic-accent);
		border-radius: 50%;
		color: var(--pic-accent);
	}
	.fall {
		position: absolute;
		left: 11.5px;
		top: 24px;
		width: 1px;
		height: calc(var(--above) - 24px);
		background: var(--pic-accent);
		transform-origin: top center;
	}
	.woke {
		border-color: var(--pic-accent);
	}
	.say {
		font-size: 13px;
		line-height: 18px;
	}
	.alt {
		margin-left: 4px;
		color: var(--pic-faint);
		font-weight: 400;
	}
	.caret {
		display: inline-block;
		width: 6px;
		height: 12px;
		margin-left: 2px;
		vertical-align: -1px;
		background: var(--pic-accent);
	}
	.s3:global(.off) .dot {
		background: var(--pic-faint);
	}
	.s3:global(.off) .woke {
		border-color: var(--rule);
		opacity: 0.62;
	}

	/* 4: asking, on your phone, the phone around the window. */
	.phone {
		position: absolute;
		left: 0;
		right: 0;
		top: calc(var(--above) - 22px);
		bottom: 0;
		border: 1px solid var(--rule-strong);
		border-radius: 18px;
		background: var(--sunken);
	}
	.notch {
		position: absolute;
		left: 50%;
		top: 7px;
		width: 30px;
		height: 5px;
		margin-left: -15px;
		border-radius: 3px;
		background: var(--rule-strong);
	}
	.card {
		left: 8px;
		right: 8px;
		background: var(--surface);
	}
	.ask {
		margin: 0 0 6px;
		font-size: 14px;
		line-height: 19px;
		font-weight: 600;
	}
	.btns {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 6px;
	}
	.btns > span {
		display: grid;
		place-items: center;
		height: 28px;
		border-radius: 6px;
		font-weight: 600;
	}
	.yes {
		background: var(--pic-accent);
		color: var(--surface);
		transition: background-color 200ms ease;
	}
	.yes:global(.hi) {
		background: var(--pic-ink);
	}
	.no {
		border: 1px solid var(--rule-strong);
		color: var(--pic-dim);
	}

	/* 5: done. */
	.finished {
		border-color: var(--color-emerald-500);
	}
	.sum {
		font-size: 17px;
		line-height: 22px;
		font-weight: 600;
	}
	.sub {
		color: var(--pic-dim);
		font-size: 12px;
	}

	/* Below 1180px: one state under the other, title at the left, the
	   arrows running down. */
	@media (max-width: 1179.98px) {
		.steps {
			--above: 0px;
			--hh: 26px;
			--dw: 220px;
			--tc: 96px;
			grid-template-columns: minmax(0, 1fr);
			row-gap: 18px;
			max-width: 26rem;
			margin-inline: auto;
		}
		.scene {
			grid-template-rows: auto;
			grid-template-columns: var(--tc) minmax(0, 1fr);
			column-gap: 12px;
		}
		.ttl {
			margin: 3px 0 0;
			font-size: 14px;
			line-height: 18px;
		}
		.thread {
			left: calc(var(--tc) + 12px + 24px);
			right: auto;
			top: calc(100% + 2px);
			width: 1px;
			height: 14px;
		}
		.thread::after {
			right: -3px;
			top: auto;
			bottom: 0;
			transform: rotate(135deg);
		}
		.tok {
			left: -3.5px;
			top: 0;
		}
		.draw {
			width: auto;
			max-width: var(--dw);
			height: auto;
		}
		.win {
			position: relative;
		}
		.head {
			gap: 6px;
			padding: 0 8px;
		}
		.body {
			padding: 7px 8px 8px;
		}
		.body.agent {
			padding-bottom: 8px;
		}
		.big {
			font-size: 14px;
		}
		.job-say {
			padding: 0 8px 7px;
		}
		.lap {
			height: 26px;
			padding: 0 8px;
		}
		.above {
			position: relative;
			margin-bottom: 8px;
		}
		.fall {
			display: none;
		}
		.s4 .draw {
			padding: 18px 7px 8px;
		}
		.zz {
			padding: 6px 8px;
		}
		.phone {
			top: 0;
			border-radius: 16px;
		}
		.notch {
			top: 7px;
		}
		.card {
			left: 0;
			right: 0;
		}
		.now {
			display: none;
		}
		.ask {
			font-size: 13px;
			line-height: 17px;
		}
		.btns > span {
			height: 26px;
		}
		.sum {
			font-size: 15px;
			line-height: 20px;
		}
	}

	@media (prefers-reduced-motion: reduce) {
		.thread,
		.thread::after,
		.yes,
		.win {
			transition: none !important;
		}
	}
</style>
