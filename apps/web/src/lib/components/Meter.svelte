<!--
  One share of a plan's limit (Guide B 12.11): memory running, disk held,
  egress this period. Label left, value right, a 6px bar under them, the
  anatomy MemoryMeter shares. The fill is the accent while comfortable,
  amber under 20% left, red under 5% left or past the limit, and grey when
  nothing is counted yet. The value text carries the number and the state
  ("112.4 GB of 100 GB · over"), so the fill's colour is never the only
  signal; aria-valuetext reads the same words.
-->
<script lang="ts">
	import { share } from '$lib/format';

	let {
		label,
		used,
		limit,
		unit = 'GB',
		valueText,
		testid,
		stack = false
	}: {
		label: string;
		used: number;
		limit: number;
		/** The unit after each figure: "24 GB of 32 GB". */
		unit?: string;
		/** The reading, when "{used} {unit} of {limit} {unit}" is not it. */
		valueText?: string;
		/** data-testid; "meter-" plus the label in kebab case by default. */
		testid?: string;
		/** The value under the label rather than beside it: a long value. */
		stack?: boolean;
	} = $props();

	const num = (n: number) => (Number.isInteger(n) ? String(n) : n.toFixed(1));
	let over = $derived(limit > 0 && used > limit);
	let fill = $derived(over ? 1 : share(used, limit));
	let left = $derived(limit > 0 ? 1 - used / limit : 1);
	let tone = $derived(
		used === 0 ? 'neutral' : over || left < 0.05 ? 'err' : left < 0.2 ? 'warn' : 'ok'
	);
	let reading = $derived(
		valueText ??
			`${num(used)} ${unit} of ${num(limit)} ${unit}`.replace(/ +/g, ' ').trim() +
				(over ? ' · over' : '')
	);
	let id = $derived(testid ?? `meter-${label.toLowerCase().replace(/[^a-z0-9]+/g, '-')}`);
</script>

<div
	class="hk-meter"
	class:hk-meter--stack={stack}
	class:hk-meter--warn={tone === 'warn'}
	class:hk-meter--err={tone === 'err'}
	class:hk-meter--neutral={tone === 'neutral'}
	data-testid={id}
>
	<div class="hk-meter-top">
		<span class="hk-meter-label">{label}</span>
		<span class="hk-meter-value">{reading}</span>
	</div>
	<!-- aria-valuenow stops at the limit, which is all a meter can hold, so
	     aria-valuetext gives the real reading, over the limit included. -->
	<div
		role="meter"
		aria-label={label}
		aria-valuemin={0}
		aria-valuemax={limit}
		aria-valuenow={Math.min(used, limit)}
		aria-valuetext={reading}
	>
		<div class="hk-meter-track">
			<div class="hk-meter-fill" style:width="{fill * 100}%"></div>
		</div>
	</div>
</div>
