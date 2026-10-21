<script lang="ts">
	import { resolve } from '$app/paths';
	import { FEEDBACK_URL, SOURCE_URL, pricingHref } from '$lib/links';
	import Logo from './Logo.svelte';
	import ThemeSwitch from './ThemeSwitch.svelte';

	let {
		compact = false
	}: {
		/** One row (dashboard pages, a signed-in error page): links and the theme switch. */
		compact?: boolean;
	} = $props();
</script>

<!--
  The one footer (DECISIONS I-655, I-837), on every page, on the shared
  frame, and the one home of the theme switch. Full on the landing, the
  docs and the legal pages: the mark and word, then Product and Policies,
  then the switch on its own row. Compact on the dashboard: the links and
  the switch on one row. Both keep nav[aria-label=Footer].
-->
<footer class="site-footer" class:site-footer--compact={compact}>
	{#if compact}
		<div class="frame row">
			<nav aria-label="Footer">
				<ul class="inline">
					<li><a href={resolve('/docs')}>Docs</a></li>
					<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -- off-site -->
					<li><a href={FEEDBACK_URL}>Feedback</a></li>
					<li><a href={resolve('/terms')}>Terms</a></li>
					<li><a href={resolve('/privacy')}>Privacy</a></li>
					<li><a href={resolve('/refunds')}>Refunds</a></li>
				</ul>
			</nav>
			<ThemeSwitch />
		</div>
	{:else}
		<div class="frame">
			<div class="grid">
				<div class="brand"><Logo size="sm" mark /></div>
				<nav aria-label="Footer" class="cols">
					<div>
						<h2 class="head">Product</h2>
						<ul>
							<!-- eslint-disable svelte/no-navigation-without-resolve -- pricingHref() builds it with resolve(); llms.txt is a file route; the rest are off-site -->
							<li><a href={resolve('/docs')}>Docs</a></li>
							<li><a href={pricingHref()}>Pricing</a></li>
							<li><a href="/llms.txt">llms.txt</a></li>
							<li><a href={SOURCE_URL}>GitHub</a></li>
							<li><a href={FEEDBACK_URL}>Feedback</a></li>
							<!-- eslint-enable svelte/no-navigation-without-resolve -->
						</ul>
					</div>
					<div>
						<h2 class="head">Policies</h2>
						<ul>
							<li><a href={resolve('/terms')}>Terms</a></li>
							<li><a href={resolve('/privacy')}>Privacy</a></li>
							<li><a href={resolve('/refunds')}>Refunds</a></li>
						</ul>
					</div>
				</nav>
			</div>
			<div class="bottom">
				<ThemeSwitch />
			</div>
		</div>
	{/if}
</footer>

<style>
	.site-footer {
		border-top: 1px solid var(--hk-hairline);
		padding: 3rem 0 2rem;
		font-size: 0.8125rem;
	}
	.site-footer--compact {
		padding: 1.25rem 0;
	}
	/* The rail's height counts on this row's (--dash-foot-h, I-841). */
	@media (min-width: 1024px) {
		.site-footer--compact {
			box-sizing: border-box;
			height: var(--dash-foot-h);
		}
	}
	ul {
		margin: 0;
		padding: 0;
		list-style: none;
	}
	a {
		color: var(--hk-text-3);
		text-decoration: none;
		transition: color var(--hk-dur-fast) var(--hk-ease);
	}
	a:hover {
		color: var(--hk-text);
	}
	.grid {
		display: grid;
		grid-template-columns: 1fr;
		gap: 2rem;
	}
	.cols {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 2rem;
	}
	@media (min-width: 768px) {
		.grid {
			grid-template-columns: 2fr 2fr;
		}
	}
	.cols ul {
		display: grid;
		gap: 0.5rem;
	}
	/* A column's head: 12px sans 600, never the heading serif. */
	.head {
		margin: 0 0 0.75rem;
		font-family: var(--hk-font-sans);
		font-size: 0.75rem;
		font-weight: 600;
		letter-spacing: 0;
		line-height: 1rem;
		color: var(--hk-text);
	}
	.bottom {
		margin-top: 2.5rem;
		padding-top: 1.25rem;
		border-top: 1px solid var(--hk-hairline);
	}
	.row {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		justify-content: space-between;
		gap: 1rem 1.5rem;
	}
	.inline {
		display: flex;
		flex-wrap: wrap;
		gap: 0.5rem 1.25rem;
	}
	@media (pointer: coarse), (max-width: 639.98px) {
		a {
			display: inline-flex;
			align-items: center;
			min-height: 2.75rem;
		}
		.cols ul {
			gap: 0;
		}
	}
</style>
