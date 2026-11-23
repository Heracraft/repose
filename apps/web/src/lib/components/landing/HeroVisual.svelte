<!--
  HeroVisual: the hero's figure. Three things a visitor reads at a glance,
  with as few words as the picture allows (owner, 2026-11-22; council
  round 2):
    1. Your laptop: the repo, git settings and the gh login go up, so the
       sandbox is set up like the laptop (/docs/secrets, "Logins copied
       from your laptop"). SSH keys, never copied, stay, faded.
    2. The sandbox: the same three, and the five preinstalled agents by
       their marks alone.
    3. Its snapshots: a rail of restore points and the undo back to one,
       with no times to read (section 02 tells the restore with times).
  One wire, `repose run`, from the rows that go. No inner dividers: the
  parts of the sandbox are set apart by space. Brand colours only: the
  slate accent, zinc, and one small status dot.
-->
<script lang="ts">
	import { agentMarks, type Mark } from '$lib/components/illustrations/marks';

	// What `repose run` copies (dirs end in a slash); `.ssh/` stays behind.
	const goes = ['your-project/', '.gitconfig', '.config/gh/'];
</script>

{#snippet mark(m: Mark, size: number)}
	<svg viewBox="0 0 24 24" width={size} height={size} fill="currentColor" aria-hidden="true"
		>{#each m.paths as d (d)}<path
				{d}
				fill-rule={m.evenodd ? 'evenodd' : 'nonzero'}
				clip-rule={m.evenodd ? 'evenodd' : 'nonzero'}
			/>{/each}</svg
	>
{/snippet}

{#snippet icon(name: string)}
	<svg
		viewBox="0 0 16 16"
		width="14"
		height="14"
		fill="none"
		stroke="currentColor"
		stroke-width="1.3"
		stroke-linejoin="round"
		aria-hidden="true"
	>
		{#if name.endsWith('/')}
			<path d="M1.75 4.25v8h12.5v-6.5H7.5L6 4.25z" />
		{:else}
			<path d="M4 1.75h5L12.25 5v9.25H4z M8.75 1.75V5.25h3.5" />
		{/if}
	</svg>
{/snippet}

<figure
	class="hv"
	aria-label="Your repo, git settings and GitHub login go from your laptop to a Linux sandbox, while your SSH keys and documents stay on the laptop. The sandbox has Claude Code, Codex, opencode, Gemini CLI and pi. Its snapshots let you roll the whole machine back."
>
	<div class="hv-row" aria-hidden="true">
		<!-- The laptop -->
		<div class="hv-win hv-lap">
			<p class="hv-head">
				<svg
					viewBox="0 0 20 20"
					width="16"
					height="16"
					fill="none"
					stroke="currentColor"
					stroke-width="1.5"
					stroke-linejoin="round"
				>
					<rect x="3" y="4" width="14" height="9.5" rx="1.2" />
					<path d="M1.5 16h17" stroke-linecap="round" />
				</svg>
				Your laptop
			</p>
			<ul class="hv-files">
				{#each goes as f (f)}
					<li class="go">{@render icon(f)}{f}</li>
				{/each}
				<li class="stay">{@render icon('.ssh/')}.ssh/</li>
			</ul>
		</div>

		<!-- The one wire, level with the rows that go -->
		<div class="hv-wire">
			<span class="hv-cmd">repose run</span>
			<i></i>
		</div>

		<!-- The sandbox -->
		<div class="hv-win hv-box">
			<p class="hv-head">
				<span class="hv-dot"></span>
				your-project
			</p>
			<ul class="hv-files">
				{#each goes as f (f)}
					<li class="go">{@render icon(f)}{f}</li>
				{/each}
			</ul>
			<ul class="hv-agents">
				{#each agentMarks as m (m.name)}
					<li title={m.name}>{@render mark(m, 26)}</li>
				{/each}
			</ul>
			<div class="hv-snaps">
				<p class="hv-snap-h">snapshots</p>
				<div class="hv-undo">
					<span class="hv-undo-l">undo</span>
					<svg viewBox="0 0 100 20" preserveAspectRatio="none" aria-hidden="true">
						<path d="M87.5 19 C 87.5 1, 62.5 1, 62.5 17" fill="none" />
					</svg>
					<i class="hv-undo-tip"></i>
				</div>
				<ol class="hv-rail" aria-hidden="true">
					<li><i></i></li>
					<li><i></i></li>
					<li class="back"><i></i></li>
					<li class="now"><i></i></li>
				</ol>
			</div>
		</div>
	</div>
</figure>

<style>
	.hv {
		width: 100%;
		max-width: 40rem;
		margin: 0 auto;
		color: var(--pic-ink);
		font-family: var(--hk-font-mono);
		font-size: 0.875rem;
		line-height: 1.5rem;
	}

	.hv-row {
		display: grid;
		grid-template-columns: minmax(0, 0.8fr) auto minmax(0, 1.2fr);
		align-items: start;
	}

	.hv-win {
		border: 1px solid var(--rule);
		border-radius: 6px;
		background: var(--surface);
		overflow: hidden;
	}
	.hv-head {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		height: 2.5rem;
		margin: 0;
		padding: 0 1rem;
		border-bottom: 1px solid var(--rule);
		background: var(--sunken);
		font-family: var(--hk-font-sans);
		font-size: 0.875rem;
		font-weight: 500;
		color: var(--ink);
	}
	.hv-dot {
		width: 6px;
		height: 6px;
		border-radius: 50%;
		background: var(--hk-ok-dot);
		flex: none;
	}

	.hv-files {
		display: flex;
		flex-direction: column;
		gap: 0.375rem;
		margin: 0;
		padding: 1rem;
		list-style: none;
	}
	.hv-files li {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		white-space: nowrap;
	}
	.hv-files li :global(svg) {
		flex: none;
		color: var(--pic-dim);
	}
	.hv-files .go {
		color: var(--pic-ink);
	}
	/* What stays on the laptop: faded, not struck. The picture's dim
	   colour rather than opacity, which took the text under 4.5:1
	   (lighthouse color-contrast, CI on 7b93f8c1). */
	.hv-files .stay {
		color: var(--pic-dim);
	}

	.hv-wire {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 0.5rem;
		width: 8rem;
		padding: 0 0.5rem;
		/* Level with the middle of the three rows that go:
		   head 2.5rem + pad 1rem + 1.5 rows of (1.5rem + 0.375rem gap),
		   less the label's line and its gap. */
		margin-top: calc(2.5rem + 1rem + 2.625rem - 1.5rem - 0.5rem);
	}
	.hv-wire i {
		position: relative;
		display: block;
		width: 100%;
		height: 0;
		border-top: 1.5px solid var(--pic-accent);
	}
	.hv-wire i::after {
		content: '';
		position: absolute;
		right: -1px;
		top: -5.5px;
		border: 4.5px solid transparent;
		border-left: 6px solid var(--pic-accent);
		border-right: 0;
	}
	.hv-cmd {
		color: var(--pic-accent);
		font-weight: 500;
		white-space: nowrap;
	}

	.hv-box .hv-files {
		padding-bottom: 0;
	}
	.hv-agents {
		display: flex;
		gap: 1.25rem;
		margin: 0;
		padding: 1rem 1rem 0;
		list-style: none;
	}
	.hv-agents li {
		display: grid;
		place-items: center;
		color: var(--pic-ink);
	}

	/* The snapshot rail: the card's full width, its undo arc in the accent. */
	.hv-snaps {
		padding: 1.25rem 1rem 1.25rem;
	}
	.hv-snap-h {
		margin: 0;
		font-family: var(--hk-font-sans);
		font-size: 0.8125rem;
		line-height: 1.25rem;
		color: var(--pic-dim);
	}
	.hv-undo {
		position: relative;
		height: 1.75rem;
	}
	.hv-undo svg {
		position: absolute;
		inset: 0;
		width: 100%;
		height: 100%;
		overflow: visible;
	}
	.hv-undo path {
		stroke: var(--pic-accent);
		stroke-width: 2;
		stroke-linecap: round;
		vector-effect: non-scaling-stroke;
	}
	/* The arrowhead lands on the lit dot (the third of four, at 62.5%). */
	.hv-undo-tip {
		position: absolute;
		left: 62.5%;
		bottom: -3px;
		width: 0;
		height: 0;
		border: 5px solid transparent;
		border-top: 6px solid var(--pic-accent);
		border-bottom: 0;
		transform: translateX(-50%);
	}
	.hv-undo-l {
		position: absolute;
		left: 75%;
		top: -0.875rem;
		transform: translateX(-50%);
		font-family: var(--hk-font-sans);
		font-size: 0.8125rem;
		line-height: 1rem;
		font-weight: 500;
		color: var(--pic-accent);
	}
	.hv-rail {
		position: relative;
		display: grid;
		grid-template-columns: repeat(4, 1fr);
		margin: 0.375rem 0 0;
		padding: 0;
		list-style: none;
	}
	.hv-rail::before {
		content: '';
		position: absolute;
		left: 12.5%;
		right: 12.5%;
		top: 5px;
		border-top: 1px solid var(--rule-strong);
	}
	.hv-rail li {
		display: flex;
		justify-content: center;
	}
	.hv-rail i {
		position: relative;
		width: 11px;
		height: 11px;
		border: 1.5px solid var(--rule-strong);
		border-radius: 50%;
		background: var(--surface);
	}
	.hv-rail .back i {
		border-color: var(--pic-accent);
		background: var(--pic-accent);
	}
	.hv-rail .now i {
		border-style: dashed;
	}

	/* Beside the headline, the figure stands taller so it, not the stage
	   around it, holds the eye. */
	@media (min-width: 1024px) {
		.hv-head {
			height: 2.75rem;
		}
		.hv-files {
			gap: 0.5rem;
			padding: 1.25rem 1rem;
		}
		.hv-wire {
			margin-top: calc(2.75rem + 1.25rem + 2.75rem - 1.5rem - 0.5rem);
		}
		.hv-agents {
			padding-top: 1.25rem;
		}
		.hv-snaps {
			padding: 1.75rem 1rem 1.5rem;
		}
	}

	@media (max-width: 639.98px) {
		.hv {
			font-size: 0.8125rem;
		}
		.hv-row {
			grid-template-columns: minmax(0, 1fr);
		}
		.hv-wire {
			width: auto;
			margin: 0;
			flex-direction: row;
			gap: 0.625rem;
			justify-content: center;
			padding: 0.5rem 0;
		}
		.hv-wire i {
			width: 0;
			height: 1.5rem;
			border-top: 0;
			border-left: 1.5px solid var(--pic-accent);
		}
		.hv-wire i::after {
			right: auto;
			top: auto;
			left: -5.25px;
			bottom: -1px;
			border: 4.5px solid transparent;
			border-top: 6px solid var(--pic-accent);
			border-bottom: 0;
		}
	}
</style>
