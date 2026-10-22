<script lang="ts">
	import { MediaQuery } from 'svelte/reactivity';
	import CopyButton from '$lib/hk/CopyButton.svelte';
	import { tabStopWhenScrolls } from '$lib/scroller';

	// One lone command on a light page with its own Copy (Guide B 12.18):
	// `repose run`, the install line, `ssh todo-app.repose`. A light box
	// in CommandList's shape (12px corners, a ghost Copy), mono 13px, the
	// command scrolling sideways inside it, the button at its end outside
	// the scroll. On a phone the command wraps, so all of it shows, and
	// Copy is its icon. Several commands together go in CommandList;
	// near-black is for code panes, so a page keeps one dark anchor.
	const phone = new MediaQuery('max-width: 639.98px');
	let {
		command,
		copy,
		what,
		prompt = false
	}: {
		/** The line shown, with no "$ " prompt. */
		command: string;
		/** What goes on the clipboard, when it differs from the line shown. */
		copy?: string;
		/** What the line is, for the button's name: "Copy {what}". */
		what: string;
		/** Show a muted "$ " before the command. */
		prompt?: boolean;
	} = $props();

	/** A line scrolled to its end shows its last characters unfaded. */
	function markEnd(e: Event) {
		const el = e.currentTarget as HTMLElement;
		el.dataset.end = String(el.scrollLeft + el.clientWidth >= el.scrollWidth - 1);
	}
</script>

<div class="code-copy">
	<pre class="line" use:tabStopWhenScrolls onscroll={markEnd}>{#if prompt}<span
				class="prompt"
				aria-hidden="true">$&nbsp;</span
			>{/if}{#each command.split(/(?<=\/)/) as part, i (i)}{#if i}<wbr />{/if}{part}{/each}</pre>
	<CopyButton
		text={copy ?? command}
		label={phone.current ? undefined : 'Copy'}
		ariaLabel="Copy {what}"
		tone="page"
		size="sm"
	/>
</div>

<style>
	/* As wide as its command, from 20rem to 40rem, so Copy sits beside the
	   command (as CommandList does); a phone's column caps it first. */
	.code-copy {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		width: fit-content;
		min-width: min(100%, 20rem);
		max-width: min(100%, 40rem);
		padding: 0.5rem 0.5rem 0.5rem 0.75rem;
		border: 1px solid var(--hk-border);
		border-radius: var(--hk-radius);
		background: var(--hk-surface-2);
	}
	/* Copy is a ghost, as on a code pane: the box is the frame. */
	.code-copy :global(.hk-copy.hk-copy--labelled:not(.is-copied)) {
		border-color: transparent;
		background: transparent;
		color: var(--hk-text-2);
	}
	.code-copy :global(.hk-copy.hk-copy--labelled:not(.is-copied):hover) {
		border-color: transparent;
		background: var(--hk-hover);
		color: var(--hk-text);
	}
	.line {
		flex: 1;
		min-width: 0;
		margin: 0;
		overflow-x: auto;
		scrollbar-width: thin;
		font-family: var(--hk-font-mono);
		font-size: 0.8125rem;
		line-height: 1.25rem;
		color: var(--hk-text);
		white-space: pre;
	}
	/* A line that scrolls fades over its last 1.5rem, as CommandList's
	   do, so a cut command reads as more text (review r4). */
	.line:global([data-scrolls='true']:not([data-end='true'])) {
		mask-image: linear-gradient(to right, black calc(100% - 1.5rem), transparent);
	}
	.line:focus-visible {
		outline-offset: 2px;
	}
	/* On a phone the command wraps, after a slash where it can (the <wbr>s),
	   anywhere where it cannot, so its end shows. */
	@media (max-width: 639.98px) {
		.line {
			overflow-x: visible;
			white-space: pre-wrap;
			overflow-wrap: anywhere;
		}
	}
	.prompt {
		color: var(--hk-text-3);
		user-select: none;
	}
</style>
