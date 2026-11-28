<!--
  02 Always on: horizontal scroll-driven expanding cards.
  Natural viewport scroll drives 2.1 -> 2.2 -> 2.3 (zero empty runway gap).
  Collapsed cards act as clean architectural book-spines with a subtle
  bottom cue + icon so they never look like half-empty boxes.
-->
<script lang="ts">
	import { onMount } from 'svelte';

	let trackEl: HTMLElement | undefined = $state();
	let active = $state(0);
	let manualUntil = 0;

	function pick(idx: number) {
		active = idx;
		manualUntil = Date.now() + 900;
	}

	onMount(() => {
		const reduced = window.matchMedia('(prefers-reduced-motion: reduce)').matches;
		if (reduced) return;

		let raf = 0;
		const update = () => {
			raf = 0;
			if (!trackEl || window.innerWidth < 960) return;
			if (Date.now() < manualUntil) return;
			const rect = trackEl.getBoundingClientRect();
			const vh = window.innerHeight || 900;
			const progress = Math.max(0, Math.min(1, (vh * 0.62 - rect.top) / (vh * 0.56)));
			active = progress < 0.34 ? 0 : progress < 0.72 ? 1 : 2;
		};
		const onScroll = () => {
			if (!raf) raf = requestAnimationFrame(update);
		};
		update();
		window.addEventListener('scroll', onScroll, { passive: true });
		window.addEventListener('resize', onScroll, { passive: true });
		return () => {
			window.removeEventListener('scroll', onScroll);
			window.removeEventListener('resize', onScroll);
			if (raf) cancelAnimationFrame(raf);
		};
	});
</script>

<div class="uc-track" bind:this={trackEl} data-testid="use-cases-stack">
	<div class="uc-row">
		<!-- Card 1: Development -->
		<!-- svelte-ignore a11y_click_events_have_key_events -->
		<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
		<article class="uc-card" class:is-open={active === 0} onclick={() => pick(0)}>
			<div class="uc-head">
				<p class="label num"><span class="n">1.1 </span>Development</p>
				<h3 class="item-title">Your tools and secrets, ready</h3>
				<p class="copy">
					Bring your own setup and chosen credentials. When an agent hits a missing key, it asks
					you directly instead of stalling.
				</p>
			</div>
			<div class="uc-body" aria-hidden="true">
				<div class="uc-stage">
					<div class="uc-bg-win">
						<div class="uc-bg-bar">
							<span class="uc-dot uc-dot--on"></span>
							<span class="uc-mono">payments-api</span>
							<span class="uc-tag">tools + gh synced</span>
						</div>
						<div class="uc-bg-lines">
							<span>Running integration tests against checkout…</span>
							<span>Missing env STRIPE_TEST_KEY — waiting for secret</span>
						</div>
					</div>
					<div class="uc-popover">
						<div class="uc-pop-h">
							<svg
								class="uc-ico uc-ico--warn"
								viewBox="0 0 16 16"
								width="13"
								height="13"
								fill="none"
								stroke="currentColor"
								stroke-width="1.5"
								stroke-linecap="round"
								stroke-linejoin="round"
								aria-hidden="true"
							>
								<circle cx="5.5" cy="8" r="2.75" />
								<path d="M8.25 8h5.5m-1.75 0v2m-2-2v1.5" />
							</svg>
							<span>Agent requested a secret</span>
						</div>
						<div class="uc-pop-row">
							<code class="uc-key">STRIPE_TEST_KEY</code>
							<span class="uc-pill">Grant</span>
						</div>
					</div>
				</div>
			</div>
			<div class="uc-peek" aria-hidden="true">
				<svg
					class="uc-ico"
					viewBox="0 0 16 16"
					width="13"
					height="13"
					fill="none"
					stroke="currentColor"
					stroke-width="1.5"
					stroke-linecap="round"
					stroke-linejoin="round"
				>
					<circle cx="5.5" cy="8" r="2.75" />
					<path d="M8.25 8h5.5m-1.75 0v2m-2-2v1.5" />
				</svg>
				<span>Secret prompts &amp; logins</span>
			</div>
		</article>

		<!-- Card 2: Scheduled -->
		<!-- svelte-ignore a11y_click_events_have_key_events -->
		<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
		<article class="uc-card" class:is-open={active === 1} onclick={() => pick(1)}>
			<div class="uc-head">
				<p class="label num"><span class="n">1.2 </span>Scheduled</p>
				<h3 class="item-title">Work that finishes while you sleep</h3>
				<p class="copy">
					Nightly dependency updates, flaky-test sweeps, or long refactors — with your laptop shut.
				</p>
			</div>
			<div class="uc-body" aria-hidden="true">
				<div class="uc-stage">
					<div class="uc-bg-win uc-bg-fade">
						<div class="uc-bg-bar">
							<svg
								class="uc-ico"
								viewBox="0 0 16 16"
								width="13"
								height="13"
								fill="none"
								stroke="currentColor"
								stroke-width="1.5"
								stroke-linecap="round"
								stroke-linejoin="round"
								aria-hidden="true"
							>
								<circle cx="8" cy="8" r="5.75" />
								<path d="M8 4.75V8l2.25 1.5" />
							</svg>
							<span class="uc-mono">02:00 UTC</span>
							<span class="uc-tag">laptop closed</span>
						</div>
						<div class="uc-bg-lines">
							<span>Woke sandbox · pulled main</span>
							<span>Upgraded 14 packages · all checks green</span>
						</div>
					</div>
					<div class="uc-popover">
						<div class="uc-pop-h">
							<span class="uc-dot uc-dot--on"></span>
							<span class="uc-mono">PR #142</span>
							<span class="uc-diff"><i class="add">+135</i> <i class="del">-42</i></span>
						</div>
						<p class="uc-pop-sub">Ready for morning review</p>
					</div>
				</div>
			</div>
			<div class="uc-peek" aria-hidden="true">
				<svg
					class="uc-ico"
					viewBox="0 0 16 16"
					width="13"
					height="13"
					fill="none"
					stroke="currentColor"
					stroke-width="1.5"
					stroke-linecap="round"
					stroke-linejoin="round"
				>
					<circle cx="8" cy="8" r="5.75" />
					<path d="M8 4.75V8l2.25 1.5" />
				</svg>
				<span>Nightly &amp; cron runs</span>
			</div>
		</article>

		<!-- Card 3: Webhooks -->
		<!-- svelte-ignore a11y_click_events_have_key_events -->
		<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
		<article class="uc-card" class:is-open={active === 2} onclick={() => pick(2)}>
			<div class="uc-head">
				<p class="label num"><span class="n">1.3 </span>Webhooks</p>
				<h3 class="item-title">Wake an agent on any event</h3>
				<p class="copy">
					Trigger from a security advisory, failing alert, or PR. Reproduce in isolation and open a
					fix.
				</p>
			</div>
			<div class="uc-body" aria-hidden="true">
				<div class="uc-stage uc-stage--grid">
					<div class="uc-pipe">
						<div class="uc-node">
							<span class="uc-node-k">
								<svg
									class="uc-ico"
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
									<path d="M9.5 2L4.25 8.75H8L6.5 14l5.25-6.75H8L9.5 2z" />
								</svg>
								Webhook
							</span>
							<span class="uc-node-v">CVE alert</span>
						</div>
						<span class="uc-link"></span>
						<div class="uc-node">
							<span class="uc-node-k">Isolated VM</span>
							<span class="uc-node-v">Reproduce &amp; patch</span>
						</div>
						<span class="uc-link"></span>
						<div class="uc-node">
							<span class="uc-node-k">Output</span>
							<span class="uc-node-v">Fix PR opened</span>
						</div>
					</div>
				</div>
			</div>
			<div class="uc-peek" aria-hidden="true">
				<svg
					class="uc-ico"
					viewBox="0 0 16 16"
					width="13"
					height="13"
					fill="none"
					stroke="currentColor"
					stroke-width="1.5"
					stroke-linecap="round"
					stroke-linejoin="round"
				>
					<path d="M9.5 2L4.25 8.75H8L6.5 14l5.25-6.75H8L9.5 2z" />
				</svg>
				<span>Alerts &amp; PR triggers</span>
			</div>
		</article>
	</div>
</div>

<style>
	.uc-track {
		padding-bottom: 2.25rem;
	}
	.uc-row {
		display: flex;
		flex-direction: column;
		gap: 1rem;
	}
	.uc-card {
		display: flex;
		flex-direction: column;
		justify-content: space-between;
		gap: 1.25rem;
		padding: 1.5rem;
		border: 1px solid var(--hk-border);
		border-radius: 12px;
		background: var(--hk-surface);
		box-shadow: 0 2px 8px -4px rgb(0 0 0 / 0.1);
		min-width: 0;
		overflow: hidden;
	}
	.uc-peek {
		display: none;
	}

	@media (min-width: 960px) {
		.uc-row {
			flex-direction: row;
			align-items: stretch;
			min-height: 24rem;
			gap: 1rem;
		}
		.uc-card {
			flex: 1 1 0%;
			cursor: pointer;
			background: transparent;
			border-color: var(--hk-hairline);
			box-shadow: none;
			transition:
				flex-grow 420ms cubic-bezier(0.22, 1, 0.36, 1),
				background-color 280ms ease,
				border-color 280ms ease,
				box-shadow 280ms ease;
		}
		.uc-card:not(.is-open) {
			justify-content: space-between;
		}
		.uc-card:not(.is-open) .uc-head :global(.item-title) {
			font-size: 1.125rem;
			line-height: 1.35;
			color: var(--hk-text-2);
		}
		.uc-card:not(.is-open):hover {
			border-color: var(--hk-border);
			background: color-mix(in srgb, var(--hk-surface) 45%, transparent);
		}
		.uc-card:not(.is-open):hover .uc-head :global(.item-title) {
			color: var(--hk-text);
		}
		.uc-card.is-open {
			flex: 3.1 1 0%;
			cursor: default;
			background: var(--hk-surface);
			border-color: var(--hk-border);
			box-shadow:
				inset 0 2px 0 var(--pic-accent),
				0 12px 26px -18px rgb(0 0 0 / 0.22);
		}
		.uc-card:not(.is-open) .uc-head :global(.copy),
		.uc-card:not(.is-open) .uc-body {
			display: none;
		}
		.uc-card:not(.is-open) .uc-peek {
			display: flex;
			align-items: center;
			gap: 0.45rem;
			padding-top: 0.875rem;
			border-top: 1px solid var(--hk-hairline);
			font-family: var(--hk-font-mono);
			font-size: 0.75rem;
			color: var(--hk-text-3);
			white-space: nowrap;
			overflow: hidden;
			text-overflow: ellipsis;
		}
	}

	.uc-head > :global(.num) {
		margin-bottom: 0.375rem;
	}
	.uc-head :global(.copy) {
		margin-top: 0.45rem;
		max-width: 34rem;
	}

	.uc-stage {
		position: relative;
		padding: 1rem;
		border: 1px solid var(--hk-hairline);
		border-radius: 9px;
		background: var(--hk-bg);
		min-height: 10.5rem;
		display: flex;
		flex-direction: column;
		justify-content: center;
		overflow: hidden;
	}
	.uc-stage--grid {
		background-image: radial-gradient(var(--hk-border) 1px, transparent 1px);
		background-size: 16px 16px;
	}
	.uc-bg-win {
		border: 1px solid var(--hk-hairline);
		border-radius: 7px;
		background: var(--hk-surface);
		padding: 0.75rem 0.875rem;
		opacity: 0.6;
	}
	.uc-bg-fade {
		mask-image: linear-gradient(90deg, #000 66%, transparent 100%);
		-webkit-mask-image: linear-gradient(90deg, #000 66%, transparent 100%);
	}
	.uc-bg-bar {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		padding-bottom: 0.5rem;
		border-bottom: 1px solid var(--hk-hairline);
		font-size: 0.75rem;
		color: var(--hk-text-3);
	}
	.uc-bg-lines {
		display: flex;
		flex-direction: column;
		gap: 0.3rem;
		margin-top: 0.5rem;
		font-family: var(--hk-font-mono);
		font-size: 0.75rem;
		color: var(--hk-text-3);
	}
	.uc-mono {
		font-family: var(--hk-font-mono);
		color: var(--hk-text);
	}
	.uc-tag {
		margin-left: auto;
		font-family: var(--hk-font-mono);
		font-size: 0.6875rem;
		color: var(--hk-text-3);
	}
	.uc-dot {
		width: 6px;
		height: 6px;
		border-radius: 50%;
		flex: none;
	}
	.uc-dot--on {
		background: var(--pic-accent);
	}

	.uc-popover {
		position: relative;
		z-index: 2;
		margin-top: -1.65rem;
		margin-left: auto;
		width: min(100%, 20rem);
		padding: 0.8rem 0.95rem;
		border: 1px solid var(--hk-border);
		border-radius: 8px;
		background: var(--hk-surface);
		box-shadow: 0 12px 24px -12px rgb(0 0 0 / 0.24);
	}
	.uc-pop-h {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		font-size: 0.75rem;
		color: var(--hk-text-2);
	}
	.uc-pop-row {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.75rem;
		margin-top: 0.55rem;
	}
	.uc-pop-sub {
		margin: 0.35rem 0 0;
		font-size: 0.8125rem;
		color: var(--hk-text-2);
	}
	.uc-diff {
		margin-left: auto;
		font-family: var(--hk-font-mono);
		font-size: 0.75rem;
	}
	.uc-diff .add {
		font-style: normal;
		color: var(--hk-ok, #2f855a);
	}
	.uc-diff .del {
		font-style: normal;
		color: var(--hk-text-3);
	}
	.uc-key {
		font-family: var(--hk-font-mono);
		font-size: 0.8125rem;
		color: var(--hk-text);
	}
	.uc-pill {
		padding: 0.25rem 0.65rem;
		border-radius: 5px;
		background: var(--pic-accent);
		color: #fff;
		font-size: 0.75rem;
		font-weight: 500;
	}

	.uc-pipe {
		display: flex;
		flex-direction: column;
		align-items: stretch;
		gap: 0.5rem;
	}
	@media (min-width: 640px) {
		.uc-pipe {
			flex-direction: row;
			align-items: center;
			gap: 0;
		}
	}
	.uc-node {
		flex: 1;
		display: flex;
		flex-direction: column;
		gap: 0.125rem;
		padding: 0.7rem 0.85rem;
		border: 1px solid var(--hk-border);
		border-radius: 8px;
		background: var(--hk-surface);
		box-shadow: 0 4px 12px -8px rgb(0 0 0 / 0.16);
	}
	.uc-ico {
		flex: none;
		color: var(--hk-text-3);
	}
	.uc-ico--warn {
		color: var(--hk-warn, #c47f17);
	}
	.uc-node-k {
		display: inline-flex;
		align-items: center;
		gap: 0.3rem;
		font-family: var(--hk-font-mono);
		font-size: 0.6875rem;
		color: var(--hk-text-3);
		text-transform: uppercase;
		letter-spacing: 0.04em;
	}
	.uc-node-v {
		font-size: 0.8125rem;
		font-weight: 500;
		color: var(--hk-text);
	}
	.uc-link {
		display: none;
	}
	@media (min-width: 640px) {
		.uc-link {
			display: block;
			position: relative;
			width: 1.5rem;
			height: 1px;
			background: var(--hk-border-strong);
			flex: none;
		}
		.uc-link::after {
			content: '';
			position: absolute;
			top: -3px;
			right: 0;
			border-block: 3.5px solid transparent;
			border-left: 5px solid var(--hk-border-strong);
		}
	}
	@media (prefers-reduced-motion: reduce) {
		.uc-row {
			flex-direction: column;
		}
		.uc-card:not(.is-open) .uc-head :global(.copy),
		.uc-card:not(.is-open) .uc-body {
			display: block;
		}
		.uc-card:not(.is-open) .uc-peek {
			display: none;
		}
	}
</style>
