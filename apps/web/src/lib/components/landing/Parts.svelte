<!--
  Parts: the hero's picture since DECISIONS I-1167. The parts of an
  always-on agent on repose, drawn as one object on the page's own tone:
  the machine in the middle, with the agent, your setup, a port, a
  scheduled job, the secrets and the browser inside it; two ways in from
  outside wired into its left wall (your laptop's message into the
  agent, a webhook delivery into the port); the line to you out of its
  right wall (a question on your phone). Each wire carries the command
  or the host it runs through as a chip, as the wreck picture's wires
  do, and ends on a dot at each end. Flat, in the pictures' kit, and
  still: it has no loop, so no pause button and no entry in LANDING.md's
  "Motion".

  Every string is from the public docs: the webhook's host and the
  github route (webhooks.md), the timer (machine.md "Scheduled jobs"),
  the secrets' names (secrets.md), the notification title and the
  question (notifications.md, with the pictures' checkout name). From
  1200px the machine's rows and the wires beside them share one grid
  (subgrid), so a wire runs level into the row it lands on; below, the
  picture is one 26rem column: the two ways in side by side over the
  machine, the line out under it, and each wire runs down.
-->
<script lang="ts">
	import WinHead from './WinHead.svelte';
	import { agentMarks, type Mark } from '$lib/components/illustrations/marks';
</script>

{#snippet mark(m: Mark)}
	<svg viewBox="0 0 24 24" width="20" height="20" fill="currentColor" aria-hidden="true"
		><title>{m.name}</title>{#each m.paths as d (d)}<path
				{d}
				fill-rule={m.evenodd ? 'evenodd' : 'nonzero'}
				clip-rule={m.evenodd ? 'evenodd' : 'nonzero'}
			/>{/each}</svg
	>
{/snippet}

{#snippet glyph(
	kind: 'folder' | 'port' | 'clock' | 'key' | 'browser' | 'laptop' | 'globe' | 'phone'
)}
	<svg
		class="gl"
		viewBox="0 0 16 16"
		width="16"
		height="16"
		fill="none"
		stroke="currentColor"
		stroke-width="1.25"
		stroke-linejoin="round"
		stroke-linecap="round"
		aria-hidden="true"
	>
		{#if kind === 'folder'}
			<path d="M2 4.5V12.5h12V5.5H7.5L6 4H2z" />
		{:else if kind === 'port'}
			<rect x="2.5" y="4.5" width="11" height="7" rx="1" /><path
				d="M5.5 8h5M5.5 6.5v3M10.5 6.5v3"
			/>
		{:else if kind === 'clock'}
			<circle cx="8" cy="8" r="5.5" /><path d="M8 5v3l2 1.5" />
		{:else if kind === 'key'}
			<circle cx="5.5" cy="8" r="2.75" /><path d="M8.25 8H14M12 8v2M14 8v1.5" />
		{:else if kind === 'browser'}
			<rect x="2" y="3" width="12" height="10" rx="1" /><path d="M2 6h12" />
		{:else if kind === 'laptop'}
			<rect x="3" y="3.5" width="10" height="7" rx="0.75" /><path d="M1.5 12.5h13" />
		{:else if kind === 'globe'}
			<circle cx="8" cy="8" r="5.5" /><path
				d="M2.5 8h11M8 2.5c-1.8 1.6-2.6 3.4-2.6 5.5s.8 3.9 2.6 5.5M8 2.5c1.8 1.6 2.6 3.4 2.6 5.5s-.8 3.9-2.6 5.5"
			/>
		{:else}
			<rect x="4.5" y="1.5" width="7" height="13" rx="1.25" /><path d="M7 12.25h2" />
		{/if}
	</svg>
{/snippet}

<div
	class="parts"
	role="img"
	aria-label="The parts of an always-on agent on repose. A cloud machine runs your agent, any of Claude Code, Codex, opencode, Gemini CLI and pi, beside your project, a port, a scheduled job, your secrets and a browser. From your laptop, repose say sends the agent a message; a webhook from a provider such as Stripe, GitHub or Clerk comes through hooks.repose.run to port 3000 on the machine. When the agent needs you, repose-ask sends a question to your phone, such as Drop the legacy sessions table?, and you answer yes or no."
>
	<div class="grid" aria-hidden="true">
		<!-- The ways in, from outside the machine. -->
		<div class="win src src-say">
			<div class="phead">{@render glyph('laptop')}<span class="who">your laptop</span></div>
			<div class="sbody mono">your-project/</div>
		</div>
		<span class="wire w-say"
			><i class="d"></i><span class="pic-chip">repose say</span><i class="d"></i></span
		>
		<div class="win src src-hook">
			<div class="phead">{@render glyph('globe')}<span class="who">a webhook</span></div>
			<div class="sbody">Stripe, GitHub, Clerk</div>
		</div>
		<span class="wire w-hook"
			><i class="d"></i><span class="pic-chip">hooks.repose.run</span><i class="d"></i></span
		>

		<!-- The machine. -->
		<div class="win machine">
			<WinHead who="machine" dot />
			<div class="row agent">
				<span class="lab">agent</span>
				<span class="marks">
					{#each agentMarks as m (m.name)}{@render mark(m)}{/each}
				</span>
			</div>
			<div class="row">
				{@render glyph('folder')}<span class="mono strong">your-project/</span><span class="note"
					>code, tools, logins</span
				>
			</div>
			<div class="row">
				{@render glyph('port')}<span class="mono strong">:3000</span><span class="mono note"
					>/api/webhooks/github</span
				>
			</div>
			<div class="row">
				{@render glyph('clock')}<span class="mono strong">backup.timer</span><span class="mono note"
					>OnCalendar=daily</span
				>
			</div>
			<div class="row">
				{@render glyph('key')}<span class="mono strong">STRIPE_SECRET_KEY</span><span
					class="mono note">OPENAI_API_KEY</span
				>
			</div>
			<div class="row">
				{@render glyph('browser')}<span class="mono strong">Chromium</span><span class="note"
					>the agent's browser</span
				>
			</div>
		</div>

		<!-- The line out, to you. -->
		<span class="wire w-ask out"
			><i class="d"></i><span class="pic-chip">repose-ask</span><i class="d"></i></span
		>
		<div class="win phone">
			<div class="phead">{@render glyph('phone')}<span class="who">your phone</span></div>
			<div class="pbody">
				<span class="title">your-project: claude asks</span>
				<span class="q">Drop the legacy sessions table?</span>
				<span class="btns"
					><span class="pic-chip lit">yes</span><span class="pic-chip">no</span></span
				>
			</div>
		</div>
	</div>
</div>

<style>
	.parts {
		--wire: var(--rule-strong);
		--row-h: 36px;
		color: var(--pic-ink);
		font-family: var(--font-sans);
		font-size: 12px;
		line-height: 16px;
	}

	/* Below 1200px: the two ways in side by side, each wire running down
	   onto the machine's head; the line out runs down from its foot onto
	   your phone. */
	.grid {
		display: grid;
		grid-template-columns: repeat(2, minmax(0, 1fr));
		column-gap: 12px;
		max-width: 26rem;
		margin-inline: auto;
		grid-template-areas:
			'src-say src-hook'
			'w-say w-hook'
			'machine machine'
			'w-ask w-ask'
			'phone phone';
		justify-items: center;
	}
	.src-say {
		grid-area: src-say;
	}
	.src-hook {
		grid-area: src-hook;
	}
	.w-say {
		grid-area: w-say;
	}
	.w-hook {
		grid-area: w-hook;
	}
	.machine {
		grid-area: machine;
	}
	.w-ask {
		grid-area: w-ask;
	}
	.phone {
		grid-area: phone;
	}

	.src {
		width: 100%;
	}
	.sbody {
		padding: 8px 12px 10px;
		color: var(--pic-dim);
		white-space: nowrap;
		overflow: hidden;
	}
	.gl {
		flex: none;
		color: var(--pic-dim);
	}

	.win {
		display: flex;
		flex-direction: column;
		width: 100%;
		min-width: 0;
		border: 1px solid var(--rule);
		border-radius: 6px;
		background: var(--surface);
	}
	.machine {
		max-width: 26rem;
	}
	.row {
		display: flex;
		align-items: center;
		gap: 8px;
		min-width: 0;
		min-height: var(--row-h);
		padding: 0 12px;
		border-top: 1px solid var(--rule);
	}
	.agent {
		border-top: 0;
	}
	.mono {
		font-family: var(--font-mono);
		white-space: nowrap;
	}
	.strong {
		font-weight: 500;
	}
	.note {
		min-width: 0;
		margin-left: auto;
		overflow: hidden;
		color: var(--pic-dim);
		white-space: nowrap;
	}
	/* The one lit part: the agent, any of the five. */
	.agent {
		box-shadow: inset 2px 0 0 var(--pic-accent);
	}
	.agent .lab {
		font-weight: 600;
		color: var(--pic-accent);
	}
	.marks {
		display: flex;
		gap: 12px;
		margin-left: auto;
	}
	.marks > :global(svg) {
		flex: none;
	}

	.phone {
		max-width: 15rem;
	}
	.phead {
		display: flex;
		align-items: center;
		gap: 8px;
		height: var(--shot-head, 28px);
		padding: 0 10px;
		border-bottom: 1px solid var(--rule);
		border-radius: 5px 5px 0 0;
		background: var(--sunken);
	}
	.phead .gl {
		color: var(--pic-ink);
	}
	.who {
		font-weight: 600;
	}
	.pbody {
		display: flex;
		flex-direction: column;
		gap: 8px;
		padding: 10px 12px 12px;
	}
	.title {
		font-weight: 600;
	}
	.q {
		margin-top: -4px;
	}
	.btns {
		display: flex;
		gap: 6px;
	}
	.pic-chip.lit {
		background: var(--pic-accent);
		color: var(--surface);
	}

	/* A wire: a hairline with a dot at each end, the dots on the two
	   things it joins, and its chip in the middle. Stacked, it runs down
	   36px between them. */
	.wire {
		position: relative;
		display: grid;
		place-items: center;
		width: 100%;
		height: 56px;
	}
	.wire::before {
		content: '';
		position: absolute;
		top: 3px;
		bottom: 3px;
		left: 50%;
		width: 1px;
		background: var(--wire);
	}
	.wire > .pic-chip {
		position: relative;
		margin-inline: 14px;
	}
	.d {
		position: absolute;
		left: calc(50% - 3px);
		width: 6px;
		height: 6px;
		border: 1px solid var(--wire);
		border-radius: 50%;
		background: var(--surface);
	}
	.d:first-child {
		top: 0;
	}
	.d:last-child {
		bottom: 0;
	}
	/* The line out starts on the agent's side with the accent's dot. */
	.out > .d:first-child {
		border-color: var(--pic-accent);
		background: var(--pic-accent);
	}

	/* From 1200px: the ways in, the wires, the machine, the line out and
	   your phone across one row, on a grid whose rows the machine shares
	   (subgrid), so each wire stands level with the row it lands on. */
	@media (min-width: 1200px) {
		.parts {
			--row-h: 38px;
			font-size: 13px;
		}
		.grid {
			/* The two outer columns share one width, so the machine stands
			   on the page's centre line. */
			grid-template-columns: 12.5rem minmax(10.5rem, 1fr) minmax(0, 23rem) minmax(
					10.5rem,
					1fr
				) 12.5rem;
			grid-template-rows: repeat(7, auto);
			grid-template-areas: none;
			column-gap: 0;
			max-width: none;
			justify-items: stretch;
			align-items: center;
		}
		/* The side windows hang from their rows: each head stands level
		   with the row its wire lands on. */
		.src,
		.phone {
			align-self: start;
			margin-top: calc((var(--row-h) - var(--shot-head, 28px)) / 2);
		}
		.src-say {
			grid-area: 2 / 1 / span 2;
		}
		.w-say {
			grid-area: 2 / 2;
		}
		.src-hook {
			grid-area: 4 / 1 / span 2;
		}
		.w-hook {
			grid-area: 4 / 2;
		}
		.machine {
			display: grid;
			grid-area: 1 / 3 / span 7;
			grid-template-rows: subgrid;
			max-width: none;
		}
		.w-ask {
			grid-area: 2 / 4;
		}
		.phone {
			grid-area: 2 / 5 / span 6;
			width: 100%;
		}
		.wire {
			height: 24px;
		}
		.wire::before {
			inset: 50% 3px auto;
			width: auto;
			height: 1px;
		}
		.d:first-child,
		.d:last-child {
			top: calc(50% - 2.5px);
			bottom: auto;
		}
		.d:first-child {
			left: 0;
		}
		.d:last-child {
			left: auto;
			right: 0;
		}
	}
</style>
