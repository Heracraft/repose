<!--
  The hero: the whole pitch as one moving picture, in the style of "Your
  working state, in one command" (OneCommand.svelte): two panels with
  hairline borders, rows and chips, blue for what moves, anime.js.

  Left, your laptop: your repo and a few private things (SSH keys, a cat
  photo, a tax return). `repose run`: the repo row travels into your cloud
  machine and opens there; the private things stay. The agent (Claude Code's
  mark, orange, red when rogue with no halo around it; Claude Code's own pink "⏵⏵ bypass permissions on" footer at
  the machine's bottom left) does good work: three files gain their diff
  stat on the rows themselves, and snapshots are taken along the way (a
  miniature of the machine shrinks into the snapshots panel on the right;
  the older one steps behind it as an icon and a time). Only then does it
  reach the internet, the bare sphere under the snapshots, where a
  "malicious skill" comes in. The picture is your laptop, your cloud
  machine, and on the right the snapshots panel over the globe, so nothing
  hangs off an edge. Then the agent turns red and does damage git can't
  undo (docs/LANDING.md, "Snapshots are about the machine"): its uncommitted
  edits are discarded (the diff stats struck), the app's database is emptied
  (3,532 rows to 0) and node is gone from the machine ("not found"). The skill
  runs at your laptop's private things and stops at the machine's wall. The
  newest miniature grows back over the machine: the edits, every row and the
  toolchain are back, and the agent is orange again.

  The files and their counts are the real fetch-timeout change in the
  owner's job-alerts repo (`git diff --numstat master` in the landing
  checkout, 2026-09-26: fetcher.ts 16/4, config.ts 2/0, and the new
  timeout.test.ts, 27 lines), the same change "Your working state" shows.
  Below the repo, the machine's own state, real on the recruiting machine
  (2026-09-26): `select count(*) from roles` in the recruiting-postgres-1
  container is 3,532, and `node -v` is v24.20.0. The wreck's values (0 rows,
  not found) are illustration of that damage, not a capture.
  The snapshot times are those of the real wreck run on wira (2026-09-25,
  `repose snapshots list`: 21:22 and 21:25). Claude Code runs in
  bypassPermissions mode on every machine (/docs/agents); the footer line is
  verbatim from the real Claude Code capture (scratchpad wreck/02-claude.ansi,
  colour 211, #ff87af). The laptop's private items
  are illustration, and "malicious skill" is not a real package. What
  reaches what is /docs/secrets "What an agent on the machine can reach": the
  machine's own contents and the internet are reachable from it, the laptop
  is not; the picture shows the attack failing against the laptop only,
  never the machine's own files as safe.

  Under prefers-reduced-motion, and until the script has run, one still
  frame shows how the story ends (docs/LANDING.md, "Motion": the final
  state): the repo on both sides, the edits back with their counts, the
  database at 3,532 rows and node v24.20.0, the agent orange, two
  snapshots stacked with the newest lit, and the skill stopped at the
  machine's wall as the cross, with the laptop's private rows untouched.
  The connectors, the newest snapshot's arrow back into the machine among
  them, are drawn from measure() once the script has mounted, so the
  server-rendered and no-JS frame has none. With motion allowed, the first
  paint is already the start of the story (the empty machine, drawn in
  CSS under html.js), so the restored frame never flashes before the loop.
  If the app's script never mounts the picture (the bundle failed to load
  or threw), that empty machine gives way to the still frame after 4s.
  Playback pauses off screen, and stops on the still frame if the visitor
  asks for less motion while the page is open.
-->
<script lang="ts">
	import { onMount, tick } from 'svelte';
	import { agentMarks } from '$lib/components/illustrations/marks';
	import Shape from '$lib/components/landing/Shape.svelte';
	import { watchReducedMotion } from './inview';

	const agent = agentMarks[0];

	const repo = 'your-project';
	// The fetch-timeout change: w = worked on (and later struck), add/del its
	// diff stat, nu = a new file the agent writes.
	type Kid = { n: string; dir?: string; add?: number; del?: number; nu?: boolean };
	const kids: Kid[] = [
		{ n: 'fetcher.ts', dir: 'apps/worker/src/ingest', add: 16, del: 4 },
		{ n: 'config.ts', dir: 'apps/worker/src', add: 2 },
		{ n: 'timeout.test.ts', dir: 'apps/worker/src/ingest', add: 27, nu: true }
	];
	// The machine's own state, which git knows nothing about: the app's
	// database and the toolchain. ok is how it stands, bad after the wreck.
	type Sys = { n: string; dir?: string; icon: 'db' | 'tool'; ok: string; bad: string };
	const sys: Sys[] = [
		{ n: 'postgres', dir: 'roles table', icon: 'db', ok: '3,532 rows', bad: '0 rows' },
		{ n: 'node', icon: 'tool', ok: 'v24.20.0', bad: 'not found' }
	];
	type Priv = { n: string; icon: 'key' | 'image' | 'doc' };
	const privs: Priv[] = [
		{ n: '.ssh/', icon: 'key' },
		{ n: 'cat.jpg', icon: 'image' },
		{ n: 'taxes-2025.pdf', icon: 'doc' }
	];
	const times = ['21:22', '21:25'];

	// The role=img's name is the gist; the whole story is its description.
	const label = 'The agent wrecks your cloud machine; a snapshot restores it';
	const story =
		'Your laptop holds your repo and your private things: SSH keys, cat.jpg and a tax return. ' +
		'repose run copies only the repo to your cloud machine. There the agent, in bypass permissions mode, ' +
		'edits fetcher.ts and config.ts and writes a test, next to the app’s Postgres database (3,532 rows in the roles table) ' +
		'and node v24.20.0, and snapshots are taken at 21:22 and 21:25. ' +
		'Then it pulls in a malicious skill from the internet and turns rogue: its uncommitted edits are discarded, the database ' +
		'is emptied to 0 rows and node is no longer found. The skill goes for your private things and is stopped at the ' +
		'machine’s wall. The 21:25 snapshot is restored: the edits, all 3,532 rows and node are back.';

	let pic: HTMLDivElement;
	let fly: HTMLDivElement;
	let fire = $state(false);

	type Geo = {
		reach: string;
		back: string;
		backHead: string;
		w: number;
		h: number;
	};
	let geo: Geo | null = $state(null);
	// The snapshot miniature: the machine's size and the scale it shrinks by.
	// The server-rendered value is what measure() finds at 1280 to 1920
	// wide (the machine 344 by 296, an 84px miniature), so hydration does
	// not resize the slot there: a taller slot pushed the globe below it
	// down, a layout shift at load.
	let mini = $state({ w: 344, h: 296, s: 84 / 344 });

	function measure() {
		const box = pic.getBoundingClientRect();
		const r = (el: Element) => {
			const b = el.getBoundingClientRect();
			return {
				l: b.left - box.left,
				r: b.right - box.left,
				t: b.top - box.top,
				b: b.bottom - box.top,
				cx: b.left - box.left + b.width / 2,
				cy: b.top - box.top + b.height / 2,
				w: b.width,
				h: b.height
			};
		};
		const win = pic.querySelector('.mwin .win')!;
		const W = r(win);
		const wide = window.matchMedia('(min-width: 768px)').matches;
		mini = { w: W.w, h: W.h, s: (wide ? 84 : 72) / W.w };

		const globe = r(pic.querySelector('.net .globe')!);
		const rail = r(pic.querySelector('.rail')!);
		const th = r(pic.querySelector('.rail .slot')!);
		// The agent's path out to the internet: across the gap to the globe
		// at its height; down to it on a phone.
		const reach = wide
			? `M${W.r + 1} ${globe.cy}H${globe.l - 6}`
			: `M${globe.cx} ${W.b + 1}V${globe.t - 6}`;
		// The snapshot's way back: out of its panel into the machine.
		const back = wide ? `M${rail.l - 2} ${th.cy}H${W.r + 3}` : `M${th.cx} ${rail.t - 2}V${W.b + 3}`;
		const backHead = wide
			? `M${W.r + 8} ${th.cy - 4}L${W.r + 3} ${th.cy}L${W.r + 8} ${th.cy + 4}`
			: `M${th.cx - 4} ${W.b + 8}L${th.cx} ${W.b + 3}L${th.cx + 4} ${W.b + 8}`;
		geo = { reach, back, backHead, w: box.width, h: box.height };
	}

	// Everything the timeline writes inline styles on. Going back to the
	// still frame strips these, so the CSS still frame shows again; .slot
	// and .mini are left alone because Svelte owns their style.
	const MOVED =
		'.m-in, .m-kid, .m-sys, .m-late, .mwin .crew, .mwin .mode, .mwin .agent, .thumb, .older, ' +
		'.t-time, .back, .mwin .stat b, .kid.nu, .mwin .strike, .mwin .xbg, .mwin .wk, .mwin .red, ' +
		'.wallhit, .stopper, .mwin .shutter, .mwin .sys .ok, .mwin .sys .bad, .skill-in, .priv .tg, ' +
		'.lines path, .rail .cam';

	onMount(() => {
		// The script has mounted the picture: the CSS fallback that shows
		// the still frame when it never does is cancelled.
		pic.classList.add('mounted');
		measure();
		let dead = false;
		let visible = false;
		let tl: { pause(): unknown; play(): unknown; revert(): unknown } | null = null;
		let io: IntersectionObserver | undefined;
		let raf = 0;
		let go: (() => void) | undefined;
		let fresh = true;
		// Bumped on every start and stop, so an import that resolves after
		// the visitor switched motion off does not start anything.
		let gen = 0;
		// Set by watchReducedMotion below, before anything reads it.
		let reduce = false;
		const q = (s: string) => Array.from(pic.querySelectorAll<HTMLElement>(s));

		const onResize = () => {
			cancelAnimationFrame(raf);
			raf = requestAnimationFrame(() => {
				if (reduce || !tl) return measure();
				tl.pause();
				tl = null;
				fresh = true;
				if (visible) go?.();
			});
		};
		window.addEventListener('resize', onResize);

		// Back to the still frame: no timeline, no observer, no inline
		// styles, so the CSS draws the restored machine.
		function stop() {
			gen++;
			io?.disconnect();
			io = undefined;
			tl?.pause();
			tl = null;
			go = undefined;
			fresh = true;
			visible = false;
			for (const e of q(MOVED)) e.removeAttribute('style');
			pic.querySelector<HTMLElement>('.mwin .win')?.style.removeProperty('min-height');
			// eslint-disable-next-line svelte/no-dom-manipulating
			fly.replaceChildren();
			fire = false;
			pic.classList.remove('live', 'wrecked');
			measure();
		}

		function start() {
			const my = ++gen;
			import('animejs')
				.then(({ createTimeline, utils, stagger }) => {
					if (dead || my !== gen) return;

					// Before the run: the machine is empty, nothing is struck, the
					// agent is itself, no work, no snapshot, no skill anywhere.
					function pre() {
						utils.set(
							q(
								'.m-in, .m-kid, .m-sys, .m-late, .mwin .kid.nu, .mwin .crew, .mwin .mode, .thumb, .older, .t-time, .back'
							),
							{
								opacity: 0,
								x: 0,
								y: 0,
								scale: 1
							}
						);
						utils.set(q('.mwin .stat b'), { opacity: 0, scale: 1 });
						utils.set(q('.mwin .strike'), { scaleX: 0 });
						utils.set(q('.mwin .xbg, .mwin .wk, .mwin .red, .wallhit, .stopper, .mwin .shutter'), {
							opacity: 0
						});
						utils.set(q('.mwin .sys .ok'), { opacity: 1 });
						utils.set(q('.mwin .sys .bad'), { opacity: 0 });
						utils.set(q('.thumb .kid.nu'), { opacity: 0 });
						utils.set(q('.skill-in'), { opacity: 0, x: 0, y: 0 });
						utils.set(q('.priv .tg'), { opacity: 0 });
						utils.set(q('.lines path'), { strokeDashoffset: 1, opacity: 1 });
						// The snapshot mark keeps the angle the last loop left it at,
						// folded into 0 to 360. Two quarter turns and the full turn
						// back leave it at -180deg; setting 0 here cut it a half turn
						// with no motion at each restart, a move LANDING.md does not
						// list, and the panel title's pinwheel is in view throughout.
						const cam = q('.rail .cam');
						const deg = cam.length ? Number(utils.get(cam[0], 'rotate', false)) || 0 : 0;
						utils.set(cam, { rotate: ((deg % 360) + 360) % 360 });
						fire = false;
						pic.classList.remove('wrecked');
					}

					function cycle() {
						if (dead || my !== gen) return;
						if (!visible) {
							tl = null;
							return;
						}
						// .fly is an empty layer Svelte never renders into; only a
						// throwaway copy of the laptop's repo row lives in it.
						// eslint-disable-next-line svelte/no-dom-manipulating
						fly.replaceChildren();
						utils.set(q('.thumb, .back, .older, .skill-in'), { x: 0, y: 0, scale: 1 });
						measure();
						// Let the miniature and connectors re-render for the new
						// geometry, then measure once more against that.
						tick()
							.then(() => {
								measure();
								return tick();
							})
							.then(() => !dead && my === gen && run());
					}

					function run() {
						// .live hands the picture from the CSS first paint (the
						// empty machine) to the inline styles pre() writes, in the
						// same frame. Set here, not when anime.js arrives: a picture
						// under the observer's threshold at load (stacked, on a
						// phone) would otherwise drop the first-paint rule and draw
						// the restored machine until it scrolled into view.
						pre();
						pic.classList.add('live');
						const wide = window.matchMedia('(min-width: 768px)').matches;

						const box = pic.getBoundingClientRect();
						const rel = (el: Element) => {
							const b = el.getBoundingClientRect();
							return { l: b.left - box.left, t: b.top - box.top, w: b.width, h: b.height };
						};
						// The repo row's copy, flying from the laptop to the machine.
						const from = pic.querySelector<HTMLElement>('[data-from]')!;
						const to = pic.querySelector<HTMLElement>('[data-to]')!;
						const a = rel(from);
						const b = rel(to);
						const c = from.cloneNode(true) as HTMLElement;
						c.removeAttribute('data-from');
						c.classList.add('clone');
						Object.assign(c.style, {
							left: `${a.l}px`,
							top: `${a.t}px`,
							width: `${a.w}px`,
							opacity: '0'
						});
						// eslint-disable-next-line svelte/no-dom-manipulating
						fly.appendChild(c);

						// The miniature: from covering the machine to its slot.
						const win = rel(pic.querySelector('.mwin .win')!);
						const slot = rel(pic.querySelector('.rail .slot')!);
						const k = win.w / slot.w;
						const cover = { x: win.l - slot.l, y: win.t - slot.t, scale: k };
						const peek = -(parseFloat(getComputedStyle(pic).getPropertyValue('--peek')) || 64);

						// The skill: from the internet, to the agent, to the wall.
						// Its home is where it is stopped: the left wall beside the
						// laptop, or the top edge under it on a phone.
						const home = rel(pic.querySelector('.skill-in')!);
						const src = rel(pic.querySelector('.net .skill')!);
						const ag = rel(pic.querySelector('.mwin .agent')!);
						const at = { x: src.l - home.l, y: src.t - home.t };
						// It docks beside the agent, then runs at the wall.
						const dock = {
							x: ag.l - 12 - home.w - home.l,
							y: ag.t + ag.h / 2 - (home.t + home.h / 2)
						};
						// Its knock back off the wall: right on a wide screen, down on a phone.
						const knock: Record<string, number[]> = wide ? { x: [0, 7, 0] } : { y: [0, 7, 0] };

						const T = {
							run: 600,
							fly: 800,
							open: 1750,
							agent: 2300,
							work: 2900,
							shot1: 4100,
							write: 5500,
							shot2: 6600,
							reach: 8600,
							pull: 9100,
							rogue: 10200,
							strike: 10700,
							lunge: 12000,
							hit: 12700,
							restore: 13800,
							back: 14600,
							out: 18200
						};
						const snap = (t: ReturnType<typeof createTimeline>, at: number) =>
							t
								// The snapshot mark clicks a quarter turn: one more state kept.
								.add(q('.rail .cam'), { rotate: '+=90', duration: 420, ease: 'outQuad' }, at)
								.add(
									q('.mwin .shutter'),
									{ opacity: [0, 0.9, 0], duration: 460, ease: 'outQuad' },
									at
								)
								.set(q('.thumb'), { opacity: 1, ...cover }, at + 120)
								.add(
									q('.thumb'),
									{ x: 0, y: 0, scale: 1, duration: 850, ease: 'inOutCubic' },
									at + 220
								);
						// A file being written: the row lights, its diff stat pops in.
						const work = (t: ReturnType<typeof createTimeline>, sel: string, at: number) =>
							t
								.add(
									q(`${sel} .wk`),
									{ opacity: [0, 1, 0], duration: 1000, ease: 'inOutCubic' },
									at
								)
								.add(
									q(`${sel} .stat b`),
									{
										opacity: [0, 1],
										scale: [0.6, 1],
										duration: 380,
										delay: stagger(140),
										ease: 'outBack'
									},
									at + 250
								);

						const t = createTimeline({ autoplay: false, onComplete: () => cycle() })
							// repose run: the repo row travels, the machine opens it.
							.call(() => (fire = true), T.run)
							.call(() => (fire = false), T.run + 520)
							.set(c, { opacity: 1 }, T.fly)
							.add(c, { x: b.l - a.l, y: b.t - a.t, duration: 900, ease: 'inOutCubic' }, T.fly)
							.set(q('.m-in'), { opacity: 1 }, T.fly + 900)
							.set(c, { opacity: 0 }, T.fly + 916)
							.add(
								q('.m-kid'),
								{
									opacity: [0, 1],
									y: [-6, 0],
									duration: 320,
									delay: stagger(90),
									ease: 'outCubic'
								},
								T.open
							)
							.add(q('.m-late'), { opacity: [0, 1], duration: 300 }, T.open + 200)
							.add(
								q('.m-sys'),
								{
									opacity: [0, 1],
									y: [-6, 0],
									duration: 320,
									delay: stagger(90),
									ease: 'outCubic'
								},
								T.open + 380
							)
							.add(
								q('.mwin .crew'),
								{ opacity: [0, 1], scale: [0.85, 1], duration: 380, ease: 'outBack' },
								T.agent
							)
							.add(q('.mwin .mode'), { opacity: [0, 1], duration: 300 }, T.agent + 150)
							// Good work: the agent edits two files; each gains its diff stat.
							.add(
								q('.mwin .agent'),
								{ y: [0, -2, 0, -2, 0], duration: 1100, ease: 'inOutCubic' },
								T.work
							);
						work(t, '.mwin .kid.k0', T.work);
						work(t, '.mwin .kid.k1', T.work + 650);
						// First snapshot, 21:22.
						snap(t, T.shot1)
							.set(q('.t-time.n0'), { opacity: 0 }, T.shot1)
							.add(q('.t-time.n0'), { opacity: [0, 1], duration: 300 }, T.shot1 + 1000)
							// It writes a test: a new file row opens in the room its
							// row has kept all along, so nothing below it moves.
							.add(
								q('.mwin .agent'),
								{ y: [0, -2, 0, -2, 0], duration: 1100, ease: 'inOutCubic' },
								T.write
							)
							.add(
								q('.mwin .kid.nu'),
								{ opacity: [0, 1], y: [-6, 0], duration: 320, ease: 'outCubic' },
								T.write
							);
						work(t, '.mwin .kid.k2', T.write + 200);
						// Second snapshot, 21:25: the 21:22 one steps behind it.
						t.set(q('.older'), { opacity: 1, x: 0 }, T.shot2)
							.set(q('.thumb, .t-time.n0'), { opacity: 0 }, T.shot2)
							.add(q('.older'), { x: peek, duration: 520, ease: 'inOutCubic' }, T.shot2 + 60)
							.set(q('.thumb .kid.nu'), { opacity: 1 }, T.shot2);
						snap(t, T.shot2 + 300)
							.add(q('.t-time.n1'), { opacity: [0, 1], duration: 300 }, T.shot2 + 1300)
							// Only now: it reaches the internet, and the malicious skill comes in.
							.add(
								q('.lines .reach'),
								{ strokeDashoffset: [1, 0], duration: 380, ease: 'outCubic' },
								T.reach
							)
							// The one skill chip comes out of the internet and travels in,
							// under the agent (.crew sits above it) to its side.
							.set(q('.skill-in'), { ...at }, T.reach + 300)
							.add(
								q('.skill-in'),
								{ opacity: [0, 1], scale: [0.6, 1], duration: 300, ease: 'outBack' },
								T.reach + 300
							)
							.add(q('.skill-in'), { ...dock, duration: 1000, ease: 'inOutCubic' }, T.pull)
							.add(
								q('.lines .reach'),
								{ opacity: [1, 0], duration: 300, ease: 'inQuad' },
								T.pull + 1000
							)
							// The agent turns rogue and deletes its own work.
							.add(q('.mwin .agent .red'), { opacity: [0, 1], duration: 350 }, T.rogue)
							// A shake: four moves of 120ms.
							.add(
								q('.mwin .agent'),
								{ x: [0, -2, 2, -1, 0], duration: 480, ease: 'inOutCubic' },
								T.rogue + 200
							)
							// .wrecked greys the struck names and stats (a 200ms colour change).
							.call(() => pic.classList.add('wrecked'), T.strike)
							.add(
								q('.mwin .kid.x .xbg'),
								{ opacity: [0, 1], duration: 250, delay: stagger(120) },
								T.strike
							)
							.add(
								q('.mwin .kid.x .strike'),
								{ scaleX: [0, 1], duration: 380, delay: stagger(120), ease: 'outCubic' },
								T.strike + 80
							)
							// The machine itself: the database emptied, the toolchain gone.
							.add(
								q('.mwin .sys .xbg'),
								{ opacity: [0, 1], duration: 250, delay: stagger(160) },
								T.strike + 420
							)
							.add(
								q('.mwin .sys .ok'),
								{ opacity: [1, 0], duration: 300, delay: stagger(160), ease: 'inQuad' },
								T.strike + 420
							)
							.add(
								q('.mwin .sys .bad'),
								{ opacity: [0, 1], duration: 260, delay: stagger(160) },
								T.strike + 560
							)
							// The skill goes for the laptop's private things and stops at the wall.
							.add(q('.priv .tg'), { opacity: [0, 1], duration: 300, delay: stagger(60) }, T.lunge)
							.add(q('.skill-in'), { x: 0, y: 0, duration: 700, ease: 'inOutCubic' }, T.lunge)
							.add(q('.wallhit'), { opacity: [0, 1], duration: 200, ease: 'outQuad' }, T.hit)
							.add(
								q('.stopper'),
								{ opacity: [0, 1], scale: [0.6, 1], duration: 300, ease: 'outBack' },
								T.hit
							)
							.add(q('.skill-in'), { ...knock, duration: 380, ease: 'outQuad' }, T.hit)
							.add(q('.priv .tg'), { opacity: 0, duration: 500, ease: 'inQuad' }, T.hit + 700)
							// The newest snapshot comes back over the machine, work included;
							// the snapshot mark rewinds a full turn as it does.
							.add(
								q('.lines .ret'),
								{ strokeDashoffset: [1, 0], duration: 300, ease: 'outCubic' },
								T.restore
							)
							.add(
								q('.rail .cam'),
								{ rotate: '-=360', duration: 1000, ease: 'inOutCubic' },
								T.restore + 150
							)
							.set(q('.back'), { opacity: 1 }, T.restore + 250)
							.add(q('.back'), { ...cover, duration: 800, ease: 'inOutCubic' }, T.restore + 280)
							.add(q('.lines .ret'), { opacity: [1, 0], duration: 300, ease: 'inQuad' }, T.back)
							.call(() => pic.classList.remove('wrecked'), T.back + 100)
							.set(q('.mwin .strike'), { scaleX: 0 }, T.back + 100)
							.set(
								q('.mwin .xbg, .mwin .agent .red, .mwin .sys .bad, .wallhit, .stopper, .skill-in'),
								{ opacity: 0 },
								T.back + 100
							)
							.set(q('.mwin .sys .ok'), { opacity: 1 }, T.back + 100)
							.add(q('.back'), { opacity: [1, 0], duration: 500, ease: 'inQuad' }, T.back + 150)
							// Rest on the restored machine, then clear it and go again.
							.add(
								q(
									'.m-in, .m-kid, .m-sys, .mwin .kid.nu, .m-late, .mwin .crew, .mwin .mode, .thumb, .older, .t-time'
								),
								{ opacity: 0, duration: 450, ease: 'inQuad' },
								T.out
							)
							.add({ duration: 500 }, T.out + 450);

						tl = t;
						fresh = false;
						t.play();
					}
					go = cycle;

					io = new IntersectionObserver(
						([e]) => {
							visible = e.isIntersecting;
							if (visible) {
								if (tl && !fresh) tl.play();
								else cycle();
							} else {
								tl?.pause();
							}
						},
						{ threshold: 0.35 }
					);
					io.observe(pic);
				})
				// No anime.js, no story: the still frame, not an empty machine.
				.catch(() => !dead && my === gen && pic.classList.add('still'));
		}

		// A visitor can ask for less motion while the page is open: stop on
		// the still frame then, and start the story if they allow it again.
		// The first call is the setting at mount: start unless it is reduce.
		let first = true;
		const unwatch = watchReducedMotion((r) => {
			reduce = r;
			if (first) {
				first = false;
				if (!r) start();
			} else if (r) stop();
			else start();
		});

		return () => {
			dead = true;
			io?.disconnect();
			tl?.pause();
			unwatch();
			window.removeEventListener('resize', onResize);
			cancelAnimationFrame(raf);
		};
	});
</script>

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

{#snippet repoIcon()}
	<svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true" class="fi">
		<g fill="none" stroke="currentColor" stroke-width="1.1" stroke-linejoin="round">
			<path d="M1.5 3.5h4.3l1.4 1.6h7.3v8.4h-13z" />
			<circle cx="6" cy="10.8" r="1.1" />
			<circle cx="10.5" cy="8.4" r="1.1" />
			<path d="M6 9.7V7.6M10.5 9.5c0 1.2-4.5 0.4-4.5 1.3" />
		</g>
	</svg>
{/snippet}

{#snippet privIcon(kind: Priv['icon'])}
	<svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true" class="fi">
		<g fill="none" stroke="currentColor" stroke-width="1.1" stroke-linejoin="round">
			{#if kind === 'key'}
				<circle cx="5" cy="8" r="2.8" />
				<path d="M7.8 8h6.2M12 8v2.3M10 8v1.7" stroke-linecap="round" />
			{:else if kind === 'image'}
				<rect x="2" y="3" width="12" height="10" />
				<path d="M2 11l3.5-3.2 2.7 2.4 2-1.7L14 11.6" />
				<circle cx="10.6" cy="5.9" r="1" />
			{:else}
				<path d="M4 1.5h5.2L12.5 4.8v9.7H4z M9 1.5v3.5h3.5" />
				<path d="M6 8.5h4.5M6 11h4.5" />
			{/if}
		</g>
	</svg>
{/snippet}

{#snippet sysIcon(kind: Sys['icon'])}
	<svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true" class="fi">
		<g fill="none" stroke="currentColor" stroke-width="1.1" stroke-linejoin="round">
			{#if kind === 'db'}
				<ellipse cx="8" cy="3.8" rx="5" ry="1.9" />
				<path
					d="M3 3.8v8.4c0 1.05 2.24 1.9 5 1.9s5-.85 5-1.9V3.8M3 8c0 1.05 2.24 1.9 5 1.9s5-.85 5-1.9"
				/>
			{:else}
				<rect x="1.8" y="2.5" width="12.4" height="11" />
				<path d="M4.5 6.2l2.2 1.8-2.2 1.8M8.3 10.3h3.2" stroke-linecap="round" />
			{/if}
		</g>
	</svg>
{/snippet}

<!-- The snapshot mark: the pinwheel of the shape set, two quarters of a
     circle, the state before and the state after (docs/LANDING.md, "Shape
     language"). -->
{#snippet camIcon(size: number, cls: string)}
	<svg viewBox="0 0 16 16" width={size} height={size} aria-hidden="true" class={cls}>
		<path d="M8 8V0A8 8 0 0 0 0 8Z" fill="currentColor" />
		<path d="M8 8V16A8 8 0 0 0 16 8Z" fill="currentColor" opacity="0.45" />
	</svg>
{/snippet}

{#snippet laptopMark()}
	<svg viewBox="0 0 24 24" width="18" height="18" aria-hidden="true" class="mark">
		<g fill="none" stroke="currentColor" stroke-width="1.5" stroke-linejoin="round">
			<rect x="4.5" y="5" width="15" height="10.5" rx="1" />
			<path d="M2 18.5h20" stroke-linecap="round" />
		</g>
	</svg>
{/snippet}

{#snippet cloudMark()}
	<svg viewBox="0 0 24 24" width="18" height="18" aria-hidden="true" class="mark">
		<path
			d="M7 18.5h10.5a4 4 0 0 0 .6-7.96A5.5 5.5 0 0 0 7.4 9.1 4.7 4.7 0 0 0 7 18.5z"
			fill="none"
			stroke="currentColor"
			stroke-width="1.5"
			stroke-linejoin="round"
		/>
	</svg>
{/snippet}

{#snippet stopIcon()}
	<svg viewBox="0 0 16 16" width="18" height="18" aria-hidden="true">
		<circle cx="8" cy="8" r="6.5" fill="var(--surface)" />
		<g fill="none" stroke="currentColor" stroke-width="1.5">
			<circle cx="8" cy="8" r="6" />
			<path d="M3.8 12.2l8.4-8.4" />
		</g>
	</svg>
{/snippet}

{#snippet skillChip(cls: string)}
	<span class="skill {cls}">
		<svg viewBox="0 0 16 16" width="13" height="13" aria-hidden="true">
			<g fill="none" stroke="currentColor" stroke-width="1.2" stroke-linejoin="round">
				<path d="M8 1.8l5.5 3v6.4L8 14.2l-5.5-3V4.8z" />
				<path d="M2.5 4.8L8 7.8l5.5-3M8 7.8v6.4" />
			</g>
		</svg>
		malicious skill
	</span>
{/snippet}

{#snippet machineWin(live: boolean)}
	<div class="win">
		<div class="title">
			{@render cloudMark()}<span class="who">your cloud machine</span><i
				class="dot"
				class:m-late={live}
			></i>
		</div>
		<ul class="rows">
			<li class="fr head" class:m-in={live} data-to={live ? 'repo' : undefined}>
				<svg viewBox="0 0 10 10" width="9" height="9" aria-hidden="true" class="chev">
					<path d="M2 3.5l3 3 3-3" fill="none" stroke="currentColor" stroke-width="1.3" />
				</svg>
				{@render repoIcon()}<span class="fname">{repo}/</span>
			</li>
			{#each kids as k, i (k.n)}
				<li class="fr kid k{i}" class:x={k.add} class:nu={k.nu} class:m-kid={live && !k.nu}>
					{#if k.add}<i class="wk"></i><i class="xbg"></i>{/if}
					<span class="dim">{@render fileIcon()}</span>
					<span class="fname dim">{k.n}</span>
					{#if k.dir}<span class="path dim">{k.dir}</span>{/if}
					{#if k.add}
						<span class="stat"
							><b class="plus">+{k.add}</b>{#if k.del}<b class="minus">−{k.del}</b>{/if}<i
								class="strike"
							></i></span
						>
					{/if}
				</li>
			{/each}
			{#each sys as m (m.n)}
				<li class="fr sys" class:m-sys={live}>
					<i class="xbg"></i>
					{@render sysIcon(m.icon)}<span class="fname">{m.n}</span>
					{#if m.dir}<span class="path">{m.dir}</span>{/if}
					<span class="val"><span class="ok">{m.ok}</span><span class="bad">{m.bad}</span></span>
				</li>
			{/each}
		</ul>
		<div class="lane">
			{#if live}
				<span class="wallhit"></span>
				<span class="stopper">{@render stopIcon()}</span>
				{@render skillChip('skill-in')}
			{/if}
			<span class="crew">
				<span class="agent">
					<svg viewBox="0 0 24 24" width="28" height="28" aria-hidden="true">
						{#each agent.paths as d (d)}
							<path {d} fill-rule="evenodd" class="orange" />
						{/each}
					</svg>
					<svg viewBox="0 0 24 24" width="28" height="28" aria-hidden="true" class="red">
						{#each agent.paths as d (d)}
							<path {d} fill-rule="evenodd" />
						{/each}
					</svg>
				</span>
			</span>
		</div>
		<div class="mode">⏵⏵ bypass permissions on</div>
		{#if live}<i class="shutter"></i>{/if}
	</div>
{/snippet}

<div class="hero-pic" role="img" aria-label={label} aria-describedby="hero-story" bind:this={pic}>
	<p id="hero-story" hidden>{story}</p>
	<div class="side laptop" aria-hidden="true">
		<div class="win">
			<div class="title">{@render laptopMark()}<span class="who">your laptop</span></div>
			<ul class="rows">
				<li class="fr head repo" data-from="repo">
					<svg viewBox="0 0 10 10" width="9" height="9" aria-hidden="true" class="chev side-chev">
						<path d="M3.5 2l3 3-3 3" fill="none" stroke="currentColor" stroke-width="1.3" />
					</svg>
					{@render repoIcon()}<span class="fname">{repo}/</span>
				</li>
				{#each privs as p (p.n)}
					<li class="fr priv">
						<i class="tg"></i>
						{@render privIcon(p.icon)}<span class="fname">{p.n}</span>
					</li>
				{/each}
			</ul>
		</div>
	</div>

	<div class="hop" aria-hidden="true">
		<span class="wire"></span>
		<span class="chip" class:fire>repose run</span>
		<span class="wire arrow"></span>
	</div>

	<div class="machine" aria-hidden="true">
		<div class="mwin">{@render machineWin(true)}</div>

		<div class="side right">
			<div class="rail win">
				<div class="title">{@render camIcon(18, 'cam')}<span class="who">snapshots</span></div>
				<div class="railbody">
					<span class="slot" style:width="{mini.w * mini.s}px" style:height="{mini.h * mini.s}px">
						<span class="older">
							{@render camIcon(12, 'o-cam')}<span class="o-time">{times[0]}</span>
						</span>
						{#each ['thumb', 'back'] as cls (cls)}
							<span class={cls}>
								<span
									class="mini"
									style:width="{mini.w}px"
									style:height="{mini.h}px"
									style:transform="scale({mini.s})">{@render machineWin(false)}</span
								>
							</span>
						{/each}
						<span class="t-time n0">{times[0]}</span>
						<span class="t-time n1">{times[1]}</span>
					</span>
				</div>
			</div>

			<div class="net">
				<span class="globe"
					><Shape kind="sphere" /><svg viewBox="0 0 24 24" class="meridians" aria-hidden="true"
						><g fill="none" stroke="currentColor" stroke-width="0.9"
							><ellipse cx="12" cy="12" rx="4.2" ry="11.6" /><path d="M0.4 8h23.2M0.4 16h23.2" /></g
						></svg
					></span
				>
				{@render skillChip('')}
			</div>
		</div>
	</div>

	{#if geo}
		<svg class="lines" width={geo.w} height={geo.h} aria-hidden="true">
			<path class="reach" d={geo.reach} pathLength="1" />
			<path class="ret" d={geo.back} pathLength="1" />
			<path class="ret" d={geo.backHead} pathLength="1" />
		</svg>
	{/if}

	<div class="fly" aria-hidden="true" bind:this={fly}></div>
</div>

<style>
	.hero-pic {
		/* The picture's colours are the --pic-* tokens in layout.css; only
		   the real tools' own colours are set here. */
		--claude: #d97757;
		/* Claude Code's colour 211 (#ff87af) on its dark theme; deepened on
		   paper so it reads. */
		--mode: #c2416f;
		--rogue: #c0271c;
		position: relative;
		display: grid;
		grid-template-columns: minmax(0, 1fr);
		min-width: 0;
		font-size: 13px;
		color: var(--pic-ink);
	}
	.side,
	.mwin {
		display: flex;
		min-width: 0;
	}

	/* Windows, as in "Your working state" */
	.win {
		position: relative;
		flex: 1;
		display: flex;
		flex-direction: column;
		min-width: 0;
		border: 1px solid var(--rule-strong);
		border-radius: 3px;
		background: var(--surface);
		color: var(--pic-ink);
		font-size: 13px;
	}
	/* The machine's wall. */
	.mwin > .win {
		border-color: var(--pic-ink);
	}
	.title {
		display: flex;
		align-items: center;
		gap: 8px;
		height: 36px;
		padding: 0 14px;
		border-bottom: 1px solid var(--rule);
		border-radius: 2px 2px 0 0;
		background: var(--sunken);
	}
	.mark {
		flex: none;
	}
	.who {
		font-weight: 600;
		white-space: nowrap;
	}
	.dot {
		width: 7px;
		height: 7px;
		margin-left: auto;
		border-radius: 50%;
		background: var(--color-emerald-500);
	}
	.rows {
		margin: 0;
		padding: 10px 0 4px;
		list-style: none;
	}
	.fr {
		position: relative;
		display: flex;
		align-items: center;
		gap: 7px;
		height: 28px;
		padding: 0 14px 0 26px;
		white-space: nowrap;
	}
	.fr.head {
		padding-left: 12px;
		gap: 6px;
	}
	.chev {
		flex: none;
		color: var(--pic-dim);
	}
	.fi {
		position: relative;
		flex: none;
		display: block;
		color: var(--pic-dim);
	}
	.dim {
		position: relative;
		flex: none;
	}
	.fname {
		position: relative;
		flex: none;
		font-family: var(--font-mono);
		font-size: 12.5px;
	}
	.head .fname {
		font-weight: 600;
	}
	.head .fi {
		color: var(--pic-accent);
	}
	/* The folder a file sits in, as in "Your working state". */
	.path {
		position: relative;
		flex: 0 1 auto;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		font-size: 12px;
		/* Context, muted: --pic-faint holds 4.5:1 on the surface and on a
		   red row; --pic-dim fell to 4.3:1 on the red in light. */
		color: var(--pic-faint);
	}
	/* The diff stat. */
	.stat {
		position: relative;
		display: flex;
		gap: 6px;
		margin-left: auto;
		padding-left: 8px;
		font-family: var(--font-mono);
		font-size: 12px;
	}
	.stat b {
		display: inline-block;
		font-weight: 600;
	}
	.plus {
		color: var(--pic-add);
	}
	.minus {
		color: var(--pic-stop);
	}
	/* A file being written: the row lights in the accent. */
	.wk {
		position: absolute;
		inset: 0;
		opacity: 0;
		background: color-mix(in oklab, var(--pic-accent) 9%, var(--surface));
		box-shadow: inset 2px 0 0 var(--pic-accent);
	}

	/* The laptop's private things: targeted, never reached. */
	.tg {
		position: absolute;
		inset: 0;
		opacity: 0;
		background: color-mix(in oklab, var(--pic-stop) 10%, var(--surface));
		box-shadow: inset 2px 0 0 var(--pic-stop);
	}
	.laptop .rows {
		padding-bottom: 10px;
	}
	/* Your repo, then the rest of your life. */
	.repo {
		margin-bottom: 7px;
	}
	.repo::after {
		content: '';
		position: absolute;
		left: 12px;
		right: 14px;
		bottom: -4px;
		border-top: 1px solid var(--rule);
	}

	/* The machine's own state, below the repo. */
	.kid.nu {
		overflow: hidden;
	}
	.kid + .sys {
		margin-top: 7px;
	}
	.kid + .sys::before {
		content: '';
		position: absolute;
		left: 12px;
		right: 14px;
		top: -4px;
		border-top: 1px solid var(--rule);
	}
	.sys {
		padding-left: 12px;
	}
	.val {
		position: relative;
		display: grid;
		justify-items: end;
		margin-left: auto;
		padding-left: 8px;
		font-family: var(--font-mono);
		font-size: 12px;
	}
	.val > span {
		grid-area: 1 / 1;
	}
	.bad {
		font-weight: 600;
		color: var(--pic-stop);
	}
	/* The still frame is the restored machine: the wreck's values and
	   marks are drawn only by the timeline, which sets them inline. */
	.bad {
		opacity: 0;
	}

	/* Lost on the machine */
	.xbg {
		position: absolute;
		inset: 0;
		opacity: 0;
		background: color-mix(in oklab, var(--pic-stop) 9%, var(--surface));
	}
	.strike {
		position: absolute;
		left: -2px;
		right: -2px;
		top: calc(50% - 1px);
		border-top: 1.5px solid var(--pic-stop);
		transform: scaleX(0);
		transform-origin: left center;
	}
	.stat .strike {
		left: 5px;
	}
	/* Struck work greys to --pic-faint at full opacity, which holds 4.5:1
	   on the red row; the strike and the red row say "lost" without the
	   colour. A colour change of 200ms, which DESIGN-LANGUAGE "Motion"
	   lets stay for everyone. */
	.mwin .x .fname,
	.mwin .x .path,
	.mwin .x .stat b {
		transition: color 0.2s ease-out;
	}
	.hero-pic:global(.wrecked) .mwin .x .fname,
	.hero-pic:global(.wrecked) .mwin .x .path,
	.hero-pic:global(.wrecked) .mwin .x .stat b {
		color: var(--pic-faint);
	}
	.hero-pic:global(.wrecked) .mwin .sys .fi {
		color: var(--pic-stop);
	}

	/* The lane: where the agent sits and the skill runs at the wall. */
	.lane {
		position: relative;
		display: flex;
		align-items: center;
		justify-content: flex-end;
		height: 42px;
		margin-top: auto;
		padding: 0 14px;
	}
	/* Claude Code's footer line for its permission mode, in its own pink. */
	.mode {
		padding: 0 14px 9px;
		font-family: var(--font-mono);
		font-size: 12px;
		color: var(--mode);
		white-space: nowrap;
	}
	/* Above the skill chip, which passes under the agent on its way in. */
	.crew {
		position: relative;
		z-index: 4;
		display: flex;
		align-items: center;
		gap: 8px;
		flex: none;
	}
	.agent {
		position: relative;
		display: grid;
		flex: none;
		color: var(--claude);
	}
	.agent svg {
		grid-area: 1 / 1;
		fill: currentColor;
	}
	.agent .red {
		opacity: 0;
		color: var(--rogue);
	}
	.skill {
		display: inline-flex;
		align-items: center;
		gap: 5px;
		padding: 2px 7px;
		border: 1px solid var(--pic-stop);
		border-radius: 3px;
		background: var(--surface);
		font-family: var(--font-mono);
		font-size: 12px;
		color: var(--pic-stop);
		white-space: nowrap;
	}
	/* The skill's home is where it is stopped. In the still frame only the
	   cross and the lit wall stay to say it was. */
	.skill-in {
		position: absolute;
		left: 8px;
		top: 50%;
		translate: 0 -50%;
		z-index: 3;
		opacity: 0;
	}
	.wallhit {
		position: absolute;
		left: -1.5px;
		top: 6px;
		bottom: 6px;
		border-left: 2px solid var(--pic-stop);
	}
	.stopper {
		position: absolute;
		left: -9px;
		top: 50%;
		translate: 0 -50%;
		z-index: 4;
		display: grid;
		color: var(--pic-stop);
	}
	.shutter {
		position: absolute;
		inset: 0;
		opacity: 0;
		background: color-mix(in oklab, var(--pic-accent) 14%, transparent);
		box-shadow: inset 0 0 0 2px var(--pic-accent);
		pointer-events: none;
	}

	/* The right-hand stack: the snapshots panel above, in the grammar of
	   the laptop and the machine, and the internet below it as a bare
	   globe: it is not a machine of anyone's, so it gets no window. */
	.side.right {
		flex-direction: column;
		gap: 14px;
	}
	.rail.win {
		flex: none;
	}
	/* The internet: the globe, and the place the skill comes out of it. */
	.net {
		position: relative;
		flex: 1;
		display: flex;
		flex-direction: column;
		align-items: center;
		justify-content: center;
		gap: 10px;
		padding: 14px;
		color: var(--pic-ink);
	}
	/* The internet is the sphere of the shape set, with a globe's meridians
	   drawn over it in paper so it reads as the world and not a stone. */
	.globe {
		position: relative;
		flex: none;
		width: 52px;
		height: 52px;
		border-radius: 50%;
		overflow: hidden;
	}
	.meridians {
		position: absolute;
		inset: 0;
		color: var(--sh-paper);
		opacity: 0.8;
	}
	.net .skill {
		visibility: hidden;
	}
	/* The snapshots: the newest as a miniature with its time under it,
	   the older stepped back behind it as an icon and a time. */
	.railbody {
		position: relative;
		padding: 14px 14px 30px;
	}
	.slot {
		position: relative;
		display: block;
		margin-left: var(--peek);
	}
	.thumb,
	.back,
	.older {
		position: absolute;
		inset: 0;
		overflow: hidden;
		border-radius: 2px;
		outline: 1.5px solid var(--pic-accent);
		background: var(--surface);
		transform-origin: 0 0;
	}
	.back {
		opacity: 0;
		z-index: 5;
	}
	/* An older snapshot: behind the newest, only its icon and time. */
	.older {
		display: flex;
		align-items: center;
		gap: 4px;
		padding-left: 7px;
		outline-color: var(--rule-strong);
		background: var(--sunken);
		color: var(--pic-dim);
		translate: calc(-1 * var(--peek)) 0;
	}
	.hero-pic.live .older {
		translate: none;
	}
	.o-time {
		font-family: var(--font-mono);
		font-size: 12px;
	}
	.mini {
		position: absolute;
		left: 0;
		top: 0;
		display: flex;
		transform-origin: 0 0;
		pointer-events: none;
	}
	.t-time {
		position: absolute;
		left: 0;
		top: calc(100% + 4px);
		font-family: var(--font-mono);
		font-size: 12px;
		color: var(--pic-dim);
	}
	.hero-pic:not(.live) .t-time.n0 {
		display: none;
	}

	/* The command between the panels */
	.hop {
		position: relative;
		z-index: 2;
		display: flex;
		flex-direction: column;
		align-items: center;
		padding: 6px 0;
	}
	.wire {
		width: 0;
		height: 16px;
		border-left: 1.5px solid var(--pic-accent);
	}
	.wire.arrow {
		position: relative;
	}
	.wire.arrow::after {
		content: '';
		position: absolute;
		left: -5.5px;
		bottom: -1px;
		border: 5px solid transparent;
		border-top: 6px solid var(--pic-accent);
		border-bottom: 0;
	}
	.chip {
		padding: 3px 9px;
		border: 1px solid var(--pic-accent);
		border-radius: 3px;
		background: var(--surface);
		font-family: var(--font-mono);
		font-size: 13px;
		font-weight: 600;
		color: var(--pic-accent);
		white-space: nowrap;
		transition:
			background-color 0.18s,
			color 0.18s;
	}
	.chip.fire {
		background: var(--pic-accent);
		color: var(--surface);
	}

	/* Connectors */
	.lines {
		position: absolute;
		left: 0;
		top: 0;
		z-index: 2;
		overflow: visible;
		pointer-events: none;
	}
	.lines path {
		fill: none;
		stroke-width: 1.5;
		stroke-dasharray: 1;
		stroke-dashoffset: 0;
	}
	/* The agent's reach out to the internet is a drawn wire, so it takes
	   the picture palette's red for what is blocked (--pic-stop); --rogue
	   is the mascot's own red and colours the mascot alone. */
	.lines .reach {
		opacity: 0;
		stroke: var(--pic-stop);
	}
	.lines .ret {
		stroke: var(--pic-accent);
	}

	/* The repo row in flight (animated only). */
	.fly {
		position: absolute;
		inset: 0;
		z-index: 3;
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

	/* The miniature is the machine as the snapshot took it: whole. */
	.mini .xbg,
	.mini .wk,
	.mini .strike,
	.mini .agent .red {
		display: none;
	}

	/* With motion allowed and the script coming, the first paint is the
	   start of the story, the empty machine, so the restored still frame
	   does not flash before the timeline takes over (.live). .still is set
	   if anime.js fails to load, and shows the still frame instead. */
	@media (prefers-reduced-motion: no-preference) {
		:global(html.js) .hero-pic:not(.live):not(.still) .m-in,
		:global(html.js) .hero-pic:not(.live):not(.still) .m-kid,
		:global(html.js) .hero-pic:not(.live):not(.still) .m-sys,
		:global(html.js) .hero-pic:not(.live):not(.still) .m-late,
		:global(html.js) .hero-pic:not(.live):not(.still) .mwin .kid.nu,
		:global(html.js) .hero-pic:not(.live):not(.still) .mwin .crew,
		:global(html.js) .hero-pic:not(.live):not(.still) .mwin .mode,
		:global(html.js) .hero-pic:not(.live):not(.still) .thumb,
		:global(html.js) .hero-pic:not(.live):not(.still) .older,
		:global(html.js) .hero-pic:not(.live):not(.still) .t-time,
		:global(html.js) .hero-pic:not(.live):not(.still) .wallhit,
		:global(html.js) .hero-pic:not(.live):not(.still) .stopper,
		:global(html.js) .hero-pic:not(.live):not(.still) .lines path {
			opacity: 0;
		}
		/* The empty machine is only a promise that the loop is coming. If
		   the app's script never mounts the picture (.mounted, set first
		   thing in onMount), the bundle failed to load or threw, and after
		   4s the still frame shows, as it does with scripts off. Animations
		   win over the opacity above; the fill holds until a class change
		   drops the rule. A mount later than 4s draws the still frame
		   and then the empty machine again, which beats a blank one. */
		:global(html.js) .hero-pic:not(.live):not(.still):not(.mounted) .m-in,
		:global(html.js) .hero-pic:not(.live):not(.still):not(.mounted) .m-kid,
		:global(html.js) .hero-pic:not(.live):not(.still):not(.mounted) .m-sys,
		:global(html.js) .hero-pic:not(.live):not(.still):not(.mounted) .m-late,
		:global(html.js) .hero-pic:not(.live):not(.still):not(.mounted) .mwin .kid.nu,
		:global(html.js) .hero-pic:not(.live):not(.still):not(.mounted) .mwin .crew,
		:global(html.js) .hero-pic:not(.live):not(.still):not(.mounted) .mwin .mode,
		:global(html.js) .hero-pic:not(.live):not(.still):not(.mounted) .thumb,
		:global(html.js) .hero-pic:not(.live):not(.still):not(.mounted) .older,
		:global(html.js) .hero-pic:not(.live):not(.still):not(.mounted) .t-time,
		:global(html.js) .hero-pic:not(.live):not(.still):not(.mounted) .wallhit,
		:global(html.js) .hero-pic:not(.live):not(.still):not(.mounted) .stopper,
		:global(html.js) .hero-pic:not(.live):not(.still):not(.mounted) .lines path {
			animation: hero-unhold 0s linear 4s forwards;
		}
		@keyframes hero-unhold {
			to {
				opacity: 1;
			}
		}
	}

	/* Phone: stacked; the internet and the snapshots share a row under
	   the machine, with room above them for the two connectors. */
	.hero-pic {
		--peek: 56px;
		grid-template-columns: minmax(0, 1fr);
		grid-template-areas: 'lap' 'hop' 'win' 'side';
	}
	.machine {
		display: contents;
	}
	.laptop {
		grid-area: lap;
	}
	.hop {
		grid-area: hop;
	}
	.mwin {
		grid-area: win;
	}
	.side.right {
		grid-area: side;
		flex-direction: row;
		gap: 12px;
		margin-top: 28px;
	}
	.side.right .rail.win {
		flex: 1;
	}
	/* The globe sits level with the newest snapshot beside it, not
	   floating in the middle of its half. */
	.net {
		justify-content: flex-start;
		padding-top: 56px;
	}
	/* The laptop is above the machine here, so the skill is stopped at
	   the machine's top edge: the lane lets go of its children, which
	   then sit against the window itself. */
	.lane {
		position: static;
	}
	.skill-in {
		left: auto;
		right: 34px;
		top: 18px;
	}
	.wallhit {
		left: auto;
		right: 24px;
		top: -1.5px;
		bottom: auto;
		width: 140px;
		border-left: 0;
		border-top: 2px solid var(--pic-stop);
	}
	.stopper {
		left: auto;
		right: 85px;
		top: -9px;
		translate: none;
	}
	@media (min-width: 768px) {
		.net {
			justify-content: center;
			padding-top: 14px;
		}
		.lane {
			position: relative;
		}
		.skill-in {
			left: 8px;
			right: auto;
			top: 50%;
		}
		.wallhit {
			left: -1.5px;
			right: auto;
			top: 6px;
			bottom: 6px;
			width: auto;
			border-top: 0;
			border-left: 2px solid var(--pic-stop);
		}
		.stopper {
			left: -9px;
			right: auto;
			top: 50%;
			translate: 0 -50%;
		}
		/* The laptop hugs its rows instead of stretching to the machine. */
		.laptop {
			align-self: start;
		}
		.hero-pic {
			--peek: 64px;
			grid-template-columns: minmax(0, 1fr) auto minmax(0, 1.45fr) 56px minmax(0, 0.95fr);
			grid-template-areas: 'lap hop win . side';
			column-gap: 0;
		}
		.side.right {
			flex-direction: column;
			margin-top: 0;
		}
		.side.right .rail.win {
			flex: none;
		}
		.hop {
			flex-direction: row;
			align-self: start;
			margin-top: 49px;
			padding: 0 8px;
		}
		.wire {
			width: 16px;
			height: 0;
			border-left: 0;
			border-top: 1.5px solid var(--pic-accent);
		}
		.wire.arrow::after {
			left: auto;
			right: -1px;
			bottom: auto;
			top: -6.25px;
			border: 5px solid transparent;
			border-left: 6px solid var(--pic-accent);
			border-right: 0;
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
		.title,
		.skill,
		.mode,
		.t-time,
		.o-time,
		.stat,
		.val,
		.path {
			font-size: 11px;
		}
		.chip {
			font-size: 12px;
		}
	}
	@media (prefers-color-scheme: dark) {
		.hero-pic {
			--rogue: #ff4b3e;
			--mode: #ff87af;
		}
		.mwin > .win {
			border-color: var(--pic-dim);
		}
	}
</style>
