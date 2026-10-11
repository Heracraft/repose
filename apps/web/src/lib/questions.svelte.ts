// The count of questions agents are waiting on, across every project, that
// DashNav's Projects badge shows. One count for the whole dashboard, so a
// page that answers a question or reads the pending list updates the
// badge at once; before, the nav polled on its own every 30s and kept
// showing a question the reader had just answered.
import { listQuestions } from './api/client';

class PendingQuestions {
	count = $state(0);
	#at = 0;

	/** Sets the count from a pending list a page has just read. */
	set(n: number): void {
		this.count = n;
		this.#at = Date.now();
	}

	/**
	 * Reads the pending list again, unless it was read less than `maxAge`
	 * ms ago. A failed read keeps the last count and says nothing; the
	 * page's own load reports an outage.
	 */
	async refresh(maxAge = 0): Promise<void> {
		if (Date.now() - this.#at < maxAge) return;
		this.#at = Date.now();
		try {
			this.count = (await listQuestions('pending')).length;
		} catch {
			// Keep the last count.
		}
	}
}

export const pendingQuestions = new PendingQuestions();
