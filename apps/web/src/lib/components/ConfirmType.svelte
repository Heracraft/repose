<script lang="ts">
	import { focusOnMount } from '$lib/focus';

	// A destructive action's confirm control: the button stays disabled until
	// the exact word is typed, per 08-dashboard.md 5.2 ("Destroy with confirm
	// typing the slug") and 6 ("Destroy typed wrong -> button disabled until
	// the slug matches exactly"). It is the one pattern for an action that
	// cannot be undone: destroying a project, deleting the account, and
	// restoring a snapshot over a project's disk all use it.
	let {
		word,
		label,
		onconfirm,
		disabled = false,
		busy = false,
		busyLabel,
		oncancel,
		autofocus = false
	}: {
		word: string;
		label: string;
		onconfirm: () => void;
		disabled?: boolean;
		/** The action is under way: the button says so and stays disabled. */
		busy?: boolean;
		busyLabel?: string;
		/** When given, a Cancel beside the button closes the panel around it. */
		oncancel?: () => void;
		/**
		 * Focus the field when the control appears: set by a panel that opens
		 * in place of the button that asked for it, so focus follows the
		 * click instead of falling to the page.
		 */
		autofocus?: boolean;
	} = $props();

	const fieldId = $props.id();
	let typed = $state('');
	let ready = $derived(!disabled && !busy && typed === word);
</script>

<!-- A form so Enter confirms once the word matches, the same as a click.
     The field has a visible label naming the word to type (DESIGN-LANGUAGE.md,
     "Fields"): a placeholder vanished at the first keystroke, and with it
     the only on-screen copy of the word. -->
<form
	onsubmit={(e) => {
		e.preventDefault();
		if (ready) onconfirm();
	}}
>
	<label for={fieldId} class="block text-sm text-ink-muted"
		>Type <span class="font-mono text-ink">{word}</span> to confirm</label
	>
	<div class="mt-1.5 flex flex-col gap-2 sm:flex-row sm:items-center">
		<input
			id={fieldId}
			class="field w-full sm:w-56"
			bind:value={typed}
			autocomplete="off"
			spellcheck="false"
			use:focusOnMount={autofocus}
		/>
		<div class="flex items-center gap-2">
			<!-- Disabled, it has the one disabled look; armed, the filled red,
			     so the moment it can act is plain (I-661). -->
			<button type="submit" class="btn-danger {ready ? 'btn-danger--armed' : ''}" disabled={!ready}>
				{busy && busyLabel ? busyLabel : label}
			</button>
			{#if oncancel}
				<button type="button" class="btn-ghost" onclick={oncancel}>Cancel</button>
			{/if}
		</div>
	</div>
</form>
