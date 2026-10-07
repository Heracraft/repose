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
  card, 640px for phones (static/landing/localhost-home-*.webp).

  Animated with anime.js (loaded in onMount): the Local line appears, the
  bar lists the forwarded ports, `⇄ 5173` lifts out of the bar and travels
  down to the laptop, the address bar fills and the page loads; it rests,
  then goes again. Paused off-screen. Without JS, or under
  prefers-reduced-motion, the final state is shown still; turning reduced
  motion on while the page is open stops the loop there.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { watchReducedMotion } from './inview';

	const LOCAL =
		'\u001b[35m@job-alerts/web:dev: \u001b[39m  \u001b[32m➜\u001b[39m  \u001b[1mLocal\u001b[0m:   \u001b[36mhttp://localhost:\u001b[1m5173\u001b[0m\u001b[36m/\u001b[39m';
	const BAR_LEFT = '0:shell- 1:dev*';
	// '⇄ 5173 5433 9101 │ "job-alerts" 19:38 25-Sep-26', split at the
	// forward that travels.
	const BAR_PORT = ' 5173';
	const BAR_REST = ' 5433 9101 │ "job-alerts" ';
	const BAR_CLOCK = '19:38 25-Sep-26';
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
	let fire = $state(false);
	let typing = $state(false);

	onMount(() => {
		let dead = false;
		let visible = false;
		let tl: { pause(): unknown; play(): unknown } | null = null;
		let io: IntersectionObserver | undefined;
		// Bumped on each start and stop, so an anime.js import that resolves
		// after a stop does not start a loop.
		let run = 0;

		const q = (s: string) => Array.from(pic.querySelectorAll<HTMLElement>(s));
		const moved = () => q('.tr, .ports, .chip, .addr, .page, .wire');

		// Stop the loop and show the final state still: the markup's own
		// state, so every inline style anime.js wrote comes off.
		function still() {
			run++;
			io?.disconnect();
			io = undefined;
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

				// Before the dev server starts: no Local line, no forwards, an
				// empty browser.
				function pre() {
					utils.set(q('.tr, .ports, .chip, .addr, .page'), { opacity: 0 });
					utils.set(q('.chip'), { x: 0, y: 0, scale: 1 });
					utils.set(q('.wire'), { scaleY: 0 });
					fire = false;
					typing = false;
				}
				pre();

				function cycle() {
					if (dead || me !== run) return;
					if (!visible) {
						tl = null;
						return;
					}
					pre();

					// The chip starts over the bar's ⇄ 5173 and travels to its place.
					const chip = pic.querySelector<HTMLElement>('.chip')!;
					const port = pic.querySelector<HTMLElement>('.port')!;
					const a = port.getBoundingClientRect();
					const b = chip.getBoundingClientRect();
					const dx = a.left + a.width / 2 - (b.left + b.width / 2);
					const dy = a.top + a.height / 2 - (b.top + b.height / 2);

					const t = createTimeline({ autoplay: false, onComplete: () => cycle() })
						.add(q('.tr'), { opacity: [0, 1], duration: 350 }, 500)
						.add(q('.ports'), { opacity: [0, 1], duration: 350 }, 1100)
						.set(chip, { x: dx, y: dy }, 1700)
						.call(() => (fire = true), 1700)
						.add(chip, { opacity: [0, 1], scale: [0.85, 1], duration: 250, ease: 'outBack' }, 1700)
						.add(chip, { x: [dx, 0], y: [dy, 0], duration: 900, ease: 'inOutCubic' }, 1950)
						.add(q('.wire'), { scaleY: [0, 1], duration: 260, ease: 'outCubic' }, 2800)
						.call(() => (fire = false), 3100)
						.call(() => (typing = true), 2950)
						.call(() => (typing = false), 3750)
						// The address arrives as one fade: a character a frame was
						// a flicker, not typing (I-397).
						.add(q('.addr'), { opacity: [0, 1], duration: 300, ease: 'outQuad' }, 3000)
						.add(q('.page'), { opacity: [0, 1], duration: 500, ease: 'outQuad' }, 3700)
						// rest on the loaded page, then clear and go again
						.add(
							q('.tr, .ports, .chip, .addr, .page'),
							{ opacity: 0, duration: 450, ease: 'inQuad' },
							10700
						)
						.add(q('.wire'), { scaleY: 0, duration: 300, ease: 'inQuad' }, 10700)
						.add({ duration: 400 }, 11150);

					tl = t;
					t.play();
				}

				io = new IntersectionObserver(
					([e]) => {
						visible = e.isIntersecting;
						if (visible) {
							if (tl) tl.play();
							else cycle();
						} else {
							tl?.pause();
						}
					},
					{ threshold: 0.5 }
				);
				io.observe(pic);
			});
		}

		const unwatch = watchReducedMotion((reduce) => {
			if (reduce) still();
			else start();
		});

		return () => {
			dead = true;
			unwatch();
			io?.disconnect();
			tl?.pause();
		};
	});
</script>

<!-- ⇄ drawn one cell wide: several monospace fonts lack the glyph, and the
     fallback is a different width. -->
{#snippet fw()}<svg class="fw" viewBox="0 0 10 16"
		><path d="M1 6.5h8M6.5 4l2.5 2.5M9 10.5H1M3.5 13L1 10.5" /></svg
	>{/snippet}

<div class="shot frame">
	<div
		class="lh"
		bind:this={pic}
		role="img"
		aria-label="Your cloud machine runs the Vite dev server on localhost:5173, and its tmux status bar lists port 5173 as forwarded. The forward reaches your laptop, whose browser opens localhost:5173 and shows the app served from the machine."
	>
		<div class="win machine" aria-hidden="true">
			<div class="title">
				<svg viewBox="0 0 24 24" width="16" height="16" class="mark">
					<path
						d="M7 18.5h10.5a4 4 0 0 0 .6-7.96A5.5 5.5 0 0 0 7.4 9.1 4.7 4.7 0 0 0 7 18.5z"
						fill="none"
						stroke="currentColor"
						stroke-width="1.5"
						stroke-linejoin="round"
					/>
				</svg>
				<span class="who">your cloud machine</span><i class="dot"></i>
			</div>
			<div class="term">
				<div class="tr">
					{#each local as seg, j (j)}<span style={seg.style}>{seg.text}</span>{/each}
				</div>
				<div class="bar">
					<span class="left">{BAR_LEFT}</span>
					<span class="right"
						><span class="ports"
							><span class="port">{@render fw()}{BAR_PORT}</span><span>{BAR_REST}</span><span
								class="clock">{BAR_CLOCK}</span
							></span
						></span
					>
				</div>
			</div>
		</div>

		<div class="hop" aria-hidden="true">
			<i class="wire"></i>
			<span class="chip" class:fire>{@render fw()}{BAR_PORT}</span>
			<i class="wire arrow"></i>
		</div>

		<div class="win laptop" aria-hidden="true">
			<div class="title">
				<svg viewBox="0 0 24 24" width="16" height="16" class="mark">
					<g fill="none" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round">
						<rect x="4.5" y="5" width="15" height="10.5" rx="1" />
						<path d="M2 18.5h20" stroke-linecap="round" />
					</g>
				</svg>
				<span class="who">your laptop</span>
				<span class="url" class:typing><span class="addr">{ADDR}</span></span>
			</div>
			<div class="page">
				<picture>
					<source
						srcset="/landing/localhost-home-narrow-dark.webp"
						media="(max-width: 479px) and (prefers-color-scheme: dark)"
						width="640"
						height="280"
					/>
					<source
						srcset="/landing/localhost-home-narrow-light.webp"
						media="(max-width: 479px)"
						width="640"
						height="280"
					/>
					<source
						srcset="/landing/localhost-home-dark.webp"
						media="(prefers-color-scheme: dark)"
						width="720"
						height="300"
					/>
					<img
						src="/landing/localhost-home-light.webp"
						width="720"
						height="300"
						loading="lazy"
						decoding="async"
						alt="The app's home page: Get to new roles on time."
					/>
				</picture>
			</div>
		</div>
	</div>
</div>

<style>
	.frame {
		container-type: inline-size;
	}
	.lh {
		position: relative;
		display: flex;
		flex-direction: column;
		height: 100%;
		padding: 12px 12px 0;
	}

	/* The two windows, as in "Your working state". */
	.win {
		flex: none;
		display: flex;
		flex-direction: column;
		border: 1px solid var(--rule-strong);
		border-radius: 3px;
		background: var(--surface);
		overflow: hidden;
		font-size: 12px;
		color: var(--pic-ink);
	}
	.laptop {
		flex: 1;
		min-height: 0;
		border-bottom: 0;
		border-radius: 3px 3px 0 0;
	}
	.title {
		flex: none;
		display: flex;
		align-items: center;
		gap: 7px;
		height: 27px;
		padding: 0 10px;
		border-bottom: 1px solid var(--rule);
		background: var(--sunken);
	}
	.mark {
		flex: none;
		color: var(--pic-ink);
	}
	.who {
		flex: none;
		font-weight: 600;
	}
	.dot {
		width: 7px;
		height: 7px;
		margin-left: auto;
		border-radius: 50%;
		background: var(--color-emerald-500);
	}

	/* The laptop's browser on localhost:5173. */
	.url {
		flex: 1;
		min-width: 0;
		height: 20px;
		margin-left: 6px;
		padding: 0 8px;
		border: 1px solid var(--rule-strong);
		border-radius: 2px;
		background: var(--surface);
		font-family: var(--font-mono);
		font-size: 12px;
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
		flex: 1;
		min-height: 0;
		overflow: hidden;
	}
	.page img {
		display: block;
		width: 100%;
		max-width: none;
		height: auto;
		/* the app's own header padding, cropped */
		margin-top: -1.4%;
	}
	@container (max-width: 420px) {
		.page img {
			margin-top: 0;
		}
	}

	/* The forward between them. */
	.hop {
		flex: none;
		display: flex;
		flex-direction: column;
		align-items: center;
		height: 36px;
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
	.chip {
		position: relative;
		z-index: 2;
		flex: none;
		padding: 0 7px;
		border: 1px solid var(--pic-accent);
		border-radius: 3px;
		background: var(--surface);
		font-family: var(--font-mono);
		font-size: 12px;
		font-weight: 600;
		line-height: 18px;
		color: var(--pic-accent);
		white-space: pre;
		transition:
			background-color 0.18s,
			color 0.18s;
	}
	.chip.fire {
		background: var(--pic-accent);
		color: var(--surface);
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
	/* At card width the bar is cropped after the host name; on a phone,
	   without the left end, after the clock. */
	.bar .clock {
		display: none;
	}
	@container (max-width: 420px) {
		.bar .left {
			display: none;
		}
		.bar .right {
			text-align: left;
		}
		.bar .clock {
			display: inline;
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
	.chip .fw {
		height: 18px;
		stroke-width: 1.5;
	}
</style>
