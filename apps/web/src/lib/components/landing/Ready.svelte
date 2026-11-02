<!--
  Ready: what is on a machine the moment it boots. The five agents are the
  hero, as their marks alone (owner, 2026-09-27, I-287: no names or
  commands under them; the aria-label names them, and each mark's title
  names it under the pointer);
  below them the toolchain, and a shell on the machine where a missing
  command prints the real command-not-found hint (nix/guest/base/devtools.nix,
  captured for real on a repose machine).
-->
<script lang="ts">
	import WinHead from './WinHead.svelte';
	import { agentMarks, toolMarks, type Mark } from '$lib/components/illustrations/marks';

	// Labels from docs/machine.md "What's installed".
	const toolLabel: Record<string, string> = {
		'Node.js': 'Node.js 24',
		Python: 'Python 3.12',
		Rust: 'rustup'
	};

	const everyday = 'uv gcc make cmake git gh tmux jq ripgrep psql neovim Chromium';

	// A real capture from a repose machine (tmux capture-pane -p -e -J,
	// 2026-09-25): pgcli typed in the checkout, the command-not-found hint, the
	// suggested install, the tool running. Colours as the capture set them,
	// mapped as ops/dev/hero/convert.py maps them. The first line of each
	// two-line starship prompt ('dev in job-alerts in' and the checkout's
	// folder, an internal name) is cropped out, as rows are cropped out of
	// the browser card's log; each command keeps its ❯ line, and the status
	// bar still names the machine (I-397).
	type Row = [string, string][];
	const rows: (Row | 'hint')[] = [
		[
			['❯', 'g b'],
			[' pgcli -p 5433', '']
		],
		[['pgcli: command not found', '']],
		'hint',
		[['Other packages with pgcli: python314Packages.pgcli, python313Packages.pgcli', '']],
		[],
		[
			['❯', 'r'],
			[' nix profile add nixpkgs#pgcli', '']
		],
		[],
		[
			['❯', 'g b'],
			[' pgcli --version', '']
		],
		[['Version: 4.6.0', '']]
	];
	// The status bar as a client on that window drew it (the mode flag
	// cropped, and its first 10 columns, the session name, cropped off the
	// left so it starts at the window list).
	const barHost = '"your-project" ';
	const barDate = ' 25-Sep-26';
	const hint = [
		['  nix profile add nixpkgs#pgcli', 'install it on this machine'],
		['  repose config add pgcli      ', 'keep it on every rebuild (run this on your laptop)']
	];
</script>

{#snippet mark(m: Mark, size: number, named = false)}
	<svg
		viewBox="0 0 24 24"
		width={size}
		height={size}
		class="shrink-0"
		fill="currentColor"
		aria-hidden="true"
		>{#if named}<title>{m.name}</title>{/if}{#each m.paths as d (d)}<path
				{d}
				fill-rule={m.evenodd ? 'evenodd' : 'nonzero'}
				clip-rule={m.evenodd ? 'evenodd' : 'nonzero'}
			/>{/each}</svg
	>
{/snippet}

<div
	class="ready"
	role="img"
	aria-label="A new machine has five coding agents installed: Claude Code, Codex CLI, opencode, Gemini CLI and pi. Also Node.js 24, pnpm, Python 3.12, Go, rustup, Docker, Nix, Chromium and everyday tools. A shell on the machine: typing pgcli, which is not installed, prints how to install it with nix profile add nixpkgs#pgcli or keep it on every rebuild with repose config add pgcli; after the install, pgcli --version prints 4.6.0."
>
	<ul class="agents" aria-hidden="true">
		{#each agentMarks as m (m.name)}
			<li>
				<span class="am">{@render mark(m, 32, true)}</span>
			</li>
		{/each}
	</ul>

	<div class="below" aria-hidden="true">
		<div class="kit">
			<ul class="tools">
				{#each toolMarks as m (m.name)}
					<li>
						{@render mark(m, 18)}{toolLabel[m.name] ?? m.name}
					</li>
				{/each}
			</ul>
			<p class="more">
				{everyday} …
			</p>
		</div>

		<div class="term shot-win">
			<WinHead who="machine" />
			<div class="lines">
				{#each rows as row, i (i)}
					{#if row === 'hint'}
						{#each hint as [cmd, what] (cmd)}
							<div class="hint">
								<span>{cmd}</span><span>&nbsp;&nbsp;</span><span>{what}</span>
							</div>
						{/each}
					{:else}
						<div>
							{#each row as [text, cls], j (j)}<span class={cls}>{text}</span
								>{/each}{#if row.length === 0}&nbsp;{/if}
						</div>
					{/if}
				{/each}
			</div>
			<div class="bar">
				<span class="pre">0:shell- 1:dev&nbsp; 2:ready*</span><span class="clock"
					><span class="long">{barHost}</span>19:49<span class="long">{barDate}</span></span
				>
			</div>
		</div>
	</div>
</div>

<style>
	/* Everything sits on the stage's one inset (landing.css): the marks'
	   band, the hairline under it and the two columns under that share
	   the same left and right edges. */
	.ready {
		display: grid;
	}
	.agents {
		display: grid;
		grid-template-columns: repeat(5, minmax(0, 1fr));
		gap: 8px;
		margin: 0;
		padding: 16px 0 40px;
		list-style: none;
	}
	.agents li {
		display: flex;
		justify-content: center;
	}
	.am {
		color: var(--ink);
	}
	.below {
		display: grid;
		gap: 24px;
		padding-top: 32px;
		align-items: start;
		border-top: 1px solid var(--rule);
	}
	.tools {
		display: grid;
		grid-template-columns: repeat(2, minmax(0, 1fr));
		gap: 12px 16px;
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.tools li {
		display: flex;
		align-items: center;
		gap: 10px;
		font-size: 13px;
		color: var(--pic-ink);
	}
	/* The tools' column is as tall as the shell beside it: the list at
	   its top, the everyday tools' line at its foot, level with the
	   shell's bar. */
	.kit {
		display: flex;
		flex-direction: column;
		height: 100%;
	}
	.more {
		margin: auto 0 0;
		padding-top: 16px;
		font-family: var(--font-mono);
		font-size: 12px;
		line-height: 20px;
		color: var(--ink-faint);
	}
	/* Below 1024px the page is one column, and so is this: the tools
	   over the shell, which keeps the stage's width. */
	@media (min-width: 1024px) {
		.below {
			grid-template-columns: minmax(0, 15rem) minmax(0, 1fr);
			gap: 40px;
			align-items: stretch;
		}
	}

	/* The shell, a window like every other on the page, in the terminals'
	   palette (pictures.css). */
	.term {
		container-type: inline-size;
		font-family: var(--font-mono);
		font-size: 12px;
		line-height: 19px;
		background: var(--t-bg);
		color: var(--t-fg);
	}
	/* A terminal does not re-flow: rows keep white-space: pre and a narrow
	   frame crops them on the right, as the editor and browser captures
	   are cropped (I-397). */
	.lines {
		flex: 1;
		padding: 12px 14px 14px;
		overflow: hidden;
		white-space: pre;
	}
	.y {
		color: var(--t-yellow);
	}
	.g {
		color: var(--t-green);
	}
	.c {
		color: var(--t-cyan);
	}
	.r {
		color: var(--t-red);
	}
	.pre,
	.clock span {
		white-space: pre;
	}
	.pre {
		min-width: 0;
		overflow: hidden;
	}
	.clock {
		flex: none;
	}
	/* A shell narrower than its rows (a phone) drops the bar's host and
	   date, and its rows run off the right edge into a fade, as a
	   picture's do, so no row reads as cut mid-word. */
	@container (max-width: 560px) {
		.long {
			display: none;
		}
		.lines {
			mask-image: linear-gradient(to right, #000 calc(100% - 48px), transparent);
		}
	}
	.b {
		font-weight: 700;
	}
	/* The window list gives way before the clock: the right part keeps
	   its width, the left one crops. */
	.bar {
		display: flex;
		justify-content: space-between;
		gap: 2ch;
		padding: 1px 14px;
		border-top: 1px solid var(--rule);
		background: var(--t-bar);
		color: var(--t-bar-fg);
		white-space: nowrap;
		overflow: hidden;
	}
	@media (max-width: 639px) {
		.term {
			font-size: 11px;
			line-height: 17px;
		}
		.lines {
			padding: 10px 10px 12px;
		}
		.bar {
			padding: 1px 10px;
		}
		.agents {
			gap: 4px;
			padding: 8px 0 24px;
		}
		.agents :global(svg) {
			width: 26px;
			height: 26px;
		}
		.below {
			padding-top: 24px;
		}
	}
</style>
