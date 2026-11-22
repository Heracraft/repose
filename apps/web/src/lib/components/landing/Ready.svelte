<!--
  Ready: what is preinstalled on every machine at first boot. Five coding
  agents with both their marks and visible names, followed by the base
  runtimes and how your laptop's own tools and Nix packages carry over.
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

<div class="ready">
	<ul class="agents" aria-label="Preinstalled coding agents">
		{#each agentMarks as m (m.name)}
			<li>
				<span class="am">{@render mark(m, 20)}</span>
				<span class="aname">{m.name}</span>
			</li>
		{/each}
	</ul>

	<div class="below">
		<div class="kit">
			<ul class="tools" aria-label="Preinstalled runtimes">
				{#each toolMarks as m (m.name)}
					<li>
						{@render mark(m, 16)}<span>{toolLabel[m.name] ?? m.name}</span>
					</li>
				{/each}
			</ul>
			<p class="more">{everyday}</p>
		</div>

		<div class="extras">
			<p class="copy">
				<code>repose run</code> installs your laptop's global npm, Go, Cargo and uv tools in the
				background when a machine boots. Add any Nix package to every rebuild with
				<code>repose config add</code>.
			</p>
		</div>
	</div>
</div>

<style>
	.ready {
		display: grid;
	}
	.agents {
		display: grid;
		grid-template-columns: repeat(2, minmax(0, 1fr));
		gap: 0.75rem 1rem;
		margin: 0;
		padding: 1.375rem var(--edge, 16px);
		list-style: none;
		background:
			radial-gradient(70% 90% at 0% 0%, var(--stage-sheen), transparent 70%), var(--stage);
		box-shadow: inset 0 1px 0 var(--stage-top);
	}
	@media (min-width: 640px) {
		.agents {
			grid-template-columns: repeat(5, minmax(0, 1fr));
			padding-block: 1.5rem;
		}
	}
	.agents li {
		display: flex;
		align-items: center;
		gap: 0.625rem;
		min-width: 0;
	}
	@media (min-width: 640px) {
		.agents li {
			justify-content: center;
		}
	}
	.am {
		display: flex;
		color: var(--ink);
	}
	.aname {
		font-size: 0.875rem;
		font-weight: 500;
		line-height: 1.25rem;
		color: var(--ink);
		white-space: nowrap;
	}
	.below {
		display: grid;
		border-top: 1px solid var(--rule-in, var(--rule));
	}
	.kit,
	.extras {
		padding: 1.25rem var(--edge, 16px);
	}
	.extras {
		border-top: 1px solid var(--rule-in, var(--rule));
		display: flex;
		align-items: center;
	}
	.tools {
		display: flex;
		flex-wrap: wrap;
		gap: 0.5rem 0.9rem;
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.tools li {
		display: inline-flex;
		align-items: center;
		gap: 0.4rem;
		font-size: 0.8125rem;
		line-height: 1.25rem;
		color: var(--pic-ink);
		white-space: nowrap;
	}
	.tools li :global(svg) {
		color: var(--pic-dim);
	}
	.more {
		margin: 0.625rem 0 0;
		font-family: var(--font-mono);
		font-size: 0.75rem;
		line-height: 1.25rem;
		color: var(--ink-faint);
	}
	@media (min-width: 1024px) {
		.below {
			grid-template-columns: repeat(2, minmax(0, 1fr));
		}
		.kit {
			padding: 1.5rem var(--in) 1.5rem var(--edge, 16px);
		}
		.extras {
			border-top: 0;
			border-left: 1px solid var(--rule-in, var(--rule));
			padding: 1.5rem var(--edge, 16px) 1.5rem calc(var(--in) - 1px);
		}
	}
</style>
