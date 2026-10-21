<script lang="ts">
	import CopyButton from '$lib/hk/CopyButton.svelte';
	import { tabStopWhenScrolls } from '$lib/scroller';

	// One lone command on a light page with its own Copy (Guide B 12.18):
	// `repose run`, the install line, `ssh todo-app.repose`. A light box,
	// mono 13px, the command scrolling sideways inside it and never
	// wrapping, the button at its end outside the scroll. Several commands
	// together go in CommandList; near-black is for code panes, so a page
	// keeps one dark anchor.
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
			>{/if}{command}</pre>
	<CopyButton text={copy ?? command} label="Copy" ariaLabel="Copy {what}" tone="page" size="sm" />
</div>

<style>
	.code-copy {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		min-width: 0;
		padding: 0.5rem 0.5rem 0.5rem 0.75rem;
		border: 1px solid var(--hk-border);
		border-radius: var(--hk-radius);
		background: var(--hk-surface-2);
	}
	/* On touch the sm Copy is 44px: the box grows to hold it, border to
	   border, so its edge never draws over the box's. */
	@media (pointer: coarse), (max-width: 639.98px) {
		.code-copy {
			min-height: 2.875rem;
			padding-block: 0;
		}
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
		mask-image: linear-gradient(to right, #000 calc(100% - 1.5rem), transparent);
	}
	.line:focus-visible {
		outline-offset: 2px;
	}
	.prompt {
		color: var(--hk-text-3);
		user-select: none;
	}
</style>
