<!-- The name for a project restored from a snapshot, asked in place: under
     a row in "Recently destroyed" (DECISIONS I-167) and in a snapshot's
     restore panel on the project page ("Restore as new"). One component so
     both carry the same visible label, the same error wiring and the same
     wrap in a narrow box. It opens where the button that asked for it was,
     so it takes focus into its field unless told otherwise; the caller
     puts focus back on that button when it closes. -->
<script lang="ts">
	import { focusOnMount } from '$lib/focus';

	let {
		id,
		value = $bindable(''),
		error,
		busy = false,
		submitLabel = 'Restore',
		busyLabel = 'Restoring…',
		autofocus = true,
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
		/** Focus the field when the form opens. */
		autofocus?: boolean;
		onsubmit: (name: string) => void;
		/** Draws Cancel; without it the form has none (a panel that has its own). */
		oncancel?: () => void;
	} = $props();

	let fieldId = $derived(`restore-name-${id}`);
	let errorId = $derived(`restore-error-${id}`);
	/** Submitted empty: said under the field, which takes focus. */
	let empty = $state(false);
	let field = $state<HTMLInputElement>();
	let shown = $derived(empty ? 'Name is required.' : error);
</script>

<!-- From 36rem of its own width the label, the field and the buttons share
     one line; narrower, the label sits above and the buttons wrap under
     the field as one group (I-680). -->
<form
	class="rnf"
	onsubmit={(e) => {
		e.preventDefault();
		if (busy) return;
		if (value.trim() === '') {
			empty = true;
			field?.focus();
			return;
		}
		onsubmit(value.trim());
	}}
>
	<div class="rnf-row">
		<label for={fieldId} class="hk-field-label rnf-label">Name for the restored project</label>
		<div class="rnf-controls">
			<input
				id={fieldId}
				class="hk-input rnf-input"
				bind:value
				bind:this={field}
				oninput={() => (empty = false)}
				autocomplete="off"
				spellcheck="false"
				aria-invalid={shown ? 'true' : undefined}
				aria-describedby={shown ? errorId : undefined}
				use:focusOnMount={autofocus}
			/>
			<div class="rnf-buttons">
				<button type="submit" class="hk-btn-secondary" disabled={busy}
					>{busy ? busyLabel : submitLabel}</button
				>
				{#if oncancel}
					<button type="button" class="hk-btn-ghost" onclick={oncancel}>Cancel</button>
				{/if}
			</div>
		</div>
	</div>
	{#if shown}
		<p id={errorId} class="hk-field-error">{shown}</p>
	{/if}
</form>

<style>
	.rnf {
		container-type: inline-size;
	}
	.rnf-row {
		display: flex;
		flex-direction: column;
		gap: 0.375rem;
	}
	.rnf-label {
		margin: 0;
	}
	.rnf-controls {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 0.5rem;
		min-width: 0;
	}
	.rnf-input {
		flex: 1 1 12rem;
		min-width: 0;
		max-width: 24rem;
		font-family: var(--hk-font-mono);
	}
	.rnf-buttons {
		display: flex;
		flex: none;
		align-items: center;
		gap: 0.25rem;
	}
	@container (min-width: 36rem) {
		.rnf-row {
			flex-direction: row;
			align-items: center;
			gap: 0.75rem;
		}
		.rnf-label {
			flex: none;
		}
		.rnf-controls {
			flex: 1 1 auto;
		}
	}
</style>
