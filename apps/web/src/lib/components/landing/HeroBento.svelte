<!--
  HeroBento: the hero's picture as a bento (hero loop, 2026-11-12, the
  owner's "tiles on a huge subtle grid that connect in different ways and
  strengths"). Seven tiles of different sizes sit in the cells of a faint
  hairline grid, each one idea in plain words with a small true picture:
  what wakes the agent, any agent, the machine itself (the anchor, an
  on-call responder at work), how big it is, the walls around it, that it
  keeps working with your laptop shut, and that it asks you on your phone.
  Connectors run along the grid lines between tiles that work together,
  at three strengths: lit (the accent), solid and dashed.

  The loop: the machine freezes and the lit path goes out; an alert fires,
  the path runs into the machine, it wakes and works down its log, then the
  path runs along the grid to the phone, which asks. It moves by class
  changes (colour, opacity) and stroke-dashoffset, timed by anime.js.

  Still frame (reduced motion, no script, and the loop's seam): the whole
  path lit, the machine running with its log written, the phone asking.
-->
<script lang="ts">
	import { onMount } from 'svelte';
	import { watchReducedMotion } from './inview';
	import { Picture } from './motion.svelte';
	import { agentMarks, type Mark } from '$lib/components/illustrations/marks';

	type Glyph = 'bell' | 'clock' | 'globe' | 'laptop' | 'shut' | 'phone';

	const agents = ['Claude Code', 'Codex', 'opencode', 'Gemini CLI', 'pi'].map(
		(n) => agentMarks.find((m) => m.name === n)!
	);
	const claude = agents[0];

	const wakes: { glyph: Glyph; text: string; lit?: boolean }[] = [
		{ glyph: 'bell', text: 'an alert goes off', lit: true },
		{ glyph: 'clock', text: 'every morning at 7' },
		{ glyph: 'globe', text: 'a new job is posted' }
	];
	const log = [
		{ t: '02:14', text: 'An alert: checkout is slow' },
		{ t: '02:14', text: 'Woke up in 2 seconds' },
		{ t: '02:15', text: 'Read the logs and the last change' },
		{ t: '02:17', text: 'Found it: the database disk is full' },
		{ t: '02:17', text: 'Asked you before fixing it', ask: true },
		{ t: '02:17', text: 'Waiting for your answer', wait: true }
	];
	const brings = ["your app's code", 'your tools', 'your metrics login'];

	/*
	  The grid and its connectors, in the grid's own units: x in percent
	  of the picture's width, y in pixels. They are drawn in an SVG
	  stretched over the picture with strokes that do not scale, so a
	  connector runs on the grid's hairlines at any width.

	  From 1200px: twelve columns with 2% gutters (the line between
	  columns k and k + 1 at 8.5k - 1) and four rows of 84px with 24px
	  between (the line under row r at 108r - 12). Below: two columns with
	  a 3.5% gutter and fourteen rows of 60px with 12px between (the line
	  under row r at 72r - 6).
	*/
	type Layout = {
		name: 'wide' | 'narrow';
		box: [number, number, number, number];
		cols: number[];
		rows: number[];
		lit: string[];
		len: ((w: number) => number)[];
		firm: string[];
		faint: string[];
		dots: [number, number, ('a' | 'b')?][];
	};
	const gx = (k: number) => 8.5 * k - 1;
	const gy = (r: number) => 108 * r - 12;
	const ny = (r: number) => 72 * r - 6;
	const range = (n: number) => Array.from({ length: n }, (_, i) => i);
	const ALERT = 81; // the alert chip's middle
	const ASK = 248; // the log's last line
	const N_ASK = 370;
	const layouts: Layout[] = [
		{
			name: 'wide',
			box: [-1, -12, 102, 432],
			cols: range(13).map(gx),
			rows: range(5).map(gy),
			lit: [`M23.5 ${ALERT}H25.5`, `M57.5 ${ASK}H${gx(7)}V${gy(1)}H${gx(11)}V108`],
			len: [
				(w) => (2 * w) / 100,
				(w) => ((gx(11) - 57.5) * w) / 100 + (ASK - gy(1)) + (108 - gy(1))
			],
			firm: [`M23.5 312H25.5`, `M57.5 42H59.5`],
			faint: [`M57.5 204H59.5`, `M57.5 366H59.5`],
			dots: [
				[gx(3), ALERT, 'a'],
				[gx(7), ASK, 'b'],
				[gx(7), gy(1), 'b'],
				[gx(11), gy(1), 'b'],
				[gx(3), 312],
				[gx(7), 42],
				[gx(7), 204],
				[gx(7), 366]
			]
		},
		{
			name: 'narrow',
			box: [-1.75, -6, 103.5, 1008],
			cols: [-1.75, 50, 101.75],
			rows: range(15).map(ny),
			lit: [`M25 132V144`, `M100 ${N_ASK}H101.75V${ny(7)}H25V504`],
			len: [() => 12, (w) => (78.5 * w) / 100 + (ny(7) - N_ASK) + 6],
			firm: [`M75 636V648`, `M50 852V864`],
			faint: [`M25 780V792`],
			dots: [
				[25, ny(2), 'a'],
				[101.75, N_ASK, 'b'],
				[101.75, ny(7), 'b'],
				[25, ny(7), 'b'],
				[75, ny(9)],
				[50, ny(12)],
				[25, ny(11)]
			]
		}
	];

	let pic: HTMLDivElement;
	const picture = new Picture();

	onMount(() => {
		let dead = false;
		let run = 0;
		let tl: { pause(): unknown; play(): unknown } | null = null;

		function rest() {
			tl?.pause();
			tl = null;
			pic.querySelectorAll('.hot').forEach((el) => el.removeAttribute('style'));
			pic.classList.remove('cold', 'quiet');
			pic.querySelectorAll('.hide').forEach((el) => el.classList.remove('hide'));
		}

		function start() {
			const me = ++run;
			import('animejs').then(({ createTimeline }) => {
				if (dead || me !== run) return;
				const wide = () => matchMedia('(min-width: 1200px)').matches;
				const path = (i: number) =>
					pic.querySelector<SVGPathElement>(
						`.lines.${wide() ? 'wide' : 'narrow'} .hot[data-i="${i}"]`
					)!;
				const rows = [...pic.querySelectorAll<HTMLElement>('.log li')];
				const cls = (c: string, on: boolean) => () => pic.classList.toggle(c, on);
				// Draw a lit path from nothing: dashed to its own length on
				// screen, so the dash offset runs it out along the grid.
				function draw(t: TL, i: number, at: number, duration: number) {
					const L = layouts[wide() ? 0 : 1].len[i](pic.clientWidth);
					const el = path(i);
					t.call(() => {
						el.style.strokeDasharray = `${L} ${L}`;
						el.style.strokeDashoffset = `${L}`;
						el.style.opacity = '1';
					}, at - 1);
					t.add(el, { strokeDashoffset: [L, 0], duration, ease: 'inOutSine' }, at);
				}
				type TL = ReturnType<typeof createTimeline>;
				function cycle() {
					if (dead || me !== run) return;
					const t = createTimeline({ autoplay: false, onComplete: () => cycle() });
					// Out of the still frame: the lit path fades, the machine
					// freezes and its log clears, the phone goes quiet.
					t.add([path(0), path(1)], { opacity: [1, 0], duration: 600, ease: 'outQuad' }, 0);
					t.call(cls('cold', true), 300).call(cls('quiet', true), 300);
					t.call(() => rows.forEach((r) => r.classList.add('hide')), 300);
					// An alert: the chip fires, the path runs in, the machine wakes
					// and works down its log.
					t.call(cls('cold', false), 2200);
					draw(t, 0, 2200, 500);
					const at = [2700, 3200, 4200, 5300, 6300, 8100];
					rows.forEach((r, i) => t.call(() => r.classList.remove('hide'), at[i]));
					// It asks: the path runs along the grid to the phone.
					draw(t, 1, 6400, 1400);
					t.call(cls('quiet', false), 7700);
					t.call(() => {}, 13000);
					tl = t;
					t.play();
				}
				picture.start(pic, (m) => {
					if (m === 'rest') rest();
					else if (m === 'hold') tl?.pause();
					else if (tl) tl.play();
					else cycle();
				});
			});
		}

		const unwatch = watchReducedMotion((reduce) => {
			if (reduce) {
				run++;
				picture.stop();
				rest();
			} else start();
		});
		return () => {
			dead = true;
			unwatch();
			picture.stop();
			tl?.pause();
		};
	});
</script>

{#snippet markSvg(m: Mark, size = 16)}
	<svg viewBox="0 0 24 24" width={size} height={size} fill="currentColor" aria-hidden="true"
		>{#each m.paths as d (d)}<path
				{d}
				fill-rule={m.evenodd ? 'evenodd' : 'nonzero'}
				clip-rule={m.evenodd ? 'evenodd' : 'nonzero'}
			/>{/each}</svg
	>
{/snippet}

{#snippet glyph(kind: Glyph, size = 14)}
	<svg
		class="gl"
		viewBox="0 0 16 16"
		width={size}
		height={size}
		fill="none"
		stroke="currentColor"
		stroke-width="1.25"
		stroke-linejoin="round"
		stroke-linecap="round"
		aria-hidden="true"
	>
		{#if kind === 'bell'}
			<path d="M4 11V7.5a4 4 0 0 1 8 0V11l1 1.5H3zM6.5 14h3" />
		{:else if kind === 'clock'}
			<circle cx="8" cy="8" r="5.5" /><path d="M8 5v3.25l2 1.25" />
		{:else if kind === 'globe'}
			<circle cx="8" cy="8" r="5.5" /><path
				d="M2.5 8h11M8 2.5c-1.8 1.6-2.6 3.4-2.6 5.5s.8 3.9 2.6 5.5M8 2.5c1.8 1.6 2.6 3.4 2.6 5.5s-.8 3.9-2.6 5.5"
			/>
		{:else if kind === 'laptop'}
			<rect x="3" y="3.5" width="10" height="7" rx="0.75" /><path d="M1.5 12.5h13" />
		{:else if kind === 'shut'}
			<path d="M2 9.25h12M1 11.5h14l-.75 1.25H1.75z" />
		{:else}
			<rect x="4.5" y="1.5" width="7" height="13" rx="1.25" /><path d="M7 12.25h2" />
		{/if}
	</svg>
{/snippet}

{#snippet brick()}
	<svg class="brick" viewBox="0 0 13 47" width="13" height="47" aria-hidden="true">
		{#each { length: 5 }, r (r)}
			{#if r % 2}
				<rect x="0" y={r * 9.5} width="5.75" height="8" rx="1" /><rect
					x="7.25"
					y={r * 9.5}
					width="5.75"
					height="8"
					rx="1"
				/>
			{:else}
				<rect x="0" y={r * 9.5} width="13" height="8" rx="1" />
			{/if}
		{/each}
	</svg>
{/snippet}

<div class="bn-frame" use:picture.frame>
	<div
		class="bn"
		bind:this={pic}
		role="img"
		aria-label="Seven tiles joined by lines. An alert goes off, a schedule comes round or a new job is posted, and that wakes your agent. Claude Code, an on-call responder, runs on a machine of its own with your app's code, your tools and your metrics login: it woke up because checkout is slow, read the logs, found the database disk is full, and asked you before fixing it. It asks on your phone: delete old logs to free space, yes or no. Any agent runs: Claude Code, Codex, opencode, Gemini CLI or pi. Each machine is as big as its job, from 2 GB to 16 GB. It is walled off from your laptop and the internet, and keeps working with your laptop shut."
	>
		{#each layouts as g (g.name)}
			<svg
				class="lines {g.name}"
				viewBox={g.box.join(' ')}
				preserveAspectRatio="none"
				style="left: {g.box[0]}%; top: {g.box[1]}px; width: {g.box[2]}%; height: {g.box[3]}px"
				aria-hidden="true"
			>
				{#each g.faint as d (d)}<path class="faint" {d} />{/each}
				{#each g.firm as d (d)}<path class="firm" {d} />{/each}
				{#each g.lit as d (d)}<path class="under" {d} />{/each}
				{#each g.lit as d, i (d)}<path class="hot" data-i={i} {d} />{/each}
			</svg>
			<div class="mesh {g.name}" aria-hidden="true">
				{#each g.cols as x (x)}<i class="v" style="left: {x}%"></i>{/each}
				{#each g.rows as y (y)}<i class="h" style="top: {y}px"></i>{/each}
				{#each g.dots as [x, y, on] (`${x}-${y}`)}
					<i class="dot {on ?? ''}" class:on style="left: {x}%; top: {y}px"></i>
				{/each}
			</div>
		{/each}

		<div class="tiles" aria-hidden="true">
			<section class="tile wake">
				<h3>Wakes on an alert, a schedule or a webhook</h3>
				<ul class="chips">
					{#each wakes as w (w.text)}
						<li class:on={w.lit}>{@render glyph(w.glyph)}{w.text}</li>
					{/each}
				</ul>
			</section>

			<section class="tile agents">
				<h3>Any agent</h3>
				<ul class="marks">
					{#each agents as m (m.name)}
						<li class:cur={m === claude}>{@render markSvg(m)}{m.name}</li>
					{/each}
				</ul>
			</section>

			<section class="tile machine">
				<h3>Your agent, on a machine of its own</h3>
				<div class="win">
					<div class="head">
						<span class="ag">{@render markSvg(claude, 18)}<span class="bd">{claude.name}</span></span>
						<span class="state"
							><i class="run"></i><span class="sw"
								><span class="s-run">running</span><span class="s-frz">frozen</span></span
							></span
						>
					</div>
					<ol class="log">
						{#each log as l (l.text)}
							<li class:ask={l.ask} class:wait={l.wait}>
								<time>{l.t}</time><span>{l.text}</span>{#if l.ask}<i class="stub"></i>{/if}
							</li>
						{/each}
					</ol>
					<div class="brings">
						<span class="nm">It has</span>
						{#each brings as b (b)}<span class="tag">{b}</span>{/each}
					</div>
				</div>
			</section>

			<section class="tile size">
				<h3>As big as the job needs</h3>
				<div class="meter">
					<span class="ends">2 GB</span>
					<span class="bar"
						>{#each { length: 8 }, i (i)}<i class:f={i < 1}></i>{/each}</span
					>
					<span class="ends">16 GB</span>
				</div>
			</section>

			<section class="tile walls">
				<h3>Walled off from your laptop and the internet</h3>
				<div class="wall">
					<span class="side">{@render glyph('laptop', 22)}<small>your laptop</small></span>
					<i class="gap"></i>
					{@render brick()}
					<span class="box">{@render markSvg(claude, 20)}</span>
					{@render brick()}
					<i class="gap"></i>
					<span class="side">{@render glyph('globe', 22)}<small>the internet</small></span>
				</div>
			</section>

			<section class="tile shut">
				<span class="lid">{@render glyph('shut', 32)}<i class="run"></i></span>
				<h3>Keeps working with your laptop shut</h3>
			</section>

			<section class="tile phone">
				<h3>Asks you on your phone</h3>
				<div class="handset">
					<i class="ear"></i>
					<span class="clock">02:17</span>
					<div class="note">
						<span class="from">{@render markSvg(claude, 12)}Claude Code</span>
						<p>The disk is full. Delete old logs to free space?</p>
						<span class="btns"><span class="bd">No</span><span class="bd yes">Yes</span></span>
					</div>
				</div>
			</section>
		</div>
	</div>
</div>

<style>
	.bn {
		position: relative;
		color: var(--pic-ink);
		font-family: var(--font-sans);
		font-size: 13px;
		line-height: 18px;
	}

	/* The grid: twelve columns with 2% gutters, four 84px rows with 24px
	   between. The hairlines run down the middle of each gutter. */
	.tiles {
		position: relative;
		display: grid;
		grid-template-columns: repeat(12, minmax(0, 1fr));
		grid-template-rows: repeat(4, 84px);
		column-gap: 2%;
		row-gap: 24px;
	}
	.lines {
		position: absolute;
		overflow: visible;
		fill: none;
	}
	.narrow {
		display: none;
	}
	.lines path {
		vector-effect: non-scaling-stroke;
	}
	/* The grid's hairlines run on through the whole field: the
	   columns from its top to its bottom, the rows from edge to edge. */
	.mesh {
		position: absolute;
		inset: 0;
	}
	.mesh > i {
		position: absolute;
	}
	.mesh > :is(.v, .h) {
		background: var(--rule);
	}
	.mesh > .v {
		top: calc(-1 * var(--field-y, 48px) - 12px);
		bottom: calc(-1 * var(--field-y, 48px) - 12px);
		width: 1px;
	}
	.mesh > .h {
		left: calc(-1 * var(--edge, 24px));
		right: calc(-1 * var(--edge, 24px));
		height: 1px;
	}
	.faint {
		stroke: var(--rule-strong);
		stroke-dasharray: 3 3;
	}
	.firm {
		stroke: var(--pic-faint);
	}
	.under {
		stroke: var(--rule-strong);
	}
	.hot {
		stroke: var(--pic-accent);
		stroke-width: 1.5;
	}
	.dot {
		position: absolute;
		width: 7px;
		height: 7px;
		margin: -3.5px 0 0 -3.5px;
		border: 1px solid var(--pic-faint);
		border-radius: 50%;
		background: var(--stage-hero, #101012);
	}
	.dot.on {
		border-color: var(--pic-accent);
		background: var(--pic-accent);
		transition:
			background-color 300ms ease,
			border-color 300ms ease;
	}
	.bn:global(.cold) .dot.a,
	.bn:global(.quiet) .dot.b {
		border-color: var(--pic-faint);
		background: var(--stage-hero, #101012);
	}

	.tile {
		position: relative;
		display: flex;
		flex-direction: column;
		gap: 12px;
		min-width: 0;
		padding: 14px 16px;
		border: 1px solid var(--rule-strong);
		border-radius: 6px;
		background: var(--surface);
	}
	h3 {
		margin: 0;
		font-family: var(--font-sans);
		font-size: 14px;
		font-weight: 500;
		line-height: 19px;
		color: var(--pic-ink);
		text-wrap: balance;
	}
	ul,
	ol {
		margin: 0;
		padding: 0;
		list-style: none;
	}
	.gl {
		flex: none;
	}

	.wake {
		grid-area: 1 / 1 / 3 / 4;
	}
	.agents {
		grid-area: 3 / 1 / 5 / 4;
	}
	.machine {
		grid-area: 1 / 4 / 5 / 8;
		padding: 14px;
		background: var(--sunken);
	}
	.size {
		grid-area: 1 / 8 / 2 / 13;
	}
	.walls {
		grid-area: 2 / 8 / 4 / 11;
	}
	.shut {
		grid-area: 4 / 8 / 5 / 11;
	}
	.phone {
		grid-area: 2 / 11 / 5 / 13;
	}

	/* What wakes it: three chips, the alert lit. */
	.chips {
		display: grid;
		gap: 8px;
	}
	.chips li {
		display: flex;
		align-items: center;
		gap: 8px;
		padding: 6px 10px;
		border: 1px solid var(--rule-strong);
		border-radius: 6px;
		color: var(--pic-dim);
		transition:
			background-color 200ms ease,
			border-color 200ms ease,
			color 200ms ease;
	}
	.chips li.on {
		position: relative;
		border-color: var(--pic-accent);
		color: var(--pic-accent);
	}
	/* The lit path starts at the chip: its stub runs to the tile's edge. */
	.chips li.on::after {
		position: absolute;
		top: 50%;
		left: 100%;
		width: 17px;
		height: 1.5px;
		margin-top: -0.75px;
		background: var(--pic-accent);
		content: '';
		transition: opacity 300ms ease;
	}
	.bn:global(.cold) .chips li.on {
		border-color: var(--rule-strong);
		color: var(--pic-dim);
	}
	.bn:global(.cold) .chips li.on::after {
		opacity: 0;
	}

	/* Any agent: the five marks with their names. */
	.marks {
		display: flex;
		flex-wrap: wrap;
		gap: 8px;
	}
	.marks li {
		display: flex;
		align-items: center;
		gap: 7px;
		padding: 5px 10px 5px 8px;
		border: 1px solid var(--rule);
		border-radius: 6px;
		color: var(--pic-dim);
	}
	.marks li.cur {
		border-color: var(--rule-strong);
		color: var(--pic-ink);
	}

	/* The machine: a window with the agent at work. */
	.win {
		display: flex;
		flex: 1;
		flex-direction: column;
		min-height: 0;
		border: 1px solid var(--control-edge);
		border-radius: 6px;
		background: var(--surface);
		transition: border-color 300ms ease;
	}
	.bn:global(.cold) .win {
		border-color: var(--rule-strong);
	}
	.head {
		display: flex;
		align-items: center;
		gap: 10px;
		height: 42px;
		padding: 0 14px;
		border-bottom: 1px solid var(--rule);
	}
	.ag {
		display: flex;
		align-items: center;
		gap: 8px;
		color: var(--pic-accent);
		font-size: 14px;
	}
	.ag .bd {
		font-weight: 600;
	}
	.state {
		display: flex;
		align-items: center;
		gap: 6px;
		margin-left: auto;
		font-family: var(--font-mono);
		font-size: 12px;
		color: var(--pic-ink);
	}
	.run {
		width: 6px;
		height: 6px;
		border-radius: 50%;
		background: var(--color-emerald-500);
		transition: background-color 300ms ease;
	}
	.bn:global(.cold) .machine .run {
		background: var(--pic-faint);
	}
	.sw {
		display: inline-grid;
	}
	.sw > span {
		grid-area: 1 / 1;
		text-align: right;
		transition: opacity 200ms ease;
	}
	.s-frz,
	.bn:global(.cold) .s-run {
		opacity: 0;
	}
	.bn:global(.cold) .s-frz {
		opacity: 1;
	}

	.log {
		display: grid;
		flex: 1;
		align-content: start;
		padding: 6px 0;
	}
	.log li {
		display: flex;
		align-items: baseline;
		gap: 14px;
		height: 34px;
		padding: 8px 14px 0;
		transition: opacity 300ms ease;
	}
	.log li:global(.hide) {
		opacity: 0;
	}
	.log time {
		flex: none;
		font-family: var(--font-mono);
		font-size: 12px;
		color: var(--pic-faint);
	}
	.log li.wait span {
		color: var(--pic-faint);
	}
	.log li.ask span {
		color: var(--pic-accent);
	}
	/* The lit path leaves from the question: a stub from its words to the
	   tile's edge. */
	.stub {
		flex: 1;
		align-self: center;
		height: 1.5px;
		margin: 2px -15px 0 -4px;
		background: var(--pic-accent);
	}
	.brings {
		display: flex;
		flex-wrap: wrap;
		align-items: center;
		gap: 6px;
		padding: 12px 14px;
		border-top: 1px solid var(--rule);
		border-radius: 0 0 5px 5px;
		background: var(--sunken);
	}
	.nm {
		margin-right: 4px;
		color: var(--pic-faint);
	}
	.tag {
		padding: 2px 8px;
		border: 1px solid var(--rule-strong);
		border-radius: 6px;
		color: var(--pic-dim);
	}

	/* How big: a meter from 2 GB to 16 GB, this one at 2. */
	.meter {
		display: flex;
		align-items: center;
		gap: 10px;
		font-family: var(--font-mono);
		font-size: 12px;
		color: var(--pic-dim);
	}
	.bar {
		display: flex;
		flex: 1;
		gap: 3px;
	}
	.bar i {
		flex: 1;
		height: 12px;
		border-radius: 1px;
		background: var(--rule);
	}
	.bar i.f {
		background: var(--pic-accent);
	}

	/* The walls: the machine between your laptop and the internet. */
	.wall {
		display: flex;
		flex: 1;
		align-items: center;
		color: var(--pic-faint);
	}
	.wall {
		gap: 6px;
		justify-content: center;
	}
	.side {
		position: relative;
		display: flex;
		justify-content: center;
		width: 22px;
	}
	.side small {
		position: absolute;
		top: calc(100% + 8px);
		font-size: 12px;
		white-space: nowrap;
		color: var(--pic-faint);
	}
	.gap {
		width: 22px;
		border-top: 1px dashed var(--pic-faint);
	}
	.brick {
		flex: none;
		fill: var(--pic-dim);
	}
	.box {
		display: grid;
		place-items: center;
		width: 46px;
		height: 46px;
		border: 1px solid var(--control-edge);
		border-radius: 6px;
		color: var(--pic-accent);
	}

	.shut {
		flex-direction: row;
		align-items: center;
	}
	.lid {
		position: relative;
		display: flex;
		color: var(--pic-dim);
	}
	.lid .run {
		position: absolute;
		top: 8px;
		right: -2px;
	}

	/* The phone: its lock screen at 02:17, the agent's question on it. */
	.handset {
		display: flex;
		flex: 1;
		flex-direction: column;
		align-items: stretch;
		gap: 10px;
		padding: 10px 8px 8px;
		border: 1px solid var(--control-edge);
		border-radius: 14px;
		background: var(--sunken);
	}
	.ear {
		align-self: center;
		width: 28px;
		height: 4px;
		border-radius: 2px;
		background: var(--rule-strong);
	}
	.clock {
		font-size: 22px;
		font-weight: 300;
		line-height: 26px;
		text-align: center;
		color: var(--pic-dim);
	}
	.note {
		display: flex;
		flex-direction: column;
		gap: 8px;
		margin-top: auto;
		padding: 10px;
		border: 1px solid var(--pic-accent);
		border-radius: 8px;
		background: var(--surface);
		transition:
			opacity 300ms ease,
			transform 300ms ease;
	}
	.bn:global(.quiet) .note {
		opacity: 0;
		transform: translateY(6px);
	}
	.from {
		display: flex;
		align-items: center;
		gap: 6px;
		font-size: 12px;
		color: var(--pic-dim);
	}
	.note p {
		margin: 0;
	}
	.btns {
		display: grid;
		grid-template-columns: 1fr 1fr;
		gap: 6px;
	}
	.btns .bd {
		padding: 3px 0;
		border: 1px solid var(--rule-strong);
		border-radius: 6px;
		font-weight: 500;
		text-align: center;
		color: var(--pic-dim);
	}
	.btns .yes {
		border-color: var(--pic-accent);
		color: var(--pic-accent);
	}

	/* Below 1200px: a two-column bento, the machine across both, the
	   phone beside the walls and the meter. */
	@media (max-width: 1199.98px) {
		.bn {
			max-width: 34rem;
			margin-inline: auto;
		}
		.wide {
			display: none;
		}
		.narrow {
			display: block;
		}
		.tiles {
			grid-template-columns: repeat(2, minmax(0, 1fr));
			grid-template-rows: repeat(14, 60px);
			column-gap: 3.5%;
			row-gap: 12px;
		}
		.tile {
			gap: 10px;
			padding: 12px 13px;
		}
		.wake {
			grid-area: 1 / 1 / 3 / 3;
		}
		.chips {
			display: flex;
			flex-wrap: wrap;
		}
		.chips li.on::after {
			display: none;
		}
		.machine {
			grid-area: 3 / 1 / 8 / 3;
			padding: 12px;
		}
		.log li {
			height: 30px;
			padding-top: 6px;
		}
		.stub {
			margin-right: -13px;
		}
		.phone {
			grid-area: 8 / 1 / 12 / 2;
		}
		.walls {
			grid-area: 8 / 2 / 10 / 3;
		}
		.size {
			grid-area: 10 / 2 / 12 / 3;
		}
		.shut {
			grid-area: 12 / 1 / 13 / 3;
		}
		.agents {
			grid-area: 13 / 1 / 15 / 3;
		}
		.meter {
			display: grid;
			grid-template-columns: auto auto;
			justify-content: space-between;
			row-gap: 8px;
		}
		.bar {
			grid-column: 1 / -1;
			grid-row: 1;
		}
		.gap {
			width: 6px;
		}
		.wall {
			gap: 4px;
		}
		.side small,
		.log li.wait {
			display: none;
		}
		.box {
			width: 38px;
			height: 38px;
		}
		.brick {
			width: 10px;
			height: 36px;
		}
	}
	@media (prefers-reduced-motion: reduce) {
		.tile *,
		.tile *::after {
			transition: none !important;
		}
	}
</style>
