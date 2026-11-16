<!--
  HeroOrbit: the hero's picture as one machine and the parts that orbit it
  (hero loop, rounds 11-12, 2026-11-12). In the middle, one agent's own
  machine: a window with what it is doing in plain words over the
  developer's detail. On a ring around it, as icons with short names,
  what you put in: any agent (the five marks, one chosen), your code,
  your keys, and what wakes it (an alert, a schedule, a new posting).
  A line runs in from the alert and out to the phone it asks you on, so
  cause and effect read in the still frame. Two other agents you built
  sleep at the edges, other sizes, wired to nothing.

  The loop starts from the still frame, the machine asking you on your
  phone: you tap Yes, the phone and the lines go, the machine sleeps
  (dim, a crescent), the alert rings, a pulse runs in, it wakes and
  works, a pulse runs out and it asks you again. It moves by class
  changes (colour, opacity, scale in CSS) and the pulses' translate,
  timed by anime.js.

  Still frame (reduced motion, no script, and the loop's seam): the alert
  lit, both lines drawn, the machine awake and asking, the phone showing
  Yes and No, the two agents at the edges asleep.

  At 1024px and up the parts stand on the ring; below, they stack: what
  wakes it, the line down, the machine, the line down, the phone, what
  goes in, the two asleep.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { watchReducedMotion } from './inview';
	import { Picture } from './motion.svelte';
	import { agentMarks, type Mark } from '$lib/components/illustrations/marks';

	type Glyph = 'bell' | 'clock' | 'case' | 'code' | 'key' | 'moon';
	type Part = { text: string; glyph: Glyph; a: number; cls?: string; side: 'l' | 'r' };
	type Sleeper = { cls: string; m: Mark; name: string; gb: string; hint: string };
	const mark = (n: string) => agentMarks.find((m) => m.name === n)!;

	// What wakes it, on the ring's left; the alert level with the window.
	const wakes: Part[] = [
		{ text: 'a schedule', glyph: 'clock', a: -146, side: 'l' },
		{ text: 'an alert', glyph: 'bell', a: 180, cls: 'alert', side: 'l' },
		{ text: 'a new posting', glyph: 'case', a: 146, side: 'l' }
	];
	// What goes in, on the ring's right.
	const ins: Part[] = [
		{ text: 'your code', glyph: 'code', a: -30, side: 'r' },
		{ text: 'your keys', glyph: 'key', a: 30, side: 'r' }
	];
	const chosen = mark('Claude Code');
	const five = agentMarks.slice(0, 5);
	// The other agents you built, asleep: top left and bottom right.
	const sleepers: Sleeper[] = [
		{ cls: 'sa', m: mark('opencode'), name: 'dev environment', gb: '16 GB', hint: 'tests pass' },
		{ cls: 'sb', m: mark('pi'), name: 'job search', gb: '8 GB', hint: 'applied to 3' }
	];

	let pic: HTMLDivElement;
	const picture = new Picture();

	onMount(() => {
		let dead = false;
		let run = 0;
		let tl: { pause(): unknown; play(): unknown } | null = null;
		const STATES = ['s-yes', 's-done', 's-sleep', 's-ring', 's-wake'];

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
				// A pulse along one line: in (alert to machine) or out (machine
				// to phone); level at 1024px and up, falling below.
				function pulse(t: TL, which: 'in' | 'out', at: number) {
					const el = pic.querySelector<HTMLElement>(`.wire.${which} .pulse`);
					if (!el) return;
					t.add(
						el,
						across()
							? { x: ['-100%', '100%'], duration: 900, ease: 'inOutSine' }
							: { y: ['-100%', '100%'], duration: 700, ease: 'inOutSine' },
						at
					);
				}
				function cycle() {
					if (dead || me !== run) return;
					const t = createTimeline({ autoplay: false, onComplete: () => cycle() });
					// From the still frame: you tap Yes, the phone goes.
					t.call(on('s-yes'), 700)
						.call(on('s-done'), 1500)
						// The machine sleeps.
						.call(on('s-sleep'), 2700)
						// The alert rings and a pulse runs in.
						.call(on('s-sleep', 's-ring'), 5200);
					pulse(t, 'in', 5600);
					// It wakes and works, then a pulse runs out and it asks you:
					// the still frame again.
					t.call(on('s-wake'), 6500);
					pulse(t, 'out', 8500);
					t.call(on(), 9300).call(() => {}, 11000);
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

{#snippet glyph(kind: Glyph, size = 18)}
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
		{:else if kind === 'case'}
			<rect x="2" y="5" width="12" height="8.5" rx="1.25" /><path d="M6 5V3.5h4V5M2 9h12" />
		{:else if kind === 'code'}
			<path d="M5.5 4.5 2 8l3.5 3.5M10.5 4.5 14 8l-3.5 3.5M9 3.5l-2 9" />
		{:else if kind === 'key'}
			<circle cx="5.5" cy="10.5" r="3" /><path d="M7.6 8.4 13 3M11 5l1.75 1.75M9.5 6.5l1.25 1.25" />
		{:else}
			<path d="M10.5 2.5a5.75 5.75 0 1 0 3 9.5A5 5 0 0 1 10.5 2.5z" />
		{/if}
	</svg>
{/snippet}

{#snippet part(p: Part)}
	<span class="part {p.cls ?? ''} {p.side}" style="--a: {p.a}deg"
		><span class="ic">{@render glyph(p.glyph)}<i class="rip"></i><i class="rip b"></i></span><span
			class="lb">{p.text}</span
		></span
	>
{/snippet}

<!-- A line with a pulse that can run along it. -->
{#snippet wire(which: 'in' | 'out')}
	<span class="wire {which}"><i class="pulse"><i></i></i></span>
{/snippet}

<div class="orb-frame" use:picture.frame>
	<div
		class="orb"
		bind:this={pic}
		role="img"
		aria-label="One agent's own machine. Around it, what you put in: any agent (Claude Code, Codex, opencode, Gemini CLI or pi), your code, your keys, and what wakes it: an alert, a schedule or a new job posting. An alert has gone off and woken it: checkout broke after the 2 pm update, and it found the change that did it. It asks you on your phone: undo the 2 pm update? Yes or no. Then it goes back to sleep. At the edges, two other agents you built sleep on machines of their own: a dev environment with 16 GB and a job search with 8 GB."
	>
		<div class="art" aria-hidden="true">
			<svg class="track"><ellipse cx="50%" cy="50%" rx="50%" ry="50%" /></svg>

			<div class="wakes">
				{#each wakes as p (p.text)}{@render part(p)}{/each}
			</div>
			{@render wire('in')}

			<div class="machine">
				<i class="ripple"></i>
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
						<span class="ln l5">Asking you before undoing it<i class="cur"></i></span>
					</span>
				</div>
			</div>

			{@render wire('out')}
			<div class="phone">
				<i class="notch"></i>
				<span class="pwho">{@render markSvg(chosen, 12)}Claude Code asks</span>
				<span class="q">Undo the 2 pm update?</span>
				<span class="yn"><span class="yes">Yes</span><span class="no">No</span></span>
			</div>

			<div class="ins">
				{@render part(ins[0])}
				<span class="part agent" style="--a: -90deg"
					><span class="pick"
						>{#each five as m (m.name)}<span class="opt" class:on={m === chosen}
								>{@render markSvg(m, 14)}</span
							>{/each}</span
					><span class="lb">any agent</span></span
				>
				{@render part(ins[1])}
			</div>

			<div class="others">
				{#each sleepers as z (z.name)}
					<span class="sleeper {z.cls}"
						><span class="sname">{@render markSvg(z.m, 14)}{z.name}</span><span class="zz"
							>{@render glyph('moon', 12)}asleep<span class="gb">{z.gb}</span></span
						><span class="hint">{z.hint}</span></span
					>
				{/each}
			</div>
		</div>
	</div>
</div>

<style>
	.orb {
		--h: 360px;
		--rx: 318px;
		--ry: 150px;
		--ww: 340px;
		--pw: 176px;
		position: relative;
		height: var(--h);
		color: var(--pic-ink);
		font-family: var(--font-sans);
		font-size: 13px;
		line-height: 18px;
	}
	.art {
		position: absolute;
		inset: 0;
	}

	/* ---------- The ring the parts stand on ---------- */
	.track {
		position: absolute;
		top: 50%;
		left: 50%;
		width: calc(var(--rx) * 2 + 2px);
		height: calc(var(--ry) * 2 + 2px);
		overflow: visible;
		transform: translate(-50%, -50%);
	}
	.track ellipse {
		fill: none;
		stroke: var(--pic-faint);
		stroke-dasharray: 2 5;
		opacity: 0.7;
	}

	/* ---------- A part: an icon in a circle, its name under it ---------- */
	.wakes,
	.ins,
	.others {
		display: contents;
	}
	.part {
		position: absolute;
		top: calc(50% + sin(var(--a)) * var(--ry));
		left: calc(50% + cos(var(--a)) * var(--rx));
		width: 0;
		height: 0;
	}
	.ic {
		position: absolute;
		top: -20px;
		left: -20px;
		display: grid;
		place-items: center;
		width: 40px;
		height: 40px;
		border: 1px solid var(--rule-strong);
		border-radius: 50%;
		background: var(--surface);
		color: var(--pic-ink);
		transition:
			background-color 240ms ease,
			border-color 240ms ease,
			color 240ms ease;
	}
	.lb {
		position: absolute;
		top: 26px;
		left: 0;
		color: var(--pic-dim);
		font-size: 12px;
		line-height: 16px;
		white-space: nowrap;
		transform: translateX(-50%);
	}
	/* Names stand outside the ring, level with their icons. */
	.part.l .lb,
	.part.r .lb {
		top: -8px;
		transform: none;
	}
	.part.l .lb {
		right: 30px;
		left: auto;
	}
	.part.r .lb {
		left: 30px;
	}
	/* The alert rings: its circle lights and two rings go out from it. */
	.alert .ic {
		border-color: var(--pic-accent);
		background: var(--pic-accent);
		color: var(--surface);
	}
	.alert .lb {
		color: var(--pic-ink);
	}
	.orb:global(:is(.s-done, .s-sleep):not(.s-ring)) .alert .ic {
		background: var(--surface);
		color: var(--pic-ink);
	}
	.orb:global(:is(.s-done, .s-sleep):not(.s-ring)) .alert .lb {
		color: var(--pic-dim);
	}
	.rip {
		position: absolute;
		inset: -1px;
		border: 1px solid var(--pic-accent);
		border-radius: 50%;
		opacity: 0;
	}
	.orb:global(.s-ring) .alert .rip {
		animation: ring 1100ms cubic-bezier(0.2, 0.6, 0.3, 1) 2 both;
	}
	.orb:global(.s-ring) .alert .rip.b {
		animation-delay: 300ms;
	}
	@keyframes ring {
		from {
			opacity: 0.9;
			transform: scale(1);
		}
		to {
			opacity: 0;
			transform: scale(1.9);
		}
	}

	/* The agent: the five marks you can put in, one chosen. */
	.pick {
		position: absolute;
		top: -19px;
		left: 0;
		display: flex;
		gap: 4px;
		padding: 4px;
		border: 1px solid var(--rule-strong);
		border-radius: 19px;
		background: var(--surface);
		transform: translateX(-50%);
	}
	.opt {
		display: grid;
		place-items: center;
		width: 28px;
		height: 28px;
		border: 1px solid transparent;
		border-radius: 50%;
		color: var(--pic-faint);
	}
	.opt.on {
		border-color: var(--pic-accent);
		color: var(--pic-ink);
	}
	.agent .lb {
		top: 26px;
	}

	/* ---------- The lines: in from the alert, out to the phone ----------
	   Each a hairline in the accent, and a short bright pulse that a
	   wrapper the line's length carries across it. */
	.wire {
		position: absolute;
		top: calc(50% - 1px);
		height: 3px;
		overflow: hidden;
		transition: opacity 300ms ease;
	}
	.wire::before {
		content: '';
		position: absolute;
		inset: 1px 0;
		background: var(--pic-accent);
	}
	.wire.in {
		left: calc(50% - var(--rx) + 20px);
		width: calc(var(--rx) - 20px - var(--ww) / 2);
	}
	.wire.out {
		right: var(--pw);
		left: calc(50% + var(--ww) / 2);
	}
	.orb:global(:is(.s-done, .s-sleep):not(.s-ring)) .wire.in,
	.orb:global(:is(.s-done, .s-sleep, .s-wake)) .wire.out {
		opacity: 0;
	}
	.pulse {
		position: absolute;
		inset: 0;
		transform: translateX(-100%);
	}
	.pulse > i {
		position: absolute;
		top: 0.5px;
		right: 0;
		width: 40px;
		height: 2px;
		border-radius: 1px;
		background: var(--pic-ink);
	}

	/* ---------- The machine ---------- */
	.machine {
		position: absolute;
		top: 50%;
		left: 50%;
		width: var(--ww);
		transform: translate(-50%, -50%);
	}
	.win {
		position: relative;
		display: flex;
		flex-direction: column;
		border: 1px solid var(--pic-accent);
		border-radius: 6px;
		background: var(--surface);
		transition: border-color 400ms ease;
	}
	.tbar {
		display: flex;
		align-items: center;
		justify-content: space-between;
		padding: 8px 14px;
		border-bottom: 1px solid var(--rule);
		font-size: 12px;
		line-height: 16px;
	}
	.who {
		display: flex;
		align-items: center;
		gap: 8px;
		color: var(--pic-ink);
		transition: color 400ms ease;
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
	.term {
		display: flex;
		flex-direction: column;
		padding: 12px 14px 14px;
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
		margin: 8px -14px 10px;
		padding: 4px 14px;
		background: var(--sunken);
		color: var(--pic-dim);
		font-family: var(--font-mono);
		font-size: 12px;
		line-height: 18px;
	}
	.diff .bd {
		display: inline-block;
		width: 2ch;
		color: var(--pic-faint);
		font-weight: 400;
	}
	.l5 {
		color: var(--pic-ink);
		font-size: 13px;
		line-height: 18px;
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
	/* The machine wakes: one hairline goes out from the window and fades. */
	.ripple {
		position: absolute;
		inset: 0;
		border: 1px solid var(--pic-accent);
		border-radius: 6px;
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
			transform: scale(1.12, 1.2);
		}
	}

	/* Asleep: dim, its screen frozen, a crescent for the running dot. */
	.orb:global(.s-sleep) .win {
		border-color: var(--rule-strong);
	}
	.orb:global(.s-sleep) .who {
		color: var(--pic-faint);
	}
	.orb:global(.s-sleep) .awake,
	.orb:global(:is(.s-sleep, .s-done)) .l5 .cur {
		opacity: 0;
		animation: none;
	}
	.orb:global(.s-sleep) .asleep {
		opacity: 1;
	}
	.orb:global(.s-sleep) .ln {
		opacity: 0.22;
	}
	/* Awake and working: the lines come in one by one, the ask last. */
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
		animation: none;
		opacity: 0;
	}
	@keyframes line {
		from {
			opacity: 0;
		}
	}

	/* ---------- The other agents you built, asleep at the edges ---------- */
	.sleeper {
		position: absolute;
		display: flex;
		flex-direction: column;
		gap: 4px;
		padding: 10px 12px 12px;
		border: 1px solid var(--rule);
		border-radius: 6px;
		background: var(--surface);
		color: var(--pic-dim);
		font-size: 12px;
		line-height: 16px;
	}
	.sname {
		display: flex;
		align-items: center;
		gap: 8px;
		color: var(--pic-dim);
		font-size: 13px;
		line-height: 18px;
	}
	.sname :global(svg) {
		flex: none;
		color: var(--pic-faint);
	}
	.zz {
		display: flex;
		align-items: center;
		gap: 6px;
		color: var(--pic-faint);
	}
	.gb {
		margin-left: auto;
	}
	.hint {
		margin-top: 6px;
		padding-top: 8px;
		border-top: 1px solid var(--rule);
		color: var(--pic-faint);
		font-family: var(--font-mono);
		font-size: 12px;
		line-height: 16px;
		opacity: 0.8;
	}
	.sa {
		top: 4px;
		left: 0;
		width: 180px;
	}
	.sb {
		right: 0;
		bottom: 4px;
		width: 160px;
	}

	/* ---------- The phone it asks you on ---------- */
	.phone {
		position: absolute;
		top: 50%;
		right: 0;
		display: flex;
		flex-direction: column;
		gap: 8px;
		width: var(--pw);
		padding: 10px 12px 12px;
		border: 1px solid var(--pic-accent);
		border-radius: 16px;
		background: var(--surface);
		transform: translateY(-50%);
		transition:
			opacity 300ms ease,
			transform 300ms ease;
	}
	.orb:global(:is(.s-done, .s-sleep, .s-wake)) .phone {
		opacity: 0;
		transform: translateY(calc(-50% + 8px));
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
		font-weight: 500;
	}
	.yn {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 6px;
	}
	.yn > span {
		padding: 4px 0;
		border: 1px solid var(--rule-strong);
		border-radius: 6px;
		font-size: 12px;
		line-height: 16px;
		text-align: center;
		transition:
			background-color 200ms ease,
			color 200ms ease;
	}
	.yes {
		border-color: var(--pic-accent) !important;
		font-weight: 500;
	}
	.orb:global(.s-yes) .yes {
		background: var(--pic-accent);
		color: var(--surface);
	}
	.no {
		color: var(--pic-dim);
	}

	/* ---------- 1024 to 1279px: a narrower ring ---------- */
	@media (max-width: 1279.98px) {
		.orb {
			--rx: 236px;
			--ww: 300px;
			--pw: 168px;
		}
		.sa {
			width: 160px;
		}
		.sb {
			width: 136px;
		}
	}

	/* ---------- Below 1024px: stacked ----------
	   What wakes it in a row, the line down into the machine, the line
	   down to the phone, what goes in in a row, the two asleep. */
	@media (max-width: 1023.98px) {
		.orb {
			height: auto;
			max-width: 26rem;
			margin-inline: auto;
		}
		.art {
			position: static;
			display: flex;
			flex-direction: column;
			align-items: center;
		}
		.track {
			display: none;
		}
		.wakes,
		.ins {
			display: grid;
			grid-template-columns: 1fr auto 1fr;
			align-items: start;
			gap: 8px;
			width: 100%;
		}
		.ins {
			margin-top: 32px;
		}
		.part {
			position: relative;
			top: auto;
			left: auto;
			display: flex;
			flex-direction: column;
			align-items: center;
			gap: 6px;
			width: auto;
			height: auto;
		}
		.ic,
		.lb,
		.pick {
			position: relative;
			top: auto;
			left: auto;
			transform: none;
		}
		.part .lb,
		.part.l .lb,
		.part.r .lb {
			top: auto;
			right: auto;
			left: auto;
		}
		.wire,
		.wire.in,
		.wire.out {
			position: relative;
			top: auto;
			right: auto;
			left: auto;
			flex: none;
			width: 3px;
			height: 28px;
		}
		.wire::before {
			inset: 0 1px;
		}
		.wire.in {
			margin-top: 6px;
		}
		.pulse {
			transform: translateY(-100%);
		}
		.pulse > i {
			top: auto;
			bottom: 0;
			left: 0.5px;
			width: 2px;
			height: 14px;
		}
		.machine {
			position: relative;
			top: auto;
			left: auto;
			width: 100%;
			max-width: 320px;
			transform: none;
		}
		.phone {
			--pw: 220px;
			position: relative;
			top: auto;
			transform: none;
		}
		.orb:global(:is(.s-done, .s-sleep, .s-wake)) .phone {
			transform: translateY(8px);
		}
		.others {
			display: grid;
			grid-template-columns: 1fr 1fr;
			gap: 12px;
			width: 100%;
			margin-top: 32px;
		}
		.sleeper {
			position: static;
			width: auto;
		}
	}
	@media (prefers-reduced-motion: reduce) {
		.ic,
		.win,
		.who,
		.state > span,
		.ln,
		.wire,
		.phone,
		.yn > span {
			transition: none !important;
		}
		.cur,
		.rip,
		.ripple {
			animation: none !important;
		}
	}
</style>
