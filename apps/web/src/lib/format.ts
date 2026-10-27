/** "$1.23" from a cents integer, per api.md's *_cents fields. */
export function money(cents: number): string {
	return `$${(cents / 100).toFixed(2)}`;
}

/** "2h14m" from a start timestamp to now. Under a minute this is "0m";
 * a line a reader sees goes through `upLabel` or `sinceLabel`, which say
 * "just now" there, as `ageShort` does. */
export function uptime(startedAt?: string, now = Date.now()): string {
	if (!startedAt) return '—';
	const ms = now - new Date(startedAt).getTime();
	if (ms < 0) return '0m';
	const totalMinutes = Math.floor(ms / 60_000);
	const hours = Math.floor(totalMinutes / 60);
	const minutes = totalMinutes % 60;
	if (hours === 0) return `${minutes}m`;
	return `${hours}h${minutes}m`;
}

/** True under a minute since `iso`, or when `iso` is ahead of `now`. */
function underAMinute(iso: string, now: number): boolean {
	return now - new Date(iso).getTime() < 60_000;
}

/** A running machine's age: "up 2h14m", or "started just now" in its first
 * minute (review r4), so it never reads "up 0m". */
export function upLabel(startedAt: string | undefined, now = Date.now()): string {
	if (startedAt && underAMinute(startedAt, now)) return 'started just now';
	return `up ${uptime(startedAt, now)}`;
}

/** How long a state has held after its word: "for 1h35m" ("frozen for
 * 1h35m"), or "just now" in its first minute. `word` is the preposition. */
export function sinceLabel(since: string, word = 'for', now = Date.now()): string {
	if (underAMinute(since, now)) return 'just now';
	return `${word} ${uptime(since, now)}`;
}

const GB = 1024 * 1024 * 1024;

/** "40 GB" from a byte count, the unit every size class and volume is
 * quoted in. For a figure that can be smaller than a gigabyte — a
 * snapshot, memory in use, a process's RSS — use `bytes`. */
export function gb(n: number): string {
	return `${(n / GB).toFixed(n % GB === 0 ? 0 : 1)} GB`;
}

/**
 * A byte count that may be small: "812 MB", "3.4 GB", "8 GB", "7 KB".
 * The dashboard had four of these — two copies of the same `memGb`, a
 * `mb` in Recently destroyed and `gb` itself — so one snapshot read
 * "812.3 MB" in one place and "0.8 GB" in another.
 */
export function bytes(n: number): string {
	if (n < 1024 * 1024) return `${Math.max(1, Math.round(n / 1024))} KB`;
	if (n < GB) return `${Math.round(n / (1024 * 1024))} MB`;
	return `${(n / GB).toFixed(1).replace(/\.0$/, '')} GB`;
}

/** "2026-09-20 14:02" in the browser's local time, for timestamps that are
 * not "just now" material (created_at, snapshot times). */
export function dateTime(iso: string): string {
	const d = new Date(iso);
	const pad = (n: number) => String(n).padStart(2, '0');
	return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

/**
 * "3m ago" for events, logs and rows. The same words as `ageShort`, and
 * for its reason: this had a seconds bucket, but every caller is redrawn
 * by a 10-second poll or a 30-second clock, so "3s ago" stayed on screen
 * for up to a minute. One screen said ages in two dialects, and the one
 * that counted seconds was the one that could not keep them.
 */
export function relativeTime(iso: string): string {
	return ageShort(iso);
}

/**
 * "3m ago", "2h ago", "1d ago"; "just now" under a minute. The age a row
 * shows on a 30-second clock (lib/clock.svelte.ts), so it never counts
 * seconds it will not update. A time ahead of `now` is "just now".
 */
export function ageShort(iso: string, now = Date.now()): string {
	const minutes = Math.floor((now - new Date(iso).getTime()) / 60_000);
	if (minutes < 1) return 'just now';
	if (minutes < 60) return `${minutes}m ago`;
	const hours = Math.floor(minutes / 60);
	if (hours < 24) return `${hours}h ago`;
	return `${Math.floor(hours / 24)}d ago`;
}

/** Whole minutes until a time, rounded up so the last minute is 1; 0 once passed. */
export function minutesLeft(iso: string, now = Date.now()): number {
	const ms = new Date(iso).getTime() - now;
	return ms <= 0 ? 0 : Math.ceil(ms / 60_000);
}

/**
 * Normalizes a git remote URL for display, the same way interfaces/cli-
 * config.md does for its lookup cache: strip the scheme and `git@`, turn
 * the `:` after the host into `/`, drop a trailing `.git`, lowercase the
 * host. `git@github.com:a/b.git` and `https://github.com/a/b` both become
 * `github.com/a/b`, so the connect card shows one form regardless of how
 * the project's remote was recorded.
 */
export function normalizeRemoteDisplay(remoteUrl: string): string {
	let s = remoteUrl.trim();
	s = s.replace(/^[a-z]+:\/\//i, '');
	s = s.replace(/^git@/i, '');
	const slash = s.indexOf('/');
	const colon = s.indexOf(':');
	if (colon !== -1 && (slash === -1 || colon < slash)) {
		s = s.slice(0, colon) + '/' + s.slice(colon + 1);
	}
	s = s.replace(/\.git$/i, '');
	const firstSlash = s.indexOf('/');
	if (firstSlash === -1) return s.toLowerCase();
	return s.slice(0, firstSlash).toLowerCase() + s.slice(firstSlash);
}

/** "$29" for a whole-dollar price, "$2.50" otherwise: a plan's price a month. */
export function price(cents: number): string {
	return cents % 100 === 0 ? `$${cents / 100}` : money(cents);
}

/** "40 GB" or "1.5 GB" from a GB figure the api already computed. */
export function gbs(n: number): string {
	return `${Number.isInteger(n) ? n : n.toFixed(1)} GB`;
}

/** "2026-10-04" in the browser's local time, for a charge or renewal date. */
export function dateOnly(iso: string): string {
	const d = new Date(iso);
	const pad = (n: number) => String(n).padStart(2, '0');
	return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

/**
 * "3 days" or "18 hours" until a time, for a waitlist hold or a trial's
 * end; "less than an hour" under one, "" once passed.
 */
export function timeUntil(iso: string, now = new Date()): string {
	const ms = new Date(iso).getTime() - now.getTime();
	if (ms <= 0) return '';
	const hours = Math.floor(ms / 3_600_000);
	if (hours < 1) return 'less than an hour';
	if (hours < 48) return hours === 1 ? '1 hour' : `${hours} hours`;
	const days = Math.floor(hours / 24);
	return `${days} days`;
}

/** The share of a limit, clamped to [0, 1] for a usage bar; 0 when there is no limit. */
export function share(used: number, limit: number): number {
	if (limit <= 0) return 0;
	return Math.min(1, Math.max(0, used / limit));
}

/** A temporary machine's time left, as the CLI says it (I-347): "destroyed
 * in 5h", minutes under an hour, rounded up; "time is up" once passed (the
 * destroy waits while someone is attached). */
export function tempLeft(expiresAt: string, now = new Date()): string {
	const ms = new Date(expiresAt).getTime() - now.getTime();
	if (ms <= 0) return 'time is up';
	if (ms < 3_600_000) return `destroyed in ${Math.max(1, Math.ceil(ms / 60_000))}m`;
	return `destroyed in ${Math.ceil(ms / 3_600_000)}h`;
}

/** "1 TB" for a whole number of thousands of GB, "N GB" otherwise: a plan's egress or disk. */
export function sizeGB(gb: number): string {
	if (gb >= 1000 && gb % 1000 === 0) return `${gb / 1000} TB`;
	return `${gb} GB`;
}

/** An agent's state as words: "needs_input" is "needs input"; unknown or empty is "". */
export function agentStateLabel(state?: string | null): string {
	if (!state || state === 'unknown') return '';
	return state.replace(/_/g, ' ');
}

const pad2 = (n: number) => String(n).padStart(2, '0');

/** "14:02", 24-hour, in the browser's local time: an event's time within its day. */
export function clockTime(iso: string): string {
	const d = new Date(iso);
	return `${pad2(d.getHours())}:${pad2(d.getMinutes())}`;
}

const DAYS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];
const MONTHS_SHORT = [
	'Jan',
	'Feb',
	'Mar',
	'Apr',
	'May',
	'Jun',
	'Jul',
	'Aug',
	'Sep',
	'Oct',
	'Nov',
	'Dec'
];

/** "Today", "Yesterday" or "Thu 9 Oct" in local time: the heading over a day's events. */
export function dayLabel(iso: string, now = new Date()): string {
	const d = new Date(iso);
	const day = (x: Date) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
	const diff = Math.round((day(now) - day(d)) / 86_400_000);
	if (diff === 0) return 'Today';
	if (diff === 1) return 'Yesterday';
	return `${DAYS[d.getDay()]} ${d.getDate()} ${MONTHS_SHORT[d.getMonth()]}`;
}

/** A size class's memory in GB (PRICING.md: small 4, large 8, xl 16); 0 for an unknown class. */
export function classMemoryGB(size: string): number {
	return ({ small: 4, large: 8, xl: 16 } as Record<string, number>)[size] ?? 0;
}

/**
 * A size class's vCPUs (DESIGN.md §5: small 1, large 2, xl 4 — 4 GB of
 * the class's memory each, DECISIONS I-784); 0 for an unknown class.
 * `scheduler.ClassVCPU` is where the figure changes: plans.test.ts, the
 * test that reads Go source, parses it out of scheduler.go and out of
 * the published table in content/docs/machine.md and fails when any of
 * the three differ.
 */
export function classVCPUs(size: string): number {
	return ({ small: 1, large: 2, xl: 4 } as Record<string, number>)[size] ?? 0;
}
