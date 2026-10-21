<!--
  Let it break the whole machine, drawn in the working-state section's style
  (OneCommand.svelte): a light panel with hairline borders, rows, the blue
  accent for what moves. The panel lists the machine, not the repo: things
  git doesn't hold. Every value but the Codex login was read on the real
  machine of the owner's job-alerts app (ssh recruiting.repose, 2026-09-26,
  read-only):
  `git ls-files apps packages | wc -l` 578; `git status --short` 3 changes
  (.env.example and poller.ts staged, timeout.test.ts untracked), the same
  three "Your working state" shows; `select count(*) from roles` in the
  app's Postgres (recruiting-postgres-1, data in the gitignored pgdata/)
  3532; `pnpm --version` 10.30.3 (packageManager pnpm@10.30.3); the login
  shell's PATH has 28 entries; `gh auth status` logged in. The logins row
  names gh and Codex: both are kept on the machine's disk and so are in a
  snapshot (docs agents.md "Log in"). The Codex half is from those docs,
  not read on that machine (I-398). Claude Code's login lives on the
  user's login share, which a snapshot does not hold (I-278), so the row
  does not name it (I-398).
  The table row says what the rows are, "job listings", not the table's
  name: "roles" read as database roles (F50).
  The snapshots have the causes the product gives them (docs lifecycle.md,
  "Snapshots": nightly, on stop, and when you ask, F01): the older is the
  nightly one, at 03:00, with the moon; the newer is one you took, at
  21:25, with the prompt. The times are illustrative, in one timezone
  (the machine's). Both are drawn with SnapshotTile, the hero's grammar
  (I-666): the newest a tile with the machine's rows in miniature, an
  older one its head alone, 4px under it.
  A snapshot holds the whole disk (docs lifecycle.md, "Snapshots";
  DESIGN.md §6: the root overlay's upper dir and /home).

  The picture is the grid's (I-669): the machine a window under "your
  cloud machine", closed and inset alike on all sides (I-675), the
  snapshots stacked beside it on the same top line, or in a row under it
  where the picture is under 410px wide.

  The loop, with anime.js: the machine is whole and 03:00 is its only
  snapshot. 21:25 is taken (the rows shrink into a small tile) and 03:00
  folds to its head under it. The agent wrecks the machine, row by row:
  the tracked files, the uncommitted edits and the listings count down to
  0, pnpm is not found, PATH and the logins are struck. git acts first
  (its mark lights) and brings back only the tracked files; the rest stays
  red. Then 21:25 lights and each row it holds wipes back in over its
  wrecked twin, from the snapshot's side, into its own slot (F16), taking
  the accent tint and a tick, the listings and the edits included (blue
  for what came back: the landing draws no green but a diff stat's,
  LANDING.md, I-400). It rests; then 21:25 fades and 03:00 unfolds back
  to the top, the frame the loop starts from.
  Rest, again. Under prefers-reduced-motion, or before anime.js loads, the
  restored state is shown still, with nothing moving; a visitor who turns
  reduced motion on mid-loop gets that still frame at once. It is also the
  frame the picture keeps while another grid picture has the turn
  (motion.svelte.ts); the loop's first frame is set when it starts, not at
  mount, so an offscreen picture costs no layout reads.

  Marks: pnpm from marks.ts; git, GitHub and PostgreSQL from simple-icons
  (CC0, 16.32.0), Codex from lobe-icons via marks.ts. Each belongs to
  its owner and is shown only to say the tool is on the machine.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { agentMarks, toolMarks } from '$lib/components/illustrations/marks';
	import { watchReducedMotion } from './inview';
	import { Picture } from './motion.svelte';
	import PauseButton from './PauseButton.svelte';
	import SnapshotTile from './SnapshotTile.svelte';
	import WinHead from './WinHead.svelte';

	const pnpmPaths = toolMarks.find((m) => m.name === 'pnpm')!.paths;
	const codex = agentMarks.find((m) => m.name === 'Codex')!;
	const gitPath =
		'M13.09 23.549a1.54 1.54 0 0 1-2.18 0L.451 13.089a1.54 1.54 0 0 1 0-2.179l7.191-7.19 2.733 2.733a1.85 1.85 0 0 0 .964 2.326v6.66a1.849 1.849 0 1 0 1.54 0V8.957l2.508 2.508a1.85 1.85 0 1 0 1.09-1.09l-2.634-2.634a1.85 1.85 0 0 0-2.378-2.377L8.73 2.63 10.91.451a1.54 1.54 0 0 1 2.179 0l10.459 10.46a1.54 1.54 0 0 1 0 2.179z';
	const githubPath =
		'M12 .297c-6.63 0-12 5.373-12 12 0 5.303 3.438 9.8 8.205 11.385.6.113.82-.258.82-.577 0-.285-.01-1.04-.015-2.04-3.338.724-4.042-1.61-4.042-1.61C4.422 18.07 3.633 17.7 3.633 17.7c-1.087-.744.084-.729.084-.729 1.205.084 1.838 1.236 1.838 1.236 1.07 1.835 2.809 1.305 3.495.998.108-.776.417-1.305.76-1.605-2.665-.3-5.466-1.332-5.466-5.93 0-1.31.465-2.38 1.235-3.22-.135-.303-.54-1.523.105-3.176 0 0 1.005-.322 3.3 1.23.96-.267 1.98-.399 3-.405 1.02.006 2.04.138 3 .405 2.28-1.552 3.285-1.23 3.285-1.23.645 1.653.24 2.873.12 3.176.765.84 1.23 1.91 1.23 3.22 0 4.61-2.805 5.625-5.475 5.92.42.36.81 1.096.81 2.22 0 1.606-.015 2.896-.015 3.286 0 .315.21.69.825.57C20.565 22.092 24 17.592 24 12.297c0-6.627-5.373-12-12-12';
	const pgPath =
		'M23.5594 14.7228a.5269.5269 0 0 0-.0563-.1191c-.139-.2632-.4768-.3418-1.0074-.2321-1.6533.3411-2.2935.1312-2.5256-.0191 1.342-2.0482 2.445-4.522 3.0411-6.8297.2714-1.0507.7982-3.5237.1222-4.7316a1.5641 1.5641 0 0 0-.1509-.235C21.6931.9086 19.8007.0248 17.5099.0005c-1.4947-.0158-2.7705.3461-3.1161.4794a9.449 9.449 0 0 0-.5159-.0816 8.044 8.044 0 0 0-1.3114-.1278c-1.1822-.0184-2.2038.2642-3.0498.8406-.8573-.3211-4.7888-1.645-7.2219.0788C.9359 2.1526.3086 3.8733.4302 6.3043c.0409.818.5069 3.334 1.2423 5.7436.4598 1.5065.9387 2.7019 1.4334 3.582.553.9942 1.1259 1.5933 1.7143 1.7895.4474.1491 1.1327.1441 1.8581-.7279.8012-.9635 1.5903-1.8258 1.9446-2.2069.4351.2355.9064.3625 1.39.3772a.0569.0569 0 0 0 .0004.0041 11.0312 11.0312 0 0 0-.2472.3054c-.3389.4302-.4094.5197-1.5002.7443-.3102.064-1.1344.2339-1.1464.8115-.0025.1224.0329.2309.0919.3268.2269.4231.9216.6097 1.015.6331 1.3345.3335 2.5044.092 3.3714-.6787-.017 2.231.0775 4.4174.3454 5.0874.2212.5529.7618 1.9045 2.4692 1.9043.2505 0 .5263-.0291.8296-.0941 1.7819-.3821 2.5557-1.1696 2.855-2.9059.1503-.8707.4016-2.8753.5388-4.1012.0169-.0703.0357-.1207.057-.1362.0007-.0005.0697-.0471.4272.0307a.3673.3673 0 0 0 .0443.0068l.2539.0223.0149.001c.8468.0384 1.9114-.1426 2.5312-.4308.6438-.2988 1.8057-1.0323 1.5951-1.6698zM2.371 11.8765c-.7435-2.4358-1.1779-4.8851-1.2123-5.5719-.1086-2.1714.4171-3.6829 1.5623-4.4927 1.8367-1.2986 4.8398-.5408 6.108-.13-.0032.0032-.0066.0061-.0098.0094-2.0238 2.044-1.9758 5.536-1.9708 5.7495-.0002.0823.0066.1989.0162.3593.0348.5873.0996 1.6804-.0735 2.9184-.1609 1.1504.1937 2.2764.9728 3.0892.0806.0841.1648.1631.2518.2374-.3468.3714-1.1004 1.1926-1.9025 2.1576-.5677.6825-.9597.5517-1.0886.5087-.3919-.1307-.813-.5871-1.2381-1.3223-.4796-.839-.9635-2.0317-1.4155-3.5126zm6.0072 5.0871c-.1711-.0428-.3271-.1132-.4322-.1772.0889-.0394.2374-.0902.4833-.1409 1.2833-.2641 1.4815-.4506 1.9143-1.0002.0992-.126.2116-.2687.3673-.4426a.3549.3549 0 0 0 .0737-.1298c.1708-.1513.2724-.1099.4369-.0417.156.0646.3078.26.3695.4752.0291.1016.0619.2945-.0452.4444-.9043 1.2658-2.2216 1.2494-3.1676 1.0128zm2.094-3.988-.0525.141c-.133.3566-.2567.6881-.3334 1.003-.6674-.0021-1.3168-.2872-1.8105-.8024-.6279-.6551-.9131-1.5664-.7825-2.5004.1828-1.3079.1153-2.4468.079-3.0586-.005-.0857-.0095-.1607-.0122-.2199.2957-.2621 1.6659-.9962 2.6429-.7724.4459.1022.7176.4057.8305.928.5846 2.7038.0774 3.8307-.3302 4.7363-.084.1866-.1633.3629-.2311.5454zm7.3637 4.5725c-.0169.1768-.0358.376-.0618.5959l-.146.4383a.3547.3547 0 0 0-.0182.1077c-.0059.4747-.054.6489-.115.8693-.0634.2292-.1353.4891-.1794 1.0575-.11 1.4143-.8782 2.2267-2.4172 2.5565-1.5155.3251-1.7843-.4968-2.0212-1.2217a6.5824 6.5824 0 0 0-.0769-.2266c-.2154-.5858-.1911-1.4119-.1574-2.5551.0165-.5612-.0249-1.9013-.3302-2.6462.0044-.2932.0106-.5909.019-.8918a.3529.3529 0 0 0-.0153-.1126 1.4927 1.4927 0 0 0-.0439-.208c-.1226-.4283-.4213-.7866-.7797-.9351-.1424-.059-.4038-.1672-.7178-.0869.067-.276.1831-.5875.309-.9249l.0529-.142c.0595-.16.134-.3257.213-.5012.4265-.9476 1.0106-2.2453.3766-5.1772-.2374-1.0981-1.0304-1.6343-2.2324-1.5098-.7207.0746-1.3799.3654-1.7088.5321a5.6716 5.6716 0 0 0-.1958.1041c.0918-1.1064.4386-3.1741 1.7357-4.4823a4.0306 4.0306 0 0 1 .3033-.276.3532.3532 0 0 0 .1447-.0644c.7524-.5706 1.6945-.8506 2.802-.8325.4091.0067.8017.0339 1.1742.081 1.939.3544 3.2439 1.4468 4.0359 2.3827.8143.9623 1.2552 1.9315 1.4312 2.4543-1.3232-.1346-2.2234.1268-2.6797.779-.9926 1.4189.543 4.1729 1.2811 5.4964.1353.2426.2522.4522.2889.5413.2403.5825.5515.9713.7787 1.2552.0696.087.1372.1714.1885.245-.4008.1155-1.1208.3825-1.0552 1.717-.0123.1563-.0423.4469-.0834.8148-.0461.2077-.0702.4603-.0994.7662zm.8905-1.6211c-.0405-.8316.2691-.9185.5967-1.0105a2.8566 2.8566 0 0 0 .135-.0406 1.202 1.202 0 0 0 .1342.103c.5703.3765 1.5823.4213 3.0068.1344-.2016.1769-.5189.3994-.9533.6011-.4098.1903-1.0957.333-1.7473.3636-.7197.0336-1.0859-.0807-1.1721-.151zm.5695-9.2712c-.0059.3508-.0542.6692-.1054 1.0017-.055.3576-.112.7274-.1264 1.1762-.0142.4368.0404.8909.0932 1.3301.1066.887.216 1.8003-.2075 2.7014a3.5272 3.5272 0 0 1-.1876-.3856c-.0527-.1276-.1669-.3326-.3251-.6162-.6156-1.1041-2.0574-3.6896-1.3193-4.7446.3795-.5427 1.3408-.5661 2.1781-.463zm.2284 7.0137a12.3762 12.3762 0 0 0-.0853-.1074l-.0355-.0444c.7262-1.1995.5842-2.3862.4578-3.4385-.0519-.4318-.1009-.8396-.0885-1.2226.0129-.4061.0666-.7543.1185-1.0911.0639-.415.1288-.8443.1109-1.3505.0134-.0531.0188-.1158.0118-.1902-.0457-.4855-.5999-1.938-1.7294-3.253-.6076-.7073-1.4896-1.4972-2.6889-2.0395.5251-.1066 1.2328-.2035 2.0244-.1859 2.0515.0456 3.6746.8135 4.8242 2.2824a.908.908 0 0 1 .0667.1002c.7231 1.3556-.2762 6.2751-2.9867 10.5405zm-8.8166-6.1162c-.025.1794-.3089.4225-.6211.4225a.5821.5821 0 0 1-.0809-.0056c-.1873-.026-.3765-.144-.5059-.3156-.0458-.0605-.1203-.178-.1055-.2844.0055-.0401.0261-.0985.0925-.1488.1182-.0894.3518-.1226.6096-.0867.3163.0441.6426.1938.6113.4186zm7.9305-.4114c.0111.0792-.049.201-.1531.3102-.0683.0717-.212.1961-.4079.2232a.5456.5456 0 0 1-.075.0052c-.2935 0-.5414-.2344-.5607-.3717-.024-.1765.2641-.3106.5611-.352.297-.0414.6111.0088.6356.1851z';

	// How each row breaks: a count falls to zero, the value is replaced
	// (pnpm: not found), or it is struck.
	type Row = {
		k: string;
		name: string;
		val: string;
		n?: number;
		unit?: string;
		lost?: string;
		git?: boolean;
	};
	const rows: Row[] = [
		{ k: 'src', name: 'apps/ packages/', val: '578 files', n: 578, unit: 'files', git: true },
		{ k: 'wip', name: 'uncommitted edits', val: '3 files', n: 3, unit: 'files' },
		{ k: 'db', name: 'job listings', val: '3,532 rows', n: 3532, unit: 'rows' },
		{ k: 'pnpm', name: 'pnpm', val: '10.30.3', lost: 'not found' },
		{ k: 'path', name: 'PATH', val: '28 dirs' },
		{ k: 'login', name: 'gh, codex', val: 'logged in' }
	];

	const label = 'Your cloud machine broken and brought back from a snapshot';
	const story =
		'Your cloud machine holds more than the repo: 578 tracked files in apps and packages, 3 files of uncommitted edits, ' +
		'3,532 job listings in Postgres, pnpm 10.30.3, a PATH of 28 directories, and gh and Codex logged in. ' +
		'It has a nightly snapshot from 03:00, and at 21:25 you take another. The agent wrecks the machine: the files, ' +
		'the edits and the listings drop to zero, pnpm is not found, PATH is broken and the logins are gone. ' +
		'git brings back only the 578 tracked files. Restoring the 21:25 snapshot brings back everything else: ' +
		'the uncommitted edits, all 3,532 listings, pnpm, PATH and the logins.';
	const descId = 'comes-back-desc';

	let pic: HTMLDivElement;
	const picture = new Picture({ group: 'grid' });
	let fly: HTMLDivElement;
	let mRows: HTMLUListElement;

	// The snapshot tiles hold the machine's rows at this scale.
	let mw = $state(300);
	let tw = $state(88);
	const s = $derived(tw / mw);

	onMount(() => {
		const ro = new ResizeObserver(() => {
			mw = mRows.offsetWidth;
			tw = (pic.querySelector('.tile.new') as HTMLElement).offsetWidth - 2;
		});
		ro.observe(pic);

		let dead = false;
		let visible = false;
		let tl: { pause(): unknown; play(): unknown } | null = null;
		// Bumped on each start and stop, so an anime.js import that resolves
		// after a stop does not start a loop.
		let run = 0;

		const q = (sel: string) => Array.from(pic.querySelectorAll<HTMLElement>(sel));
		const row = (k: string) => pic.querySelector<HTMLElement>(`.m-row[data-k="${k}"]`)!;
		const valOf = (k: string) => row(k).querySelector<HTMLElement>('.vt')!;
		// "1 file", not "1 files": the edits count down through one.
		const fmt = (n: number, unit: string) =>
			`${n.toLocaleString('en-US')} ${n === 1 ? unit.replace(/s$/, '') : unit}`;
		const STATE = ['hit', 'lit', 'rewind', 'fire', 'ok', 'gone', 'fix', 'fresh'];
		// The old tile folds by a clip from below, so its box keeps its
		// height and nothing around it is laid out again each frame.
		const clip = (px: number) => `inset(0px 0px ${px}px 0px round 3px)`;
		// The snapshot mark's angle: a quarter turn more per snapshot.
		let turn = 0;
		// The still frame is showing: the next cycle starts at the seam, so
		// it leaves the still through the loop's own exit, not a cut.
		let fromStill = false;

		// Stop the loop and show the restored machine still: the markup's own
		// state, with every class and inline style the loop wrote taken off.
		function still() {
			run++;
			picture.stop();
			rest();
		}
		// The still frame, with the loop kept registered: another grid
		// picture has the turn, or the loop is held before it has begun.
		function rest() {
			tl?.pause();
			tl = null;
			visible = false;
			// eslint-disable-next-line svelte/no-dom-manipulating
			fly.replaceChildren();
			for (const r of rows) valOf(r.k).textContent = r.val;
			for (const el of q('.tile, .arr, .cam, .m-row .strike, .m-row .bad, .m-row .tick'))
				el.removeAttribute('style');
			turn = 0;
			fromStill = true;
			for (const el of q(STATE.map((c) => '.' + c).join(', '))) el.classList.remove(...STATE);
			q('.tile.new')[0].classList.add('lit');
		}

		function start() {
			const me = ++run;
			import('animejs').then(({ createTimeline, utils, stagger }) => {
				if (dead || me !== run) return;

				// Where the old tile sits from the new one (under it beside the
				// machine, after it in the row under a narrow machine), and
				// the clip that folds it to its head: read before any write.
				function measure() {
					const newT = q('.tile.new')[0];
					const oldT = q('.tile.old')[0];
					const mini = newT.querySelector<HTMLElement>('.mini')!;
					return {
						lift: { x: newT.offsetLeft - oldT.offsetLeft, y: newT.offsetTop - oldT.offsetTop },
						fold: mini.offsetHeight + 1
					};
				}

				// Before anything: the machine is whole, 03:00 is the only
				// snapshot, unfolded where 21:25 will land. Layout is read
				// first, then every write, so the browser lays out once.
				function pre(lift: { x: number; y: number }) {
					for (const r of rows) valOf(r.k).textContent = r.val;
					utils.set(q('.tile.new, .arr'), { opacity: 0 });
					utils.set(q('.tile.old'), { opacity: 1, ...lift, clipPath: clip(0) });
					utils.set(q('.m-row .strike'), { scaleX: 0 });
					utils.set(q('.m-row .bad, .m-row .tick'), { opacity: 0, scale: 0.6 });
					for (const el of q('.hit, .lit, .rewind, .fire, .ok, .gone, .fix'))
						el.classList.remove('hit', 'lit', 'rewind', 'fire', 'ok', 'gone', 'fix');
					for (const el of q('.tile.old')) el.classList.add('fresh');
				}
				pre(measure().lift);

				function cycle() {
					if (dead || me !== run) return;
					if (!visible) {
						tl = null;
						return;
					}
					// .fly is an empty layer Svelte never renders into; only
					// throwaway copies of the rows live in it.
					// eslint-disable-next-line svelte/no-dom-manipulating
					fly.replaceChildren();
					const { lift, fold } = measure();
					pre(lift);

					const base = pic.getBoundingClientRect();
					const cls = (els: HTMLElement[], c: string, on: boolean) => () => {
						for (const el of els) el.classList.toggle(c, on);
					};
					const at = (el: Element) => {
						const r = el.getBoundingClientRect();
						return { x: r.left - base.left, y: r.top - base.top, w: r.width, h: r.height };
					};
					const place = (el: HTMLElement, b: { x: number; y: number; w: number }) => {
						Object.assign(el.style, {
							left: `${b.x}px`,
							top: `${b.y}px`,
							width: `${b.w}px`,
							opacity: '0'
						});
						// eslint-disable-next-line svelte/no-dom-manipulating
						fly.appendChild(el);
						return el;
					};
					const mBox = at(mRows);
					const newT = q('.tile.new')[0];
					const oldT = q('.tile.old')[0];
					const to = at(newT.querySelector('.mini ul')!);
					// the machine's rows, whole, as a copy that travels into the tile
					const take = mRows.cloneNode(true) as HTMLElement;
					for (const li of [take, ...take.querySelectorAll('li')]) li.classList.remove('m-row');
					take.classList.add('clone');
					place(take, mBox);
					const dx = to.x - mBox.x;
					const dy = to.y - mBox.y;
					const scale = s;
					// The restore wipes in from the snapshots' side: the right
					// beside the machine, the left when they sit under it.
					const left = at(newT).x < mBox.x + mBox.w / 2;
					const shut = left ? 'inset(0px 100% 0px 0px)' : 'inset(0px 0px 0px 100%)';
					const open = left ? 'inset(0px 0% 0px 0px)' : 'inset(0px 0px 0px 0%)';
					// each row the snapshot gives back, whole, as a copy laid on
					// its own slot (the machine is whole at this point)
					const heal = rows.filter((r) => r.k !== 'src');
					const wipes = heal.map((r) => {
						const c = row(r.k).cloneNode(true) as HTMLElement;
						c.classList.remove('m-row');
						c.classList.add('wipe');
						return place(c, at(row(r.k)));
					});

					// Counts that fall and rise, written into the row's value.
					function count(
						k: string,
						from: number,
						too: number,
						unit: string,
						at0: number,
						d: number
					) {
						const o = { v: from };
						t.add(
							o,
							{
								v: too,
								duration: d,
								ease: 'outQuad',
								onUpdate: () => {
									valOf(k).textContent = fmt(Math.round(o.v), unit);
								}
							},
							at0
						);
					}

					const T = { snap: 500, wreck: 2500, git: 5200, back: 7600 };
					const dur = 850;
					const t = createTimeline({ autoplay: false, onComplete: () => cycle() });

					// 1. snapshot 21:25: the rows shrink into its tile; 03:00 folds under it
					const mark = newT.querySelector('.cam') as HTMLElement;
					const cams = [q('.machine')[0], mark];
					const s0 = T.snap;
					// The copy reaches its size early (its own outCubic) and is
					// gone as it sets down; the tile shows only then, so the
					// shrunken copy and the tile's own rows never lie one on the
					// other.
					const landed = s0 + 150 + dur;
					// .add with a 16ms duration, never .set: anime.js can pass
					// over a timeline's .set, and the copy stayed on.
					t.call(cls(cams, 'fire', true), s0)
						.add(q('.take'), { opacity: [0, 1], duration: 250 }, s0)
						.call(cls([oldT], 'fresh', false), s0 + 150)
						.add(
							oldT,
							{ clipPath: [clip(0), clip(fold)], duration: 550, ease: 'inOutCubic' },
							s0 + 150
						)
						.add(oldT, { x: 0, y: 0, duration: 700, ease: 'inOutCubic' }, s0 + 150)
						.add(take, { opacity: [0, 1], duration: 16 }, s0 + 150)
						.add(
							take,
							{
								x: { to: dx, duration: dur, ease: 'inOutCubic' },
								y: { to: dy, duration: dur, ease: 'inOutCubic' },
								scale: { to: scale, duration: dur * 0.6, ease: 'outCubic' }
							},
							s0 + 150
						)
						.add(take, { opacity: 0, duration: 150, ease: 'inQuad' }, landed - 150)
						.add(newT, { opacity: [0, 1], duration: 250 }, landed)
						// The mark's quarter turn, as the new tile shows. It adds up,
						// and only the restore's full turn takes it back (LANDING.md,
						// "Motion").
						.call(() => {
							turn += 90;
							mark.style.setProperty('--turn', `${turn}deg`);
						}, landed)
						.add(q('.take'), { opacity: 0, duration: 400, ease: 'inQuad' }, landed + 300)
						.call(cls(cams, 'fire', false), landed + 300);

					// 2. the wreck, row by row: counts fall, pnpm is gone, the rest struck
					const order = ['wip', 'src', 'db', 'path', 'pnpm', 'login'];
					order.forEach((k, i) => {
						const r = rows.find((x) => x.k === k)!;
						const at0 = T.wreck + i * 230;
						t.call(cls([row(k)], 'hit', true), at0);
						if (r.n !== undefined) count(k, r.n, 0, r.unit!, at0 + 60, r.n > 100 ? 500 : 300);
						else if (r.lost)
							t.call(() => (valOf(k).textContent = r.lost!), at0 + 120).call(
								cls([row(k)], 'gone', true),
								at0 + 120
							);
						else
							t.add(
								row(k).querySelector('.strike')!,
								{ scaleX: [0, 1], duration: 360, ease: 'outCubic' },
								at0 + 60
							);
						t.add(
							row(k).querySelector('.bad')!,
							{ opacity: [0, 1], scale: [0.6, 1], duration: 280, ease: 'outBack' },
							at0 + 200
						);
					});

					// 3. git first: its mark lights, the tracked files come back, nothing else
					const src = row('src');
					const gitMark = src.querySelector<HTMLElement>('.gm')!;
					t.call(cls([gitMark], 'fire', true), T.git)
						.call(cls([src], 'hit', false), T.git + 300)
						.call(cls([src], 'fix', true), T.git + 300)
						.call(cls([src], 'fix', false), T.git + 950)
						.call(cls([src], 'ok', true), T.git + 950)
						.add(
							src.querySelector('.bad')!,
							{ opacity: 0, duration: 300, ease: 'inQuad' },
							T.git + 300
						);
					count('src', 0, 578, 'files', T.git + 300, 500);
					t.add(
						src.querySelector('.tick')!,
						{ opacity: [0, 1], scale: [0.6, 1], duration: 300, ease: 'outBack' },
						T.git + 950
					)
						.call(cls([gitMark], 'fire', false), T.git + 1300)
						// the rest stays broken: each cross pulses once, to 1.35
						// and back in 420ms, 60ms apart
						.add(
							q('.m-row:not([data-k="src"]) .bad'),
							{ scale: [1, 1.35, 1], duration: 420, delay: stagger(60), ease: 'inOutCubic' },
							T.git + 1400
						);

					// 4. the restore: 21:25 lights, and each row it holds wipes
					// back in over its wrecked twin, from the snapshot's side
					const b0 = T.back;
					const wipe = 450;
					const gap = 90;
					t.call(cls([newT], 'lit', true), b0 - 350)
						.call(cls([newT], 'rewind', true), b0 - 350)
						.add(q('.give'), { opacity: [0, 1], duration: 250 }, b0 - 350);
					heal.forEach((r, i) => {
						const el = row(r.k);
						const c = wipes[i];
						const at0 = b0 + i * gap;
						const done = at0 + wipe;
						t.add(c, { opacity: [0, 1], duration: 16 }, at0)
							.add(
								c,
								{
									clipPath: [shut, open],
									duration: wipe,
									ease: 'inOutCubic'
								},
								at0
							)
							// landed: the row under the copy takes the restored state,
							// and the copy goes once the row's tint and value colour
							// have finished their 250ms change, so the swap never shows
							.call(cls([el], 'hit', false), done)
							.call(cls([el], 'gone', false), done)
							.call(cls([el], 'ok', true), done)
							.call(() => (valOf(r.k).textContent = r.val), done)
							.add(el.querySelector('.bad')!, { opacity: 0, duration: 16 }, done)
							.add(el.querySelector('.strike')!, { scaleX: 0, duration: 16 }, done)
							.add(c, { opacity: 0, duration: 16 }, done + 300)
							.add(
								el.querySelector('.tick')!,
								{ opacity: [0, 1], scale: [0.6, 1], duration: 300, ease: 'outBack' },
								done + 300
							);
					});
					const done = b0 + (heal.length - 1) * gap + wipe + 600;
					const away = done + 4600;
					t.call(cls(q('.m-row'), 'ok', false), done + 900)
						.call(cls([newT], 'lit', false), done + 1600)
						.call(cls([newT], 'rewind', false), done + 1600)
						.add(q('.give'), { opacity: 0, duration: 400, ease: 'inQuad' }, done + 1600)
						// rest on the restored machine; then 21:25 goes and 03:00
						// unfolds back to the top, the frame the loop starts from
						.add(q('.tile.new, .m-row .tick'), { opacity: 0, duration: 450, ease: 'inQuad' }, away)
						.call(cls([oldT], 'fresh', true), away + 200)
						.add(
							oldT,
							{ clipPath: [clip(fold), clip(0)], duration: 550, ease: 'inOutCubic' },
							away + 200
						)
						.add(oldT, { ...lift, duration: 700, ease: 'inOutCubic' }, away + 200)
						.add({ duration: 600 }, away + 900);
					// The seam: hand the grid's turn on if nobody is pointing here
					// (motion.svelte.ts). A cycle that starts at the seam skips it.
					const atSeam = fromStill;
					t.call(() => atSeam || picture.looped(), away - 1);

					tl = t;
					// After the still frame (the restored machine), start at the
					// seam, where the loop rests on that same machine, so it
					// leaves through its own exit. Callbacks are muted: the
					// mark's quarter turn must not replay.
					if (fromStill) t.seek(away, true);
					fromStill = false;
					t.play();
				}

				// The loop plays once most of the picture shows and it has the
				// grid's turn; until then it keeps the still frame.
				picture.start(
					pic,
					(m) => {
						visible = m === 'play';
						if (m === 'rest' || (!visible && !tl)) rest();
						else if (!visible) tl?.pause();
						else if (tl) tl.play();
						else cycle();
					},
					0.6
				);
			});
		}

		const unwatch = watchReducedMotion((reduce) => {
			if (reduce) still();
			else start();
		});

		return () => {
			dead = true;
			unwatch();
			ro.disconnect();
			picture.stop();
			tl?.pause();
		};
	});
</script>

{#snippet glyph(d: string | string[], size = 13, evenodd = false)}
	<svg viewBox="0 0 24 24" width={size} height={size} aria-hidden="true" fill="currentColor"
		>{#each Array.isArray(d) ? d : [d] as p (p)}<path
				d={p}
				fill-rule={evenodd ? 'evenodd' : 'nonzero'}
			/>{/each}</svg
	>
{/snippet}

{#snippet fileIcon()}
	<svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true">
		<path
			d="M4 1.5h5.2L12.5 4.8v9.7H4z M9 1.5v3.5h3.5"
			fill="none"
			stroke="currentColor"
			stroke-width="1.1"
			stroke-linejoin="round"
		/>
	</svg>
{/snippet}

{#snippet pathIcon()}
	<svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true">
		<g fill="none" stroke="currentColor" stroke-width="1.2" stroke-linejoin="round">
			<rect x="1.5" y="2.5" width="13" height="11" rx="1" />
			<path d="M4 6.2l2 1.8-2 1.8M7.8 10.5h4" stroke-linecap="round" />
		</g>
	</svg>
{/snippet}

{#snippet icon(k: string)}
	<span class="ic">
		{#if k === 'src'}<span class="gm">{@render glyph(gitPath, 13)}</span>
		{:else if k === 'wip'}{@render fileIcon()}
		{:else if k === 'db'}{@render glyph(pgPath, 14)}
		{:else if k === 'pnpm'}{@render glyph(pnpmPaths, 12)}
		{:else if k === 'path'}{@render pathIcon()}
		{:else}{@render glyph(githubPath, 13)}
		{/if}
	</span>
	{#if k === 'login'}<span class="ic ic2">{@render glyph(codex.paths, 13, codex.evenodd)}</span
		>{/if}
{/snippet}

{#snippet row(r: Row)}
	<li class="fr m-row" data-k={r.k}>
		{@render icon(r.k)}
		<span class="nm">{r.name}</span>
		<span class="val"><span class="vt">{r.val}</span><i class="strike"></i></span>
		<span class="mark">
			<svg class="bad" viewBox="0 0 12 12" width="11" height="11" aria-hidden="true"
				><path d="M3 3l6 6M9 3l-6 6" fill="none" stroke="currentColor" stroke-width="1.6" /></svg
			>
			<svg class="tick" viewBox="0 0 12 12" width="12" height="12" aria-hidden="true"
				><path
					d="M2.5 6.2l2.3 2.3 4.7-5"
					fill="none"
					stroke="currentColor"
					stroke-width="1.6"
				/></svg
			>
		</span>
	</li>
{/snippet}

<!-- a row in a snapshot tile: its shape only, the contents hidden at this size -->
{#snippet thumb(r: Row)}
	<li class="fr">
		{@render icon(r.k)}
		<i class="bar" style:width="{r.name.length * 7}px"></i>
		<i class="bar vb" style:width="{r.val.length * 7}px"></i>
	</li>
{/snippet}

<!-- the machine in miniature, inside a snapshot tile -->
{#snippet mini()}
	<div class="mini">
		<ul class="rows" style:width="{mw}px" style:transform="scale({s})">
			{#each rows as r (r.k)}{@render thumb(r)}{/each}
		</ul>
	</div>
{/snippet}

<p id={descId} class="sr-only">{story}</p>
<div class="shot" use:picture.frame>
	<PauseButton {picture} />
	<div
		class="pic"
		bind:this={pic}
		role="img"
		aria-label={label}
		aria-describedby={descId}
		style:--s={s}
	>
		<div class="shot-win machine">
			<WinHead who="machine" />
			<ul class="rows" bind:this={mRows}>
				{#each rows as r (r.k)}{@render row(r)}{/each}
			</ul>
		</div>

		<div class="hop">
			<svg class="arr take" viewBox="0 0 20 10" aria-hidden="true"
				><path
					d="M1 5h16M13 1.5L17.5 5 13 8.5"
					fill="none"
					stroke="currentColor"
					stroke-width="1.5"
				/></svg
			>
			<svg class="arr give" viewBox="0 0 20 10" aria-hidden="true"
				><path
					d="M19 5H3M7 1.5L2.5 5 7 8.5"
					fill="none"
					stroke="currentColor"
					stroke-width="1.5"
				/></svg
			>
		</div>

		<div class="stack">
			<SnapshotTile time="03:00" cause="nightly" class="tile old">{@render mini()}</SnapshotTile>
			<SnapshotTile time="21:25" cause="user" class="tile new" lit>{@render mini()}</SnapshotTile>
		</div>

		<div class="fly" bind:this={fly}></div>
	</div>
</div>

<style>
	/* Colours are the picture tokens (--pic-*, routes/layout.css), which
	   carry their own dark values. A restored row is --pic-accent, what
	   moves and comes back: --pic-add is a diff stat's green alone. */
	/* The machine is a closed window, inset alike on all four sides: its
	   six rows share what the frame leaves (the frame, its 1px edges, two
	   insets and the window's head and edges), so the last row ends 8px
	   above the window's bottom edge at every width. The snapshots stand
	   beside it in a column up to 120px wide, the hero's tile read at
	   this size.
	   Where the picture is under 410px wide (a phone, the two-column grid
	   at 768 to 1023), a row beside the column would cut its label, so
	   the snapshots go under the machine in a row, and the rows share
	   what the machine and the newest tile leave between them. */
	.shot {
		container-type: inline-size;
	}
	.pic {
		--pad: 8px;
		--rows-h: calc(var(--shot-h) - 2px - 2 * var(--shot-in) - 30px);
		--row: calc((var(--rows-h) - 2 * var(--pad)) / 6);
		/* 120px where the machine keeps 270px for its rows, down to 96px */
		--tile-w: clamp(96px, 100cqw - 325px, 120px);
		/* the newest tile, whole: its edges, head, rule and miniature */
		--full: calc(var(--shot-head) + 1px + var(--rows-h) * var(--s) + 1px);
		position: relative;
		height: 100%;
		display: grid;
		grid-template-columns: minmax(0, 1fr) 30px var(--tile-w);
		grid-template-rows: minmax(0, 1fr);
		align-items: start;
		padding: var(--shot-in);
		font-size: 13px;
		color: var(--pic-ink);
	}

	.machine {
		height: 100%;
		transition: border-color 0.3s;
	}
	.machine:global(.fire) {
		border-color: var(--pic-accent);
	}

	.rows {
		margin: 0;
		padding: var(--pad) 0;
		list-style: none;
	}
	.fr {
		position: relative;
		display: flex;
		align-items: center;
		gap: 7px;
		height: var(--row);
		padding: 0 12px 0 12px;
		white-space: nowrap;
		overflow: hidden;
		transition: background-color 0.25s;
	}
	.ic {
		display: flex;
		flex: none;
		width: 14px;
		justify-content: center;
		color: var(--pic-dim);
	}
	.ic2 {
		margin-left: -3px;
	}
	.gm {
		display: flex;
		transition: color 0.2s;
	}
	.gm:global(.fire) {
		color: var(--pic-accent);
	}
	.nm {
		flex: 0 1 auto;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		font-family: var(--font-mono);
		font-size: 12.5px;
	}
	.val {
		position: relative;
		flex: none;
		margin-left: auto;
		font-family: var(--font-mono);
		font-size: 12px;
		color: var(--pic-dim);
		font-variant-numeric: tabular-nums;
		transition: color 0.25s;
	}
	.fr:global(.hit) {
		background: color-mix(in oklab, var(--pic-stop) 9%, var(--surface));
	}
	.fr:global(.hit) .val {
		color: var(--pic-stop);
	}
	.fr:global(.fix) {
		background: color-mix(in oklab, var(--pic-accent) 9%, var(--surface));
		box-shadow: inset 2px 0 0 var(--pic-accent);
	}
	.fr:global(.ok) {
		background: color-mix(in oklab, var(--pic-accent) 8%, var(--surface));
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
	.mark {
		position: relative;
		flex: none;
		width: 12px;
		height: 14px;
	}
	.bad,
	.tick {
		position: absolute;
		inset: 0;
		margin: auto;
	}
	.bad {
		opacity: 0;
		color: var(--pic-stop);
	}
	.tick {
		color: var(--pic-accent);
	}

	/* Between the machine and its snapshots: which way the copy goes, at
	   the newest tile's middle. */
	.hop {
		position: relative;
		height: var(--full);
	}
	.arr {
		position: absolute;
		inset: 0;
		margin: auto;
		width: 18px;
		height: 10px;
	}
	.take {
		opacity: 0;
		color: var(--pic-dim);
	}
	.give {
		color: var(--pic-accent);
	}

	/* The snapshots (SnapshotTile, the hero's grammar): the newest on top,
	   level with the machine's top edge, its head as tall as the machine's;
	   the older one folded to its head 4px under it. */
	.stack {
		position: relative;
		height: calc(var(--full) + 4px + var(--shot-head));
	}
	.stack :global(.tile) {
		position: absolute;
		left: 0;
		right: 0;
		top: 0;
	}
	.stack :global(.tile.new) {
		z-index: 1;
	}
	/* Folded: the mini keeps its height and the tile is clipped to its
	   head, so folding is a clip, never a change of layout. The cut falls
	   on the mini's top rule, which stands in for the tile's bottom
	   border, so it takes the border's colour. */
	.stack :global(.tile.old) {
		z-index: 0;
		top: calc(var(--full) + 4px);
		clip-path: inset(0 0 calc(var(--rows-h) * var(--s) + 1px) 0 round 3px);
	}
	.stack :global(.tile.old .s-mini) {
		border-top-color: var(--rule-strong);
	}
	.stack :global(.tile .cam.fire) {
		color: var(--pic-accent);
	}
	.stack :global(.tile .s-time) {
		transition: color 0.4s;
	}
	.stack :global(.tile.old:not(.fresh) .s-time) {
		color: var(--pic-dim);
	}
	/* The mark clicks a quarter turn when the snapshot is taken, and
	   rewinds a full turn when it is restored, and nothing else. The loop
	   adds 90deg to --turn per snapshot and never takes it off: a class
	   that set the angle would transition back when it came off, a
	   quarter turn the other way that LANDING.md does not list. The
	   rewind ends where it began, a full turn on. It hangs on .rewind,
	   which only the loop's restore sets, and not on .lit: the still
	   frame renders the new tile lit, and the rewind spun there on load
	   and whenever still() ran with motion allowed. */
	@media (prefers-reduced-motion: no-preference) {
		/* 420ms out-quad, the hero's quarter turn (LANDING.md, "Motion"). */
		.stack :global(.tile .cam svg) {
			transition: transform 420ms cubic-bezier(0.25, 0.46, 0.45, 0.94);
		}
		.stack :global(.tile.rewind .cam svg) {
			animation: rewind 1s cubic-bezier(0.65, 0, 0.35, 1) both;
		}
		@keyframes rewind {
			to {
				transform: rotate(calc(var(--turn, 0deg) - 360deg));
			}
		}
	}
	.mini {
		height: calc(var(--rows-h) * var(--s));
		overflow: hidden;
	}
	.mini ul {
		transform-origin: 0 0;
	}
	.bar {
		display: block;
		flex: none;
		height: 8px;
		border-radius: 2px;
		background: var(--pic-faint);
	}
	.bar.vb {
		margin-left: auto;
		opacity: 0.6;
	}

	/* The rows in flight: copies, tinted like the working-state section's.
	   The snapshot's rows travel whole into the tile; a restored row wipes
	   in over its own slot. */
	.fly {
		position: absolute;
		inset: 0;
		z-index: 2;
		pointer-events: none;
	}
	.fly :global(.clone) {
		position: absolute;
		margin: 0;
		transform-origin: 0 0;
		background: color-mix(in oklab, var(--pic-accent) 9%, var(--surface));
		box-shadow: inset 2px 0 0 var(--pic-accent);
		will-change: transform;
	}
	.fly :global(.wipe) {
		position: absolute;
		margin: 0;
		background: color-mix(in oklab, var(--pic-accent) 8%, var(--surface));
	}
	.fly :global(.clone .mark),
	.fly :global(.wipe .mark) {
		visibility: hidden;
	}

	@container (max-width: 410px) {
		.pic {
			--pad: 4px;
			--tile-w: 92px;
			/* frame, edges, two insets, the machine's and the tile's head
			   and edges (60px) and the 8px between them */
			--rows-h: calc((var(--shot-h) - 2px - 2 * var(--shot-in) - 68px) / (1 + var(--s)));
			grid-template-columns: 18px minmax(0, 1fr);
			grid-template-rows: auto auto;
			gap: 8px 4px;
			font-size: 12px;
		}
		.machine {
			grid-column: 1 / -1;
			height: auto;
		}
		.fr {
			gap: 5px;
			padding: 0 9px;
		}
		.nm,
		.val {
			font-size: 11px;
		}
		.arr {
			width: 14px;
			transform: rotate(90deg);
		}
		.stack {
			height: var(--full);
		}
		.stack :global(.tile) {
			right: auto;
			width: var(--tile-w);
		}
		/* Folded, the older tile is its head beside the newest one. */
		.stack :global(.tile.old) {
			top: 0;
			left: calc(var(--tile-w) + 6px);
		}
	}
</style>
