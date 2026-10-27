<script lang="ts" module>
	export type CodeLang = 'shell' | 'text';

	export interface CodeTab {
		label: string;
		code: string;
		lang?: CodeLang;
	}

	export interface CodeLine {
		kind: 'command' | 'output' | 'comment' | 'plain';
		text: string;
	}

	/** Splits a shell session: "$ cmd" lines are commands, "# note" lines
	 *  comments, the rest output. Plain text is one plain run per line. */
	export function shellLines(code: string, lang: CodeLang): CodeLine[] {
		return code
			.replace(/\n+$/, '')
			.split('\n')
			.map((line) => {
				if (lang !== 'shell') return { kind: 'plain', text: line };
				if (line.startsWith('$ ')) return { kind: 'command', text: line.slice(2) };
				if (line.startsWith('# ')) return { kind: 'comment', text: line };
				return { kind: 'output', text: line };
			});
	}

	/** What Copy takes: the commands alone for a shell session (as the docs'
	 *  Copy does), the whole text otherwise. */
	export function copyText(code: string, lang: CodeLang): string {
		const lines = shellLines(code, lang);
		if (lang !== 'shell' || !lines.some((l) => l.kind === 'command'))
			return code.replace(/\n+$/, '');
		return lines
			.filter((l) => l.kind === 'command')
			.map((l) => l.text)
			.join('\n');
	}
</script>

<script lang="ts">
	/* The one code pane (Guide B 12.7): near-black in both themes, a 42px bar
	   with a title or tabs, mono 13px/20px, a dark Copy at the bar's end.
	   No syntax colours beyond a shell session's: the "$ " prompt and output
	   muted, the command in full code text, "# " comments italic. Line
	   numbers only past eight lines. The body takes a tab stop so a keyboard
	   can scroll a line wider than the pane (WCAG 2.1.1). */
	import type { Snippet } from 'svelte';
	import CopyButton from './CopyButton.svelte';

	interface Props {
		tabs?: CodeTab[];
		code?: string;
		lang?: CodeLang;
		/** The bar's label. No title and no tabs: the bar holds Copy alone. */
		title?: string;
		/** What the copy button copies, when it differs from the pane. */
		copy?: string;
		/** The copy button's accessible name. */
		copyLabel?: string;
		lineNumbers?: boolean | 'auto';
		/** Fold long lines at the pane edge instead of scrolling sideways. */
		wrap?: boolean;
		/** The visible tab. Bindable. */
		selected?: number;
		/** CSS max-height for the scrolling body, e.g. "22rem". */
		maxHeight?: string;
		/** Accessible name of the scrolling body (the nearest heading). */
		label?: string;
		/** Bar controls left of Copy. */
		actions?: Snippet;
		class?: string;
	}

	let {
		tabs,
		code = '',
		lang = 'shell',
		title,
		copy,
		copyLabel = 'Copy code',
		lineNumbers = 'auto',
		wrap = false,
		selected = $bindable(0),
		maxHeight,
		label,
		actions,
		class: klass = ''
	}: Props = $props();

	const uid = $props.id();
	const NL = '\n';
	const list = $derived<CodeTab[]>(tabs?.length ? tabs : [{ label: title ?? '', code, lang }]);
	const current = $derived(list[Math.min(selected, list.length - 1)] ?? list[0]);
	const currentLang = $derived<CodeLang>(current.lang ?? lang);
	const lines = $derived(shellLines(current.code, currentLang));
	const showLines = $derived(lineNumbers === 'auto' ? lines.length > 8 : lineNumbers);
	const hasTabs = $derived((tabs?.length ?? 0) > 1);

	let tabEls: HTMLButtonElement[] = $state([]);
	let tabsEl: HTMLDivElement | undefined = $state();
	// The tab strip scrolls sideways on a narrow pane with its scrollbar
	// hidden; a fade on the cut edge, measured, says there is more.
	let tabsCut = $state(false);
	function measureTabs() {
		if (!tabsEl) return;
		tabsCut = tabsEl.scrollLeft + tabsEl.clientWidth < tabsEl.scrollWidth - 1;
	}
	$effect(() => {
		if (!tabsEl) return;
		measureTabs();
		const ro = new ResizeObserver(measureTabs);
		ro.observe(tabsEl);
		return () => ro.disconnect();
	});
	function onTabKey(e: KeyboardEvent, i: number) {
		const n = list.length;
		let next = -1;
		if (e.key === 'ArrowRight') next = (i + 1) % n;
		else if (e.key === 'ArrowLeft') next = (i - 1 + n) % n;
		else if (e.key === 'Home') next = 0;
		else if (e.key === 'End') next = n - 1;
		if (next < 0) return;
		e.preventDefault();
		selected = next;
		tabEls[next]?.focus();
	}
</script>

<div class="hk-code cb {klass}" class:hk-code--wrap={wrap}>
	<div class="hk-code-bar">
		{#if hasTabs}
			<div
				class="tabs"
				class:tabs--cut={tabsCut}
				bind:this={tabsEl}
				onscroll={measureTabs}
				role="tablist"
				aria-label={title ?? 'Variants'}
			>
				{#each list as tab, i (tab.label)}
					<button
						bind:this={tabEls[i]}
						type="button"
						role="tab"
						id="{uid}-tab-{i}"
						aria-selected={i === selected}
						aria-controls="{uid}-panel"
						tabindex={i === selected ? 0 : -1}
						class="tab"
						onclick={() => (selected = i)}
						onkeydown={(e) => onTabKey(e, i)}>{tab.label}</button
					>
				{/each}
			</div>
		{:else}
			<span class="title">{title ?? ''}</span>
		{/if}
		<div class="right">
			{@render actions?.()}
			<CopyButton
				text={() => copy ?? copyText(current.code, currentLang)}
				tone="dark"
				size="sm"
				ariaLabel={copyLabel}
			/>
		</div>
	</div>
	<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
	<div
		class="hk-code-body body"
		id="{uid}-panel"
		role={hasTabs ? 'tabpanel' : 'region'}
		aria-labelledby={hasTabs ? `${uid}-tab-${selected}` : undefined}
		aria-label={hasTabs ? undefined : (label ?? title ?? 'Code')}
		style:max-height={maxHeight}
		tabindex="0"
	>
		{#if showLines}
			<div class="gutter" aria-hidden="true">
				{#each lines.keys() as n (n)}<span>{n + 1}</span>{/each}
			</div>
		{/if}
		<pre><code
				>{#each lines as line, n (n)}{#if n > 0}{NL}{/if}{#if line.kind === 'command'}<span
							class="prompt">$&nbsp;</span
						><span class="cmd">{line.text}</span>{:else}<span class={line.kind}>{line.text}</span
						>{/if}{/each}</code
			></pre>
	</div>
</div>

<style>
	.cb {
		position: relative;
	}
	.cb :global(.hk-code-bar) {
		height: 2.625rem;
	}
	.tabs {
		display: flex;
		align-self: stretch;
		gap: 1.125rem;
		min-width: 0;
		overflow-x: auto;
		scrollbar-width: none;
	}
	.tabs--cut {
		mask-image: linear-gradient(to right, #000 calc(100% - 2rem), transparent);
	}
	.tabs::-webkit-scrollbar {
		display: none;
	}
	.tab {
		position: relative;
		flex: none;
		background: none;
		border: 0;
		padding: 0;
		font: inherit;
		color: var(--hk-code-muted);
		cursor: pointer;
		transition: color var(--hk-dur-fast) var(--hk-ease);
	}
	.tab:hover {
		color: var(--hk-code-key);
	}
	.tab[aria-selected='true'] {
		color: var(--hk-code-text);
	}
	.tab::after {
		content: '';
		position: absolute;
		left: 0;
		right: 0;
		bottom: -1px;
		height: 1px;
		background: var(--hk-code-text);
		transform: scaleX(0);
		transition: transform var(--hk-dur) var(--hk-ease);
	}
	.tab[aria-selected='true']::after {
		transform: scaleX(1);
	}
	.tab:focus-visible {
		outline: none;
		color: var(--hk-code-text);
		box-shadow: inset 0 -2px 0 var(--hk-code-string);
	}
	.title {
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		color: var(--hk-code-key);
		font-weight: 500;
	}
	.right {
		display: flex;
		align-items: center;
		gap: 0.25rem;
		flex: none;
	}
	.body {
		display: flex;
		padding-inline: 0;
	}
	.gutter {
		position: sticky;
		left: 0;
		display: flex;
		flex-direction: column;
		flex: none;
		padding: 0 0.875rem 0 var(--cb-pad);
		text-align: right;
		color: var(--hk-code-dim);
		background: var(--hk-code-bg);
		user-select: none;
		font-variant-numeric: tabular-nums;
	}
	pre {
		flex: 1;
		min-width: 0;
		padding: 0 var(--cb-pad) !important;
	}
	.gutter + pre {
		padding-left: 0 !important;
	}
	.prompt,
	.output {
		color: var(--hk-code-muted);
	}
	.prompt {
		user-select: none;
	}
	.cmd {
		color: var(--hk-code-text);
	}
	.comment {
		color: var(--hk-code-comment);
		font-style: italic;
	}
</style>
