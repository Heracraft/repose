<!--
  The machine's browser, watched from the dashboard (DECISIONS I-1216).
  noVNC is drawn here, on the dashboard's own origin: the guest's viewer
  page cannot be framed, since no repose page is (`frame-ancestors:
  none`, svelte.config.js), and the stream is a WebSocket to the api,
  which pipes it to the edge's relay and the guest's SSH.

  The stream arrives past the RFB SecurityResult: the relay holds the
  guest's per-boot password for one handshake and offers this page
  security type None, so nothing here has a credential to send. noVNC's
  `credentialsrequired` and `securityfailure` handlers, which the guest's
  own viewer page has, are therefore not ported.

  The RFB cut-text channel is carried in both directions, but only on a
  click, and it never touches the watcher's own clipboard on its own
  (I-1216). The guest's viewer page bridges it automatically both ways
  (nix/guest/base/desktop/viewer/viewer.js): it writes guest cut-text
  straight into the watcher's clipboard, and pushes the watcher's
  clipboard into the guest on `focus` and `visibilitychange`. That is
  defensible over an SSH forward the user started themselves, and not
  here, because a guest the tenant has root on is the one thing on this
  path repose does not trust: automatic, it would read whatever was on
  the watcher's clipboard the moment the tab was focused, and choose what
  their next paste anywhere else would be. So this page is the panel
  instead: text reaches the machine when Send is pressed, and what the
  machine copied sits in the page until the watcher presses Copy. Both
  are tenant text, so neither is ever logged
  (docs/features/browser.md, docs/ops/OBSERVABILITY.md).

  Every connect, including a reconnect, goes through the authenticated
  mint, because the mint is the only place a frozen machine wakes. So a
  close the api explains as idle, four hours or 2 GB is not reconnected
  to: it offers Resume view, one click, one mint.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import CopyButton from '$lib/hk/CopyButton.svelte';
	import type RFBClass from '@novnc/novnc';
	import { mintBrowserView, viewStreamUrl } from '$lib/api/client';
	import { errorText } from '$lib/api/errors';
	import { VIEW_RETRIES, viewEnded } from '$lib/browserView';
	import type { Project } from '$lib/api/types';

	/** The socket a connect opens, so a test can close one without a server. */
	type SocketOpener = (url: string) => WebSocket;

	let {
		project,
		openSocket = (url: string) => new WebSocket(url)
	}: { project: Project; openSocket?: SocketOpener } = $props();

	/** connecting: a mint or a handshake is in flight. watching: frames. */
	type Phase = 'connecting' | 'watching' | 'stopped';
	let phase = $state<Phase>('connecting');
	/** Why it stopped, in one line; undefined while frames are coming. */
	let note = $state<string | undefined>(undefined);
	/** The mint in flight is waking a frozen machine, which takes seconds. */
	let waking = $state(false);

	let screen: HTMLDivElement | undefined;
	let rfb: RFBClass | undefined;
	let sock: WebSocket | undefined;
	/** The close frame's reason word, read before noVNC sees the close. */
	let reason: string | undefined;
	let retries = 0;
	let timer: ReturnType<typeof setTimeout> | undefined;
	let dropped = false;
	/** A stream is up, so its end is still to be answered, once. */
	let live = false;
	/** The panel's halves: what Send gives the machine, and what the
	    machine's last copy left in the page. Neither is logged, and
	    `copied` is only ever filled by the machine's own cut-text. */
	let toSend = $state('');
	let copied = $state('');
	let sent = $state(false);
	let sentTimer: ReturnType<typeof setTimeout> | undefined;

	/**
	 * Drops the stream and the socket; both are replaced by a reconnect.
	 * noVNC takes its own canvas out of the box on the way down, so this
	 * leaves the box to Svelte and touches no DOM of its own.
	 */
	function teardown() {
		clearTimeout(timer);
		timer = undefined;
		live = false;
		clearTimeout(sentTimer);
		sent = false;
		rfb?.disconnect();
		rfb = undefined;
		sock = undefined;
	}

	/**
	 * Hands the machine's clipboard what is in the field. A gesture only:
	 * nothing reads the watcher's own clipboard, here or anywhere in this
	 * file. The face says Sent for a moment, because a remote paste shows
	 * nothing on screen until the machine is asked to paste it.
	 */
	function send() {
		if (!toSend || !rfb) return;
		pasteToMachine(toSend);
		sent = true;
		clearTimeout(sentTimer);
		sentTimer = setTimeout(() => (sent = false), 1200);
	}

	/** The one way text reaches the machine's clipboard: Send, or the
	 * watcher's own paste on the view. */
	function pasteToMachine(text: string) {
		rfb?.clipboardPasteFrom(text);
	}

	let frame: HTMLDivElement | undefined;
	let full = $state(false);
	/** Full screen on the view alone (ENG-241); Esc, or the button, leaves. */
	async function toggleFull() {
		if (document.fullscreenElement) await document.exitFullscreen();
		else await frame?.requestFullscreen();
	}

	/**
	 * Ctrl+V or Cmd+V on the view pastes what the watcher pasted (ENG-241).
	 * The paste is the watcher's own gesture: the browser hands the page
	 * the text only for it, so this reads no clipboard on its own (I-1216).
	 * The key itself is held back from noVNC, which would send it before
	 * the text arrives and paste what the machine had; once the text is in
	 * the machine's clipboard, Ctrl+V is sent there, the machine's own key.
	 */
	function onKeydownCapture(e: KeyboardEvent) {
		if ((e.ctrlKey || e.metaKey) && !e.altKey && e.key.toLowerCase() === 'v') {
			e.stopPropagation();
		}
	}
	function onPaste(e: ClipboardEvent) {
		const text = e.clipboardData?.getData('text/plain') ?? '';
		if (!text || !rfb) return;
		e.preventDefault();
		pasteToMachine(text);
		rfb.sendKey(0xffe3, 'ControlLeft', true);
		rfb.sendKey(0x76, 'KeyV', true);
		rfb.sendKey(0x76, 'KeyV', false);
		rfb.sendKey(0xffe3, 'ControlLeft', false);
	}

	/** Stops with a line, and the Resume view button beside it. */
	function stop(line: string) {
		teardown();
		phase = 'stopped';
		note = line;
	}

	/**
	 * A stream that ended: reconnect while a retry is left, or stop with
	 * the line the close reason gives.
	 */
	function ended(reason: string | undefined) {
		if (dropped || !live) return;
		live = false;
		const end = viewEnded(reason);
		if (!end.retry) {
			stop(end.note ?? 'The view stopped.');
			return;
		}
		const wait = VIEW_RETRIES[retries];
		if (wait === undefined) {
			stop('The view could not be reached.');
			return;
		}
		retries += 1;
		teardown();
		phase = 'connecting';
		note = undefined;
		timer = setTimeout(() => void connect(), wait);
	}

	/** A mint, then a stream. Called by the tab, a retry and Resume view. */
	async function connect() {
		if (dropped) return;
		teardown();
		phase = 'connecting';
		note = undefined;
		reason = undefined;

		// noVNC is loaded before the socket exists: on the first connect its
		// chunk is a fetch, and a socket that failed while that was in flight
		// cannot be attached to ("Cannot use already closed WebSocket").
		let RFB;
		try {
			({ default: RFB } = await import('@novnc/novnc'));
		} catch {
			stop('The view could not be opened.');
			return;
		}
		if (dropped) return;

		let ticket;
		waking = !!project.resting;
		try {
			ticket = await mintBrowserView(project.id);
		} catch (err) {
			stop(errorText(err, 'The view could not be opened.'));
			return;
		} finally {
			waking = false;
		}
		if (dropped) return;
		if (ticket.resting) {
			stop(`${project.slug} is frozen, so its screen is off.`);
			return;
		}
		const url = viewStreamUrl(ticket.stream_path);
		if (!url) {
			stop('The view could not be opened.');
			return;
		}

		const ws = openSocket(url);
		sock = ws;
		live = true;
		// Before noVNC attaches its own handler, so the api's reason word is
		// read first and the end below knows which close this was.
		ws.addEventListener('close', (e: CloseEvent) => {
			if (ws !== sock) return;
			reason = e.reason;
			// A close before noVNC attached (its chunk, a refused upgrade)
			// fires no disconnect of its own.
			if (!rfb) ended(e.reason);
		});

		let r;
		try {
			r = new RFB(screen!, ws, { credentials: {}, shared: true });
		} catch {
			// The socket was gone before the handshake began.
			ended(reason);
			return;
		}
		rfb = r;
		// The remote screen takes this box's size (SetDesktopSize, which
		// Xvnc honours) and whatever is left over is scaled; quality 9 and
		// compression 1 are the guest viewer's own, for a screen of text.
		r.scaleViewport = true;
		r.resizeSession = true;
		r.clipViewport = false;
		r.qualityLevel = 9;
		r.compressionLevel = 1;
		r.showDotCursor = false;
		r.focusOnClick = true;
		// The letterbox around a screen that does not fill the box is the
		// pane's own colour; noVNC assigns this to the element's style, so
		// a token reference resolves there.
		r.background = 'var(--hk-code-bg)';
		r.addEventListener('connect', () => {
			retries = 0;
			phase = 'watching';
			note = undefined;
		});
		// The machine copied something: it goes in the page and no further.
		// navigator.clipboard is reached only by the Copy button below.
		r.addEventListener('clipboard', (e: CustomEvent<{ text: string }>) => {
			if (dropped || r !== rfb) return;
			copied = e.detail.text;
		});
		r.addEventListener('disconnect', () => {
			if (dropped || r !== rfb) return;
			// This instance is gone: teardown must not disconnect it again.
			rfb = undefined;
			ended(reason);
		});
	}

	onMount(() => {
		void connect();
		return () => {
			dropped = true;
			teardown();
		};
	});
</script>

<svelte:document onfullscreenchange={() => (full = document.fullscreenElement === frame)} />

<section class="hk-section" aria-labelledby="browser-title">
	<!-- The tab says Browser; the heading names the region for a screen reader. -->
	<h2 id="browser-title" class="hk-sr">Browser</h2>
	<div
		class="frame"
		bind:this={frame}
		onkeydowncapture={onKeydownCapture}
		onpaste={onPaste}
		data-testid="view-frame"
	>
		<!-- noVNC adds its canvas here and sizes it to this box. -->
		<div class="screen" bind:this={screen} data-testid="view-screen"></div>
		{#if phase === 'watching'}
			<button type="button" class="fullbtn" onclick={() => void toggleFull()}
				>{full ? 'Exit full screen' : 'Full screen'}</button
			>
		{/if}
		{#if phase !== 'watching'}
			<div class="over">
				<p class="line" role="status">
					{phase === 'connecting' ? (waking ? `Waking ${project.slug}…` : 'Connecting…') : note}
				</p>
				{#if phase === 'stopped'}
					<button type="button" class="resume" onclick={() => void connect()}>Resume view</button>
				{/if}
			</div>
		{/if}
	</div>
	{#if phase === 'watching'}
		<div class="clip">
			<div class="half">
				<label class="hk-field-label" for="view-paste">Paste into the machine</label>
				<div class="row">
					<textarea
						id="view-paste"
						class="hk-textarea clipfield"
						rows="2"
						bind:value={toSend}
						data-testid="view-paste"
					></textarea>
					<button
						type="button"
						class="hk-btn-secondary hk-btn-sm"
						aria-disabled={toSend ? undefined : 'true'}
						onclick={send}>{sent ? 'Sent' : 'Send'}</button
					>
				</div>
			</div>
			{#if copied}
				<div class="half">
					<span class="hk-field-label" id="view-copied-label">Copied in the machine</span>
					<div class="row">
						<output class="clipout" aria-labelledby="view-copied-label" data-testid="view-copied"
							>{copied}</output
						>
						<CopyButton text={() => copied} size="sm" ariaLabel="Copy what the machine copied" />
					</div>
				</div>
			{/if}
		</div>
	{/if}
</section>

<style>
	/* The screen is a pane of the code panes' near-black, the one surface
	   the design language already keeps dark in both schemes, so the
	   picture arrives on the colour it will have and the empty states
	   read on it. */
	.frame {
		position: relative;
		display: grid;
		background: var(--hk-code-bg);
		border: 1px solid var(--hk-code-border);
		/* A screen's corners, not a card's (ENG-241). */
		border-radius: var(--hk-radius-sm);
		aspect-ratio: 16 / 10;
		max-height: calc(100vh - 18rem);
		min-height: 15rem;
		overflow: hidden;
	}
	.screen {
		position: absolute;
		inset: 0;
	}
	.frame:fullscreen {
		max-height: none;
		aspect-ratio: auto;
		border: 0;
		border-radius: 0;
	}
	/* Over the picture's corner, in the pane's own ink. */
	.fullbtn {
		position: absolute;
		top: 0.5rem;
		right: 0.5rem;
		z-index: 1;
		height: 1.75rem;
		padding: 0 0.5rem;
		border: 1px solid var(--hk-code-border);
		border-radius: var(--hk-radius-sm);
		background: var(--hk-code-bg);
		color: var(--hk-code-text);
		font: inherit;
		font-size: 0.8125rem;
		opacity: 0.85;
	}
	.fullbtn:hover,
	.fullbtn:focus-visible {
		opacity: 1;
	}
	/* The state sits over the pane, centred, so the picture never moves
	   when it arrives or goes. */
	.over {
		position: relative;
		display: flex;
		flex-direction: column;
		align-items: center;
		justify-content: center;
		gap: 0.875rem;
		padding: 1.5rem;
		text-align: center;
	}
	.line {
		margin: 0;
		font-size: 0.875rem;
		color: var(--hk-code-muted);
	}
	/* hk-btn-secondary's shape on the dark pane, in the pane's own ink. */
	.resume {
		display: inline-flex;
		align-items: center;
		height: 1.875rem;
		padding: 0 0.625rem;
		border: 1px solid var(--hk-code-border);
		border-radius: var(--hk-radius-7);
		background: transparent;
		color: var(--hk-code-text);
		font: inherit;
		font-size: 0.8125rem;
		font-weight: 500;
		line-height: 1;
		cursor: pointer;
		transition:
			background-color var(--hk-dur-fast) var(--hk-ease),
			border-color var(--hk-dur-fast) var(--hk-ease);
	}
	.resume:hover {
		background: var(--hk-code-border);
	}
	@media (pointer: coarse) {
		.resume {
			height: 2.75rem;
			padding: 0 0.875rem;
		}
	}
	/* The clipboard panel under the screen: one column on a phone, two
	   from 768px. Both halves are controls a thumb hits, so the field and
	   the output keep 44px on a coarse pointer, as .resume above does. */
	.clip {
		display: grid;
		gap: 0.75rem;
		margin-top: 0.75rem;
	}
	.half {
		display: flex;
		flex-direction: column;
		gap: 0.375rem;
		min-width: 0;
	}
	.row {
		display: flex;
		align-items: flex-start;
		gap: 0.5rem;
	}
	.clipfield {
		flex: 1 1 auto;
		min-width: 0;
		resize: vertical;
	}
	/* The page's own surface, not --hk-code-bg: that pane is dark in both
	   themes, and this text is not code on a pane, it is what the machine
	   copied. On the dark pane in light mode it was dark on dark. */
	.clipout {
		flex: 1 1 auto;
		min-width: 0;
		min-height: 3.5rem;
		padding: 0.5rem 0.625rem;
		border: 1px solid var(--hk-border);
		border-radius: var(--hk-radius-sm);
		background: var(--hk-surface-2);
		color: var(--hk-text);
		font-family: var(--hk-font-mono);
		font-size: 0.8125rem;
		line-height: 1.25rem;
		white-space: pre-wrap;
		overflow-wrap: anywhere;
		max-height: 7rem;
		overflow-y: auto;
	}
	@media (min-width: 768px) {
		.clip {
			grid-template-columns: 1fr 1fr;
		}
	}
	@media (pointer: coarse) {
		.clipfield {
			min-height: 2.75rem;
		}
	}
</style>
