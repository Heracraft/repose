<!--
  One command, your working state. Two source-control panels, drawn (no
  vendor's chrome), listing real data from 2026-09-25: the laptop checkout
  of the owner's job-alerts app (git -C .../landing/recruiting log/status)
  and the machine `repose run` creates for it. Since I-367 run syncs only
  into a machine with no commit yet, so the picture is that first run: the
  machine starts empty and every commit travels, master's 7ea43a8 with the
  branch's two, and the machine's title reads the quickstart's
  "Ready in 16s" (docs index.md, held there by OneCommand.test.ts), the
  time of a run that creates the machine (I-397, I-400). On the machine
  the edits sit under Changes exactly as on the laptop: the sync sends
  staged and unstaged work as two patches and keeps the split (I-258,
  internal/cli/sync.go); the untracked test file stays untracked.
  Dependency directories never travel (docs sync.md "What doesn't"), so
  the laptop's node_modules/ is struck and stays behind, and the machine
  shows none: the run gives it no node_modules of its own.

  Then the way back (I-668): the agent commits on the machine, and
  `git fetch repose` on the laptop brings the commit over as
  repose/fetch-timeout (docs sync.md "Getting work back"; `repose stop`
  makes the same fetch, lifecycle.md). That commit, e41b9c2, is
  illustrative: the capture has no agent commit. It touches none of the
  three changed files, so both Changes lists stay as they are.

  `animated` (set by routes/+page.svelte) plays it with anime.js: the
  laptop's commits and changed files lift off, at most two at a time, each
  in its own row's band, and travel into the machine's panel; the agent's
  commit comes back the same way. A copy is seen only inside the panel
  it lands in, sliding in through its wall (I-675), and the machine's
  rows from the last loop stay dimmed until new ones land. Without it, or under
  prefers-reduced-motion, the end state is shown still: both panels
  holding the agent's commit. A visitor who turns reduced motion on
  mid-loop gets the still frame at once. anime.js is only loaded when
  animated and motion is allowed.
-->
<script module lang="ts">
	/** The quickstart's time for a run that creates the machine (docs index.md). */
	export const READY = 'Ready in 16s';
</script>

<script lang="ts">
	import { onMount } from 'svelte';
	import { watchReducedMotion } from './inview';
	import { Picture } from './motion.svelte';
	import PauseButton from './PauseButton.svelte';
	import WinHead from './WinHead.svelte';

	let { animated = false }: { animated?: boolean } = $props();

	type Commit = { msg: string; hash: string; base?: boolean };
	type File = { name: string; dir?: string; st: 'M' | 'U' | 'I' };

	const agent: Commit = { msg: 'Retry fetchBoard once', hash: 'e41b9c2' };
	const commits: Commit[] = [
		{ msg: 'Let fetchBoard take a timeout instead of the fixed 20 seconds', hash: 'a1dd18e' },
		{ msg: 'Make the board fetch timeout a worker setting', hash: 'cc300c3' },
		{
			msg: 'Drop the helper line under the audience switch; no hand-holding in the UI',
			hash: '7ea43a8',
			base: true
		}
	];
	const envExample: File = { name: '.env.example', st: 'M' };
	const poller: File = { name: 'poller.ts', dir: 'apps/worker/src/ingest', st: 'M' };
	const test: File = { name: 'timeout.test.ts', dir: 'apps/worker/src/ingest', st: 'U' };
	const env: File = { name: '.env', st: 'I' };

	const label = 'Your laptop and your cloud machine after repose run and git fetch repose';
	const story =
		'Two source control panels side by side, your laptop and your cloud machine. On the laptop, branch fetch-timeout ' +
		'has 2 new commits, 2 modified files (.env.example and poller.ts), an untracked timeout.test.ts and a gitignored .env; ' +
		`its node_modules folder is struck out and stays behind. After repose run, ready in ${READY.replace(/\D/g, '')} seconds, the cloud machine shows ` +
		'the same branch, the same commits, the same two modified files and untracked test, and the same .env. ' +
		'An agent on the machine then commits "Retry fetchBoard once", and git fetch repose ' +
		'brings that commit to the laptop as repose/fetch-timeout.';
	const descId = 'one-command-desc';

	let pic: HTMLDivElement;
	const picture = new Picture();
	let fly: HTMLDivElement;
	let fire = $state(false);
	let fetchFire = $state(false);

	onMount(() => {
		if (!animated) return;

		let dead = false;
		let visible = false;
		let tl: { pause(): unknown; play(): unknown } | null = null;
		// Bumped on each start and stop, so an anime.js import that resolves
		// after a stop does not start a loop.
		let run = 0;

		const q = (s: string) => Array.from(pic.querySelectorAll<HTMLElement>(s));
		const arrivals = () => q('[data-to]');
		const later = () => q('.m-later, .m-grp');
		// The rows under each graph's top slot: they sit one row up until a
		// commit takes the slot.
		const below = (side: 0 | 1) =>
			Array.from(
				q('.side')[side].querySelectorAll<HTMLElement>('.graph > .commit:not(.slot), .files')
			);
		const moved = () => [
			...arrivals(),
			...later(),
			...q('.slot, .rref, .graph > .commit, .files'),
			...q('.l-stop .strike, .l-stop .stop, .l-stop .fname, .l-stop .fi'),
			...q('.hop .leg')
		];

		// Stop the loop and show the end state still: the markup's own
		// state, so every inline style anime.js wrote comes off.
		function still() {
			run++;
			picture.stop();
			tl?.pause();
			tl = null;
			visible = false;
			// eslint-disable-next-line svelte/no-dom-manipulating
			fly.replaceChildren();
			fly.style.removeProperty('clip-path');
			for (const el of moved()) el.removeAttribute('style');
			for (const el of q('.lift, .cut')) el.classList.remove('lift', 'cut');
			fire = false;
			fetchFire = false;
		}

		function start() {
			const me = ++run;
			import('animejs').then(({ createTimeline, utils }) => {
				if (dead || me !== run) return;

				// The machine's rows from the last loop, dimmed (as the hero
				// dims its rows): the panel is never an empty box.
				const dimmed = () => [...arrivals(), ...q('.m-grp, .m-head')];
				const DIM = 0.22;

				// Before the sync: the machine has no commit yet (last loop's
				// rows dimmed), the laptop's node_modules is not yet marked,
				// no agent has committed. The lone `repose run` chip sits
				// `centre` px along `axis` (right stacked, down side by
				// side), in the middle until its pair comes.
				function pre(shift: number, axis: 'x' | 'y', centre: number) {
					utils.set([...q('.ready, .fetch, .slot')], { opacity: 0 });
					utils.set(dimmed(), { opacity: DIM });
					utils.set(q('.leg.run'), { [axis]: centre });
					utils.set(q('.rref'), { opacity: 0, scale: 1 });
					utils.set([...below(0), ...below(1)], { y: -shift });
					utils.set(q('.l-stop .strike'), { scaleX: 0 });
					utils.set(q('.l-stop .stop'), { opacity: 0, scale: 0.6 });
					utils.set(q('.l-stop .fname, .l-stop .fi'), { opacity: 1 });
					for (const el of q('.lift')) el.classList.remove('lift');
					// The machine's graph line runs only between commits it has.
					for (const el of q('.side:last-child .graph > .commit')) el.classList.add('cut');
					fire = false;
					fetchFire = false;
				}

				function cycle() {
					if (dead || me !== run) return;
					if (!visible) {
						tl = null;
						return;
					}
					// .fly is an empty layer Svelte never renders into; only the
					// throwaway copies of rows live in it.
					// eslint-disable-next-line svelte/no-dom-manipulating
					fly.replaceChildren();
					const [lapSide, macSide] = q('.side');
					const lapSlot = lapSide.querySelector<HTMLElement>('.slot')!;
					const macSlot = macSide.querySelector<HTMLElement>('.slot')!;
					const sh = lapSlot.offsetHeight;
					// The chips stand in the middle of the gap: side by side in
					// a row stacked, one over the other between the panels
					// side by side. Half the fetch leg and the gap is how far
					// off the middle run stands until its pair comes, on the
					// axis the pair lines up along.
					const runLeg = q('.leg.run')[0];
					const fetchLeg = q('.leg.fetch')[0];
					const stacked = runLeg.offsetTop === fetchLeg.offsetTop;
					const axis = stacked ? 'x' : 'y';
					const centre = stacked
						? (fetchLeg.offsetLeft +
								fetchLeg.offsetWidth -
								runLeg.offsetLeft -
								runLeg.offsetWidth) /
							2
						: (fetchLeg.offsetTop +
								fetchLeg.offsetHeight -
								runLeg.offsetTop -
								runLeg.offsetHeight) /
							2;
					pre(sh, axis, centre);

					const base = pic.getBoundingClientRect();
					const rel = (r: DOMRect) => ({
						l: r.left - base.left,
						t: r.top - base.top,
						r: r.right - base.left,
						b: r.bottom - base.top
					});
					const lap = rel(lapSide.firstElementChild!.getBoundingClientRect());
					const mac = rel(macSide.firstElementChild!.getBoundingClientRect());
					const macRows = rel(macSide.querySelector('.graph')!.getBoundingClientRect()).t;
					const vertical = mac.t >= lap.b;
					const box = (x0: number, y0: number, x1: number, y1: number) =>
						`M${x0} ${y0}H${x1}V${y1}H${x0}Z`;
					// A copy is seen only inside the panel it lands in, within
					// its walls, never in the gap: a lane there cut every copy
					// mid-word. It slides in from that panel's wall: going to
					// the machine, under its head (stacked) or through its left
					// wall (side by side); coming back, into the laptop's top
					// slot, from under it or through its right wall.
					const slot = rel(lapSlot.getBoundingClientRect());
					const toMachine = box(mac.l + 1, macRows, mac.r - 1, mac.b - 1);
					const toLaptop = box(lap.l + 1, slot.t, lap.r - 1, slot.b);

					// A copy looks like the row it lands as, starts over the row it
					// leaves, and moves by the difference.
					function copy(from: HTMLElement, to: HTMLElement) {
						const a = from.getBoundingClientRect();
						const b = to.getBoundingClientRect();
						const c = to.cloneNode(true) as HTMLElement;
						for (const el of [c, ...c.querySelectorAll<HTMLElement>('[style]')])
							el.removeAttribute('style');
						c.removeAttribute('data-to');
						c.classList.add('clone', 'cut');
						// Which row it copies: tests/landing-onecommand.spec.ts.
						c.dataset.copy = from.dataset.from ?? 'agent';
						Object.assign(c.style, {
							left: `${a.left - base.left}px`,
							top: `${a.top - base.top}px`,
							width: `${a.width}px`,
							opacity: '0'
						});
						// eslint-disable-next-line svelte/no-dom-manipulating
						fly.appendChild(c);
						return {
							c,
							dx: b.left - a.left,
							dy: b.top - a.top,
							top: a.top - base.top,
							left: a.left - base.left,
							w: a.width
						};
					}

					const pairs = q('[data-from]').flatMap((from) => {
						const to = macSide.querySelector<HTMLElement>(`[data-to="${from.dataset.from}"]`);
						return to ? [{ from, to, ...copy(from, to) }] : [];
					});

					// Side by side, rows go top to bottom. Stacked, the lower rows
					// go first, so a copy passes only slots still empty.
					const n = pairs.length;
					const order = pairs.map((_, i) => (vertical ? n - 1 - i : i));

					const t0 = 500;
					const go = t0 + 260;
					// At most two copies in flight: the next leaves when the one
					// before is halfway.
					const dur = 820;
					const step = 420;
					const landed = go + step * (n - 1) + dur;

					fly.style.clipPath = `path('${toMachine}')`;
					const t = createTimeline({ autoplay: false, onComplete: () => cycle() })
						.call(() => (fire = true), t0)
						.call(() => (fire = false), t0 + 520)
						.add(
							q('.l-stop .strike'),
							{ scaleX: [0, 1], duration: 380, ease: 'outCubic' },
							go + 140
						)
						.add(
							q('.l-stop .stop'),
							{ opacity: [0, 1], scale: [0.6, 1], duration: 300, ease: 'outBack' },
							go + 420
						)
						.add(q('.m-head'), { opacity: [DIM, 1], duration: 300 }, go + 200);

					// travel: lift, glide, set down; then the real row takes over
					// under the copy, which fades off it.
					// A commit landing on the machine joins the line to its
					// neighbours that are there already.
					// Bookkeeping, not state: nothing renders from it.
					// eslint-disable-next-line svelte/prefer-svelte-reactivity
					const there = new Set<Element>();
					const join = (row: HTMLElement) => {
						if (!row.classList.contains('commit')) return;
						there.add(row);
						const prev = row.previousElementSibling;
						if (prev && there.has(prev)) prev.classList.remove('cut');
						const next = row.nextElementSibling;
						if (next && there.has(next)) row.classList.remove('cut');
					};

					type Flight = {
						from: HTMLElement;
						to: HTMLElement;
						c: HTMLElement;
						dx: number;
						dy: number;
						top: number;
						left: number;
						w: number;
					};
					// Across the gap unseen, to just outside the wall of the
					// panel it lands in, then eased in from that wall onto its
					// slot: on y stacked, on x side by side.
					const legs = (enter: number, to: number) => [
						{ to: enter, duration: dur * 0.25, ease: 'linear' },
						{ to, duration: dur * 0.75, ease: 'outCubic' }
					];
					function travel(p: Flight, at: number) {
						const fwd = p.dy > 0 || p.dx > 0;
						const motion = vertical
							? {
									x: { to: p.dx, duration: dur, ease: 'inOutCubic' },
									y: legs((fwd ? macRows - sh : slot.b) - p.top, p.dy)
								}
							: {
									x: legs((fwd ? mac.l - p.w : lap.r) - p.left, p.dx),
									y: { to: p.dy, duration: dur, ease: 'inOutCubic' }
								};
						t.call(() => p.from.classList.add('lift'), at)
							// .add with a 16ms duration, never .set: anime.js can
							// pass over a timeline's .set.
							.add(p.c, { opacity: [0, 1], duration: 16 }, at)
							.add(p.c, motion, at)
							// The real row comes up under the copy as the copy goes.
							.add(p.to, { opacity: [DIM, 1], duration: 160, ease: 'outQuad' }, at + dur)
							.call(() => join(p.to), at + dur)
							.call(() => p.from.classList.remove('lift'), at + dur)
							.add(p.c, { opacity: 0, duration: 160, ease: 'inQuad' }, at + dur);
					}

					pairs.forEach((p, i) => travel(p, go + step * order[i]));

					// A group's heading shows once its first row has landed and
					// every copy crossing its band has passed.
					for (const g of q('.m-grp')) {
						const gr = g.getBoundingClientRect();
						let at = go + 380;
						pairs.forEach((p, i) => {
							const a = p.from.getBoundingClientRect();
							const y0 = Math.min(a.top, a.top + p.dy);
							const y1 = Math.max(a.bottom, a.bottom + p.dy);
							const end = go + step * order[i] + dur;
							if (y0 < gr.bottom && y1 > gr.top) at = Math.max(at, end);
							else if (p.to.previousElementSibling === g) at = Math.max(at, end - 120);
						});
						t.add(g, { opacity: [DIM, 1], duration: 300 }, at);
					}

					t.add(q('.ready'), { opacity: [0, 1], duration: 400 }, landed + 150);

					// The way back. The agent commits on the machine: its rows
					// make room and the commit drops into the top slot.
					const agentAt = landed + 2600;
					t.add(below(1), { y: [-sh, 0], duration: 450, ease: 'inOutCubic' }, agentAt).add(
						macSlot,
						{ opacity: [0, 1], y: [-6, 0], duration: 320, ease: 'outCubic' },
						agentAt + 260
					);
					t.call(() => join(macSlot), agentAt + 260);

					// git fetch repose on the laptop: its chip arrives and fires,
					// the laptop's rows make room, and the commit comes over.
					const fetchAt = agentAt + 1300;
					const back = { from: macSlot, to: lapSlot, ...copy(macSlot, lapSlot) };
					back.c.querySelector<HTMLElement>('.rref')?.style.setProperty('opacity', '0');
					const backAt = fetchAt + 620;
					t.add(q('.fetch'), { opacity: [0, 1], duration: 300 }, fetchAt)
						.add(runLeg, { [axis]: 0, duration: 450, ease: 'inOutCubic' }, fetchAt)
						.call(() => (fetchFire = true), fetchAt + 260)
						.call(() => (fetchFire = false), fetchAt + 780)
						// The rows make room as the fetch fires, done before the
						// copy reaches the slot.
						.add(below(0), { y: [-sh, 0], duration: 450, ease: 'inOutCubic' }, fetchAt + 260)
						.call(() => (fly.style.clipPath = `path('${toLaptop}')`), backAt);
					travel(back, backAt);
					const home = backAt + dur;
					t.add(
						q('.rref'),
						{ opacity: [0, 1], scale: [0.6, 1], duration: 300, ease: 'outBack' },
						home + 80
					);

					// Rest on the end state, then clear both sides and go again:
					// the machine's rows dim and stay, both panels' rows move
					// back up a row, and run slides back to the middle.
					const out = home + 5600;
					t.add(
						[...q('.ready, .fetch'), macSlot, lapSlot],
						{ opacity: 0, duration: 450, ease: 'inQuad' },
						out
					)
						.add(dimmed(), { opacity: DIM, duration: 450, ease: 'inQuad' }, out)
						.add(q('.l-stop .strike'), { scaleX: 0, duration: 300, ease: 'inQuad' }, out + 50)
						.add(q('.l-stop .stop'), { opacity: 0, duration: 300, ease: 'inQuad' }, out + 50)
						.add(
							[...below(0), ...below(1)],
							{ y: [0, -sh], duration: 450, ease: 'inOutCubic' },
							out + 450
						)
						.add(runLeg, { [axis]: centre, duration: 450, ease: 'inOutCubic' }, out + 450)
						.add({ duration: 200 }, out + 900);

					tl = t;
					t.play();
				}

				// Start when the machine's panel (the destination) is in view; on a
				// phone it sits below the laptop's.
				picture.start(pic.querySelectorAll('.side')[1], (m) => {
					visible = m === 'play';
					if (!visible) tl?.pause();
					else if (tl) tl.play();
					else cycle();
				});
			});
		}

		const unwatch = watchReducedMotion((reduce) => {
			if (reduce) still();
			else start();
		});

		return () => {
			dead = true;
			unwatch();
			picture.stop();
			tl?.pause();
		};
	});
</script>

{#snippet branchIcon()}
	<svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true">
		<g fill="none" stroke="currentColor" stroke-width="1.3">
			<circle cx="4.5" cy="3.5" r="1.6" />
			<circle cx="4.5" cy="12.5" r="1.6" />
			<circle cx="11.5" cy="5" r="1.6" />
			<path d="M4.5 5.1v5.8M11.5 6.6c0 3-7 2-7 4.3" />
		</g>
	</svg>
{/snippet}

{#snippet fileIcon()}
	<svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true" class="fi">
		<path
			d="M4 1.5h5.2L12.5 4.8v9.7H4z M9 1.5v3.5h3.5"
			fill="none"
			stroke="currentColor"
			stroke-width="1.1"
			stroke-linejoin="round"
		/>
	</svg>
{/snippet}

{#snippet folderIcon()}
	<svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true" class="fi">
		<path
			d="M1.5 3.5h4.3l1.4 1.6h7.3v8.4h-13z"
			fill="none"
			stroke="currentColor"
			stroke-width="1.1"
			stroke-linejoin="round"
		/>
	</svg>
{/snippet}

{#snippet file(f: File, side: 'laptop' | 'machine')}
	<li
		class="fr"
		class:ign={f.st === 'I'}
		data-from={side === 'laptop' ? f.name : undefined}
		data-to={side === 'machine' ? f.name : undefined}
	>
		{@render fileIcon()}
		<span class="fname">{f.name}</span>
		{#if f.dir}<span class="dir">{f.dir}</span>{/if}
		{#if f.st !== 'I'}<span class="st st-{f.st}">{f.st}</span>{/if}
	</li>
{/snippet}

{#snippet group(title: string, count: number | null, later = false)}
	<li class="grp" class:m-grp={later}>
		<svg viewBox="0 0 10 10" width="9" height="9" aria-hidden="true" class="chev">
			<path d="M2 3.5l3 3 3-3" fill="none" stroke="currentColor" stroke-width="1.3" />
		</svg>
		<span>{title}</span>
		{#if count !== null}<span class="count">{count}</span>{/if}
	</li>
{/snippet}

{#snippet panel(where: 'laptop' | 'machine')}
	{@const m = where === 'machine'}
	<div class="win">
		{#if m}
			<WinHead who="machine" dot={false}><span class="ready m-later">{READY}</span></WinHead>
		{:else}
			<WinHead who="laptop" />
		{/if}
		<div class="scm">
			<div class="head">
				<span class="h">Source control</span>
				<span class="branch" class:m-head={m} class:m-later={m}
					>{@render branchIcon()}fetch-timeout</span
				>
			</div>

			<ol class="graph">
				<!-- The agent's commit: made on the machine, fetched to the laptop. -->
				<li class="commit slot">
					<i class="node"></i>
					<span class="msg">{agent.msg}</span>
					{#if !m}<span class="ref rref">repose/fetch-timeout</span>{/if}
					<span class="hash">{agent.hash}</span>
				</li>
				{#each commits as c (c.hash)}
					<li
						class="commit"
						class:base={c.base}
						data-from={!m ? c.hash : undefined}
						data-to={m ? c.hash : undefined}
					>
						<i class="node"></i>
						<span class="msg">{c.msg}</span>
						{#if c.base}<span class="ref">master</span>{/if}
						<span class="hash">{c.hash}</span>
					</li>
				{/each}
			</ol>

			<ul class="files">
				{#if !m}
					{@render group('Changes', 3)}
					{@render file(envExample, where)}
					{@render file(poller, where)}
					{@render file(test, where)}
					{@render group('Ignored', null)}
					{@render file(env, where)}
					<li class="fr nm l-stop">
						{@render folderIcon()}
						<span class="fname">node_modules/<i class="strike"></i></span>
						<svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true" class="stop">
							<g fill="none" stroke="currentColor" stroke-width="1.5">
								<circle cx="8" cy="8" r="6" />
								<path d="M3.8 12.2l8.4-8.4" />
							</g>
						</svg>
					</li>
				{:else}
					{@render group('Changes', 3, true)}
					{@render file(envExample, where)}
					{@render file(poller, where)}
					{@render file(test, where)}
					{@render group('Ignored', null, true)}
					{@render file(env, where)}
				{/if}
			</ul>
		</div>
	</div>
{/snippet}

<div class="oc" use:picture.frame>
	<PauseButton {picture} />
	<p id={descId} class="sr-only">{story}</p>
	<div class="pic" role="img" aria-label={label} aria-describedby={descId} bind:this={pic}>
		<div class="side" aria-hidden="true">{@render panel('laptop')}</div>

		<!-- The two commands in the middle of the gap, one over the other
		     between the panels side by side, in a row between them stacked.
		     No copy is seen in the gap, so none crosses a chip. -->
		<div class="hop" aria-hidden="true">
			<div class="leg run">
				<span class="wire"></span>
				<span class="chip" class:fire>repose run</span>
				<span class="wire arrow"></span>
			</div>
			<div class="leg fetch m-later">
				<span class="wire arrow back"></span>
				<span class="chip" class:fire={fetchFire}>git fetch repose</span>
				<span class="wire"></span>
			</div>
		</div>

		<div class="side" aria-hidden="true">{@render panel('machine')}</div>

		<div class="fly" aria-hidden="true" bind:this={fly}></div>
	</div>
</div>

<style>
	/* Colours are the picture tokens (--pic-*, routes/layout.css), which
	   carry their own dark values. The bands are fixed heights, so the
	   chips between the panels sit on the title and head rows exactly. */
	.pic {
		--head-top: 10px;
		--row: 26px;
		position: relative;
		display: grid;
		grid-template-columns: minmax(0, 1fr);
		/* Each window is as tall as its rows, so neither ends on an empty
		   band: the machine's is a row shorter, having no node_modules. */
		align-items: start;
		min-width: 0;
	}
	.side {
		display: flex;
		min-width: 0;
	}

	/* Window */
	.win {
		flex: 1;
		display: flex;
		flex-direction: column;
		border: 1px solid var(--rule-strong);
		border-radius: var(--radius);
		background: var(--surface);
		overflow: hidden;
		font-size: 13px;
		color: var(--pic-ink);
	}
	.scm {
		flex: 1;
		min-width: 0;
		padding: var(--head-top) 0 12px;
	}
	.head {
		display: flex;
		align-items: center;
		justify-content: space-between;
		gap: 10px;
		height: var(--row);
		padding: 0 14px;
	}
	.h {
		font-weight: 600;
	}
	.branch {
		display: inline-flex;
		align-items: center;
		gap: 5px;
		font-family: var(--font-mono);
		font-size: 12px;
		color: var(--pic-ink);
	}

	/* Commit graph */
	.graph {
		position: relative;
		margin: 0 0 6px;
		padding: 0;
		list-style: none;
	}
	.commit {
		position: relative;
		display: flex;
		align-items: center;
		gap: 8px;
		height: var(--row);
		padding: 0 14px 0 34px;
	}
	.node {
		position: absolute;
		left: 20px;
		top: 50%;
		width: 9px;
		height: 9px;
		margin-top: -4.5px;
		border-radius: 50%;
		background: var(--pic-accent);
		z-index: 1;
	}
	.graph .commit:not(:last-child)::after {
		content: '';
		position: absolute;
		left: 24px;
		top: 13px;
		height: var(--row);
		border-left: 1.5px solid var(--pic-accent);
	}
	.graph .commit:global(.cut)::after {
		display: none;
	}
	.commit.base .node {
		background: var(--surface);
		border: 1.5px solid var(--pic-faint);
	}
	.msg {
		flex: 1;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.base .msg {
		color: var(--pic-dim);
	}
	.hash,
	.ref {
		flex: none;
		font-family: var(--font-mono);
		font-size: 12px;
		color: var(--pic-dim);
	}
	.ref {
		padding: 0 4px;
		border: 1px solid var(--rule-strong);
		border-radius: var(--radius);
		line-height: 16px;
	}
	/* The fetched branch: what git fetch repose brought, in the chips'
	   accent. */
	.rref {
		border-color: var(--pic-accent);
		color: var(--pic-accent);
	}

	/* Files */
	.files {
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.grp {
		display: flex;
		align-items: center;
		gap: 5px;
		height: var(--row);
		padding: 0 14px 0 12px;
		font-size: 12px;
		font-weight: 600;
		color: var(--pic-dim);
	}
	.chev {
		flex: none;
	}
	.count {
		margin-left: auto;
		min-width: 18px;
		padding: 0 5px;
		border-radius: var(--radius);
		background: var(--sunken);
		border: 1px solid var(--rule);
		text-align: center;
		font-weight: 500;
		line-height: 16px;
	}
	.fr {
		position: relative;
		display: flex;
		align-items: center;
		gap: 7px;
		height: var(--row);
		padding: 0 14px 0 26px;
		white-space: nowrap;
	}
	.fi {
		flex: none;
		color: var(--pic-dim);
	}
	.fname {
		flex: none;
		font-family: var(--font-mono);
		font-size: 12.5px;
	}
	.dir {
		flex: 1;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		font-size: 12px;
		color: var(--pic-dim);
	}
	.st {
		flex: none;
		width: 12px;
		margin-left: auto;
		text-align: center;
		font-family: var(--font-mono);
		font-size: 12px;
		font-weight: 600;
	}
	/* The git letters take the picture palette (LANDING.md, "The drawn
	   pictures have one palette"): a new file is a line count's green,
	   a changed one the grey of a line at rest. Amber, a source control
	   panel's own colour for M, is not the landing's to use. */
	.st-M {
		color: var(--pic-dim);
	}
	.st-U {
		color: var(--pic-add);
	}
	.ign .fname {
		color: var(--pic-dim);
	}

	/* node_modules: the laptop's is struck and stays behind. Name and icon
	   in --pic-faint, which holds 4.5:1 on the panel; the strike and the
	   red stop sign say it does not travel. */
	.nm .fi,
	.nm .fname {
		color: var(--pic-faint);
	}
	.l-stop .fname {
		position: relative;
	}
	.strike {
		position: absolute;
		left: -2px;
		right: -2px;
		top: calc(50% - 2px);
		border-top: 1.5px solid var(--pic-stop);
		transform-origin: left center;
	}
	.stop {
		flex: none;
		margin-left: auto;
		color: var(--pic-stop);
	}

	/* A row whose copy is in flight: its text greys to --pic-faint until
	   the copy lands (a class change, 200ms). */
	.commit,
	.fr,
	.commit *,
	.fr * {
		transition:
			color 0.2s,
			background-color 0.2s,
			border-color 0.2s;
	}
	.commit:global(.lift),
	.commit:global(.lift) *,
	.fr:global(.lift),
	.fr:global(.lift) * {
		color: var(--pic-faint);
	}
	.commit:global(.lift) .node {
		background: var(--pic-faint);
	}

	/* The machine reports ready in its own title bar. */
	.ready {
		display: inline-flex;
		align-items: center;
		gap: 6px;
		margin-left: auto;
		font-family: var(--font-mono);
		font-size: 12px;
		color: var(--ink-muted);
		white-space: nowrap;
	}
	.ready::before {
		content: '';
		width: 7px;
		height: 7px;
		border-radius: 50%;
		background: var(--color-emerald-500);
	}

	/* The commands between them. Stacked (phone): two columns in the
	   middle of the gap, each a wire, its chip and an arrow; no copy is
	   seen in the gap. */
	.hop {
		position: relative;
		z-index: 2;
		display: flex;
		justify-content: center;
		gap: 12px;
		padding: 6px 14px;
	}
	.leg {
		display: flex;
		flex-direction: column;
		align-items: center;
	}
	.fetch {
		flex-direction: column;
	}
	.wire {
		flex: 1;
		width: 0;
		min-height: 14px;
		border-left: 1.5px solid var(--pic-accent);
	}
	.arrow {
		position: relative;
	}
	.arrow::after {
		content: '';
		position: absolute;
		left: -5.75px;
		bottom: -1px;
		border: 5px solid transparent;
		border-top: 6px solid var(--pic-accent);
		border-bottom: 0;
	}
	.arrow.back::after {
		bottom: auto;
		top: -1px;
		border-top: 0;
		border-bottom: 6px solid var(--pic-accent);
	}

	/* Rows in flight (animated only): copies of the rows they land as, on
	   the accent's tint with an accent edge on the left, so a copy reads
	   as a row in transit and never as bare text over the rows it passes;
	   under the chips (.hop is above), clipped by the script to the panel
	   they land in. */
	.fly {
		position: absolute;
		inset: 0;
		z-index: 1;
		pointer-events: none;
		font-size: 13px;
		color: var(--pic-ink);
	}
	.fly :global(.clone) {
		position: absolute;
		margin: 0;
		list-style: none;
		background: color-mix(in oklab, var(--pic-accent) 9%, var(--surface));
		box-shadow: inset 2px 0 0 var(--pic-accent);
		will-change: transform;
	}
	.fly :global(.clone.commit) {
		padding-right: 14px;
	}

	@media (min-width: 1024px) {
		.pic {
			grid-template-columns: minmax(0, 1fr) auto minmax(0, 1fr);
		}
		/* Side by side, the two commands stand one over the other in the
		   middle of the gap, halfway down the panels: repose run, then
		   git fetch repose under it. */
		.hop {
			flex-direction: column;
			align-self: center;
			justify-content: center;
			gap: 10px;
			padding: 0;
		}
		.leg,
		.fetch {
			flex-direction: row;
			align-items: center;
		}
		.wire {
			width: auto;
			height: 0;
			min-width: 14px;
			min-height: 0;
			border-left: 0;
			border-top: 1.5px solid var(--pic-accent);
		}
		.arrow::after {
			left: auto;
			right: -1px;
			bottom: auto;
			top: -6.25px;
			border: 5px solid transparent;
			border-left: 6px solid var(--pic-accent);
			border-right: 0;
		}
		.arrow.back::after {
			right: auto;
			left: -1px;
			top: -6.25px;
			border: 5px solid transparent;
			border-right: 6px solid var(--pic-accent);
			border-left: 0;
		}
	}
	@media (max-width: 480px) {
		.win,
		.fly {
			font-size: 12px;
		}
		.fname {
			font-size: 11.5px;
		}
		.hash,
		.ref,
		.dir,
		.grp,
		.branch,
		.st,
		.ready {
			font-size: 11px;
		}
		.hop {
			gap: 10px;
		}
	}
</style>
