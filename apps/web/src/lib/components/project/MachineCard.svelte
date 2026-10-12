<!--
  The machine behind a project: the memory in use now (the newest
  sample, I-492) and the disk (the guest's root filesystem, I-567), each
  a figure with a small ring (I-674). The size's vCPUs and memory are on
  the page's status line, so the card does not repeat them. The disk grows in
  place: Resize… opens a panel on the disk's row (DESIGN-LANGUAGE.md,
  "Focus follows the panel").
-->
<script lang="ts">
	import UsageRing from '$lib/components/UsageRing.svelte';
	import { focusAfterRender, focusOnMount } from '$lib/focus';
	import { diskFullPercent } from '$lib/disk';
	import { gb, classMemoryGB } from '$lib/format';
	import type { Project } from '$lib/api/types';

	let {
		project,
		memUsed,
		memTotal,
		busy = false,
		resizing = false,
		onresize
	}: {
		project: Project;
		/** The newest sample's memory in use, in bytes; undefined while not running. */
		memUsed?: number;
		/** The machine's memory in bytes, from the samples. */
		memTotal?: number;
		/** Another operation runs: Grow waits. */
		busy?: boolean;
		resizing?: boolean;
		/** Grows the disk to this many bytes; resolves true when the panel should close. */
		onresize: (bytes: number) => Promise<boolean>;
	} = $props();

	const GIB = 1024 * 1024 * 1024;
	const SIZES_GB = [20, 40, 80, 160, 320];

	let ownGB = $derived(classMemoryGB(project.class));
	let memBytes = $derived(memTotal ?? ownGB * GIB);
	/** A memory figure as "3.4 GB", or "410 MB" under a gigabyte. */
	const memGb = (b: number) =>
		b < GIB
			? `${Math.round(b / (1024 * 1024))} MB`
			: `${(b / GIB).toFixed(1).replace(/\.0$/, '')} GB`;

	// Used is the guest's root filesystem, what its writes run out of; the
	// volume's allocated figure keeps deleted files until the daily fstrim
	// and is not shown (I-567, I-585).
	let diskUsed = $derived(project.root_used_bytes);
	let diskSize = $derived(project.root_size_bytes || project.volume_bytes);
	let full = $derived(diskFullPercent(project));

	// Resize. A volume only grows, so the choices are the sizes strictly
	// larger than the disk now, and the select opens on the smallest of them.
	let showResize = $state(false);
	let largerSizes = $derived(SIZES_GB.filter((s) => s * GIB > project.volume_bytes));
	let resizeTo = $state<number | undefined>(undefined);
	let resizeForm = $state<HTMLFormElement | undefined>(undefined);
	// A poll can grow the disk under an open panel (another tab, the CLI);
	// the choice then moves to the next size that is still a grow.
	$effect(() => {
		if (showResize && (resizeTo === undefined || !largerSizes.includes(resizeTo))) {
			resizeTo = largerSizes[0];
		}
	});

	function openResize() {
		resizeTo = largerSizes[0];
		showResize = true;
	}

	/**
	 * Close the resize panel. Focus goes back to the Resize… button (or, when
	 * the disk is now at the largest size, to the sentence that replaces it),
	 * but only if it was inside the panel: a grow ends after a poll, and by
	 * then the person may be somewhere else on the page.
	 */
	function closeResize() {
		const hadFocus = !!resizeForm?.contains(document.activeElement);
		showResize = false;
		if (hadFocus) void focusAfterRender('resize-open', 'resize-largest');
	}

	async function submit() {
		// The select only offers grows, but the api must never be asked to
		// shrink a disk from a button labelled Grow, whatever the select holds.
		if (resizeTo === undefined || resizeTo * GIB <= project.volume_bytes) return;
		if (await onresize(resizeTo * GIB)) closeResize();
	}
</script>

<section class="card" data-testid="machine-card" aria-labelledby="machine-title">
	<h2 id="machine-title" class="text-xl font-semibold">Machine</h2>

	<div class="m-row mt-4">
		{#if memUsed !== undefined}
			<UsageRing label="Memory in use" used={memUsed} limit={memBytes} format={memGb} />
		{:else}
			<span class="m-ring-gap" aria-hidden="true"></span>
		{/if}
		<span class="m-label">Memory in use</span>
		<span class="m-fig">
			{#if memUsed !== undefined}{memGb(memUsed)}
				<span class="m-of"> of {memGb(memBytes)}</span>{:else}
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
			>{diskUsed !== undefined ? gb(diskUsed) : '—'}
			<span class="m-of"> of {gb(diskSize)}</span></span
		>
	</div>
	<div class="m-sub">
		{#if full !== null}
			<p
				class="mt-2 text-xs text-amber-800 tabular-nums dark:text-amber-300"
				data-testid="disk-full"
			>
				{full} percent full
			</p>
		{/if}
		{#if !showResize}
			<div class="mt-1 flex flex-wrap items-baseline justify-between gap-x-4">
				<span class="text-xs text-ink-muted tabular-nums" data-testid="disk-volume"
					>{gb(project.volume_bytes)} volume</span
				>
				{#if largerSizes.length > 0}
					<button
						type="button"
						id="resize-open"
						class="btn-ghost -mr-2 text-xs"
						disabled={busy}
						onclick={openResize}>Resize…</button
					>
				{:else}
					<!-- tabindex=-1: after a grow to the largest size, focus comes
					     here in place of the button that went away. -->
					<p id="resize-largest" tabindex="-1" class="py-1 text-xs text-ink-muted tabular-nums">
						{SIZES_GB[SIZES_GB.length - 1]} GB is the largest size.
					</p>
				{/if}
			</div>
		{:else}
			<!-- The panel takes the Resize… button's place, so the select
			     takes its focus, and Cancel gives it back. -->
			<form
				class="mt-3 rounded-sm border p-3 border-rule"
				bind:this={resizeForm}
				onsubmit={(e) => {
					e.preventDefault();
					void submit();
				}}
			>
				<label for="resize-to" class="block text-sm text-ink-muted">Grow to</label>
				<div class="mt-1.5 flex flex-wrap items-center gap-2">
					<select id="resize-to" class="field tabular-nums" bind:value={resizeTo} use:focusOnMount>
						{#each largerSizes as s (s)}
							<option value={s}>{s} GB</option>
						{/each}
					</select>
					<button type="submit" class="btn-quiet" disabled={busy || resizeTo === undefined}>
						{resizing ? 'Resizing…' : 'Grow'}
					</button>
					<button type="button" class="btn-ghost" onclick={closeResize}>Cancel</button>
				</div>
			</form>
		{/if}
	</div>
</section>

<style>
	.m-row {
		display: grid;
		grid-template-columns: auto 1fr auto;
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
		font-size: var(--text-sm);
		color: var(--ink-muted);
	}
	.m-fig {
		font-size: 0.9375rem;
		font-variant-numeric: tabular-nums;
		white-space: nowrap;
	}
	.m-of {
		margin-left: 0;
		font-size: var(--text-sm);
		color: var(--ink-muted);
	}
	/* Under the disk row, aligned with its label. */
	.m-sub {
		padding-left: calc(18px + 0.75rem);
	}
</style>
