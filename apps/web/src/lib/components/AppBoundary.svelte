<script lang="ts">
	// The root layout's error boundary: a page that throws while it renders
	// shows the 500 page in its shell, with Reload, instead of a skeleton
	// that stays for good. ErrorView owns the one <main>.
	import type { Snippet } from 'svelte';
	import ErrorView from './ErrorView.svelte';

	let { children }: { children: Snippet } = $props();
</script>

<svelte:boundary>
	{@render children()}
	{#snippet failed()}
		<ErrorView status={500} title="Something went wrong">
			{#snippet action()}
				<button type="button" class="hk-btn" onclick={() => location.reload()}>Reload</button>
			{/snippet}
		</ErrorView>
	{/snippet}
</svelte:boundary>
