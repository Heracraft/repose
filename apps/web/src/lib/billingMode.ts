// The account's billing as one word, and the lines the rail's footer and
// the phone's plan line print for it (DECISIONS I-841, ia-final 1.2 and
// 1.3). Pure, so each mode's text is tested without a browser.
import type { Billing, Me } from './api/types';
import { dateOnly, sizeGB } from './format';

export type BillingMode =
	/** Neither answer is in yet. */
	| 'unknown'
	/** Billing is switched off, or GET /billing failed: nothing is drawn. */
	| 'off'
	| 'exempt'
	| 'none'
	/** On the waitlist, no seat held yet. */
	| 'waitlist'
	/** No plan, not on the waitlist, and no plan has a free seat. */
	| 'full'
	/** An invitation holds a seat. */
	| 'held'
	| 'trial'
	| 'active'
	| 'cancelled'
	| 'scheduled'
	| 'past_due'
	| 'suspended';

/** The modes the phone's plan line shows in, and the rail's footer marks. */
export const TROUBLE: ReadonlySet<BillingMode> = new Set([
	'past_due',
	'suspended',
	'none',
	'waitlist',
	'full',
	'held'
]);

export interface BillingFacts {
	me?: Me;
	billing?: Billing;
	/** GET /billing answered billing_disabled, or failed. */
	off: boolean;
}

export function billingMode({ me, billing, off }: BillingFacts, now = Date.now()): BillingMode {
	if (off) return 'off';
	if (!billing || !me) return 'unknown';
	const status = me.billing.status;
	if (status === 'exempt') return 'exempt';
	if (status === 'suspended') return 'suspended';
	const sub = billing.subscription;
	if (status === 'past_due' || sub?.status === 'past_due') return 'past_due';
	if (sub) {
		if (sub.cancel_at) return 'cancelled';
		if (sub.scheduled_plan) return 'scheduled';
		if (sub.status === 'trialing') return 'trial';
		return 'active';
	}
	const w = billing.waitlist;
	if (w?.hold_until && new Date(w.hold_until).getTime() > now) return 'held';
	if (w) return 'waitlist';
	if (billing.plans.length > 0 && !billing.plans.some((p) => p.available)) return 'full';
	return 'none';
}

/** The plan's name, "Pro", from the api's plans. */
function planName(billing: Billing, id: string | null | undefined): string {
	if (!id) return '';
	return billing.plans.find((p) => p.id === id)?.name ?? id.charAt(0).toUpperCase() + id.slice(1);
}

export interface FooterLines {
	/** "Plan", or "Running" for an exempt account. */
	label: string;
	/** The serif line: "Pro", "No plan"; absent for an exempt account. */
	name?: string;
	/** The state line: text, or a link to /billing. */
	state: string;
	link: boolean;
	/** A red dot before the state line (payment failed, suspended). */
	err: boolean;
}

/**
 * The rail footer's three lines (ia-final 1.2). `runningGB` is the memory
 * the projects hold now; the store sums it from the projects list when it
 * has one, so a start shows before the next billing read.
 */
export function footerLines(
	mode: BillingMode,
	{ billing }: BillingFacts,
	runningGB: number
): FooterLines | undefined {
	if (mode === 'unknown' || mode === 'off' || !billing) return undefined;
	const sub = billing.subscription;
	const name = planName(billing, sub?.plan);
	const plan = (state: string, link = false, err = false): FooterLines => ({
		label: 'Plan',
		name,
		state,
		link,
		err
	});
	const noPlan = (state: string): FooterLines => ({
		label: 'Plan',
		name: 'No plan',
		state,
		link: true,
		err: false
	});
	switch (mode) {
		case 'exempt':
			return { label: 'Running', state: sizeGB(runningGB), link: false, err: false };
		case 'none':
			return noPlan('Choose a plan');
		case 'waitlist':
			return noPlan('On the waitlist');
		case 'full':
			return noPlan('repose is full');
		case 'held':
			return noPlan('Seat held');
		case 'trial':
			return plan(sub?.trial_end ? `Trial until ${dateOnly(sub.trial_end)}` : 'Trial');
		case 'cancelled':
			return plan(`Ends ${dateOnly(sub!.cancel_at!)}`);
		case 'scheduled':
			return plan(
				`Changes to ${planName(billing, sub!.scheduled_plan)} ${dateOnly(sub!.next_billed_at ?? sub!.period_end)}`
			);
		case 'past_due':
			return plan('Payment failed', true, true);
		case 'suspended':
			return plan('Suspended', true, true);
		case 'active': {
			const limit = billing.usage.memory_gb;
			if (!limit) return plan(`${sizeGB(runningGB)} running`);
			const over = runningGB > limit ? ' · over' : '';
			return plan(`${sizeGB(runningGB)} of ${sizeGB(limit)} running${over}`);
		}
	}
}

export interface PlanLine {
	/** The sentence before the link. */
	text: string;
	link: string;
	err: boolean;
}

/** The phone's one plan line under the strip, only in trouble (ia-final 1.3). */
export function planLine(mode: BillingMode, { billing }: BillingFacts): PlanLine | undefined {
	if (!TROUBLE.has(mode) || !billing) return undefined;
	switch (mode) {
		case 'past_due': {
			const name = planName(billing, billing.subscription?.plan);
			return {
				text: name ? `${name} · Payment failed` : 'Payment failed',
				link: 'Update card',
				err: true
			};
		}
		case 'suspended':
			return { text: 'Suspended', link: 'Update card and pay', err: true };
		case 'none':
			return { text: 'No plan', link: 'Choose a plan', err: false };
		case 'waitlist':
			return { text: 'On the waitlist', link: 'Billing', err: false };
		case 'full':
			return { text: 'repose is full', link: 'Billing', err: false };
		case 'held':
			return { text: 'Seat held', link: 'Billing', err: false };
	}
	return undefined;
}
