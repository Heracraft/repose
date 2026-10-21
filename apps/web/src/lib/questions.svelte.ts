// The questions agents are waiting on, across every project: the count on
// the Questions place in the rail or strip, the "(N)" in every dashboard
// title, and the amber dot on a project's rail row (DECISIONS I-841). One
// store for the whole dashboard, so a page that answers a question or
// reads the pending list updates all three at once.
import { SvelteMap } from 'svelte/reactivity';
import { listQuestions } from './api/client';
import type { Question } from './api/types';

class PendingQuestions {
	/** The pending list, newest first, as GET /questions?state=pending answers it. */
	items = $state<Question[]>([]);
	count = $state(0);
	/** Pending questions per project id. */
	byProject = new SvelteMap<string, number>();
	#at = 0;

	/**
	 * Sets the store from a pending list a page has just read. A bare
	 * number sets the count alone (older callers); pass the list so the
	 * rail's dots follow too.
	 */
	set(list: Question[] | number): void {
		this.#at = Date.now();
		if (typeof list === 'number') {
			this.count = list;
			return;
		}
		const pending = list.filter((q) => q.state === 'pending');
		this.items = pending;
		this.count = pending.length;
		const per: Record<string, number> = {};
		for (const q of pending) per[q.project_id] = (per[q.project_id] ?? 0) + 1;
		for (const id of [...this.byProject.keys()]) if (!(id in per)) this.byProject.delete(id);
		for (const [id, n] of Object.entries(per)) {
			if (this.byProject.get(id) !== n) this.byProject.set(id, n);
		}
	}

	/** Whether an agent on this project waits on the user. */
	waiting(projectId: string): boolean {
		return (this.byProject.get(projectId) ?? 0) > 0;
	}

	/**
	 * Reads the pending list again, unless it was read less than `maxAge`
	 * ms ago. A failed read keeps the last list and says nothing; the
	 * page's own load reports an outage.
	 */
	async refresh(maxAge = 0): Promise<void> {
		if (Date.now() - this.#at < maxAge) return;
		this.#at = Date.now();
		try {
			// Passive: a background count must not clear an outage the page
			// it sits over has just reported.
			this.set(await listQuestions('pending', { passive: true }));
		} catch {
			// Keep the last list.
		}
	}
}

export const pendingQuestions = new PendingQuestions();
