<script lang="ts">
	import { toast } from 'svelte-sonner';
	import { tabStopWhenScrolls } from '$lib/scroller';

	// A command on a dashboard page with its own Copy button (I-659): the
	// install line, `repose code todo-app`, `repose run`. One line of
	// .codeblock in a row with the button: the line scrolls sideways in
	// its own box and the button sits beside it, outside the scroll, so
	// no text runs under it at any width. The button's face says "Copied"
	// for 1.5s and its own live region says it to a screen reader, which
	// does not announce a change to the text of the button it is on.
	let {
		command,
		copy,
		what
	}: {
		/** The line shown, with no "$ " prompt. */
		command: string;
		/** What goes on the clipboard, when it differs from the line shown. */
		copy?: string;
		/** What the line is, for the button's name: "Copy {what}". */
		what: string;
	} = $props();

	let copied = $state(false);
	let timer: ReturnType<typeof setTimeout> | undefined;

	async function onclick() {
		try {
			await navigator.clipboard.writeText(copy ?? command);
		} catch {
			toast.error('Could not copy. Select the command instead.');
			return;
		}
		copied = true;
		clearTimeout(timer);
		timer = setTimeout(() => (copied = false), 1500);
	}

	$effect(() => () => clearTimeout(timer));
</script>

<div class="codeblock flex items-center gap-2 p-0">
	<pre
		class="codeblock min-w-0 flex-1 rounded-none border-0 pr-1"
		use:tabStopWhenScrolls>{command}</pre>
	<button
		type="button"
		class="btn-quiet mr-1.5 h-7 shrink-0 px-2 py-0 text-xs font-normal"
		aria-label="Copy {what}"
		{onclick}>{copied ? 'Copied' : 'Copy'}</button
	>
	<span class="sr-only" role="status">{copied ? 'Copied to the clipboard.' : ''}</span>
</div>
