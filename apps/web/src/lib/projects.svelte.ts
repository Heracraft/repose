// The account's projects as the rail lists them (DECISIONS I-841). /projects
// polls the list every 10s and hands each answer here with set(); a
// project's layout patches its own row with patch(). Elsewhere the rail
// reads it every 60s while the tab is in view and on each navigation that
// finds it older than 30s. A hidden tab reads nothing.
import { listProjects } from './api/client';
import type { Project } from './api/types';

class Projects {
	/** Undefined until the first answer; the rail draws no group before it. */
	list = $state<Project[] | undefined>(undefined);
	#at = 0;

	/** Sets the list from GET /projects a page has just read. */
	set(list: Project[]): void {
		this.list = list;
		this.#at = Date.now();
	}

	/** Replaces one project's row with a fresher copy (a project page's poll). */
	patch(p: Project): void {
		if (!this.list) return;
		const i = this.list.findIndex((q) => q.id === p.id);
		if (i < 0) return;
		const row = this.list[i];
		if (row.state === p.state && row.name === p.name && row.resting === p.resting) return;
		this.list[i] = p;
	}

	/** Reads the list again unless it was read less than `maxAge` ms ago. */
	async refresh(maxAge = 0): Promise<void> {
		if (Date.now() - this.#at < maxAge) return;
		this.#at = Date.now();
		try {
			// Passive (I-766): a background read must not raise or clear the
			// outage bar the page under it owns.
			this.list = await listProjects({ passive: true });
		} catch {
			// Keep the last list.
		}
	}
}

export const projectsStore = new Projects();
