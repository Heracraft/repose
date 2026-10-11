<!--
  The machine behind a project: what its size gives, the memory in use
  now (the newest sample, I-492) and the disk (the guest's root
  filesystem, I-567), each a meter of its limit. The size's share of the
  plan's memory is the line under the memory meter. The disk grows in
  place: Resize… opens a panel on the disk's row (DESIGN-LANGUAGE.md,
  "Focus follows the panel").
-->
<script lang="ts">
	import Meter from '$lib/components/Meter.svelte';
	import { focusAfterRender, focusOnMount } from '$lib/focus';
	import { diskFullPercent } from '$lib/disk';
	import { gb, classMemoryGB } from '$lib/format';
	import type { Me, Project } from '$lib/api/types';

	let {
		project,
		me,
		vcpus,
		memUsed,
		memTotal,
		busy = false,
		resizing = false,
		onresize
	}: {
		project: Project;
		me?: Me;
		vcpus?: number;
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

	<dl class="spec mt-4 grid grid-cols-2 rounded-sm border border-rule">
		<div class="px-3 py-2.5">
			<dt class="text-xs text-ink-muted">vCPUs</dt>
			<dd class="mt-0.5 font-display text-xl font-semibold tabular-nums">{vcpus ?? '—'}</dd>
		</div>
		<div class="border-l px-3 py-2.5 border-rule">
			<dt class="text-xs text-ink-muted">Memory</dt>
			<dd class="mt-0.5 font-display text-xl font-semibold tabular-nums">
				{ownGB ? `${ownGB} GB` : '—'}
			</dd>
		</div>
	</dl>

	<div class="mt-5">
		{#if memUsed !== undefined}
			<Meter label="Memory in use" used={memUsed} limit={memBytes} format={memGb} />
		{:else}
			<div class="flex items-baseline justify-between gap-4 text-sm">
				<span class="font-medium">Memory in use</span>
				<span class="text-ink-muted">not running</span>
			</div>
		{/if}
		{#if me && me.limits.memory_gb > 0 && ownGB}
			<p class="mt-2 text-xs text-ink-muted" data-testid="plan-memory">
				{project.class} uses {ownGB} of {me.limits.memory_gb} GB plan memory
			</p>
		{/if}
	</div>

	<div class="mt-5">
		{#if diskUsed !== undefined && diskSize}
			<Meter label="Disk" used={diskUsed} limit={diskSize} format={gb} />
		{:else}
			<div class="flex items-baseline justify-between gap-4 text-sm">
				<span class="font-medium">Disk</span>
				<span class="font-mono text-compact text-ink-muted tabular-nums">— of {gb(diskSize)}</span>
			</div>
		{/if}
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
