<!--
  The hero's terminal (Guide B 12.9, DECISIONS I-837): the Quickstart's
  `repose run` session as index.md prints it, read at build time
  (session.ts), so the pane shows what the CLI shows and nothing invented.

  Motion: once, when 40% of the pane is in view, each line rises 4px and
  fades in, one every 450ms; a still caret ends the last line. No prompt
  follows it: `repose run -p` attaches you to the agent's tmux window, so
  the real session never returns to a shell. No copy button either: the
  `cd` into the example folder fails on a visitor's laptop, and the hero's
  own copy button is the one the reader acts on.
  No loop and no blink, so nothing needs a pause control (WCAG 2.2.2).
  Lines wait hidden by opacity and transform only, so the pane's height
  never changes. They are hidden from the first paint only where app.html's
  script set `html.js` and motion is welcome; if the bundle never runs, a
  CSS fallback shows them all 5s in. Without scripts, under reduced motion
  and in the prerendered HTML read by a crawler, every line is there.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { SESSION } from './session';

	/** The checkout the session runs in: index.md's `cd` target. */
	const TITLE = '~/code/your-project';
	const STEP = 450;

	// Each line is its mark ("$ " before a command, "✓ " before a step
	// done) and its text, so the mark takes its own colour.
	type Line = { cmd: boolean; mark: string; text: string };
	const lines: Line[] = [
		...SESSION.commands.map((text) => ({ cmd: true, mark: '$ ', text })),
		...SESSION.output.map((text) => {
			const done = text.startsWith('✓ ');
			return { cmd: false, mark: done ? '✓ ' : '', text: done ? text.slice(2) : text };
		})
	];
	const total = lines.length;

	let pane: HTMLElement;
	let playing = $state(false);
	let count = $state(total);

	onMount(() => {
		if (matchMedia('(prefers-reduced-motion: reduce)').matches) return;
		// Read the pane, not the clock: on a first load the lines wait
		// hidden (html.js) until the CSS fallback shows them 5s in, and a
		// pane mounted by a client-side navigation starts hidden again.
		// Once the fallback has run, replaying would blank a pane the
		// reader is already reading.
		const first = pane.querySelector('.ln');
		if (!first || getComputedStyle(first).opacity === '1') return;
		count = 0;
		playing = true;
		let timer: ReturnType<typeof setInterval> | undefined;
		const io = new IntersectionObserver(
			(entries) => {
				if (!entries.some((e) => e.isIntersecting)) return;
				io.disconnect();
				count = 1;
				timer = setInterval(() => {
					count += 1;
					if (count >= total) clearInterval(timer);
				}, STEP);
			},
			{ threshold: 0.4 }
		);
		io.observe(pane);
		return () => {
			io.disconnect();
			clearInterval(timer);
		};
	});
</script>

<div class="term" class:playing bind:this={pane} role="region" aria-label="A repose run session">
	<div class="bar">
		<span class="title">{TITLE}</span>
	</div>
	<div class="body" aria-live="off">
		{#each lines as line, i (i)}
			<p class="ln" class:on={i < count} class:cmd={line.cmd}>
				<span class={line.cmd ? 'dim' : 'ok'} aria-hidden={line.cmd}>{line.mark}</span><span
					class:dim={!line.cmd}>{line.text}</span
				>{#if i === total - 1}<span class="caret" aria-hidden="true"></span>{/if}
			</p>
		{/each}
	</div>
</div>

<style>
	.term {
		display: flex;
		flex-direction: column;
		min-width: 0;
		overflow: hidden;
		border-radius: var(--hk-radius-lg);
		background: var(--hk-code-bg);
		box-shadow:
			0 0 0 1px var(--hk-code-border-outer),
			inset 0 0 0 1px var(--hk-code-border),
			var(--hk-shadow-terminal);
		color: var(--hk-code-text);
	}
	.bar {
		display: flex;
		flex: none;
		align-items: center;
		justify-content: space-between;
		height: 2.375rem;
		padding: 0 1rem;
		border-bottom: 1px solid var(--hk-code-border);
		background: var(--hk-code-bar);
	}
	.title {
		min-width: 0;
		overflow: hidden;
		font-family: var(--hk-font-sans);
		font-size: 0.75rem;
		color: var(--hk-code-muted);
		white-space: nowrap;
		text-overflow: ellipsis;
	}
	.body {
		padding: 0.875rem 1rem;
		font-family: var(--hk-font-mono);
		font-size: 0.75rem;
		line-height: 1.25rem;
	}
	/* One line each; a long one wraps and hangs two columns in. */
	.ln {
		margin: 0;
		padding-left: 2ch;
		text-indent: -2ch;
		white-space: pre-wrap;
		overflow-wrap: anywhere;
	}
	.cmd {
		color: var(--hk-code-text);
	}
	.dim {
		color: var(--hk-code-muted);
	}
	.ok {
		color: var(--hk-code-ok);
	}
	.dim[aria-hidden='true'] {
		user-select: none;
	}
	.caret {
		display: inline-block;
		margin-left: 1ch;
		width: 7px;
		height: 14px;
		vertical-align: -2px;
		background: var(--hk-code-text);
	}

	@media (prefers-reduced-motion: no-preference) {
		:global(html.js) .ln {
			opacity: 0;
			transform: translateY(4px);
			animation: term-fallback 0s linear 5s forwards;
		}
		:global(html.js) .playing .ln {
			animation: none;
			transition:
				opacity var(--hk-dur-enter) var(--hk-ease),
				transform var(--hk-dur-enter) var(--hk-ease);
		}
		:global(html.js) .playing .ln.on {
			opacity: 1;
			transform: none;
		}
	}
	@keyframes term-fallback {
		to {
			opacity: 1;
			transform: none;
		}
	}
	@media (forced-colors: active) {
		.term {
			border: 1px solid CanvasText;
		}
		.caret {
			forced-color-adjust: none;
			background: CanvasText;
		}
	}
</style>
