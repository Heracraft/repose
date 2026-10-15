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
