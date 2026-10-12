/**
 * The landing's motion controller: one place that decides when each looping
 * picture plays (DECISIONS I-666, LANDING.md "Motion").
 *
 * A picture makes one `Picture` in its script, puts `use:picture.frame` on
 * the element that holds both the drawing and its `PauseButton`, and calls
 * `picture.start(el, onMode)` where it used to make its own
 * IntersectionObserver. From then on the controller calls `onMode` with
 *
 * - `'play'`: run the loop (resume the timeline, or start a new cycle);
 *   the second argument, `fromStill`, is true when the picture shows its
 *   still frame (it has not played since `start`, or it last rested), so
 *   its first cycle starts at its seam: the still frame is the loop's
 *   last beat, and it leaves through the loop's own exit motion instead
 *   of a cut to the first frame;
 * - `'hold'`: pause the timeline where it is (out of view, paused by the
 *   visitor, or under their pointer or focus);
 * - `'rest'`: a grid picture another grid picture has the turn from: show
 *   the still frame the picture draws with reduced motion.
 *
 * `picture.stop()` (reduced motion turned on, or the component going away)
 * stops the observer and drops the picture from the coordinator.
 *
 * `picture.looped()` is called by a looping picture at its seam, the beat
 * where its frame equals its still frame, once each cycle. For the grid's
 * leader that ends its turn: the next in-view grid picture in DOM order
 * takes it, and the leader gets `'rest'` (its still frame, the frame it
 * already shows) before `looped()` returns.
 *
 * Pausing (WCAG 2.2.2): the visitor's pause holds the frame through scroll
 * out and in, until they press play. Pointer or focus inside the frame
 * holds it too, and play resumes HOLD_MS after both leave.
 *
 * The coordinator: of the pictures in the `'grid'` group, only one plays,
 * the one under the pointer or focus, or else the one last pointed at
 * while it stays in view, or else the one handed the turn by `looped()`
 * while it stays in view, or else the in-view one nearest the viewport's
 * middle. So pictures that share a row take turns, a loop each. The hero
 * and OneCommand are in no group.
 */

export type Mode = 'play' | 'hold' | 'rest';
export type Group = 'grid';

/** How long a picture keeps holding after the pointer or focus leaves. */
export const HOLD_MS = 2000;
/** A new grid picture takes the turn only when nearer the middle by this. */
const HYSTERESIS = 24;

// A registry, not state: nothing renders from it.
// eslint-disable-next-line svelte/prefer-svelte-reactivity
const all = new Set<Picture>();
// The grid picture last pointed at or focused, kept while it is in view.
let chosen: Picture | null = null;
let leader: Picture | null = null;
// The grid picture a finished turn was handed to, kept while it is in view.
let turn: Picture | null = null;
let raf = 0;
let listening = false;

function grid() {
	return [...all].filter((p) => p.group === 'grid' && p.target);
}

function middle(p: Picture) {
	const r = p.target!.getBoundingClientRect();
	return Math.abs(r.top + r.height / 2 - window.innerHeight / 2);
}

function pick(): Picture | null {
	const g = grid();
	const pointed = g.find((p) => p.inside);
	if (pointed) return (chosen = pointed);
	if (chosen && !(g.includes(chosen) && chosen.inView)) chosen = null;
	if (chosen) return chosen;
	if (turn && !(g.includes(turn) && turn.inView)) turn = null;
	if (turn) return turn;
	const seen = g.filter((p) => p.inView);
	if (!seen.length) return null;
	let best = seen[0];
	let bestD = middle(best);
	for (const p of seen.slice(1)) {
		const d = middle(p);
		if (d < bestD) [best, bestD] = [p, d];
	}
	// Keep the current one unless another is clearly nearer: two cells in
	// one row are the same distance from the middle.
	if (leader && leader !== best && seen.includes(leader) && middle(leader) - bestD < HYSTERESIS)
		return leader;
	return best;
}

function coordinate() {
	if (raf) cancelAnimationFrame(raf);
	raf = 0;
	leader = pick();
	for (const p of all) p.update();
}

function schedule() {
	if (typeof window === 'undefined' || raf) return;
	raf = requestAnimationFrame(coordinate);
}

function listen(on: boolean) {
	if (on === listening) return;
	listening = on;
	if (on) {
		window.addEventListener('scroll', schedule, { passive: true });
		window.addEventListener('resize', schedule);
	} else {
		window.removeEventListener('scroll', schedule);
		window.removeEventListener('resize', schedule);
	}
}

export class Picture {
	readonly group: Group | undefined;
	/** The visitor pressed pause: the PauseButton's aria-pressed. */
	paused = $state(false);
	/** The loop is running (motion allowed, anime.js loaded): the button shows. */
	moving = $state(false);

	target: Element | null = null;
	inView = false;
	/** Pointer or focus is inside the frame. */
	inside = false;

	private onMode: ((m: Mode, fromStill: boolean) => void) | null = null;
	// The loop has played since start() or the last 'rest': the frame on
	// screen is the loop's, not the still frame.
	private played = false;
	private mode: Mode | null = null;
	private io: IntersectionObserver | null = null;
	private node: HTMLElement | null = null;
	private hover = false;
	private focus = false;
	private timer: ReturnType<typeof setTimeout> | undefined;
	// Play pressed while pointer or focus is still inside: no hold until
	// both have left.
	private released = false;

	constructor(opts: { group?: Group } = {}) {
		this.group = opts.group;
	}

	/**
	 * Starts watching `target` (the element whose visibility decides play)
	 * and calling `onMode`. `threshold` is the share of it in view that
	 * counts as in view.
	 */
	start(target: Element, onMode: (m: Mode, fromStill: boolean) => void, threshold = 0.5) {
		this.stop();
		this.target = target;
		this.onMode = onMode;
		this.mode = null;
		this.played = false;
		this.moving = true;
		all.add(this);
		listen(true);
		this.io = new IntersectionObserver(
			([e]) => {
				this.inView = e.isIntersecting && e.intersectionRatio >= threshold;
				if (this.group) schedule();
				else this.update();
			},
			{ threshold: [0, threshold] }
		);
		this.io.observe(target);
	}

	/** Stops the observer and leaves the coordinator; the caller draws its still frame. */
	stop() {
		this.io?.disconnect();
		this.io = null;
		this.onMode = null;
		this.mode = null;
		this.target = null;
		this.inView = false;
		this.moving = false;
		if (this.node) delete this.node.dataset.motion;
		if (all.delete(this)) {
			if (chosen === this) chosen = null;
			if (turn === this) turn = null;
			if (!all.size) listen(false);
			schedule();
		}
	}

	toggle() {
		this.paused = !this.paused;
		if (!this.paused) {
			this.released = true;
			clearTimeout(this.timer);
			this.timer = undefined;
		}
		this.update();
	}

	/** The mode now; for a grid picture, `coordinate` has picked the leader. */
	private want(): Mode {
		const held = !this.released && (this.hover || this.focus || this.timer !== undefined);
		if (this.paused || held || !this.inView) return 'hold';
		if (this.group && leader !== this) return 'rest';
		return 'play';
	}

	update() {
		if (!this.onMode) return;
		const m = this.want();
		if (this.node) this.node.dataset.motion = m;
		if (m === this.mode) return;
		this.mode = m;
		const fromStill = !this.played;
		if (m === 'rest') this.played = false;
		if (m === 'play') this.played = true;
		this.onMode(m, fromStill);
	}

	/**
	 * The picture is at its seam: its frame equals its still frame. A grid
	 * leader that nobody points at hands the turn to the next in-view grid
	 * picture in DOM order (wrapping round); the coordinator runs at once,
	 * so this picture's `'rest'` comes before this returns.
	 */
	looped() {
		if (!this.group || leader !== this || this.inside) return;
		if (chosen === this && this.inView) return;
		const seen = grid().filter((p) => p.inView);
		if (seen.length < 2) return;
		seen.sort((a, b) =>
			a.target!.compareDocumentPosition(b.target!) & Node.DOCUMENT_POSITION_FOLLOWING ? -1 : 1
		);
		const i = seen.indexOf(this);
		turn = seen[(i + 1) % seen.length];
		if (turn === this) return;
		coordinate();
	}

	private enter() {
		clearTimeout(this.timer);
		this.timer = undefined;
		this.inside = true;
		this.changed();
	}

	private leave() {
		this.inside = this.hover || this.focus;
		if (this.inside) return;
		clearTimeout(this.timer);
		this.timer = undefined;
		if (this.released) {
			// Play was pressed inside: it plays on, with no hold after.
			this.released = false;
			return this.changed();
		}
		this.timer = setTimeout(() => {
			this.timer = undefined;
			this.changed();
		}, HOLD_MS);
		this.changed();
	}

	private changed() {
		if (this.group) schedule();
		else this.update();
	}

	/**
	 * Svelte action for the element that holds the picture and its pause
	 * button: pointer and focus inside it hold the frame. It also carries
	 * `data-motion` (the current mode) and `data-motion-group`.
	 */
	frame = (node: HTMLElement) => {
		this.node = node;
		node.classList.add('pic-frame');
		if (this.group) node.dataset.motionGroup = this.group;
		// A hold starts on the pointer's own movement or a press, not on
		// pointerenter: scrolling past a resting pointer enters the frame
		// too, with no movement, and must not stop the picture under it.
		const on = (e: PointerEvent) => {
			if (this.hover) return;
			if (e.type === 'pointermove' && !e.movementX && !e.movementY) return;
			this.hover = true;
			this.enter();
		};
		const off = () => {
			if (!this.hover) return;
			this.hover = false;
			this.leave();
		};
		const fin = () => {
			if (!this.focus) {
				this.focus = true;
				this.enter();
			}
		};
		const fout = (e: FocusEvent) => {
			if (node.contains(e.relatedTarget as Node | null)) return;
			this.focus = false;
			this.leave();
		};
		node.addEventListener('pointermove', on);
		node.addEventListener('pointerdown', on);
		node.addEventListener('pointerleave', off);
		node.addEventListener('focusin', fin);
		node.addEventListener('focusout', fout);
		return {
			destroy: () => {
				node.removeEventListener('pointermove', on);
				node.removeEventListener('pointerdown', on);
				node.removeEventListener('pointerleave', off);
				node.removeEventListener('focusin', fin);
				node.removeEventListener('focusout', fout);
				clearTimeout(this.timer);
				this.stop();
				this.node = null;
			}
		};
	};
}

/**
 * Calls `onWidth(width, before)` when `node`'s own width changes, and never
 * for a change of height alone: a phone's address bar collapsing resizes
 * the window, not the picture's width, and must not restart a loop (F20).
 * Returns the function that stops watching. A no-op during SSR.
 */
export function watchWidth(
	node: Element,
	onWidth: (width: number, before: number) => void
): () => void {
	if (typeof ResizeObserver === 'undefined') return () => {};
	let last = -1;
	const ro = new ResizeObserver(([e]) => {
		const w = Math.round(e.contentRect.width);
		if (last < 0) {
			last = w;
			return;
		}
		if (w === last) return;
		const before = last;
		last = w;
		onWidth(w, before);
	});
	ro.observe(node);
	return () => ro.disconnect();
}
