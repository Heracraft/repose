<!--
  Open it in your editor (docs run-and-attach.md "SSH and editors"). Not a
  drawing: LazyVim, installed on the real recruiting machine the documented
  way (the LazyVim starter cloned to ~/.config/nvim, plugins synced by
  lazy.nvim, stock config, tokyonight), in the job-alerts checkout on its
  fetch-timeout branch (the same state the "Your working state" section
  shows). The worker's poller.ts and config.ts are open, the cursor on the
  fetchTimeoutMs setting, and the snacks explorer on the left (opened with
  Snacks.explorer() and narrowed with :vertical resize 20, from a -c luafile
  so no stray keys show in lualine), so the tree stays shallow and the code
  gets most of the width. The 78x20 tmux pane was captured with
  `tmux capture-pane -p -e -N` and converted to styled runs verbatim; the
  block cursor is the terminal's own, at the cell tmux reported. It stands
  in the grid's window under the head that says where it runs, "your
  cloud machine" (WinHead.svelte, I-669), as Localhost's tmux does: the
  card's line says Neovim runs on the machine itself. From 640 wide the
  font is sized so all 78 columns fit, so every line ends where nvim cut
  it and the statusline keeps its clock (F18); on a phone it stays 8.4px
  and the screen crops on the right, as a terminal would (I-397).
  Two explorer rows read "education": two files whose names nvim cut at
  the explorer's 9 columns. The machine is the owner's and not reachable
  from here to recapture with a wider explorer, and a crop cannot drop a
  middle row, so they stay as captured (I-669).

  Nerd Font icons come from a subset of Symbols Nerd Font Mono (Nerd Fonts,
  MIT; the icons are from Material Design Icons, Codicons, Octicons and
  Powerline), only the 9 glyphs this capture uses, served from
  static/landing/editor-nerd-symbols.woff2 and used by the capture alone.
  Every non-ASCII character sits in a one-cell box so the grid holds in any
  monospace font.

  The rows are the picture's pixels, not text to read: the screen is one
  role="img" with its own label, and each row is aria-hidden and inert, so
  it is out of the tab order, find in page and selection as a screenshot
  would be. Tokyo Night's comment grey, explorer grey and statusline blue
  sit under 4.5:1 on its grounds (2.46 to 4.27); a picture's incidental
  text is outside WCAG 1.4.3, and inert is what tells the a11y gate's
  contrast check (axe skips inert nodes) that this is a picture. The
  colours stay the tool's own (LANDING.md, "Real, and whole, or not at
  all"; I-400).
-->
<script lang="ts">
	import WinHead from './WinHead.svelte';

	// Verbatim capture: one inline style per distinct SGR state, and each row
	// as [text, style] runs.
	const styles: string[] = [
		'color:#c8d3f5;background:#1e2030;',
		'color:#636da6;background:#191b28;',
		'color:#0db9d7;background:#191b28;',
		'color:#12131d;background:#191b28;',
		'color:#7ca1f2;background:#1b1d2b;',
		'color:#c8d3f5;background:#222436;font-weight:700;font-style:italic;',
		'color:#0db9d7;background:#222436;',
		'color:#c8d3f5;background:#222436;',
		'color:#636da6;background:#12131d;',
		'color:#ff966c;background:#1e2030;',
		'color:#1b1d2b;background:#222436;font-weight:700;',
		'color:#3b4261;background:#222436;',
		'color:#86e1fc;background:#222436;',
		'color:#fca7ea;background:#222436;font-style:italic;',
		'color:#89ddff;background:#222436;',
		'color:#828bb8;background:#222436;',
		'color:#65bcff;background:#1e2030;',
		'color:#545c7e;background:#1e2030;',
		'color:#65bcff;background:#222436;',
		'color:#4fd6be;background:#222436;',
		'color:#82aaff;background:#222436;',
		'color:#c3e88d;background:#222436;',
		'color:#ff966c;background:#222436;',
		'background:#1e2030;',
		'color:#0db9d7;background:#1e2030;',
		'color:#82aaff;background:#1e2030;',
		'color:#3b4261;background:#1e2030;',
		'color:#fca7ea;background:#1e2030;',
		'color:#ffc777;background:#1e2030;',
		'color:#636da6;background:#222436;font-style:italic;',
		'color:#ff966c;background:#222436;font-weight:700;',
		'color:#65bcff;background:#2f334d;',
		'color:#c8d3f5;background:#2f334d;',
		'color:#4fd6be;background:#2f334d;',
		'color:#89ddff;background:#2f334d;',
		'color:#82aaff;background:#2f334d;',
		'color:#828bb8;background:#2f334d;',
		'color:#c3e88d;background:#2f334d;',
		'color:#ff966c;background:#2f334d;',
		'background:#2f334d;',
		'color:#3b4261;background:#2f334d;',
		'color:#0db9d7;background:#2f334d;',
		'color:#1b1d2b;background:#82aaff;',
		'color:#82aaff;background:#3b4261;',
		'color:#828bb8;background:#1e2030;',
		'color:#c8d3f5;background:#1e2030;font-weight:700;'
	];
	const rows: [string, number][][] = [
		[
			['                    ', 0],
			['    ', 1],
			['\udb81\udee6 ', 2],
			['poller.ts    \uea76 ', 1],
			['\u2595', 3],
			['\u258e', 4],
			['   ', 5],
			['\udb81\udee6 ', 6],
			['config.ts    ', 5],
			['\uea76 ', 7],
			['               ', 8]
		],
		[
			['\u256d\u2500\u2500\u2500\u2500 Explorer \u2500\u2500\u2500\u2500\u256e', 9],
			['\u2502', 10],
			['   9   ', 11],
			['export', 12],
			[' ', 7],
			['const', 13],
			[' config ', 7],
			['=', 14],
			[' ', 7],
			['{', 15],
			['                           ', 7]
		],
		[
			['\u2502', 9],
			['\uf105 ', 16],
			['          ', 0],
			['45/45', 17],
			[' ', 0],
			['\u2502', 9],
			['\u2502', 10],
			['   8   ', 11],
			['\u2502', 18],
			[' ', 7],
			['databaseUrl', 19],
			[':', 14],
			[' process', 7],
			['.', 14],
			['env', 19],
			['.', 14],
			['DATABASE_URL', 19],
			[',', 14],
			['          ', 7]
		],
		[
			[
				'\u2570\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u256f',
				9
			],
			['\u2502', 10],
			['   7   ', 11],
			['\u2502', 18],
			[' ', 7],
			['pollIntervalMin', 19],
			[':', 14],
			[' ', 7],
			['num', 20],
			['(', 15],
			['"POLL_INTERVAL_MIN"', 21],
			[',', 14],
			[' ', 7],
			['10', 22],
			[')', 15],
			[',', 14],
			['  ', 7]
		],
		[
			[' ', 23],
			['\udb81\udf70 ', 24],
			['recruiting', 25],
			['       ', 0],
			['\u2502', 10],
			['   6   ', 11],
			['\u2502', 18],
			[' ', 7],
			['userAgent', 19],
			[':', 14],
			['                                      ', 7]
		],
		[
			[' ', 23],
			['\u251c\u2574', 26],
			['\udb81\udf70 ', 24],
			['apps', 25],
			['           ', 0],
			['\u2502', 10],
			['   5   ', 11],
			['\u2502', 18],
			[' ', 7],
			['\u2502', 11],
			[' process', 7],
			['.', 14],
			['env', 19],
			['.', 14],
			['HTTP_USER_AGENT', 19],
			[' ', 7],
			['??', 14],
			['                ', 7]
		],
		[
			[' ', 23],
			['\u2502 \u251c\u2574', 26],
			['\udb80\ude4b ', 24],
			['web', 25],
			['          ', 0],
			['\u2502', 10],
			['   4   ', 11],
			['\u2502', 18],
			[' ', 7],
			['\u2502', 11],
			[' ', 7],
			['"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/5', 21]
		],
		[
			[' ', 0],
			['\u2502 \u2514\u2574', 26],
			['\udb81\udf70 ', 24],
			['worker', 25],
			['       ', 0],
			['\u2502', 10],
			['   3   ', 11],
			['\u2502', 18],
			[' ', 7],
			['fetchConcurrency', 19],
			[':', 14],
			[' ', 7],
			['num', 20],
			['(', 15],
			['"FETCH_CONCURRENCY"', 21],
			[',', 14],
			[' ', 7],
			['6', 22],
			[')', 15],
			[',', 14],
			['  ', 7]
		],
		[
			[' ', 23],
			['\u2502   \u251c\u2574', 26],
			['\udb81\udf70 ', 27],
			['src', 25],
			['        ', 0],
			['\u2502', 10],
			['   2   ', 11],
			['\u2502', 18],
			[' ', 7],
			['fetchPerHost', 19],
			[':', 14],
			[' ', 7],
			['num', 20],
			['(', 15],
			['"FETCH_PER_HOST"', 21],
			[',', 14],
			[' ', 7],
			['2', 22],
			[')', 15],
			[',', 14],
			['         ', 7]
		],
		[
			[' ', 23],
			['\u2502   \u2502 \u251c\u2574', 26],
			['\udb80\ude4b ', 24],
			['ingest', 28],
			[' ', 0],
			['\u25cb', 28],
			[' ', 0],
			['\u2502', 10],
			['   1   ', 11],
			['\u2502', 18],
			[' ', 7],
			['/** Per-request timeout for board fetches, in mi', 29]
		],
		[
			[' ', 0],
			['\u2502   \u2502 \u251c\u2574', 26],
			['\udb80\ude4b ', 24],
			['notify', 25],
			['   ', 0],
			['\u2502', 10],
			['  21   ', 30],
			['\u2502', 31],
			[' ', 32],
			['fetchTimeoutMs', 33],
			[':', 34],
			[' ', 32],
			['num', 35],
			['(', 36],
			['"FETCH_TIMEOUT_MS"', 37],
			[',', 34],
			[' ', 32],
			['20_000', 38],
			[')', 36],
			[',', 34]
		],
		[
			[' ', 0],
			['\u2502   \u2502 \u251c\u2574', 26],
			['\udb80\ude4b ', 24],
			['ops', 25],
			['      ', 0],
			['\u2502', 10],
			['   1   ', 11],
			['\u2502', 18],
			['                                                 ', 7]
		],
		[
			[' ', 23],
			['\u2502   \u2502 \u251c\u2574', 26],
			['\udb80\ude4b ', 24],
			['util', 25],
			['     ', 0],
			['\u2502', 10],
			['   2   ', 11],
			['\u2502', 18],
			[' ', 7],
			['resendApiKey', 19],
			[':', 14],
			[' process', 7],
			['.', 14],
			['env', 19],
			['.', 14],
			['RESEND_API_KEY', 19],
			[' ', 7],
			['??', 14],
			[' ', 7],
			['""', 21],
			[',', 14],
			[' ', 7]
		],
		[
			[' ', 39],
			['\u2502   \u2502 \u251c\u2574', 40],
			['\udb81\udee6 ', 41],
			['config.ts', 32],
			['\u2502', 10],
			['   3   ', 11],
			['\u2502', 18],
			[' ', 7],
			['emailFrom', 19],
			[':', 14],
			[' process', 7],
			['.', 14],
			['env', 19],
			['.', 14],
			['EMAIL_FROM', 19],
			[' ', 7],
			['??', 14],
			[' ', 7],
			['"alerts@exa', 21]
		],
		[
			[' ', 0],
			['\u2502   \u2502 \u251c\u2574', 26],
			['\udb81\udee6 ', 24],
			['dashboard', 0],
			['\u2502', 10],
			['   4   ', 11],
			['\u2502', 18],
			[' ', 7],
			['emailFromName', 19],
			[':', 14],
			[' process', 7],
			['.', 14],
			['env', 19],
			['.', 14],
			['EMAIL_FROM_NAME', 19],
			[' ', 7],
			['??', 14],
			[' ', 7],
			['"J', 21]
		],
		[
			[' ', 0],
			['\u2502   \u2502 \u251c\u2574', 26],
			['\udb81\udee6 ', 24],
			['digest-on', 0],
			['\u2502', 10],
			['   5   ', 11],
			['\u2502', 18],
			['                                                 ', 7]
		],
		[
			[' ', 23],
			['\u2502   \u2502 \u251c\u2574', 26],
			['\udb81\udee6 ', 24],
			['education', 0],
			['\u2502', 10],
			['   6   ', 11],
			['\u2502', 18],
			[' ', 7],
			['operatorGchatWebhook', 19],
			[':', 14],
			[' process', 7],
			['.', 14],
			['env', 19],
			['.', 14],
			['OPERATOR_GCHAT', 19]
		],
		[
			[' ', 0],
			['\u2502   \u2502 \u251c\u2574', 26],
			['\udb81\udee6 ', 24],
			['education', 0],
			['\u2502', 10],
			['   7   ', 11],
			['\u2502', 18],
			['                                                 ', 7]
		],
		[
			[' ', 23],
			['\u2502   \u2502 \u251c\u2574', 26],
			['\udb81\udee6 ', 24],
			['feedbackR', 0],
			['\u2502', 10],
			['   8   ', 11],
			['\u2502', 18],
			[' ', 7],
			['/** Prometheus /metrics + /healthz HTTP surface.', 29]
		],
		[
			[' NORMAL ', 42],
			['\ue0b0 \ue0a0 fetch-timeout ', 43],
			['\ue0b0', 26],
			[' \udb81\udee6 ', 24],
			[' apps/\u2026/src/', 44],
			['config.ts', 45],
			['    ', 44],
			['\ue0b2', 26],
			[' 31%  21:3  \ue0b2', 43],
			[' \uf43a 02:08 ', 42]
		]
	];

	// Cursor cell as tmux reported it (#{cursor_x}, #{cursor_y}).
	const CURSOR = { x: 30, y: 10 };

	// Split each run into plain ASCII text and single non-ASCII characters,
	// and mark the cursor's cell.
	// Box drawing is drawn in CSS to fill the cell, as terminals draw it, so
	// the explorer's frame and tree lines join whatever the font.
	const BOX: Record<string, string> = {
		'─': 'h',
		'│': 'v',
		'├': 'vr',
		'└': 'ur',
		'╴': 'l',
		'╭': 'dr',
		'╮': 'dl',
		'╰': 'ur r',
		'╯': 'ul',
		'▎': 'q',
		'▕': 'e'
	};
	type Piece = { t: string; s: number; cell: boolean; cursor: boolean; box?: string };
	// The statusline's right segment (from its first \ue0b2 separator: the
	// scroll position, the cursor's line and column, the clock), pinned to
	// the screen's right edge so a phone's crop takes the file path in the
	// middle and keeps these whole.
	const STATUS = rows.length - 1;
	const STATUS_RIGHT = rows[STATUS].findIndex(([t]) => t === '\ue0b2');
	const lines: Piece[][] = rows.map((row, y) => {
		const out: Piece[] = [];
		let x = 0;
		for (const [text, s] of row) {
			let buf = '';
			for (const ch of text) {
				const wide = ch.charCodeAt(0) > 0x7e;
				const cursor = y === CURSOR.y && x === CURSOR.x;
				if (wide || cursor) {
					if (buf) out.push({ t: buf, s, cell: false, cursor: false });
					buf = '';
					const box = BOX[ch];
					out.push({ t: box ? '' : ch, s, cell: true, cursor, box });
				} else buf += ch;
				x++;
			}
			if (buf) out.push({ t: buf, s, cell: false, cursor: false });
		}
		return out;
	});
	// The first piece of the statusline's right segment.
	const statusSplit = (() => {
		let n = 0;
		for (const [text] of rows[STATUS].slice(0, STATUS_RIGHT)) {
			let buf = 0;
			for (const ch of text) {
				if (ch.charCodeAt(0) > 0x7e) {
					if (buf) n++;
					buf = 0;
					n++;
				} else buf++;
			}
			if (buf) n++;
		}
		return n;
	})();
</script>

<div class="shot">
	<div
		class="shot-win open ed"
		role="img"
		aria-label="Neovim with LazyVim running on your cloud machine, full screen: a narrow file explorer of the repo on the left, two of the worker's files open in tabs, and its TypeScript config in the editor."
	>
		<WinHead who="machine" />
		<div class="screen">
			{#snippet pieces(ps: Piece[])}
				{#each ps as p, i (i)}<span
						class:c={p.cell}
						class:cur={p.cursor}
						class={p.box ? `bx ${p.box}` : undefined}
						style={styles[p.s]}>{p.t}</span
					>{/each}
			{/snippet}
			{#each lines as line, y (y)}
				{#if y === STATUS}
					<div class="ln sl" aria-hidden="true" inert>
						<span class="sl-l">{@render pieces(line.slice(0, statusSplit))}</span><span class="sl-r"
							>{@render pieces(line.slice(statusSplit))}</span
						>
					</div>
				{:else}
					<div class="ln" aria-hidden="true" inert>{@render pieces(line)}</div>
				{/if}
			{/each}
		</div>
	</div>
</div>

<style>
	@font-face {
		font-family: 'Editor Nerd Symbols';
		src: url('/landing/editor-nerd-symbols.woff2') format('woff2');
		unicode-range: U+E0A0, U+E0B0, U+E0B2, U+EA76, U+F105, U+F43A, U+F024B, U+F06E6, U+F0770;
		font-display: block;
	}
	.ed {
		height: calc(100% - var(--shot-in));
		margin: var(--shot-in) var(--shot-in) 0;
		container-type: inline-size;
	}
	/* The screen's 20 rows share the window under its head, and its 78
	   columns fill the window's width: JetBrains Mono's cell is 0.6em, so
	   78 cells are 46.8em (I-669). */
	.screen {
		--lh: calc((var(--shot-h) - 2px - var(--shot-in) - 1px - var(--shot-head)) / 20);
		flex: 1;
		min-height: 0;
		background: #222436;
		font-family: 'Editor Nerd Symbols', var(--font-mono);
		font-size: min(10px, calc(100cqw / 46.8));
		font-variant-ligatures: none;
		color: #c8d3f5;
	}
	.ln {
		height: var(--lh);
		line-height: var(--lh);
		white-space: pre;
		overflow: hidden;
	}
	/* The statusline: its left part gives way on a phone, its right part
	   (position, line and column, clock) stays whole at the right edge.
	   Where all 78 columns fit, the two meet as nvim drew them. */
	.sl {
		display: flex;
		background: #1e2030;
	}
	.sl-l {
		flex: 1 1 auto;
		min-width: 0;
		overflow: hidden;
	}
	.sl-r {
		flex: none;
	}
	/* On a phone, 78 columns would be 7px. Below sm the whole capture is
	   drawn at 8.4px instead and crops on the right, so the code keeps
	   about 40 columns (I-397). A crop from the left was tried and
	   dropped: no crop leaves only the explorer's icons, since the top
	   folders' icons sit in its first columns, and cutting through them
	   left name fragments. */
	@media (max-width: 639px) {
		.screen {
			font-size: 8.4px;
		}
	}
	/* One terminal cell: keeps the grid whatever font draws the glyph. */
	.c {
		display: inline-block;
		width: 1ch;
		height: var(--lh);
		vertical-align: top;
		overflow: visible;
		/* A Nerd Font icon is wider than a cell; like most terminals, let it
		   spill over the space nvim leaves after it instead of clipping it. */
		position: relative;
		z-index: 1;
	}
	.bx::before,
	.bx::after {
		content: '';
		position: absolute;
		border: 0 solid currentColor;
	}
	/* │ ├ └: the vertical stroke */
	.v::before,
	.vr::before {
		left: calc(50% - 0.5px);
		top: 0;
		bottom: 0;
		border-left-width: 1px;
	}
	/* ─ ╴ and the right arm of ├ */
	.h::before,
	.l::before,
	.vr::after {
		top: calc(50% - 0.5px);
		border-top-width: 1px;
	}
	.h::before {
		left: 0;
		right: 0;
	}
	.l::before {
		left: 0;
		right: 50%;
	}
	.vr::after {
		left: 50%;
		right: 0;
	}
	/* rounded corners ╭ ╮ ╰ ╯ and └ */
	.dr::before,
	.dl::before,
	.ur::before,
	.ul::before {
		width: calc(50% + 0.5px);
		height: calc(50% + 0.5px);
	}
	.dr::before {
		left: calc(50% - 0.5px);
		top: calc(50% - 0.5px);
		border-top-width: 1px;
		border-left-width: 1px;
		border-top-left-radius: 3px;
	}
	.dl::before {
		right: calc(50% - 0.5px);
		top: calc(50% - 0.5px);
		border-top-width: 1px;
		border-right-width: 1px;
		border-top-right-radius: 3px;
	}
	.ur::before {
		left: calc(50% - 0.5px);
		top: 0;
		border-bottom-width: 1px;
		border-left-width: 1px;
	}
	.ur.r::before {
		border-bottom-left-radius: 3px;
	}
	.ul::before {
		right: calc(50% - 0.5px);
		top: 0;
		border-bottom-width: 1px;
		border-right-width: 1px;
		border-bottom-right-radius: 3px;
	}
	/* ▎ left quarter block, ▕ right eighth block */
	.q::before {
		left: 0;
		top: 0;
		bottom: 0;
		width: 25%;
		background: currentColor;
	}
	.e::before {
		right: 0;
		top: 0;
		bottom: 0;
		width: 12.5%;
		background: currentColor;
	}
	.cur {
		background: #c8d3f5 !important;
		color: #222436 !important;
	}
</style>
