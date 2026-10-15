/** "$1.23" from a cents integer, per api.md's *_cents fields. */
export function money(cents: number): string {
	return `$${(cents / 100).toFixed(2)}`;
}

/** "2h14m" from a start timestamp to now. */
export function uptime(startedAt?: string): string {
	if (!startedAt) return '—';
	const ms = Date.now() - new Date(startedAt).getTime();
	if (ms < 0) return '0m';
	const totalMinutes = Math.floor(ms / 60_000);
	const hours = Math.floor(totalMinutes / 60);
	const minutes = totalMinutes % 60;
	if (hours === 0) return `${minutes}m`;
	return `${hours}h${minutes}m`;
}

const GB = 1024 * 1024 * 1024;

/** "40 GB" from a byte count, the unit every size class is quoted in. */
export function gb(bytes: number): string {
	return `${(bytes / GB).toFixed(bytes % GB === 0 ? 0 : 1)} GB`;
}

/** "2026-09-20 14:02" in the browser's local time, for timestamps that are
 * not "just now" material (created_at, snapshot times). */
export function dateTime(iso: string): string {
	const d = new Date(iso);
	const pad = (n: number) => String(n).padStart(2, '0');
	return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

/** "3 minutes ago" for events and logs. */
export function relativeTime(iso: string): string {
	const ms = Date.now() - new Date(iso).getTime();
	const seconds = Math.floor(ms / 1000);
	if (seconds < 5) return 'just now';
	if (seconds < 60) return `${seconds}s ago`;
	const minutes = Math.floor(seconds / 60);
	if (minutes < 60) return `${minutes}m ago`;
	const hours = Math.floor(minutes / 60);
	if (hours < 24) return `${hours}h ago`;
	const days = Math.floor(hours / 24);
	return `${days}d ago`;
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
