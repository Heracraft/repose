import { describe, it, expect } from 'vitest';
import {
	ageShort,
	agentStateLabel,
	bytes,
	classMemoryGB,
	clockTime,
	dateOnly,
	dayLabel,
	gb,
	gbs,
	minutesLeft,
	money,
	normalizeRemoteDisplay,
	price,
	relativeTime,
	share,
	sizeGB,
	tempLeft,
	timeUntil,
	upLabel,
	sinceLabel,
	uptime
} from './format';

describe('money', () => {
	it('formats cents as dollars', () => {
		expect(money(9900)).toBe('$99.00');
		expect(money(0)).toBe('$0.00');
		expect(money(31)).toBe('$0.31');
	});
});

describe('gb', () => {
	it('formats whole GB without a decimal', () => {
		expect(gb(40 * 1024 * 1024 * 1024)).toBe('40 GB');
	});
	it('formats a fraction with one decimal', () => {
		expect(gb(1.5 * 1024 * 1024 * 1024)).toBe('1.5 GB');
	});
});

describe('price', () => {
	it('drops the cents of a whole-dollar plan price', () => {
		expect(price(2900)).toBe('$29');
		expect(price(5900)).toBe('$59');
	});
	it('keeps the cents of an overage', () => {
		expect(price(250)).toBe('$2.50');
	});
});

describe('gbs', () => {
	it("formats the api's GB figures", () => {
		expect(gbs(100)).toBe('100 GB');
		expect(gbs(37.25)).toBe('37.3 GB');
		expect(gbs(0)).toBe('0 GB');
	});
});

describe('dateOnly', () => {
	it('gives the local date of a charge', () => {
		const d = new Date(2026, 9, 4, 15, 30);
		expect(dateOnly(d.toISOString())).toBe('2026-10-04');
	});
});

describe('timeUntil', () => {
	const now = new Date(2026, 8, 27, 12, 0, 0);
	it('counts hours under two days and days after', () => {
		expect(timeUntil(new Date(2026, 8, 27, 13, 30).toISOString(), now)).toBe('1 hour');
		expect(timeUntil(new Date(2026, 8, 28, 12, 0).toISOString(), now)).toBe('24 hours');
		expect(timeUntil(new Date(2026, 8, 30, 12, 0).toISOString(), now)).toBe('3 days');
	});
	it('says less than an hour, then nothing once passed', () => {
		expect(timeUntil(new Date(2026, 8, 27, 12, 20).toISOString(), now)).toBe('less than an hour');
		expect(timeUntil(new Date(2026, 8, 27, 11, 0).toISOString(), now)).toBe('');
	});
});

describe('ageShort', () => {
	const now = new Date(2026, 9, 12, 12, 0, 0).getTime();
	const at = (min: number, sec = 0) => new Date(now - min * 60_000 - sec * 1000).toISOString();
	it('says just now under a minute, so a 30-second clock never shows seconds', () => {
		expect(ageShort(at(0, 0), now)).toBe('just now');
		expect(ageShort(at(0, 59), now)).toBe('just now');
	});
	it('counts minutes, hours and days', () => {
		expect(ageShort(at(3), now)).toBe('3m ago');
		expect(ageShort(at(59, 59), now)).toBe('59m ago');
		expect(ageShort(at(60), now)).toBe('1h ago');
		expect(ageShort(at(26 * 60), now)).toBe('1d ago');
	});
	it('reads a time in the future (a skewed clock) as just now', () => {
		expect(ageShort(at(-5), now)).toBe('just now');
	});
});

describe('minutesLeft', () => {
	const now = new Date(2026, 9, 12, 12, 0, 0).getTime();
	const inMs = (ms: number) => new Date(now + ms).toISOString();
	it('rounds up, so the last minute reads 1m and not 0m', () => {
		expect(minutesLeft(inMs(4 * 60_000), now)).toBe(4);
		expect(minutesLeft(inMs(3 * 60_000 + 1), now)).toBe(4);
		expect(minutesLeft(inMs(10_000), now)).toBe(1);
	});
	it('is 0 once passed', () => {
		expect(minutesLeft(inMs(0), now)).toBe(0);
		expect(minutesLeft(inMs(-60_000), now)).toBe(0);
	});
});

describe('tempLeft', () => {
	const now = new Date(2026, 8, 27, 12, 0, 0);
	it('rounds up to hours, minutes under an hour, as the CLI does', () => {
		expect(tempLeft(new Date(2026, 8, 27, 17, 0).toISOString(), now)).toBe('destroyed in 5h');
		expect(tempLeft(new Date(2026, 8, 27, 16, 1).toISOString(), now)).toBe('destroyed in 5h');
		expect(tempLeft(new Date(2026, 8, 27, 12, 30).toISOString(), now)).toBe('destroyed in 30m');
		expect(tempLeft(new Date(2026, 8, 27, 11, 0).toISOString(), now)).toBe('time is up');
	});
});

describe('share', () => {
	it('clamps a usage bar to the limit and treats no limit as empty', () => {
		expect(share(4, 8)).toBe(0.5);
		expect(share(300, 250)).toBe(1);
		expect(share(3, 0)).toBe(0);
		expect(share(-1, 8)).toBe(0);
	});
});

describe('normalizeRemoteDisplay', () => {
	it('normalizes an ssh-style remote', () => {
		expect(normalizeRemoteDisplay('git@github.com:heracraft/repose.git')).toBe(
			'github.com/heracraft/repose'
		);
	});

	it('normalizes an https remote to the same form', () => {
		expect(normalizeRemoteDisplay('https://GitHub.com/heracraft/repose')).toBe(
			'github.com/heracraft/repose'
		);
	});

	it('agrees on both spellings of the same remote', () => {
		const a = normalizeRemoteDisplay('git@github.com:a/b.git');
		const b = normalizeRemoteDisplay('https://github.com/a/b');
		expect(a).toBe(b);
	});
});

describe('sizeGB', () => {
	it('says TB for whole thousands', () => {
		expect(sizeGB(1000)).toBe('1 TB');
		expect(sizeGB(2000)).toBe('2 TB');
	});
	it('says GB otherwise', () => {
		expect(sizeGB(250)).toBe('250 GB');
		expect(sizeGB(1500)).toBe('1500 GB');
		expect(sizeGB(0)).toBe('0 GB');
	});
});

describe('agentStateLabel', () => {
	it('spaces the words', () => {
		expect(agentStateLabel('needs_input')).toBe('needs input');
		expect(agentStateLabel('working')).toBe('working');
	});
	it('gives nothing for an unknown or empty state', () => {
		expect(agentStateLabel('unknown')).toBe('');
		expect(agentStateLabel('')).toBe('');
		expect(agentStateLabel(undefined)).toBe('');
	});
});

describe('clockTime', () => {
	it('is 24-hour local time', () => {
		expect(clockTime(new Date(2026, 9, 9, 14, 2).toISOString())).toBe('14:02');
		expect(clockTime(new Date(2026, 9, 9, 7, 5).toISOString())).toBe('07:05');
	});
});

describe('dayLabel', () => {
	const now = new Date(2026, 9, 11, 9, 0);
	it('says Today and Yesterday', () => {
		expect(dayLabel(new Date(2026, 9, 11, 0, 1).toISOString(), now)).toBe('Today');
		expect(dayLabel(new Date(2026, 9, 10, 23, 59).toISOString(), now)).toBe('Yesterday');
	});
	it('gives the weekday, day and month before that', () => {
		expect(dayLabel(new Date(2026, 9, 9, 12, 0).toISOString(), now)).toBe('Fri 9 Oct');
		expect(dayLabel(new Date(2026, 8, 30, 12, 0).toISOString(), now)).toBe('Wed 30 Sep');
	});
});

describe('classMemoryGB', () => {
	it('gives each size class its memory', () => {
		expect(classMemoryGB('small')).toBe(4);
		expect(classMemoryGB('large')).toBe(8);
		expect(classMemoryGB('xl')).toBe(16);
		expect(classMemoryGB('huge')).toBe(0);
	});
});

// Four formatters printed byte counts before: two copies of the same
// `memGb`, a `mb` in Recently destroyed, and `gb` itself, so one snapshot
// read "812.3 MB" in one place and "0.8 GB" in another.
describe('bytes', () => {
	it('drops to MB under a gigabyte and KB under a megabyte', () => {
		expect(bytes(812 * 1024 * 1024)).toBe('812 MB');
		expect(bytes(7 * 1024)).toBe('7 KB');
		expect(bytes(10)).toBe('1 KB');
	});

	it('gives a gigabyte figure one decimal, and drops a trailing zero', () => {
		expect(bytes(3.4 * 1024 ** 3)).toBe('3.4 GB');
		expect(bytes(8 * 1024 ** 3)).toBe('8 GB');
	});

	it('is the same figure wherever it is printed', () => {
		const snapshot = 812 * 1024 * 1024;
		expect(bytes(snapshot)).toBe(bytes(snapshot));
		// gb() stays the size-class unit and is deliberately different.
		expect(gb(40 * 1024 ** 3)).toBe('40 GB');
	});
});

// relativeTime had a seconds bucket its callers could not keep current:
// every one is redrawn by a 10s poll or a 30s clock, so "3s ago" sat on
// screen for up to a minute while ageShort beside it said "just now".
describe('relativeTime', () => {
	it('says the same words as ageShort, with no seconds bucket', () => {
		const now = Date.now();
		for (const ms of [0, 3_000, 59_000, 90_000, 3 * 3600_000, 26 * 3600_000]) {
			const iso = new Date(now - ms).toISOString();
			expect(relativeTime(iso)).toBe(ageShort(iso, now));
		}
	});

	it('never counts seconds', () => {
		expect(relativeTime(new Date(Date.now() - 3_000).toISOString())).toBe('just now');
	});
});

describe('upLabel and sinceLabel', () => {
	const now = Date.parse('2026-10-21T12:00:00Z');
	const ago = (ms: number) => new Date(now - ms).toISOString();
	it('keeps uptime as "2h14m"', () => {
		expect(uptime(ago((2 * 60 + 14) * 60_000), now)).toBe('2h14m');
	});
	it('says "just now" under a minute, as ageShort does', () => {
		expect(upLabel(ago(30_000), now)).toBe('started just now');
		expect(sinceLabel(ago(30_000), 'for', now)).toBe('just now');
		expect(ageShort(ago(30_000), now)).toBe('just now');
	});
	it('says "just now" for a start ahead of the clock', () => {
		expect(upLabel(ago(-5_000), now)).toBe('started just now');
	});
	it('counts from the first minute', () => {
		expect(upLabel(ago(60_000), now)).toBe('up 1m');
		expect(upLabel(ago(14 * 3_600_000 + 8 * 60_000), now)).toBe('up 14h8m');
		expect(sinceLabel(ago(98 * 60_000), 'for', now)).toBe('for 1h38m');
	});
	it('keeps the dash when there is no start', () => {
		expect(upLabel(undefined, now)).toBe('up —');
	});
});

describe('uptime past two days', () => {
	// A machine left running for a week read "172h14m" (I-892).
	const base = Date.parse('2026-10-28T00:00:00Z');
	const ago = (h: number, m = 0) => new Date(base - (h * 60 + m) * 60_000).toISOString();
	it('keeps hours and minutes up to two days', () => {
		expect(uptime(ago(2, 14), base)).toBe('2h14m');
		expect(uptime(ago(47, 59), base)).toBe('47h59m');
	});
	it('rolls to whole days past two', () => {
		expect(uptime(ago(48), base)).toBe('2d');
		expect(uptime(ago(74, 30), base)).toBe('3d');
		expect(uptime(ago(172, 14), base)).toBe('7d');
	});
});
