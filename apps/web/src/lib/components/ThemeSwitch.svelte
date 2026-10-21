<script lang="ts">
	// The theme switch, in the footer of every page and nowhere else
	// (DECISIONS I-837): System follows the computer, Light and Dark
	// override it for this browser. The prerendered pages render System
	// checked; once the page runs it shows the stored choice, at the same
	// size.
	import Segmented from '$lib/hk/Segmented.svelte';
	import { setTheme, theme, type ThemeChoice } from '$lib/theme.svelte';

	const options: { value: ThemeChoice; label: string }[] = [
		{ value: 'system', label: 'System' },
		{ value: 'light', label: 'Light' },
		{ value: 'dark', label: 'Dark' }
	];
</script>

<Segmented
	{options}
	value={theme.choice}
	name="theme"
	label="Theme"
	size="sm"
	onchange={(v) => setTheme(v)}
/>
