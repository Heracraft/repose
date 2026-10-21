// A box that scrolls sideways (a command block on a phone) is one a
// keyboard user cannot read to its end unless it takes focus (axe
// scrollable-region-focusable; only Chromium focuses one on its own).
// DocPage marks the docs' blocks the same way.

/**
 * A Svelte action: the box takes a tab stop while it is wider than its
 * content box and gives it up when it fits, checked again on resize of
 * the box or its first child, so where the text fits Tab skips it. With
 * a `label`, the box is also a named region while it scrolls (a focusable
 * box needs a name), and a plain box when it fits.
 */
export function tabStopWhenScrolls(node: HTMLElement, label?: string) {
	let name = label;
	const mark = () => {
		const scrolls = node.scrollWidth > node.clientWidth;
		node.dataset.scrolls = scrolls ? 'true' : 'false';
		if (scrolls) node.tabIndex = 0;
		else node.removeAttribute('tabindex');
		if (name && scrolls) {
			node.setAttribute('role', 'region');
			node.setAttribute('aria-label', name);
		} else if (name) {
			node.removeAttribute('role');
			node.removeAttribute('aria-label');
		}
	};
	mark();
	const ro = new ResizeObserver(mark);
	ro.observe(node);
	if (node.firstElementChild) ro.observe(node.firstElementChild);
	return {
		update(next?: string) {
			name = next;
			mark();
		},
		destroy() {
			ro.disconnect();
		}
	};
}
