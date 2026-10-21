<!--
  The plan's memory, held by what runs now (I-674, I-837), in one of three
  looks (lib/memlook.ts; owner feedback 2026-10-21 retired the saturated
  squares). No look uses hue for a running machine: running memory is
  ink, a frozen machine's is the ice ink, memory past the plan is amber.
  a  One segmented bar (Guide B 12.11): a segment per machine in zinc
     steps, the plan's limit a tick, GB over it drawn past the tick.
  b  One square per GB in the same inks; free GB are hairline outlines.
  c  A row per machine: its name, a bar on the plan's scale, its GB.
  Colour is never the only carrier: the reading ("36 GB of 32 GB, over")
  is the meter's value text, the legend names each machine with its GB and
  "frozen", and "Over the plan" is a legend row of its own.
-->
<script lang="ts">
	import { page } from '$app/state';
	import { memLook, type MemLook } from '$lib/memlook';

	type Segment = { name: string; gb: number; frozen?: boolean; href?: string };

	let {
		label,
		segments,
		limit,
		perRow = 32,
		size = 10,
		meter = true,
		legend = false,
		legendLabel,
		look
	}: {
		label: string;
		/** What runs, in order: one segment, run of squares or row each. */
		segments: Segment[];
		limit: number;
		/** Squares per row in look b, halved where they do not fit. */
		perRow?: number;
		/** The square's side in px in look b: 10 on /billing, 8 in a narrow row. */
		size?: 8 | 10;
		/**
		 * The figure carries role=meter itself. False when a Meter wraps it
		 * as its figure and is the meter (one meter, one name).
		 */
		meter?: boolean;
		/** A key under the figure naming each machine. Look c is its own key. */
		legend?: boolean;
		/** The legend list's accessible name; "{label} by machine" by default. */
		legendLabel?: string;
		/** Pins a look (the /design gallery); otherwise ?memlook= in dev. */
		look?: MemLook;
	} = $props();

	let shown = $derived(look ?? memLook(page.url));

	let used = $derived(segments.reduce((n, s) => n + s.gb, 0));
	let over = $derived(Math.max(0, used - limit));
	let scale = $derived(Math.max(limit, used, 1));
	/** Look c: each row's track is the plan, unless one machine is larger. */
	let rowScale = $derived(Math.max(limit, ...segments.map((s) => s.gb), 1));
	let listName = $derived(legendLabel ?? `${label} by machine`);

	/** Running machines' ink, by their place among the running ones: a mix
	 * of the text ink into the page, so each step holds 3:1 in both themes. */
	const STEPS: Record<number, number[]> = { 1: [86], 2: [86, 56] };
	let inks = $derived.by(() => {
		const n = segments.filter((s) => !s.frozen).length;
		const steps = STEPS[n] ?? [86, 64, 48];
		let k = 0;
		return segments.map((s) =>
			s.frozen ? '' : `color-mix(in srgb, var(--hk-text) ${steps[k++ % steps.length]}%, var(--hk-bg))`
		);
	});

	// Look a: each segment's span on the bar, cut at the plan's limit; what
	// lies past the limit is one amber span after the tick.
	let spans = $derived.by(() => {
		const out: { start: number; gb: number; i: number }[] = [];
		let at = 0;
		segments.forEach((s, i) => {
			const end = Math.min(at + s.gb, limit);
			if (end > at) out.push({ start: at, gb: end - at, i });
			at += s.gb;
		});
		return out;
	});
	const pct = (gb: number) => `${(gb / scale) * 100}%`;

	// Look b: one square per GB of the plan or of what runs, if more.
	let squares = $derived.by(() => {
		const out: { seg: number; over: boolean }[] = [];
		segments.forEach((s, i) => {
			for (let k = 0; k < s.gb; k++) out.push({ seg: i, over: out.length >= limit });
		});
		while (out.length < scale) out.push({ seg: -1, over: false });
		return out;
	});
	let cols = $derived(Math.min(scale, perRow));
	let narrow = $derived(Math.min(scale, Math.ceil(perRow / 2)));

	let reading = $derived(
		`${used} GB of ${limit} GB${over ? ', over' : ''}` +
			(segments.length
				? `: ${segments.map((s) => `${s.name} ${s.gb} GB${s.frozen ? ' frozen' : ''}`).join(', ')}`
				: '')
	);
	let meterAttrs = $derived(
		meter
			? {
					role: 'meter',
					'aria-label': label,
					'aria-valuemin': 0,
					'aria-valuemax': limit,
					'aria-valuenow': Math.min(used, limit),
					'aria-valuetext': reading
				}
			: { 'aria-hidden': 'true' as const }
	);
</script>

{#snippet swatch(s: Segment, i: number)}
	<i class="sw" class:ice={s.frozen} style:--ink={inks[i] || undefined} aria-hidden="true"></i>
{/snippet}

{#snippet name(s: Segment)}
	{#if s.href}
		<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -- callers pass a resolve()d path -->
		<a href={s.href} class="link">{s.name}</a>
	{:else}
		<span class="nm">{s.name}</span>
	{/if}
{/snippet}

<div class="mb mb--{shown}" class:mb--8={size === 8} style:--sq="{size}px">
	{#if shown === 'a'}
		<div class="bar" {...meterAttrs}>
			<div class="clip">
				{#each spans as p (p.i)}
					<i
						class="seg"
						class:ice={segments[p.i].frozen}
						style:left={pct(p.start)}
						style:width={p.start + p.gb >= scale ? pct(p.gb) : `calc(${pct(p.gb)} - 2px)`}
						style:--ink={inks[p.i] || undefined}
						title={segments[p.i].name}
					></i>
				{/each}
				{#if over}
					<i class="seg over" style:left={pct(limit)} style:width={pct(over)}></i>
				{/if}
			</div>
			{#if over}
				<i class="tick" style:left="calc({pct(limit)} - 1.5px)"></i>
			{/if}
		</div>
	{:else if shown === 'b'}
		<div class="sqs" style:--cols={cols} style:--cols-narrow={narrow} {...meterAttrs}>
			{#each squares as q, i (i)}
				<i
					class="sq"
					class:held={q.seg >= 0 && !segments[q.seg].frozen && !q.over}
					class:ice={q.seg >= 0 && segments[q.seg].frozen && !q.over}
					class:over={q.over}
					style:--ink={q.seg >= 0 ? inks[q.seg] || undefined : undefined}
					title={q.seg >= 0 ? segments[q.seg].name : undefined}
				></i>
			{/each}
		</div>
	{:else}
		<span class="sr-only" {...meterAttrs}></span>
	{/if}

	{#if shown === 'c' && segments.length}
		<ul class="rows" aria-label={listName}>
			{#each segments as s, i (`${s.name}-${i}`)}
				<li class:frozen={s.frozen}>
					<span class="who">{@render name(s)}</span>
					<span class="gb">{s.gb} GB{#if s.frozen}, <span class="tag">frozen</span>{/if}</span>
					<span class="track" aria-hidden="true">
						<i class:ice={s.frozen} style:width={`${(s.gb / rowScale) * 100}%`} style:--ink={inks[i] || undefined}
						></i>
					</span>
				</li>
			{/each}
			{#if over}
				<li class="is-over">
					<span class="who">Over the plan</span>
					<span class="gb">{over} GB</span>
					<span class="track" aria-hidden="true"
						><i class="over" style:width={`${(over / rowScale) * 100}%`}></i></span
					>
				</li>
			{/if}
		</ul>
	{:else if legend && segments.length}
		<ul class="key" aria-label={listName}>
			{#each segments as s, i (`${s.name}-${i}`)}
				<li>
					{@render swatch(s, i)}{@render name(s)}<span class="gb"
						>{s.gb} GB{#if s.frozen},
							<span class="ice-word">frozen</span>{/if}</span
					>
				</li>
			{/each}
			{#if over}
				<li>
					<i class="sw over" aria-hidden="true"></i><span class="nm">Over the plan</span><span
						class="gb">{over} GB</span
					>
				</li>
			{/if}
		</ul>
	{/if}
</div>

<style>
	.mb {
		/* Amber past the plan: between the dot and the warn text, 3.2:1 on the
		   light page (the dot alone is 2.06:1); the dot itself at night. */
		--over: color-mix(in srgb, var(--hk-warn-dot) 60%, var(--hk-warn));
		--free: color-mix(in srgb, var(--hk-text) 9%, var(--hk-bg));
		/* The rime on frozen memory: the ice ink with a fine paler hatch. */
		--rime: repeating-linear-gradient(
			135deg,
			var(--hk-ice-ink) 0 2px,
			color-mix(in srgb, var(--hk-ice-ink) 74%, var(--hk-bg)) 2px 3.5px
		);
		container-type: inline-size;
	}

	/* a: the bar. 6px like every meter (Guide B 12.11); the tick stands
	   3px proud of it on each side. */
	.bar {
		position: relative;
		height: 6px;
		margin-block: 3px;
		border-radius: var(--hk-radius-pill);
		background: var(--free);
	}
	.seg {
		position: absolute;
		top: 0;
		bottom: 0;
		background: var(--ink);
		border-radius: 1px;
	}
	.clip {
		position: absolute;
		inset: 0;
		overflow: hidden;
		border-radius: var(--hk-radius-pill);
	}
	.seg.ice {
		background: var(--rime);
	}
	.seg.over {
		background: var(--over);
	}
	.tick {
		position: absolute;
		top: -3px;
		bottom: -3px;
		width: 1px;
		background: var(--hk-text);
	}

	/* b: one square per GB, laid out by the box it sits in. */
	.sqs {
		display: grid;
		grid-template-columns: repeat(var(--cols), var(--sq));
		gap: 2px;
		width: max-content;
	}
	/* 32 squares of 10px take 382px, of 8px 318px. */
	@container (width < 382px) {
		.mb:not(.mb--8) .sqs {
			grid-template-columns: repeat(var(--cols-narrow), var(--sq));
		}
	}
	@container (width < 318px) {
		.mb--8 .sqs {
			grid-template-columns: repeat(var(--cols-narrow), var(--sq));
		}
	}
	.sq {
		display: block;
		width: var(--sq);
		height: var(--sq);
		border-radius: 2px;
		box-shadow: inset 0 0 0 1px color-mix(in srgb, var(--hk-text) 22%, var(--hk-bg));
	}
	.sq.held {
		box-shadow: none;
		background: var(--ink);
	}
	.sq.ice {
		box-shadow: none;
		background: var(--rime);
	}
	.sq.over {
		box-shadow: none;
		background: var(--over);
	}

	/* The key under a and b: a quiet list, GB at the right. */
	.key {
		display: grid;
		gap: 0.375rem;
		margin: 0.875rem 0 0;
		padding: 0;
		list-style: none;
		font-size: 0.8125rem;
		line-height: 1.25rem;
	}
	.key li {
		display: flex;
		align-items: center;
		gap: 0.5rem;
		min-width: 0;
	}
	.sw {
		flex: none;
		width: 8px;
		height: 8px;
		border-radius: 2px;
		background: var(--ink);
	}
	.mb--b .sw {
		width: 10px;
		height: 10px;
	}
	.sw.ice {
		background: var(--rime);
	}
	.sw.over {
		background: var(--over);
	}
	.nm,
	.link {
		min-width: 0;
		overflow-wrap: anywhere;
		color: var(--hk-text);
	}
	.link {
		text-decoration: underline;
		text-decoration-color: transparent;
		text-underline-offset: 3px;
		transition: text-decoration-color var(--hk-dur-fast) var(--hk-ease);
	}
	.link:hover {
		text-decoration-color: color-mix(in srgb, currentColor 35%, transparent);
	}
	.gb {
		margin-left: auto;
		padding-left: 1rem;
		white-space: nowrap;
		color: var(--hk-text-3);
		font-variant-numeric: tabular-nums;
	}
	.ice-word,
	.tag {
		color: var(--hk-ice-ink);
	}

	/* c: a row per machine, its name and GB over a bar on the shared scale. */
	.rows {
		display: grid;
		gap: 0.625rem;
		margin: 0;
		padding: 0;
		list-style: none;
		font-size: 0.8125rem;
		line-height: 1.25rem;
	}
	.rows li {
		display: grid;
		grid-template-columns: minmax(0, 1fr) max-content;
		column-gap: 1rem;
		row-gap: 0.3125rem;
		align-items: baseline;
	}
	.who {
		min-width: 0;
		overflow-wrap: anywhere;
	}
	.rows .track {
		grid-column: 1 / -1;
	}
	.track {
		display: block;
		height: 4px;
		border-radius: var(--hk-radius-pill);
		background: var(--free);
	}
	.track i {
		display: block;
		height: 100%;
		border-radius: var(--hk-radius-pill);
		background: var(--ink);
	}
	.track i.ice {
		background: var(--rime);
	}
	.track i.over {
		background: var(--over);
	}
	.rows .gb {
		padding-left: 0;
	}
	.is-over .who,
	.is-over .gb {
		color: var(--hk-warn);
	}

	/* A machine's name takes a thumb's height without growing its row. */
	@media (pointer: coarse), (max-width: 639.98px) {
		.link {
			display: inline-flex;
			align-items: center;
			min-height: 2.75rem;
			margin-block: -0.75rem;
		}
	}

	/* Forced colours: ink is CanvasText, frozen GrayText, over Highlight;
	   the gaps keep segments apart and the key's words carry the rest. */
	@media (forced-colors: active) {
		.seg,
		.sq,
		.sw,
		.track i,
		.tick {
			forced-color-adjust: none;
		}
		.bar,
		.track {
			forced-color-adjust: none;
			background: Canvas;
			outline: 1px solid GrayText;
		}
		.seg,
		.sq.held,
		.sw,
		.track i,
		.tick {
			background: CanvasText;
		}
		.seg.ice,
		.sq.ice,
		.sw.ice,
		.track i.ice {
			background: GrayText;
		}
		.seg.over,
		.sq.over,
		.sw.over,
		.track i.over {
			background: Highlight;
		}
		.sq {
			box-shadow: inset 0 0 0 1px GrayText;
		}
	}
</style>
