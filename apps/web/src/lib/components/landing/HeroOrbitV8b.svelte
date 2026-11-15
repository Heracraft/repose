<!--
  HeroOrbit: the hero's picture as the agents you build from one kit (hero
  loop, rounds 11-16, 2026-11-12). Three machines stand side by side, each
  its own small computer of its own size, each a plainly named agent with
  its own trigger over it: your dev environment (16 GB, every day at
  8:00) and job search (8 GB, a new posting) asleep, dim, a crescent each;
  the payments agent (2 GB) woken by its Stripe webhook. Under them the
  kit they are built from: the agents you can put in, your code, your
  keys, what wakes it; a key from the kit sits on the payments agent,
  its slot in the kit empty. The woken one asks you on your phone, and
  the end is plain: paid, $420 recovered. Your laptop stays shut under
  the phone. Nothing joins one machine to another.

  The loop holds the still frame, the whole story, for 4.5s, then puts it
  to sleep one step at a time (the result, the phone, the payments agent;
  each ghosts, none goes blank): the webhook fires and a pulse runs down,
  the agent wakes and works, a pulse runs to the phone, you tap Yes, a
  pulse runs to the result. It moves by class changes (colour, opacity,
  scale in CSS) and the pulses' translate, timed by anime.js.

  Still frame (reduced motion, no script, and the loop's seam): two
  machines asleep, the payments agent awake and asking, Yes chosen, the
  result showing, the laptop closed.

  At 1024px and up the machines stand in a row with the flow to the
  right; below, the woken one's flow runs down, then the two asleep side
  by side, then the kit.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { watchReducedMotion } from './inview';
	import { Picture } from './motion.svelte';
	import { agentMarks, type Mark } from '$lib/components/illustrations/marks';

	type Glyph = 'bolt' | 'clock' | 'case' | 'code' | 'key' | 'moon' | 'tick';
	type Machine = {
		cls: string;
		name: string;
		m: Mark;
		gb: string;
		wake: { glyph: Glyph; text: string };
		parts: Glyph[];
		lines: string[];
	};
	const mark = (n: string) => agentMarks.find((m) => m.name === n)!;

	// Left to right: two asleep, then the one the webhook wakes.
	const dev: Machine = {
		cls: 'ma',
		name: 'your dev environment',
		m: mark('opencode'),
		gb: '16 GB',
		wake: { glyph: 'clock', text: 'every day, 8:00' },
		parts: [],
		lines: []
	};
	const jobs: Machine = {
		cls: 'mc',
		name: 'job search',
		m: mark('pi'),
		gb: '8 GB',
		wake: { glyph: 'case', text: 'a new posting' },
		parts: [],
		lines: []
	};
	const pay: Machine = {
		cls: 'mb',
		name: 'payments agent',
		m: mark('Claude Code'),
		gb: '2 GB',
		wake: { glyph: 'bolt', text: 'Stripe webhook' },
		parts: [],
		lines: ['A $420 payment failed']
	};

	let pic: HTMLDivElement;
	const picture = new Picture();

	onMount(() => {
		let dead = false;
		let run = 0;
		let tl: { pause(): unknown; play(): unknown } | null = null;
		const STATES = ['s-r', 's-p', 's-sleep', 's-ring', 's-wake', 's-ask', 's-yes'];

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
				// A pulse along one step: the webhook's runs down; the others
				// run across at 1024px and up, down below.
				function pulse(t: TL, n: number, at: number) {
					const el = pic.querySelector<HTMLElement>(`.w${n} .pulse`);
					if (!el) return;
					t.add(
						el,
						n > 1 && across()
							? { x: ['-100%', '100%'], duration: 700, ease: 'inOutSine' }
							: { y: ['-100%', '100%'], duration: 500, ease: 'inOutSine' },
						at
					);
				}
				function cycle() {
					if (dead || me !== run) return;
					const t = createTimeline({ autoplay: false, onComplete: () => cycle() });
					// Hold the whole story, then put it to sleep a step at a time:
					// the result, the phone, the payments agent.
					t.call(on('s-r'), 4500)
						.call(on('s-r', 's-p'), 5200)
						.call(on('s-r', 's-p', 's-sleep'), 5900)
						// The webhook fires and a pulse runs down into its machine.
						.call(on('s-r', 's-p', 's-sleep', 's-ring'), 7900);
					pulse(t, 1, 8300);
					// It wakes and works.
					t.call(on('s-r', 's-p', 's-wake'), 8800);
					// It asks you on your phone, and you tap Yes.
					pulse(t, 2, 10600);
					t.call(on('s-r', 's-ask'), 11100).call(on('s-r', 's-ask', 's-yes'), 12500);
					// Done: a pulse runs to the result, the still frame again.
					pulse(t, 3, 13000);
					t.call(on(), 13600).call(() => {}, 14400);
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
		{#if kind === 'bolt'}
			<path d="M9 1.75 3.75 9H8l-1 5.25L12.25 7H8z" />
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

<!-- One agent: its trigger over it, then its own computer, a screen on a
     stand with its name, its state and what it is doing; its size and
     the parts it is built from on the chin. -->
{#snippet machine(k: Machine, woken: boolean)}
	<div class="mach {k.cls}" class:woken>
		<span class="trig">{@render glyph(k.wake.glyph, 14)}<span>{k.wake.text}</span></span>
		{#if woken}{@render step(1)}{:else}<i class="drop"></i>{/if}
		<div class="box">
			<i class="ripple"></i>
			<div class="bezel">
				<div class="win">
					<span class="tbar">
						<span class="who">{@render markSvg(k.m, 13)}{k.name}</span>
						{#if woken}
							<span class="state"
								><span class="awake"><i class="dot"></i></span><span class="asleep"
									>{@render glyph('moon', 12)}</span
								></span
							>
						{:else}
							<span class="zz">{@render glyph('moon', 12)}</span>
						{/if}
					</span>
					<span class="term">
						{#each k.lines as l (l)}<span class="ln">{l}</span>{/each}
						{#if woken}<span class="ln l5">Asking you first<i class="cur"></i></span>{/if}
					</span>
				</div>
				<span class="chin"
					><i class="led"></i><span class="kit-on"
						>{#each k.parts as g (g)}{@render glyph(g, 12)}{/each}</span
					></span
				>
			</div>
			<i class="neck"></i><i class="foot"></i>
		</div>
	</div>
{/snippet}

<!-- Your laptop, closed and dark. -->
{#snippet laptop(cls: string)}
	<span class="laptop {cls}"
		><svg viewBox="0 0 104 62" width="88" height="52" aria-hidden="true"
			><rect class="lid" x="0.5" y="0.5" width="103" height="54" rx="6" /><rect
				class="lip"
				x="8"
				y="54.5"
				width="88"
				height="6"
				rx="2.5"
			/><path
				class="moon"
				transform="translate(43 18) scale(1.1)"
				d="M10.5 2.5a5.75 5.75 0 1 0 3 9.5A5 5 0 0 1 10.5 2.5z"
			/></svg
		><span>your laptop, shut</span></span
	>
{/snippet}

<div class="orb-frame" use:picture.frame>
	<div
		class="orb"
		bind:this={pic}
		role="img"
		aria-label="Three agents you built, each on a small computer of its own, each with its own trigger: your dev environment (16 GB, wakes every day at 8:00) and job search (8 GB, wakes on a new posting) are asleep; the payments agent (2 GB) has just been woken by a Stripe webhook. Under them, the kit they are all built from: the agent you choose (Claude Code, Codex, opencode, Gemini CLI or pi), your code, your keys, and what wakes it; a key from the kit has just been put on the payments agent. A $420 payment failed; the agent asks you on your phone: retry the $420 payment? You tap Yes, and it is paid: $420 recovered. Your laptop stays shut. Nothing connects the machines to each other."
	>
		<div class="art" aria-hidden="true">
			<div class="fleet">
				<div class="row">
					{@render machine(dev, false)}
					{@render machine(jobs, false)}
					{@render machine(pay, true)}
				</div>
			</div>

			{@render step(2)}

			<!-- You: the phone the woken one asks you on, your laptop shut. -->
			<div class="you">
				<div class="phone">
					<i class="notch"></i>
					<span class="pwho">{@render markSvg(pay.m, 12)}</span>
					<span class="q">Retry the $420 payment?</span>
					<span class="yn"
						><span class="yes">{@render glyph('tick', 14)}Yes</span><span class="no">No</span></span
					>
				</div>
				{@render laptop('')}
			</div>

			{@render step(3)}

			<!-- The end: paid. -->
			<div class="result">
				<span class="tick">{@render glyph('tick', 22)}</span>
				<span class="bd">Paid</span>
			</div>
			{@render laptop('m')}
		</div>
	</div>
</div>

<style>
	.orb {
		--flow-y: 118px;
		position: relative;
		height: 360px;
		color: var(--pic-ink);
		font-family: var(--font-sans);
		font-size: 13px;
		line-height: 18px;
	}
	.art {
		display: flex;
		align-items: flex-start;
		justify-content: center;
		height: 100%;
		padding-top: 32px;
	}

	/* ---------- The machines ---------- */
	.fleet {
		display: flex;
		flex: none;
		flex-direction: column;
		align-items: flex-start;
	}
	.row {
		display: flex;
		align-items: flex-end;
		gap: 28px;
	}
	.mach {
		display: flex;
		flex-direction: column;
		align-items: center;
	}
	.ma {
		width: 214px;
	}
	.mc {
		width: 178px;
	}
	.mb {
		width: 236px;
	}
	.trig {
		display: flex;
		align-items: center;
		gap: 6px;
		padding: 4px 10px 4px 8px;
		border: 1px solid var(--rule-strong);
		border-radius: 14px;
		background: var(--surface);
		color: var(--pic-dim);
		font-size: 12px;
		line-height: 16px;
		white-space: nowrap;
		transition:
			background-color 240ms ease,
			color 240ms ease,
			border-color 240ms ease;
	}
	.drop {
		width: 1px;
		height: 18px;
		background: var(--rule-strong);
	}
	/* The woken one's trigger: lit, firing. */
	.woken .trig {
		border-color: var(--pic-accent);
		background: var(--pic-accent);
		color: var(--surface);
		font-weight: 500;
	}
	.orb:global(.s-sleep:not(.s-ring)) .woken .trig {
		border-color: var(--rule-strong);
		background: var(--surface);
		color: var(--pic-dim);
		font-weight: 400;
	}

	.box {
		position: relative;
		display: flex;
		flex-direction: column;
		align-items: center;
		width: 100%;
	}
	.bezel {
		position: relative;
		width: 100%;
		padding: 6px 6px 0;
		border: 1px solid var(--rule-strong);
		border-radius: 10px;
		background: var(--sunken);
	}
	.win {
		display: flex;
		flex-direction: column;
		border: 1px solid var(--rule-strong);
		border-radius: 6px;
		background: var(--surface);
		transition: border-color 400ms ease;
	}
	.woken .win {
		border-color: var(--pic-accent);
	}
	.tbar {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 8px;
		padding: 6px 10px;
		border-bottom: 1px solid var(--rule);
		font-size: 12px;
		line-height: 16px;
		white-space: nowrap;
	}
	.who {
		display: flex;
		align-items: center;
		gap: 7px;
		font-weight: 500;
		transition: opacity 400ms ease;
	}
	.state {
		display: grid;
		color: var(--pic-dim);
	}
	.state > span {
		display: flex;
		grid-area: 1 / 1;
		align-items: center;
		justify-content: flex-end;
		gap: 6px;
		transition: opacity 300ms ease;
	}
	.asleep {
		opacity: 0;
	}
	.dot {
		width: 6px;
		height: 6px;
		border-radius: 50%;
		background: var(--color-emerald-500);
	}
	.zz {
		display: flex;
		color: var(--pic-dim);
	}
	.term {
		display: flex;
		flex-direction: column;
		padding: 8px 10px 10px;
		color: var(--pic-dim);
		font-size: 13px;
		line-height: 19px;
		white-space: nowrap;
	}
	.woken .term {
		color: var(--pic-ink);
	}
	.ln {
		transition: opacity 300ms ease;
	}
	.woken .ln:first-child {
		font-weight: 500;
	}
	.l5 {
		color: var(--pic-dim);
	}
	.cur {
		display: inline-block;
		width: 7px;
		height: 13px;
		margin-left: 4px;
		vertical-align: -2px;
		background: var(--pic-accent);
		animation: blink 1s steps(1) infinite;
	}
	@keyframes blink {
		50% {
			opacity: 0;
		}
	}
	.chin {
		display: flex;
		align-items: center;
		gap: 7px;
		height: 24px;
		padding: 0 6px;
		color: var(--pic-dim);
		font-size: 12px;
		line-height: 16px;
	}
	.kit-on {
		display: flex;
		gap: 6px;
		margin-left: auto;
		color: var(--pic-faint);
	}
	.led {
		width: 6px;
		height: 6px;
		border-radius: 50%;
		background: var(--rule-strong);
		transition: background-color 300ms ease;
	}
	.woken .led {
		background: var(--color-emerald-500);
	}
	.neck {
		width: 34px;
		height: 12px;
		border-inline: 1px solid var(--rule-strong);
		background: var(--sunken);
	}
	.foot {
		width: 44%;
		height: 5px;
		border: 1px solid var(--rule-strong);
		border-radius: 3px;
		background: var(--sunken);
	}
	/* Asleep: a dim, frozen screen. */
	.mach:not(.woken) .win {
		opacity: 0.7;
	}
	.mach:not(.woken) .term {
		color: var(--pic-faint);
	}
	/* The key from the kit, just put on the woken machine. */
	.attach {
		position: absolute;
		right: 24px;
		bottom: 6px;
		display: flex;
		align-items: center;
		gap: 6px;
		padding: 4px 10px 4px 8px;
		border: 1px solid var(--pic-accent);
		border-radius: 14px;
		background: var(--surface);
		font-size: 12px;
		line-height: 16px;
		white-space: nowrap;
		transform: translateX(50%);
	}
	/* Its trail down from the slot in the kit it was taken from. */
	.attach::after {
		content: '';
		position: absolute;
		top: 100%;
		left: 50%;
		height: 40px;
		border-left: 1px dashed var(--pic-accent);
	}

	/* The woken machine wakes: one hairline goes out from it and fades. */
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
	.orb:global(.s-sleep) .woken .win {
		border-color: var(--rule-strong);
	}
	.orb:global(.s-sleep) .woken .who {
		opacity: 0.5;
	}
	.orb:global(.s-sleep) .woken .awake {
		opacity: 0;
	}
	.orb:global(.s-sleep) .woken .asleep {
		opacity: 1;
	}
	/* The two states cross without overlapping: the one going out first. */
	.orb:global(.s-sleep) .woken .asleep,
	.orb:global(:not(.s-sleep)) .woken .awake {
		transition-delay: 300ms;
	}
	.orb:global(.s-sleep) .woken .ln {
		opacity: 0.25;
	}
	.orb:global(.s-sleep) .woken .led {
		background: var(--rule-strong);
	}
	.orb:global(.s-wake) .woken .ln {
		animation: line 300ms ease both;
	}
	.orb:global(.s-wake) .woken .ln:nth-child(2) {
		animation-delay: 600ms;
	}
	.orb:global(.s-wake) .woken .l5 {
		animation-delay: 1300ms;
	}
	@keyframes line {
		from {
			opacity: 0;
		}
	}

	/* ---------- The kit ---------- */
	.kit {
		display: flex;
		align-self: flex-end;
		align-items: center;
		gap: 12px;
		margin-top: 30px;
		padding: 6px 8px 6px 14px;
		border: 1px solid var(--rule-strong);
		border-radius: 10px;
		background: var(--surface);
	}
	.kname {
		color: var(--pic-dim);
		font-size: 12px;
		line-height: 16px;
		white-space: nowrap;
	}
	.slots {
		display: flex;
		align-items: center;
		gap: 4px;
	}
	.slot {
		display: grid;
		place-items: center;
		width: 30px;
		height: 30px;
		border: 1px solid var(--rule);
		border-radius: 6px;
		background: var(--sunken);
		color: var(--pic-dim);
	}
	.slot.empty {
		border: 1px dashed var(--pic-accent);
		background: none;
	}
	.sep {
		width: 1px;
		height: 18px;
		margin: 0 4px;
		background: var(--rule-strong);
	}

	/* ---------- A step: a bold line with an arrowhead ---------- */
	.step {
		position: relative;
		flex: 1;
		min-width: 36px;
		max-width: 64px;
		height: 12px;
		margin: calc(var(--flow-y) - 6px) 10px 0;
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
		width: 30px;
		height: 2px;
		background: var(--pic-ink);
	}
	/* The webhook's step falls into its machine. */
	.w1,
	.w1.step {
		flex: none;
		width: 12px;
		height: 18px;
		min-width: 0;
		margin: 0;
	}
	.w1 .wire {
		top: 0;
		right: auto;
		bottom: 3px;
		left: 5px;
		width: 2px;
		height: auto;
	}
	.w1 .arrow {
		top: auto;
		right: auto;
		bottom: 1px;
		left: 1px;
		width: 8px;
		height: 8px;
		transform: rotate(135deg);
	}
	.w1 .pulse {
		transform: translateY(-100%);
	}
	.w1 .pulse > i {
		top: auto;
		bottom: 0;
		width: 2px;
		height: 10px;
	}
	/* A step not reached yet: a quiet rule. */
	.orb:global(.s-sleep:not(.s-ring)) .w1 .wire,
	.orb:global(.s-p) .w2 .wire,
	.orb:global(.s-r) .w3 .wire {
		background: var(--rule-strong);
	}
	.orb:global(.s-sleep:not(.s-ring)) .w1 .arrow,
	.orb:global(.s-p) .w2 .arrow,
	.orb:global(.s-r) .w3 .arrow {
		border-color: var(--rule-strong);
	}

	/* ---------- You: the phone, the laptop shut ---------- */
	.you {
		position: relative;
		flex: none;
		margin-top: calc(var(--flow-y) - 72px);
	}
	.phone {
		display: flex;
		flex-direction: column;
		gap: 8px;
		width: 214px;
		padding: 10px 14px 14px;
		border: 1px solid var(--pic-accent);
		border-radius: 18px;
		background: var(--surface);
		transition: opacity 300ms ease;
	}
	/* A step not reached yet ghosts; it never goes blank. */
	.orb:global(.s-p) .phone {
		opacity: 0.28;
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
		display: flex;
		align-items: center;
		justify-content: center;
		gap: 4px;
		border-color: var(--pic-accent) !important;
		background: var(--pic-accent);
		color: var(--surface);
		font-weight: 600;
	}
	.orb:global(.s-ask:not(.s-yes)) .yes {
		background: transparent;
		color: var(--pic-ink);
	}
	.orb:global(.s-ask:not(.s-yes)) .yes .gl {
		display: none;
	}
	.no {
		color: var(--pic-dim);
	}
	.laptop {
		position: absolute;
		top: calc(100% + 26px);
		left: 50%;
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 6px;
		color: var(--pic-dim);
		font-size: 12px;
		line-height: 16px;
		white-space: nowrap;
		transform: translateX(-50%);
	}
	/* Closed and dark: a lid seen from above, its front lip, a crescent. */
	.lid {
		fill: var(--sunken);
		stroke: var(--rule-strong);
	}
	.lip {
		fill: var(--surface);
		stroke: var(--rule-strong);
	}
	.moon {
		fill: none;
		stroke: var(--pic-faint);
		stroke-width: 1.1;
		stroke-linejoin: round;
	}
	.laptop.m {
		display: none;
	}

	/* ---------- The end: paid ---------- */
	.result {
		display: flex;
		flex: none;
		flex-direction: column;
		align-items: center;
		gap: 6px;
		width: 120px;
		margin-top: calc(var(--flow-y) - 46px);
		text-align: center;
		transition: opacity 300ms ease;
	}
	.tick {
		display: grid;
		place-items: center;
		width: 46px;
		height: 46px;
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
		white-space: nowrap;
	}
	.orb:global(.s-r) .result {
		opacity: 0.28;
	}

	/* ---------- 1024 to 1279px: one machine fewer awake to read ---------- */
	@media (max-width: 1279.98px) {
		.row {
			gap: 14px;
		}
		.ma {
			width: 200px;
		}
		.mc {
			width: 150px;
		}
		.mb {
			width: 216px;
		}
		.phone {
			width: 180px;
		}
		.step {
			min-width: 28px;
			margin-inline: 4px;
		}
		.result {
			width: 96px;
		}
	}

	/* ---------- Below 1024px: the woken one's flow runs down ---------- */
	@media (max-width: 1023.98px) {
		.orb {
			height: auto;
			max-width: 26rem;
			margin-inline: auto;
		}
		.art {
			display: grid;
			grid-template-columns: 1fr 1fr;
			justify-items: center;
			gap: 0 12px;
			height: auto;
			padding: 0;
		}
		.fleet,
		.row {
			display: contents;
		}
		.mb {
			grid-column: 1 / -1;
			order: 1;
			width: 100%;
			max-width: 300px;
		}
		.step:not(.w1) {
			grid-column: 1 / -1;
			flex: none;
			width: 12px;
			height: 36px;
			min-width: 0;
			margin: 10px 0;
		}
		.w2 {
			order: 2;
		}
		.w3 {
			order: 4;
		}
		.step:not(.w1) .wire {
			top: 0;
			right: auto;
			bottom: 3px;
			left: 5px;
			width: 2px;
			height: auto;
		}
		.step:not(.w1) .arrow {
			top: auto;
			right: auto;
			bottom: 1px;
			left: 1px;
			transform: rotate(135deg);
		}
		.step:not(.w1) .pulse {
			transform: translateY(-100%);
		}
		.step:not(.w1) .pulse > i {
			top: auto;
			bottom: 0;
			width: 2px;
			height: 14px;
		}
		.you {
			grid-column: 1 / -1;
			order: 3;
			margin: 0;
		}
		.phone {
			width: 230px;
		}
		.you .laptop {
			display: none;
		}
		.result {
			grid-column: 1 / -1;
			order: 5;
			width: auto;
			margin: 0;
		}
		.ma,
		.mc {
			order: 6;
			width: 100%;
			margin-top: 36px;
		}
		.ma .who,
		.mc .who {
			white-space: normal;
		}
		.kit {
			grid-column: 1 / -1;
			order: 7;
			flex-direction: column;
			gap: 8px;
			margin-top: 24px;
			padding: 10px;
		}
		/* Two rows of five: the agents, then the rest. */
		.slots {
			flex-wrap: wrap;
			justify-content: center;
			gap: 6px;
			max-width: 200px;
		}
		.sep:first-of-type {
			flex-basis: 100%;
			height: 0;
			margin: 0;
		}
		.sep:last-of-type {
			display: none;
		}
		.laptop.m {
			grid-column: 1 / -1;
			order: 8;
			position: static;
			display: flex;
			margin-top: 28px;
			transform: none;
		}
		.attach {
			right: 52px;
			bottom: 4px;
		}
		.attach::after {
			display: none;
		}
	}
	@media (prefers-reduced-motion: reduce) {
		.trig,
		.wire,
		.arrow,
		.win,
		.led,
		.who,
		.state > span,
		.ln,
		.phone,
		.yn > span,
		.result {
			transition: none !important;
		}
		.cur,
		.ripple {
			animation: none !important;
		}
	}
</style>
