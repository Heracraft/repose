// The account's billing, read once for the whole dashboard (DECISIONS
// I-841): the rail's footer, the phone's plan line, the /projects banners
// and a project's Start refusal read it here instead of each asking GET
// /billing themselves. The root layout reads it in its first batch, then
// every 60s while the tab is in view, passively (I-766). A page that reads
// or changes billing with its own active call hands the answer to set().
import { browser } from '$app/environment';
import { ApiError } from './api/errors';
import { getBilling, getMe } from './api/client';
import type { Billing, Me } from './api/types';
import { TROUBLE, billingMode, type BillingMode } from './billingMode';

/** The last mode seen, kept per browser so a reload reserves the phone's plan line (CLS, I-664). */
const LAST_MODE = 'repose-billing-mode';

function readLast(): BillingMode | undefined {
	if (!browser) return undefined;
	try {
		return (localStorage.getItem(LAST_MODE) as BillingMode | null) ?? undefined;
	} catch {
		return undefined;
	}
}

class BillingStore {
	/** GET /billing's answer; undefined before it, while off, or after a failed first read. */
	info = $state<Billing | undefined>(undefined);
	/** GET /me's answer: its billing.status says exempt, past_due and suspended. */
	me = $state<Me | undefined>(undefined);
	/** billing_disabled, or a read that failed with nothing to show. */
	off = $state(false);
	mode = $derived<BillingMode>(billingMode({ me: this.me, billing: this.info, off: this.off }));
	/** The mode last seen, this visit or the one before: what the plan line reserves room for. */
	lastMode = $state<BillingMode | undefined>(readLast());
	/** Whether the reserved room is for a trouble line. */
	reserve = $derived(
		TROUBLE.has(this.mode === 'unknown' ? (this.lastMode ?? 'unknown') : this.mode)
	);
	#at = 0;
	#inflight: Promise<void> | undefined;

	/** Sets GET /billing's answer from a page's own active read. */
	set(info: Billing): void {
		this.info = info;
		this.off = false;
		this.#at = Date.now();
		this.#remember();
	}

	/** Sets GET /me's answer from a page's own read. */
	setMe(me: Me): void {
		this.me = me;
		this.#remember();
	}

	/** Marks billing switched off (a page's own read answered billing_disabled). */
	setOff(): void {
		this.off = true;
		this.info = undefined;
		this.#remember();
	}

	#remember() {
		const m = this.mode;
		if (m === 'unknown') return;
		this.lastMode = m;
		try {
			localStorage.setItem(LAST_MODE, m);
		} catch {
			// Private window or blocked storage: the line is not reserved.
		}
	}

	/**
	 * Reads /me and /billing again, unless read less than `maxAge` ms ago.
	 * Passive by default: its success never clears an outage the page
	 * reported. A failure keeps what is on screen.
	 */
	refresh({
		passive = true,
		maxAge = 0
	}: { passive?: boolean; maxAge?: number } = {}): Promise<void> {
		if (this.#inflight) return this.#inflight;
		if (Date.now() - this.#at < maxAge) return Promise.resolve();
		this.#at = Date.now();
		this.#inflight = (async () => {
			const [me, bill] = await Promise.allSettled([getMe({ passive }), getBilling({ passive })]);
			if (me.status === 'fulfilled') this.me = me.value;
			if (bill.status === 'fulfilled') {
				this.info = bill.value;
				this.off = false;
			} else if (bill.reason instanceof ApiError && bill.reason.code === 'billing_disabled') {
				this.off = true;
				this.info = undefined;
			} else if (!this.info) {
				this.off = true;
			}
			this.#remember();
		})().finally(() => (this.#inflight = undefined));
		return this.#inflight;
	}
}

export const billingStore = new BillingStore();
