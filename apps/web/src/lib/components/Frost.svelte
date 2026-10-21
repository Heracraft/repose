<!--
  Window frost on a frozen machine's surface (I-683, I-841 amendment
  2026-10-21): the project head's far corner, the Machine section's memory
  bar, a /projects row's rule. Drawn through a generated mask
  (lib/hk/frost-masks.css) in the ice tokens, behind the surface's text;
  the surface sets position and isolation ([data-frost-on]). Decorative:
  the word "frozen" and its flake carry the state, and forced colours drop
  the frost. It forms over 600ms when the machine froze in the last
  minute (you are watching it freeze, not opening a frozen page), and
  thaws in 300ms when it wakes; with reduced motion it comes and goes at
  once.
-->
<script lang="ts">
	import { cubicOut } from 'svelte/easing';

	let {
		kind,
		since,
		n = 0
	}: {
		kind: 'head' | 'meter' | 'row';
		/** When the machine froze (resting.since). */
		since: string;
		/** A row's index: three masks in turn, so adjacent rows differ. */
		n?: number;
	} = $props();

	const FRESH_MS = 60_000;

	function frost(_node: Element, { duration, intro }: { duration: number; intro?: boolean }) {
		const still =
			typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches;
		const fresh = !intro || Date.now() - Date.parse(since) < FRESH_MS;
		return {
			duration: still || !fresh ? 0 : duration,
			easing: cubicOut,
			css: (t: number) => `opacity: ${t}`
		};
	}
</script>

<span
	class="hk-frost hk-frost--{kind}"
	data-frost-mask={kind === 'row' ? (n % 3) + 1 : undefined}
	aria-hidden="true"
	in:frost={{ duration: 600, intro: true }}
	out:frost={{ duration: 300 }}
></span>
