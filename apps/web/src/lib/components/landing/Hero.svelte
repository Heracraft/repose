<!--
  The hero: the whole pitch as one moving picture, in the style of "Your
  laptop's setup, in one command" (OneCommand.svelte): panels with hairline
  borders, rows and chips, blue for what moves, anime.js. docs/LANDING.md,
  "The hero" and "Motion", is the spec; DECISIONS I-667 records this
  layout and loop.

  One flat machine room on one bottom line. Left, your laptop: your repo
  and the private rest of your life (SSH keys, a cat photo, a tax return).
  Middle, your cloud machine, already running the app's Postgres and node
  (its own state, which git knows nothing about) and Claude Code in its
  bypass permissions mode. Right, a stack exactly as tall as the machine:
  the snapshots panel over the internet's globe on the floor, each wired
  to the machine by a permanent hairline that motion only lights.

  The loop: `repose run` carries the repo row across and the project rows
  light up; the agent does good work (OneCommand's uncommitted set: two
  modified files and a new test, marked with the same git letters); you
  take a snapshot with `repose snapshots create` (a shutter, and a copy
  of the machine comes in along its wire onto the rail above the nightly
  one, drawn only inside the rail); the
  agent fetches a malicious skill along the globe's wire, turns red and
  wrecks what git can't bring back (the edits discarded, the database
  emptied, node gone); the skill's run at your laptop is a red dashed
  trail that ends on the machine's wall, where it is stopped. Then the
  machine stops (its dot greys, the agent dims, and a stop snapshots the
  wreck first, so a stop tile lands on the rail), is restored from 21:25
  (the snapshot grows into the machine from the wire's end), and the
  agent comes back (I-636 reopens its window). The restored frame holds
  5s; at the seam back to the start the project rows dim and the nightly
  tile rises behind the stop tile, which rolls up off it, so neither the
  machine nor the rail is ever empty (I-677).

  Provenance. The repo's rows are OneCommand's uncommitted set on the
  job-alerts fetch-timeout branch (the laptop checkout, 2026-09-25):
  .env.example and poller.ts modified, timeout.test.ts untracked. Their
  line counts were never recorded and that checkout is only on the
  owner's laptop (the recruiting machine is on master, checked
  2026-10-11), so the rows carry git's letters, as OneCommand's do, and
  no counts (I-667). The machine's own state is real on the recruiting
  machine (2026-09-26): `select count(*) from roles` in its postgres
  container was 3,532 (a table of job listings, so the row reads "job
  listings"), and `node -v` is v24.20.0 (rechecked 2026-10-11). The wreck's
  values (0 rows, not found) illustrate that damage. The snapshot times
  are illustrative, on one clock: the nightly at 03:00 (docs lifecycle.md,
  nightly and on every stop, the last 7 kept), yours at 21:25. Claude Code
  runs in bypassPermissions mode on every machine (/docs/agents); its
  footer line is verbatim from a real capture (colour 211, #ff87af). The
  laptop's private items are illustration, and "malicious skill" is not a
  real package. What reaches what is /docs/secrets "What an agent on the
  machine can reach": on the machine the skill only destroys, and its one
  attempt to take something aims at the laptop and stops at the wall.

  First paint and still frames. Server-rendered, the machine already has
  its header, postgres, node and the agent, and the rail its nightly
  tile; with motion allowed (html.js) the project rows are dimmed until
  `repose run` lights them, which is also where each loop ends. Under
  prefers-reduced-motion, without scripts, and when the app's script
  never mounts (after 4s), one still frame shows how the story ends: the
  restored machine, the three tiles with 21:25 lit and the permanent
  wires, with no chip inside the machine and no cross. anime.js is
  imported statically, so its chunk is preloaded with the page (F37).
-->
<script lang="ts">
	import { onMount, tick } from 'svelte';
	import { createTimeline, utils, stagger } from 'animejs';
	import { agentMarks } from '$lib/components/illustrations/marks';
	import Shape from '$lib/components/landing/Shape.svelte';
	import { watchReducedMotion } from './inview';
	import { Picture, watchWidth } from './motion.svelte';
	import PauseButton from './PauseButton.svelte';
	import WinHead from './WinHead.svelte';
	import SnapshotTile from './SnapshotTile.svelte';

	const agent = agentMarks[0];

	const repo = 'your-project';
	// OneCommand's uncommitted set, in its order and with its letters.
	type Kid = { n: string; dir?: string; st: 'M' | 'U' };
	const kids: Kid[] = [
		{ n: '.env.example', st: 'M' },
		{ n: 'poller.ts', dir: 'apps/worker/src/ingest', st: 'M' },
		{ n: 'timeout.test.ts', dir: 'apps/worker/src/ingest', st: 'U' }
	];
	// The machine's own state, which git knows nothing about: the app's
	// database and the toolchain. ok is how it stands, bad after the wreck.
	// A row with a count falls to zero through every number in between,
	// as ComesBack's counts do; one without swaps its value.
	type Sys = {
		n: string;
		dir?: string;
		icon: 'db' | 'tool';
		ok: string;
		bad: string;
		count?: { n: number; unit: string };
	};
	const sys: Sys[] = [
		{
			n: 'postgres',
			dir: 'job listings',
			icon: 'db',
			ok: '3,532 rows',
			bad: '0 rows',
			count: { n: 3532, unit: 'rows' }
		},
		{ n: 'node', icon: 'tool', ok: 'v24.20.0', bad: 'not found' }
	];
	type Priv = { n: string; icon: 'key' | 'image' | 'doc' };
	const privs: Priv[] = [
		{ n: '.ssh/', icon: 'key' },
		{ n: 'cat.jpg', icon: 'image' },
		{ n: 'taxes-2025.pdf', icon: 'doc' }
	];

	// The role=img's name is the gist; the whole story is its description.
	const label = 'The agent wrecks your cloud machine; a snapshot restores it';
	const story =
		'Your laptop holds your repo and your private things: SSH keys, cat.jpg and a tax return. ' +
		'Your cloud machine runs the app’s Postgres database (3,532 rows of job listings) and node v24.20.0, ' +
		'and has a nightly snapshot from 03:00. repose run copies only the repo to it. There Claude Code, ' +
		'in bypass permissions mode, edits .env.example and poller.ts and writes a test, and you take a ' +
		'snapshot with repose snapshots create at 21:25. Then the agent pulls in a malicious skill from the ' +
		'internet and turns rogue: its uncommitted edits are discarded, the database is emptied to 0 rows ' +
		'and node is no longer found. The skill’s attempt to reach your laptop stops at the machine’s wall. ' +
		'The machine is stopped, which takes one more snapshot, and restored from the 21:25 one: the edits, ' +
		'all 3,532 rows and node are back, and the agent starts again.';

	let pic: HTMLDivElement;
	const picture = new Picture();
	let fly: HTMLDivElement;
	let fire = $state(false);
	let ask = $state(false);
	// The snapshot the machine was restored from is lit; so it is in the
	// still frame, and the loop clears it until the restore.
	let restored = $state(true);

	// The skill's trail, in the picture's coordinates.
	let geo: { trail: string; w: number; h: number } | null = $state(null);

	function rel(el: Element) {
		const box = pic.getBoundingClientRect();
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
	}
	// The three columns need 1024px: narrower, the machine's agent row
	// cannot hold the mode line and the skill side by side, so a tablet
	// gets the phone's stack.
	// A cut inside the timeline: a one-frame tween. anime.js's own
	// timeline .set() was skipped for targets an earlier tween in the loop
	// had moved (the restored machine kept its wreck), so the loop never
	// uses it.
	const CUT = 16;
	const isWide = () => window.matchMedia('(min-width: 1024px)').matches;

	function measure() {
		const box = pic.getBoundingClientRect();
		const W = rel(pic.querySelector('.lw')!);
		const c = rel(pic.querySelector('.lw .skill-in')!);
		let trail: string;
		if (isWide()) {
			// From the skill's left end, up to the rule over the agent's row
			// and along it to the wall, level with the laptop's private rows.
			const sep = rel(pic.querySelector('.lw .agentrow')!).t - 3.5;
			const x = c.l - 12;
			trail = `M${c.l - 3} ${c.cy}H${x}V${sep}H${W.l + 1}`;
		} else {
			// Phone: the laptop is above, so up the right edge to the rule
			// under the machine's head, where it is stopped by the status dot.
			const head = rel(pic.querySelector('.lw > .shot-head')!);
			const x = W.r - 7;
			trail = `M${c.r + 3} ${c.cy}H${x}V${head.b}`;
		}
		geo = { trail, w: box.width, h: box.height };
	}

	// Everything the timeline writes inline styles on. Going back to the
	// still frame strips these, so the CSS still frame shows again.
	const MOVED =
		'.m-proj, .lw .kid.nu, .lw .st, .lw .wk, .lw .xbg, .lw .strike, .lw .sys .ok, ' +
		'.lw .sys .bad, .lw .agent .red, .lw .agent, .lw .crew, .lw .mode, .skill-in, ' +
		'.wallhit, .stopper, .lw .shutter, .back, .rail .cam, .link .lit, .trail, .trail-m, .tile';

	onMount(() => {
		// The script has mounted the picture: the CSS fallback that shows
		// the still frame when it never does is cancelled.
		pic.classList.add('mounted');
		measure();
		let dead = false;
		let visible = false;
		let tl: ReturnType<typeof createTimeline> | null = null;
		let raf = 0;
		let fresh = true;
		// Where the loop was when the picture's width changed: the rebuilt
		// timeline starts there (F20).
		let resume = 0;
		let running = false;
		let reduce = false;
		const q = (s: string) => Array.from(pic.querySelectorAll<HTMLElement>(s));

		// Only a change of the picture's own width rebuilds the loop: a
		// phone's address bar collapsing changes the window's height alone
		// and must not restart the story (F20, I-666).
		const unwidth = watchWidth(pic, () => {
			cancelAnimationFrame(raf);
			raf = requestAnimationFrame(() => {
				if (reduce || !tl) return measure();
				resume = tl.currentTime;
				tl.pause();
				tl = null;
				fresh = true;
				if (visible) cycle();
			});
		});

		// What the loop writes that is not a style: the count's number and
		// the classes on the rows it moves.
		function reset() {
			sys.forEach((m, i) => {
				const v = q(`.lw .sys.s${i} .cnt`)[0];
				if (m.count && v) v.textContent = m.ok;
			});
			for (const e of q('.lift, .falling')) e.classList.remove('lift', 'falling');
		}

		// Back to the still frame: no timeline, no inline styles, so the
		// CSS draws the restored machine.
		function stop() {
			running = false;
			picture.stop();
			tl?.pause();
			tl = null;
			fresh = true;
			resume = 0;
			visible = false;
			for (const e of q(MOVED)) e.removeAttribute('style');
			// eslint-disable-next-line svelte/no-dom-manipulating
			fly.replaceChildren();
			fire = false;
			ask = false;
			restored = true;
			reset();
			pic.classList.remove('live', 'wrecked', 'stopped');
			measure();
		}

		// The first frame of every loop, which is also the server-rendered
		// first paint (html.js) and where each loop's seam ends: the
		// machine with its services and the agent, the project rows dimmed,
		// the nightly tile alone on the rail.
		function pre() {
			utils.set(q('.m-proj'), { opacity: 0.22, y: 0 });
			utils.set(q('.lw .kid.nu'), { opacity: 0, y: 0 });
			utils.set(q('.lw .st'), { opacity: 0, scale: 1 });
			utils.set(q('.lw .strike'), { scaleX: 0 });
			utils.set(q('.lw .xbg, .lw .wk, .lw .agent .red, .wallhit, .stopper, .lw .shutter'), {
				opacity: 0
			});
			utils.set(q('.lw .sys .ok, .lw .crew, .lw .mode'), { opacity: 1 });
			reset();
			utils.set(q('.lw .sys .bad'), { opacity: 0 });
			utils.set(q('.lw .agent'), { x: 0, y: 0 });
			utils.set(q('.skill-in'), { opacity: 0, x: 0, y: 0, scale: 1 });
			utils.set(q('.back'), {
				opacity: 0,
				x: 0,
				y: 0,
				scale: 1,
				clipPath: 'inset(0px 0px 0px 0px)'
			});
			utils.set(q('.link .lit'), { opacity: 0, scaleX: 1, scaleY: 1 });
			utils.set(q('.trail'), { opacity: 1 });
			utils.set(q('.trail-m'), { strokeDashoffset: 1 });
			utils.set(q('.tile'), { opacity: 0, x: 0, y: 0, clipPath: 'inset(0px 0px 0px 0px)' });
			utils.set(q('.tile.t-n-full'), { opacity: 1 });
			// The snapshot mark keeps the angle the last loop left it at,
			// folded into 0 to 360, so it never cuts to another angle (I-401).
			const cam = q('.rail .cam');
			const deg = cam.length ? Number(utils.get(cam[0], 'rotate', false)) || 0 : 0;
			utils.set(cam, { rotate: ((deg % 360) + 360) % 360 });
			fire = false;
			ask = false;
			restored = false;
			pic.classList.remove('wrecked', 'stopped');
		}

		function cycle() {
			if (dead || !running) return;
			if (!visible) {
				tl = null;
				return;
			}
			// eslint-disable-next-line svelte/no-dom-manipulating
			fly.replaceChildren();
			measure();
			tick().then(() => !dead && running && run());
		}

		// A copy of the machine as it is now, flying from where it stands
		// into a tile's place on the rail and folding to that tile's height.
		// It is drawn only inside the rail's body, so it never covers the
		// machine, the gap or a head; the wire carries it across. It shrinks
		// early (outCubic) and travels late (inOutCubic), lands by at+640,
		// and is gone by at+750, before the tile it becomes fades in.
		function shoot(t: ReturnType<typeof createTimeline>, at: number, slot: ReturnType<typeof rel>) {
			const win = pic.querySelector<HTMLElement>('.lw')!;
			const W = rel(win);
			const body = rel(pic.querySelector('.railbody')!);
			const k = slot.w / W.w;
			const clip = document.createElement('div');
			clip.className = 'shotclip';
			Object.assign(clip.style, {
				left: `${body.l}px`,
				top: `${body.t}px`,
				width: `${body.w}px`,
				height: `${body.h}px`
			});
			const shot = document.createElement('div');
			shot.className = 'shot';
			Object.assign(shot.style, {
				left: `${W.l - body.l}px`,
				top: `${W.t - body.t}px`,
				width: `${W.w}px`,
				height: `${W.h}px`,
				opacity: '0'
			});
			clip.appendChild(shot);
			const fold = `inset(0px 0px ${W.h - slot.h / k}px 0px)`;
			t.call(() => {
				const c = win.cloneNode(true) as HTMLElement;
				c.querySelector('.shutter')?.remove();
				c.querySelectorAll('.skill-in, .wallhit, .stopper').forEach((e) => e.remove());
				shot.replaceChildren(c);
				// eslint-disable-next-line svelte/no-dom-manipulating
				fly.appendChild(clip);
			}, at)
				.add(shot, { opacity: 1, duration: CUT }, at + 10)
				.add(
					shot,
					{
						x: { to: slot.l - W.l, duration: 620, ease: 'inOutCubic' },
						y: { to: slot.t - W.t, duration: 620, ease: 'inOutCubic' },
						scale: { to: k, duration: 620, ease: 'outCubic' },
						clipPath: {
							from: 'inset(0px 0px 0px 0px)',
							to: fold,
							duration: 620,
							ease: 'outCubic'
						}
					},
					at + 20
				)
				.add(shot, { opacity: 0, duration: 150, ease: 'inQuad' }, at + 600);
		}

		// A wire lighting from one end, then letting go.
		function light(
			t: ReturnType<typeof createTimeline>,
			sel: string,
			at: number,
			hold: number,
			from: 'start' | 'end'
		) {
			const el = q(sel);
			const axis = isWide() ? 'scaleX' : 'scaleY';
			return t
				.call(() => el.forEach((e) => e.classList.toggle('from-end', from === 'end')), at)
				.add(el, { opacity: [0, 1], [axis]: [0, 1], duration: 380, ease: 'outCubic' }, at)
				.add(el, { opacity: 0, duration: 300, ease: 'inQuad' }, at + hold);
		}

		function run() {
			// .live hands the picture from the CSS first paint to the inline
			// styles pre() writes, in the same frame.
			pre();
			pic.classList.add('live');
			const wide = isWide();

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
			// Its path keeps it off both commands. It flies under the laptop's
			// window, so it is first seen leaving the laptop's wall. Wide, it
			// rises at once into the clear band between repose run and your
			// snapshot command, crosses there, and settles onto the machine's
			// row once past the laptop. Stacked, the commands fill the gap, so
			// it comes into view on the machine, over its head, and settles.
			const runChip = rel(pic.querySelector('.hop .pic-chip')!);
			const askChip = rel(pic.querySelector('.ask .pic-chip')!);
			// Just over repose run, so it is still behind the laptop's head
			// until it clears the laptop's wall.
			const band = Math.max(runChip.t - 4 - a.h, askChip.b + 2) - a.t;
			const flight =
				wide && runChip.t - askChip.b >= a.h + 4
					? {
							x: { to: b.l - a.l, duration: 900, ease: 'inOutCubic' },
							y: [
								{ to: band, duration: 300, ease: 'outQuad' },
								{ to: b.t - a.t, duration: 600, ease: 'inOutQuad' }
							]
						}
					: null;

			// The rail's places: the newest tile on top, filling what the
			// stubs under it leave (one stub under 21:25, two under the stop
			// tile), so the rail is full at every beat.
			const body = rel(pic.querySelector('.railbody')!);
			const H = body.h;
			const slot = (n: number) => ({ ...body, h: H - 32 * n, b: body.b - 32 * n });
			const W = rel(pic.querySelector('.lw')!);
			const stubU = rel(pic.querySelector('.tile.t-u-stub')!);
			const pg = pic.querySelector<HTMLElement>('.lw .sys.s0 .cnt')!;
			const pgCount = sys[0].count!;

			// The skill: out of the globe, along its wire, to its place by
			// the agent (its home). On a phone it comes up from under.
			const home = rel(pic.querySelector('.skill-in')!);
			const globe = rel(pic.querySelector('.globe')!);
			const src = wide
				? { x: globe.l - 6 - home.r, y: globe.cy - home.cy }
				: { x: globe.r - home.r, y: globe.t - 4 - home.b };
			const knock: Record<string, number[]> = wide ? { x: [0, -8, 0] } : { y: [0, -8, 0] };

			const T = {
				run: 250,
				fly: 400,
				land: 1300,
				work: 1800,
				ask: 3700,
				shot: 4000,
				reach: 5500,
				skill: 5850,
				rogue: 7300,
				strike: 7800,
				lunge: 8800,
				hit: 9500,
				stop: 10000,
				stopshot: 10350,
				restore: 11450,
				back: 12350,
				seam: 17350,
				end: 18400
			};

			const t = createTimeline({ autoplay: false, onComplete: () => cycle() })
				// repose run: the repo row travels, the project rows light.
				// The laptop's row dims while its copy is away, so the name is
				// never drawn twice at full ink.
				.call(() => (fire = true), T.run)
				.call(() => (fire = false), T.run + 520)
				.call(() => from.classList.add('lift'), T.fly);
			if (flight) t.add(c, { opacity: 1, duration: CUT }, T.fly).add(c, flight, T.fly);
			else
				t.add(c, { x: b.l - a.l, y: b.t - a.t - 10, duration: CUT }, T.fly + 200)
					.add(c, { opacity: [0, 1], duration: 200, ease: 'outQuad' }, T.fly + 240)
					.add(c, { y: b.t - a.t, duration: 600, ease: 'outCubic' }, T.fly + 240);
			t.add(q('.m-proj.head'), { opacity: 1, duration: CUT }, T.land)
				.add(c, { opacity: 0, duration: CUT }, T.land + 16)
				.call(() => from.classList.remove('lift'), T.land + 16)
				.add(
					q('.m-proj.kid'),
					{ opacity: [0.22, 1], duration: 320, delay: stagger(90), ease: 'outCubic' },
					T.land + 60
				);
			// Good work: each file lights as the agent writes it, and gains
			// its git letter; the new test's row opens in the room it kept.
			t.add(
				q('.lw .agent'),
				{ y: [0, -2, 0, -2, 0, -2, 0], duration: 1600, ease: 'inOutCubic' },
				T.work
			);
			kids.forEach((k, i) => {
				const at = T.work + i * 600;
				if (k.st === 'U')
					t.add(
						q('.lw .kid.nu'),
						{ opacity: [0, 1], y: [-6, 0], duration: 320, ease: 'outCubic' },
						at
					);
				t.add(
					q(`.lw .k${i} .wk`),
					{ opacity: [0, 1, 0], duration: 1000, ease: 'inOutCubic' },
					at
				).add(
					q(`.lw .k${i} .st`),
					{ opacity: [0, 1], scale: [0.6, 1], duration: 300, ease: 'outBack' },
					at + 250
				);
			});
			// A snapshot lands on the rail: the copy comes in along the wire
			// (shoot), the tile it lands over folds to its head under it
			// while it covers that place, and only once the copy is gone does
			// the new tile fade in, so the two drawings never overlap.
			function land(at: number, n: number, full: string, stub: string, next: string) {
				shoot(t, at + 150, slot(n));
				t.add(
					q(full),
					{
						y: H - 28 - 32 * (n - 1),
						clipPath: ['inset(0px 0px 0px 0px)', `inset(0px 0px ${H - 32 * (n - 1) - 28}px 0px)`],
						duration: 220,
						ease: 'inOutCubic'
					},
					at + 630
				)
					.add(q(full), { opacity: 0, duration: CUT }, at + 850)
					.add(q(stub), { opacity: 1, duration: CUT }, at + 850)
					.add(q(next), { opacity: [0, 1], duration: 250, ease: 'outQuad' }, at + 900);
			}
			// You ask for a snapshot: your command fires, the shutter washes
			// the machine and its copy lands on the rail above the nightly one.
			t.call(() => (ask = true), T.ask)
				.call(() => (ask = false), T.ask + 520)
				.add(q('.rail .cam'), { rotate: '+=90', duration: 420, ease: 'outQuad' }, T.shot)
				.add(q('.lw .shutter'), { opacity: [0, 0.9, 0], duration: 460, ease: 'outQuad' }, T.shot);
			light(t, '.w-snap .lit', T.shot + 100, 900, 'start');
			land(T.shot, 1, '.tile.t-n-full', '.tile.t-n-stub', '.tile.t-u-full');
			// Only now: it reaches the internet, and the malicious skill
			// comes in along the same wire.
			light(t, '.w-net .lit', T.reach, 1550, 'start');
			t.add(q('.skill-in'), { ...src, duration: CUT }, T.skill)
				.add(
					q('.skill-in'),
					{ opacity: [0, 1], scale: [0.6, 1], duration: 300, ease: 'outBack' },
					T.skill
				)
				.add(q('.skill-in'), { x: 0, y: 0, duration: 1000, ease: 'inOutCubic' }, T.skill + 350)
				// The agent turns rogue and wrecks what git can't bring back.
				.add(q('.lw .agent .red'), { opacity: [0, 1], duration: 350 }, T.rogue)
				.add(
					q('.lw .agent'),
					{ x: [0, -2, 2, -1, 0], duration: 480, ease: 'inOutCubic' },
					T.rogue + 200
				)
				// .wrecked greys the struck names (a 200ms colour change).
				.call(() => pic.classList.add('wrecked'), T.strike)
				.add(q('.lw .kid .xbg'), { opacity: [0, 1], duration: 250, delay: stagger(120) }, T.strike)
				.add(
					q('.lw .kid .strike'),
					{ scaleX: [0, 1], duration: 380, delay: stagger(120), ease: 'outCubic' },
					T.strike + 80
				)
				.add(
					q('.lw .sys .xbg'),
					{ opacity: [0, 1], duration: 250, delay: stagger(160) },
					T.strike + 420
				);
			// The database counts down to zero in its own value, as
			// ComesBack's do; node's version goes, then "not found" comes.
			const o = { v: pgCount.n };
			t.call(() => pg.classList.add('falling'), T.strike + 420)
				.add(
					o,
					{
						v: [pgCount.n, 0],
						duration: 600,
						ease: 'outQuad',
						onUpdate: () => {
							pg.textContent = `${Math.round(o.v).toLocaleString('en-US')} ${pgCount.unit}`;
						}
					},
					T.strike + 420
				)
				.add(q('.lw .sys.s1 .ok'), { opacity: 0, duration: 300, ease: 'inQuad' }, T.strike + 580)
				.add(q('.lw .sys.s1 .bad'), { opacity: [0, 1], duration: 240 }, T.strike + 880)
				// The skill runs at your laptop: its trail draws to the wall,
				// where it is stopped. The laptop itself is never touched.
				.add(q('.skill-in'), { ...knock, duration: 380, ease: 'outQuad' }, T.lunge)
				.add(
					q('.trail-m'),
					{ strokeDashoffset: [1, 0], duration: 700, ease: 'inOutCubic' },
					T.lunge
				)
				.add(q('.wallhit'), { opacity: [0, 1], duration: 200, ease: 'outQuad' }, T.hit)
				.add(
					q('.stopper'),
					{ opacity: [0, 1], scale: [0.6, 1], duration: 300, ease: 'outBack' },
					T.hit
				)
				// The machine stops: its dot greys and the agent's process goes,
				// so its row dims; the skill, its trail and the cross stay
				// until the restore.
				.call(() => pic.classList.add('stopped'), T.stop)
				.add(q('.lw .crew, .lw .mode'), { opacity: 0.35, duration: 400, ease: 'inQuad' }, T.stop)
				// A stop snapshots first, wreck and all: a stop tile lands.
				.add(q('.rail .cam'), { rotate: '+=90', duration: 420, ease: 'outQuad' }, T.stopshot)
				.add(
					q('.lw .shutter'),
					{ opacity: [0, 0.9, 0], duration: 460, ease: 'outQuad' },
					T.stopshot
				);
			light(t, '.w-snap .lit', T.stopshot + 100, 900, 'start');
			land(T.stopshot, 2, '.tile.t-u-full', '.tile.t-u-stub', '.tile.t-s-full');
			// Restored from 21:25: its tile lights, the mark rewinds a full
			// turn, and the snapshot grows back into the machine from the
			// wire's end (drawn only inside the machine's walls).
			t.call(() => (restored = true), T.restore).add(
				q('.wallhit, .stopper, .skill-in, .trail'),
				{ opacity: 0, duration: 400, ease: 'inQuad' },
				T.restore
			);
			light(t, '.w-snap .lit', T.restore, 900, 'end');
			const k = stubU.w / W.w;
			t.add(
				q('.rail .cam'),
				{ rotate: '-=360', duration: 1000, ease: 'inOutCubic' },
				T.restore + 50
			)
				.add(
					q('.back'),
					{
						opacity: 1,
						x: stubU.l - W.l,
						y: stubU.t - W.t,
						scale: k,
						clipPath: `inset(0px 0px ${W.h - stubU.h / k}px 0px)`,
						duration: CUT
					},
					T.restore + 100
				)
				.add(
					q('.back'),
					{
						x: { to: 0, duration: 800, ease: 'inOutCubic' },
						y: { to: 0, duration: 800, ease: 'inOutCubic' },
						scale: { to: 1, duration: 800, ease: 'inCubic' },
						clipPath: { to: 'inset(0px 0px 0px 0px)', duration: 800, ease: 'inCubic' }
					},
					T.restore + 150
				)
				.call(() => {
					pic.classList.remove('wrecked');
					pg.classList.remove('falling');
					pg.textContent = sys[0].ok;
				}, T.back)
				.add(q('.lw .strike'), { scaleX: 0, duration: 120 }, T.back)
				.add(q('.lw .xbg, .lw .agent .red, .lw .sys .bad'), { opacity: 0, duration: 120 }, T.back)
				.add(q('.lw .sys .ok'), { opacity: 1, duration: 120 }, T.back)
				.add(q('.back'), { opacity: [1, 0], duration: 500, ease: 'inQuad' }, T.back + 50)
				// Started again: the dot turns green and the agent is back.
				.call(() => pic.classList.remove('stopped'), T.back + 150)
				.add(
					q('.lw .crew, .lw .mode'),
					{ opacity: 1, duration: 400, ease: 'outQuad' },
					T.back + 250
				)
				// Rest on the restored machine, then the seam: the project rows
				// dim and the newer tiles go. The nightly one, folded at the
				// rail's foot since 21:25, rises and unfolds behind the stop
				// tile, which then rolls up off it from the bottom, so the rail
				// always shows one whole drawing.
				.call(() => (restored = false), T.seam)
				.add(q('.m-proj'), { opacity: 0.22, duration: 450, ease: 'inQuad' }, T.seam)
				.add(
					q('.lw .kid.nu, .lw .st, .tile.t-u-stub'),
					{ opacity: 0, duration: 450, ease: 'inQuad' },
					T.seam
				)
				.add(q('.tile.t-n-full'), { opacity: 1, duration: CUT }, T.seam)
				.add(q('.tile.t-n-stub'), { opacity: 0, duration: CUT }, T.seam + 16)
				.add(
					q('.tile.t-n-full'),
					{ y: 0, clipPath: 'inset(0px 0px 0px 0px)', duration: 550, ease: 'inOutCubic' },
					T.seam + 50
				)
				.add(
					q('.tile.t-s-full'),
					{ clipPath: `inset(0px 0px ${H - 64}px 0px)`, duration: 500, ease: 'inOutCubic' },
					T.seam + 600
				)
				.add(q('.tile.t-s-full'), { opacity: 0, duration: CUT }, T.seam + 1100)
				.add({ duration: 1 }, T.end);

			tl = t;
			fresh = false;
			t.play();
			if (resume) t.seek(resume);
			resume = 0;
		}

		function start() {
			running = true;
			picture.start(
				pic,
				(m) => {
					visible = m === 'play';
					if (!visible) tl?.pause();
					else if (tl && !fresh) tl.play();
					else cycle();
				},
				0.35
			);
		}

		// A visitor can ask for less motion while the page is open: stop on
		// the still frame then, and start the story if they allow it again.
		const unwatch = watchReducedMotion((r) => {
			reduce = r;
			if (r) {
				if (running) stop();
			} else if (!running) start();
		});

		return () => {
			dead = true;
			picture.stop();
			tl?.pause();
			unwatch();
			unwidth();
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

<!-- The snapshot mark: the pinwheel of the shape set (LANDING.md, "Shape
     language"). -->
{#snippet camIcon(size: number, cls: string)}
	<svg viewBox="0 0 16 16" width={size} height={size} aria-hidden="true" class={cls}>
		<path d="M8 8V0A8 8 0 0 0 0 8Z" fill="currentColor" />
		<path d="M8 8V16A8 8 0 0 0 16 8Z" fill="currentColor" opacity="0.45" />
	</svg>
{/snippet}

{#snippet snapMark()}{@render camIcon(16, 'cam')}{/snippet}

{#snippet stopIcon()}
	<svg viewBox="0 0 16 16" width="18" height="18" aria-hidden="true">
		<circle cx="8" cy="8" r="6.5" fill="var(--surface)" />
		<g fill="none" stroke="currentColor" stroke-width="1.5">
			<circle cx="8" cy="8" r="6" />
			<path d="M3.8 12.2l8.4-8.4" />
		</g>
	</svg>
{/snippet}

<!-- The machine in a snapshot tile: its rows as shapes, the contents
     hidden at this size, as "Let it break the whole machine" draws them.
     The stop tile's machine is the wrecked one, its agent gone. -->
{#snippet glyph(when: 'night' | 'work' | 'wreck')}
	<span class="gl" class:wreck={when === 'wreck'} class:night={when === 'night'}>
		<i></i>
		<i class="gr"><b class="gi acc"></b><b class="gb" style:width="30%"></b></i>
		{#each kids as k (k.n)}
			<i class="gr kid" class:nu={k.st === 'U'}
				><b class="gi"></b><b class="gb" style:width="{k.n.length * 2.6}%"></b>{#if k.dir}<b
						class="gp"
						style:width="{k.dir.length * 1.5}%"
					></b>{/if}<b class="gs" class:u={k.st === 'U'}></b></i
			>
		{/each}
		<i class="gsep"></i>
		{#each sys as m (m.n)}
			<i class="gr"
				><b class="gi"></b><b class="gb" style:width="{m.n.length * 2.8}%"></b>{#if m.dir}<b
						class="gp"
						style:width="{m.dir.length * 1.5}%"
					></b>{/if}<b class="gv"></b></i
			>
		{/each}
		<i class="gsep"></i>
		<i class="gr"><b class="gc"></b><b class="gm"></b></i>
	</span>
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
	<div class="win" class:lw={live}>
		<WinHead who="machine" />
		<ul class="rows">
			<li class="fr head" class:m-proj={live} data-to={live ? 'repo' : undefined}>
				<svg viewBox="0 0 10 10" width="9" height="9" aria-hidden="true" class="chev">
					<path d="M2 3.5l3 3 3-3" fill="none" stroke="currentColor" stroke-width="1.3" />
				</svg>
				{@render repoIcon()}<span class="fname">{repo}/</span>
			</li>
			{#each kids as k, i (k.n)}
				<li class="fr kid k{i}" class:nu={k.st === 'U'} class:m-proj={live && k.st !== 'U'}>
					<i class="wk"></i><i class="xbg"></i>
					<span class="dim">{@render fileIcon()}</span>
					<span class="fname dim">{k.n}</span>
					{#if k.dir}<span class="path dim">{k.dir}</span>{/if}
					<span class="st st-{k.st}">{k.st}<i class="strike"></i></span>
				</li>
			{/each}
			{#each sys as m, i (m.n)}
				<li class="fr sys s{i}">
					<i class="xbg"></i>
					{@render sysIcon(m.icon)}<span class="fname">{m.n}</span>
					{#if m.dir}<span class="path">{m.dir}</span>{/if}
					<span class="val"
						>{#if m.count}<span class="ok cnt">{m.ok}</span>{:else}<span class="ok">{m.ok}</span
							><span class="bad">{m.bad}</span>{/if}</span
					>
				</li>
			{/each}
		</ul>
		<!-- Claude Code: its mark, and its own footer line for the mode. -->
		<div class="agentrow">
			<span class="crew">
				<span class="agent">
					<svg viewBox="0 0 24 24" width="22" height="22" aria-hidden="true">
						{#each agent.paths as d (d)}
							<path {d} fill-rule="evenodd" />
						{/each}
					</svg>
					<svg viewBox="0 0 24 24" width="22" height="22" aria-hidden="true" class="red">
						{#each agent.paths as d (d)}
							<path {d} fill-rule="evenodd" />
						{/each}
					</svg>
				</span>
			</span>
			<span class="mode">⏵⏵ bypass permissions on</span>
			{#if live}{@render skillChip('skill-in')}{/if}
		</div>
		{#if live}
			<span class="wallhit"></span>
			<span class="stopper">{@render stopIcon()}</span>
			<i class="shutter"></i>
		{/if}
	</div>
{/snippet}

<div class="hero-frame" use:picture.frame>
	<PauseButton {picture} />
	<div class="hero-pic" role="img" aria-label={label} aria-describedby="hero-story" bind:this={pic}>
		<p id="hero-story" hidden>{story}</p>

		<!-- Your command for a snapshot, typed on the laptop. -->
		<div class="ask" aria-hidden="true">
			<span class="pic-chip" class:fire={ask}>repose snapshots create</span>
			<span class="wire arrow"></span>
		</div>

		<div class="side laptop" aria-hidden="true">
			<div class="win">
				<WinHead who="laptop" />
				<ul class="rows">
					<li class="fr head repo" data-from="repo">
						<svg viewBox="0 0 10 10" width="9" height="9" aria-hidden="true" class="chev side-chev">
							<path d="M3.5 2l3 3-3 3" fill="none" stroke="currentColor" stroke-width="1.3" />
						</svg>
						{@render repoIcon()}<span class="fname">{repo}/</span>
					</li>
					{#each privs as p (p.n)}
						<li class="fr priv">{@render privIcon(p.icon)}<span class="fname">{p.n}</span></li>
					{/each}
				</ul>
			</div>
		</div>

		<div class="hop" aria-hidden="true">
			<span class="wire"></span>
			<span class="pic-chip" class:fire>repose run</span>
			<span class="wire arrow"></span>
		</div>

		<div class="mwin" aria-hidden="true">
			{@render machineWin(true)}
			<!-- The snapshot restored, growing back over the machine: drawn
			     only inside the machine's walls, so it never crosses the gap
			     or the rail; the wire carries it there. -->
			<div class="backclip"><div class="back">{@render machineWin(false)}</div></div>
		</div>

		<div class="side right" aria-hidden="true">
			<i class="link w-snap"><b class="lit"></b></i>
			<div class="rail win">
				<WinHead who="snapshots" mark={snapMark} />
				<div class="railbody">
					<SnapshotTile class="tile t-n-full" time="03:00" cause="nightly"
						>{@render glyph('night')}</SnapshotTile
					>
					<SnapshotTile class="tile t-u-full" time="21:25" cause="user"
						>{@render glyph('work')}</SnapshotTile
					>
					<SnapshotTile class="tile t-s-full" time="21:31" cause="stop"
						>{@render glyph('wreck')}</SnapshotTile
					>
					<SnapshotTile class="tile t-u-stub" time="21:25" cause="user" stub lit={restored} />
					<SnapshotTile class="tile t-n-stub" time="03:00" cause="nightly" stub />
				</div>
			</div>
			<div class="net">
				<i class="link w-net"><b class="lit"></b></i>
				<span class="globe"
					><Shape kind="sphere" /><svg viewBox="0 0 24 24" class="meridians" aria-hidden="true"
						><g fill="none" stroke="currentColor" stroke-width="0.9"
							><ellipse cx="12" cy="12" rx="4.2" ry="11.6" /><path d="M0.4 8h23.2M0.4 16h23.2" /></g
						></svg
					></span
				>
			</div>
		</div>

		{#if geo}
			<svg class="lines" width={geo.w} height={geo.h} aria-hidden="true">
				<defs>
					<mask id="hero-trail" maskUnits="userSpaceOnUse" x="0" y="0" width={geo.w} height={geo.h}>
						<path class="trail-m" d={geo.trail} pathLength="1" />
					</mask>
				</defs>
				<path class="trail" d={geo.trail} mask="url(#hero-trail)" />
			</svg>
		{/if}

		<div class="fly" aria-hidden="true" bind:this={fly}></div>
	</div>
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
		/* The gap between the machine and the right-hand stack, which the
		   two wires cross. */
		--gap: 48px;
		/* A row and the room a panel keeps above its first row: with the
		   window head (--shot-head, WinHead), every height in the picture
		   is made of these. */
		--row: 28px;
		--pad: 9px;
		/* The agent's row, at the machine's floor; the globe's row on the
		   right-hand stack is as tall. */
		--agent: 40px;
		/* The dim the project rows rest at before repose run lights them. */
		--dimmed: 0.22;
		position: relative;
		display: grid;
		min-width: 0;
		font-size: 13px;
		color: var(--pic-ink);
	}
	.side,
	.mwin {
		display: flex;
		min-width: 0;
	}
	.mwin {
		position: relative;
		container-type: inline-size;
	}

	/* Windows, as in "Your laptop's setup" */
	.win {
		position: relative;
		flex: 1;
		display: flex;
		flex-direction: column;
		min-width: 0;
		border: 1px solid var(--rule-strong);
		border-radius: var(--radius);
		background: var(--surface);
		color: var(--pic-ink);
		font-size: 13px;
	}
	/* The heads are WinHead's; the machine's wall is landing.css's
	   --pic-wall, as on every machine window (I-673). The window's own
	   corners clip its head's ground. */
	.win > :global(.shot-head) {
		border-radius: calc(var(--radius) - 1px) calc(var(--radius) - 1px) 0 0;
	}
	.hero-pic:global(.stopped) .lw :global(.dot) {
		background: var(--pic-faint);
	}
	.rows {
		margin: 0;
		padding: var(--pad) 0 0;
		list-style: none;
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
	/* The folder a file sits in, as in "Your laptop's setup". */
	.path {
		position: relative;
		flex: 0 1 auto;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		font-size: 12px;
		/* Context, muted: --pic-faint holds 4.5:1 on the surface and on a
		   red row. */
		color: var(--pic-faint);
	}
	/* git's letter for the file, as OneCommand draws it: a new file in a
	   line count's green, a changed one the grey of a line at rest. */
	.st {
		position: relative;
		display: inline-block;
		flex: none;
		width: 12px;
		margin-left: auto;
		text-align: center;
		font-family: var(--font-mono);
		font-size: 12px;
		font-weight: 600;
	}
	.st-M {
		color: var(--pic-dim);
	}
	.st-U {
		color: var(--pic-add);
	}
	/* A file being written: the row lights in the accent. */
	.wk {
		position: absolute;
		inset: 0;
		opacity: 0;
		background: color-mix(in oklab, var(--pic-accent) 9%, var(--surface));
		box-shadow: inset 2px 0 0 var(--pic-accent);
	}
	.laptop .rows {
		padding-bottom: var(--pad);
	}
	/* Your repo, then the rest of your life. */
	.repo {
		margin-bottom: 7px;
	}
	/* The repo row while its copy is away on repose run. */
	.repo,
	.repo * {
		transition: color 0.2s;
	}
	.repo:global(.lift),
	.repo:global(.lift) * {
		color: var(--pic-faint);
	}
	.repo::after {
		content: '';
		position: absolute;
		left: 12px;
		right: 14px;
		bottom: -4px;
		border-top: 1px solid var(--rule);
	}

	/* The machine's own state, below the repo, and the agent below that,
	   each after a rule. */
	.kid.nu {
		overflow: hidden;
	}
	.kid + .sys,
	.agentrow {
		margin-top: 7px;
	}
	.kid + .sys::before,
	.agentrow::before {
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
	/* The still frame is the restored machine: the wreck's values are
	   drawn only by the timeline, which sets them inline. */
	.bad,
	.cnt:global(.falling) {
		font-weight: 600;
		color: var(--pic-stop);
	}
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
		left: -3px;
		right: -3px;
		top: calc(50% - 1px);
		border-top: 1.5px solid var(--pic-stop);
		transform: scaleX(0);
		transform-origin: left center;
	}
	/* Struck work greys to --pic-faint at full opacity, which holds 4.5:1
	   on the red row. A colour change of 200ms. */
	.lw .kid .fname,
	.lw .kid .path,
	.lw .st {
		transition: color 0.2s ease-out;
	}
	.hero-pic:global(.wrecked) .lw .kid .fname,
	.hero-pic:global(.wrecked) .lw .kid .path,
	.hero-pic:global(.wrecked) .lw .st {
		color: var(--pic-faint);
	}
	.hero-pic:global(.wrecked) .lw .sys .fi {
		color: var(--pic-stop);
	}

	/* The agent's row: Claude Code's mark and its footer line for the
	   permission mode, in its own pink; the skill docks at the row's end. */
	.agentrow {
		position: relative;
		display: flex;
		flex: none;
		align-items: center;
		gap: 10px;
		height: var(--agent);
		padding: 0 14px 0 12px;
	}
	.mode {
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
		height: 22px;
		padding: 0 7px;
		border: 1px solid var(--pic-stop);
		border-radius: var(--radius);
		background: var(--surface);
		font-family: var(--font-mono);
		font-size: 12px;
		color: var(--pic-stop);
		white-space: nowrap;
	}
	/* The skill's home is its place by the agent; it is drawn only while
	   the loop runs. */
	.skill-in {
		position: absolute;
		right: 14px;
		top: 50%;
		translate: 0 -50%;
		z-index: 3;
		opacity: 0;
	}
	/* Where the skill is stopped: on the wall facing the laptop, level
	   with the rule over the agent's row. */
	.wallhit {
		position: absolute;
		opacity: 0;
	}
	.stopper {
		position: absolute;
		z-index: 4;
		display: grid;
		color: var(--pic-stop);
		opacity: 0;
	}
	.shutter {
		position: absolute;
		inset: 0;
		opacity: 0;
		background: color-mix(in oklab, var(--pic-accent) 14%, transparent);
		box-shadow: inset 0 0 0 2px var(--pic-accent);
		pointer-events: none;
	}
	/* The restored machine, over the machine; only the loop shows it. */
	.backclip {
		position: absolute;
		inset: 0;
		z-index: 5;
		overflow: hidden;
		pointer-events: none;
	}
	.back {
		position: absolute;
		inset: 0;
		display: flex;
		opacity: 0;
		transform-origin: 0 0;
		pointer-events: none;
	}
	.back .win {
		outline: 1.5px solid var(--pic-accent);
		outline-offset: -1.5px;
	}
	.back .xbg,
	.back .wk,
	.back .strike,
	.back .agent .red {
		display: none;
	}

	/* The right-hand stack: the snapshots panel, then the internet on the
	   floor as a bare globe (no one's machine, so no window). */
	.side.right {
		position: relative;
		flex-direction: column;
		gap: 10px;
	}
	.rail {
		flex: 1;
	}
	.railbody {
		position: relative;
		flex: 1;
		margin: 9px 10px 10px;
	}
	/* The tiles: the newest on top with its machine, the older ones under
	   it as heads (SnapshotTile, I-666). Each sits in its place; the loop
	   moves them between places by translate and folds them by a clip. */
	.railbody :global(.tile) {
		position: absolute;
		left: 0;
		right: 0;
		top: 0;
		display: flex;
		flex-direction: column;
		height: 100%;
	}
	.railbody :global(.tile .s-mini) {
		flex: 1;
		min-height: 0;
	}
	/* The newest tile fills what the stubs under it leave: the nightly
	   one alone fills the rail, 21:25 sits over one stub, the stop tile
	   over two. */
	/* A stub is a tile's head and edges: 27px and 2px (SnapshotTile, one
	   head height everywhere, I-673). Stubs stand 3px apart, so each
	   takes 32px of the rail, the step the loop's slot() moves by. */
	.railbody :global(.t-u-full) {
		height: calc(100% - 32px);
	}
	.railbody :global(.t-s-full) {
		height: calc(100% - 64px);
	}
	.railbody :global(.t-u-stub) {
		top: calc(100% - 61px);
		height: 29px;
	}
	.railbody :global(.t-n-stub) {
		top: calc(100% - 29px);
		height: 29px;
	}
	/* The still frame: the stop tile newest, 21:25 lit under it, then the
	   nightly one. */
	.railbody :global(.t-n-full),
	.railbody :global(.t-u-full) {
		opacity: 0;
	}

	/* The machine in a tile, as shapes: its rows at the heights the
	   machine gives them (the top pad, four project rows, a rule, the two
	   services, a rule, the agent's row), so the miniature is the machine
	   at the tile's size, whatever that size is, and the copy that flies
	   onto the rail fades into it in place. */
	.gl {
		display: grid;
		grid-template-rows: 9fr repeat(4, 28fr) 7fr repeat(2, 28fr) 7fr 40fr;
		height: 100%;
		padding: 0 7% 0 5%;
	}
	.gr {
		display: flex;
		align-items: center;
		gap: 4%;
		min-height: 0;
	}
	.gr.kid {
		padding-left: 6%;
	}
	.gr > b {
		flex: none;
		height: 3px;
		border-radius: 1px;
	}
	.gi {
		width: 3px;
		background: var(--pic-dim);
	}
	.gi.acc {
		background: var(--pic-accent);
	}
	.gb {
		background: var(--pic-faint);
		opacity: 0.8;
	}
	.gp {
		background: var(--pic-faint);
		opacity: 0.35;
	}
	.gr > .gs,
	.gr > .gv {
		margin-left: auto;
	}
	.gs {
		width: 3px;
		background: var(--pic-dim);
	}
	.gs.u {
		background: var(--pic-add);
	}
	.gv {
		width: 16%;
		background: var(--pic-faint);
		opacity: 0.6;
	}
	.gr > .gc {
		width: 5px;
		height: 5px;
		border-radius: 50%;
		background: var(--claude);
	}
	.gm {
		width: 42%;
		background: var(--mode);
		opacity: 0.6;
	}
	.gsep {
		align-self: center;
		height: 1px;
		background: var(--rule);
	}
	/* The nightly one is from before the agent's work: no letters yet,
	   and no test file. */
	.night .gs,
	.night .gr.nu {
		visibility: hidden;
	}
	.wreck .gr.kid .gb,
	.wreck .gr.kid .gp {
		opacity: 0.3;
	}
	.wreck .gs,
	.wreck .gc,
	.wreck .gm {
		visibility: hidden;
	}
	.wreck .gv {
		background: var(--pic-stop);
		opacity: 0.85;
	}

	/* The internet: the globe, on the stack's floor, level with the
	   agent's row. */
	.net {
		position: relative;
		display: flex;
		flex: none;
		align-items: center;
		justify-content: center;
		height: 40px;
	}
	/* The internet is the sphere of the shape set, with a globe's meridians
	   drawn over it in paper so it reads as the world and not a stone. */
	.globe {
		position: relative;
		z-index: 1;
		flex: none;
		width: 40px;
		height: 40px;
		border-radius: 50%;
		overflow: hidden;
	}
	.meridians {
		position: absolute;
		inset: 0;
		color: var(--sh-paper);
		opacity: 0.8;
	}

	/* The permanent wires: a --pic-dim hairline with a dot on each end;
	   the loop only lights them (.lit, the accent, or red for the reach). */
	.link {
		position: absolute;
		display: block;
		border-top: 1px solid var(--pic-dim);
	}
	.w-snap::before,
	.w-snap::after,
	.w-net::before {
		content: '';
		position: absolute;
		top: -3px;
		width: 5px;
		height: 5px;
		border-radius: 50%;
		background: var(--pic-dim);
	}
	.w-snap::before,
	.w-net::before {
		left: -2.5px;
	}
	.w-snap::after {
		right: -2.5px;
	}
	.link .lit {
		position: absolute;
		left: 0;
		right: 0;
		top: -1px;
		height: 0;
		border-top: 1.5px solid var(--pic-accent);
		opacity: 0;
		transform-origin: left center;
	}
	.link :global(.lit.from-end) {
		transform-origin: right center;
	}
	.w-net .lit {
		border-color: var(--pic-stop);
	}
	/* Wide: the snapshot wire crosses the gap level with the newest tile's
	   head and the machine's first row; the globe wire runs from the wall
	   at the agent's row to the globe. */
	.w-snap {
		left: calc(-1 * var(--gap));
		width: var(--gap);
		top: calc(2px + var(--shot-head) + 9px + 13px);
	}
	.w-net {
		left: calc(-1 * var(--gap));
		right: 50%;
		top: 50%;
	}

	/* The commands between the panels: repose run, and yours above the
	   laptop for a snapshot. */
	.hop,
	.ask {
		position: relative;
		z-index: 4;
		display: flex;
		align-items: center;
	}
	.hop {
		flex-direction: column;
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
	.ask {
		flex-direction: column;
	}

	/* The skill's trail, drawn from the skill to the wall. */
	.lines {
		position: absolute;
		left: 0;
		top: 0;
		z-index: 3;
		overflow: visible;
		pointer-events: none;
	}
	.trail {
		fill: none;
		stroke: var(--pic-stop);
		stroke-width: 1.5;
		stroke-dasharray: 4 3;
		opacity: 0;
	}
	/* The trail's mask: white shows, so the dashed line appears as the
	   mask's one solid dash slides along it. Not a colour anyone sees. */
	.trail-m {
		fill: none;
		stroke: white;
		stroke-width: 6;
		stroke-dasharray: 1;
		stroke-dashoffset: 1;
	}
	/* Only the loop draws the trail; the still frame has no skill. */
	.hero-pic:not(:global(.live)) .trail {
		display: none;
	}

	/* The copies in flight (animated only). */
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
	.fly :global(.shotclip) {
		position: absolute;
		overflow: hidden;
	}
	.fly :global(.shot) {
		position: absolute;
		display: flex;
		transform-origin: 0 0;
		will-change: transform;
	}
	.fly :global(.shot > .win) {
		outline: 1.5px solid var(--pic-accent);
		outline-offset: -1.5px;
	}

	/* With motion allowed and the script coming, the first paint is the
	   start of the loop: the project rows dimmed, the nightly tile alone,
	   no cross yet. .still is never set now that anime.js is part of the
	   page; the 4s fallback below covers a bundle that never mounts. */
	@media (prefers-reduced-motion: no-preference) {
		:global(html.js) .hero-pic:not(.live) .m-proj {
			opacity: var(--dimmed);
		}
		:global(html.js) .hero-pic:not(.live) .lw .kid.nu,
		:global(html.js) .hero-pic:not(.live) .lw .st,
		:global(html.js) .hero-pic:not(.live) .railbody :global(.t-s-full),
		:global(html.js) .hero-pic:not(.live) .railbody :global(.t-u-stub),
		:global(html.js) .hero-pic:not(.live) .railbody :global(.t-n-stub) {
			opacity: 0;
		}
		:global(html.js) .hero-pic:not(.live) .railbody :global(.t-n-full) {
			opacity: 1;
		}
		/* The first paint is only a promise that the loop is coming. If the
		   app's script never mounts the picture (.mounted, set first thing
		   in onMount), after 4s the still frame shows, as it does with
		   scripts off: a cut with no motion. */
		:global(html.js) .hero-pic:not(.live):not(.mounted) .m-proj,
		:global(html.js) .hero-pic:not(.live):not(.mounted) .lw .kid.nu,
		:global(html.js) .hero-pic:not(.live):not(.mounted) .lw .st,
		:global(html.js) .hero-pic:not(.live):not(.mounted) .railbody :global(.t-s-full),
		:global(html.js) .hero-pic:not(.live):not(.mounted) .railbody :global(.t-u-stub),
		:global(html.js) .hero-pic:not(.live):not(.mounted) .railbody :global(.t-n-stub) {
			animation: hero-unhold 0s linear 4s forwards;
		}
		:global(html.js) .hero-pic:not(.live):not(.mounted) .railbody :global(.t-n-full) {
			animation: hero-hold 0s linear 4s forwards;
		}
		@keyframes hero-unhold {
			to {
				opacity: 1;
			}
		}
		@keyframes hero-hold {
			to {
				opacity: 0;
			}
		}
	}

	/* Phone: stacked. The laptop, both commands side by side under it,
	   the machine, then the snapshots and the globe in one row under the
	   machine, each wired up to it. */
	.hero-pic {
		grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
		grid-template-areas: 'lap lap' 'hop ask' 'win win' 'side side';
	}
	/* Over the copies in flight: the repo row's copy leaves from under
	   the laptop's wall. */
	.laptop {
		position: relative;
		z-index: 4;
		grid-area: lap;
	}
	.hop {
		grid-area: hop;
	}
	.ask {
		grid-area: ask;
		padding: 6px 0;
	}
	.ask .wire.arrow {
		order: 1;
	}
	.ask::before {
		content: '';
		width: 0;
		height: 16px;
	}
	.mwin {
		grid-area: win;
	}
	.side.right {
		grid-area: side;
		display: grid;
		grid-template-columns: minmax(0, 1fr) 40px;
		align-items: start;
		gap: 16px;
		margin-top: 24px;
	}
	.railbody {
		flex: none;
		height: 160px;
	}
	/* The wires drop from the machine's floor: to the snapshots panel's
	   mark, and to the globe. */
	.w-snap,
	.w-net {
		top: -24px;
		width: 0;
		height: 24px;
		border-top: 0;
		border-left: 1px solid var(--pic-dim);
	}
	.w-snap {
		left: 23px;
	}
	.w-net {
		left: 50%;
		right: auto;
	}
	.w-snap::before,
	.w-snap::after,
	.w-net::before {
		left: -3px;
		top: auto;
	}
	.w-snap::before,
	.w-net::before {
		top: -2.5px;
	}
	.w-snap::after {
		right: auto;
		bottom: -2.5px;
	}
	.link .lit {
		left: -1px;
		right: auto;
		top: 0;
		bottom: 0;
		height: auto;
		width: 0;
		border-top: 0;
		border-left: 1.5px solid var(--pic-accent);
		transform-origin: center top;
	}
	.w-net .lit {
		border-color: var(--pic-stop);
	}
	.link :global(.lit.from-end) {
		transform-origin: center bottom;
	}
	/* The laptop is above the machine here, so the skill runs up the
	   machine's right edge and is stopped at the rule under its head, the
	   cross over the status dot, inside the wall. */
	.wallhit {
		right: 0;
		top: calc(var(--shot-head) - 1.5px);
		width: 26px;
		border-top: 2px solid var(--pic-stop);
	}
	.stopper {
		/* Centred on WinHead's dot: 10px padding + 3.5px, less half of 18. */
		right: 4.5px;
		top: calc((var(--shot-head) - 18px) / 2);
	}

	@media (min-width: 1024px) {
		/* The machine is the tallest thing on the stage: the laptop stands on
		   its floor, your snapshot command waits in the room above it, and
		   the right-hand stack is exactly as tall as the machine. */
		.hero-pic {
			grid-template-columns: minmax(0, 0.82fr) auto minmax(0, 1.7fr) var(--gap) minmax(0, 0.9fr);
			grid-template-rows: 1fr auto;
			grid-template-areas:
				'ask ask win . side'
				'lap hop win . side';
		}
		.laptop {
			align-self: end;
		}
		.mwin,
		.side.right {
			grid-row: 1 / 3;
		}
		/* The rail's foot is level with the rule over the machine's agent
		   row, and the globe's row is that row: the wire meets the globe
		   at the agent's height, and the stack ends on the machine's floor. */
		.side.right {
			display: flex;
			align-items: stretch;
			gap: 2px;
			margin-top: 0;
		}
		.railbody {
			flex: 1;
			height: auto;
		}
		.net {
			height: calc(var(--agent) + 2px);
		}
		/* Each command's wire lands on the machine's wall at a row: repose
		   run at the repo row (the laptop's first row), yours at the
		   machine's first row, level with the snapshot wire across. */
		.hop {
			flex-direction: row;
			align-self: start;
			margin-top: calc(1px + var(--shot-head) + var(--pad) + var(--row) / 2 - 12px);
			padding: 0 8px;
		}
		.ask {
			flex-direction: row;
			align-self: start;
			margin-top: calc(1px + var(--shot-head) + var(--pad) + var(--row) / 2 - 12px);
			padding: 0 0 0 0;
		}
		.ask::before {
			display: none;
		}
		.ask .wire.arrow {
			flex: 1;
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
		.w-snap,
		.w-net {
			height: 0;
			border-left: 0;
			border-top: 1px solid var(--pic-dim);
		}
		.w-snap {
			left: calc(-1 * var(--gap));
			width: var(--gap);
			top: calc(2px + var(--shot-head) + 9px + 13px);
		}
		.w-net {
			left: calc(-1 * var(--gap));
			right: 50%;
			width: auto;
			top: 50%;
		}
		.w-snap::before,
		.w-snap::after,
		.w-net::before {
			top: -3px;
			bottom: auto;
		}
		.w-snap::before,
		.w-net::before {
			left: -2.5px;
		}
		.w-snap::after {
			left: auto;
			right: -2.5px;
		}
		.link .lit {
			left: 0;
			right: 0;
			top: -1px;
			bottom: auto;
			width: auto;
			height: 0;
			border-left: 0;
			border-top: 1.5px solid var(--pic-accent);
			transform-origin: left center;
		}
		.w-net .lit {
			border-color: var(--pic-stop);
		}
		.link :global(.lit.from-end) {
			transform-origin: right center;
		}
		.wallhit {
			left: -1.5px;
			right: auto;
			top: auto;
			bottom: calc(var(--agent) - 10.5px);
			width: 0;
			height: 28px;
			border-top: 0;
			border-left: 2px solid var(--pic-stop);
		}
		.stopper {
			left: -9.5px;
			right: auto;
			top: auto;
			bottom: calc(var(--agent) - 5.5px);
		}
	}
	/* A machine under 400px wide (the three columns at 1024 to 1100): the
	   agent's row sets its mode line and the skill a step smaller, so the
	   two stand side by side. */
	@container (max-width: 400px) {
		.agentrow {
			gap: 8px;
		}
		.mode,
		.skill {
			font-size: 11px;
		}
	}
	@media (max-width: 480px) {
		.hero-pic {
			--row: 26px;
		}
		/* A phone has no room for two columns under the machine: the globe
		   hangs on its wire at the machine's right corner, and the
		   snapshots panel takes the full width under it, its wire running
		   down past the globe. */
		.side.right {
			grid-template-columns: minmax(0, 1fr);
			gap: 8px;
			margin-top: 16px;
		}
		.net {
			order: -1;
			justify-self: end;
			width: 40px;
		}
		.w-net {
			top: -16px;
			height: 16px;
		}
		.w-snap {
			top: -16px;
			height: calc(16px + 40px + 8px);
		}
		.wire,
		.ask::before {
			height: 10px;
		}
		.agentrow {
			gap: 8px;
			padding-right: 10px;
		}
		.skill-in {
			right: 10px;
		}
		.skill {
			gap: 4px;
			padding: 0 5px;
		}
		.skill svg {
			width: 11px;
			height: 11px;
		}
		.win,
		.fly {
			font-size: 12px;
		}
		.fname {
			font-size: 11.5px;
		}
		.skill,
		.mode,
		.st,
		.val,
		.path {
			font-size: 11px;
		}
	}
	@media (prefers-color-scheme: dark) {
		.hero-pic {
			--rogue: #ff4b3e;
			--mode: #ff87af;
		}
	}
</style>
