// A clock that ticks every 30 seconds while something on the page uses
// it: the questions' "asked 3m ago" and "expires in 4m" move with it, not
// with the api poll, which slows to a minute in a hidden tab. One timer for
// the page however many rows read it.
import { browser } from '$app/environment';

const TICK_MS = 30_000;

class Clock {
	now = $state(Date.now());
	#users = 0;
	#timer: ReturnType<typeof setInterval> | undefined;

	/**
	 * Reads the clock between ticks, for a fact that has just arrived and
	 * is drawn against it. A question held open (I-1242) comes back from
	 * the api as "five minutes from now": against a tick up to 30 seconds
	 * old, `minutesLeft` rounds that up to "expires in 6m", a minute more
	 * than the owner has.
	 */
	read(): number {
		if (browser) this.now = Date.now();
		return this.now;
	}

	/** Starts the ticks for one user; call the returned function to stop. */
	use(): () => void {
		if (!browser) return () => {};
		this.now = Date.now();
		if (this.#users++ === 0) this.#timer = setInterval(() => (this.now = Date.now()), TICK_MS);
		return () => {
			if (--this.#users === 0) {
				clearInterval(this.#timer);
				this.#timer = undefined;
			}
		};
	}
}

export const clock = new Clock();
