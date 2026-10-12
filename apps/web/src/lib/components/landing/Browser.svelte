<!--
  The agent's browser, and you in it. A real run on the `recruiting` repose
  machine (the owner's job-alerts app), 2026-09-26.

  Top: the machine's Chromium as the desktop shows it, captured through
  the desktop viewer (noVNC canvas, 1x; the command is `repose browser`
  since I-292) while the run below happened: the
  toolbar and the page, cropped out of the window (static/landing/browser-*).
  The window was 586px wide at 125% zoom so the text survives the scale-down.
  States, in the order they happened: about:blank; /feedback loaded; the bug
  report typed into the text box; Bug selected; then, from the desktop view
  on this box (the laptop side), a click in the text box and " Only with the
  Backend filter on." typed by hand (three captures while typing). The agent
  read that sentence back with its next page snapshot.

  Bottom: rows of that agent session (Claude Code with the playwright MCP
  server, on the machine in tmux), verbatim `tmux capture-pane -p -e -J` of
  its transcript view (ctrl+o): the prompt, and for each browser call the
  call row and the Playwright code or result row under it, cropped at the
  frame's right edge; the rows between them (### headings, ``` fences, page
  snapshots, a click that timed out on the hidden radio) are cropped out.
  OSC 8 hyperlink wrappers are dropped; they carry no visible text.

  Both stand in the grid's window under "your cloud machine" (WinHead,
  I-669): the browser on top, the log under its viewport, so the page's
  tabs are never under the log and the log's rows crop on the right as a
  terminal's do (F18). The pans stop where the crop's edges fall between
  the page's rows (the Type tabs, Write and Record, the text box's top),
  never through them.
  The captures are the light scheme's only: they came from the owner's
  machine, which is not reachable from here, so there is no dark set to
  swap in under dark mode (F47, I-669); in the dark scheme the toolbar and
  page are dimmed (brightness 0.86) to sit beside the dark windows.

  The loop opens on about:blank for 400ms and ends on it: at the seam the
  page fades to the blank page under it and the toolbar cuts back to
  about:blank, so the last frame equals the first. Captures that do not
  line up (the page scrolled between them, the toolbar's URL) swap in one
  frame instead of crossfading. The still frame is the loop's last beat
  before the seam, so a first cycle starting from it begins at the seam.

  The pan of the page, the blue focus box, the command chip and the pointer
  are the illustration; everything inside the page and terminal is captured.
  anime.js plays it when the card is on screen; without JS, or under
  prefers-reduced-motion, the last beat is shown still, and turning reduced
  motion on while the page is open stops the loop there. Everything moves
  by translate, scale and opacity (I-397).
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { watchReducedMotion } from './inview';
	import { Picture } from './motion.svelte';
	import PauseButton from './PauseButton.svelte';
	import WinHead from './WinHead.svelte';

	const ROWS = [
		'\u001b[38;5;239m\u001b[48;5;237m❯ \u001b[38;5;231mTest the feedback form at http://localhost:5173/feedback with the playwright browser tools, in the browser window that is already open. Choose Bug, type a one-line bug report into the text box, and\u001b[39m',
		'\u001b[38;5;114m●\u001b[39m \u001b[1mplaywright - Navigate to a URL (MCP)\u001b[0m(url: "http://localhost:5173/feedback")',
		"     await page.goto('\u001b[94mhttp://localhost:5173/feedback\u001b[39m');",
		'\u001b[38;5;114m●\u001b[39m \u001b[1mplaywright - Type text (MCP)\u001b[0m(target: "f1e32", element: "Your feedback textbox", text: "The Roles page filter resets when I navigate back from a role.")',
		"     await page.getByRole('textbox', { name: 'Your feedback' }).fill('The Roles page filter resets when I navigate back from a role.');",
		'\u001b[38;5;114m●\u001b[39m \u001b[1mplaywright - Get console messages (MCP)\u001b[0m(level: "warning", all: true)',
		'     Total messages: 2 (Errors: 0, Warnings: 0)',
		'\u001b[38;5;114m●\u001b[39m \u001b[1mplaywright - Click (MCP)\u001b[0m(target: "f1e18", element: "Bug label")',
		"     await page.getByText('Bug').click();"
	];

	// The xterm-256 and bright colours this capture uses.
	const X256: Record<number, string> = {
		114: '#87d787',
		231: '#ffffff',
		237: '#3a3a3a',
		239: '#8a8a8a',
		246: '#949494'
	};
	const BRIGHT_BLUE = '#8fb3ff';

	// An SGR escape: ESC [ params m.
	const SGR = new RegExp(`${String.fromCharCode(27)}\\[([0-9;]*)m`, 'g');

	type Seg = { text: string; style: string };
	type Row = { segs: Seg[]; bg: string };
	function parse(line: string): Row {
		const segs: Seg[] = [];
		let fg = '';
		let rowBg = '';
		let bold = false;
		let pos = 0;
		const push = (text: string) => {
			if (!text) return;
			let style = fg ? `color:${fg};` : '';
			if (bold) style += 'font-weight:700;color:' + (fg || '#f2f2ef') + ';';
			segs.push({ text, style });
		};
		for (const m of line.matchAll(SGR)) {
			push(line.slice(pos, m.index));
			pos = m.index + m[0].length;
			const ps = (m[1] || '0').split(';').map(Number);
			for (let i = 0; i < ps.length; i++) {
				const p = ps[i];
				if (p === 0) [fg, bold] = ['', false];
				else if (p === 1) bold = true;
				else if (p === 22) bold = false;
				else if (p === 39) fg = '';
				else if (p === 94) fg = BRIGHT_BLUE;
				else if (p === 38 && ps[i + 1] === 5) {
					fg = X256[ps[i + 2]] ?? '';
					i += 2;
				} else if (p === 48 && ps[i + 1] === 5) {
					rowBg = X256[ps[i + 2]] ?? '';
					i += 2;
				}
			}
		}
		push(line.slice(pos));
		return { segs, bg: rowBg };
	}
	const rows = ROWS.map(parse);

	// Geometry, in capture pixels of the 574px-wide crops. The toolbar is
	// 48px tall; the page crops start right under it.
	const TB = 48;
	const URLBAR = { x: 127, y: 6, w: 362, h: 35 };
	const TEXTBOX = { x: 29, y: 204, w: 511, h: 209 };
	const BUG = { x: 28, y: 1, w: 67, h: 37 };
	// Where the pointer is drawn: in the text box, under the text (the real
	// click, at y 350, sits below the frame's crop).
	const POINTER = { x: 330, y: 296 };
	// Where the page sits, in capture pixels from its top: each stop puts
	// the viewport's top edge in the page's white space (nav: under the
	// app's name, above its links; typed and you: under "Say it your way",
	// above Write and Record; bug: 12 capture pixels above the page's top,
	// so the ring round Bug clears the toolbar's line by 8 instead of
	// sitting on it, over the page's own ground), and at typed and you
	// the chip lands on the page's empty right side, over no text.
	const PAN = { nav: 52, typed: 120, bug: -12, you: 120 };
	// The seam (ms): the beat where the frame equals the still frame, and
	// where a first cycle starts when the still frame is on screen.
	const SEAM = 17200;
	// A swap: two captures that do not line up (the page scrolled between
	// them, or the toolbar's text changed) change in one frame, since a
	// crossfade shows both texts at once.
	const SWAP = 16;
	// The opacity an older log row rests at. At 0.7 the dimmest captured
	// text colour (#8fb3ff) holds 5.0:1 on #0d0d0c and the prompt's white
	// 6.9:1 on its own #3a3a3a; 0.45 left rows at 2.7 to 4.2:1 (I-397).
	// Only the prompt's ❯ glyph (#8a8a8a on #3a3a3a, 3.3:1 at full
	// opacity) stays under 4.5:1: the capture's colour, kept (I-392).
	const REST = 0.7;

	const label =
		"The agent's browser on a cloud machine above the agent's log, and your pointer taking over through repose browser.";
	const story =
		"The agent opens the app's feedback page, types a bug report into the text box, reads the console (0 errors, 0 warnings) " +
		'and clicks Bug; each Playwright call in the log changes the page above it. Then repose browser shows the same browser ' +
		'on your laptop, and your pointer clicks into the text box and adds a sentence.';
	const descId = $props.id();

	// The focus ring, padded 4 capture pixels round what it marks.
	const ring = (r: { x: number; y: number; w: number; h: number }, pan: number) => ({
		'--rx': r.x - 4,
		'--ry': TB + r.y - pan - 4,
		'--rw': r.w + 8,
		'--rh': r.h + 8
	});

	let pic: HTMLDivElement;
	const picture = new Picture({ group: 'grid' });

	onMount(() => {
		let dead = false;
		let visible = false;
		let tl: { pause(): unknown; play(): unknown } | null = null;
		// Bumped on each start and stop, so an anime.js import that resolves
		// after a stop does not start a loop.
		let run = 0;

		const q = (s: string) => Array.from(pic.querySelectorAll<HTMLElement>(s));
		const one = (s: string) => pic.querySelector<HTMLElement>(s)!;
		const moved = () => [pic, ...q('.layer, .ring, .chip, .pointer, .rows, .tr')];
		// The style attributes the markup gave them (a row's captured
		// background), so a stop puts back exactly those.
		const markup = new Map(moved().map((el) => [el, el.getAttribute('style')]));

		// Stop the loop and show the last beat still: the markup's own state,
		// so every inline style anime.js wrote comes off.
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
			for (const [el, style] of markup) {
				if (style === null) el.removeAttribute('style');
				else el.setAttribute('style', style);
			}
			one('.chip').classList.remove('fire');
			q('.tr').forEach((r) => r.classList.remove('new'));
		}

		function start() {
			const me = ++run;
			import('animejs').then(({ createTimeline, utils, steps }) => {
				if (dead || me !== run) return;

				const rowEls = q('.tr');
				const list = one('.rows');
				const rowH = () => rowEls[0].getBoundingClientRect().height;
				// Keep row `last` at the bottom of the terminal.
				const offset = (last: number) => (rowEls.length - 1 - last) * rowH();

				function pre() {
					utils.set(q('.layer'), { opacity: 0 });
					utils.set(q('.l-blank, .tb-blank'), { opacity: 1 });
					utils.set(pic, { '--pan': PAN.nav });
					utils.set(one('.ring'), { opacity: 0, ...ring(URLBAR, TB) });
					utils.set(one('.chip'), { opacity: 0, scale: 0.85 });
					one('.chip').classList.remove('fire');
					utils.set(one('.pointer'), { opacity: 0 });
					utils.set(rowEls, { opacity: 0 });
					utils.set(rowEls[0], { opacity: 1 });
					utils.set(list, { y: offset(0) });
					rowEls.forEach((r) => r.classList.remove('new'));
				}

				function cycle(at = 0) {
					if (dead || me !== run) return;
					if (!visible) {
						rest();
						return;
					}
					pre();

					const t = createTimeline({ autoplay: false, onComplete: () => cycle() });

					// A browser call lands in the log: its two rows rise in, the
					// older rows dim, the call row is marked while it acts.
					const log = (first: number, at: number) => {
						t.call(() => {
							rowEls.forEach((r) => r.classList.remove('new'));
							rowEls[first].classList.add('new');
						}, at)
							// a terminal scrolls a whole row at a time
							.add(list, { y: offset(first + 1), duration: 240, ease: steps(2) }, at)
							.add(rowEls.slice(0, first), { opacity: REST, duration: 400, ease: 'outQuad' }, at)
							.add([rowEls[first], rowEls[first + 1]], { opacity: [0, 1], duration: 380 }, at + 60);
					};
					const box = (
						r: { x: number; y: number; w: number; h: number },
						pan: number,
						at: number
					) => t.add(one('.ring'), { ...ring(r, pan), duration: 700, ease: 'inOutCubic' }, at);
					const show = (sel: string, at: number, duration = 300) =>
						t.add(q(sel), { opacity: [0, 1], duration, ease: 'outQuad' }, at);
					const swap = (sel: string, at: number, to = 1) =>
						t.add(q(sel), { opacity: to, duration: SWAP }, at);
					const pan = (to: number, at: number, duration = 800) =>
						t.add(pic, { '--pan': to, duration, ease: 'inOutCubic' }, at);

					// 1. Navigate: the page loads 250ms in (about:blank shows
					// 400ms in all, with the seam's last 150).
					log(1, 50);
					t.add(one('.ring'), { opacity: [0, 1], duration: 250 }, 100);
					swap('.tb-live', 250);
					show('.l-nav', 250, 350);

					// 2. Type into the text box (the page scrolled it into view,
					// and the pan moves with the ring).
					log(3, 2900);
					box(TEXTBOX, PAN.typed, 3000);
					pan(PAN.typed, 3000, 700);
					swap('.l-typed', 3100);

					// 3. Read the console: nothing on the page moves.
					log(5, 4900);
					t.add(one('.ring'), { opacity: 0, duration: 300, ease: 'inQuad' }, 5000);

					// 4. Click Bug.
					log(7, 6600);
					pan(PAN.bug, 6700, 800);
					t.add(one('.ring'), { ...ring(BUG, PAN.bug), duration: SWAP }, 7500).add(
						one('.ring'),
						{ opacity: [0, 1], duration: 250 },
						7520
					);
					swap('.l-bug', 7800);

					// 5. You, from the laptop: the desktop view opens on the same
					// window and your pointer types into it.
					t.add(one('.ring'), { opacity: 0, duration: 300, ease: 'inQuad' }, 8800);
					rowEls.forEach((r) => t.call(() => r.classList.remove('new'), 8800));
					t.add(rowEls, { opacity: REST, duration: 500, ease: 'outQuad' }, 8800);
					t.add(
						one('.chip'),
						{ opacity: [0, 1], scale: [0.85, 1], duration: 320, ease: 'outBack' },
						9000
					);
					t.call(() => one('.chip').classList.add('fire'), 9450);
					t.call(() => one('.chip').classList.remove('fire'), 9950);
					pan(PAN.you, 9300, 900);

					const k = pic.querySelector('.br')!.getBoundingClientRect().width / 574;
					const c = one('.chip').getBoundingClientRect();
					const b = pic.querySelector('.br')!.getBoundingClientRect();
					const sx = (c.left - b.left + c.width * 0.3) / k;
					const sy = (c.bottom - b.top - 4) / k;
					const ex = POINTER.x;
					const ey = TB + POINTER.y - PAN.you;
					t.add(one('.pointer'), { '--px': sx, '--py': sy, duration: SWAP }, 0)
						.add(one('.pointer'), { opacity: [0, 1], duration: 200 }, 10000)
						.add(
							one('.pointer'),
							{ '--px': ex, '--py': ey, duration: 900, ease: 'inOutCubic' },
							10050
						)
						.add(one('.pointer'), { scale: [1, 0.86, 1], duration: 220, ease: 'outQuad' }, 11000);
					show('.l-t0', 11080, 200);
					show('.l-t1', 11700, 200);
					show('.l-t2', 12400, 200);

					// Rest on it, then go back to the first frame: the page and
					// the log's rows fade, the blank page (under them all along)
					// shows, and the toolbar and log cut to the first frame's,
					// so the last frame equals the first.
					// The pages under the top one go first, unseen, so the fade
					// shows no older page through it.
					// A cycle started at its seam has no seam of its own to
					// report (and anime.js runs a muted seek's callbacks on
					// the first tick).
					t.call(() => at || picture.looped(), SEAM - 1);
					swap('.l-nav, .l-typed, .l-bug, .l-t0, .l-t1', SEAM, 0);
					t.add(
						q('.l-t2, .chip, .pointer'),
						{ opacity: 0, duration: 450, ease: 'inQuad' },
						SEAM
					).add(rowEls, { opacity: 0, duration: 450, ease: 'inQuad' }, SEAM);
					swap('.tb-live', SEAM + 450, 0);
					t.add(list, { y: offset(0), duration: SWAP }, SEAM + 450).add(
						rowEls[0],
						{ opacity: 1, duration: 150, ease: 'outQuad' },
						SEAM + 450
					);

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

<div class="shot shot-box" use:picture.frame>
	<PauseButton {picture} />
	<p id={descId} class="sr-only">{story}</p>
	<div class="stage" role="img" aria-label={label} aria-describedby={descId} bind:this={pic}>
		<div class="shot-win open bw" aria-hidden="true">
			<WinHead who="machine" />
			<div class="br">
				<div class="tb">
					<img
						class="layer tb-blank"
						src="/landing/browser-tb-blank.webp"
						width="574"
						height="48"
						loading="lazy"
						decoding="async"
						alt=""
					/>
					<img
						class="layer tb-live on"
						src="/landing/browser-tb.webp"
						width="574"
						height="48"
						loading="lazy"
						decoding="async"
						alt=""
					/>
				</div>
				<div class="vp">
					<div class="pg">
						<div class="layer l-blank"></div>
						<img
							class="layer l-nav"
							src="/landing/browser-p-nav.webp"
							width="574"
							height="536"
							loading="lazy"
							decoding="async"
							alt=""
						/>
						<img
							class="layer l-typed"
							src="/landing/browser-p-typed.webp"
							width="574"
							height="536"
							loading="lazy"
							decoding="async"
							alt=""
						/>
						<img
							class="layer l-bug"
							src="/landing/browser-p-bug.webp"
							width="574"
							height="536"
							loading="lazy"
							decoding="async"
							alt=""
						/>
						<img
							class="layer l-t0"
							src="/landing/browser-p-t0.webp"
							width="574"
							height="536"
							loading="lazy"
							decoding="async"
							alt=""
						/>
						<img
							class="layer l-t1"
							src="/landing/browser-p-t1.webp"
							width="574"
							height="536"
							loading="lazy"
							decoding="async"
							alt=""
						/>
						<img
							class="layer l-t2 on"
							src="/landing/browser-p-t2.webp"
							width="574"
							height="536"
							loading="lazy"
							decoding="async"
							alt=""
						/>
					</div>
				</div>
				<i class="ring"><i class="rt"></i><i class="rb"></i><i class="rl"></i><i class="rr"></i></i>
				<span class="chip">repose browser</span>
				<svg
					class="pointer"
					viewBox="0 0 12 18"
					aria-hidden="true"
					style:--px={POINTER.x}
					style:--py={TB + POINTER.y - PAN.you}
				>
					<path
						d="M1 1v13.2l3.3-3.1 2.2 5.1 2.1-.9-2.2-5h4.6z"
						fill="#111"
						stroke="#fff"
						stroke-width="1.1"
						stroke-linejoin="round"
					/>
				</svg>
			</div>
			<div class="term">
				<div class="clip">
					<div class="rows">
						{#each rows as row, i (i)}
							<div class="tr" style={row.bg ? `background:${row.bg}` : ''}>
								{#each row.segs as seg, j (j)}<span style={seg.style}>{seg.text}</span>{/each}
							</div>
						{/each}
					</div>
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
	.stage {
		/* The chip sits on the captured page, which is white in either
		   scheme, so it keeps the light scheme's --pic-accent value: the
		   dark scheme's is 2.5:1 on #fff. */
		--chip: var(--color-blue-600);
		/* The log is dark in either scheme, so its acting row's bar keeps
		   the dark scheme's --pic-accent value; the ring, on the white
		   page, takes --chip's. A mark over a capture follows the
		   capture's ground, not the page's scheme. */
		--on-log: var(--color-blue-400);
		/* one capture pixel, at the window's inner width (.bw) */
		--k: calc(100cqw / 574);
		--pan: 120;
		position: relative;
		display: flex;
		flex-direction: column;
		height: 100%;
		padding: var(--shot-in) var(--shot-in) 0;
	}
	.bw {
		flex: 1;
		min-height: 0;
		container-type: inline-size;
	}

	/* The browser: flush with the frame's top and sides, a crop of the window. */
	.br {
		position: relative;
		flex: 1;
		min-height: 0;
		display: flex;
		flex-direction: column;
		overflow: hidden;
		background: #fff;
	}
	.tb {
		position: relative;
		flex: none;
		height: calc(var(--k) * 48);
		border-bottom: 1px solid #d9dce0;
		background: #fff;
	}
	/* The page's own ground (#fafafa in the captures), so the Bug stop's
	   12 pixels above the page's top read as the page. */
	.vp {
		position: relative;
		flex: 1;
		overflow: hidden;
		background: #fafafa;
	}
	/* The captures are the light scheme's only (I-669), so in the dark
	   scheme the page and toolbar are dimmed to sit beside the dark
	   windows instead of glaring white; the ring, the chip and the pointer
	   are drawn over them undimmed. */
	@media (prefers-color-scheme: dark) {
		.tb,
		.vp {
			filter: brightness(0.86);
		}
	}
	/* Panned with translate, not top: a pan moves no layout box, so it
	   counts for nothing in CLS (I-397). */
	.pg {
		position: absolute;
		left: 0;
		right: 0;
		top: 0;
		height: calc(var(--k) * 536);
		translate: 0 calc(var(--k) * var(--pan) * -1);
	}
	.layer {
		position: absolute;
		inset: 0;
		width: 100%;
		max-width: none;
		height: 100%;
		opacity: 0;
	}
	.layer.on {
		opacity: 1;
	}
	.l-blank {
		background: #fff;
	}

	/* The agent's focus, travelling to what it acts on. Four 2px edges,
	   each a fixed 100-capture-pixel bar moved by translate and stretched
	   along its length by scale, so the ring changes place and size with
	   no layout (left/top/width/height cost CLS, I-397) and the edges stay
	   2px thick at any size. --rx/--ry/--rw/--rh are capture pixels. */
	.ring {
		--rx: 25;
		--ry: 0;
		--rw: 519;
		--rh: 217;
		position: absolute;
		inset: 0 auto auto 0;
		opacity: 0;
		pointer-events: none;
	}
	.ring > i {
		position: absolute;
		left: 0;
		top: 0;
		width: 2px;
		height: 2px;
		background: var(--chip);
		transform-origin: 0 0;
	}
	.ring .rt,
	.ring .rb {
		width: calc(var(--k) * 100);
		scale: calc(var(--rw) / 100) 1;
	}
	.ring .rl,
	.ring .rr {
		height: calc(var(--k) * 100);
		scale: 1 calc(var(--rh) / 100);
	}
	.ring .rt,
	.ring .rl {
		translate: calc(var(--k) * var(--rx)) calc(var(--k) * var(--ry));
	}
	.ring .rb {
		translate: calc(var(--k) * var(--rx)) calc(var(--k) * (var(--ry) + var(--rh)) - 2px);
	}
	.ring .rr {
		translate: calc(var(--k) * (var(--rx) + var(--rw)) - 2px) calc(var(--k) * var(--ry));
	}
	@media (forced-colors: active) {
		/* a background is dropped in forced colours; the edges are the ring */
		.ring > i {
			background: Highlight;
			forced-color-adjust: none;
		}
	}

	/* You: the command, and your pointer in the same window. The grid's
	   chip (landing.css), in --chip on the white page. */
	.stage .chip {
		position: absolute;
		right: 10px;
		top: calc(var(--k) * 48 + 10px);
		border-color: var(--chip);
		background: #fff;
		color: var(--chip);
	}
	.stage .chip:global(.fire) {
		background: var(--chip);
		color: #fff;
	}
	/* Placed with translate, so anime.js's scale (the click) composes with
	   it and the pointer's travel moves no layout box. */
	.pointer {
		position: absolute;
		left: 0;
		top: 0;
		translate: calc(var(--k) * var(--px) - 1px) calc(var(--k) * var(--py) - 1px);
		width: 13px;
		height: 19px;
		transform-origin: 1px 1px;
	}

	/* The agent's log. */
	.term {
		position: relative;
		flex: none;
		height: 52px;
		overflow: hidden;
		border-top: 1px solid #2a2a28;
		background: #0d0d0c;
		color: #d4d4d0;
		font-family: var(--font-mono);
		font-size: 11px;
		line-height: 14px;
		font-variant-ligatures: none;
	}
	.clip {
		position: absolute;
		left: 0;
		right: 0;
		bottom: 5px;
		height: 42px;
		overflow: hidden;
	}
	.rows {
		position: absolute;
		left: 0;
		right: 0;
		bottom: 0;
	}
	.tr {
		position: relative;
		padding: 0 12px;
		white-space: pre;
		overflow: hidden;
		text-overflow: ellipsis;
	}
	/* Rows rest dimmed, at REST in the script: 0.7 keeps every captured
	   text colour at 5:1 or more on #0d0d0c. The still frame is the loop's
	   last beat, where you have taken over and every row rests. */
	.tr {
		opacity: 0.7;
	}
	/* The call that is acting: a 2px bar at the row's left. A border on a
	   pseudo-element, not an inset box-shadow, so it fades by opacity alone
	   and forced colours still draw it (as CanvasText). */
	.tr::before {
		content: '';
		position: absolute;
		left: 0;
		top: 0;
		bottom: 0;
		border-left: 2px solid var(--on-log);
		opacity: 0;
		transition: opacity 0.3s;
	}
	.tr:global(.new)::before {
		opacity: 1;
	}

	@container (max-width: 400px) {
		.term {
			font-size: 10px;
			line-height: 13px;
			height: 49px;
		}
		.clip {
			height: 39px;
		}
		.tr {
			padding: 0 10px;
		}
	}
</style>
