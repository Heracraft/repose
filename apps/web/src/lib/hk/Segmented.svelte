<script lang="ts" module>
	export interface SegmentOption<T extends string = string> {
		value: T;
		label: string;
	}
</script>

<script lang="ts" generics="T extends string">
	/* Pick one of four or fewer short options (Guide B 12.6). Native radios
	   under the labels: a form posts it, the arrow keys move between options
	   and a screen reader says "radio, 2 of 3". The selected option is a
	   raised thumb that slides between them; label weight never changes, so
	   nothing beside it shifts. Before hydration the checked label carries
	   the thumb's look itself, so the prerendered page shows the selection
	   at the same size. */
	import { onMount } from 'svelte';

	interface Props {
		options: SegmentOption<T>[];
		value: T;
		/** Radio group name; also the form field name. */
		name: string;
		/** The group's accessible name. */
		label: string;
		size?: 'sm' | 'md';
		/** Full width, 44px tall: a form's control on a phone. */
		touch?: boolean;
		onchange?: (value: T) => void;
		class?: string;
	}

	let {
		options,
		value = $bindable(),
		name,
		label,
		size = 'md',
		touch = false,
		onchange,
		class: klass = ''
	}: Props = $props();

	const index = $derived(
		Math.max(
			0,
			options.findIndex((o) => o.value === value)
		)
	);
	let ready = $state(false);
	onMount(() => {
		ready = true;
	});
</script>

<div
	class="hk-seg hk-seg--slide {klass}"
	class:hk-seg--sm={size === 'sm'}
	class:hk-seg--touch={touch}
	role="radiogroup"
	aria-label={label}
	data-ready={ready ? '' : undefined}
	style:--n={options.length}
	style:--i={index}
>
	<span class="hk-seg-thumb" aria-hidden="true"></span>
	{#each options as opt (opt.value)}
		<label>
			<input
				type="radio"
				{name}
				value={opt.value}
				checked={opt.value === value}
				onchange={() => {
					value = opt.value;
					onchange?.(opt.value);
				}}
			/>
			<span>{opt.label}</span>
		</label>
	{/each}
</div>
