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

  Neither direction of the RFB cut-text channel is bridged to the
  watcher's own machine. The guest's viewer page bridges both
  (nix/guest/base/desktop/viewer/viewer.js), and a guest the tenant has
  root on is the one thing on this path repose does not trust: it would
  choose what a watcher pastes next, and read what they copied. The word
  for that channel appears nowhere in this file on purpose, so a grep
  over apps/web/src finds nothing here (I-1216, docs/features/browser.md).

  Every connect, including a reconnect, goes through the authenticated
  mint, because the mint is the only place a frozen machine wakes. So a
  close the api explains as idle, four hours or 2 GB is not reconnected
  to: it offers Resume view, one click, one mint.
-->
<script lang="ts">
	import { onMount } from 'svelte';
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

	let screen: HTMLDivElement | undefined;
	let rfb: { disconnect(): void; focus(): void } | undefined;
	let sock: WebSocket | undefined;
	/** The close frame's reason word, read before noVNC sees the close. */
	let reason: string | undefined;
	let retries = 0;
	let timer: ReturnType<typeof setTimeout> | undefined;
	let dropped = false;
	/** A stream is up, so its end is still to be answered, once. */
	let live = false;

	/**
	 * Drops the stream and the socket; both are replaced by a reconnect.
	 * noVNC takes its own canvas out of the box on the way down, so this
	 * leaves the box to Svelte and touches no DOM of its own.
	 */
	function teardown() {
		clearTimeout(timer);
		timer = undefined;
		live = false;
		rfb?.disconnect();
		rfb = undefined;
		sock = undefined;
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
		try {
			ticket = await mintBrowserView(project.id);
		} catch (err) {
			stop(errorText(err, 'The view could not be opened.'));
			return;
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

<section class="hk-section" aria-labelledby="browser-title">
	<!-- The tab says Browser; the heading names the region for a screen reader. -->
	<h2 id="browser-title" class="hk-sr">Browser</h2>
	<div class="frame">
		<!-- noVNC adds its canvas here and sizes it to this box. -->
		<div class="screen" bind:this={screen} data-testid="view-screen"></div>
		{#if phase !== 'watching'}
			<div class="over">
				<p class="line" role="status">{phase === 'connecting' ? 'Connecting…' : note}</p>
				{#if phase === 'stopped'}
					<button type="button" class="resume" onclick={() => void connect()}>Resume view</button>
				{/if}
			</div>
		{/if}
	</div>
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
		border-radius: var(--hk-radius);
		aspect-ratio: 16 / 10;
		max-height: calc(100vh - 18rem);
		min-height: 15rem;
		overflow: hidden;
	}
	.screen {
		position: absolute;
		inset: 0;
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
</style>
