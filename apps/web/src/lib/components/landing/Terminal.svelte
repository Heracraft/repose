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
	const STEP = 420;

	// Boot sequence from index.md (session.ts) followed by the attached agent
	// pane and real tmux status bar so visitors immediately see what repose
	// actually delivers: isolated VM, snapshot safety, bypass permissions,
	// passing tests, and forwarded localhost ports.
	type Line = { kind: 'cmd' | 'ok' | 'dim' | 'agent' | 'sep'; mark: string; text: string };
	const bootLines: Line[] = [
		...SESSION.commands.map((text): Line => ({ kind: 'cmd', mark: '$ ', text })),
		...SESSION.output
			.filter((text) => !text.startsWith('Logins copied:'))
			.map((text): Line => {
				const done = text.startsWith('✓ ');
				return {
					kind: done ? 'ok' : 'dim',
					mark: done ? '✓ ' : '',
					text: done ? text.slice(2) : text
				};
			})
	];
	const agentLines: Line[] = [
		{ kind: 'sep', mark: '', text: '' },
		{ kind: 'agent', mark: '▸ ', text: 'claude  bypass permissions on · snapshot 21:25 saved' },
		{ kind: 'ok', mark: '✓ ', text: 'Wrote src/billing.test.ts (6 tests passing, 0.8s)' },
		{ kind: 'ok', mark: '✓ ', text: 'Committed 4f9c2a "test(billing): cover proration"' }
	];
	const lines: Line[] = [...bootLines, ...agentLines];
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
				// The first two lines arrive together: on its own, line one
				// is `cd` into a folder, and a visitor who glances and
				// scrolls would read a directory change as the whole idea.
				// Together they open on the command this page is about
				// (I-1446; the release conductor read the opening frame at
				// 300 ms before the push).
				count = 2;
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
		<span class="env-pill">Ubuntu 24.04 · isolated VM</span>
	</div>
	<div class="body" aria-live="off">
		{#each lines as line, i (i)}
			{#if line.kind === 'sep'}
				<div class="ln sep" class:on={i < count} aria-hidden="true"></div>
			{:else}
				<p
					class="ln"
					class:on={i < count}
					class:cmd={line.kind === 'cmd'}
					class:agent={line.kind === 'agent'}
				>
					<span
						class={line.kind === 'cmd'
							? 'prompt-mark'
							: line.kind === 'ok'
								? 'ok'
								: line.kind === 'agent'
									? 'agent-mark'
									: 'dim'}
						aria-hidden={line.kind === 'cmd'}>{line.mark}</span
					><span class:dim={line.kind === 'dim'} class:agent-text={line.kind === 'agent'}
						>{line.text}</span
					>{#if i === total - 1}<span class="caret" aria-hidden="true"></span>{/if}
				</p>
			{/if}
		{/each}
	</div>
	<div class="tmux-bar" aria-hidden="true">
		<span class="tmux-left">0:shell- &nbsp;<b>1:claude*</b></span>
		<span class="tmux-right">⇄ 5173 │ snap 21:25 │ your-project</span>
	</div>
</div>

<style>
	.term {
		display: flex;
		flex-direction: column;
		min-width: 0;
		overflow: hidden;
		border-radius: var(--hk-radius-lg);
		background: #0d0e11;
		box-shadow:
			0 0 0 1px #272932,
			inset 0 1px 0 rgb(255 255 255 / 0.07),
			0 28px 60px -28px rgb(0 0 0 / 0.45);
		color: #f0f0f3;
	}
	@media (prefers-color-scheme: dark) {
		:global(:root:not(.light):not([data-theme='light'])) .term {
			background: #131419;
			box-shadow:
				0 0 0 1px #2e303b,
				inset 0 1px 0 rgb(255 255 255 / 0.08),
				0 28px 64px -24px rgb(0 0 0 / 0.72);
		}
	}
	:global(:root[data-theme='dark']) .term,
	:global(:root.dark) .term {
		background: #131419;
		box-shadow:
			0 0 0 1px #2e303b,
			inset 0 1px 0 rgb(255 255 255 / 0.08),
			0 28px 64px -24px rgb(0 0 0 / 0.72);
	}
	.bar {
		display: flex;
		flex: none;
		align-items: center;
		justify-content: space-between;
		gap: 0.75rem;
		height: 2.375rem;
		padding: 0 1rem;
		border-bottom: 1px solid #23252e;
		background: #16171d;
	}
	.title {
		min-width: 0;
		overflow: hidden;
		font-family: var(--hk-font-mono);
		font-size: 0.75rem;
		color: #a4a6b3;
		white-space: nowrap;
		text-overflow: ellipsis;
	}
	.env-pill {
		flex: none;
		font-family: var(--hk-font-mono);
		font-size: 0.6875rem;
		color: #93a7d0;
		white-space: nowrap;
	}
	.body {
		padding: 1rem 1.125rem;
		font-family: var(--hk-font-mono);
		font-size: 0.75rem;
		line-height: 1.375rem;
	}
	/* One line each; a long one wraps and hangs two columns in. */
	.ln {
		margin: 0;
		padding-left: 2ch;
		text-indent: -2ch;
		white-space: pre-wrap;
		overflow-wrap: anywhere;
	}
	.ln.sep {
		height: 1px;
		margin: 0.625rem 0;
		padding: 0;
		text-indent: 0;
		background: #23252e;
	}
	.cmd {
		color: #f4f4f6;
	}
	.prompt-mark {
		color: #7c8090;
		user-select: none;
	}
	.dim {
		color: #a9acb8;
	}
	.ok {
		color: #4ade80;
	}
	.agent-mark {
		color: #93a7d0;
	}
	.agent-text {
		color: #d6d9e4;
	}
	.caret {
		display: inline-block;
		margin-left: 0.75ch;
		width: 7px;
		height: 13px;
		vertical-align: -2px;
		background: #e4e4e7;
	}
	.tmux-bar {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 0.75rem;
		padding: 0.4375rem 1.125rem;
		border-top: 1px solid #23252e;
		background: #171920;
		font-family: var(--hk-font-mono);
		font-size: 0.6875rem;
		line-height: 1.125rem;
		color: #9aa0b0;
		white-space: nowrap;
		overflow: hidden;
	}
	.tmux-left b {
		font-weight: 500;
		color: #e8eaef;
	}
	.tmux-right {
		overflow: hidden;
		text-overflow: ellipsis;
		color: #93a7d0;
	}
	@media (max-width: 639.98px) {
		.body {
			padding: 0.875rem 0.875rem;
			font-size: 0.6875rem;
			line-height: 1.25rem;
		}
		.env-pill {
			display: none;
		}
		.tmux-bar {
			padding: 0.375rem 0.875rem;
			font-size: 0.625rem;
		}
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
