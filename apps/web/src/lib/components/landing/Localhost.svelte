<!--
  Localhost: your cloud machine above, your laptop below. On the machine,
  Vite's Local line from `pnpm dev` and the real tmux status bar with the
  ports `repose attach` forwards (⇄). Between them the forward itself, a
  `⇄ 5173` chip on a wire; below, the laptop's browser on localhost:5173
  showing the app the machine serves.

  READY is Vite's own line over it, as Vite 8.0.1 printed it for this
  repository's web app on the build box (2026-11-02), with the blank row
  Vite leaves under it; the machine's capture kept the Local row alone.
  LOCAL and BAR are verbatim `tmux capture-pane -p -e` output (2026-09-25):
  LOCAL from the dev window on the machine (its 21-column turbo prefix
  cropped), BAR the last row of the attached client on the laptop, its
  padding spaces replaced by a flexible gap and its first 10 columns (the
  session name, an internal name that tells a visitor nothing) cropped off
  the left, so it starts at the window list (I-397). The browser images are
  http://localhost:5173/ on this box while `repose attach recruiting` ran
  (2026-09-26), captured at 2x in light and dark: 720px viewport for the
  card, 640px for phones (static/landing/localhost-home-*.webp). The page
  before the forward is Chromium 153's own for http://localhost:5173/ with
  nothing listening ("This site can't be reached", "localhost refused to
  connect."), captured the same way on this box, 2026-10-11, in both
  schemes (static/landing/localhost-refused-*.webp, F17, I-669).

  Animated with anime.js (loaded in onMount): the dev server's Local line
  stays on the machine throughout; the laptop's browser is on
  localhost:5173 and shows Chromium's refused page, dimmed. The bar lists
  the forwarded ports, `⇄ 5173` appears under the bar and travels down to
  the laptop, the address bar's edge turns blue, the refused page fades
  out and the app fades in after it, so the two pages never show at once;
  it rests, then the app fades out and the refused page back in. Paused
  off-screen. Without JS, or under prefers-reduced-motion, the final state
  is shown still; turning reduced motion on while the page is open stops
  the loop there. The still frame is the loop's beat at its seam (SEAM),
  so a first cycle starting from it begins there (motion.svelte.ts).

  Each page's crop ends in the page's white space, whatever the pane's
  width: the pane is a size container, and the image's top is set so a
  chosen row of the capture (between two lines of text) lands on the
  pane's bottom edge, the row picked by the pane's aspect ratio. The
  app's page shows its nav, cropped under its rule: drawn at the window's
  width, the 48px headline under it set at about 34px, louder than the
  cell's own title, and a smaller drawing would take the nav under 11px;
  an empty strip of ground under the nav read as a page still loading.
  So the page's pane is as tall as that crop (140 capture rows) and the
  forward's wire takes the rest of the height (I-961).
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { theme } from '$lib/theme.svelte';
	import { watchReducedMotion } from './inview';
	import { Picture } from './motion.svelte';
	import PauseButton from './PauseButton.svelte';
	import WinHead from './WinHead.svelte';

	const LOCAL =
		'\u001b[35m@your-project/web:dev: \u001b[39m  \u001b[32m➜\u001b[39m  \u001b[1mLocal\u001b[0m:   \u001b[36mhttp://localhost:\u001b[1m5173\u001b[0m\u001b[36m/\u001b[39m';
	const BAR_LEFT = '0:shell- 1:dev*';
	// '⇄ 5173 5433 9101 │ "your-project" 19:38 25-Sep-26', split at the
	// forward that travels, and cropped after the host name at every width:
	// the clock and date clipped mid-date on a phone (F25).
	const BAR_PORT = ' 5173';
	const BAR_REST = ' 5433 9101 │ ';
	const BAR_HOST = '"your-project"';
	const ADDR = 'localhost:5173';

	// The capture's colours, mapped as the hero maps them (ops/dev/hero/convert.py).
	const BASE = [
		'var(--t-bg-2)',
		'var(--t-red)',
		'var(--t-green)',
		'var(--t-yellow)',
		'var(--t-blue)',
		'var(--t-magenta)',
		'var(--t-cyan)',
		'var(--t-fg)'
	];

	// An SGR escape: ESC [ params m.
	const SGR = new RegExp(`${String.fromCharCode(27)}\\[([0-9;]*)m`, 'g');

	type Seg = { text: string; style: string };
	function parse(line: string): Seg[] {
		const out: Seg[] = [];
		let fg = '';
		let bold = false;
		let pos = 0;
		const push = (text: string) => {
			if (!text) return;
			let style = fg ? `color:${fg};` : '';
			if (bold) style += 'font-weight:700;';
			out.push({ text, style });
		};
		for (const m of line.matchAll(SGR)) {
			push(line.slice(pos, m.index));
			pos = m.index + m[0].length;
			for (const p of (m[1] || '0').split(';').map(Number)) {
				if (p === 0) [fg, bold] = ['', false];
				else if (p === 1) bold = true;
				else if (p === 22) bold = false;
				else if (p >= 30 && p <= 37) fg = BASE[p - 30];
				else if (p === 39) fg = '';
			}
		}
		push(line.slice(pos));
		return out;
	}
	// Without the turbo prefix.
	const local = parse(LOCAL).slice(1);
	// Vite's ready line: VITE and its version in bold green, the rest dim
	// but the time, as Vite colours it.
	const ready: Seg[] = [
		{ text: '  VITE', style: 'color:var(--t-green);font-weight:700;' },
		{ text: ' v8.0.1', style: 'color:var(--t-green);' },
		{ text: '  ready in ', style: 'color:var(--t-dim);' },
		{ text: '1494', style: 'font-weight:700;' },
		{ text: ' ms', style: 'color:var(--t-dim);' }
	];

	let pic: HTMLDivElement;
	const picture = new Picture({ group: 'grid' });
	// The seam (ms): the frame equals the still frame here.
	const SEAM = 9900;
	let fire = $state(false);
	let typing = $state(false);

	onMount(() => {
		let dead = false;
		let visible = false;
		let tl: { pause(): unknown; play(): unknown } | null = null;
		// Bumped on each start and stop, so an anime.js import that resolves
		// after a stop does not start a loop.
		let run = 0;

		const q = (s: string) => Array.from(pic.querySelectorAll<HTMLElement>(s));
		const moved = () => q('.ports, .pic-chip, .home, .err, .wire');

		// Stop the loop and show the final state still: the markup's own
		// state, so every inline style anime.js wrote comes off.
		function still() {
			run++;
			picture.stop();
			rest();
		}
		// The still frame, with the loop kept registered: another grid
		// picture has the turn, or the loop is held before it has begun.
		function rest() {
			tl?.pause();
			tl = null;
			visible = false;
			for (const el of moved()) el.removeAttribute('style');
			fire = false;
			typing = false;
		}

		function start() {
			const me = ++run;
			import('animejs').then(({ createTimeline, utils }) => {
				if (dead || me !== run) return;

				// The dev server is up; no forwards yet, and the laptop's
				// browser already on localhost:5173, refused.
				function pre() {
					utils.set(q('.ports, .pic-chip, .home'), { opacity: 0 });
					utils.set(q('.err'), { opacity: 0.6 });
					utils.set(q('.pic-chip'), { x: 0, y: 0, scale: 1 });
					utils.set(q('.wire'), { scaleY: 0 });
					fire = false;
					typing = false;
				}

				function cycle(at = 0) {
					if (dead || me !== run) return;
					if (!visible) {
						rest();
						return;
					}
					pre();

					// The chip starts under the bar's ⇄ 5173, clear of the bar,
					// and travels to its place.
					const chip = pic.querySelector<HTMLElement>('.pic-chip')!;
					const port = pic.querySelector<HTMLElement>('.port')!;
					const a = port.getBoundingClientRect();
					const b = chip.getBoundingClientRect();
					const dx = a.left + a.width / 2 - (b.left + b.width / 2);
					const bar = pic.querySelector<HTMLElement>('.bar')!.getBoundingClientRect();
					const dy = Math.min(0, bar.bottom + 3 - b.top);

					const t = createTimeline({ autoplay: false, onComplete: () => cycle() })
						.add(q('.ports'), { opacity: [0, 1], duration: 350 }, 550)
						.add(chip, { x: dx, y: dy, duration: 16 }, 900)
						.call(() => (fire = true), 900)
						.add(chip, { opacity: [0, 1], scale: [0.85, 1], duration: 250, ease: 'outBack' }, 900)
						.add(chip, { x: [dx, 0], y: [dy, 0], duration: 900, ease: 'inOutCubic' }, 1150)
						.add(q('.wire'), { scaleY: [0, 1], duration: 260, ease: 'outCubic' }, 2000)
						// the forward lands: the address bar's edge turns blue and
						// the refused page reloads to the app
						.call(() => (typing = true), 2150)
						.call(() => (fire = false), 2300)
						// one page out, then the other in: never both at once
						.add(q('.err'), { opacity: 0, duration: 200, ease: 'inQuad' }, 2400)
						.add(q('.home'), { opacity: [0, 1], duration: 300, ease: 'outQuad' }, 2550)
						.call(() => (typing = false), 3300)
						// rest on the loaded page, then back to the refused page
						// not from a cycle started at its seam (Browser.svelte)
						.call(() => at || picture.looped(), SEAM - 1)
						.add(q('.ports, .pic-chip'), { opacity: 0, duration: 450, ease: 'inQuad' }, SEAM)
						.add(q('.home'), { opacity: 0, duration: 200, ease: 'inQuad' }, SEAM)
						.add(q('.err'), { opacity: 0.6, duration: 300, ease: 'outQuad' }, SEAM + 150)
						.add(q('.wire'), { scaleY: 0, duration: 300, ease: 'inQuad' }, SEAM)
						.add({ duration: 400 }, SEAM + 450);

					tl = t;
					if (at) t.seek(at, true);
					t.play();
				}

				picture.start(pic, (m, fromStill) => {
					visible = m === 'play';
					if (m === 'rest' || (!visible && !tl)) rest();
					else if (!visible) tl?.pause();
					else if (tl) tl.play();
					else cycle(fromStill ? SEAM : 0);
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

	// The captures follow the site's theme switch: the OS setting under
	// System, otherwise the choice, which a media query cannot see.
	const dark = (narrow: boolean) => {
		const width = narrow ? '(max-width: 479px)' : 'all';
		if (theme.choice === 'light') return 'not all';
		if (theme.choice === 'dark') return width;
		return narrow
			? '(max-width: 479px) and (prefers-color-scheme: dark)'
			: '(prefers-color-scheme: dark)';
	};
</script>

<!-- ⇄ drawn one cell wide: several monospace fonts lack the glyph, and the
     fallback is a different width. -->
{#snippet fw()}<svg class="fw" viewBox="0 0 10 16"
		><path d="M1 5h8M6.5 2.5L9 5 6.5 7.5M9 11.5H1M3.5 9L1 11.5 3.5 14" /></svg
	>{/snippet}

{#snippet shot(name: string, alt: string)}
	<picture>
		<source srcset="/landing/{name}-narrow-dark.webp" media={dark(true)} width="640" height="280" />
		<source
			srcset="/landing/{name}-narrow-light.webp"
			media="(max-width: 479px)"
			width="640"
			height="280"
		/>
		<source srcset="/landing/{name}-dark.webp" media={dark(false)} width="720" height="300" />
		<img
			src="/landing/{name}-light.webp"
			width="720"
			height="300"
			loading="lazy"
			decoding="async"
			{alt}
		/>
	</picture>
{/snippet}

<div class="shot shot-box" use:picture.frame>
	<PauseButton {picture} name="ports" />
	<div
		class="lh"
		bind:this={pic}
		role="img"
		aria-label="Your laptop's browser on localhost:5173 says the site can't be reached. Your cloud machine runs the Vite dev server on localhost:5173, and its tmux status bar lists port 5173 as forwarded. The forward reaches your laptop, and the browser reloads to the app served from the machine."
	>
		<div class="shot-win machine" aria-hidden="true">
			<WinHead who="machine" />
			<div class="term">
				<div class="tr">
					{#each ready as seg, j (j)}<span style={seg.style}>{seg.text}</span>{/each}
				</div>
				<div class="tr">&nbsp;</div>
				<div class="tr">
					{#each local as seg, j (j)}<span style={seg.style}>{seg.text}</span>{/each}
				</div>
				<div class="bar">
					<span class="left">{BAR_LEFT}</span>
					<span class="right"
						><span class="ports"
							><span class="port">{@render fw()}{BAR_PORT}</span><span>{BAR_REST}</span></span
						><span>{BAR_HOST}</span></span
					>
				</div>
			</div>
		</div>

		<div class="hop" aria-hidden="true">
			<i class="wire"></i>
			<span class="pic-chip" class:fire>{@render fw()}{BAR_PORT}</span>
			<i class="wire arrow"></i>
		</div>

		<div class="shot-win laptop" aria-hidden="true">
			<WinHead who="laptop"><span class="url" class:typing>{ADDR}</span></WinHead>
			<div class="page">
				<div class="err">
					{@render shot(
						'localhost-refused',
						"This site can't be reached: localhost refused to connect."
					)}
				</div>
				<div class="home">
					{@render shot('localhost-home', "The app's home page: Get to new roles on time.")}
				</div>
			</div>
		</div>
	</div>
</div>

<style>
	/* A size container. Not named `frame`: that is the site's frame
	   utility (layout.css), which would pad it to the page gutter. */
	.shot-box {
		container-type: inline-size;
	}
	.lh {
		position: relative;
		display: flex;
		flex-direction: column;
		height: 100%;
		padding: var(--shot-in) var(--shot-r, var(--shot-in)) var(--shot-in)
			var(--shot-l, var(--shot-in));
	}

	/* The two windows, the grid's own (landing.css, WinHead.svelte). */
	.shot-win {
		flex: none;
		font-size: 12px;
	}
	/* --min-w: the narrowest the captures are drawn. On a phone that is
	   wider than the window, so their text keeps 11px or more and the
	   window crops them on the right into a fade, as the Editor's rows
	   are cropped (review round 2, I-961). */
	.lh {
		--pw: 1440;
		--min-w: 0px;
	}
	@media (max-width: 479px) {
		.lh {
			--pw: 1280;
			--min-w: 480px;
		}
		.page {
			mask-image: linear-gradient(to right, #000 calc(100% - 32px), transparent);
		}
	}

	/* The laptop's browser on localhost:5173. */
	.url {
		flex: 1;
		min-width: 0;
		height: 20px;
		margin-left: 4px;
		padding: 0 8px;
		border: 1px solid var(--rule-strong);
		background: var(--surface);
		font-family: var(--font-mono);
		font-size: 12px;
		font-weight: 400;
		line-height: 18px;
		color: var(--pic-ink);
		white-space: nowrap;
		overflow: hidden;
		transition: border-color 0.18s;
	}
	.url.typing {
		border-color: var(--pic-accent);
	}
	/* The page's pane: 140 rows of the capture at the window's width
	   (the nav and its rule). */
	.page {
		position: relative;
		flex: none;
		height: calc(
			max(
					100cqw - var(--shot-l, var(--shot-in)) - var(--shot-r, var(--shot-in)) - 2px,
					var(--min-w)
				) *
				140 / var(--pw)
		);
		overflow: hidden;
		container-type: size;
	}
	.err,
	.home {
		position: absolute;
		inset: 0;
		overflow: hidden;
	}
	/* Each page on its capture's own ground, so a crop that starts above
	   the capture's top shows the page, not the window. */
	.err {
		background: #fff;
	}
	.home {
		background: #fafafa;
	}
	@media (prefers-color-scheme: dark) {
		:global(:root:not(.light):not([data-theme='light'])) .err {
			background: #1f2125;
		}
		:global(:root:not(.light):not([data-theme='light'])) .home {
			background: #09090b;
		}
	}
	:global(:root[data-theme='dark']) .err,
	:global(:root.dark) .err {
		background: #1f2125;
	}
	:global(:root[data-theme='dark']) .home,
	:global(:root.dark) .home {
		background: #09090b;
	}
	/* Refused, before the forward: the page is there, dimmed. */
	.err {
		opacity: 0.6;
	}
	/* The crop. Capture row --b (in the capture's own pixels, --iw wide)
	   lands on the pane's bottom edge, so the pane ends in white space
	   between two lines of text; --cap 0px keeps the capture's top from
	   coming below the pane's (no extra ground above it). The pane is 140
	   capture rows tall (.page), so one row serves every width.
	   Rows, from the captures: the app's nav ends at 85 (Job Alerts) and
	   its rule at 128, so the app ends at 140, under the rule; Chromium's refused page has its title at
	   308 and "refused to connect" at 385, so it ends at 405, under them.
	   The phone's (640px) captures share the app's rows, and Chromium's
	   sit 8 to 24 rows higher. */
	.page img {
		--dw: max(100cqw, var(--min-w));
		position: absolute;
		left: 0;
		top: min(var(--cap), calc(100cqh - var(--b) * var(--dw) / var(--iw)));
		display: block;
		width: var(--dw);
		max-width: none;
		height: auto;
	}
	.home img {
		--iw: 1440;
		--b: 140;
		--cap: 0px;
	}
	.err img {
		--iw: 1440;
		--b: 405;
		--cap: 9999px;
	}
	/* The phone's captures, 1280 pixels wide. */
	@media (max-width: 479px) {
		.page .home img {
			--iw: 1280;
		}
		.page .err img {
			--iw: 1280;
			--b: 395;
		}
	}
	/* The forward between them: its wire takes what the two windows
	   leave, the chip at its middle. */
	.hop {
		flex: 1;
		min-height: 30px;
		display: flex;
		flex-direction: column;
		align-items: center;
	}
	.wire {
		flex: 1;
		width: 0;
		border-left: 1.5px solid var(--pic-accent);
		transform-origin: center top;
	}
	.wire.arrow {
		position: relative;
	}
	.wire.arrow::after {
		content: '';
		position: absolute;
		left: -5.5px;
		bottom: 0;
		border: 5px solid transparent;
		border-top: 6px solid var(--pic-accent);
		border-bottom: 0;
	}
	.pic-chip {
		position: relative;
		z-index: 2;
		flex: none;
	}

	/* The machine's terminal: its tmux, as in the hero. */
	.term {
		background: var(--t-bg);
		color: var(--t-fg);
		font-family: var(--font-mono);
		font-size: 12px;
		font-variant-ligatures: none;
		line-height: 16px;
	}
	.tr {
		padding: 3px 8px;
		margin-left: -2ch;
	}
	.tr,
	.bar {
		white-space: nowrap;
		overflow: hidden;
	}
	.tr span,
	.bar span {
		white-space: pre;
	}
	.bar {
		display: flex;
		gap: 1ch;
		padding: 0 8px;
		border-top: 1px solid var(--rule);
		background: var(--t-bar);
		color: var(--t-bar-fg);
	}
	.bar .left {
		flex: none;
	}
	.bar .right {
		flex: 1;
		min-width: 0;
		overflow: hidden;
		white-space: nowrap;
		text-align: right;
	}
	.port {
		display: inline-block;
	}
	/* On a phone the bar keeps its right end, the forwards and the host
	   name; before the forwards arrive, the host name alone. */
	@container (max-width: 420px) {
		.bar .left {
			display: none;
		}
	}
	.fw {
		display: inline-block;
		width: 1ch;
		height: 16px;
		vertical-align: top;
		fill: none;
		stroke: currentColor;
		stroke-width: 1.3;
	}
	.pic-chip .fw {
		height: 18px;
		stroke-width: 1.5;
	}
	@media (max-width: 480px) {
		.url {
			font-size: 11px;
		}
	}
</style>
