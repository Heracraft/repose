<!--
  HeroVisual: the hero's figure. Three things a visitor reads at a glance,
  with as few words as the picture allows (owner, 2026-11-22):
    1. Your laptop: the repo goes up, keys and personal files stay.
    2. The sandbox: the five preinstalled agents, by their marks alone.
    3. Its snapshots: a rail of restore points, and the undo back to one.
  Brand colours only: the slate accent, zinc, and one small status dot.
-->
<script lang="ts">
	import { agentMarks, type Mark } from '$lib/components/illustrations/marks';
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

{#snippet lock()}
	<svg
		viewBox="0 0 16 16"
		width="12"
		height="12"
		fill="none"
		stroke="currentColor"
		stroke-width="1.5"
		stroke-linecap="round"
		stroke-linejoin="round"
		aria-hidden="true"
	>
		<rect x="3.5" y="7" width="9" height="6.5" rx="1.2" />
		<path d="M5.5 7V5a2.5 2.5 0 0 1 5 0v2" />
	</svg>
{/snippet}

{#snippet folder()}
	<svg
		viewBox="0 0 16 16"
		width="13"
		height="13"
		fill="none"
		stroke="currentColor"
		stroke-width="1.4"
		stroke-linejoin="round"
		aria-hidden="true"
	>
		<path d="M1.75 4.25v8h12.5v-6.5H7.5L6 4.25z" />
	</svg>
{/snippet}

<figure
	class="hv"
	aria-label="Your repo goes from your laptop to a Linux sandbox, while your SSH keys and documents stay on the laptop. The sandbox has Claude Code, Codex, opencode, Gemini CLI and pi. Its snapshots let you roll the whole machine back."
>
	<div class="hv-row" aria-hidden="true">
		<!-- The laptop -->
		<div class="hv-win hv-lap">
			<p class="hv-head">
				<svg
					viewBox="0 0 20 20"
					width="14"
					height="14"
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
				<li class="go">{@render folder()}your-project/</li>
				<li class="stay">{@render lock()}.ssh/</li>
				<li class="stay">{@render lock()}Documents/</li>
			</ul>
		</div>

		<!-- The one wire: only the repo crosses -->
		<div class="hv-wire">
			<span class="hv-cmd">repose run</span>
			<i></i>
		</div>

		<!-- The sandbox -->
		<div class="hv-win hv-box">
			<p class="hv-head">
				<span class="hv-dot"></span>
				your-project
				<span class="hv-os">Ubuntu 24.04</span>
			</p>
			<ul class="hv-files">
				<li class="go">{@render folder()}your-project/</li>
			</ul>
			<ul class="hv-agents">
				{#each agentMarks as m (m.name)}
					<li title={m.name}>{@render mark(m, 18)}</li>
				{/each}
			</ul>
			<div class="hv-snaps">
				<p class="hv-snap-h">snapshots</p>
				<svg class="hv-undo" viewBox="0 0 400 24" preserveAspectRatio="none" aria-hidden="true">
					<path d="M350 23 C 350 2, 250 2, 250 20" fill="none" />
					<path d="M245 15 L250 21 L255 15" fill="none" />
				</svg>
				<ol class="hv-rail" aria-hidden="true">
					<li><i></i>03:00</li>
					<li><i></i>14:10</li>
					<li class="back"><i></i>21:25</li>
					<li class="now"><i></i>now</li>
				</ol>
			</div>
		</div>
	</div>
</figure>

<style>
	.hv {
		display: flex;
		flex-direction: column;
		gap: 1.25rem;
		width: 100%;
		max-width: 40rem;
		margin: 0 auto;
		color: var(--pic-ink);
		font-family: var(--hk-font-mono);
		font-size: 0.8125rem;
		line-height: 1.375rem;
	}

	.hv-row {
		display: grid;
		grid-template-columns: minmax(0, 0.78fr) auto minmax(0, 1.22fr);
		align-items: center;
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
		margin: 0;
		padding: 0.5rem 0.75rem;
		border-bottom: 1px solid var(--rule);
		background: var(--sunken);
		font-family: var(--hk-font-sans);
		font-size: 0.8125rem;
		font-weight: 500;
		color: var(--ink);
	}
	.hv-os {
		margin-left: auto;
		font-family: var(--hk-font-mono);
		font-size: 0.6875rem;
		font-weight: 400;
		color: var(--pic-dim);
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
		gap: 0.25rem;
		margin: 0;
		padding: 0.625rem 0.75rem;
		list-style: none;
	}
	.hv-files li {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		white-space: nowrap;
	}
	.hv-files .go {
		color: var(--pic-accent);
	}
	.hv-files .stay {
		color: var(--pic-faint);
	}

	.hv-wire {
		position: relative;
		display: flex;
		flex-direction: column;
		align-items: center;
		width: 6.5rem;
	}
	.hv-wire i {
		position: relative;
		display: block;
		width: 100%;
		height: 0;
		margin-top: 0.375rem;
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
	}

	.hv-box .hv-files {
		padding-bottom: 0.25rem;
	}
	.hv-agents {
		display: flex;
		gap: 0.375rem;
		margin: 0;
		padding: 0.5rem 0.75rem 0.75rem;
		list-style: none;
	}
	.hv-agents li {
		display: grid;
		place-items: center;
		width: 2.375rem;
		height: 2.375rem;
		border: 1px solid var(--rule);
		border-radius: 6px;
		background: var(--sunken);
		color: var(--pic-ink);
	}

	/* The snapshot rail sits under the sandbox, the width of the figure. */
	.hv-snaps {
		position: relative;
		padding: 0.5rem 0.75rem 0.75rem;
		border-top: 1px solid var(--rule);
		background: var(--sunken);
	}
	.hv-snap-h {
		margin: 0 0 0.125rem;
		font-family: var(--hk-font-sans);
		font-size: 0.75rem;
		color: var(--pic-dim);
	}
	.hv-undo {
		display: block;
		width: 100%;
		height: 1.5rem;
		overflow: visible;
	}
	.hv-undo path {
		stroke: var(--pic-accent);
		stroke-width: 1.5;
		stroke-linecap: round;
		stroke-linejoin: round;
		vector-effect: non-scaling-stroke;
	}
	.hv-rail {
		position: relative;
		display: grid;
		grid-template-columns: repeat(4, 1fr);
		margin: 0;
		padding: 0;
		list-style: none;
		font-size: 0.75rem;
		color: var(--pic-dim);
	}
	.hv-rail::before {
		content: '';
		position: absolute;
		left: 12.5%;
		right: 12.5%;
		top: 4px;
		border-top: 1px solid var(--rule-strong);
	}
	.hv-rail li {
		display: flex;
		flex-direction: column;
		align-items: center;
		gap: 0.375rem;
	}
	.hv-rail i {
		position: relative;
		width: 9px;
		height: 9px;
		border: 1.5px solid var(--rule-strong);
		border-radius: 50%;
		background: var(--surface);
	}
	.hv-rail .back {
		color: var(--pic-accent);
		font-weight: 500;
	}
	.hv-rail .back i {
		border-color: var(--pic-accent);
		background: var(--pic-accent);
	}
	.hv-rail .now i {
		border-style: dashed;
	}

	@media (max-width: 639.98px) {
		.hv-row {
			grid-template-columns: minmax(0, 1fr);
		}
		.hv-wire {
			width: auto;
			flex-direction: row;
			gap: 0.5rem;
			justify-content: center;
			padding: 0.375rem 0;
		}
		.hv-wire i {
			width: 0;
			height: 1.25rem;
			margin: 0;
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
