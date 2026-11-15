<!--
  HeroOrbit: the hero's picture as one flow through one machine (hero
  loop, rounds 11-14, 2026-11-12). Left to right on one bold line: an
  alert rings, the agent's own machine (a screen on a stand, 2 GB, its
  quiet parts on a faint ring around it) works out what broke, your phone
  asks "Undo the 2 pm update?", and the result: fixed. Your laptop sleeps
  under the phone, shut, apart from the machine. Nothing connects one
  machine to another; there is only the one.

  The loop starts from the still frame, the story's end: the machine
  sleeps (dim, a crescent) and everything after it fades, the alert
  rings and a pulse runs in, the machine wakes and works, a pulse runs
  to the phone, you tap Yes, a pulse runs to the result. It moves by
  class changes (colour, opacity, scale in CSS) and the pulses'
  translate, timed by anime.js.

  Still frame (reduced motion, no script, and the loop's seam): every
  step lit, Yes chosen, the result showing, the laptop shut.

  At 1024px and up the flow runs across; below, it runs down.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { watchReducedMotion } from './inview';
	import { Picture } from './motion.svelte';
	import { agentMarks, type Mark } from '$lib/components/illustrations/marks';

	type Glyph = 'bell' | 'clock' | 'case' | 'code' | 'key' | 'moon' | 'tick';
	const chosen = agentMarks.find((m) => m.name === 'Claude Code')!;
	const five = agentMarks.slice(0, 5);

	let pic: HTMLDivElement;
	const picture = new Picture();

	onMount(() => {
		let dead = false;
		let run = 0;
		let tl: { pause(): unknown; play(): unknown } | null = null;
		const STATES = ['s-sleep', 's-ring', 's-wake', 's-ask', 's-yes'];

		function rest() {
			tl?.pause();
			tl = null;
			pic.querySelectorAll<HTMLElement>('.pulse').forEach((el) => el.removeAttribute('style'));
			pic.classList.remove(...STATES);
		}

		function start() {
			const me = ++run;
			import('animejs').then(({ createTimeline }) => {
				if (dead || me !== run) return;
				const on =
					(...c: string[]) =>
					() => {
						pic.classList.remove(...STATES);
						pic.classList.add(...c);
					};
				const across = () => matchMedia('(min-width: 1024px)').matches;
				type TL = ReturnType<typeof createTimeline>;
				// A pulse along one step of the flow: level at 1024px and up,
				// falling below.
				function pulse(t: TL, n: number, at: number) {
					const el = pic.querySelector<HTMLElement>(`.w${n} .pulse`);
					if (!el) return;
					t.add(
						el,
						across()
							? { x: ['-100%', '100%'], duration: 700, ease: 'inOutSine' }
							: { y: ['-100%', '100%'], duration: 600, ease: 'inOutSine' },
						at
					);
				}
				function cycle() {
					if (dead || me !== run) return;
					const t = createTimeline({ autoplay: false, onComplete: () => cycle() });
					// From the still frame, the story's end: the machine sleeps.
					t.call(on('s-sleep'), 800)
						// The alert rings and a pulse runs to the machine.
						.call(on('s-sleep', 's-ring'), 3000);
					pulse(t, 1, 3400);
					// It wakes and works out what broke.
					t.call(on('s-wake'), 4100);
					// It asks you on your phone, and you tap Yes.
					pulse(t, 2, 6000);
					t.call(on('s-ask'), 6500).call(on('s-ask', 's-yes'), 8000);
					// The fix: a pulse runs to the result, the still frame again.
					pulse(t, 3, 8500);
					t.call(on(), 9100).call(() => {}, 12000);
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

{#snippet glyph(kind: Glyph, size = 16)}
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
		{:else if kind === 'clock'}
			<circle cx="8" cy="8" r="5.75" /><path d="M8 4.75V8l2.25 1.5" />
		{:else if kind === 'case'}
			<rect x="2" y="5" width="12" height="8.5" rx="1.25" /><path d="M6 5V3.5h4V5M2 9h12" />
		{:else if kind === 'code'}
			<path d="M5.5 4.5 2 8l3.5 3.5M10.5 4.5 14 8l-3.5 3.5M9 3.5l-2 9" />
		{:else if kind === 'key'}
			<circle cx="5.5" cy="10.5" r="3" /><path d="M7.6 8.4 13 3M11 5l1.75 1.75M9.5 6.5l1.25 1.25" />
		{:else if kind === 'tick'}
			<path d="M3.5 8.5 6.5 11.5 12.5 5" />
		{:else}
			<path d="M10.5 2.5a5.75 5.75 0 1 0 3 9.5A5 5 0 0 1 10.5 2.5z" />
		{/if}
	</svg>
{/snippet}

<!-- One step of the flow: a bold line, an arrowhead, a pulse. -->
{#snippet step(n: number)}
	<span class="step w{n}"
		><span class="wire"><i class="pulse"><i></i></i></span><i class="arrow"></i></span
	>
{/snippet}

<!-- Your laptop, shut and asleep, apart from the machine. -->
{#snippet laptop(cls: string)}
	<span class="laptop {cls}"
		><svg viewBox="0 0 84 14" width="84" height="14" aria-hidden="true"
			><rect x="6" y="1" width="72" height="7" rx="2" /><rect
				x="1"
				y="8"
				width="82"
				height="4"
				rx="2"
			/></svg
		><span>{@render glyph('moon', 12)}your laptop, shut</span></span
	>
{/snippet}

<div class="orb-frame" use:picture.frame>
	<div
		class="orb"
		bind:this={pic}
		role="img"
		aria-label="An alert rings and wakes an agent on a machine of its own, 2 GB, built from the agent you choose, your code and your keys; a schedule or a new job posting can wake it too. It finds that checkout broke after the 2 pm update and asks you on your phone: undo the 2 pm update? You tap Yes, and it is fixed: checkout works again. All the while your laptop stays shut."
	>
		<div class="art" aria-hidden="true">
			<!-- What wakes it: the alert, ringing; a schedule or a new job too. -->
			<div class="wakes">
				<span class="alt">{@render glyph('clock', 14)}<span>schedule</span></span>
				<span class="alert"
					><span class="bell"
						><i class="halo"></i><i class="halo b"></i>{@render glyph('bell', 24)}</span
					><span class="bd">Alert</span></span
				>
				<span class="alt">{@render glyph('case', 14)}<span>new job</span></span>
			</div>

			{@render step(1)}

			<!-- The agent's own machine: a screen on a stand, its size on the chin. -->
			<div class="machine">
				<svg class="track"><ellipse cx="50%" cy="50%" rx="50%" ry="50%" /></svg>
				<span class="pick"
					>{#each five as m (m.name)}<span class="opt" class:on={m === chosen}
							>{@render markSvg(m, 12)}</span
						>{/each}</span
				>
				<span class="part code">{@render glyph('code', 14)}<span>code</span></span>
				<span class="part key">{@render glyph('key', 14)}<span>keys</span></span>

				<div class="box">
					<i class="ripple"></i>
					<div class="bezel">
						<div class="win">
							<span class="tbar">
								<span class="who">{@render markSvg(chosen, 14)}on call</span>
								<span class="state"
									><span class="awake"><i class="dot"></i>working</span><span class="asleep"
										>{@render glyph('moon', 12)}asleep</span
									></span
								>
							</span>
							<span class="term">
								<span class="ln l1">Checkout broke after the 2 pm update</span>
								<span class="diff">
									<span class="ln l3"><span class="bd">-</span> retries: 3</span>
									<span class="ln l4"><span class="bd">+</span> retries: 0</span>
								</span>
								<span class="ln l5"
									><span class="ask">Asking you before undoing it<i class="cur"></i></span><span
										class="done">Undone. Checkout works again</span
									></span
								>
							</span>
						</div>
						<span class="chin"><i class="led"></i><span>its own machine · 2 GB</span></span>
					</div>
					<i class="neck"></i><i class="foot"></i>
				</div>
			</div>

			{@render step(2)}

			<!-- You: the phone it asks you on, your laptop shut under it. -->
			<div class="you">
				<div class="phone">
					<i class="notch"></i>
					<span class="pwho">{@render markSvg(chosen, 12)}Claude Code asks</span>
					<span class="q">Undo the 2 pm update?</span>
					<span class="yn"><span class="yes">Yes</span><span class="no">No</span></span>
				</div>
				{@render laptop('')}
			</div>

			{@render step(3)}

			<!-- The end: fixed. -->
			<div class="result">
				<span class="tick">{@render glyph('tick', 22)}</span>
				<span class="bd">Fixed</span>
				<span>checkout works again</span>
			</div>
			{@render laptop('m')}
		</div>
	</div>
</div>

<style>
	.orb {
		--rx: 236px;
		--ry: 160px;
		--mw: 330px;
		position: relative;
		height: 360px;
		color: var(--pic-ink);
		font-family: var(--font-sans);
		font-size: 13px;
		line-height: 18px;
	}
	.art {
		display: flex;
		align-items: center;
		height: 100%;
	}

	/* ---------- What wakes it ---------- */
	.wakes {
		display: flex;
		flex: none;
		flex-direction: column;
		align-items: center;
		gap: 22px;
		width: 96px;
	}
	.alt {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 4px;
		color: var(--pic-faint);
		font-size: 12px;
		line-height: 16px;
	}
	.alert {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 16px;
	}
	.alert .bd {
		font-size: 14px;
		font-weight: 600;
		line-height: 18px;
		transition: color 240ms ease;
	}
	.bell {
		position: relative;
		display: grid;
		place-items: center;
		width: 60px;
		height: 60px;
		border: 1px solid var(--pic-accent);
		border-radius: 50%;
		background: var(--pic-accent);
		color: var(--surface);
		transition:
			background-color 240ms ease,
			color 240ms ease;
	}
	/* Ringing: two rings stand round the bell in the still frame and go
	   out from it in the loop. */
	.halo {
		position: absolute;
		inset: -1px;
		border: 1px solid var(--pic-accent);
		border-radius: 50%;
		opacity: 0.5;
		transform: scale(1.25);
	}
	.halo.b {
		opacity: 0.22;
		transform: scale(1.5);
	}
	.orb:global(.s-sleep:not(.s-ring)) .bell {
		background: var(--surface);
		color: var(--pic-dim);
	}
	.orb:global(.s-sleep:not(.s-ring)) .alert .bd {
		color: var(--pic-dim);
	}
	.orb:global(:is(.s-sleep, .s-wake, .s-ask)) .halo {
		opacity: 0;
	}
	.orb:global(.s-ring) .halo {
		animation: ring 1100ms cubic-bezier(0.2, 0.6, 0.3, 1) 2 both;
	}
	.orb:global(.s-ring) .halo.b {
		animation-delay: 300ms;
	}
	@keyframes ring {
		from {
			opacity: 0.9;
			transform: scale(1);
		}
		to {
			opacity: 0;
			transform: scale(1.8);
		}
	}

	/* ---------- A step: a bold line with an arrowhead ---------- */
	.step {
		position: relative;
		flex: 1;
		min-width: 40px;
		height: 12px;
		margin: 0 10px;
	}
	.wire {
		position: absolute;
		top: 5px;
		right: 3px;
		left: 0;
		height: 2px;
		overflow: hidden;
		border-radius: 1px;
		background: var(--pic-accent);
		transition: background-color 300ms ease;
	}
	.arrow {
		position: absolute;
		top: 1px;
		right: 1px;
		width: 10px;
		height: 10px;
		border-top: 2px solid var(--pic-accent);
		border-right: 2px solid var(--pic-accent);
		transform: rotate(45deg);
		transition: border-color 300ms ease;
	}
	.pulse {
		position: absolute;
		inset: 0;
		transform: translateX(-100%);
	}
	.pulse > i {
		position: absolute;
		top: 0;
		right: 0;
		width: 36px;
		height: 2px;
		background: var(--pic-ink);
	}
	/* A step not yet reached: a quiet rule. */
	.orb:global(.s-sleep:not(.s-ring)) .w1 .wire,
	.orb:global(:is(.s-sleep, .s-wake)) :is(.w2, .w3) .wire,
	.orb:global(.s-ask) .w3 .wire {
		background: var(--rule-strong);
	}
	.orb:global(.s-sleep:not(.s-ring)) .w1 .arrow,
	.orb:global(:is(.s-sleep, .s-wake)) :is(.w2, .w3) .arrow,
	.orb:global(.s-ask) .w3 .arrow {
		border-color: var(--rule-strong);
	}

	/* ---------- The machine ---------- */
	.machine {
		position: relative;
		flex: none;
		width: var(--mw);
	}
	.track {
		position: absolute;
		top: 50%;
		left: 50%;
		width: calc(var(--rx) * 2);
		height: calc(var(--ry) * 2);
		overflow: visible;
		transform: translate(-50%, -50%);
	}
	.track ellipse {
		fill: none;
		stroke: var(--pic-faint);
		stroke-dasharray: 2 5;
		opacity: 0.55;
	}
	/* Its parts, quiet on the ring: the agent you chose, code, keys. */
	.pick {
		position: absolute;
		top: calc(50% - var(--ry));
		left: 50%;
		display: flex;
		gap: 2px;
		padding: 3px;
		border: 1px solid var(--rule);
		border-radius: 14px;
		background: var(--surface);
		transform: translate(-50%, -50%);
	}
	.opt {
		display: grid;
		place-items: center;
		width: 22px;
		height: 22px;
		border: 1px solid transparent;
		border-radius: 50%;
		color: var(--pic-faint);
	}
	.opt.on {
		border-color: var(--rule-strong);
		color: var(--pic-dim);
	}
	.part {
		position: absolute;
		top: calc(50% + var(--ry) * 0.72);
		display: flex;
		align-items: center;
		gap: 6px;
		padding: 3px 9px 3px 7px;
		border: 1px solid var(--rule);
		border-radius: 12px;
		background: var(--surface);
		color: var(--pic-faint);
		font-size: 12px;
		line-height: 16px;
		transform: translate(-50%, -50%);
	}
	.part.code {
		left: calc(50% - var(--rx) * 0.69);
	}
	.part.key {
		left: calc(50% + var(--rx) * 0.69);
	}

	.box {
		position: relative;
		display: flex;
		flex-direction: column;
		align-items: center;
	}
	.bezel {
		position: relative;
		width: 100%;
		padding: 7px 7px 0;
		border: 1px solid var(--rule-strong);
		border-radius: 10px;
		background: var(--sunken);
		transition: border-color 400ms ease;
	}
	.win {
		display: flex;
		flex-direction: column;
		border: 1px solid var(--pic-accent);
		border-radius: 6px;
		background: var(--surface);
		transition: border-color 400ms ease;
	}
	.chin {
		display: flex;
		align-items: center;
		justify-content: center;
		gap: 8px;
		height: 26px;
		color: var(--pic-dim);
		font-size: 12px;
		line-height: 16px;
	}
	.led {
		width: 6px;
		height: 6px;
		border-radius: 50%;
		background: var(--color-emerald-500);
		transition: background-color 300ms ease;
	}
	.neck {
		width: 40px;
		height: 14px;
		border-inline: 1px solid var(--rule-strong);
		background: var(--sunken);
	}
	.foot {
		width: 110px;
		height: 5px;
		border: 1px solid var(--rule-strong);
		border-radius: 3px;
		background: var(--sunken);
	}
	.tbar {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 7px 12px;
		border-bottom: 1px solid var(--rule);
		font-size: 12px;
		line-height: 16px;
	}
	.who {
		display: flex;
		align-items: center;
		gap: 8px;
		transition: color 400ms ease;
	}
	.state {
		display: grid;
		color: var(--pic-dim);
	}
	.state > span,
	.l5 > span {
		display: flex;
		grid-area: 1 / 1;
		align-items: center;
		gap: 6px;
		transition: opacity 300ms ease;
	}
	.state > span {
		justify-content: flex-end;
	}
	.asleep,
	.ask {
		opacity: 0;
	}
	.dot {
		width: 6px;
		height: 6px;
		border-radius: 50%;
		background: var(--color-emerald-500);
	}
	.term {
		display: flex;
		flex-direction: column;
		padding: 10px 12px 12px;
		white-space: nowrap;
	}
	.ln {
		transition: opacity 300ms ease;
	}
	.l1 {
		font-size: 14px;
		font-weight: 500;
		line-height: 20px;
	}
	/* The developer's detail: the change it found, as a diff. */
	.diff {
		display: flex;
		flex-direction: column;
		margin: 6px -12px 8px;
		padding: 3px 12px;
		background: var(--sunken);
		color: var(--pic-dim);
		font-family: var(--font-mono);
		font-size: 12px;
		line-height: 17px;
	}
	.diff .bd {
		display: inline-block;
		width: 2ch;
		color: var(--pic-faint);
		font-weight: 400;
	}
	.l5 {
		display: grid;
		font-size: 13px;
		line-height: 18px;
	}
	.cur {
		display: inline-block;
		width: 7px;
		height: 13px;
		margin-left: -2px;
		background: var(--pic-accent);
		animation: blink 1s steps(1) infinite;
	}
	@keyframes blink {
		50% {
			opacity: 0;
		}
	}
	.orb:global(:is(.s-wake, .s-ask)) .ask {
		opacity: 1;
	}
	.orb:global(:is(.s-wake, .s-ask)) .done {
		opacity: 0;
	}
	/* The machine wakes: one hairline goes out from the screen and fades. */
	.ripple {
		position: absolute;
		top: 0;
		left: 0;
		width: 100%;
		height: calc(100% - 17px);
		border: 1px solid var(--pic-accent);
		border-radius: 10px;
		opacity: 0;
	}
	.orb:global(.s-wake) .ripple {
		animation: wake 1200ms cubic-bezier(0.2, 0.6, 0.3, 1) both;
	}
	@keyframes wake {
		from {
			opacity: 1;
			transform: scale(1);
		}
		to {
			opacity: 0;
			transform: scale(1.1, 1.16);
		}
	}
	/* Asleep: dim, its screen frozen, a crescent for the running dot. */
	.orb:global(.s-sleep) .win {
		border-color: var(--rule-strong);
	}
	.orb:global(.s-sleep) .who {
		opacity: 0.35;
	}
	.orb:global(.s-sleep) .awake {
		opacity: 0;
	}
	.orb:global(.s-sleep) .asleep {
		opacity: 1;
	}
	.orb:global(.s-sleep) .ln {
		opacity: 0.22;
	}
	.orb:global(.s-sleep) .led {
		background: var(--rule-strong);
	}
	/* Awake and working: the lines come in one by one. */
	.orb:global(.s-wake) .ln {
		animation: line 300ms ease both;
	}
	.orb:global(.s-wake) .l3 {
		animation-delay: 700ms;
	}
	.orb:global(.s-wake) .l4 {
		animation-delay: 900ms;
	}
	.orb:global(.s-wake) .l5 {
		animation-delay: 1500ms;
	}
	@keyframes line {
		from {
			opacity: 0;
		}
	}

	/* ---------- You: the phone, the laptop shut ---------- */
	.you {
		position: relative;
		flex: none;
	}
	.phone {
		display: flex;
		flex-direction: column;
		gap: 8px;
		width: 212px;
		padding: 10px 14px 14px;
		border: 1px solid var(--pic-accent);
		border-radius: 18px;
		background: var(--surface);
		transition:
			opacity 300ms ease,
			transform 300ms ease;
	}
	.orb:global(:is(.s-sleep, .s-wake)) .phone {
		opacity: 0;
		transform: translateY(8px);
	}
	.notch {
		align-self: center;
		width: 28px;
		height: 4px;
		border-radius: 2px;
		background: var(--rule-strong);
	}
	.pwho {
		display: flex;
		align-items: center;
		gap: 6px;
		color: var(--pic-dim);
		font-size: 12px;
		line-height: 16px;
	}
	.q {
		font-size: 15px;
		font-weight: 600;
		line-height: 20px;
	}
	.yn {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 6px;
		margin-top: 2px;
	}
	.yn > span {
		padding: 5px 0;
		border: 1px solid var(--rule-strong);
		border-radius: 6px;
		font-size: 13px;
		line-height: 18px;
		text-align: center;
		transition:
			background-color 200ms ease,
			color 200ms ease;
	}
	.yes {
		border-color: var(--pic-accent) !important;
		background: var(--pic-accent);
		color: var(--surface);
		font-weight: 600;
	}
	.orb:global(.s-ask:not(.s-yes)) .yes {
		background: transparent;
		color: var(--pic-ink);
	}
	.no {
		color: var(--pic-dim);
	}
	.laptop {
		position: absolute;
		top: calc(100% + 28px);
		left: 50%;
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 6px;
		color: var(--pic-faint);
		font-size: 12px;
		line-height: 16px;
		white-space: nowrap;
		transform: translateX(-50%);
	}
	.laptop svg {
		fill: var(--surface);
		stroke: var(--rule-strong);
	}
	.laptop > span {
		display: flex;
		align-items: center;
		gap: 6px;
	}
	.laptop.m {
		display: none;
	}

	/* ---------- The end: fixed ---------- */
	.result {
		display: flex;
		flex: none;
		flex-direction: column;
		align-items: center;
		gap: 6px;
		width: 132px;
		text-align: center;
		transition:
			opacity 300ms ease,
			transform 300ms ease;
	}
	.tick {
		display: grid;
		place-items: center;
		width: 48px;
		height: 48px;
		margin-bottom: 4px;
		border: 1px solid var(--color-emerald-500);
		border-radius: 50%;
		color: var(--color-emerald-500);
	}
	.result .bd {
		font-size: 16px;
		font-weight: 600;
		line-height: 20px;
	}
	.result > span:last-child {
		color: var(--pic-dim);
		font-size: 12px;
		line-height: 16px;
	}
	.orb:global(:is(.s-sleep, .s-wake, .s-ask)) .result {
		opacity: 0;
		transform: translateX(-8px);
	}

	/* ---------- 1024 to 1279px: a tighter row ---------- */
	@media (max-width: 1279.98px) {
		.orb {
			--rx: 200px;
			--mw: 300px;
		}
		.l1 {
			font-size: 13px;
		}
		.phone {
			width: 180px;
		}
		.wakes {
			width: 80px;
		}
		.result {
			width: 128px;
		}
		.step {
			margin: 0 6px;
		}
	}

	/* ---------- Below 1024px: the flow runs down ---------- */
	@media (max-width: 1023.98px) {
		.orb {
			height: auto;
			max-width: 26rem;
			margin-inline: auto;
		}
		.art {
			flex-direction: column;
			height: auto;
		}
		.wakes {
			display: grid;
			grid-template-columns: 1fr auto 1fr;
			align-items: center;
			justify-items: center;
			gap: 8px;
			width: 100%;
		}
		.step {
			flex: none;
			width: 12px;
			height: 40px;
			min-width: 0;
			margin: 10px 0;
		}
		.wire {
			top: 0;
			right: auto;
			bottom: 3px;
			left: 5px;
			width: 2px;
			height: auto;
		}
		.arrow {
			top: auto;
			right: auto;
			bottom: 1px;
			left: 1px;
			transform: rotate(135deg);
		}
		.pulse {
			transform: translateY(-100%);
		}
		.pulse > i {
			top: auto;
			bottom: 0;
			width: 2px;
			height: 16px;
		}
		.track {
			display: none;
		}
		.pick,
		.part {
			position: static;
			transform: none;
		}
		/* Its parts in one quiet row under the stand. */
		.machine {
			display: grid;
			grid-template-columns: 1fr auto 1fr;
			align-items: center;
			gap: 12px 10px;
			width: 100%;
			max-width: 320px;
		}
		.box {
			grid-column: 1 / -1;
		}
		.part.code {
			grid-row: 2;
			grid-column: 1;
			justify-self: end;
		}
		.pick {
			grid-row: 2;
			grid-column: 2;
		}
		.part.key {
			grid-row: 2;
			grid-column: 3;
			justify-self: start;
		}
		.phone {
			width: 230px;
		}
		.you {
			display: flex;
			flex-direction: column;
			align-items: center;
		}
		.you .laptop {
			display: none;
		}
		.result {
			width: auto;
		}
		.orb:global(:is(.s-sleep, .s-wake, .s-ask)) .result {
			transform: translateY(-8px);
		}
		.laptop.m {
			position: static;
			display: flex;
			margin-top: 32px;
			transform: none;
		}
	}
	@media (prefers-reduced-motion: reduce) {
		.bell,
		.alert .bd,
		.wire,
		.arrow,
		.bezel,
		.win,
		.led,
		.who,
		.state > span,
		.l5 > span,
		.ln,
		.phone,
		.yn > span,
		.result {
			transition: none !important;
		}
		.cur,
		.halo,
		.ripple {
			animation: none !important;
		}
	}
</style>
