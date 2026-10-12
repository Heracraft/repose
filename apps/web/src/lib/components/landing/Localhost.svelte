<!--
  Localhost: your cloud machine above, your laptop below. On the machine,
  Vite's Local line from `pnpm dev` and the real tmux status bar with the
  ports `repose attach` forwards (⇄). Between them the forward itself, a
  `⇄ 5173` chip on a wire; below, the laptop's browser on localhost:5173
  showing the app the machine serves.

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
  pane's bottom edge, the row picked by the pane's aspect ratio.
-->
<script lang="ts">
	import { onMount } from 'svelte';
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
		'#1c1c1c',
		'#e06c75',
		'#98c379',
		'#e5c07b',
		'#61afef',
		'#c678dd',
		'#56b6c2',
		'#dcdfe4'
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
</script>

<!-- ⇄ drawn one cell wide: several monospace fonts lack the glyph, and the
     fallback is a different width. -->
{#snippet fw()}<svg class="fw" viewBox="0 0 10 16"
		><path d="M1 6.5h8M6.5 4l2.5 2.5M9 10.5H1M3.5 13L1 10.5" /></svg
	>{/snippet}

{#snippet shot(name: string, alt: string)}
	<picture>
		<source
			srcset="/landing/{name}-narrow-dark.webp"
			media="(max-width: 479px) and (prefers-color-scheme: dark)"
			width="640"
			height="280"
		/>
		<source
			srcset="/landing/{name}-narrow-light.webp"
			media="(max-width: 479px)"
			width="640"
			height="280"
		/>
		<source
			srcset="/landing/{name}-dark.webp"
			media="(prefers-color-scheme: dark)"
			width="720"
			height="300"
		/>
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
	<PauseButton {picture} />
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

		<div class="shot-win open laptop" aria-hidden="true">
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
		padding: var(--shot-in) var(--shot-in) 0;
	}

	/* The two windows, the grid's own (landing.css, WinHead.svelte). */
	.shot-win {
		flex: none;
		font-size: 12px;
	}
	.laptop {
		flex: 1;
		min-height: 0;
	}

	/* The laptop's browser on localhost:5173. */
	.url {
		flex: 1;
		min-width: 0;
		height: 20px;
		margin-left: 4px;
		padding: 0 8px;
		border: 1px solid var(--rule-strong);
		border-radius: var(--radius);
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
	.page {
		position: relative;
		flex: 1;
		min-height: 0;
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
		.err {
			background: #1f2125;
		}
		.home {
			background: #09090b;
		}
	}
	/* Refused, before the forward: the page is there, dimmed. */
	.err {
		opacity: 0.6;
	}
	/* The crop. Capture row --b (in the capture's own pixels, --iw wide)
	   lands on the pane's bottom edge, so the pane ends in white space
	   between two lines of text; --cap 0px keeps the capture's top from
	   coming below the pane's (no extra ground above it). Which row
	   depends on how many capture rows the pane shows, its aspect ratio.
	   Rows, from the captures: the app's lines end at 85 (Job Alerts),
	   306 (the headline), 409 and 467 (the paragraph), 542 (Apply...);
	   Chromium's at 308 (the title), 385 (refused to connect), 452
	   (Try:), 555 (the buttons). The phone's (640px) captures share the
	   app's rows, and Chromium's sit 8 to 24 rows higher. */
	.page img {
		position: absolute;
		left: 0;
		top: min(var(--cap), calc(100cqh - var(--b) * 100cqw / var(--iw)));
		display: block;
		width: 100cqw;
		max-width: none;
		height: auto;
	}
	.home img {
		--iw: 1440;
		--b: 548;
		--cap: 0px;
	}
	.err img {
		--iw: 1440;
		--b: 575;
		--cap: 9999px;
	}
	@container (min-aspect-ratio: 2667/1000) {
		.err img {
			--b: 465;
		}
	}
	@container (min-aspect-ratio: 2835/1000) {
		.home img {
			--b: 490;
		}
	}
	@container (min-aspect-ratio: 3165/1000) {
		.home img {
			--b: 430;
			--cap: 9999px;
		}
	}
	@container (min-aspect-ratio: 2939/1000) {
		.err img {
			--b: 405;
		}
	}
	@container (min-aspect-ratio: 3692/1000) {
		.home img {
			--b: 350;
		}
	}
	@container (min-aspect-ratio: 4645/1000) {
		.home img {
			--b: 320;
		}
	}
	/* The phone's captures, 1280 pixels wide. */
	@media (max-width: 479px) {
		.page .home img {
			--iw: 1280;
			--b: 548;
			--cap: 0px;
		}
		.page .err img {
			--iw: 1280;
			--b: 545;
		}
	}
	@container (min-aspect-ratio: 2462/1000) {
		@media (max-width: 479px) {
			.page .err img {
				--b: 453;
			}
		}
	}
	@container (min-aspect-ratio: 2520/1000) {
		@media (max-width: 479px) {
			.page .home img {
				--b: 490;
			}
		}
	}
	@container (min-aspect-ratio: 2813/1000) {
		@media (max-width: 479px) {
			.page .home img {
				--b: 430;
				--cap: 9999px;
			}
		}
	}
	@container (min-aspect-ratio: 2723/1000) {
		@media (max-width: 479px) {
			.page .err img {
				--b: 395;
			}
		}
	}
	@container (min-aspect-ratio: 3282/1000) {
		@media (max-width: 479px) {
			.page .home img {
				--b: 350;
			}
		}
	}
	@container (max-width: 420px) {
		/* room under the bar for the chip to start clear of it */
		.lh .hop {
			height: 36px;
		}
	}

	/* The forward between them. */
	.hop {
		flex: none;
		display: flex;
		flex-direction: column;
		align-items: center;
		height: 30px;
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
		background: #0d0d0c;
		color: #d4d4d0;
		font-family: var(--font-mono);
		font-size: 11px;
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
		background: #1f9d55;
		color: #0b0b0b;
	}
	.bar .left {
		flex: none;
	}
	.bar .right {
		flex: 1;
		min-width: 0;
		overflow: hidden;
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
