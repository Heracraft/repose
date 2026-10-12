<script lang="ts">
	import { resolve } from '$app/paths';
	import { FEEDBACK_URL, SOURCE_URL, pricingHref } from '$lib/links';
	import Logo from './Logo.svelte';

	const LINK = 'transition-[color] duration-150 hover:text-ink';
</script>

<!--
  The one footer (DECISIONS I-655), on every page, on the shared frame:
  the mark and the word, then the product's links and the policies. From
  768px up the two groups are one row; below it they are two columns, so
  the lines align instead of wrapping where the width falls.
-->
<footer class="border-t border-rule">
	<div
		class="frame flex flex-col gap-6 py-7 text-sm text-ink-muted md:flex-row md:items-center md:justify-between"
	>
		<span class="text-ink"><Logo size="sm" mark /></span>
		<nav aria-label="Footer" class="grid grid-cols-2 gap-x-6 md:flex md:gap-x-10">
			<!-- eslint-disable svelte/no-navigation-without-resolve -- pricingHref() builds it with resolve(); the other two are off-site -->
			<ul class="flex flex-col gap-2 md:flex-row md:gap-6">
				<li><a href={resolve('/docs')} class={LINK}>Docs</a></li>
				<li><a href={pricingHref()} class={LINK}>Pricing</a></li>
				<li><a href={SOURCE_URL} class={LINK}>GitHub</a></li>
				<li><a href={FEEDBACK_URL} class={LINK}>Feedback</a></li>
			</ul>
			<!-- eslint-enable svelte/no-navigation-without-resolve -->
			<ul class="flex flex-col gap-2 md:flex-row md:gap-6">
				<li><a href={resolve('/terms')} class={LINK}>Terms</a></li>
				<li><a href={resolve('/privacy')} class={LINK}>Privacy</a></li>
				<li><a href={resolve('/refunds')} class={LINK}>Refunds</a></li>
			</ul>
		</nav>
	</div>
</footer>
