// The three plans as the landing and /billing draw them (DECISIONS I-663):
// one shape, so the prerendered landing and the signed-in plan picker
// cannot disagree. The figures are internal/billing/plans.go's and
// docs/PRICING.md's; plans.test.ts parses plans.go and fails when one
// differs. /billing maps the api's plans onto the same shape (fromApi),
// so a figure the api changes shows there without a release of the web.
import type { Plan as ApiPlan, PlanId } from '$lib/api/types';
import { sizeGB } from '$lib/format';

export type { PlanId };

export interface PlanFigures {
	id: PlanId;
	name: string;
	/** A month, after any introductory price. */
	priceCents: number;
	trialDays: number;
	/** Seats the plan holds on the fleet: 8 GB each (I-290). */
	seats: number;
	/** Memory that may run at once. */
	memoryGB: number;
	/** Disk the projects may hold. */
	diskGB: number;
	/** Egress a month, after any introductory offer. */
	egressGB: number;
	/** The introductory offer, for an account's first subscription (I-497). */
	intro?: { priceCents: number; months: number; egressGB: number };
}

/** internal/billing/plans.go's Solo, Plus and Pro, in that order. */
export const PLANS: PlanFigures[] = [
	{
		id: 'solo',
		name: 'Solo',
		priceCents: 2900,
		trialDays: 7,
		seats: 1,
		memoryGB: 8,
		diskGB: 100,
		egressGB: 250,
		intro: { priceCents: 2000, months: 3, egressGB: 100 }
	},
	{
		id: 'plus',
		name: 'Plus',
		priceCents: 5900,
		trialDays: 7,
		seats: 2,
		memoryGB: 16,
		diskGB: 250,
		egressGB: 500
	},
	{
		id: 'pro',
		name: 'Pro',
		priceCents: 9900,
		trialDays: 7,
		seats: 4,
		memoryGB: 32,
		diskGB: 500,
		egressGB: 1000
	}
];

/** One of GET /billing's plans in the shape the cells draw. */
export function fromApi(p: ApiPlan): PlanFigures {
	const intro =
		p.intro_price_cents && p.intro_months
			? {
					priceCents: p.intro_price_cents,
					months: p.intro_months,
					egressGB: p.intro_egress_gb || p.egress_gb
				}
			: undefined;
	return {
		id: p.id,
		name: p.name,
		priceCents: p.price_cents,
		trialDays: p.trial_days,
		seats: p.seats,
		memoryGB: p.memory_gb,
		diskGB: p.disk_gb,
		egressGB: p.egress_gb,
		intro
	};
}

/** "8 GB running at once": what a plan sells, in the owner's words (b7f7a97c). */
export function memoryLine(gb: number): string {
	return `${gb} GB running at once`;
}

/** A plan's disk or egress: "100 GB", "1 TB". */
export const planSize = sizeGB;

/** The ordinal of a place on the waitlist: "1st", "2nd", "13th", "22nd". */
export function ordinal(n: number): string {
	const mod100 = n % 100;
	if (mod100 >= 11 && mod100 <= 13) return `${n}th`;
	return `${n}${{ 1: 'st', 2: 'nd', 3: 'rd' }[n % 10] ?? 'th'}`;
}
