<!--
  Stack: the hero's picture since DECISIONS I-1169. An always-on agent on
  repose is composed in layers, so the picture is a stack of plates seen
  from above at 30 degrees: the machine at the bottom, then its walls,
  your setup, its secrets, its triggers and the agent on top, the one lit
  plate. The layers' names stand on the left on short dotted leaders, and
  what each layer holds for one agent stands on the right. The loop
  composes three agents in turn (a dev environment, a shopping agent, an
  agent that applies for jobs): the plates lift off, and the next agent's
  plates drop in from the bottom, with a dashed ghost where an agent
  leaves a layer out. The grammar is voidzero.dev's stack (LANDING.md,
  "The centrepiece"): one object in white space, its layers named on
  leader lines; the finish is the pictures' own, four flat tones and one
  muted blue, no gradient and no glow.

  Still frame (reduced motion, no script, and the loop's seam): the dev
  environment, built. Motion is in LANDING.md's "Motion" list. The 2D
  drawing it replaced is Parts.svelte, kept unused for comparison (branch
  landing-centrepiece-2d has it in place).
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { watchReducedMotion } from './inview';
	import { Picture } from './motion.svelte';
	import PauseButton from './PauseButton.svelte';
	import { agentMarks } from '$lib/components/illustrations/marks';

	// The plates, bottom to top, in px: a square plate S wide seen at 30
	// degrees, T thick (the machine thicker), G apart.
	const S = 156;
	const C = Math.cos(Math.PI / 6);
	const N = Math.sin(Math.PI / 6);
	const G = 20;
	const PAD = 2;
	const thick = [18, 12, 12, 12, 12, 12];
	const z: number[] = [];
	thick.reduce((acc, t, i) => ((z[i] = acc), acc + t + G), 0);
	const ZTOP = z[5] + thick[5];
	const W = Math.round(2 * S * C + 2 * PAD);
	const H = Math.round(S + ZTOP + 2 * PAD);
	const X = (x: number) => x + S * C + PAD;
	const Y = (y: number) => y + ZTOP + PAD;
	const pts = (p: [number, number][]) => p.map(([x, y]) => `${X(x)},${Y(y)}`).join(' ');

	function plate(i: number) {
		const lo = z[i];
		const hi = z[i] + thick[i];
		return {
			top: pts([
				[0, -hi],
				[S * C, S * N - hi],
				[0, S - hi],
				[-S * C, S * N - hi]
			]),
			left: pts([
				[-S * C, S * N - hi],
				[0, S - hi],
				[0, S - lo],
				[-S * C, S * N - lo]
			]),
			right: pts([
				[0, S - hi],
				[S * C, S * N - hi],
				[S * C, S * N - lo],
				[0, S - lo]
			]),
			// Where a layer's leaders meet it: the side corners, halfway
			// down its edge.
			row: Y(S * N - lo - thick[i] / 2)
		};
	}
	const plates = thick.map((_, i) => plate(i));

	const names = ['Machine', 'Walls', 'Your setup', 'Secrets', 'Triggers', 'Agent'];
	// What each layer holds for each agent; null leaves the layer out. The
	// machine and its walls are every agent's base and stay; the layers
	// above are the agent's own, so each agent is a stack of its own shape.
	const uses: { name: string; layers: (string | null)[] }[] = [
		{
			name: 'A dev environment',
			layers: [
				'2 cores, 8 GB',
				'nothing from the internet gets in',
				'your code, tools and logins',
				'STRIPE_SECRET_KEY',
				null,
				'opencode, on your branch'
			]
		},
		{
			name: 'A shopping agent',
			layers: [
				'2 GB, on all day',
				'nothing from the internet gets in',
				null,
				"the store's API key",
				'every morning at 7',
				'Codex, told to ask before it pays'
			]
		},
		{
			name: 'An agent that applies for jobs',
			layers: [
				'8 GB, on all day',
				"only the job board's webhook gets in",
				'your resumes',
				"the job board's signing key",
				'a role.created webhook',
				'Claude Code, told to ask at a captcha'
			]
		}
	];
	// How far down (px) each upper plate sits for an agent: the layers it
	// keeps close up on the walls, in order, with no gap where one is left
	// out.
	const PITCH = thick[2] + G;
	const drop = uses.map((u) => {
		let rank = 0;
		return u.layers.map((v, i) => {
			if (i < 2 || v === null) return 0;
			return z[i] - (z[2] + rank++ * PITCH);
		});
	});
	// Each agent's mark, facing you in the middle of the top plate, as
	// voidzero sets its marks.
	const markOf = ['opencode', 'Codex', 'Claude Code'].map(
		(n) => agentMarks.find((m) => m.name === n)!
	);
	const face = `translate(${X(0) - 24} ${Y(S / 2 - (z[5] + thick[5])) - 24}) scale(2)`;

	let pic: HTMLDivElement;
	const picture = new Picture();

	onMount(() => {
		let dead = false;
		let visible = false;
		let tl: { pause(): unknown; play(): unknown } | null = null;
		let run = 0;
		const q = (s: string) => Array.from(pic.querySelectorAll<HTMLElement | SVGElement>(s));
		const moved = () => q('.pl, .v, .use > span, .mk');
		const rows = () => q('.rows > li') as HTMLElement[];

		function rest() {
			tl?.pause();
			tl = null;
			visible = false;
			for (const el of moved()) el.removeAttribute('style');
			for (const li of rows()) {
				li.style.removeProperty('--dy');
				li.style.removeProperty('--o');
			}
		}
		function still() {
			run++;
			picture.stop();
			rest();
		}

		function start() {
			const me = ++run;
			import('animejs').then(({ createTimeline }) => {
				if (dead || me !== run) return;
				type TL = ReturnType<typeof createTimeline>;
				const HOLD = 4200;

				// From agent u to agent v at `at`: the title and the base's
				// values swap in place; u's own plates leave top first, each
				// rising 16px and fading in its last 120ms; v's drop in from the
				// walls up, 24px down to their place, opaque within 120ms, each
				// row's name and value with its plate.
				function swap(t: TL, u: number, v: number, at: number) {
					const fade = (sel: string, on: boolean, when: number, ms = 300) =>
						t.add(
							q(sel),
							{ opacity: on ? [0, 1] : 0, duration: ms, ease: on ? 'outQuad' : 'inQuad' },
							when
						);
					fade(`.use > [data-u="${u}"]`, false, at, 200);
					fade(`.use > [data-u="${v}"]`, true, at + 200);
					// Every value of u's goes at once, a left-out row's included;
					// the base's come back as v's at once, the rest with their plates.
					fade(`.v[data-u="${u}"]`, false, at, 200);
					for (const i of [0, 1]) fade(`.v[data-u="${v}"][data-i="${i}"]`, true, at + 200);
					for (const i of [2, 3, 4, 5])
						if (uses[v].layers[i] === null)
							fade(`.v[data-u="${v}"][data-i="${i}"]`, true, at + 200);
					const out = [5, 4, 3, 2].filter((i) => uses[u].layers[i] !== null);
					out.forEach((i, k) => {
						const when = at + k * 50;
						t.add(
							q(`.pl[data-i="${i}"]`),
							{
								y: { to: drop[u][i] - 16, duration: 400, ease: 'inQuad' },
								opacity: { to: 0, duration: 120, delay: 280, ease: 'linear' }
							},
							when
						);
						if (i === 5) fade(`.mk[data-u="${u}"]`, false, when, 400);
						t.add(rows()[i], { '--o': 0, duration: 300, ease: 'inQuad' }, when);
					});
					const from = at + (out.length - 1) * 50 + 400;
					const into = [2, 3, 4, 5].filter((i) => uses[v].layers[i] !== null);
					into.forEach((i, k) => {
						const when = from + k * 120;
						t.add(
							q(`.pl[data-i="${i}"]`),
							{
								y: { from: drop[v][i] - 24, to: drop[v][i], duration: 520, ease: 'inOutCubic' },
								opacity: { from: 0, to: 1, duration: 120, ease: 'linear' }
							},
							when
						);
						if (i === 5) t.add(q(`.mk[data-u="${v}"]`), { opacity: [0, 1], duration: 16 }, when);
						t.add(rows()[i], { '--dy': drop[v][i], duration: 16 }, when);
						t.add(rows()[i], { '--o': [0, 1], duration: 300, ease: 'outQuad' }, when + 300);
						fade(`.v[data-u="${v}"][data-i="${i}"]`, true, when + 300);
					});
					return from + (into.length - 1) * 120 + 520;
				}

				function cycle() {
					if (dead || me !== run) return;
					if (!visible) {
						rest();
						return;
					}
					const t = createTimeline({ autoplay: false, onComplete: () => cycle() });
					// From the still frame (the dev environment) round to it.
					let at = swap(t, 0, 1, HOLD);
					at = swap(t, 1, 2, at + HOLD);
					swap(t, 2, 0, at + HOLD);
					tl = t;
					t.play();
				}

				picture.start(pic, (m) => {
					visible = m === 'play';
					if (m === 'rest' || (!visible && !tl)) rest();
					else if (!visible) tl?.pause();
					else if (tl) tl.play();
					else cycle();
				});
			});
		}

		const unwatch = watchReducedMotion((reduce) => {
			if (reduce) still();
			else start();
		});
		return () => {
			dead = true;
			unwatch();
			picture.stop();
			tl?.pause();
		};
	});
</script>

<div class="stk-frame" use:picture.frame>
	<PauseButton {picture} name="layers" />
	<div
		class="stk"
		bind:this={pic}
		role="img"
		aria-label="An always-on agent on repose, built in layers. Every agent stands on a machine and its walls; the layers above are its own. A dev environment: 2 cores and 8 GB, your code, tools and logins, a Stripe key, opencode, and nothing from the internet gets in. A shopping agent: 2 GB on all day, no setup, the store's API key, every morning at 7, Codex, told to ask before it pays. An agent that applies for jobs: 8 GB, your resumes, the job board's signing key, a role.created webhook, the only thing that gets in, and Claude Code, told to ask at a captcha."
		style="--w: {W}px; --h: {H}px;"
	>
		<p class="use" aria-hidden="true">
			{#each uses as u, k (u.name)}<span data-u={k} class:on={k === 0}
					><span class="tt" style="transform: translateY({drop[k][5]}px)">{u.name}</span></span
				>{/each}
		</p>
		<svg
			class="plates"
			viewBox="0 0 {W} {H}"
			width={W}
			height={H}
			aria-hidden="true"
			stroke-linejoin="round"
		>
			{#each plates as p, i (i)}
				<!-- The still frame is the dev environment: its plates' places
				     are in the markup, so the CSS draws it with no script. -->
				<g
					class="pl"
					class:lit={i === 5}
					class:gone={uses[0].layers[i] === null}
					data-i={i}
					transform={drop[0][i] ? `translate(0 ${drop[0][i]})` : undefined}
				>
					<polygon class="l" points={p.left} />
					<polygon class="r" points={p.right} />
					<polygon class="t" points={p.top} />
					{#if i === 5}
						{#each markOf as m, k (m.name)}
							<g class="mk" class:on={k === 0} data-u={k} transform={face}
								>{#each m.paths as d (d)}<path
										{d}
										fill-rule={m.evenodd ? 'evenodd' : 'nonzero'}
									/>{/each}</g
							>
						{/each}
					{/if}
				</g>
			{/each}
		</svg>
		<ol class="rows" aria-hidden="true">
			{#each names as name, i (name)}
				<li
					style="--y: {plates[i].row}px; --dy0: {drop[0][i]}; --o0: {uses[0].layers[i] === null
						? 0
						: 1}"
					class:lit={i === 5}
				>
					<span class="nm">{name}</span>
					<i class="lead l"></i><i class="lead r"></i>
					<span class="vals"
						>{#each uses as u, k (u.name)}<span
								class="v"
								class:on={k === 0}
								class:out={u.layers[i] === null}
								data-u={k}
								data-i={i}>{u.layers[i] ?? 'left out'}</span
							>{/each}</span
					>
				</li>
			{/each}
		</ol>
	</div>
</div>

<style>
	.stk {
		--title: 40px;
		--pl-top: var(--surface);
		--pl-r: var(--sunken);
		--pl-l: var(--stage-edge);
		--pl-edge: var(--rule-strong);
		--lead: var(--sh-grey);
		--lit-side: color-mix(in srgb, var(--pic-accent) 16%, var(--surface));
		position: relative;
		color: var(--pic-ink);
		font-family: var(--font-sans);
	}
	/* Dark: the light stays on the right, so the left face is the darker. */
	@media (prefers-color-scheme: dark) {
		:global(:root:not(.light):not([data-theme='light'])) .stk {
			--pl-l: var(--stage);
		}
	}
	:global(:root[data-theme='dark']) .stk,
	:global(:root.dark) .stk {
		--pl-l: var(--stage);
	}
	.use {
		display: grid;
		justify-items: center;
		height: var(--title);
		margin: 0;
		font-size: 15px;
		font-weight: 500;
		line-height: 20px;
	}
	/* Each title stands over its own agent's top plate, which sits lower
	   when the agent leaves a layer out. */
	.use .tt {
		display: block;
		font-weight: inherit;
	}
	.use > span,
	.vals > .v,
	.mk {
		grid-area: 1 / 1;
		opacity: 0;
	}
	.use > .on,
	.vals > .on,
	.mk.on {
		opacity: 1;
	}

	.plates {
		display: block;
		margin: 0 auto;
		overflow: visible;
	}
	.plates polygon {
		stroke: var(--pl-edge);
		stroke-width: 1;
	}
	.t {
		fill: var(--pl-top);
	}
	.l {
		fill: var(--pl-l);
	}
	.r {
		fill: var(--pl-r);
	}
	/* The one lit part: the agent's plate, its edges in the accent. */
	.lit polygon {
		stroke: var(--pic-accent);
	}
	.lit .l,
	.lit .r {
		fill: var(--lit-side);
	}
	.mk path {
		fill: var(--pic-accent);
		stroke: none;
	}
	.pl.gone {
		opacity: 0;
	}

	/* Below 1024px the names and values are a list under the stack. */
	/* Top down, as the stack reads: the agent first. */
	.rows {
		display: flex;
		flex-direction: column-reverse;
		gap: 6px;
		margin: 20px auto 0;
		padding: 0;
		max-width: 20rem;
		list-style: none;
		font-size: 12px;
		line-height: 16px;
	}
	.rows > li {
		display: grid;
		grid-template-columns: 5.5rem minmax(0, 1fr);
		column-gap: 12px;
	}
	.nm {
		font-family: var(--font-mono);
		font-size: 11px;
		letter-spacing: 0.06em;
		text-transform: uppercase;
		color: var(--pic-dim);
	}
	li.lit .nm {
		color: var(--pic-accent);
	}
	.vals {
		display: grid;
		min-width: 0;
	}
	@media (min-width: 1024px) {
		.v {
			white-space: nowrap;
		}
	}
	.v.out {
		color: var(--pic-faint);
	}
	.lead {
		display: none;
	}
	@media (max-width: 639.98px) {
		.plates {
			width: min(100%, 240px);
			height: auto;
		}
	}

	/* From 1024px the names stand left of the stack and the values right
	   of it, each on a dotted leader that ends on a dot at the plate's
	   side corner, level with the middle of its edge. */
	@media (min-width: 1024px) {
		.stk {
			height: calc(var(--title) + var(--h));
		}
		.rows {
			position: absolute;
			inset: var(--title) 0 auto;
			display: block;
			max-width: none;
			margin: 0;
			font-size: 13px;
		}
		.rows > li {
			position: absolute;
			top: calc(var(--y) - 8px + var(--dy, var(--dy0)) * 1px);
			opacity: var(--o, var(--o0));
			left: 0;
			right: 0;
			display: block;
			height: 16px;
		}
		.nm {
			position: absolute;
			right: calc(50% + var(--w) / 2 + 44px);
			white-space: nowrap;
		}
		.vals {
			position: absolute;
			left: calc(50% + var(--w) / 2 + 44px);
			width: calc(50% - var(--w) / 2 - 44px);
		}
		.lead {
			position: absolute;
			top: 8px;
			display: block;
			width: 36px;
			border-top: 1px dotted var(--lead);
		}
		.lead.l {
			right: calc(50% + var(--w) / 2 + 0.5px);
		}
		.lead.r {
			left: calc(50% + var(--w) / 2 + 0.5px);
		}
		.lead::after {
			content: '';
			position: absolute;
			top: -3px;
			width: 5px;
			height: 5px;
			border-radius: 50%;
			background: var(--lead);
		}
		.lead.l::after {
			right: -5px;
		}
		.lead.r::after {
			left: -5px;
		}
		li.lit .lead {
			border-top-color: var(--pic-accent);
		}
		li.lit .lead::after {
			background: var(--pic-accent);
		}
	}
</style>
