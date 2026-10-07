<!--
  One tile of the landing's shape language: flat geometric forms in a
  100 x 100 box. Each names a feature and appears where that feature is on the
  page; the footer's row collects them in the page's order. Colours are the pictures' own, by role
  (the --sh-* tokens in routes/layout.css): two greys, ink and the blue
  accent, nothing else. Each shape has one main tone (neutral or accent); its other parts stay grey or ink, so a
  group is mostly grey with a spot of colour, the way the pictures are
  (docs/LANDING.md, "Shape language"). The sphere is the internet, flat like the rest; with meridians it
  carries the hero globe's lines in paper, so the footer's sphere and the
  hero's globe are one drawing. Always decorative, so always aria-hidden.
-->
<script lang="ts" module>
	export type Kind =
		| 'pill'
		| 'halves'
		| 'asterisk'
		| 'ring'
		| 'arch'
		| 'sphere'
		| 'leaf'
		| 'sun'
		| 'moon'
		| 'pinwheel';
	export type Tone = 'neutral' | 'accent';

	// A shape's tone when the page doesn't pick one.
	const TONE: Record<Kind, Tone> = {
		pill: 'neutral',
		halves: 'neutral',
		asterisk: 'neutral',
		ring: 'accent',
		arch: 'neutral',
		sphere: 'neutral',
		leaf: 'neutral',
		sun: 'neutral',
		moon: 'neutral',
		pinwheel: 'neutral'
	};
	const MAIN: Record<Tone, string> = {
		neutral: 'var(--sh-grey)',
		accent: 'var(--sh-accent)'
	};
</script>

<script lang="ts">
	let {
		kind,
		tone,
		meridians = false,
		class: klass = ''
	}: {
		kind: Kind;
		tone?: Tone;
		/** The sphere only: draw the globe's meridians over it. Off by default
		    because the hero draws its own on top (Hero.svelte, .meridians). */
		meridians?: boolean;
		class?: string;
	} = $props();
	let main = $derived(MAIN[tone ?? TONE[kind]]);
</script>

<svg
	viewBox="0 0 100 100"
	class="block h-full w-full overflow-visible {klass}"
	style="--main: {main}"
	aria-hidden="true"
>
	{#if kind === 'pill'}
		<path d="M100 6 H48 A44 44 0 0 0 48 94 H100 Z" fill="var(--main)" />
		<path d="M100 30 H48 A20 20 0 0 0 48 70 H100 Z" fill="var(--sh-light)" />
	{:else if kind === 'halves'}
		<path d="M8 46 A42 42 0 0 1 92 46 Z" fill="var(--main)" />
		<path d="M8 100 A42 42 0 0 1 92 100 Z" fill="var(--main)" />
	{:else if kind === 'asterisk'}
		<g stroke="var(--main)" stroke-width="11">
			<path d="M50 0 V100 M0 50 H100 M15 15 L85 85 M85 15 L15 85" />
		</g>
	{:else if kind === 'ring'}
		<circle cx="50" cy="50" r="50" fill="var(--main)" />
		<circle cx="50" cy="50" r="30" fill="var(--sh-paper)" />
		<circle cx="50" cy="50" r="14" fill="var(--sh-ink)" />
	{:else if kind === 'arch'}
		<path d="M8 100 V44 A42 42 0 0 1 92 44 V100 Z" fill="var(--main)" />
		<path d="M29 100 V46 A21 21 0 0 1 71 46 V100 Z" fill="var(--sh-ink)" />
	{:else if kind === 'sphere'}
		<circle cx="50" cy="50" r="50" fill="var(--main)" />
		{#if meridians}
			<!-- The hero's meridians, its 24-unit drawing scaled to 100. The
			     two parallels end on the disc's edge (the hero clips them with
			     overflow: hidden; this svg draws past its box). -->
			<g
				fill="none"
				stroke="var(--sh-paper)"
				stroke-width="0.9"
				opacity="0.8"
				transform="scale(4.1667)"
			>
				<ellipse cx="12" cy="12" rx="4.2" ry="11.6" />
				<path d="M0.69 8H23.31M0.69 16H23.31" />
			</g>
		{/if}
	{:else if kind === 'leaf'}
		<path d="M100 0 V100 H0 A100 100 0 0 1 100 0 Z" fill="var(--main)" />
	{:else if kind === 'sun'}
		<circle cx="50" cy="50" r="50" fill="var(--main)" />
	{:else if kind === 'moon'}
		<path d="M72 4 A50 50 0 1 0 72 96 A40 40 0 1 1 72 4 Z" fill="var(--main)" />
	{:else if kind === 'pinwheel'}
		<path d="M50 50 V0 A50 50 0 0 0 0 50 Z" fill="var(--main)" />
		<path d="M50 50 V100 A50 50 0 0 0 100 50 Z" fill="var(--sh-light)" />
	{/if}
</svg>
