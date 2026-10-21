<script lang="ts">
	// SHELL stub (I-841): machine.nix as it was on /settings. PAGE C gives
	// it the full column and the Settings tabs (ia-final 2.1).
	import PageShell from '$lib/components/PageShell.svelte';
	import MachineNix from '$lib/components/MachineNix.svelte';
	import { title } from '$lib/title';
</script>

<svelte:head><title>{title('machine.nix · Settings')}</title></svelte:head>

<PageShell title="Settings">
	<MachineNix />
</PageShell>
