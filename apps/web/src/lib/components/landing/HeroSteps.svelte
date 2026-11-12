<!--
  HeroSteps: the hero's picture as four small scenes on one thread (hero
  loop, 2026-11-12; round 11: show, don't tell; round 12: one story; round
  13: one object). The same "Failed payments" window sits on one line that
  runs left to right through every scene, at the same height, so the eye
  follows one thing:

  1. A machine of its own: the window with the agent's code for the job;
     below it, apart, your laptop, shut and asleep.
  2. Bring your own agent: Claude Code steps down into the window; "any
     agent" and the other four marks sit quietly below.
  3. Something wakes it: an alert drops into the window, which goes from
     asleep (moon) to awake (green) and gets back to work.
  4. It asks when it matters: the window's question on your phone, Yes,
     and the result: $420 recovered.

  The loop lights one scene at a time: its stretch of the thread turns the
  accent, and its drawing does its one thing. It moves by class changes
  and anime.js translate, opacity and scale.

  Still frame (reduced motion, no script, and between loops): every scene
  drawn whole, each window in its end state.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { watchReducedMotion } from './inview';
	import { Picture } from './motion.svelte';
	import { agentMarks, type Mark } from '$lib/components/illustrations/marks';

	type Glyph = 'bell' | 'clock' | 'moon';
	const mark = (n: string) => agentMarks.find((m) => m.name === n)!;
	const claude = mark('Claude Code');
	const others = ['Codex', 'opencode', 'Gemini CLI', 'pi'].map(mark);
	const JOB = 'Failed payments';

	const titles = [
		'A machine of its own',
		'Bring your own agent',
		'Something wakes it',
		'It asks when it matters'
	];
	const code: { n: number; kw?: string; text: string }[] = [
		{ n: 1, kw: 'for', text: ' p in failed:' },
		{ n: 2, kw: '  if', text: ' p.ok:' },
		{ n: 3, text: '    charge(p)' },
		{ n: 4, kw: '  else', text: ': ask(p)' }
	];

	let pic: HTMLDivElement;
	const picture = new Picture();
	const STEP = 3400;

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

				// 1: the agent writes its lines again.
				function writes(t: TL, at: number) {
					q('.s1 .cl').forEach((el, i) => {
						t.add(el, { opacity: [0, 0], duration: 1 }, at);
						t.add(
							el,
							{ opacity: [0, 1], x: [-6, 0], duration: 360, ease: 'outCubic' },
							at + 300 + i * 380
						);
					});
				}
				// 2: the agent steps down into the window.
				function steps(t: TL, at: number) {
					const s = scene(1);
					const m = s.querySelector<HTMLElement>('.inmark')!;
					const g = s.querySelector<HTMLElement>('.ghost')!;
					const a = g.getBoundingClientRect();
					const b = m.getBoundingClientRect();
					const dx = a.left + a.width / 2 - (b.left + b.width / 2);
					const dy = a.top + a.height / 2 - (b.top + b.height / 2);
					t.call(cls(s, 'off', true), at);
					t.add(m, { x: [dx, dx], y: [dy, dy], duration: 1 }, at);
					t.add(m, { x: [dx, 0], y: [dy, 0], duration: 760, ease: 'inOutCubic' }, at + 600);
					t.call(cls(s, 'off', false), at + 1400);
				}
				// 3: the window sleeps; the alert drops in and wakes it.
				function wakes(t: TL, at: number) {
					const s = scene(2);
					const bell = s.querySelector<HTMLElement>('.trig.on')!;
					const line = s.querySelector<HTMLElement>('.fall')!;
					t.call(cls(s, 'off', true), at);
					t.call(cls(bell, 'hi', false), at);
					t.add(line, { scaleY: [0, 0], duration: 1 }, at);
					t.call(cls(bell, 'hi', true), at + 700);
					t.add(line, { scaleY: [0, 1], duration: 420, ease: 'inOutCubic' }, at + 900);
					t.call(cls(s, 'off', false), at + 1400);
				}
				// 4: the question comes in, Yes is pressed, the money comes back.
				function asks(t: TL, at: number) {
					const card = q('.s4 .card')[0];
					const yes = q('.s4 .yes')[0];
					const done = q('.s4 .done')[0];
					t.add(card, { opacity: [0, 0], y: [-12, -12], duration: 1 }, at);
					t.add(done, { opacity: [0, 0], duration: 1 }, at);
					t.add(card, { opacity: [0, 1], y: [-12, 0], duration: 560, ease: 'outCubic' }, at + 300);
					t.add(yes, { scale: [1, 0.9, 1], duration: 380, ease: 'inOutQuad' }, at + 1300);
					t.call(cls(yes, 'hi', true), at + 1300).call(cls(yes, 'hi', false), at + 2200);
					t.add(done, { opacity: [0, 1], y: [6, 0], duration: 420, ease: 'outCubic' }, at + 1800);
				}

				function cycle() {
					if (dead || me !== run) return;
					const t = createTimeline({ autoplay: false, onComplete: () => cycle() });
					[writes, steps, wakes, asks].forEach((f, k) => {
						t.call(() => light(k), k * STEP);
						f(t, k * STEP);
					});
					t.call(() => light(-1), 4 * STEP).call(() => {}, 4 * STEP + 1200);
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
		stroke-width="1.25"
		stroke-linejoin="round"
		stroke-linecap="round"
		aria-hidden="true"
	>
		{#if kind === 'bell'}
			<path d="M4 11V7.5a4 4 0 0 1 8 0V11l1 1.5H3zM6.5 14h3" />
		{:else if kind === 'clock'}
			<circle cx="8" cy="8" r="5.75" /><path d="M8 4.75V8l2.25 1.5" />
		{:else}
			<path d="M12.5 10.2A5.25 5.25 0 0 1 5.8 3.5a5.25 5.25 0 1 0 6.7 6.7z" />
		{/if}
	</svg>
{/snippet}

<!-- The window's head, the same in every scene: the agent, the job, its state. -->
{#snippet head(end: 'dot' | 'woke' | 'now')}
	<div class="head">
		<span class="who"
			><span class="m">{@render markSvg(claude, 14)}</span><span class="z"
				>{@render glyph('moon', 14)}</span
			></span
		>
		<span class="job">{JOB}</span>
		{#if end === 'now'}
			<span class="now">now</span>
		{:else if end === 'woke'}
			<span class="state"
				><span class="was">{@render glyph('moon', 13)}</span><svg
					class="to"
					viewBox="0 0 10 10"
					width="10"
					height="10"
					aria-hidden="true"><path d="M3 2l3 3-3 3" /></svg
				><i class="dot"></i></span
			>
		{:else}
			<i class="dot"></i>
		{/if}
	</div>
{/snippet}

<div class="steps-frame" use:picture.frame>
	<div
		class="steps"
		bind:this={pic}
		role="img"
		aria-label="One job, Failed payments, followed through four steps. One: Claude Code works on it in a machine of its own, writing the code, while your laptop is shut. Two: you bring the agent you like, here Claude Code, or Codex, opencode, Gemini CLI or pi. Three: the machine sleeps until an alert wakes it, and it gets back to work. Four: it asks on your phone, retry 3 declined cards, you press yes, and 420 dollars is recovered."
	>
		{#each titles as title, k (title)}
			<div class="scene s{k + 1}" data-k={k} aria-hidden="true">
				<i class="thread"></i>
				<span class="ttl">{title}</span>
				<div class="draw">
					{#if k === 0}
						<div class="win">
							{@render head('dot')}
							<div class="code">
								{#each code as l (l.n)}
									<span class="cl" data-a
										><i class="lnum">{l.n}</i>{#if l.kw}<b>{l.kw}</b>{/if}{l.text}{#if l.n === 3}<i
												class="caret"
											></i>{/if}</span
									>
								{/each}
							</div>
						</div>
						<div class="lap">
							<svg viewBox="0 0 64 24" width="64" height="24" aria-hidden="true">
								<path class="lid" d="M10.5 0.5h43l9 17h-61z" />
								<path class="base" d="M1.5 17.5h61v3a2 2 0 0 1-2 2h-57a2 2 0 0 1-2-2z" />
								<path class="moon" d="M35.2 11.4A4.2 4.2 0 0 1 29.6 5.8a4.2 4.2 0 1 0 5.6 5.6z" />
							</svg>
							<span class="cap">your laptop, shut</span>
						</div>
					{:else if k === 1}
						<span class="above">
							<span class="ghost"></span>
							<i class="fall"></i>
						</span>
						<div class="win">
							{@render head('dot')}
							<div class="inbody">
								<span class="inmark" data-a>{@render markSvg(claude, 20)}</span>
								<span class="inname">Claude Code</span>
							</div>
						</div>
						<div class="any">
							<span>or any agent</span>
							{#each others as m (m.name)}{@render markSvg(m, 14)}{/each}
						</div>
					{:else if k === 2}
						<span class="above">
							<span class="trig on"><span class="ic">{@render glyph('bell', 14)}</span>alert</span>
							<span class="trig"><span class="ic">{@render glyph('clock', 14)}</span>8:00</span>
							<i class="fall" data-a></i>
						</span>
						<div class="win">
							{@render head('woke')}
							<div class="work">charge(p)<i class="caret"></i></div>
						</div>
					{:else}
						<span class="phone"><i class="notch"></i></span>
						<div class="win card" data-a>
							{@render head('now')}
							<div class="cbody">
								<p class="ask">Retry 3 declined cards?</p>
								<div class="btns">
									<span class="yes" data-a>Yes</span><span class="no">No</span>
								</div>
							</div>
						</div>
						<span class="done" data-a
							><svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true"
								><path d="M3.5 8.5l3 3 6-7" /></svg
							>$420 recovered</span
						>
					{/if}
				</div>
			</div>
		{/each}
	</div>
</div>

<style>
	.steps {
		/* Where the thread runs: the title block, the room above the window,
		   half the window's head. */
		--th: 42px;
		--above: 40px;
		--hh: 30px;
		display: grid;
		grid-template-columns: repeat(4, minmax(0, 1fr));
		padding: 0 16px;
		color: var(--pic-ink);
		font-family: var(--font-sans);
		font-size: 13px;
		line-height: 18px;
	}

	/* A scene: its title, its drawing, and its stretch of the thread. */
	.scene {
		position: relative;
		display: grid;
		grid-template-rows: var(--th) auto;
		min-width: 0;
	}
	.thread {
		position: absolute;
		left: 0;
		right: 0;
		top: calc(var(--th) + var(--above) + var(--hh) / 2 - 0.5px);
		height: 1px;
		background: var(--rule-strong);
		transition: background-color 300ms ease;
	}
	.s4 .thread {
		right: auto;
		width: 260px;
	}
	.scene:global(.lit) .thread {
		background: var(--pic-accent);
	}
	.ttl {
		margin-right: 16px;
		font-size: 18px;
		line-height: 24px;
		font-weight: 600;
		letter-spacing: -0.01em;
	}

	/* Every drawing is the same 260 x 200 box, its window's top at --above. */
	.draw {
		position: relative;
		width: 260px;
		height: 200px;
	}

	.dot {
		flex: none;
		width: 7px;
		height: 7px;
		border-radius: 50%;
		background: var(--color-emerald-500);
		transition: background-color 300ms ease;
	}

	/* The window, the same in every scene. */
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
		gap: 8px;
		height: calc(var(--hh) - 1px);
		padding: 0 12px;
		border-bottom: 1px solid var(--rule);
		border-radius: 5px 5px 0 0;
		background: var(--surface);
		color: var(--pic-ink);
		font-size: 12px;
		font-weight: 600;
		white-space: nowrap;
	}
	.head > :last-child {
		margin-left: auto;
	}
	.who {
		display: flex;
		color: var(--pic-accent);
	}
	.who .z {
		display: none;
		color: var(--pic-dim);
	}
	.now {
		color: var(--pic-faint);
		font-weight: 400;
	}
	.state {
		display: flex;
		align-items: center;
		gap: 4px;
	}
	.was {
		display: flex;
		color: var(--pic-faint);
	}
	.to {
		fill: none;
		stroke: var(--pic-faint);
		stroke-width: 1.25;
		stroke-linecap: round;
		stroke-linejoin: round;
	}
	.code,
	.work {
		font-family: var(--font-mono);
		font-size: 12px;
		white-space: pre;
		color: var(--pic-ink);
	}
	.caret {
		display: inline-block;
		width: 7px;
		height: 14px;
		margin-left: 2px;
		vertical-align: -2px;
		background: var(--pic-accent);
	}

	/* 1: the code for the job; your laptop below, apart, shut. */
	.code {
		display: grid;
		align-content: start;
		padding: 8px 12px 10px;
		line-height: 19px;
	}
	.lnum {
		display: inline-block;
		width: 18px;
		font-style: normal;
		color: var(--pic-faint);
	}
	.code b {
		font-weight: 400;
		color: var(--pic-accent);
	}
	.lap {
		position: absolute;
		left: 0;
		bottom: 2px;
		display: flex;
		align-items: center;
		gap: 10px;
		color: var(--pic-faint);
		font-size: 12px;
	}
	.lap svg {
		flex: none;
	}
	.lid,
	.base {
		fill: var(--surface);
		stroke: var(--rule-strong);
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
	.cap {
		color: var(--pic-dim);
	}

	/* 2: the agent steps down into the window; any other agent, quietly. */
	.above {
		position: absolute;
		left: 0;
		right: 0;
		top: 0;
		height: var(--above);
		display: flex;
		align-items: flex-start;
		justify-content: center;
	}
	.ghost {
		width: 30px;
		height: 30px;
		border: 1px dashed var(--pic-faint);
		border-radius: 6px;
	}
	.fall {
		position: absolute;
		left: 50%;
		top: 32px;
		bottom: 0;
		width: 1px;
		background: var(--pic-accent);
		transform-origin: top center;
	}
	.fall::after {
		content: '';
		position: absolute;
		left: -3px;
		bottom: 1px;
		width: 5px;
		height: 5px;
		border: solid var(--pic-accent);
		border-width: 0 1px 1px 0;
		transform: rotate(45deg);
	}
	.s2 .win {
		height: 96px;
	}
	.inbody {
		display: flex;
		flex: 1;
		align-items: center;
		justify-content: center;
		gap: 10px;
		font-size: 15px;
		font-weight: 600;
	}
	.inmark {
		position: relative;
		z-index: 1;
		display: grid;
		place-items: center;
		width: 40px;
		height: 40px;
		border: 1px solid var(--pic-accent);
		border-radius: 6px;
		background: var(--surface);
		color: var(--pic-accent);
	}
	.inname,
	.s2 .who {
		transition: opacity 300ms ease;
	}
	.s2:global(.off) .inname,
	.s2:global(.off) .who {
		opacity: 0;
	}
	.s2:global(.off) .dot {
		background: var(--pic-faint);
	}
	.any {
		position: absolute;
		left: 0;
		right: 0;
		top: calc(var(--above) + 106px);
		display: flex;
		align-items: center;
		justify-content: center;
		gap: 8px;
		color: var(--pic-faint);
		font-size: 12px;
	}
	.any span {
		margin-right: 2px;
	}

	/* 3: the alert drops in; the window goes from asleep to awake. */
	.s3 .above {
		justify-content: flex-start;
		gap: 16px;
		padding-left: calc(50% - 15px);
	}
	.s3 .fall {
		top: 30px;
	}
	.trig {
		display: flex;
		align-items: center;
		gap: 7px;
		height: 30px;
		color: var(--pic-faint);
		font-size: 13px;
		transition: color 300ms ease;
	}
	.ic {
		display: grid;
		place-items: center;
		width: 28px;
		height: 28px;
		border: 1px solid var(--rule-strong);
		border-radius: 50%;
		background: var(--surface);
		color: var(--pic-dim);
		transition:
			border-color 300ms ease,
			color 300ms ease;
	}
	.trig.on {
		color: var(--pic-ink);
		font-weight: 600;
	}
	.trig.on .ic {
		border-color: var(--pic-accent);
		color: var(--pic-accent);
	}
	.s3:global(.off) .trig.on:not(:global(.hi)) {
		color: var(--pic-faint);
		font-weight: 400;
	}
	.s3:global(.off) .trig.on:not(:global(.hi)) .ic {
		border-color: var(--rule-strong);
		color: var(--pic-dim);
	}
	.s3 .win {
		border-color: var(--pic-accent);
	}
	.work {
		padding: 9px 12px 10px;
		line-height: 18px;
		color: var(--pic-dim);
		transition: opacity 300ms ease;
	}
	.s3:global(.off) .win {
		border-color: var(--rule);
	}
	.s3:global(.off) .head {
		color: var(--pic-dim);
	}
	.s3:global(.off) .who .m,
	.s3:global(.off) .state {
		display: none;
	}
	.s3:global(.off) .who .z {
		display: flex;
	}
	.s3:global(.off) .work {
		opacity: 0;
	}

	/* 4: the window's question on your phone, and what Yes got back. */
	.phone {
		position: absolute;
		left: 50px;
		top: 18px;
		width: 160px;
		bottom: 0;
		border: 1px solid var(--rule-strong);
		border-radius: 18px;
		background: var(--sunken);
	}
	.notch {
		position: absolute;
		left: 50%;
		top: 9px;
		width: 34px;
		height: 6px;
		margin-left: -17px;
		border-radius: 3px;
		background: var(--rule-strong);
	}
	.card {
		background: var(--surface);
	}
	.cbody {
		padding: 9px 12px 12px;
	}
	.ask {
		margin: 0 0 9px;
		font-size: 15px;
		line-height: 20px;
		font-weight: 600;
	}
	.btns {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 8px;
	}
	.btns > span {
		display: grid;
		place-items: center;
		height: 30px;
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
	.done {
		position: absolute;
		left: 50px;
		width: 160px;
		bottom: 12px;
		display: flex;
		align-items: center;
		justify-content: center;
		gap: 6px;
		font-size: 13px;
		font-weight: 600;
	}
	.done svg {
		fill: none;
		stroke: var(--color-emerald-500);
		stroke-width: 1.75;
		stroke-linecap: round;
		stroke-linejoin: round;
	}

	/* Below 1180px: two by two, each row its own stretch of the thread. */
	@media (max-width: 1179.98px) {
		.steps {
			grid-template-columns: repeat(2, minmax(0, 1fr));
			row-gap: 40px;
			max-width: 40rem;
			margin-inline: auto;
		}
		.s2 .thread {
			right: auto;
			width: 260px;
		}
	}

	/* Below 640px: still two by two, each drawing a narrow 148 wide. */
	@media (max-width: 639.98px) {
		.steps {
			--th: 50px;
			--above: 32px;
			--hh: 26px;
			column-gap: 0;
			row-gap: 24px;
			max-width: 22rem;
		}
		.ttl {
			margin-right: 12px;
			font-size: 15px;
			line-height: 20px;
		}
		.draw {
			width: 148px;
			height: 172px;
		}
		.s2 .thread,
		.s4 .thread {
			width: 148px;
		}
		.head {
			gap: 6px;
			padding: 0 8px;
		}
		.code {
			padding: 3px 8px 5px;
			line-height: 16px;
		}
		.lnum {
			width: 14px;
		}
		.lap {
			gap: 6px;
			width: 148px;
			white-space: nowrap;
		}
		.lap svg {
			width: 40px;
			height: 15px;
		}
		.was,
		.to {
			display: none;
		}
		.ghost {
			width: 22px;
			height: 22px;
		}
		.fall,
		.s3 .fall {
			top: 24px;
		}
		.s2 .win {
			height: 80px;
		}
		.inbody {
			gap: 8px;
			font-size: 13px;
		}
		.inmark {
			width: 30px;
			height: 30px;
		}
		.inmark :global(svg) {
			width: 16px;
			height: 16px;
		}
		.any {
			top: calc(var(--above) + 88px);
			flex-wrap: wrap;
			gap: 5px 7px;
		}
		.any span {
			flex-basis: 100%;
			text-align: center;
		}
		.s3 .above {
			gap: 8px;
			padding-left: calc(50% - 12px);
		}
		.trig {
			gap: 5px;
			height: 24px;
			font-size: 12px;
		}
		.ic {
			width: 24px;
			height: 24px;
		}
		.trig:not(.on) {
			display: none;
		}
		.work {
			padding: 4px 8px 6px;
		}
		.phone {
			left: 14px;
			width: 120px;
			top: 12px;
			border-radius: 16px;
		}
		.notch {
			top: 7px;
		}
		.cbody {
			padding: 6px 8px 8px;
		}
		.now {
			display: none;
		}
		.ask {
			margin-bottom: 6px;
			font-size: 13px;
			line-height: 17px;
		}
		.btns {
			gap: 6px;
		}
		.btns > span {
			height: 26px;
		}
		.done {
			left: 14px;
			width: 120px;
			bottom: 8px;
			gap: 4px;
			font-size: 12px;
		}
	}

	@media (prefers-reduced-motion: reduce) {
		.thread,
		.dot,
		.yes,
		.win,
		.trig,
		.ic,
		.work,
		.inname,
		.s2 .who {
			transition: none !important;
		}
	}
</style>
