// What a plan has room for when a machine is made (DECISIONS I-1612): the
// api refuses a start past the plan's memory (internal/billing/gate.go,
// `payment_required`, `plan_limit`), counting every machine in
// MEMORY_STATES at its class's memory. New project reads the same figure
// so its default is one the api takes, and says so before Enter when
// nothing fits.
import type { BillingMode } from './billingMode';
import { classMemoryGB, unsized } from './format';
import { holdsMemory } from './plans';
import type { Billing, Project } from './api/types';

/** The classes, smallest first (docs/interfaces/README.md). */
export const CLASSES = ['tiny', 'small', 'large', 'xl'] as const;

/** The api's default class, which `repose run` gets. */
export const DEFAULT_CLASS = 'large';

/** The modes with a live subscription, the only ones the gate meters by memory. */
const METERED: ReadonlySet<BillingMode> = new Set(['trial', 'active', 'cancelled', 'scheduled']);

export interface PlanRoom {
	/** The plan's memory, in GB. */
	limitGB: number;
	/** What the account's machines hold now. */
	usedGB: number;
	/** The machines holding it, by name, for the way out. */
	holders: Pick<Project, 'id' | 'name' | 'slug' | 'class'>[];
}

/**
 * The plan's memory and what holds it, or undefined where the gate does
 * not meter memory: billing off, exempt, no plan yet, a plan that sells
 * no memory on repose's servers (Own hardware, I-1364), or a read that
 * has not answered.
 */
export function planRoom(
	mode: BillingMode,
	billing: Pick<Billing, 'usage'> | undefined,
	projects: Pick<Project, 'id' | 'name' | 'slug' | 'class' | 'state' | 'sized'>[] | undefined
): PlanRoom | undefined {
	const limitGB = billing?.usage.memory_gb ?? 0;
	if (!METERED.has(mode) || limitGB <= 0 || !projects) return undefined;
	const holders = projects.filter((p) => holdsMemory(p) && !unsized(p));
	return {
		limitGB,
		usedGB: holders.reduce((n, p) => n + classMemoryGB(p.class), 0),
		holders
	};
}

/** GB free on the plan; Infinity where nothing is metered. */
export const freeGB = (room: PlanRoom | undefined) =>
	room ? Math.max(0, room.limitGB - room.usedGB) : Number.POSITIVE_INFINITY;

/**
 * The size New project starts on: the api's default when it fits, else
 * the largest class that does, else the api's default (nothing fits, and
 * the form says so). Never larger than the default: a plan with room for
 * an xl still gets what `repose run` makes.
 */
export function defaultClass(room: PlanRoom | undefined): string {
	const free = freeGB(room);
	const upTo = CLASSES.indexOf(DEFAULT_CLASS);
	for (let i = upTo; i >= 0; i--) {
		if (classMemoryGB(CLASSES[i]) <= free) return CLASSES[i];
	}
	return DEFAULT_CLASS;
}

/** Whether a class fits in what the plan has free now. */
export const fits = (room: PlanRoom | undefined, cls: string) => classMemoryGB(cls) <= freeGB(room);
