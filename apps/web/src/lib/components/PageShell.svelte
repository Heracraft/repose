<script lang="ts">
	import type { Snippet } from 'svelte';

	interface Crumb {
		label: string;
		href?: string;
	}

	let {
		title,
		crumbs = [],
		width = 'full',
		lede,
		action,
		tabs,
		children
	}: {
		title: string;
		crumbs?: Crumb[];
		/** 'full' spans the frame; 'form' holds the content to max-w-2xl, flush left. 'list' is 'full', kept so older callers compile. */
		width?: 'full' | 'form' | 'list';
		lede?: string;
		action?: Snippet;
		/** Links under the title, on its rule (ProjectTabs): the current one's edge lies on the rule. */
		tabs?: Snippet;
		children: Snippet;
	} = $props();
</script>

<!-- A <main> landmark rather than a <div>: it was the one audit every
     signed-in page failed (Lighthouse landmark-one-main, 98/100), and it is
     what a screen reader's "skip to main content" jumps to. The main is the
     shared .frame, the header's column, so the title starts under the logo
     on every page (I-656); a form page narrows only its content to
     max-w-2xl, flush left. -->
<main id="main" class="frame pt-10 pb-24">
	<div class={width === 'form' ? 'max-w-2xl' : undefined}>
		{#if crumbs.length}
			<!-- The separator is a middle dot, as DESIGN-LANGUAGE.md "Page frame" says.
			     A flex row with a gap spaces it: with margins, the template's own
			     whitespace added a space on one side only, 4px before the dot and
			     10px after it. -->
			<p class="mb-3 flex flex-wrap items-baseline gap-x-2 text-sm text-ink-muted">
				{#each crumbs as crumb, i (crumb.label)}
					{#if i > 0}<span aria-hidden="true">·</span>{/if}
					{#if crumb.href}
						<!-- eslint-disable svelte/no-navigation-without-resolve -- callers build crumb.href with $app/paths' resolve() -->
						<a href={crumb.href} class="hover:text-ink hover:underline">{crumb.label}</a>
						<!-- eslint-enable svelte/no-navigation-without-resolve -->
					{:else}
						{crumb.label}
					{/if}
				{/each}
			</p>
		{/if}
		<!-- The title is the page title's step everywhere, 36px serif at
		     -0.01em (I-660). The action sits under it on a phone and level
		     with its last line from sm up. -->
		<div class="border-b border-rule {tabs ? '' : 'pb-5'}">
			<div class="flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
				<div class="min-w-0">
					<h1 class="text-4xl font-semibold tracking-[-0.01em]">{title}</h1>
					{#if lede}<p class="mt-2 text-sm text-ink-muted">{lede}</p>{/if}
				</div>
				{#if action}<div class="flex flex-wrap items-center gap-2">{@render action()}</div>{/if}
			</div>
			{#if tabs}{@render tabs()}{/if}
		</div>
		<div class="mt-8">
			{@render children()}
		</div>
	</div>
</main>
