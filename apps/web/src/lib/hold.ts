// A question being answered on the dashboard never expires under the
// owner's hands (DECISIONS I-1242). While a row's field has focus or a
// draft, or the secret dialog is open for a question, the card holds the
// question: once at the start and then every HOLD_EVERY_MS, each hold
// moving the api's expiry to five minutes from now. The api takes at most
// one hold per 30 s per question, so this interval is the page's, not the
// api's. A hold that stops (blur with an empty field, an answer, the
// dialog closing, the card leaving) lets the question run out five
// minutes later at the latest.

/** How often a held question is held again. */
export const HOLD_EVERY_MS = 60_000;

/**
 * One timer per held id. `start` holds at once and then on the interval;
 * a second `start` for the same id changes nothing, so a row may say
 * "holding" on every keystroke. `stop` ends one, `stopAll` every one.
 */
export class Holder {
	#timers = new Map<string, ReturnType<typeof setInterval>>();
	#every: number;

	constructor(every = HOLD_EVERY_MS) {
		this.#every = every;
	}

	/** Whether id is being held. */
	holding(id: string): boolean {
		return this.#timers.has(id);
	}

	start(id: string, hold: () => void | Promise<void>): void {
		if (this.#timers.has(id)) return;
		this.#timers.set(
			id,
			setInterval(() => void hold(), this.#every)
		);
		void hold();
	}

	stop(id: string): void {
		const t = this.#timers.get(id);
		if (t === undefined) return;
		clearInterval(t);
		this.#timers.delete(id);
	}

	stopAll(): void {
		for (const id of [...this.#timers.keys()]) this.stop(id);
	}
}
