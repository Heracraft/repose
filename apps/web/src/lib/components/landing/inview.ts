const QUERY = '(prefers-reduced-motion: reduce)';

/**
 * Calls onChange with the setting at once and again each time the visitor
 * changes it (the OS switch flips while the page is open), and returns the
 * function that stops listening. A picture stops its loop and draws its
 * final frame when it is called with true. A no-op during SSR.
 */
export function watchReducedMotion(onChange: (reduce: boolean) => void): () => void {
	if (typeof window === 'undefined') return () => {};
	const mq = window.matchMedia(QUERY);
	const listener = (e: MediaQueryListEvent) => onChange(e.matches);
	onChange(mq.matches);
	mq.addEventListener('change', listener);
	return () => mq.removeEventListener('change', listener);
}

/**
 * Marks a group of shapes as landed (data-land="in") the first time it
 * comes into view; `.land` children in layout.css then land one after
 * another, each by its own --d delay. If the visitor turns on reduced
 * motion before the group lands, the group is marked landed at once and
 * the observer stops, so nothing is left waiting to move.
 */
export function landOnView(node: HTMLElement) {
	node.dataset.land = 'out';
	let io: IntersectionObserver | undefined;
	// Declared before the watch starts: it calls back at once, and with
	// reduced motion already on, land() runs before the watch returns.
	let stop: () => void = () => {};
	let landed = false;
	const land = () => {
		landed = true;
		node.dataset.land = 'in';
		io?.disconnect();
		io = undefined;
		stop();
	};
	stop = watchReducedMotion((reduce) => {
		if (reduce) land();
	});
	if (landed) {
		stop();
		return {};
	}
	io = new IntersectionObserver(
		([e]) => {
			if (e.isIntersecting) land();
		},
		{ threshold: 0.15 }
	);
	io.observe(node);
	return {
		destroy: () => {
			io?.disconnect();
			stop();
		}
	};
}
