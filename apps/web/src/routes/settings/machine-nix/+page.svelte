<script lang="ts">
	// Settings → machine.nix (DECISIONS I-490, I-841): the account's
	// home-manager module, in the full column.
	import MachineNix from '$lib/components/MachineNix.svelte';
	import { title } from '$lib/title';
</script>

<svelte:head><title>{title('machine.nix · Settings')}</title></svelte:head>

<MachineNix />
