<script lang="ts" module>
	export interface Command {
		/** The command, with no "$ " prompt. */
		command: string;
		/** What it is, for its button's name: "Copy {what}". */
		what: string;
		/** A "# note" line above it, without the "# ". */
		note?: string;
	}
</script>

<script lang="ts">
	/* Two to four commands a page offers together (Connect, the empty
	   projects list): one near-black pane, one command per 40px row, each
	   with its own Copy that copies that row alone. Rows scroll sideways
	   inside the pane and never wrap. No bar unless `title`. */
	import { MediaQuery } from 'svelte/reactivity';
	import CopyButton from './CopyButton.svelte';
	import { tabStopWhenScrolls } from '$lib/scroller';

	// Below 400px Copy drops its word (the name stays "Copy {what}" and the
	// box 44px): at 320 the word took 70px of a 280px row and cut the
	// command short (review r3 sys3-2).
	const narrow = new MediaQuery('max-width: 399.98px');

	/** A line scrolled to its end shows its last characters unfaded. */
	function markEnd(e: Event) {
		const el = e.currentTarget as HTMLElement;
		el.dataset.end = String(el.scrollLeft + el.clientWidth >= el.scrollWidth - 1);
	}

	let {
		commands,
		title,
		label,
		class: klass = ''
	}: {
		commands: Command[];
		title?: string;
		/** Accessible name of the list (the nearest heading) when no title. */
		label?: string;
		class?: string;
	} = $props();
</script>

<div class="hk-code cl {klass}">
	{#if title}
		<div class="hk-code-bar"><span class="title">{title}</span></div>
	{/if}
	<ul class="rows" aria-label={title ?? label ?? 'Commands'}>
		{#each commands as c (c.command)}
			<li class="row" class:has-note={!!c.note}>
				<!-- A tab stop only while the line is wider than the pane. -->
				<div class="line" use:tabStopWhenScrolls onscroll={markEnd}>
					{#if c.note}<span class="note"># {c.note}</span>{/if}
					<code><span class="prompt" aria-hidden="true">$&nbsp;</span>{c.command}</code>
				</div>
				<CopyButton
					text={c.command}
					label={narrow.current ? undefined : 'Copy'}
					ariaLabel="Copy {c.what}"
					tone="dark"
					size="sm"
				/>
			</li>
		{/each}
	</ul>
</div>

<style>
	/* The pane is as wide as its longest row, from 22rem to 40rem, so each
	   Copy sits beside its command and not across the page from it. On a
	   phone it takes the column; a longer command scrolls inside it. */
	.hk-code.cl {
		width: fit-content;
		min-width: min(100%, 22rem);
		max-width: min(100%, 40rem);
	}
	.title {
		color: var(--hk-code-key);
		font-weight: 500;
	}
	.rows {
		margin: 0;
		padding: 0.375rem 0;
		list-style: none;
	}
	.row {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		min-height: 2.5rem;
		padding: 0 0.5rem 0 var(--cb-pad);
	}
	.row + .row {
		border-top: 1px solid var(--hk-code-border);
	}
	.line {
		display: flex;
		flex: 1;
		flex-direction: column;
		justify-content: center;
		min-width: 0;
		padding: 0.5rem 0;
		overflow-x: auto;
		scrollbar-width: thin;
		scrollbar-color: var(--hk-code-scrollbar) transparent;
		font-family: var(--hk-font-mono);
		font-size: 0.8125rem;
		line-height: 1.25rem;
		white-space: pre;
	}
	/* A line cut at the pane's edge fades over its last 1.5rem, so the cut
	   reads as more text; scrolled to its end, it shows plain. */
	.line:global([data-scrolls='true']:not([data-end='true'])) {
		mask-image: linear-gradient(to right, #000 calc(100% - 1.5rem), transparent);
	}
	.line:focus-visible {
		outline: none;
		box-shadow: inset 0 0 0 2px var(--hk-code-focus);
		border-radius: var(--hk-radius-xs);
	}
	/* With a note above it, Copy sits level with the command line: its
	   centre 18px over the row's foot (0.5rem padding plus half the 20px
	   line), 44px tall on touch. */
	.row.has-note {
		align-items: flex-end;
	}
	.row.has-note :global(.hk-copy) {
		margin-bottom: 0.375rem;
	}
	/* On touch each row is as tall as its 44px Copy, so one Copy's box
	   never crosses the hairline into the next row; under a note the
	   line gains 4px at its foot so Copy stays level with it. */
	@media (pointer: coarse), (max-width: 639.98px) {
		.row {
			min-height: 2.75rem;
		}
		.row.has-note .line {
			padding-bottom: 0.75rem;
		}
		.row.has-note :global(.hk-copy) {
			margin-bottom: 0;
		}
	}
	/* The note wraps on a phone; the command scrolls. */
	.note {
		white-space: normal;
		font-size: 0.75rem;
		font-style: italic;
		color: var(--hk-code-comment);
	}
	code {
		margin: 0;
		padding: 0;
		border: 0;
		background: none;
		font: inherit;
		color: var(--hk-code-text);
	}
	.prompt {
		color: var(--hk-code-muted);
		user-select: none;
	}
</style>
