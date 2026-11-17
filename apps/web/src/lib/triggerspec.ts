// A trigger's schedule, as the browser builds one and reads one back
// (DECISIONS I-1266). `internal/triggerspec` is the parser the CLI and
// the api share; this is the six shapes as a form, so the dashboard can
// make a schedule without the user learning a grammar, and can open an
// existing one in the fields that made it.
//
// It writes the canonical text `internal/triggerspec` stores, so a spec
// built here round-trips: the api parses it, stores it, and this file
// reads it back into the same fields. `apps/web/src/lib/triggerspec.test.ts`
// holds the two together.

/** The six shapes of `features/triggers.md` §6. */
export type SpecShape = 'every' | 'daily' | 'weekdays' | 'weekends' | 'days' | 'monthly';

/** Monday first, as a week is written and as the parser sorts them. */
export const WEEKDAYS = ['mon', 'tue', 'wed', 'thu', 'fri', 'sat', 'sun'] as const;

export const SHAPE_LABELS: Record<SpecShape, string> = {
	every: 'Every so often',
	daily: 'Every day',
	weekdays: 'Monday to Friday',
	weekends: 'Saturday and Sunday',
	days: 'On days I pick',
	monthly: 'Once a month'
};

/** The fields the six shapes need between them. */
export interface SpecFields {
	shape: SpecShape;
	/** `every`: the interval in whole minutes, 15 to 1440. */
	minutes: number;
	/** Every other shape: the time of day, `HH:MM`. */
	at: string;
	/** `days`: the days picked, in any order. */
	days: string[];
	/** `monthly`: the day of the month, 1 to 28. */
	day: number;
}

export const EMPTY_FIELDS: SpecFields = {
	shape: 'daily',
	minutes: 60,
	at: '09:00',
	days: ['mon'],
	day: 1
};

/** `45m`, `1h` or `1h30m`, which is how a person writes an interval. */
export function everyText(minutes: number): string {
	const h = Math.floor(minutes / 60);
	const m = minutes % 60;
	if (h === 0) return `${m}m`;
	if (m === 0) return `${h}h`;
	return `${h}h${m}m`;
}

/** The spec the fields make, or a reason it is not one yet. */
export function buildSpec(f: SpecFields): { spec: string } | { error: string } {
	if (f.shape === 'every') {
		if (!Number.isInteger(f.minutes) || f.minutes < 15 || f.minutes > 1440) {
			return { error: 'An interval is 15 minutes to 24 hours, in whole minutes.' };
		}
		return { spec: `every ${everyText(f.minutes)}` };
	}
	if (!/^([01][0-9]|2[0-3]):[0-5][0-9]$/.test(f.at)) {
		return { error: 'A time of day is written 07:00.' };
	}
	switch (f.shape) {
		case 'monthly':
			if (!Number.isInteger(f.day) || f.day < 1 || f.day > 28) {
				return { error: 'A day of the month is 1 to 28, so that every month has it.' };
			}
			return { spec: `monthly ${f.day} ${f.at}` };
		case 'days': {
			const picked = WEEKDAYS.filter((d) => f.days.includes(d));
			if (picked.length === 0) return { error: 'Pick at least one day.' };
			return { spec: `${picked.join(',')} ${f.at}` };
		}
		default:
			return { spec: `${f.shape} ${f.at}` };
	}
}

/**
 * The fields a stored spec came from, so Edit opens on what is there.
 * An unreadable spec (a newer api's shape) answers undefined, and the
 * form then shows the text it cannot open.
 */
export function parseSpec(text: string): SpecFields | undefined {
	const fields = text.trim().toLowerCase().split(/\s+/);
	const base = { ...EMPTY_FIELDS };
	if (fields[0] === 'every' && fields.length === 2) {
		const m = /^(?:([0-9]+)h)?(?:([0-9]+)m)?$/.exec(fields[1]);
		if (!m || (!m[1] && !m[2])) return undefined;
		return { ...base, shape: 'every', minutes: Number(m[1] ?? 0) * 60 + Number(m[2] ?? 0) };
	}
	if (fields[0] === 'monthly' && fields.length === 3) {
		const day = Number(fields[1]);
		if (!Number.isInteger(day) || day < 1 || day > 28) return undefined;
		return { ...base, shape: 'monthly', day, at: fields[2] };
	}
	if (fields.length !== 2) return undefined;
	if (fields[0] === 'daily' || fields[0] === 'weekdays' || fields[0] === 'weekends') {
		return { ...base, shape: fields[0], at: fields[1] };
	}
	const days = fields[0].split(',');
	if (!days.every((d) => (WEEKDAYS as readonly string[]).includes(d))) return undefined;
	return { ...base, shape: 'days', days, at: fields[1] };
}
