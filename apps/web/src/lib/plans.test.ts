import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import { MEMORY_STATES, PLANS, fromApi, memoryLine, ordinal } from './plans';
import { classMemoryGB, classVCPUs } from './format';

// The landing is prerendered, so its plans are figures in the web's own
// source (lib/plans.ts). plans.go is where a price changes (PRICING.md
// "Changing prices"); this reads it and fails when the two differ, so the
// landing cannot go on selling an old price.
const goSource = readFileSync(
	join(__dirname, '..', '..', '..', '..', 'internal', 'billing', 'plans.go'),
	'utf8'
);

const usageSource = readFileSync(
	join(__dirname, '..', '..', '..', '..', 'internal', 'billing', 'usage.go'),
	'utf8'
);

const schedulerSource = readFileSync(
	join(__dirname, '..', '..', '..', '..', 'internal', 'api', 'scheduler', 'scheduler.go'),
	'utf8'
);

/** The fields of `Name = Plan{...}` in plans.go, as strings. */
function goPlan(name: string): Record<string, string> {
	const m = goSource.match(new RegExp(`\\b${name}\\s*=\\s*Plan\\{([^}]*)\\}`));
	if (!m) throw new Error(`plans.go: no ${name} = Plan{...}`);
	const fields: Record<string, string> = {};
	for (const [, key, value] of m[1].matchAll(/(\w+):\s*("[^"]*"|\d+)/g)) {
		fields[key] = value.replace(/"/g, '');
	}
	return fields;
}

describe('plans', () => {
	it('match internal/billing/plans.go figure for figure', () => {
		expect(PLANS.map((p) => p.name)).toEqual(['Solo', 'Plus', 'Pro']);
		for (const p of PLANS) {
			const go = goPlan(p.name);
			expect(p.id, p.name).toBe(go.ID);
			expect(p.priceCents, `${p.name} price`).toBe(Number(go.PriceCents));
			expect(p.trialDays, `${p.name} trial`).toBe(Number(go.TrialDays));
			expect(p.seats, `${p.name} seats`).toBe(Number(go.Seats));
			expect(p.memoryGB, `${p.name} memory`).toBe(Number(go.MemoryGB));
			expect(p.diskGB, `${p.name} disk`).toBe(Number(go.DiskGB));
			expect(p.egressGB, `${p.name} egress`).toBe(Number(go.EgressGB));
			if (go.IntroCents && Number(go.IntroCents) > 0) {
				expect(p.intro, `${p.name} intro`).toEqual({
					priceCents: Number(go.IntroCents),
					months: Number(go.IntroMonths),
					egressGB: Number(go.IntroEgressGB) || Number(go.EgressGB)
				});
			} else {
				expect(p.intro, `${p.name} intro`).toBeUndefined();
			}
		}
	});

	// /docs/billing draws PlanCells over its table, but /docs/billing.md, the
	// copy agents read through llms.txt, is the table itself (I-660), so it
	// has to say what the cells say.
	it('match the table in the Pricing doc', () => {
		const md = readFileSync(join(__dirname, '..', 'content', 'docs', 'billing.md'), 'utf8');
		const block = md.match(/<!-- plans[^>]*-->([\s\S]*?)<!-- \/plans -->/);
		expect(block, 'billing.md has a <!-- plans --> block').not.toBeNull();
		const gb = (n: number) => (n >= 1000 ? `${n / 1000} TB` : `${n} GB`);
		for (const p of PLANS) {
			const row = block![1].split('\n').find((l) => l.startsWith(`| ${p.name} `));
			expect(row, `${p.name} row`).toBeDefined();
			const [, , price, memory, disk, egress] = row!.split('|').map((c) => c.trim());
			const dollars = (c: number) => `$${c / 100}`;
			expect(price, `${p.name} price`).toBe(
				p.intro
					? `${dollars(p.intro.priceCents)} for ${p.intro.months} months, then ${dollars(p.priceCents)}`
					: dollars(p.priceCents)
			);
			expect(memory.startsWith(`${p.memoryGB} GB:`), `${p.name} memory`).toBe(true);
			expect(disk, `${p.name} disk`).toBe(gb(p.diskGB));
			expect(egress, `${p.name} egress`).toBe(
				p.intro && p.intro.egressGB !== p.egressGB
					? `${gb(p.intro.egressGB)}, then ${gb(p.egressGB)}`
					: gb(p.egressGB)
			);
		}
	});

	it('map the api plan onto the same figures', () => {
		const solo = fromApi({
			id: 'solo',
			name: 'Solo',
			price_cents: 4900,
			currency: 'USD',
			trial_days: 7,
			seats: 1,
			memory_gb: 8,
			disk_gb: 100,
			egress_gb: 250,
			available: true,
			intro_price_cents: 3900,
			intro_months: 12,
			intro_egress_gb: 0
		});
		expect(solo).toEqual(PLANS[0]);
		const pro = fromApi({
			id: 'pro',
			name: 'Pro',
			price_cents: 16900,
			currency: 'USD',
			trial_days: 7,
			seats: 4,
			memory_gb: 32,
			disk_gb: 500,
			egress_gb: 1000,
			available: true,
			intro_price_cents: 0,
			intro_months: 0,
			intro_egress_gb: 0
		});
		expect(pro).toEqual(PLANS[2]);
	});

	it('say the memory in the owner’s words and count places', () => {
		expect(memoryLine(8)).toBe('8 GB running at once');
		expect([1, 2, 3, 4, 11, 12, 13, 21, 22, 41, 103].map(ordinal)).toEqual([
			'1st',
			'2nd',
			'3rd',
			'4th',
			'11th',
			'12th',
			'13th',
			'21st',
			'22nd',
			'41st',
			'103rd'
		]);
	});
});

// The figure /projects prints over the plan's memory limit has to be the
// one the api refuses a start by, or the page offers room that is not
// there. /projects counted only `running` and disagreed with /billing
// for the same account at the same moment.
describe('the states that hold a plan memory', () => {
	it('are internal/billing/usage.go memoryStates, exactly', () => {
		const m = usageSource.match(/const\s+memoryStates\s*=\s*"([^"]*)"/);
		if (!m) throw new Error('usage.go: no `const memoryStates = "..."`');
		const go = m[1].split(',').map((s) => s.trim().replace(/'/g, ''));
		expect([...MEMORY_STATES].sort()).toEqual([...go].sort());
	});
});

// A size class's vCPUs live in four places a release can move apart: the
// api's scheduler.ClassVCPU (where the figure changes), the project
// page's own table through format.ts, the table published in
// /docs/machine, and hostd's boot arguments, which the Go test
// TestClassSpecsMatchBillingAndHost holds. This is the three the web can
// read. I-784 halved them, from 2 GB of the class's memory per vCPU to
// 4 GB; a page left at the old figure draws a machine's CPU against
// twice the vCPUs it has.
describe('the size classes', () => {
	const CLASSES = ['small', 'large', 'xl'];

	/** `case "small": return 1` out of a Go function body. */
	function goClassFunc(name: string): Record<string, number> {
		const body = schedulerSource.match(
			new RegExp(`func ${name}\\(class string\\) int \\{([\\s\\S]*?)\\n\\}`)
		);
		if (!body) throw new Error(`scheduler.go: no func ${name}(class string) int`);
		const out: Record<string, number> = {};
		for (const [, cls, n] of body[1].matchAll(/case "(\w+)":\s*\n\s*return (\d+)/g)) {
			out[cls] = Number(n);
		}
		const dflt = body[1].match(/default:\s*\n\s*return (\d+)/);
		if (!dflt) throw new Error('scheduler.go: ClassVCPU has no default');
		out.large = Number(dflt[1]);
		return out;
	}

	it('take their vCPUs from scheduler.ClassVCPU', () => {
		const go = goClassFunc('ClassVCPU');
		expect(Object.keys(go).sort()).toEqual([...CLASSES].sort());
		for (const c of CLASSES) expect(classVCPUs(c), c).toBe(go[c]);
		expect(classVCPUs('nonesuch')).toBe(0);
	});

	// The ratio is uniform across the three, which is the whole of I-784:
	// a host's placeable memory then fixes its guest-vCPU count whatever
	// mix of classes is sold, so two `small` commit what one `large` does.
	it('are 4 GB of memory per vCPU, uniformly', () => {
		for (const c of CLASSES) {
			expect(classMemoryGB(c) / classVCPUs(c), c).toBe(4);
		}
	});

	// /docs/machine is the table a user reads, and agents read the
	// markdown itself through llms.txt (I-660).
	it('match the table published in /docs/machine', () => {
		const md = readFileSync(join(__dirname, '..', 'content', 'docs', 'machine.md'), 'utf8');
		for (const c of CLASSES) {
			const row = md.split('\n').find((l) => l.startsWith(`| \`${c}\``));
			expect(row, `${c} row`).toBeDefined();
			const [, , vcpu, memory] = row!.split('|').map((x) => x.trim());
			expect(Number(vcpu), `${c} vCPU`).toBe(classVCPUs(c));
			expect(memory, `${c} memory`).toBe(`${classMemoryGB(c)} GB`);
		}
	});
});
