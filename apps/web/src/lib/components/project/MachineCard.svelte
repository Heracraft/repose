<!--
  The machine behind a project (I-837): memory in use now (the newest
  sample, I-492) and the disk (the guest's root filesystem, I-567) as
  meters, then this project's egress this billing period. The size's
  cores and memory are on the page's status line, so the section does not
  repeat them. Under the disk, Resize…, which opens the
  page's resize panel across the frame under this row, since a form needs
  room (I-680).
-->
<script module lang="ts">
	/** The disk sizes Resize offers, as /docs/machine names them (20 to 320 GB). */
	export const DISK_SIZES_GB = [20, 40, 80, 160, 320];
</script>

<script lang="ts">
	import Meter from '$lib/components/Meter.svelte';
	import { diskFullPercent } from '$lib/disk';
	import { bytes, classMemoryGB, gb, gbs } from '$lib/format';
	import type { Project } from '$lib/api/types';
	import { frozenSince } from '$lib/frozen';
	import Frost from '$lib/components/Frost.svelte';

	let {
		project,
		memUsed,
		memTotal,
		egressGb,
		busy = false,
		resizeOpen = false,
		onresizetoggle
	}: {
		project: Project;
		/** The newest sample's memory in use, in bytes; undefined while not running. */
		memUsed?: number;
		/** The machine's memory in bytes, from the samples. */
		memTotal?: number;
		/** This project's egress this billing period, in GB; undefined with billing off. */
		egressGb?: number;
		/** Another operation runs: Resize… waits. */
		busy?: boolean;
		/** The page's resize panel is open. */
		resizeOpen?: boolean;
		/** Opens the page's resize panel, or closes it when open. */
		onresizetoggle: () => void;
	} = $props();

	const GIB = 1024 * 1024 * 1024;

	let memBytes = $derived(memTotal ?? classMemoryGB(project.class) * GIB);

	// Used is the guest's root filesystem, what its writes run out of; the
	// volume's allocated figure keeps deleted files until the daily fstrim
	// and is not shown (I-567, I-585).
	let diskUsed = $derived(project.root_used_bytes);
	let diskSize = $derived(project.root_size_bytes || project.volume_bytes);
	let full = $derived(diskFullPercent(project));
	/** A volume only grows: Resize… shows while a larger size is left. */
	let frozen = $derived(frozenSince(project));
	let canGrow = $derived(DISK_SIZES_GB.some((s) => s * GIB > project.volume_bytes));
</script>

<section class="hk-section machine" data-testid="machine-card" aria-labelledby="machine-title">
	<div class="hk-section-head">
		<h2 id="machine-title" class="hk-h2-app">Machine</h2>
	</div>

	<div class="meters">
		{#if memUsed !== undefined}
			<!-- A frozen machine's memory is what is frozen: the bar takes the
			     ice ink and rime (lib/hk/frost.css). -->
			<div class:hk-meter-ice={!!frozen} data-frost-on={frozen ? 'meter' : undefined}>
				<Meter
					label="Memory in use"
					used={memUsed / GIB}
					limit={memBytes / GIB}
					valueText="{bytes(memUsed)} of {bytes(memBytes)}"
				/>
				{#if frozen}<Frost kind="meter" since={frozen} />{/if}
			</div>
		{:else}
			<div class="line">
				<span class="line-label">Memory in use</span>
				<span class="line-value line-value--off">not running</span>
			</div>
		{/if}

		<div>
			{#if diskUsed !== undefined && diskSize}
				<Meter
					label="Disk"
					used={diskUsed / GIB}
					limit={diskSize / GIB}
					valueText="{gb(diskUsed)} of {gb(diskSize)}"
				/>
			{:else}
				<div class="line">
					<span class="line-label">Disk</span>
					<span class="line-value">{gb(diskSize)}</span>
				</div>
			{/if}
			<div class="disk-foot">
				<span class="disk-facts">
					<!-- One size on screen, the meter's (what `repose status`
					     prints); the volume behind it is for Resize and a reader. -->
					<span class="hk-sr" data-testid="disk-volume">{gb(project.volume_bytes)} disk</span>
					{#if full !== null}
						<span class="disk-full" data-testid="disk-full"
							><span class="hk-dot hk-dot--warn" aria-hidden="true"></span>{full}% full</span
						>
					{/if}
				</span>
				{#if canGrow}
					<button
						type="button"
						id="resize-open"
						class="hk-btn-ghost hk-btn-sm resize"
						aria-expanded={resizeOpen}
						aria-controls="resize-panel"
						disabled={busy && !resizeOpen}
						onclick={onresizetoggle}>Resize…</button
					>
				{:else}
					<!-- tabindex=-1: after a grow to the largest size, focus comes
					     here in place of the button that went away. -->
					<span id="resize-largest" tabindex="-1" class="largest">
						{DISK_SIZES_GB[DISK_SIZES_GB.length - 1]} GB is the largest size.
					</span>
				{/if}
			</div>
		</div>

		{#if egressGb !== undefined}
			<div class="line" data-testid="egress-period">
				<span class="line-label">Egress this period</span>
				<span class="line-value">{gbs(egressGb)}</span>
			</div>
		{/if}
	</div>
</section>

<style>
	.meters {
		display: grid;
		gap: 1.25rem;
	}
	/* A figure with no bar, in the meter's top row. */
	.line {
		display: flex;
		align-items: baseline;
		justify-content: space-between;
		gap: 1rem;
		font-size: 0.8125rem;
	}
	.line-label {
		color: var(--hk-text-3);
	}
	.line-value {
		font-weight: 500;
		font-variant-numeric: tabular-nums;
		color: var(--hk-text);
	}
	.line-value--off {
		font-weight: 400;
		color: var(--hk-text-3);
	}
	.disk-foot {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.5rem 1rem;
		min-height: 1.875rem;
		margin-top: 0.375rem;
		font-size: 0.8125rem;
		color: var(--hk-text-3);
		font-variant-numeric: tabular-nums;
	}
	.disk-facts {
		display: flex;
		flex-wrap: wrap;
		gap: 0 0.75rem;
	}
	.disk-full {
		display: inline-flex;
		align-items: center;
		gap: 0.375rem;
		color: var(--hk-text-2);
	}
	.resize {
		margin-left: auto;
		margin-right: -0.625rem;
	}
	@media (forced-colors: active) {
		.resize {
			margin-right: 0;
		}
	}
	.largest {
		margin-left: auto;
		text-align: right;
	}
</style>
