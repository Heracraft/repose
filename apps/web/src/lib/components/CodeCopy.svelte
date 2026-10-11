<script lang="ts">
	import { toast } from 'svelte-sonner';
	import { tabStopWhenScrolls } from '$lib/scroller';

	// A command on a dashboard page with its own Copy button (I-659): the
	// install line, `repose attach todo-app`, a secret's `repose secrets
	// set` line. One line of .codeblock with the button inside its top
	// right corner. The button's face says "Copied" for 1.5s and its own
	// live region says it to a screen reader, which does not announce a
	// change to the text of the button it is on.
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

<!-- The line keeps its sideways scroll, and its right padding clears the
     button, so the text never runs under it. -->
<div class="relative">
	<pre class="codeblock pr-20" use:tabStopWhenScrolls>{command}</pre>
	<button
		type="button"
		class="btn-quiet absolute top-1.5 right-1.5 h-7 px-2 py-0 text-xs font-normal"
		aria-label="Copy {what}"
		{onclick}>{copied ? 'Copied' : 'Copy'}</button
	>
	<span class="sr-only" role="status">{copied ? 'Copied to the clipboard.' : ''}</span>
</div>
