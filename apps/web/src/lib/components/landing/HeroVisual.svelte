<!--
  HeroVisual: zero-jargon architectural product centerpiece for the hero.
  Communicates repose's entire value proposition in under 2 seconds with zero
  prior context and zero CLI log walls to decipher:
    1. Your laptop stays walled off (only your repo syncs; SSH keys & personal
       files never leave your laptop).
    2. Isolated Ubuntu VM with all 5 coding agents running at full permissions
       + whole-disk undo snapshots.
    3. Runs on Repose Cloud (from $39/mo) OR your own Mac mini / Linux box ($10/mo).
-->
<script lang="ts">
	import { price } from '$lib/format';
	import { agentMarks, type Mark } from '$lib/components/illustrations/marks';

	let {
		soloPriceCents,
		ownPriceCents
	}: {
		soloPriceCents: number;
		ownPriceCents: number;
	} = $props();
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
	class="hv"
	role="region"
	aria-label="How repose works: your laptop stays walled off while coding agents run with full permissions and whole-disk undo inside an isolated Ubuntu VM on Repose Cloud or your own Mac mini or Linux box."
>
	<!-- Tier 1: Your laptop stays untouched -->
	<div class="hv-laptop">
		<div class="hv-lap-left">
			<svg viewBox="0 0 20 20" width="15" height="15" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round" aria-hidden="true">
				<rect x="3" y="4" width="14" height="9.5" rx="1.2" />
				<path d="M1.5 16h17" stroke-linecap="round" />
			</svg>
			<span class="hv-lap-title">Your laptop</span>
			<span class="hv-lap-sync">Repo synced</span>
		</div>
		<div class="hv-lap-safe">
			<svg viewBox="0 0 16 16" width="12" height="12" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
				<rect x="3.5" y="7" width="9" height="6.5" rx="1.2" />
				<path d="M5.5 7V5a2.5 2.5 0 0 1 5 0v2" />
			</svg>
			<span>SSH keys &amp; personal files stay local</span>
		</div>
	</div>

	<!-- Clean single-step connector -->
	<div class="hv-link" aria-hidden="true">
		<i class="hv-wire"></i>
		<span class="hv-cmd">$ repose run</span>
		<i class="hv-wire hv-wire--arrow"></i>
	</div>

	<!-- Tier 2: Focal Sandbox Card (dark slate plate in both themes for unmistakable hierarchy) -->
	<div class="hv-sandbox">
		<div class="hv-sb-head">
			<div class="hv-sb-id">
				<span class="hv-dot" aria-hidden="true"></span>
				<span class="hv-sb-title">Isolated Ubuntu sandbox</span>
			</div>
			<span class="hv-perm">Full permissions</span>
		</div>

		<!-- Real marks of all 5 preinstalled coding agents -->
		<ul class="hv-agents" aria-label="Preinstalled coding agents">
			{#each agentMarks as m (m.name)}
				<li>
					<span class="hv-ag-ic">{@render mark(m, 14)}</span>
					<span>{m.name}</span>
				</li>
			{/each}
		</ul>

		<!-- Two crystal-clear benefits inside the sandbox -->
		<div class="hv-pillars">
			<div class="hv-pillar">
				<span class="hv-p-ic" aria-hidden="true">
					<svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
						<path d="M3 7a5 5 0 1 1 1.4 3.5" />
						<path d="M3 3.5V7h3.5" />
					</svg>
				</span>
				<div class="hv-p-copy">
					<span class="hv-p-head">Whole-disk undo</span>
					<span class="hv-p-sub">Files, DB &amp; packages</span>
				</div>
			</div>
			<div class="hv-pillar">
				<span class="hv-p-ic hv-p-ic--ok" aria-hidden="true">
					<svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
						<path d="M2.5 8h11M10 4.5L13.5 8 10 11.5" />
					</svg>
				</span>
				<div class="hv-p-copy">
					<span class="hv-p-head">Works like local</span>
					<span class="hv-p-sub">Ports, browser &amp; SSH editor</span>
				</div>
			</div>
		</div>
	</div>

	<!-- Split connector down to the two hosting choices -->
	<div class="hv-split-wire" aria-hidden="true">
		<span></span>
		<em>runs on</em>
		<span></span>
	</div>

	<!-- Tier 3: Cloud OR Your Own Hardware -->
	<div class="hv-hosts">
		<a class="hv-host" href="#agents">
			<div class="hv-host-top">
				<span class="hv-host-name">repose Cloud</span>
				<span class="hv-host-price">From {price(soloPriceCents)}/mo</span>
			</div>
			<span class="hv-host-sub">Instant hosted Linux VMs</span>
		</a>
		<a class="hv-host hv-host--own" href="#pricing">
			<div class="hv-host-top">
				<span class="hv-host-name">Your own hardware</span>
				<span class="hv-host-price">{price(ownPriceCents)}/mo</span>
			</div>
			<span class="hv-host-sub">Mac mini or Linux x86 server</span>
		</a>
	</div>
</div>

<style>
	.hv {
		display: flex;
		flex-direction: column;
		width: 100%;
		max-width: 36rem;
		margin-inline: auto;
	}

	/* ---------- Tier 1: Laptop safety bar ---------- */
	.hv-laptop {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		justify-content: space-between;
		gap: 0.375rem 0.75rem;
		padding: 0.625rem 0.875rem;
		border: 1px solid var(--rule-strong);
		border-radius: 6px;
		background: var(--surface);
		color: var(--pic-ink);
	}
	.hv-lap-left {
		display: inline-flex;
		align-items: center;
		gap: 0.5rem;
		font-size: 0.8125rem;
	}
	.hv-lap-title {
		font-weight: 600;
		color: var(--ink);
	}
	.hv-lap-sync {
		padding: 0.0625rem 0.4375rem;
		border: 1px solid var(--rule);
		border-radius: 4px;
		background: var(--sunken);
		font-family: var(--hk-font-mono);
		font-size: 0.6875rem;
		color: var(--pic-dim);
	}
	.hv-lap-safe {
		display: inline-flex;
		align-items: center;
		gap: 0.375rem;
		font-family: var(--hk-font-mono);
		font-size: 0.6875rem;
		color: var(--pic-dim);
	}
	.hv-lap-safe svg {
		color: var(--pic-accent);
		flex: none;
	}

	/* ---------- Connector: $ repose run ---------- */
	.hv-link {
		display: flex;
		flex-direction: column;
		align-items: center;
		padding: 0.1875rem 0;
	}
	.hv-wire {
		width: 0;
		height: 14px;
		border-left: 1.5px solid var(--pic-accent);
	}
	.hv-wire--arrow {
		position: relative;
		height: 16px;
	}
	.hv-wire--arrow::after {
		content: '';
		position: absolute;
		left: -5px;
		bottom: -1px;
		border: 4.5px solid transparent;
		border-top: 5.5px solid var(--pic-accent);
		border-bottom: 0;
	}
	.hv-cmd {
		padding: 0.125rem 0.625rem;
		border: 1px solid var(--pic-accent);
		border-radius: 999px;
		background: var(--surface);
		font-family: var(--hk-font-mono);
		font-size: 0.75rem;
		font-weight: 500;
		line-height: 1.25rem;
		color: var(--pic-accent);
	}

	/* ---------- Tier 2: Focal Sandbox Card ---------- */
	.hv-sandbox {
		display: flex;
		flex-direction: column;
		gap: 0.875rem;
		padding: 1rem 1.125rem 1.0625rem;
		border-radius: 8px;
		background: #101217;
		box-shadow:
			0 0 0 1px #2a2d38,
			inset 0 1px 0 rgb(255 255 255 / 0.08),
			0 10px 24px -12px rgb(9 9 11 / 0.24);
		color: #f4f4f5;
	}
	@media (prefers-color-scheme: dark) {
		:global(:root:not(.light):not([data-theme='light'])) .hv-laptop,
		:global(:root:not(.light):not([data-theme='light'])) .hv-host {
			background: #0e1016;
			border-color: rgb(255 255 255 / 0.09);
		}
		:global(:root:not(.light):not([data-theme='light'])) .hv-lap-sync {
			color: #b8bccb;
			border-color: rgb(255 255 255 / 0.12);
		}
		:global(:root:not(.light):not([data-theme='light'])) .hv-host--own {
			border-color: rgb(147 167 208 / 0.42);
			box-shadow: inset 0 1px 0 rgb(147 167 208 / 0.32);
		}
		:global(:root:not(.light):not([data-theme='light'])) .hv-sandbox {
			background: linear-gradient(180deg, #1d2232 0%, #151926 100%);
			box-shadow:
				0 0 0 1px rgb(147 167 208 / 0.42),
				inset 0 1px 0 rgb(255 255 255 / 0.16),
				0 20px 44px -18px rgb(0 0 0 / 0.85),
				0 0 56px -10px rgb(96 165 250 / 0.2);
		}
		:global(:root:not(.light):not([data-theme='light'])) .hv-split-wire span {
			background: rgb(255 255 255 / 0.16);
		}
		:global(:root:not(.light):not([data-theme='light'])) .hv-split-wire em {
			color: #b4b8c8;
		}
	}
	:global(:root[data-theme='dark']) .hv-laptop,
	:global(:root.dark) .hv-laptop,
	:global(:root[data-theme='dark']) .hv-host,
	:global(:root.dark) .hv-host {
		background: #0e1016;
		border-color: rgb(255 255 255 / 0.09);
	}
	:global(:root[data-theme='dark']) .hv-lap-sync,
	:global(:root.dark) .hv-lap-sync {
		color: #b8bccb;
		border-color: rgb(255 255 255 / 0.12);
	}
	:global(:root[data-theme='dark']) .hv-host--own,
	:global(:root.dark) .hv-host--own {
		border-color: rgb(147 167 208 / 0.42);
		box-shadow: inset 0 1px 0 rgb(147 167 208 / 0.32);
	}
	:global(:root[data-theme='dark']) .hv-sandbox,
	:global(:root.dark) .hv-sandbox {
		background: linear-gradient(180deg, #1d2232 0%, #151926 100%);
		box-shadow:
			0 0 0 1px rgb(147 167 208 / 0.42),
			inset 0 1px 0 rgb(255 255 255 / 0.16),
			0 20px 44px -18px rgb(0 0 0 / 0.85),
			0 0 56px -10px rgb(96 165 250 / 0.2);
	}
	:global(:root[data-theme='dark']) .hv-split-wire span,
	:global(:root.dark) .hv-split-wire span {
		background: rgb(255 255 255 / 0.16);
	}
	:global(:root[data-theme='dark']) .hv-split-wire em,
	:global(:root.dark) .hv-split-wire em {
		color: #b4b8c8;
	}

	.hv-sb-head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.75rem;
	}
	.hv-sb-id {
		display: inline-flex;
		align-items: center;
		gap: 0.5rem;
	}
	.hv-dot {
		width: 7px;
		height: 7px;
		border-radius: 999px;
		background: #34d399;
		box-shadow: 0 0 0 3px rgb(52 211 153 / 0.18);
	}
	.hv-sb-title {
		font-family: var(--hk-font-display);
		font-size: 1.0625rem;
		font-weight: 400;
		letter-spacing: -0.01em;
		color: #f8fafc;
	}
	.hv-perm {
		padding: 0.1875rem 0.5625rem;
		border: 1px solid rgb(52 211 153 / 0.35);
		border-radius: 999px;
		background: rgb(52 211 153 / 0.1);
		font-family: var(--hk-font-mono);
		font-size: 0.6875rem;
		font-weight: 500;
		color: #6ee7b7;
		white-space: nowrap;
	}

	/* 5 agents row: flush full-width distribution on desktop so right edge aligns with Full permissions */
	.hv-agents {
		display: flex;
		flex-wrap: wrap;
		gap: 0.375rem;
		margin: 0;
		padding: 0;
		list-style: none;
	}
	@media (min-width: 640px) {
		.hv-agents {
			display: grid;
			grid-template-columns: repeat(5, auto);
			justify-content: space-between;
			gap: 0.375rem;
		}
	}
	.hv-agents li {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		gap: 0.375rem;
		padding: 0.3125rem 0.625rem;
		border: 1px solid #2d3242;
		border-radius: 6px;
		background: #191d28;
		font-size: 0.75rem;
		font-weight: 500;
		color: #e4e6ee;
		white-space: nowrap;
	}
	.hv-ag-ic {
		display: flex;
		color: #f4f4f5;
	}

	/* 2 clear benefit pillars inside sandbox */
	.hv-pillars {
		display: grid;
		grid-template-columns: repeat(2, minmax(0, 1fr));
		gap: 0.5rem;
	}
	.hv-pillar {
		display: flex;
		align-items: flex-start;
		gap: 0.5625rem;
		padding: 0.625rem 0.75rem;
		border: 1px solid #262936;
		border-radius: 6px;
		background: #161821;
	}
	.hv-p-ic {
		display: flex;
		align-items: center;
		justify-content: center;
		width: 1.5rem;
		height: 1.5rem;
		flex: none;
		border-radius: 5px;
		background: rgb(147 167 208 / 0.14);
		color: #93b4e8;
		margin-top: 0.0625rem;
	}
	.hv-p-ic--ok {
		background: rgb(52 211 153 / 0.12);
		color: #6ee7b7;
	}
	.hv-p-copy {
		display: flex;
		flex-direction: column;
		gap: 0.125rem;
		min-width: 0;
	}
	.hv-p-head {
		font-size: 0.8125rem;
		font-weight: 500;
		line-height: 1.25;
		color: #f4f4f5;
	}
	.hv-p-sub {
		font-size: 0.75rem;
		line-height: 1.3;
		color: #b5bacb;
	}

	/* ---------- Split connector: runs on ---------- */
	.hv-split-wire {
		display: flex;
		align-items: center;
		gap: 0.625rem;
		padding: 0.6875rem 0.25rem;
	}
	.hv-split-wire span {
		flex: 1;
		height: 1px;
		background: var(--rule-strong);
	}
	.hv-split-wire em {
		font-family: var(--hk-font-mono);
		font-style: normal;
		font-size: 0.6875rem;
		letter-spacing: 0.06em;
		text-transform: uppercase;
		color: var(--pic-faint);
	}

	/* ---------- Tier 3: Cloud OR Own Hardware ---------- */
	.hv-hosts {
		display: grid;
		grid-template-columns: repeat(2, minmax(0, 1fr));
		gap: 0.625rem;
	}
	.hv-host {
		position: relative;
		overflow: hidden;
		display: flex;
		flex-direction: column;
		gap: 0.25rem;
		padding: 0.75rem 0.875rem;
		border: 1px solid var(--rule-strong);
		border-radius: 6px;
		background: var(--surface);
		text-decoration: none;
		transition: border-color 150ms ease;
	}
	.hv-host:hover {
		border-color: var(--pic-accent);
	}
	.hv-host--own {
		border-color: color-mix(in srgb, var(--pic-accent) 52%, var(--rule-strong));
		box-shadow: inset 0 1px 0 color-mix(in srgb, var(--pic-accent) 40%, transparent);
	}
	.hv-host-top {
		display: flex;
		align-items: baseline;
		justify-content: space-between;
		gap: 0.5rem;
	}
	.hv-host-name {
		font-size: 0.8125rem;
		font-weight: 600;
		color: var(--ink);
		white-space: nowrap;
	}
	.hv-host-price {
		font-family: var(--hk-font-mono);
		font-size: 0.75rem;
		font-weight: 500;
		color: var(--pic-accent);
		white-space: nowrap;
		font-variant-numeric: tabular-nums;
	}
	.hv-host-sub {
		font-size: 0.75rem;
		line-height: 1.3;
		color: var(--pic-dim);
	}

	@media (min-width: 1024px) and (max-width: 1199.98px) {
		.hv-agents {
			gap: 0.3125rem;
		}
		.hv-agents li {
			font-size: 0.6875rem;
			padding: 0.25rem 0.4375rem;
			gap: 0.25rem;
		}
	}
	@media (max-width: 639.98px) {
		.hv-laptop {
			padding: 0.5625rem 0.75rem;
		}
		.hv-sandbox {
			padding: 0.875rem;
			gap: 0.75rem;
		}
		.hv-sb-head {
			gap: 0.5rem;
		}
		.hv-sb-title {
			font-size: 0.9375rem;
		}
		.hv-perm {
			font-size: 0.625rem;
			padding: 0.125rem 0.4375rem;
		}
		.hv-pillars,
		.hv-hosts {
			grid-template-columns: minmax(0, 1fr);
		}
		.hv-agents li {
			font-size: 0.6875rem;
			padding: 0.25rem 0.4375rem;
		}
	}
</style>
