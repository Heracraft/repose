<!--
  The machine behind a project: the memory in use now (the newest
  sample, I-492) and the disk (the guest's root filesystem, I-567), each
  a figure with a small ring (I-674). The size's vCPUs and memory are on
  the page's status line, so the card does not repeat them. The disk grows in
  place: Resize… under the disk opens the page's resize panel, which
  runs the frame's width under this row, since a form needs room and this
  card is a third of it (I-680).
-->
<script module lang="ts">
	/** The disk sizes Resize offers, as /docs/machine names them (20 to 320 GB). */
	export const DISK_SIZES_GB = [20, 40, 80, 160, 320];
</script>

<script lang="ts">
	import UsageRing from '$lib/components/UsageRing.svelte';
	import Snowflake from '$lib/components/Snowflake.svelte';
	import { diskFullPercent } from '$lib/disk';
	import { bytes, classMemoryGB, gb } from '$lib/format';
	import type { Project } from '$lib/api/types';

	let {
		project,
		memUsed,
		memTotal,
		busy = false,
		frozen = false,
		resizeOpen = false,
		onresizetoggle
	}: {
		project: Project;
		/** The newest sample's memory in use, in bytes; undefined while not running. */
		memUsed?: number;
		/** The machine's memory in bytes, from the samples. */
		memTotal?: number;
		/** Another operation runs: Resize… waits. */
		busy?: boolean;
		/** The page's resize panel is open. */
		resizeOpen?: boolean;
		/** Frozen while idle (I-683, behind a flag): an ice edge and mark. */
		frozen?: boolean;
		/** Opens the page's resize panel, or closes it when open. */
		onresizetoggle: () => void;
	} = $props();

	const GIB = 1024 * 1024 * 1024;

	let ownGB = $derived(classMemoryGB(project.class));
	let memBytes = $derived(memTotal ?? ownGB * GIB);

	// Used is the guest's root filesystem, what its writes run out of; the
	// volume's allocated figure keeps deleted files until the daily fstrim
	// and is not shown (I-567, I-585).
	let diskUsed = $derived(project.root_used_bytes);
	let diskSize = $derived(project.root_size_bytes || project.volume_bytes);
	let full = $derived(diskFullPercent(project));
	/** A volume only grows: Resize… shows while a larger size is left. */
	let canGrow = $derived(DISK_SIZES_GB.some((s) => s * GIB > project.volume_bytes));
</script>

<section
	class="card flex flex-col"
	class:machine--frozen={frozen}
	data-testid="machine-card"
	aria-labelledby="machine-title"
>
	<div class="flex items-baseline justify-between gap-3">
		<h2 id="machine-title" class="text-xl font-semibold">Machine</h2>
		{#if frozen}
			<span class="machine-frost inline-flex items-center gap-1.5 text-xs"
				><Snowflake size={13} />frozen</span
			>
		{/if}
	</div>

	<div class="m-row mt-4">
		{#if memUsed !== undefined}
			<UsageRing label="Memory in use" used={memUsed} limit={memBytes} format={bytes} />
		{:else}
			<span class="m-ring-gap" aria-hidden="true"></span>
		{/if}
		<span class="m-label">Memory in use</span>
		<span class="m-fig">
			{#if memUsed !== undefined}<span class="m-n">{bytes(memUsed)}</span>
				<span class="m-of"> of {bytes(memBytes)}</span>{:else}
				<span class="m-of">not running</span>{/if}
		</span>
	</div>

	<div class="m-row">
		{#if diskUsed !== undefined && diskSize}
			<UsageRing label="Disk" used={diskUsed} limit={diskSize} format={gb} />
		{:else}
			<span class="m-ring-gap" aria-hidden="true"></span>
		{/if}
		<span class="m-label">Disk</span>
		<span class="m-fig"
			><span class="m-n">{diskUsed !== undefined ? gb(diskUsed) : '—'}</span>
			<span class="m-of"> of {gb(diskSize)}</span></span
		>
	</div>
	<div class="m-sub">
		{#if full !== null}
			<p
				class="mt-2 text-xs text-amber-800 tabular-nums dark:text-amber-300"
				data-testid="disk-full"
			>
				{full}% full
			</p>
		{/if}
		<div class="mt-1 flex flex-wrap items-baseline justify-between gap-x-4">
			<span class="text-xs text-ink-muted tabular-nums" data-testid="disk-volume"
				>{gb(project.volume_bytes)} disk</span
			>
			{#if canGrow}
				<button
					type="button"
					id="resize-open"
					class="btn-ghost -mr-2 text-xs"
					aria-expanded={resizeOpen}
					aria-controls="resize-panel"
					disabled={busy && !resizeOpen}
					onclick={onresizetoggle}>Resize…</button
				>
			{:else}
				<!-- tabindex=-1: after a grow to the largest size, focus comes
				     here in place of the button that went away. -->
				<p id="resize-largest" tabindex="-1" class="py-1 text-xs text-ink-muted tabular-nums">
					{DISK_SIZES_GB[DISK_SIZES_GB.length - 1]} GB is the largest size.
				</p>
			{/if}
		</div>
	</div>
</section>

<style>
	.m-row {
		display: grid;
		grid-template-columns: auto minmax(0, 1fr) auto;
		align-items: center;
		column-gap: 0.75rem;
		padding-block: 0.75rem;
	}
	.m-row + .m-row {
		border-top: 1px solid var(--rule);
	}
	.m-ring-gap {
		width: 18px;
	}
	.m-label {
		min-width: 0;
		font-size: var(--text-sm);
		color: var(--ink-muted);
	}
	/* A figure may wrap between its number and its "of", never inside
	   either (I-680). */
	.m-fig {
		font-size: 0.9375rem;
		font-variant-numeric: tabular-nums;
		text-align: right;
	}
	.m-n {
		white-space: nowrap;
	}
	.m-of {
		margin-left: 0;
		white-space: nowrap;
		font-size: var(--text-sm);
		color: var(--ink-muted);
	}
	/* Under the disk row, aligned with its label. */
	/* At the card's foot: when the card is as tall as its row, the space
	   sits above the volume line, not under it. */
	.m-sub {
		margin-top: auto;
		padding-left: calc(18px + 0.75rem);
	}
	.machine--frozen {
		border-color: color-mix(in srgb, var(--frost) 45%, var(--rule));
		box-shadow: inset 2px 0 0 var(--frost);
	}
	.machine-frost {
		color: var(--frost);
	}
</style>
