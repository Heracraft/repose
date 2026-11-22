<!--
  ScrollWheel: the scroll-driven quarter-wheel (owner, 2026-11-22). On desktop
  (>= 1024px, motion allowed) a tall track pins a full-viewport stage; a
  large dial, its centre past the sheet's left edge, turns a quarter per beat
  as the visitor scrolls: where it runs, the agents, the tools. Phones stack
  the three beats. Each beat is a picture with a title, not a paragraph.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { agentMarks, type Mark } from '$lib/components/illustrations/marks';

	const BEATS = [
		{ id: 'hardware', short: 'Where it runs' },
		{ id: 'agents', short: 'Any agent' },
		{ id: 'tools', short: 'Your tools' }
	] as const;
	const LAST = BEATS.length - 1;
	/* The chapter number, with the space the label's text needs after it. */
	const chapter = '01 ';

	/* Three sandboxes at once, each on its own login: the mix the beat shows.
	   Logins per docs/agents: Claude Code logs in once with a claude.ai
	   subscription, Codex's login is copied from the laptop, pi takes a
	   provider's API key as a secret. */
	const PARALLEL = [
		{ repo: 'billing-api', mark: agentMarks[0], login: 'Claude subscription' },
		{ repo: 'web-app', mark: agentMarks[1], login: 'ChatGPT login' },
		{ repo: 'infra', mark: agentMarks[4], login: 'OPENAI_API_KEY' }
	];

	let trackEl = $state<HTMLElement | null>(null);
	let step = $state(0);
	let angle = $state(0);
	let scrollDriven = $state(false);
	let pinnedParam = $state(false);

	onMount(() => {
		const q = new URLSearchParams(window.location.search).get('wheel');
		if (q) {
			const idx = BEATS.findIndex((b) => b.id === q || String(BEATS.indexOf(b)) === q);
			if (idx >= 0) {
				step = idx;
				angle = idx * 90;
				pinnedParam = true;
			}
		}

		const mqWide = window.matchMedia('(min-width: 1024px)');
		const mqReduce = window.matchMedia('(prefers-reduced-motion: reduce)');
		let raf = 0;

		function updateMode() {
			scrollDriven = mqWide.matches && !mqReduce.matches && !pinnedParam;
			if (!scrollDriven) {
				angle = step * 90;
			} else {
				syncFromScroll();
			}
		}

		function syncFromScroll() {
			if (!scrollDriven || !trackEl) return;
			const rect = trackEl.getBoundingClientRect();
			const vh = window.innerHeight - 57;
			const scrollable = Math.max(1, rect.height - vh);
			const raw = Math.min(1, Math.max(0, (57 - rect.top) / scrollable));
			const scaled = raw * LAST;
			const base = Math.floor(scaled);
			const frac = scaled - base;
			const eased = frac < 0.22 ? 0 : frac > 0.78 ? 1 : (frac - 0.22) / 0.56;
			const smooth = eased * eased * (3 - 2 * eased);
			const pos = Math.min(LAST, base + smooth);
			angle = pos * 90;
			step = Math.min(LAST, Math.round(pos));
		}

		function onScroll() {
			if (!scrollDriven) return;
			cancelAnimationFrame(raf);
			raf = requestAnimationFrame(syncFromScroll);
		}

		updateMode();
		window.addEventListener('scroll', onScroll, { passive: true });
		window.addEventListener('resize', onScroll, { passive: true });
		mqWide.addEventListener('change', updateMode);
		mqReduce.addEventListener('change', updateMode);

		return () => {
			cancelAnimationFrame(raf);
			window.removeEventListener('scroll', onScroll);
			window.removeEventListener('resize', onScroll);
			mqWide.removeEventListener('change', updateMode);
			mqReduce.removeEventListener('change', updateMode);
		};
	});

	function goTo(i: number) {
		const target = Math.min(LAST, Math.max(0, i));
		if (scrollDriven && trackEl) {
			const rect = trackEl.getBoundingClientRect();
			const trackTop = rect.top + window.scrollY;
			const vh = window.innerHeight - 57;
			const scrollable = Math.max(1, rect.height - vh);
			const y = trackTop - 57 + (target / LAST) * scrollable;
			window.scrollTo({ top: y, behavior: 'smooth' });
		} else {
			step = target;
			angle = target * 90;
		}
	}

	function onKeydown(e: KeyboardEvent) {
		if (e.key === 'ArrowDown' || e.key === 'ArrowRight') {
			e.preventDefault();
			goTo(step + 1);
		} else if (e.key === 'ArrowUp' || e.key === 'ArrowLeft') {
			e.preventDefault();
			goTo(step - 1);
		}
	}

	const rimTicks = Array.from({ length: 72 }, (_, i) => i * 5);
	const quads = [0, 90, 180, 270];
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

{#snippet sandbox(repo: string, m: Mark)}
	<li class="sw-sb">
		<span class="sw-sb-dot" aria-hidden="true"></span>
		<span class="sw-sb-repo">{repo}</span>
		<span class="sw-sb-mark" title={m.name}>{@render mark(m, 14)}</span>
	</li>
{/snippet}

{#snippet hardwareVisual()}
	<div
		class="sw-hw"
		aria-label="The same sandboxes run on repose's cloud or on a Mac mini or Linux server of yours."
	>
		<div class="sw-host">
			<p class="sw-host-head">
				<svg
					viewBox="0 0 24 24"
					width="18"
					height="18"
					fill="none"
					stroke="currentColor"
					stroke-width="1.5"
					stroke-linejoin="round"
					aria-hidden="true"
				>
					<path d="M7 18.5h10.5a4 4 0 0 0 .6-7.95A6 6 0 0 0 6.4 9.1 4.75 4.75 0 0 0 7 18.5z" />
				</svg>
				repose cloud
			</p>
			<ul class="sw-sbs">
				{@render sandbox('billing-api', agentMarks[0])}
				{@render sandbox('web-app', agentMarks[1])}
			</ul>
		</div>
		<div class="sw-host">
			<p class="sw-host-head">
				<svg
					viewBox="0 0 24 24"
					width="18"
					height="18"
					fill="none"
					stroke="currentColor"
					stroke-width="1.5"
					stroke-linejoin="round"
					aria-hidden="true"
				>
					<rect x="3" y="8" width="18" height="8" rx="2.5" />
					<path d="M6.5 13.5h2" stroke-linecap="round" />
				</svg>
				your Mac mini
				<span class="sw-host-or">or Linux server</span>
			</p>
			<ul class="sw-sbs">
				{@render sandbox('billing-api', agentMarks[0])}
				{@render sandbox('web-app', agentMarks[1])}
			</ul>
		</div>
	</div>
{/snippet}

{#snippet agentsVisual()}
	<ul
		class="sw-mix"
		aria-label="Three sandboxes at once: Claude Code on a Claude subscription, Codex on a ChatGPT login, pi on an OpenAI API key."
	>
		{#each PARALLEL as vm (vm.repo)}
			<li>
				<span class="sw-mix-mark" title={vm.mark.name}>{@render mark(vm.mark, 20)}</span>
				<span class="sw-mix-repo">{vm.repo}</span>
				<span class="sw-mix-login">{vm.login}</span>
			</li>
		{/each}
	</ul>
{/snippet}

{#snippet toolsVisual()}
	<div class="sw-tools-wrap">
		<div class="sw-port-card">
			<div class="sw-p-box">
				<div class="sw-p-bar">
					<span class="sw-p-who">your sandbox</span>
					<span class="sw-p-live">listening :5173</span>
				</div>
				<div class="sw-p-term">
					<span class="sw-t-vite">VITE v8.0.1</span>
					<span class="sw-t-dim">ready in 1.4s</span>
					<span class="sw-t-url">→ http://localhost:5173/</span>
				</div>
			</div>

			<div class="sw-p-hop" aria-hidden="true">
				<i class="sw-p-wire"></i>
				<span class="sw-p-chip">⇄ 5173 forwarded</span>
				<i class="sw-p-wire sw-p-wire--arr"></i>
			</div>

			<div class="sw-p-box">
				<div class="sw-p-bar">
					<span class="sw-p-who">your laptop</span>
					<span class="sw-p-addr">localhost:5173</span>
				</div>
				<div class="sw-p-preview">
					<div class="sw-prev-nav">
						<span class="sw-prev-brand">Job Alerts</span>
						<span class="sw-prev-links">Roles · Companies · Settings</span>
					</div>
					<div class="sw-prev-rows">
						<div class="sw-prev-row">
							<span>Staff Systems Engineer</span>
							<em>Remote · 2m ago</em>
						</div>
						<div class="sw-prev-row">
							<span>Infrastructure Lead</span>
							<em>San Francisco · 14m ago</em>
						</div>
					</div>
				</div>
			</div>
		</div>

		<p class="sw-editors">
			<code>ssh your-project.repose</code>
			<span>VS Code · Cursor · Zed · Neovim</span>
		</p>
	</div>
{/snippet}

<div
	class="sw-track"
	class:sw-track--scroll={scrollDriven}
	bind:this={trackEl}
	data-step={step}
	data-active={BEATS[step].id}
>
	<div class="sw-sticky">
		<!-- Full-bleed monumental background: 400%-scale quarter-wheel anchored on the 1200px left sheet rail -->
		<div class="sw-backdrop" aria-hidden="true">
			<div class="sw-dotfield"></div>
			<svg class="sw-mob-arc" viewBox="0 0 240 240" fill="none">
				<circle cx="240" cy="0" r="218" class="sw-ring-outer" />
				<circle cx="240" cy="0" r="196" class="sw-ring-dash" />
				<circle cx="240" cy="0" r="142" class="sw-ring-inner" />
				<path d="M 22 0 A 218 218 0 0 0 240 218" class="sw-arc sw-arc--active" />
			</svg>
			<div class="sw-rotor" class:sw-rotor--snap={!scrollDriven} style="--angle: {angle}deg">
				<svg class="sw-dial-svg" viewBox="0 0 1200 1200" fill="none">
					<circle cx="600" cy="600" r="552" class="sw-ring-outer" />
					<circle cx="600" cy="600" r="522" class="sw-ring-dash" />
					<circle cx="600" cy="600" r="452" class="sw-ring-inner" />
					<circle cx="600" cy="600" r="326" class="sw-ring-inner sw-ring-faint" />
					<circle cx="600" cy="600" r="184" class="sw-ring-inner sw-ring-faint" />

					<line x1="600" y1="48" x2="600" y2="1152" class="sw-spoke" />
					<line x1="48" y1="600" x2="1152" y2="600" class="sw-spoke" />

					{#each quads as rot, idx (rot)}
						<g transform="rotate({rot} 600 600)">
							<path
								d="M 614 48.2 A 552 552 0 0 1 1151.8 586"
								class="sw-arc-halo"
								class:sw-arc-halo--active={step === idx}
							/>
							<path
								d="M 614 48.2 A 552 552 0 0 1 1151.8 586"
								class="sw-arc"
								class:sw-arc--active={step === idx}
							/>
							<circle
								cx="600"
								cy="48"
								r="3.2"
								class="sw-node"
								class:sw-node--active={step === idx}
							/>
						</g>
					{/each}

					{#each rimTicks as deg (deg)}
						<line
							x1="600"
							y1={deg % 90 === 0 ? 48 : deg % 30 === 0 ? 50 : deg % 15 === 0 ? 57 : 63}
							x2="600"
							y2="72"
							class={deg % 90 === 0 ? 'sw-tick-major' : deg % 15 === 0 ? 'sw-tick-mid' : 'sw-tick'}
							transform="rotate({deg} 600 600)"
						/>
					{/each}
				</svg>
			</div>
		</div>

		<!-- Desktop monumental scroll-driven layout (>= 1024px) -->
		<div class="sw-inner sw-desktop">
			<div class="sw-rail">
				<p class="label num"><span class="n">{chapter}</span>How it works</p>

				<div class="sw-copy-stack">
					{#if step === 0}
						<h2 class="hk-h2" id="where-h">Our cloud, or a machine you already have</h2>
						<p class="sw-lead">A Mac mini, or any Linux server that runs VMs.</p>
					{:else if step === 1}
						<h2 class="hk-h2" id="where-h">Every agent, preinstalled</h2>
						<p class="sw-lead">Mix and match subscriptions and inference providers.</p>
					{:else}
						<h2 class="hk-h2" id="where-h">Your browser and editor still work</h2>
						<p class="sw-lead">Its dev server opens on your localhost.</p>
					{/if}
				</div>

				<div class="sw-nav" role="tablist" aria-label="Product highlights">
					{#each BEATS as b, i (b.id)}
						<button
							type="button"
							role="tab"
							id="sw-tab-{b.id}"
							aria-selected={step === i}
							aria-controls="sw-stage-{b.id}"
							class="sw-pill-btn"
							class:active={step === i}
							onclick={() => goTo(i)}
							onkeydown={onKeydown}
						>
							<span class="sw-pill-t">{b.short}</span>
						</button>
					{/each}
				</div>

				{#if scrollDriven}
					<p class="sw-hint" aria-hidden="true">
						<svg
							viewBox="0 0 12 12"
							width="11"
							height="11"
							fill="none"
							stroke="currentColor"
							stroke-width="1.4"
							stroke-linecap="round"
							stroke-linejoin="round"
						>
							<path d="M6 2v8M3 7l3 3 3-3" />
						</svg>
						<span>Scroll to turn</span>
					</p>
				{/if}
			</div>

			<div class="sw-stage" aria-live="polite">
				<div
					class="sw-panel-wrap"
					id="sw-stage-hardware"
					role="tabpanel"
					aria-labelledby="sw-tab-hardware"
					class:is-active={step === 0}
					aria-hidden={step !== 0}
				>
					{@render hardwareVisual()}
				</div>

				<div
					class="sw-panel-wrap"
					id="sw-stage-agents"
					role="tabpanel"
					aria-labelledby="sw-tab-agents"
					class:is-active={step === 1}
					aria-hidden={step !== 1}
				>
					{@render agentsVisual()}
				</div>

				<div
					class="sw-panel-wrap"
					id="sw-stage-tools"
					role="tabpanel"
					aria-labelledby="sw-tab-tools"
					class:is-active={step === 2}
					aria-hidden={step !== 2}
				>
					{@render toolsVisual()}
				</div>
			</div>
		</div>

		<!-- Mobile stacked showcase (< 1024px): all 4 beats visible on natural scroll with zero hidden tabs -->
		<div class="sw-mobile" aria-label="How repose works">
			<div class="sw-mob-head">
				<p class="label num"><span class="n">{chapter}</span>How it works</p>
			</div>

			<ol class="sw-mob-beats">
				<li>
					<div class="sw-mb-head">
						<span class="label">1.1</span>
						<h3>Our cloud, or a machine you already have</h3>
					</div>
					{@render hardwareVisual()}
				</li>
				<li>
					<div class="sw-mb-head">
						<span class="label">1.2</span>
						<h3>Every agent, preinstalled</h3>
					</div>
					{@render agentsVisual()}
				</li>
				<li>
					<div class="sw-mb-head">
						<span class="label">1.3</span>
						<h3>Your browser and editor still work</h3>
					</div>
					{@render toolsVisual()}
				</li>
			</ol>
		</div>
	</div>
</div>

<style>
	/* ---------- Outer scroll runway (desktop scroll-driven, mobile stacked) ---------- */
	.sw-track {
		position: relative;
		margin-inline: calc(-1 * var(--edge));
	}
	@media (min-width: 1024px) {
		.sw-track--scroll {
			height: 290vh;
		}
	}

	.sw-sticky {
		position: relative;
		z-index: 3;
		padding: 2.5rem var(--edge) 3rem;
		background: var(--hk-bg);
		--sw-glow: #4b6496;
	}
	@media (prefers-color-scheme: dark) {
		:global(:root:not(.light):not([data-theme='light'])) .sw-sticky {
			--sw-glow: #93a7d0;
		}
	}
	:global(:root[data-theme='dark']) .sw-sticky,
	:global(:root.dark) .sw-sticky {
		--sw-glow: #93a7d0;
	}
	@media (min-width: 1024px) {
		.sw-track--scroll .sw-sticky {
			position: sticky;
			top: var(--header-h, 57px);
			height: calc(100svh - var(--header-h, 57px));
			min-height: 44rem;
			max-height: 58rem;
			display: flex;
			align-items: center;
			padding: 4.5rem var(--edge) 3rem;
		}
		.sw-track:not(.sw-track--scroll) .sw-sticky {
			min-height: 46rem;
			display: flex;
			align-items: center;
			padding: 5.5rem var(--edge) 3.75rem;
		}
	}

	/* ---------- Full-bleed monumental 400%-scale background quarter-wheel ---------- */
	.sw-backdrop {
		position: absolute;
		top: 0;
		bottom: 0;
		left: 50%;
		width: 100vw;
		transform: translateX(-50%);
		overflow: hidden;
		pointer-events: none;
		container-type: size;
		background: var(--hk-bg);
	}
	.sw-dotfield {
		position: absolute;
		inset: 0;
		background: radial-gradient(circle at 1px 1px, var(--hero-dot) 1px, transparent 1.5px) 0 0 /
			20px 20px;
		opacity: 0.36;
	}
	.sw-mob-arc {
		position: absolute;
		top: -1px;
		right: 0;
		width: 13.5rem;
		height: 13.5rem;
		opacity: 0.32;
	}
	.sw-rotor {
		display: none;
	}
	@media (min-width: 1024px) {
		.sw-mob-arc {
			display: none;
		}
		.sw-rotor {
			--origin-x: 1.5rem;
			--drop: 108px;
			--r: min(78cqw, calc(101cqh + var(--drop)));
			display: block;
			position: absolute;
			left: calc(var(--origin-x) - var(--r));
			bottom: calc(-1 * var(--r) - var(--drop));
			width: calc(var(--r) * 2);
			height: calc(var(--r) * 2);
			transform-origin: 50% 50%;
			transform: rotate(calc(var(--angle) * -1));
			will-change: transform;
		}
	}
	@media (min-width: 1200px) {
		.sw-rotor {
			/* Outer frame left edge (40px clear of text column) so the 12-o'clock spoke never touches glyphs */
			--origin-x: calc(50cqw - 600px);
		}
	}
	.sw-rotor--snap {
		transition: transform 560ms cubic-bezier(0.22, 1, 0.36, 1);
	}
	@media (prefers-reduced-motion: reduce) {
		.sw-rotor--snap {
			transition: none;
		}
	}
	.sw-dial-svg {
		width: 100%;
		height: 100%;
	}
	.sw-ring-outer {
		stroke: var(--rule-strong);
		stroke-width: 1.25;
		opacity: 0.72;
	}
	.sw-ring-dash {
		stroke: var(--rule);
		stroke-width: 1;
		stroke-dasharray: 3 9;
	}
	.sw-ring-inner {
		stroke: var(--rule);
		stroke-width: 1;
		opacity: 0.56;
	}
	.sw-ring-faint {
		opacity: 0.34;
	}
	.sw-spoke {
		stroke: var(--rule);
		stroke-width: 1;
		opacity: 0.42;
	}
	.sw-arc-halo {
		stroke: transparent;
		stroke-width: 11;
		opacity: 0.11;
		transition: stroke 300ms ease;
	}
	.sw-arc-halo--active {
		stroke: var(--pic-accent);
	}
	.sw-arc {
		stroke: transparent;
		stroke-width: 2.25;
		opacity: 0.72;
		transition: stroke 280ms ease;
	}
	.sw-arc--active {
		stroke: var(--pic-accent);
	}
	.sw-node {
		fill: var(--surface);
		stroke: var(--rule-strong);
		stroke-width: 1.25;
	}
	.sw-node--active {
		fill: var(--pic-accent);
		stroke: var(--pic-accent);
		opacity: 0.8;
	}
	.sw-tick {
		stroke: var(--rule);
		stroke-width: 1;
	}
	.sw-tick-mid {
		stroke: var(--rule-strong);
		stroke-width: 1.15;
	}
	.sw-tick-major {
		stroke: var(--pic-accent);
		stroke-width: 1.75;
		opacity: 0.75;
	}

	/* ---------- Responsive mode toggle: Desktop Wheel vs Mobile Stacked Beats ---------- */
	.sw-desktop {
		display: none;
	}
	.sw-mobile {
		position: relative;
		z-index: 2;
		display: flex;
		flex-direction: column;
		gap: 1.75rem;
	}
	.sw-mob-head {
		display: flex;
		flex-direction: column;
		gap: 0.5rem;
	}
	.sw-mob-beats {
		margin: 0;
		padding: 0;
		list-style: none;
		display: flex;
		flex-direction: column;
		gap: 2.25rem;
	}
	.sw-mob-beats > li {
		display: flex;
		flex-direction: column;
		gap: 0.875rem;
	}
	.sw-mb-head {
		display: flex;
		align-items: baseline;
		gap: 0.5rem;
	}
	.sw-mb-head h3 {
		margin: 0;
		font-family: var(--hk-font-display);
		font-size: 1.25rem;
		font-weight: 400;
		line-height: 1.3;
		color: var(--hk-text);
	}

	@media (min-width: 1024px) {
		.sw-mobile {
			display: none;
		}
		.sw-desktop {
			position: relative;
			z-index: 2;
			width: 100%;
			display: grid;
			grid-template-columns: minmax(0, 22rem) minmax(0, 1fr);
			gap: 3.5rem;
			align-items: center;
		}
	}

	/* ---------- Desktop Left Narrative Rail ---------- */
	.sw-rail {
		display: flex;
		flex-direction: column;
		gap: 0.9375rem;
	}
	.sw-copy-stack {
		display: flex;
		flex-direction: column;
		justify-content: flex-start;
		gap: 0.625rem;
		height: 10.5rem;
	}
	.sw-lead {
		margin: 0;
		max-width: 21.5rem;
		font-size: 0.96875rem;
		line-height: 1.55;
		color: var(--hk-text-2);
		text-wrap: pretty;
	}

	.sw-nav {
		display: flex;
		flex-direction: column;
		gap: 0;
		max-width: 15.5rem;
		margin-top: 0.5rem;
		border-left: 1px solid var(--rule);
	}
	.sw-pill-btn {
		position: relative;
		display: inline-flex;
		align-items: center;
		min-height: 2.25rem;
		padding: 0.4375rem 0.875rem;
		border: 0;
		border-radius: 0;
		background: transparent;
		font: inherit;
		font-size: 0.875rem;
		color: var(--hk-text-3);
		text-align: left;
		cursor: pointer;
		transition: color 150ms ease;
	}
	.sw-pill-btn:hover,
	.sw-pill-btn.active {
		color: var(--hk-text);
	}
	.sw-pill-btn::before {
		content: '';
		position: absolute;
		left: -1.5px;
		top: 0.25rem;
		bottom: 0.25rem;
		width: 2px;
		border-radius: 1px;
		background: transparent;
		transition: background-color 160ms ease;
	}
	.sw-pill-btn.active::before {
		background: var(--pic-accent);
	}
	.sw-pill-t {
		font-weight: 500;
	}
	.sw-hint {
		display: inline-flex;
		align-items: center;
		gap: 0.375rem;
		margin: 0.25rem 0 0;
		font-family: var(--hk-font-mono);
		font-size: 0.75rem;
		color: var(--pic-faint);
	}

	/* ---------- Desktop Right Stage (uniform ~360px visual height across all 4 beats) ---------- */
	.sw-stage {
		position: relative;
		display: grid;
		align-items: center;
		min-height: 22.5rem;
	}
	.sw-panel-wrap {
		grid-area: 1 / 1;
		opacity: 0;
		transform: translateY(14px) scale(0.985);
		pointer-events: none;
		transition:
			opacity 300ms ease,
			transform 360ms cubic-bezier(0.22, 1, 0.36, 1);
	}
	.sw-panel-wrap.is-active {
		opacity: 1;
		transform: none;
		pointer-events: auto;
	}
	@media (prefers-reduced-motion: reduce) {
		.sw-panel-wrap {
			transition: none;
			transform: none;
		}
	}

	/* ---------- Beat 01: Side-by-side Cloud vs Your own hardware ---------- */

	/* ---------- Beat 02: Parallel Sandboxes Matrix + 5-Agent Footer ---------- */

	/* ---------- Beat 03: Clean Port-Forward + Browser & Editor ---------- */
	.sw-tools-wrap {
		display: flex;
		flex-direction: column;
		gap: 0.75rem;
	}
	.sw-port-card {
		display: flex;
		flex-direction: column;
		padding: 1rem 1.125rem;
		border: 1px solid var(--rule-strong);
		border-radius: 8px;
		background: var(--surface);
		box-shadow: 0 18px 44px -26px rgb(0 0 0 / 0.22);
	}
	.sw-p-box {
		border: 1px solid var(--rule);
		border-radius: 6px;
		background: var(--sunken);
		overflow: hidden;
	}
	.sw-p-bar {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.5rem;
		padding: 0.375rem 0.75rem;
		border-bottom: 1px solid var(--rule);
		background: var(--stage);
		font-family: var(--hk-font-mono);
		font-size: 0.6875rem;
	}
	.sw-p-who {
		font-weight: 600;
		color: var(--ink);
	}
	.sw-p-live {
		color: var(--pic-dim);
	}
	.sw-p-addr {
		padding: 0.0625rem 0.4375rem;
		border: 1px solid var(--pic-accent);
		border-radius: 4px;
		background: var(--surface);
		color: var(--pic-accent);
	}
	.sw-p-term {
		display: flex;
		flex-wrap: wrap;
		align-items: baseline;
		gap: 0.25rem 0.625rem;
		padding: 0.625rem 0.75rem;
		font-family: var(--hk-font-mono);
		font-size: 0.75rem;
	}
	.sw-t-dim {
		color: var(--pic-dim);
	}
	.sw-t-url {
		color: var(--pic-accent);
	}
	.sw-p-hop {
		display: flex;
		flex-direction: column;
		align-items: center;
		padding: 0.125rem 0;
	}
	.sw-p-wire {
		width: 0;
		height: 11px;
		border-left: 1.5px solid var(--pic-accent);
	}
	.sw-p-wire--arr {
		position: relative;
		height: 13px;
	}
	.sw-p-wire--arr::after {
		content: '';
		position: absolute;
		left: -5px;
		bottom: -1px;
		border: 4.5px solid transparent;
		border-top: 5.5px solid var(--pic-accent);
		border-bottom: 0;
	}
	.sw-p-chip {
		padding: 0.125rem 0.625rem;
		border: 1px solid var(--pic-accent);
		border-radius: 999px;
		background: var(--surface);
		font-family: var(--hk-font-mono);
		font-size: 0.6875rem;
		font-weight: 500;
		color: var(--pic-accent);
	}
	.sw-p-preview {
		padding: 0.625rem 0.75rem;
		background: var(--surface);
	}
	.sw-prev-nav {
		display: flex;
		align-items: baseline;
		justify-content: space-between;
		gap: 0.5rem;
		padding-bottom: 0.5rem;
		border-bottom: 1px solid var(--rule);
		font-size: 0.75rem;
	}
	.sw-prev-brand {
		font-family: var(--hk-font-display);
		font-weight: 400;
		font-size: 0.9375rem;
		color: var(--ink);
	}
	.sw-prev-links {
		color: var(--pic-dim);
		font-size: 0.6875rem;
	}
	.sw-prev-rows {
		display: flex;
		flex-direction: column;
		gap: 0.375rem;
		padding-top: 0.5rem;
	}
	.sw-prev-row {
		display: flex;
		align-items: baseline;
		justify-content: space-between;
		gap: 0.5rem;
		font-size: 0.75rem;
		color: var(--ink);
	}
	.sw-prev-row em {
		font-style: normal;
		font-family: var(--hk-font-mono);
		font-size: 0.6875rem;
		color: var(--pic-faint);
	}

	/* ---------- Beat 04: Away notifications ---------- */
	/* ---------- Beat 1: the same sandboxes, on our cloud or your machine ---------- */
	.sw-hw {
		display: grid;
		grid-template-columns: repeat(2, minmax(0, 1fr));
		gap: 1rem;
	}
	.sw-host {
		border: 1px solid var(--rule);
		border-radius: 8px;
		background: var(--surface);
		overflow: hidden;
	}
	.sw-host-head {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		margin: 0;
		padding: 0.75rem 1rem;
		border-bottom: 1px solid var(--rule);
		background: var(--sunken);
		font-size: 0.875rem;
		font-weight: 500;
		color: var(--ink);
	}
	.sw-host-head svg {
		color: var(--pic-accent);
		flex: none;
	}
	.sw-host-or {
		font-weight: 400;
		color: var(--pic-dim);
	}
	.sw-sbs {
		display: flex;
		flex-direction: column;
		gap: 0.5rem;
		margin: 0;
		padding: 1rem;
		list-style: none;
	}
	.sw-sb {
		display: flex;
		align-items: center;
		gap: 0.625rem;
		padding: 0.625rem 0.75rem;
		border: 1px solid var(--rule);
		border-radius: 6px;
		font-family: var(--hk-font-mono);
		font-size: 0.75rem;
		color: var(--pic-ink);
	}
	.sw-sb-dot {
		width: 6px;
		height: 6px;
		border-radius: 50%;
		background: var(--hk-ok-dot);
		flex: none;
	}
	.sw-sb-mark {
		display: inline-flex;
		margin-left: auto;
		color: var(--pic-dim);
	}

	/* ---------- Beat 2: three sandboxes, three logins ---------- */
	.sw-mix {
		display: flex;
		flex-direction: column;
		margin: 0;
		padding: 0;
		border: 1px solid var(--rule);
		border-radius: 8px;
		background: var(--surface);
		list-style: none;
		overflow: hidden;
	}
	.sw-mix li {
		display: grid;
		grid-template-columns: 2.5rem minmax(0, 1fr) auto;
		align-items: center;
		gap: 1rem;
		padding: 1rem 1.25rem;
	}
	.sw-mix li + li {
		border-top: 1px solid var(--rule);
	}
	.sw-mix-mark {
		display: grid;
		place-items: center;
		width: 2.5rem;
		height: 2.5rem;
		border: 1px solid var(--rule);
		border-radius: 8px;
		background: var(--sunken);
		color: var(--pic-ink);
	}
	.sw-mix-repo {
		font-family: var(--hk-font-mono);
		font-size: 0.8125rem;
		color: var(--pic-ink);
	}
	.sw-mix-login {
		font-family: var(--hk-font-mono);
		font-size: 0.75rem;
		color: var(--pic-dim);
	}

	/* ---------- Beat 3: the editor line under the port picture ---------- */
	.sw-t-vite {
		font-weight: 600;
		color: var(--pic-ink);
	}
	.sw-editors {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		justify-content: space-between;
		gap: 0.5rem 1rem;
		margin: 0.75rem 0 0;
		padding: 0.75rem 1rem;
		border: 1px solid var(--rule);
		border-radius: 8px;
		background: var(--surface);
		font-size: 0.8125rem;
		color: var(--pic-dim);
	}
	.sw-editors code {
		font-family: var(--hk-font-mono);
		font-size: 0.75rem;
		color: var(--pic-accent);
	}
	@media (max-width: 639.98px) {
		.sw-hw {
			grid-template-columns: minmax(0, 1fr);
		}
	}
</style>
