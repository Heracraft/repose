<!--
  Ready: what is on a machine the moment it boots. The five agents are the
  hero, as their marks alone (owner, 2026-09-27: no names or commands
  under them; the aria-label names them);
  below them the toolchain, and a shell on the machine where a missing
  command prints the real command-not-found hint (nix/guest/base/devtools.nix,
  captured for real on a repose machine).
-->
<script lang="ts">
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
	const barHost = '"job-alerts" ';
	const barDate = ' 25-Sep-26';
	const hint = [
		['  nix profile add nixpkgs#pgcli', 'install it on this machine'],
		['  repose config add pgcli      ', 'keep it on every rebuild (run this on your laptop)']
	];
</script>

{#snippet mark(m: Mark, size: number)}
	<svg
		viewBox="0 0 24 24"
		width={size}
		height={size}
		class="shrink-0"
		fill="currentColor"
		aria-hidden="true"
		>{#each m.paths as d (d)}<path
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
				<span class="am">{@render mark(m, 44)}</span>
			</li>
		{/each}
	</ul>

	<div class="below" aria-hidden="true">
		<div>
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

		<div class="term">
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
	.agents {
		display: grid;
		grid-template-columns: repeat(3, minmax(0, 1fr));
		gap: 28px 8px;
		padding: 32px 12px;
	}
	.agents li {
		display: flex;
		flex-direction: column;
		align-items: center;
		text-align: center;
	}
	/* The landing's own chrome around the capture: text tokens, so the
	   two schemes are paired once in layout.css, not here. */
	.am {
		color: var(--ink);
	}
	.below {
		display: grid;
		gap: 24px;
		padding: 16px;
		align-items: start;
		border-top: 1px solid var(--rule);
	}
	.tools {
		display: grid;
		grid-template-columns: repeat(2, minmax(0, 1fr));
		gap: 12px 16px;
	}
	.tools li {
		display: flex;
		align-items: center;
		gap: 10px;
		font-size: 14px;
		color: var(--pic-ink);
	}
	.more {
		margin-top: 16px;
		font-family: var(--font-mono);
		font-size: 12px;
		line-height: 20px;
		color: var(--ink-faint);
	}
	@media (min-width: 640px) {
		.agents {
			grid-template-columns: repeat(5, minmax(0, 1fr));
			padding: 40px 32px;
		}
		.below {
			padding: 32px;
		}
	}
	@media (min-width: 768px) {
		.below {
			grid-template-columns: minmax(0, 15rem) minmax(0, 1fr);
			gap: 32px;
		}
	}

	.term {
		display: flex;
		flex-direction: column;
		min-width: 0;
		border: 1px solid #2a2a28;
		border-radius: 2px;
		background: #0d0d0c;
		color: #d4d4d0;
		font-family: var(--font-mono);
		font-size: 12px;
		line-height: 19px;
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
		color: #e5c07b;
	}
	.g {
		color: #98c379;
	}
	.c {
		color: #56b6c2;
	}
	.r {
		color: #e06c75;
	}
	.pre,
	.clock span {
		white-space: pre;
	}
	.b {
		font-weight: 700;
	}
	.bar {
		display: flex;
		justify-content: space-between;
		gap: 1ch;
		padding: 1px 14px;
		background: #1f9d55;
		color: #0d0d0c;
		white-space: nowrap;
		overflow: hidden;
	}
	@media (max-width: 639px) {
		.long {
			display: none;
		}
		.term {
			font-size: 10.5px;
			line-height: 16px;
		}
		.lines {
			padding: 10px 10px 12px;
		}
		.bar {
			padding: 1px 10px;
		}
		.agents {
			display: flex;
			flex-wrap: wrap;
			justify-content: center;
		}
		.agents li {
			width: calc((100% - 16px) / 3);
		}
	}
</style>
