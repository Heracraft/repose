<!--
  ScrollWheel: the scroll-driven quarter-wheel (owner, 2026-11-22). On desktop
  (>= 1024px, motion allowed) a tall track pins a full-viewport stage; a
  large dial, its centre past the sheet's left edge, turns one step per beat
  as the visitor scrolls, bringing each beat's marker to a fixed index point:
  where it runs, the accounts, the tools. Phones stack
  the three beats. Each beat is a picture with a title, not a paragraph.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { agentMarks, type Mark } from '$lib/components/illustrations/marks';

	const BEATS = [
		{ id: 'hardware', short: 'Where it runs' },
		{ id: 'agents', short: 'Accounts' },
		{ id: 'tools', short: 'Your tools' }
	] as const;
	const LAST = BEATS.length - 1;
	/* The chapter number, with the space the label's text needs after it. */
	const chapter = '01 ';

	/* The dial turns STEP degrees per beat. STEP is no multiple of the 5, 15
	   and 30 degree tick spacing, so the ticks visibly move at every stop. */
	const STEP = 24;
	/* The fixed index point, degrees clockwise from noon: the beat whose
	   marker sits here is the active one. */
	const INDEX = 24;

	/* Three agents in one sandbox, each on its own account. Per docs/agents:
	   Claude Code logs in once with a claude.ai subscription, Codex's login is
	   copied from the laptop, Gemini CLI takes GEMINI_API_KEY as a secret. */
	const PARALLEL = [
		{ mark: agentMarks[0], login: 'Claude subscription', key: false },
		{ mark: agentMarks[1], login: 'ChatGPT login', key: false },
		{ mark: agentMarks[3], login: 'GEMINI_API_KEY', key: true }
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
				angle = idx * STEP;
				pinnedParam = true;
			}
		}

		const mqWide = window.matchMedia('(min-width: 1024px)');
		const mqReduce = window.matchMedia('(prefers-reduced-motion: reduce)');
		let raf = 0;

		function updateMode() {
			scrollDriven = mqWide.matches && !mqReduce.matches && !pinnedParam;
			if (!scrollDriven) {
				angle = step * STEP;
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
			angle = pos * STEP;
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
			angle = target * STEP;
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
	/* A point on the dial (viewBox 1200, centre 600) at deg clockwise from noon. */
	const at = (deg: number, r: number) => {
		const t = (deg * Math.PI) / 180;
		return `${(600 + r * Math.sin(t)).toFixed(1)} ${(600 - r * Math.cos(t)).toFixed(1)}`;
	};
	/* Beat i's marker sits at INDEX + i * STEP on the dial; turning the dial
	   back by i * STEP brings it to the index point. One accent arc runs
	   through the three markers and moves with them. */
	const markers = BEATS.map((_, i) => INDEX + i * STEP);
	const beatArc = `M ${at(INDEX - 6, 552)} A 552 552 0 0 1 ${at(INDEX + LAST * STEP + 6, 552)}`;
	const notch = `M ${at(INDEX, 566)} L ${at(INDEX, 590)}`;
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
	<div
		class="sw-hw"
		aria-label="billing-api and web-app run on repose cloud; ml-notebook runs on your machine."
	>
		<div class="sw-place">
			<svg
				class="sw-place-art"
				viewBox="0 0 64 48"
				fill="none"
				stroke="currentColor"
				stroke-width="1.5"
				stroke-linejoin="round"
				aria-hidden="true"
			>
				<path d="M17 40h31a10.5 10.5 0 0 0 1.6-20.9A16 16 0 0 0 19.2 15 12.5 12.5 0 0 0 17 40z" />
			</svg>
			<p class="sw-place-name">repose cloud</p>
			<ul class="sw-chips">
				<li>billing-api</li>
				<li>web-app</li>
			</ul>
		</div>
		<div class="sw-place">
			<svg
				class="sw-place-art"
				viewBox="0 0 64 48"
				fill="none"
				stroke="currentColor"
				stroke-width="1.5"
				stroke-linejoin="round"
				aria-hidden="true"
			>
				<rect x="6" y="18" width="52" height="18" rx="5" />
				<path d="M12 40h40" stroke-linecap="round" />
				<circle cx="49" cy="27" r="1.6" fill="currentColor" stroke="none" />
			</svg>
			<p class="sw-place-name">Your machine</p>
			<ul class="sw-chips">
				<li>ml-notebook</li>
			</ul>
		</div>
	</div>
{/snippet}

{#snippet agentsVisual()}
	<div
		class="sw-mix"
		aria-label="Claude Code on a Claude subscription, Codex on a ChatGPT login and Gemini CLI on a Gemini API key, all in one sandbox."
	>
		<ul class="sw-mix-list">
			{#each PARALLEL as vm (vm.login)}
				<li>
					<span class="sw-mix-mark" title={vm.mark.name}>{@render mark(vm.mark, 22)}</span>
					<span class="sw-mix-login" class:sw-mix-key={vm.key}>{vm.login}</span>
				</li>
			{/each}
		</ul>
		<svg class="sw-mix-join" viewBox="0 0 100 300" preserveAspectRatio="none" aria-hidden="true">
			<path d="M0 50 C 55 50, 45 150, 100 150" />
			<path d="M0 150 L 100 150" />
			<path d="M0 250 C 55 250, 45 150, 100 150" />
		</svg>
		<p class="sw-mix-box">your-project</p>
	</div>
{/snippet}

{#snippet toolsVisual()}
	<div
		class="sw-tools"
		aria-label="The sandbox's dev server opens in your laptop's browser at localhost:5173, and your editor opens the sandbox over SSH."
	>
		<p class="sw-src"><span class="sw-sb-dot" aria-hidden="true"></span>your-project</p>
		<div class="sw-wins">
			<div class="sw-win">
				<p class="sw-win-bar"><span class="sw-url">localhost:5173</span></p>
				<div class="sw-page" aria-hidden="true">
					<p class="sw-page-h">Todos</p>
					<p class="sw-page-t">3 open, 1 done</p>
				</div>
			</div>
			<div class="sw-win">
				<p class="sw-win-bar"><span class="sw-host-name">SSH: your-project.repose</span></p>
				<pre class="sw-code" aria-hidden="true">export function <b>App</b>() &#123;
  return &lt;Todos /&gt;;
&#125;</pre>
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
			<div class="sw-rotor" class:sw-rotor--snap={!scrollDriven} style="--angle: {angle}deg">
				<svg class="sw-dial-svg" viewBox="0 0 1200 1200" fill="none">
					<circle cx="600" cy="600" r="552" class="sw-ring-outer" />
					<circle cx="600" cy="600" r="522" class="sw-ring-dash" />
					<circle cx="600" cy="600" r="452" class="sw-ring-inner" />
					<circle cx="600" cy="600" r="326" class="sw-ring-inner sw-ring-faint" />
					<circle cx="600" cy="600" r="184" class="sw-ring-inner sw-ring-faint" />

					<path d={beatArc} class="sw-arc-halo" />
					<path d={beatArc} class="sw-arc" />
					{#each markers as d, idx (d)}
						<circle
							cx="600"
							cy="48"
							r="5"
							transform="rotate({d} 600 600)"
							class="sw-node"
							class:sw-node--active={step === idx}
						/>
					{/each}

					{#each rimTicks as deg (deg)}
						<line
							x1="600"
							y1={deg % 30 === 0 ? 54 : deg % 15 === 0 ? 57 : 63}
							x2="600"
							y2="72"
							class={deg % 15 === 0 ? 'sw-tick-mid' : 'sw-tick'}
							transform="rotate({deg} 600 600)"
						/>
					{/each}
				</svg>
			</div>
			<!-- The index point stays put while the dial turns under it. -->
			<div class="sw-rotor sw-index">
				<svg class="sw-dial-svg" viewBox="0 0 1200 1200" fill="none">
					<path d={notch} class="sw-notch" />
				</svg>
			</div>
		</div>

		<!-- Desktop monumental scroll-driven layout (>= 1024px) -->
		<div class="sw-inner sw-desktop">
			<div class="sw-rail">
				<p class="label num"><span class="n">{chapter}</span>Setup</p>

				<div class="sw-copy-stack">
					{#if step === 0}
						<h2 class="hk-h2" id="where-h">Our cloud, or a machine you already have</h2>
					{:else if step === 1}
						<h2 class="hk-h2" id="where-h">Mix subscriptions and API keys</h2>
					{:else}
						<h2 class="hk-h2" id="where-h">Use your browser and editor</h2>
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
				<p class="label num"><span class="n">{chapter}</span>Setup</p>
			</div>

			<ol class="sw-mob-beats">
				<li>
					<div class="sw-mb-head">
						<h3>Our cloud, or a machine you already have</h3>
					</div>
					{@render hardwareVisual()}
				</li>
				<li>
					<div class="sw-mb-head">
						<h3>Mix subscriptions and API keys</h3>
					</div>
					{@render agentsVisual()}
				</li>
				<li>
					<div class="sw-mb-head">
						<h3>Use your browser and editor</h3>
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
	.sw-rotor {
		display: none;
	}
	@media (min-width: 1024px) {
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
		.sw-rotor.sw-index {
			transform: none;
			will-change: auto;
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
	.sw-arc-halo {
		stroke: var(--pic-accent);
		stroke-width: 11;
		opacity: 0.1;
	}
	.sw-arc {
		stroke: var(--pic-accent);
		stroke-width: 2.25;
		opacity: 0.72;
	}
	.sw-node {
		fill: var(--hk-bg);
		stroke: var(--pic-accent);
		stroke-width: 1.5;
		transition: fill 200ms ease;
	}
	.sw-node--active {
		fill: var(--pic-accent);
	}
	.sw-notch {
		stroke: var(--pic-accent);
		stroke-width: 2.5;
		stroke-linecap: round;
	}
	.sw-tick {
		stroke: var(--rule);
		stroke-width: 1;
	}
	.sw-tick-mid {
		stroke: var(--rule-strong);
		stroke-width: 1.15;
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
		flex-direction: column;
	}
	.sw-mb-head h3 {
		margin: 0;
		font-family: var(--hk-font-display);
		font-size: 1.25rem;
		font-weight: 400;
		line-height: 1.3;
		color: var(--hk-text);
		text-wrap: balance;
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
			grid-template-columns: minmax(0, 26rem) minmax(0, 1fr);
			gap: 3rem;
			align-items: center;
			align-self: stretch;
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
		height: 7rem;
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
	/* ---------- Desktop right stage: the figure sits past the dial's rim, in
	   the top-right corner the quarter leaves open. ---------- */
	.sw-stage {
		position: relative;
		display: grid;
	}
	@media (min-width: 1024px) {
		.sw-stage {
			align-self: start;
		}
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
	@media (min-width: 1024px) {
		.sw-panel-wrap {
			justify-self: end;
			width: min(100%, 24rem);
		}
	}
	@media (min-width: 1200px) {
		.sw-panel-wrap {
			width: min(100%, 27.5rem);
		}
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

	/* ---------- Beat 1: two places, each with its own sandboxes ---------- */
	.sw-hw {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 1.5rem;
	}
	.sw-place {
		display: flex;
		flex-direction: column;
		align-items: flex-start;
		gap: 0.75rem;
		padding-top: 1rem;
		border-top: 1px solid var(--rule-strong);
	}
	.sw-place-art {
		width: 4rem;
		height: 3rem;
		color: var(--pic-ink);
	}
	.sw-place-name {
		margin: 0;
		font-size: 0.9375rem;
		font-weight: 500;
		color: var(--hk-text);
	}
	.sw-chips {
		display: flex;
		flex-direction: column;
		align-items: flex-start;
		gap: 0.5rem;
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.sw-chips li,
	.sw-mix-box {
		padding: 0.375rem 0.625rem;
		border: 1px solid var(--rule);
		border-radius: 6px;
		background: var(--surface);
		font-family: var(--hk-font-mono);
		font-size: 0.75rem;
		color: var(--pic-ink);
	}

	/* ---------- Beat 2: three accounts, one sandbox ---------- */
	.sw-mix {
		display: grid;
		grid-template-columns: auto minmax(2rem, 1fr) auto;
		align-items: center;
	}
	.sw-mix-list {
		display: grid;
		grid-auto-rows: 3.5rem;
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.sw-mix-list li {
		display: flex;
		align-items: center;
		gap: 0.75rem;
		padding-right: 0.75rem;
	}
	.sw-mix-mark {
		display: inline-flex;
		color: var(--pic-ink);
	}
	.sw-mix-login {
		font-size: 0.875rem;
		color: var(--hk-text);
	}
	.sw-mix-key {
		font-family: var(--hk-font-mono);
		font-size: 0.8125rem;
		color: var(--pic-accent);
	}
	.sw-mix-join {
		width: 100%;
		height: 10.5rem;
		fill: none;
	}
	.sw-mix-join path {
		stroke: var(--rule-strong);
		stroke-width: 1;
		vector-effect: non-scaling-stroke;
	}
	.sw-mix-box {
		margin: 0;
		padding: 0.5rem 0.75rem;
	}

	/* ---------- Beat 3: dev server to the browser, the checkout to the editor ---------- */
	.sw-tools {
		display: flex;
		flex-direction: column;
		align-items: flex-start;
	}
	.sw-src {
		display: inline-flex;
		align-items: center;
		gap: 0.5rem;
		margin: 0;
		padding: 0.5rem 0.75rem;
		border: 1px solid var(--rule);
		border-radius: 6px;
		background: var(--surface);
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
	.sw-wins {
		display: flex;
		flex-direction: column;
		align-self: stretch;
		gap: 1rem;
		margin: 0 0 0 1.0625rem;
		padding: 1rem 0 0 1.25rem;
	}
	/* A tree from the sandbox to each window: the stem runs down to the last
	   branch and stops there. */
	.sw-wins .sw-win {
		position: relative;
	}
	.sw-wins .sw-win::before,
	.sw-wins .sw-win:not(:last-child)::after {
		content: '';
		position: absolute;
		left: calc(-1.25rem - 1px);
		border-left: 1px solid var(--rule-strong);
	}
	.sw-wins .sw-win::before {
		top: -1rem;
		width: 1.25rem;
		height: 2.125rem;
		border-bottom: 1px solid var(--rule-strong);
		border-bottom-left-radius: 6px;
	}
	.sw-wins .sw-win:not(:last-child)::after {
		top: 1rem;
		bottom: 0;
	}
	.sw-win {
		border: 1px solid var(--rule);
		border-radius: 6px;
		background: var(--surface);
	}
	.sw-win-bar {
		margin: 0;
		padding: 0.5rem 0.75rem;
		border-bottom: 1px solid var(--rule);
		font-family: var(--hk-font-mono);
		font-size: 0.75rem;
	}
	.sw-url {
		color: var(--pic-accent);
	}
	.sw-host-name {
		color: var(--pic-dim);
	}
	.sw-page {
		padding: 0.875rem 0.875rem 1rem;
	}
	.sw-page-h {
		margin: 0;
		font-family: var(--hk-font-display);
		font-size: 0.875rem;
		color: var(--hk-text);
	}
	.sw-page-t {
		margin: 0.25rem 0 0;
		font-size: 0.75rem;
		color: var(--hk-text-2);
	}
	.sw-code {
		margin: 0;
		padding: 0.875rem;
		font-family: var(--hk-font-mono);
		font-size: 0.6875rem;
		line-height: 1.7;
		color: var(--pic-ink);
		white-space: pre;
		overflow: hidden;
	}
	.sw-code b {
		font-weight: 400;
		color: var(--pic-accent);
	}
</style>
