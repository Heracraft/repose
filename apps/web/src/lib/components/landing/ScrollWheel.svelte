<!--
  ScrollWheel: monumental, scroll-driven 400%-scale quarter-wheel showcase.
  On desktop (>= 1024px, motion allowed), a tall scroll track pins a full-viewport
  architectural stage where a giant 400%-area circular dial anchored cleanly to the
  1200px left sheet rail integrates cleanly into the page background and rotates
  smoothly through four customer-value beats as the visitor scrolls (0° -> -90° -> -180° -> -270°).
  On phones (< 1024px), all four concise beats stack cleanly vertically so mobile scrollers
  discover Cloud/Own hardware, Parallel agents, Localhost/Browser/Editor, and Away alerts
  without having to tap through hidden tabs.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { price } from '$lib/format';
	import { agentMarks, type Mark } from '$lib/components/illustrations/marks';

	let {
		soloPriceCents,
		ownPriceCents
	}: {
		soloPriceCents: number;
		ownPriceCents: number;
	} = $props();

	const BEATS = [
		{ id: 'hardware', num: '1.1', label: 'Hardware', short: 'Cloud or yours' },
		{ id: 'agents', num: '1.2', label: 'Agents', short: 'All 5 agents' },
		{ id: 'tools', num: '1.3', label: 'Tools', short: 'Your tools' },
		{ id: 'away', num: '1.4', label: 'Away', short: 'Walk away' }
	] as const;

	const PARALLEL_VMS = [
		{
			repo: 'billing-api',
			mark: agentMarks[0],
			task: 'Running proration tests',
			state: 'Full perms',
			active: true
		},
		{
			repo: 'web-app',
			mark: agentMarks[1],
			task: 'Refactoring OAuth callback',
			state: 'Full perms',
			active: true
		},
		{
			repo: 'infra-cli',
			mark: agentMarks[3],
			task: 'Waiting for next task',
			state: 'Auto-frozen',
			active: false
		}
	];

	let trackEl = $state<HTMLElement | null>(null);
	let step = $state(0);
	let angle = $state(0);
	let scrollDriven = $state(false);
	let pinnedParam = $state(false);

	onMount(() => {
		const q = new URLSearchParams(window.location.search).get('wheel');
		if (q) {
			const idx = BEATS.findIndex(
				(b) => b.id === q || b.num === q || String(BEATS.indexOf(b)) === q
			);
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
			const scaled = raw * 3;
			const base = Math.floor(scaled);
			const frac = scaled - base;
			const eased = frac < 0.22 ? 0 : frac > 0.78 ? 1 : (frac - 0.22) / 0.56;
			const smooth = eased * eased * (3 - 2 * eased);
			const pos = Math.min(3, base + smooth);
			angle = pos * 90;
			step = Math.min(3, Math.round(pos));
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
		const target = ((i % 4) + 4) % 4;
		if (scrollDriven && trackEl) {
			const rect = trackEl.getBoundingClientRect();
			const trackTop = rect.top + window.scrollY;
			const vh = window.innerHeight - 57;
			const scrollable = Math.max(1, rect.height - vh);
			const y = trackTop - 57 + (target / 3) * scrollable;
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
	const rimLabels = [
		{ rot: 0, text: '1.1 · HARDWARE' },
		{ rot: 90, text: '1.2 · AGENTS' },
		{ rot: 180, text: '1.3 · TOOLS' },
		{ rot: 270, text: '1.4 · AWAY' }
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

{#snippet hardwareVisual()}
	<div class="sw-hw-grid">
		<div class="sw-card">
			<div class="sw-c-head">
				<span class="sw-tag">Hosted</span>
				<span class="sw-price"><em>From</em> {price(soloPriceCents)}/mo</span>
			</div>
			<h3 class="sw-c-title">repose Cloud</h3>
			<ul class="sw-list">
				<li>Dedicated Ubuntu VM per project</li>
				<li>Auto-freezes when idle</li>
				<li>Automatic cloud snapshots</li>
			</ul>
			<p class="sw-cmd"><span>$</span> repose run</p>
		</div>

		<div class="sw-card sw-card--accent">
			<div class="sw-c-head">
				<span class="sw-tag sw-tag--accent">Bring your own</span>
				<span class="sw-price">{price(ownPriceCents)}/mo</span>
			</div>
			<h3 class="sw-c-title">Your own hardware</h3>
			<ul class="sw-list">
				<li>Same isolated VMs &amp; local snapshots</li>
				<li>Mac mini (M3+) or Linux x86 with KVM</li>
				<li>Your RAM and disk, no VPN setup</li>
			</ul>
			<p class="sw-cmd"><span>$</span> repose host add</p>
		</div>
	</div>
{/snippet}

{#snippet agentsVisual()}
	<div class="sw-card sw-card--wide sw-agents-card">
		<div class="sw-c-head">
			<span class="sw-tag sw-tag--accent">Parallel sandboxes</span>
			<span class="sw-badge-ok">Full permissions on</span>
		</div>
		<ul class="sw-vm-list" aria-label="Parallel agent sandboxes">
			{#each PARALLEL_VMS as vm (vm.repo)}
				<li class="sw-vm-row">
					<div class="sw-vm-left">
						<span class="sw-vm-dot" class:sw-vm-dot--live={vm.active} aria-hidden="true"></span>
						<span class="sw-vm-repo">{vm.repo}</span>
					</div>
					<span class="sw-vm-agent">
						<span class="sw-ag-ic">{@render mark(vm.mark, 14)}</span>
						<span>{vm.mark.name}</span>
					</span>
					<span class="sw-vm-task">{vm.task}</span>
					<span class="sw-vm-state" class:sw-vm-state--live={vm.active}>{vm.state}</span>
				</li>
			{/each}
		</ul>
		<div class="sw-ag-foot">
			<div class="sw-ag-icons" aria-label="Preinstalled agents: Claude Code, Codex, opencode, Gemini CLI, pi">
				{#each agentMarks as m (m.name)}
					<span class="sw-mini-chip" title={m.name}>
						{@render mark(m, 13)}
						<span>{m.name}</span>
					</span>
				{/each}
			</div>
			<span class="sw-ag-byo">Your own subscriptions or API keys · No token markup</span>
		</div>
	</div>
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
					<span class="sw-t-green">VITE v8.0.1</span>
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

		<div class="sw-tool-pills">
			<div class="sw-tp">
				<span class="sw-tr-k">Browser</span>
				<span class="sw-tr-v">Sign in or inspect live Chromium</span>
				<code class="sw-tr-c">$ repose browser</code>
			</div>
			<div class="sw-tp">
				<span class="sw-tr-k">Editor</span>
				<span class="sw-tr-v">VS Code, Cursor, Zed, Neovim</span>
				<code class="sw-tr-c">$ ssh project.repose</code>
			</div>
		</div>
	</div>
{/snippet}

{#snippet awayVisual()}
	<div class="sw-card sw-card--wide">
		<div class="sw-c-head">
			<span class="sw-tag sw-tag--accent">Unattended</span>
			<span class="sw-meta-mono">email · ntfy · Google Chat</span>
		</div>
		<div class="sw-ping-box">
			<div class="sw-ping-rail sw-ping-rail--done"></div>
			<div class="sw-ping-body">
				<div class="sw-ping-top">
					<span class="sw-ping-title">billing-api · claude is done</span>
					<span class="sw-ping-badge sw-ping-badge--done">Passing</span>
				</div>
				<p class="sw-ping-msg">Wrote proration tests and opened PR #42. All 6 checks passing.</p>
			</div>
		</div>
		<div class="sw-ping-box">
			<div class="sw-ping-rail sw-ping-rail--ask"></div>
			<div class="sw-ping-body">
				<div class="sw-ping-top">
					<span class="sw-ping-title">checkout · codex needs input</span>
					<span class="sw-ping-badge sw-ping-badge--ask">Waiting</span>
				</div>
				<p class="sw-ping-msg">Drop legacy sessions table before migrating?</p>
				<p class="sw-ping-cue">
					Reply in dashboard or run <code>repose attach checkout</code>
				</p>
			</div>
		</div>
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
					<defs>
						<path id="sw-label-arc" d="M 600 22 A 578 578 0 0 1 1178 600" />
					</defs>

					<circle cx="600" cy="600" r="552" class="sw-ring-outer" />
					<circle cx="600" cy="600" r="522" class="sw-ring-dash" />
					<circle cx="600" cy="600" r="452" class="sw-ring-inner" />
					<circle cx="600" cy="600" r="326" class="sw-ring-inner sw-ring-faint" />
					<circle cx="600" cy="600" r="184" class="sw-ring-inner sw-ring-faint" />

					<line x1="600" y1="48" x2="600" y2="1152" class="sw-spoke" />
					<line x1="48" y1="600" x2="1152" y2="600" class="sw-spoke" />

					{#each rimLabels as q, idx (q.rot)}
						<g transform="rotate({q.rot} 600 600)">
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
							<text class="sw-rim-text" class:sw-rim-text--active={step === idx}>
								<textPath href="#sw-label-arc" startOffset="19%">{q.text}</textPath>
							</text>
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
				<p class="label num"><span class="n">01 </span>How it works</p>

				<div class="sw-copy-stack">
					{#if step === 0}
						<h2 class="hk-h2" id="where-h">Our cloud, or your own hardware</h2>
						<p class="sw-lead">Hosted Linux VMs, or a Mac mini or Linux server you already run.</p>
					{:else if step === 1}
						<h2 class="hk-h2" id="where-h">All 5 agents, preinstalled</h2>
						<p class="sw-lead">
							Run parallel agents in isolated VMs using your own accounts. No token markup.
						</p>
					{:else if step === 2}
						<h2 class="hk-h2" id="where-h">Works with your browser and editor</h2>
						<p class="sw-lead">
							Dev ports forward to localhost. Take over the agent's browser or attach over SSH.
						</p>
					{:else}
						<h2 class="hk-h2" id="where-h">Close the lid, get pinged when done</h2>
						<p class="sw-lead">
							Tasks keep running unattended. Get alerted when an agent finishes or needs input.
						</p>
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

				<div
					class="sw-panel-wrap"
					id="sw-stage-away"
					role="tabpanel"
					aria-labelledby="sw-tab-away"
					class:is-active={step === 3}
					aria-hidden={step !== 3}
				>
					{@render awayVisual()}
				</div>
			</div>
		</div>

		<!-- Mobile stacked showcase (< 1024px): all 4 beats visible on natural scroll with zero hidden tabs -->
		<div class="sw-mobile" aria-label="How repose works">
			<div class="sw-mob-head">
				<p class="label num"><span class="n">01 </span>How it works</p>
			</div>

			<ol class="sw-mob-beats">
				<li>
					<div class="sw-mb-head">
						<span class="label">1.1</span>
						<h3>Our cloud, or your own hardware</h3>
					</div>
					{@render hardwareVisual()}
				</li>
				<li>
					<div class="sw-mb-head">
						<span class="label">1.2</span>
						<h3>All 5 agents in parallel sandboxes</h3>
					</div>
					{@render agentsVisual()}
				</li>
				<li>
					<div class="sw-mb-head">
						<span class="label">1.3</span>
						<h3>Works with your browser and editor</h3>
					</div>
					{@render toolsVisual()}
				</li>
				<li>
					<div class="sw-mb-head">
						<span class="label">1.4</span>
						<h3>Close the lid, get pinged when done</h3>
					</div>
					{@render awayVisual()}
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
		background:
			radial-gradient(
				64% 72% at 62% 52%,
				color-mix(in srgb, var(--sw-glow) 8%, transparent),
				transparent 70%
			),
			var(--hk-bg);
	}
	.sw-dotfield {
		position: absolute;
		inset: 0;
		background: radial-gradient(circle at 1px 1px, var(--hero-dot) 1px, transparent 1.5px) 0 0 /
			20px 20px;
		opacity: 0.36;
		mask-image: radial-gradient(80% 90% at 72% 48%, #000 18%, transparent 90%);
	}
	.sw-mob-arc {
		position: absolute;
		top: -1px;
		right: 0;
		width: 13.5rem;
		height: 13.5rem;
		opacity: 0.32;
		mask-image: radial-gradient(100% 100% at 100% 0%, #000 45%, transparent 95%);
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
		.sw-backdrop {
			mask-image: linear-gradient(to bottom, #000 0%, #000 94%, rgb(0 0 0 / 0.35) 100%);
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
	.sw-rim-text {
		font-family: var(--hk-font-mono);
		font-size: 12.5px;
		font-weight: 500;
		letter-spacing: 0.18em;
		fill: var(--pic-faint);
	}
	.sw-rim-text--active {
		fill: var(--pic-accent);
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
	.sw-hw-grid {
		display: grid;
		grid-template-columns: minmax(0, 1fr);
		gap: 0.875rem;
	}
	@media (min-width: 640px) {
		.sw-hw-grid {
			grid-template-columns: repeat(2, minmax(0, 1fr));
		}
	}

	.sw-card {
		display: flex;
		flex-direction: column;
		gap: 0.9375rem;
		padding: 1.5rem;
		border: 1px solid var(--rule-strong);
		border-radius: 8px;
		background: var(--surface);
		box-shadow: 0 18px 44px -26px rgb(0 0 0 / 0.22);
	}
	.sw-card--accent {
		border-color: color-mix(in srgb, var(--pic-accent) 55%, var(--rule-strong));
		box-shadow:
			inset 0 1px 0 color-mix(in srgb, var(--pic-accent) 45%, transparent),
			0 18px 44px -26px rgb(0 0 0 / 0.22);
	}
	.sw-card--wide {
		padding: 1.5rem;
		gap: 1.125rem;
	}
	@media (max-width: 639.98px) {
		.sw-card,
		.sw-card--wide {
			padding: 1rem;
			gap: 0.875rem;
		}
	}

	.sw-c-head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.5rem;
		flex-wrap: wrap;
	}
	.sw-tag {
		padding: 0.1875rem 0.5rem;
		border: 1px solid var(--rule-strong);
		border-radius: 5px;
		background: var(--sunken);
		font-family: var(--hk-font-mono);
		font-size: 0.6875rem;
		font-weight: 500;
		letter-spacing: 0.04em;
		text-transform: uppercase;
		color: var(--pic-dim);
	}
	.sw-tag--accent {
		border-color: var(--pic-accent);
		color: var(--pic-accent);
	}
	.sw-price {
		font-family: var(--hk-font-display);
		font-size: 1.125rem;
		color: var(--ink);
		font-variant-numeric: tabular-nums;
		white-space: nowrap;
	}
	.sw-price em {
		font-family: var(--hk-font-sans);
		font-style: normal;
		font-size: 0.75rem;
		color: var(--pic-dim);
		margin-right: 0.125rem;
	}
	.sw-c-title {
		margin: 0;
		font-family: var(--hk-font-display);
		font-size: 1.25rem;
		font-weight: 400;
		line-height: 1.25;
		color: var(--ink);
	}
	.sw-list {
		margin: 0;
		padding: 0;
		list-style: none;
		display: flex;
		flex-direction: column;
		gap: 0.5rem;
		font-size: 0.875rem;
		line-height: 1.4;
		color: var(--hk-text-2);
	}
	.sw-list li {
		display: flex;
		align-items: baseline;
		gap: 0.5rem;
	}
	.sw-list li::before {
		content: '✓';
		color: var(--pic-accent);
		font-family: var(--hk-font-mono);
		font-size: 0.75rem;
		flex: none;
	}
	.sw-cmd {
		margin: auto 0 0;
		padding: 0.5625rem 0.75rem;
		border: 1px solid var(--rule-strong);
		border-radius: 6px;
		background: var(--sunken);
		font-family: var(--hk-font-mono);
		font-size: 0.75rem;
		color: var(--ink);
	}
	.sw-cmd span {
		color: var(--pic-accent);
		margin-right: 0.375rem;
	}

	/* ---------- Beat 02: Parallel Sandboxes Matrix + 5-Agent Footer ---------- */
	.sw-badge-ok {
		padding: 0.1875rem 0.5625rem;
		border: 1px solid color-mix(in srgb, var(--hk-ok) 40%, transparent);
		border-radius: 999px;
		background: color-mix(in srgb, var(--hk-ok) 10%, transparent);
		font-family: var(--hk-font-mono);
		font-size: 0.6875rem;
		font-weight: 500;
		color: var(--hk-ok);
	}
	.sw-vm-list {
		margin: 0;
		padding: 0;
		list-style: none;
		display: flex;
		flex-direction: column;
		gap: 0.5rem;
	}
	.sw-vm-row {
		display: grid;
		grid-template-columns: minmax(0, auto) minmax(0, 1fr) auto;
		align-items: center;
		gap: 0.3125rem 0.625rem;
		padding: 0.6875rem 0.875rem;
		border: 1px solid var(--rule);
		border-radius: 6px;
		background: var(--sunken);
		font-size: 0.8125rem;
	}
	@media (min-width: 640px) {
		.sw-vm-row {
			grid-template-columns: 7.25rem 7.5rem minmax(0, 1fr) auto;
			gap: 0.375rem 0.75rem;
		}
	}
	.sw-vm-left {
		display: inline-flex;
		align-items: center;
		gap: 0.5rem;
		min-width: 0;
	}
	.sw-vm-dot {
		width: 7px;
		height: 7px;
		border-radius: 999px;
		background: var(--pic-faint);
		flex: none;
	}
	.sw-vm-dot--live {
		background: var(--hk-ok-dot);
		box-shadow: 0 0 0 3px color-mix(in srgb, var(--hk-ok-dot) 18%, transparent);
	}
	.sw-vm-repo {
		font-family: var(--hk-font-mono);
		font-size: 0.75rem;
		font-weight: 600;
		color: var(--ink);
		white-space: nowrap;
	}
	.sw-vm-agent {
		display: inline-flex;
		align-items: center;
		gap: 0.375rem;
		font-size: 0.75rem;
		font-weight: 500;
		color: var(--ink);
		white-space: nowrap;
	}
	.sw-ag-ic {
		display: flex;
		color: var(--ink);
		flex: none;
	}
	.sw-vm-task {
		color: var(--hk-text-2);
		font-size: 0.8125rem;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	@media (max-width: 639.98px) {
		.sw-vm-task {
			grid-column: 1 / -1;
			grid-row: 2;
			font-size: 0.75rem;
		}
		.sw-vm-state {
			grid-column: 3;
			grid-row: 1;
			justify-self: end;
		}
	}
	.sw-vm-state {
		padding: 0.125rem 0.4375rem;
		border-radius: 4px;
		background: var(--stage);
		font-family: var(--hk-font-mono);
		font-size: 0.6875rem;
		color: var(--pic-dim);
		white-space: nowrap;
	}
	.sw-vm-state--live {
		background: color-mix(in srgb, var(--hk-ok) 11%, transparent);
		color: var(--hk-ok);
	}
	.sw-ag-foot {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		justify-content: space-between;
		gap: 0.5rem 0.75rem;
		padding-top: 0.875rem;
		border-top: 1px solid var(--rule);
	}
	.sw-ag-icons {
		display: flex;
		flex-wrap: wrap;
		gap: 0.375rem;
	}
	.sw-mini-chip {
		display: inline-flex;
		align-items: center;
		gap: 0.3125rem;
		padding: 0.25rem 0.5rem;
		border: 1px solid var(--rule);
		border-radius: 5px;
		background: var(--sunken);
		font-size: 0.71875rem;
		font-weight: 500;
		color: var(--ink);
		white-space: nowrap;
	}
	.sw-ag-byo {
		font-family: var(--hk-font-mono);
		font-size: 0.6875rem;
		color: var(--pic-dim);
		white-space: nowrap;
	}

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
		color: var(--hk-ok);
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
	.sw-t-green {
		font-weight: 600;
		color: var(--t-green);
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

	.sw-tool-pills {
		display: grid;
		grid-template-columns: minmax(0, 1fr);
		gap: 0.625rem;
	}
	@media (min-width: 640px) {
		.sw-tool-pills {
			grid-template-columns: repeat(2, minmax(0, 1fr));
		}
	}
	.sw-tp {
		display: flex;
		flex-direction: column;
		gap: 0.25rem;
		padding: 0.75rem 0.875rem;
		border: 1px solid var(--rule-strong);
		border-radius: 8px;
		background: var(--surface);
	}
	.sw-meta-mono {
		font-family: var(--hk-font-mono);
		font-size: 0.75rem;
		color: var(--pic-faint);
	}
	.sw-tr-k {
		font-family: var(--hk-font-mono);
		font-size: 0.6875rem;
		text-transform: uppercase;
		letter-spacing: 0.05em;
		color: var(--pic-accent);
	}
	.sw-tr-v {
		font-size: 0.8125rem;
		color: var(--ink);
	}
	.sw-tr-c {
		align-self: flex-start;
		margin-top: 0.1875rem;
		padding: 0.125rem 0.4375rem;
		border: 1px solid var(--rule);
		border-radius: 4px;
		background: var(--sunken);
		font-family: var(--hk-font-mono);
		font-size: 0.75rem;
		color: var(--pic-dim);
		white-space: nowrap;
	}

	/* ---------- Beat 04: Away notifications ---------- */
	.sw-ping-box {
		display: flex;
		border: 1px solid var(--rule);
		border-radius: 6px;
		background: var(--sunken);
		overflow: hidden;
	}
	.sw-ping-rail {
		width: 3px;
		flex: none;
	}
	.sw-ping-rail--done {
		background: var(--hk-ok-dot);
	}
	.sw-ping-rail--ask {
		background: var(--pic-accent);
	}
	.sw-ping-body {
		flex: 1;
		min-width: 0;
		padding: 0.9375rem 1rem;
		display: flex;
		flex-direction: column;
		gap: 0.375rem;
	}
	.sw-ping-top {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.5rem;
	}
	.sw-ping-title {
		font-family: var(--hk-font-mono);
		font-size: 0.8125rem;
		font-weight: 600;
		color: var(--ink);
	}
	@media (max-width: 479.98px) {
		.sw-ping-title {
			font-size: 0.71875rem;
		}
	}
	.sw-ping-badge {
		padding: 0.0625rem 0.4375rem;
		border-radius: 999px;
		font-family: var(--hk-font-mono);
		font-size: 0.6875rem;
		font-weight: 500;
		white-space: nowrap;
	}
	.sw-ping-badge--done {
		background: color-mix(in srgb, var(--hk-ok) 12%, transparent);
		color: var(--hk-ok);
	}
	.sw-ping-badge--ask {
		background: color-mix(in srgb, var(--pic-accent) 12%, transparent);
		color: var(--pic-accent);
	}
	.sw-ping-msg {
		margin: 0;
		font-size: 0.875rem;
		line-height: 1.4;
		color: var(--hk-text-2);
	}
	.sw-ping-cue {
		margin: 0.125rem 0 0;
		font-size: 0.75rem;
		line-height: 1.65;
		color: var(--pic-dim);
	}
	.sw-ping-cue code {
		display: inline-block;
		padding: 0.0625rem 0.375rem;
		border: 1px solid var(--rule-strong);
		border-radius: 4px;
		background: var(--surface);
		font-family: var(--hk-font-mono);
		font-size: 0.75rem;
		color: var(--ink);
		white-space: nowrap;
	}
</style>
