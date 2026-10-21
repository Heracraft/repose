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
	import CopyButton from './CopyButton.svelte';
	import { tabStopWhenScrolls } from '$lib/scroller';

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
				<div class="line" use:tabStopWhenScrolls>
					{#if c.note}<span class="note"># {c.note}</span>{/if}
					<code><span class="prompt" aria-hidden="true">$&nbsp;</span>{c.command}</code>
				</div>
				<CopyButton text={c.command} label="Copy" ariaLabel="Copy {c.what}" tone="dark" size="sm" />
			</li>
		{/each}
	</ul>
</div>

<style>
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
	@media (pointer: coarse), (max-width: 639.98px) {
		.row.has-note :global(.hk-copy) {
			margin-bottom: -0.25rem;
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
