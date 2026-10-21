<script lang="ts">
	/* Copy to the clipboard with a check drawn in its place (Guide B 12.8).
	   The icon becomes a check for 1.2s and a polite live region says
	   "Copied", so the confirmation is never the icon alone. One component for
	   every copy on the site except the docs' own button.copy (DOCS styles
	   that in place; its markup is tested).

	   tone="dark" sits on a code pane, which is dark in both themes;
	   tone="page" follows the page. With `label` the face shows the word and
	   `ariaLabel` (when given) names what is copied: "Copy" on the face,
	   "Copy the install command" to a screen reader, which starts with the
	   face text (WCAG 2.5.3). */
	interface Props {
		text: string | (() => string);
		/** Visible text beside the icon. Icon only when omitted. */
		label?: string;
		/** Accessible name: required for the icon-only form. */
		ariaLabel?: string;
		tone?: 'dark' | 'page';
		/** sm 24px, md 28px, lg 40px (a hero pair). sm and md reach 44px on touch. */
		size?: 'sm' | 'md' | 'lg';
		oncopied?: () => void;
		class?: string;
	}

	let {
		text,
		label,
		ariaLabel,
		tone = 'page',
		size = 'md',
		oncopied,
		class: klass = ''
	}: Props = $props();

	let copied = $state(false);
	let timer: ReturnType<typeof setTimeout> | undefined;

	async function copy() {
		const value = typeof text === 'function' ? text() : text;
		try {
			await navigator.clipboard.writeText(value);
		} catch {
			// Clipboard API refused (an insecure origin, old Safari): a hidden
			// textarea, which execCommand can still copy from.
			const ta = document.createElement('textarea');
			ta.value = value;
			ta.setAttribute('readonly', '');
			ta.style.position = 'fixed';
			ta.style.opacity = '0';
			document.body.appendChild(ta);
			ta.select();
			document.execCommand('copy');
			ta.remove();
		}
		copied = true;
		oncopied?.();
		clearTimeout(timer);
		timer = setTimeout(() => (copied = false), 1200);
	}

	$effect(() => () => clearTimeout(timer));
</script>

<button
	type="button"
	class="hk-copy hk-copy--{tone} hk-copy--{size} {klass}"
	class:hk-copy--labelled={label}
	class:is-copied={copied}
	onclick={copy}
	aria-label={ariaLabel ?? (label ? undefined : 'Copy')}
	title={label ? undefined : copied ? 'Copied' : (ariaLabel ?? 'Copy')}
>
	<span class="icon" aria-hidden="true">
		<svg
			class="i-copy"
			viewBox="0 0 16 16"
			fill="none"
			stroke="currentColor"
			stroke-width="1.4"
			stroke-linecap="round"
			stroke-linejoin="round"
		>
			<rect x="5.25" y="5.25" width="8" height="8" rx="1.75" />
			<path
				d="M10.75 5.25V4a1.75 1.75 0 0 0-1.75-1.75H4A1.75 1.75 0 0 0 2.25 4v5c0 .97.78 1.75 1.75 1.75h1.25"
			/>
		</svg>
		<svg
			class="i-check"
			viewBox="0 0 16 16"
			fill="none"
			stroke="currentColor"
			stroke-width="1.6"
			stroke-linecap="round"
			stroke-linejoin="round"
		>
			<path d="M3.5 8.5l3 3 6-7" pathLength="1" />
		</svg>
	</span>
	{#if label}
		<!-- Both words share one grid cell, so the button keeps the wider
		     width and nothing beside it moves on copy. -->
		<span class="text" aria-hidden={ariaLabel ? 'true' : undefined}>
			<span class:off={copied}>{label}</span>
			<span class:off={!copied} aria-hidden="true">Copied</span>
		</span>
	{/if}
	<span class="hk-sr" aria-live="polite">{copied ? 'Copied' : ''}</span>
</button>

<style>
	.hk-copy {
		position: relative;
		display: inline-flex;
		flex: none;
		align-items: center;
		justify-content: center;
		gap: 0.375rem;
		border-radius: var(--hk-radius-6);
		border: 1px solid transparent;
		background: transparent;
		cursor: pointer;
		font: inherit;
		font-weight: 500;
		line-height: 1;
		white-space: nowrap;
		transition:
			color var(--hk-dur-fast) var(--hk-ease),
			background-color var(--hk-dur-fast) var(--hk-ease),
			border-color var(--hk-dur-fast) var(--hk-ease);
	}
	.hk-copy--md {
		height: 1.75rem;
		min-width: 1.75rem;
		font-size: 0.8125rem;
	}
	.hk-copy--sm {
		height: 1.5rem;
		min-width: 1.5rem;
		font-size: 0.75rem;
	}
	.hk-copy--lg {
		height: 2.5rem;
		min-width: 2.5rem;
		font-size: 0.875rem;
		border-radius: var(--hk-radius);
		gap: 0.5rem;
	}
	.hk-copy--labelled {
		padding: 0 0.5rem;
	}
	.hk-copy--lg.hk-copy--labelled {
		padding: 0 0.875rem;
	}
	/* On touch the small sizes reach 44px; the negative block margin keeps
	   the row around them at its own height. */
	@media (pointer: coarse), (max-width: 639.98px) {
		.hk-copy--sm,
		.hk-copy--md {
			min-height: 2.75rem;
			min-width: 2.75rem;
		}
		.hk-copy--sm {
			margin-block: -0.625rem;
		}
		.hk-copy--md {
			margin-block: -0.5rem;
		}
		.hk-copy--lg {
			min-height: 2.75rem;
		}
	}
	.hk-copy--dark {
		color: var(--hk-code-muted);
	}
	.hk-copy--dark:hover {
		color: var(--hk-code-text);
		background: rgb(255 255 255 / 0.06);
	}
	.hk-copy--dark.is-copied {
		color: var(--hk-code-ok);
	}
	.hk-copy--dark:focus-visible {
		outline-color: var(--hk-code-string);
	}
	.hk-copy--page {
		color: var(--hk-text-3);
	}
	.hk-copy--page:hover {
		color: var(--hk-text);
		background: var(--hk-hover);
	}
	.hk-copy--page.hk-copy--labelled {
		color: var(--hk-text);
		border-color: var(--hk-border);
		background: var(--hk-surface);
	}
	.hk-copy--page.hk-copy--labelled:hover {
		border-color: var(--hk-border-strong);
		background: var(--hk-surface-2);
	}
	.hk-copy--page.is-copied {
		color: var(--hk-ok);
	}
	.icon {
		position: relative;
		width: 0.9375rem;
		height: 0.9375rem;
		flex: none;
	}
	.icon svg {
		position: absolute;
		inset: 0;
		width: 100%;
		height: 100%;
		transition:
			opacity var(--hk-dur-icon) var(--hk-ease),
			transform var(--hk-dur) var(--hk-ease);
	}
	.i-check {
		opacity: 0;
		transform: scale(0.6);
	}
	.i-check path {
		stroke-dasharray: 1;
		stroke-dashoffset: 1;
		transition: stroke-dashoffset var(--hk-dur-draw) var(--hk-ease) 60ms;
	}
	.is-copied .i-copy {
		opacity: 0;
		transform: scale(0.6);
	}
	.is-copied .i-check {
		opacity: 1;
		transform: scale(1);
	}
	.is-copied .i-check path {
		stroke-dashoffset: 0;
	}
	.text {
		display: grid;
	}
	.text > span {
		grid-area: 1 / 1;
		text-align: center;
	}
	.text > .off {
		visibility: hidden;
	}
</style>
