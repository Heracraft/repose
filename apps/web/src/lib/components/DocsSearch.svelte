<script lang="ts">
	/* The docs search palette (Guide B 12.14): a modal over a prebuilt
	   index of pages, headings, CLI flags and config keys (lib/docs.ts,
	   SEARCH), matched in the browser, so there is no search service.
	   The docs layout owns the triggers and the shortcuts and calls show();
	   arrows move, Enter goes, Escape closes and gives focus back to the
	   trigger that opened it. */
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { tick } from 'svelte';
	import { searchDocs, type SearchResult } from '$lib/docs';

	const uid = $props.id();
	let dialog: HTMLDialogElement | undefined = $state();
	let input: HTMLInputElement | undefined = $state();
	let list: HTMLUListElement | undefined = $state();
	let query = $state('');
	let active = $state(0);
	let returnTo: HTMLElement | null = null;
	let leaving = false;

	let results = $derived(searchDocs(query));
	let typed = $derived(query.trim());

	$effect(() => {
		void query;
		active = 0;
		if (list) list.scrollTop = 0;
	});

	/** Opens the palette, empty; focus goes back to `from` when it closes without a choice. */
	export async function show(from: HTMLElement | null) {
		if (!dialog || dialog.open) return;
		returnTo = from;
		query = '';
		dialog.showModal();
		await tick();
		input?.focus();
	}

	function close() {
		dialog?.close();
	}

	function onclose() {
		if (!leaving) returnTo?.focus({ preventScroll: true });
		leaving = false;
	}

	function href(r: SearchResult): string {
		const { slug, id } = r.entry;
		const path = slug === 'index' ? resolve('/docs') : resolve('/docs/[slug]', { slug });
		return id ? `${path}#${id}` : path;
	}

	/** Goes to the entry's page and heading, which takes focus as a followed #link's would. */
	async function go(r: SearchResult | undefined) {
		if (!r) return;
		leaving = true;
		close();
		const to = href(r);
		const same = page.url.pathname + page.url.hash === to;
		// eslint-disable-next-line svelte/no-navigation-without-resolve -- href() builds the path with resolve()
		if (!same) await goto(to);
		await tick();
		const { id } = r.entry;
		const target = id
			? document.getElementById(id)
			: document.querySelector<HTMLElement>('#main h1');
		if (!target) return;
		if (id) target.scrollIntoView({ block: 'start' });
		else if (same) window.scrollTo(0, 0);
		target.tabIndex = -1;
		target.focus({ preventScroll: true });
	}

	async function onkeydown(ev: KeyboardEvent) {
		if (ev.key === 'ArrowDown' || ev.key === 'ArrowUp') {
			ev.preventDefault();
			const n = results.length;
			if (!n) return;
			active = (active + (ev.key === 'ArrowDown' ? 1 : n - 1)) % n;
			await tick();
			document.getElementById(`${uid}-o${active}`)?.scrollIntoView({ block: 'nearest' });
		} else if (ev.key === 'Enter') {
			ev.preventDefault();
			void go(results[active]);
		}
	}
</script>

<!-- The backdrop is a pointer target only; Escape closes the dialog from the keyboard. -->
<dialog
	bind:this={dialog}
	class="pal"
	aria-labelledby="{uid}-title"
	{onclose}
	onclick={(e) => {
		if (e.target === dialog) close();
	}}
>
	<h2 id="{uid}-title" class="sr-only">Search docs</h2>
	<div class="pal-in">
		<div class="pal-q">
			<svg
				viewBox="0 0 16 16"
				fill="none"
				stroke="currentColor"
				stroke-width="1.5"
				stroke-linecap="round"
				aria-hidden="true"><circle cx="7" cy="7" r="4.25" /><path d="M10.25 10.25L13.5 13.5" /></svg
			>
			<input
				bind:this={input}
				bind:value={query}
				{onkeydown}
				type="text"
				placeholder="Pages, commands, settings"
				autocomplete="off"
				autocapitalize="off"
				spellcheck="false"
				enterkeyhint="go"
				role="combobox"
				aria-expanded={results.length > 0}
				aria-controls="{uid}-list"
				aria-autocomplete="list"
				aria-activedescendant={results.length ? `${uid}-o${active}` : undefined}
				aria-label="Search docs"
			/>
			<button type="button" class="pal-esc" onclick={close}
				><span aria-hidden="true">Esc</span><span class="sr-only">Close search</span></button
			>
		</div>
		<ul
			bind:this={list}
			id="{uid}-list"
			class="pal-list"
			role="listbox"
			aria-label={typed ? 'Results' : 'Pages'}
			hidden={!results.length}
		>
			{#each results as r, i (r.entry.slug + (r.entry.id ?? '') + r.entry.kind + r.entry.label)}
				<!-- The input owns the keys (combobox); a row answers the pointer. -->
				<!-- svelte-ignore a11y_click_events_have_key_events -->
				<li
					id="{uid}-o{i}"
					role="option"
					aria-selected={i === active}
					class:on={i === active}
					onmousemove={() => (active = i)}
					onclick={() => go(r)}
				>
					<span class="pal-line">
						<span class="pal-label" class:mono={r.entry.mono}
							>{#each r.label as part, j (j)}{#if part.hit}<mark>{part.text}</mark
									>{:else}{part.text}{/if}{/each}</span
						>
						<span class="pal-ctx"
							>{#each r.context as part, j (j)}{#if part.hit}<mark>{part.text}</mark
									>{:else}{part.text}{/if}{/each}</span
						>
					</span>
					{#if r.snippet}
						<span class="pal-snip"
							>{#each r.snippet as part, j (j)}{#if part.hit}<mark>{part.text}</mark
									>{:else}{part.text}{/if}{/each}</span
						>
					{/if}
				</li>
			{/each}
		</ul>
		{#if !results.length}
			<p class="pal-none">No match for “{typed}”.</p>
		{/if}
		<p class="sr-only" role="status">
			{#if typed}{results.length
					? `${results.length} ${results.length === 1 ? 'result' : 'results'}`
					: ''}{/if}
		</p>
	</div>
</dialog>

<style>
	.pal {
		padding: 0;
		border: 0;
		margin: 12dvh auto auto;
		width: calc(100% - 2rem);
		max-width: 36rem;
		border-radius: var(--hk-radius-xl);
		background: var(--hk-surface);
		color: var(--hk-text);
		box-shadow: var(--hk-shadow-modal);
		overflow: hidden;
	}
	.pal::backdrop {
		background: var(--hk-scrim);
		backdrop-filter: blur(2px);
		-webkit-backdrop-filter: blur(2px);
	}
	.pal[open] {
		animation: pal-in var(--hk-dur-fast) var(--hk-ease);
	}
	.pal[open]::backdrop {
		animation: pal-fade var(--hk-dur-fast) var(--hk-ease);
	}
	@keyframes pal-in {
		from {
			opacity: 0;
			transform: translateY(-4px) scale(0.985);
		}
	}
	@keyframes pal-fade {
		from {
			opacity: 0;
		}
	}
	.pal-in {
		display: flex;
		flex-direction: column;
		max-height: min(32rem, 76dvh);
	}
	.pal-q {
		display: flex;
		flex: none;
		align-items: center;
		gap: 0.625rem;
		padding: 0 0.75rem 0 1rem;
		border-bottom: 1px solid var(--hk-hairline);
	}
	.pal-q svg {
		width: 1rem;
		height: 1rem;
		flex: none;
		color: var(--hk-text-3);
	}
	.pal-q input {
		flex: 1;
		min-width: 0;
		height: 3rem;
		border: 0;
		background: none;
		color: var(--hk-text);
		font: inherit;
		font-size: 1rem;
		outline: none;
	}
	.pal-q input::placeholder {
		color: var(--hk-text-3);
	}
	.pal-esc {
		position: relative;
		flex: none;
		padding: 0.125rem 0.375rem;
		border: 1px solid var(--hk-border);
		border-radius: 5px;
		background: none;
		color: var(--hk-text-3);
		font: inherit;
		font-size: 0.6875rem;
		line-height: 1rem;
		cursor: pointer;
	}
	.pal-esc:hover {
		color: var(--hk-text);
		border-color: var(--hk-border-strong);
	}
	.pal-esc:focus-visible {
		outline: 2px solid var(--hk-ring, var(--hk-accent));
		outline-offset: 2px;
	}

	.pal-list {
		margin: 0;
		padding: 0.375rem;
		list-style: none;
		overflow-y: auto;
		overscroll-behavior: contain;
		scroll-padding-block: 0.375rem;
	}
	.pal-list[hidden] {
		display: none;
	}
	.pal-list li {
		display: flex;
		flex-direction: column;
		justify-content: center;
		gap: 0.125rem;
		padding: 0.5rem 0.625rem;
		border-radius: var(--hk-radius-7);
		font-size: 0.875rem;
		line-height: 1.25rem;
		cursor: pointer;
	}
	.pal-list li.on {
		background: var(--hk-accent-wash);
	}
	/* One fixed line, so a row in the code face is as tall as one in Inter. */
	.pal-line {
		display: flex;
		height: 1.25rem;
		align-items: baseline;
		justify-content: space-between;
		gap: 1rem;
		min-width: 0;
	}
	.pal-label {
		min-width: 0;
		line-height: 1.25rem;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		color: var(--hk-text);
	}
	.on .pal-label {
		color: var(--hk-accent);
	}
	.pal-label.mono {
		font-family: var(--hk-font-mono);
		font-size: 0.8125rem;
	}
	.pal-ctx {
		flex: none;
		max-width: 45%;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		font-size: 0.75rem;
		color: var(--hk-text-3);
	}
	.pal-snip {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		font-size: 0.75rem;
		line-height: 1.125rem;
		color: var(--hk-text-3);
	}
	/* The query's words: the label's colour on a wash, so the active row's
	   accent title still shows them. */
	.pal mark {
		padding: 0;
		border-radius: 3px;
		background: color-mix(in srgb, var(--hk-accent) 16%, transparent);
		color: inherit;
		box-decoration-break: clone;
	}
	.pal-snip mark,
	.pal-ctx mark {
		color: var(--hk-text-2);
	}
	.pal-none {
		margin: 0;
		padding: 0.875rem 1rem;
		font-size: 0.875rem;
		color: var(--hk-text-3);
	}

	/* A phone: near the full width, from the top, with thumb-sized rows;
	   the label keeps more of the line than the context. */
	@media (max-width: 767.98px) {
		.pal {
			margin: 0.75rem auto auto;
			width: calc(100% - 1.5rem);
		}
		.pal-ctx {
			max-width: 36%;
		}
		.pal-in {
			max-height: min(36rem, calc(100dvh - 1.5rem));
		}
	}
	@media (pointer: coarse) {
		.pal-list li {
			min-height: 2.75rem;
		}
		/* The chip stays small; its target is 44px. */
		.pal-esc::after {
			content: '';
			position: absolute;
			inset: -0.875rem -0.5rem;
		}
	}
	@media (prefers-reduced-motion: reduce) {
		.pal[open],
		.pal[open]::backdrop {
			animation: none;
		}
	}
	/* Forced colours drop the shadow and the washes: the dialog takes an
	   edge, the active row an outline, a match the system's mark. */
	@media (forced-colors: active) {
		.pal {
			border: 1px solid CanvasText;
		}
		.pal-list li.on {
			outline: 2px solid Highlight;
			outline-offset: -2px;
		}
		.pal mark {
			background: Mark;
			color: MarkText;
		}
	}
</style>
