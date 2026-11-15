<!--
  HeroOrbit: the hero's picture as a constellation (hero loop, 2026-11-12,
  direction "orbit"). In the middle the always-on machine, the repose mark
  and name on a tile; on a ring around it the five agents as bubbles, each
  its mark and name; further out, in plain words, what wakes it (left) and
  what it carries (right), joined to the tile by hairline traces, and the
  phone it asks you on. The owner's words: the name floating in the
  middle, the agents as bubbles around it, the tools connecting.

  The loop: an alert goes off, a pulse runs along the blue path into the
  machine, the machine and Claude Code light, then a pulse runs out to the
  phone and the phone asks. It moves by class changes (colour, opacity,
  scale in CSS transitions) and the pulses' translate, timed by anime.js.

  Still frame (reduced motion, no script, and the loop's seam): every
  part drawn, the alert's path in the accent, the phone asking.

  At 1024px and up the parts stand around the tile; below, they stack:
  what wakes it, the ring, then what it carries beside the phone.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { watchReducedMotion } from './inview';
	import { Picture } from './motion.svelte';
	import { agentMarks, type Mark } from '$lib/components/illustrations/marks';

	type Side = 'l' | 'r' | 'b' | 't';
	type Glyph = 'bell' | 'clock' | 'globe' | 'code' | 'key';
	type Chip = { text: string; glyph: Glyph; dy: number; active?: boolean; dash?: boolean };
	type Agent = {
		m: Mark;
		x: number;
		y: number;
		mx: number;
		my: number;
		s: number;
		d: Side;
		mo: Side;
		active?: boolean;
	};
	const mark = (n: string) => agentMarks.find((m) => m.name === n)!;

	// What wakes it, top to bottom on the left; the middle one is the path
	// the loop runs.
	const wakes: Chip[] = [
		{ text: 'every night at 2', glyph: 'clock', dy: -124, dash: true },
		{ text: 'an alert goes off', glyph: 'bell', dy: 0, active: true },
		{ text: 'a webhook arrives', glyph: 'globe', dy: 124 }
	];
	// What it carries, top and bottom on the right; the phone between.
	const carries: Chip[] = [
		{ text: 'your code, tools, logins', glyph: 'code', dy: -124 },
		{ text: 'your secrets', glyph: 'key', dy: 124 }
	];
	// On the ring (radius 150): five bubbles at -145, -90, -35, 55 and 125
	// degrees, clear of the traces that come in level with the tile. Below
	// 1024px (mx, my; radius 100) Claude Code sits at the top, where the
	// alert's wire comes down into it, and the bottom stays clear for the
	// wire out. Each name sits where the ring does not cross it: under the
	// bubble (b), over it (t), or out to its left (l) or right (r); d at 1024px and up,
	// mo below.
	const agents: Agent[] = [
		{
			m: mark('Claude Code'),
			x: -123,
			y: -86,
			mx: 0,
			my: -100,
			s: 60,
			d: 'l',
			mo: 'b',
			active: true
		},
		{ m: mark('Codex'), x: 0, y: -150, mx: -88, my: -48, s: 56, d: 'b', mo: 't' },
		{ m: mark('opencode'), x: 123, y: -86, mx: 88, my: -48, s: 54, d: 'r', mo: 't' },
		{ m: mark('Gemini CLI'), x: 86, y: 123, mx: 71, my: 71, s: 52, d: 'b', mo: 'b' },
		{ m: mark('pi'), x: -86, y: 123, mx: -71, my: 71, s: 50, d: 'b', mo: 'b' }
	];

	let pic: HTMLDivElement;
	const picture = new Picture();

	onMount(() => {
		let dead = false;
		let run = 0;
		let tl: { pause(): unknown; play(): unknown } | null = null;
		const STATES = ['fire', 'hit', 'ask'];

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
				const set = (c: string, on: boolean) => () => pic.classList.toggle(c, on);
				const across = () => matchMedia('(min-width: 1024px)').matches;
				type TL = ReturnType<typeof createTimeline>;
				// A pulse along one wire: the in-wire (alert to machine) or the
				// out-wire (machine to phone); level at 1024px and up, falling below.
				function pulse(t: TL, which: 'in' | 'out', at: number) {
					const el = pic.querySelector<HTMLElement>(`.${across() ? 'wd' : 'wm'}.${which} .pulse`);
					if (!el) return;
					t.add(
						el,
						across()
							? { x: ['-100%', '100%'], duration: 1100, ease: 'inOutSine' }
							: { y: ['-100%', '100%'], duration: 900, ease: 'inOutSine' },
						at
					);
				}
				function cycle() {
					if (dead || me !== run) return;
					const t = createTimeline({ autoplay: false, onComplete: () => cycle() });
					// The alert goes off and a pulse runs in.
					t.call(set('fire', true), 900);
					pulse(t, 'in', 1100);
					// The machine wakes and Claude Code with it.
					t.call(set('hit', true), 2100);
					// It asks: a pulse runs out and the phone lights.
					pulse(t, 'out', 3300);
					t.call(set('ask', true), 4300)
						.call(set('fire', false), 5200)
						.call(set('hit', false), 6200)
						.call(set('ask', false), 8600)
						.call(() => {}, 9400);
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
		{#if kind === 'bell'}
			<path d="M4 11V7.5a4 4 0 0 1 8 0V11l1 1.5H3zM6.5 14h3" />
		{:else if kind === 'clock'}
			<circle cx="8" cy="8" r="5.75" /><path d="M8 4.75V8l2.25 1.5" />
		{:else if kind === 'globe'}
			<circle cx="8" cy="8" r="5.5" /><path
				d="M2.5 8h11M8 2.5c-1.8 1.6-2.6 3.4-2.6 5.5s.8 3.9 2.6 5.5M8 2.5c1.8 1.6 2.6 3.4 2.6 5.5s-.8 3.9-2.6 5.5"
			/>
		{:else if kind === 'code'}
			<rect x="2" y="3" width="12" height="10" rx="1.25" /><path
				d="M5 6.5 6.75 8 5 9.5M8.5 9.75h2.5"
			/>
		{:else}
			<circle cx="5.5" cy="10.5" r="3" /><path d="M7.6 8.4 13 3M11 5l1.75 1.75M9.5 6.5l1.25 1.25" />
		{/if}
	</svg>
{/snippet}

{#snippet chip(c: Chip, side: 'l' | 'r')}
	<span class="chip {side}" class:active={c.active} style="--dy: {c.dy}px"
		>{@render glyph(c.glyph)}<span>{c.text}</span></span
	>
{/snippet}

<!-- A wire with a pulse that can run along it: level (wd) or falling (wm). -->
{#snippet wire(cls: string)}
	<span class="wire {cls}"><i class="pulse"><i></i></i></span>
{/snippet}

<div class="orb-frame" use:picture.frame>
	<div
		class="orb"
		bind:this={pic}
		role="img"
		aria-label="repose in the middle: one always-on machine. Around it, the agents that can run on it: Claude Code, Codex, opencode, Gemini CLI and pi. On the left, what wakes it: every night at 2, an alert goes off, a webhook arrives. On the right, what it carries: your code, tools and logins, and your secrets. When an alert goes off, the machine wakes, Claude Code looks into it, and it asks you on your phone: roll back the last deploy? Yes or no."
	>
		<!-- The traces, drawn at 1024px and up. -->
		<div class="traces" aria-hidden="true">
			<i class="z l up dash"></i><i class="z2 l up dash"></i>
			<i class="z l dn"></i><i class="z2 l dn"></i>
			<i class="z r up"></i><i class="z2 r up"></i>
			<i class="z r dn"></i><i class="z2 r dn"></i>
			{@render wire('wd in')}
			{@render wire('wd out')}
		</div>

		<div class="side wake" aria-hidden="true">
			{#each wakes as c (c.text)}{@render chip(c, 'l')}{/each}
		</div>
		{@render wire('wm in')}

		<div class="hub" aria-hidden="true">
			<i class="track"></i>
			<i class="spine a"></i><i class="spine b"></i>
			<div class="core">
				<svg class="rmark" viewBox="9 9 82 82" width="30" height="30" aria-hidden="true"
					><path
						class="mother"
						d="M42.7 53.3A23 23 0 1 1 53.3 42.7A5.6 5.6 0 0 0 54.3 49.2L49.2 54.3A5.6 5.6 0 0 0 42.7 53.3Z"
					/><path
						class="daughter"
						d="M60.6 50.3A21 21 0 1 1 50.3 60.6A5.6 5.6 0 0 0 49.2 54.3L54.3 49.2A5.6 5.6 0 0 0 60.6 50.3Z"
					/></svg
				>
				<span class="bd">repose</span>
				<span class="on">always on</span>
			</div>
			{#each agents as a (a.m.name)}
				<span
					class="ag d-{a.d} m-{a.mo}"
					class:active={a.active}
					style="--x: {a.x}px; --y: {a.y}px; --mx: {a.mx}px; --my: {a.my}px; --s: {a.s}px"
					><span class="bub">{@render markSvg(a.m, Math.round(a.s * 0.4))}</span><span class="nm"
						>{a.m.name}</span
					></span
				>
			{/each}
		</div>

		<div class="side carry" aria-hidden="true">
			{@render chip(carries[0], 'r')}
			{@render wire('wm out')}
			{@render chip(carries[1], 'r')}
			<div class="phone">
				<i class="notch"></i>
				<span class="who">{@render markSvg(agents[0].m, 12)}Claude Code asks</span>
				<span class="q">Roll back the last deploy?</span>
				<span class="yn"><span class="yes">Yes</span><span class="no">No</span></span>
			</div>
		</div>
	</div>
</div>

<style>
	.orb {
		--h: 400px;
		--bus: 22%;
		position: relative;
		height: var(--h);
		color: var(--pic-ink);
		font-family: var(--font-sans);
		font-size: 13px;
		line-height: 18px;
	}

	/* ---------- Chips: what wakes it, what it carries ---------- */
	.side {
		position: absolute;
		inset: 0;
		pointer-events: none;
	}
	.chip {
		position: absolute;
		top: calc(50% + var(--dy));
		display: flex;
		align-items: center;
		gap: 8px;
		padding: 8px 12px;
		border: 1px solid var(--rule-strong);
		border-radius: 6px;
		background: var(--surface);
		color: var(--pic-dim);
		white-space: nowrap;
		transform: translateY(-50%);
		transition:
			background-color 240ms ease,
			border-color 240ms ease,
			color 240ms ease;
	}
	.chip.l {
		left: 0;
	}
	.chip.r {
		right: 0;
	}
	.chip.active {
		border-color: var(--pic-accent);
		color: var(--pic-ink);
	}
	.orb:global(.fire) .chip.active {
		background: var(--pic-accent);
		color: var(--surface);
	}
	.gl {
		flex: none;
	}

	/* ---------- Traces ----------
	   Each side's outer chips step in to a bus a quarter of the way in,
	   then run level into the tile 28px off its middle; the alert's wire
	   and the phone's run straight through. */
	.traces {
		position: absolute;
		inset: 0;
	}
	.traces > i {
		position: absolute;
		border: 0 solid var(--rule-strong);
	}
	.z,
	.z2 {
		--in: 28px;
		--out: 124px;
		--mid: calc((var(--out) + var(--in)) / 2);
	}
	/* The outer half: from the chip to the bus, then half way to the tile. */
	.z.l {
		left: 0;
		width: var(--bus);
	}
	.z.r {
		right: 0;
		width: var(--bus);
	}
	.z.up {
		top: calc(50% - var(--out));
		height: calc(var(--out) - var(--mid));
		border-top-width: 1px;
	}
	.z.dn {
		bottom: calc(50% - var(--out));
		height: calc(var(--out) - var(--mid));
		border-bottom-width: 1px;
	}
	.z.l {
		border-right-width: 1px;
	}
	.z.r {
		border-left-width: 1px;
	}
	.z.l.up {
		border-top-right-radius: 10px;
	}
	.z.l.dn {
		border-bottom-right-radius: 10px;
	}
	.z.r.up {
		border-top-left-radius: 10px;
	}
	.z.r.dn {
		border-bottom-left-radius: 10px;
	}
	/* The inner half: down from the bus, then level into the tile. */
	.z2.l {
		left: calc(var(--bus) - 1px);
		right: 50%;
		border-left-width: 1px;
	}
	.z2.r {
		right: calc(var(--bus) - 1px);
		left: 50%;
		border-right-width: 1px;
	}
	.z2.up {
		top: calc(50% - var(--mid));
		height: calc(var(--mid) - var(--in));
		border-bottom-width: 1px;
	}
	.z2.dn {
		bottom: calc(50% - var(--mid));
		height: calc(var(--mid) - var(--in));
		border-top-width: 1px;
	}
	.z2.l.up {
		border-bottom-left-radius: 10px;
	}
	.z2.l.dn {
		border-top-left-radius: 10px;
	}
	.z2.r.up {
		border-bottom-right-radius: 10px;
	}
	.z2.r.dn {
		border-top-right-radius: 10px;
	}
	.traces > .dash {
		border-style: dashed;
	}

	/* A wire: a hairline in the accent, and a short bright pulse that a
	   wrapper the wire's length carries across it. */
	.wire {
		position: absolute;
		display: block;
		overflow: hidden;
	}
	.wire::before {
		content: '';
		position: absolute;
		background: var(--pic-accent);
	}
	.pulse {
		position: absolute;
		inset: 0;
		transform: translateX(-100%);
	}
	.pulse > i {
		position: absolute;
		border-radius: 1px;
		background: var(--pic-ink);
	}
	.wd {
		top: calc(50% - 1px);
		height: 3px;
	}
	.wd::before {
		inset: 1px 0;
	}
	.wd .pulse > i {
		top: 0.5px;
		right: 0;
		width: 56px;
		height: 2px;
	}
	.wd.in {
		left: 0;
		right: 50%;
	}
	.wd.out {
		left: 50%;
		right: 0;
	}
	.wm,
	.spine {
		display: none;
	}

	/* ---------- The hub: the machine on its ring of agents ---------- */
	.hub {
		position: absolute;
		inset: 0;
	}
	.track {
		position: absolute;
		top: 50%;
		left: 50%;
		width: 300px;
		height: 300px;
		border: 1px dashed var(--rule-strong);
		border-radius: 50%;
		transform: translate(-50%, -50%);
	}
	.core {
		position: absolute;
		top: 50%;
		left: 50%;
		display: flex;
		flex-direction: column;
		align-items: center;
		justify-content: center;
		gap: 6px;
		width: 128px;
		height: 128px;
		border: 1px solid var(--rule-strong);
		border-radius: 10px;
		background: var(--surface);
		transform: translate(-50%, -50%);
		transition: border-color 300ms ease;
	}
	.orb:global(.hit) .core {
		border-color: var(--pic-accent);
	}
	.mother {
		fill: var(--pic-ink);
	}
	.daughter {
		fill: var(--pic-accent);
	}
	.core .bd {
		font-family: var(--hk-font-display);
		font-size: 19px;
		font-weight: 600;
		line-height: 1;
		letter-spacing: -0.01em;
	}
	.on {
		display: flex;
		align-items: center;
		gap: 6px;
		color: var(--pic-dim);
		font-size: 12px;
		line-height: 1;
	}

	.ag {
		position: absolute;
		top: calc(50% + var(--y));
		left: calc(50% + var(--x));
		width: var(--s);
		height: var(--s);
		transform: translate(-50%, -50%);
	}
	.bub {
		display: grid;
		place-items: center;
		width: 100%;
		height: 100%;
		border: 1px solid var(--rule-strong);
		border-radius: 50%;
		background: var(--surface);
		color: var(--pic-ink);
		transition:
			border-color 300ms ease,
			color 300ms ease;
	}
	.ag.active .bub {
		border-color: var(--pic-accent);
	}
	.orb:global(.hit) .ag.active .bub {
		color: var(--pic-accent);
	}
	.nm {
		position: absolute;
		top: calc(100% + 6px);
		left: 50%;
		color: var(--pic-dim);
		font-size: 12px;
		line-height: 16px;
		white-space: nowrap;
		transform: translateX(-50%);
	}
	.d-l .nm,
	.d-r .nm {
		top: 50%;
		transform: translateY(-50%);
	}
	.d-l .nm {
		right: calc(100% + 10px);
		left: auto;
	}
	.d-r .nm {
		left: calc(100% + 10px);
	}

	/* ---------- The phone it asks you on ---------- */
	.phone {
		position: absolute;
		top: 50%;
		right: 0;
		display: flex;
		flex-direction: column;
		gap: 8px;
		width: 176px;
		padding: 10px 12px 12px;
		border: 1px solid var(--rule-strong);
		border-radius: 16px;
		background: var(--surface);
		transform: translateY(-50%);
		transition: border-color 300ms ease;
	}
	.orb:global(.ask) .phone {
		border-color: var(--pic-accent);
	}
	.notch {
		align-self: center;
		width: 28px;
		height: 4px;
		border-radius: 2px;
		background: var(--rule-strong);
	}
	.who {
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
	}
	.yes {
		border-color: var(--pic-accent) !important;
		background: var(--pic-accent);
		color: var(--surface);
		font-weight: 500;
	}
	.no {
		color: var(--pic-dim);
	}

	/* ---------- Below 1024px: stacked ----------
	   What wakes it in a row, a wire down into Claude Code and the tile,
	   the wire on down between the two things it carries, the phone. */
	@media (max-width: 1023.98px) {
		.orb {
			display: flex;
			flex-direction: column;
			align-items: center;
			height: auto;
			max-width: 26rem;
			margin-inline: auto;
		}
		.traces {
			display: none;
		}
		.side {
			position: static;
			display: flex;
			flex-wrap: wrap;
			justify-content: center;
			gap: 8px;
		}
		.chip {
			position: static;
			transform: none;
		}
		/* The alert last, alone over the wire it sends down. */
		.chip.active {
			order: 1;
		}
		.wm {
			position: relative;
			display: block;
			flex: none;
			width: 3px;
			height: 28px;
		}
		.wm::before {
			inset: 0 1px;
		}
		.wm .pulse {
			transform: translateY(-100%);
		}
		.wm .pulse > i {
			bottom: 0;
			left: 0.5px;
			width: 2px;
			height: 16px;
		}
		.hub {
			--cy: 124px;
			position: relative;
			inset: auto;
			flex: none;
			width: 100%;
			height: 244px;
		}
		/* The wire on through the hub: down into Claude Code's bubble, and
		   out of the tile's foot. */
		.spine {
			position: absolute;
			left: calc(50% - 0.5px);
			display: block;
			width: 1px;
			background: var(--pic-accent);
		}
		.spine.a {
			top: 0;
			height: calc(var(--cy) - 100px);
		}
		.spine.b {
			top: var(--cy);
			bottom: 0;
		}
		.ag .nm {
			top: calc(100% + 6px);
			right: auto;
			left: 50%;
			transform: translateX(-50%);
		}
		.m-l .nm,
		.m-r .nm {
			top: 50%;
			transform: translateY(-50%);
		}
		.m-l .nm {
			right: calc(100% + 8px);
			left: auto;
		}
		.m-r .nm {
			left: calc(100% + 8px);
		}
		.m-t .nm {
			top: auto;
			bottom: calc(100% + 6px);
		}
		.track,
		.core {
			top: var(--cy);
		}
		.track {
			width: 200px;
			height: 200px;
		}
		.core {
			width: 96px;
			height: 96px;
		}
		.core .bd {
			font-size: 16px;
		}
		.rmark {
			width: 24px;
			height: 24px;
		}
		.ag {
			top: calc(var(--cy) + var(--my));
			left: calc(50% + var(--mx));
			width: calc(var(--s) * 0.74);
			height: calc(var(--s) * 0.74);
		}
		.bub :global(svg) {
			width: 18px;
			height: 18px;
		}
		.carry {
			display: grid;
			grid-template-columns: minmax(0, 1fr) 3px minmax(0, 1fr);
			align-items: center;
			gap: 0 14px;
			width: 100%;
		}
		.carry .wm {
			align-self: stretch;
			height: auto;
			min-height: 48px;
		}
		.chip.r {
			white-space: normal;
		}
		.chip.r:first-child {
			justify-self: end;
		}
		.chip.r:nth-child(3) {
			justify-self: start;
		}
		.phone {
			position: static;
			grid-column: 1 / -1;
			justify-self: center;
			transform: none;
		}
	}
	@media (prefers-reduced-motion: reduce) {
		.chip,
		.core,
		.bub,
		.phone {
			transition: none !important;
		}
	}
</style>
