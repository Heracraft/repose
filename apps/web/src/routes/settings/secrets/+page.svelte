<script lang="ts">
	// SHELL stub (I-841): the account secrets as they were on /settings.
	// PAGE C gives them their inline "Add secret" form and the Settings
	// tabs (ia-final 2.1).
	import PageShell from '$lib/components/PageShell.svelte';
	import AccountSecrets from '$lib/components/AccountSecrets.svelte';
	import { title } from '$lib/title';
</script>

<svelte:head><title>{title('Secrets · Settings')}</title></svelte:head>

<PageShell title="Settings">
	<AccountSecrets />
</PageShell>
