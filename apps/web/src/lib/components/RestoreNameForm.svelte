<!-- The name for a project restored from a snapshot, asked in place: under
     a row in "Recently destroyed" (DECISIONS I-167) and under a snapshot on
     the project page ("Restore as new"). One component so both carry the
     same visible label, the same error wiring and the same wrap in a
     narrow box; the two copies had drifted apart before. The form opens in
     place of the button that asked for it, so it takes focus into its
     field; the caller puts focus back on that button when it closes. -->
<script lang="ts">
	import { focusOnMount } from '$lib/focus';

	let {
		id,
		value = $bindable(''),
		error,
		busy = false,
		submitLabel = 'Restore',
		busyLabel = 'Restoring…',
		onsubmit,
		oncancel
	}: {
		/** Unique on the page; the field and its error hang off it. */
		id: string;
		value?: string;
		/** Why the api refused the name last time. */
		error?: string;
		busy?: boolean;
		submitLabel?: string;
		busyLabel?: string;
		onsubmit: (name: string) => void;
		oncancel: () => void;
	} = $props();

	let fieldId = $derived(`restore-name-${id}`);
	let errorId = $derived(`restore-error-${id}`);
</script>

<!-- The form lays out by its own width: from 36rem the label, the field
     and the buttons share one line; narrower, the label sits above. The
     field takes what the buttons leave, up to 32rem, and the buttons stay
     together, wrapping under it as one group (I-680). -->
<form
	class="mt-3 @container"
	onsubmit={(e) => {
		e.preventDefault();
		if (value.trim() !== '' && !busy) onsubmit(value.trim());
	}}
>
	<div class="flex flex-col gap-1.5 @xl:flex-row @xl:items-center @xl:gap-3">
		<label for={fieldId} class="text-sm text-ink-muted @xl:shrink-0"
			>Name for the restored project</label
		>
		<div class="flex min-w-0 flex-wrap items-center gap-2 @xl:flex-1">
			<input
				id={fieldId}
				class="field max-w-[32rem] min-w-0 flex-[1_1_12rem]"
				bind:value
				autocomplete="off"
				spellcheck="false"
				aria-invalid={error ? 'true' : undefined}
				aria-describedby={error ? errorId : undefined}
				use:focusOnMount
			/>
			<div class="flex shrink-0 items-center gap-2">
				<button type="submit" class="btn" disabled={busy || value.trim() === ''}
					>{busy ? busyLabel : submitLabel}</button
				>
				<button type="button" class="btn-ghost" onclick={oncancel}>Cancel</button>
			</div>
		</div>
	</div>
	{#if error}
		<p id={errorId} class="field-error">{error}</p>
	{/if}
</form>
