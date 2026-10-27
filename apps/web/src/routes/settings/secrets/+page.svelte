<script lang="ts">
	// Settings → Secrets (DECISIONS I-697, I-841): the account's secrets.
	import AccountSecrets from '$lib/components/AccountSecrets.svelte';
	import { title } from '$lib/title';
</script>

<svelte:head><title>{title('Secrets · Settings')}</title></svelte:head>

<AccountSecrets />
