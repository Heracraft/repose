import { describe, expect, it } from 'vitest';
import { billingMode, footerLines, planLine, type BillingFacts } from './billingMode';
import type { Billing, Me, Subscription } from './api/types';

// The rail footer and the phone's plan line, one row of ia-final 1.2 and
// 1.3 each (DECISIONS I-841).
const NOW = Date.parse('2026-10-21T12:00:00Z');

function me(status: Me['billing']['status']): Me {
	return { billing: { status } } as Me;
}

function sub(over: Partial<Subscription> = {}): Subscription {
	return {
		plan: 'pro',
		status: 'active',
		seats: 4,
		period_start: '2026-10-21T12:00:00Z',
		period_end: '2026-11-21T12:00:00Z',
		next_billed_at: '2026-11-21T12:00:00Z',
		trial_end: null,
		cancel_at: null,
		scheduled_plan: null,
		...over
	};
}

function billing(over: Partial<Billing> = {}): Billing {
	return {
		subscription: null,
		usage: { memory_gb: 32 } as Billing['usage'],
		plans: [
			{ id: 'solo', name: 'Solo', available: true },
			{ id: 'plus', name: 'Plus', available: true },
			{ id: 'pro', name: 'Pro', available: true }
		] as Billing['plans'],
		seats: { total: 30, held: 12, free: 18, waiting: 0 },
		waitlist: null,
		...over
	};
}

const facts = (m: Me, b: Billing): BillingFacts => ({ me: m, billing: b, off: false });

function view(f: BillingFacts, running = 28) {
	const mode = billingMode(f, NOW);
	const foot = footerLines(mode, f, running);
	const line = planLine(mode, f);
	return {
		mode,
		foot: foot
			? [foot.label, foot.name ?? '', foot.state, foot.link ? 'link' : '', foot.dot ?? '']
			: null,
		line: line ? `${line.text} / ${line.link}${line.dot ? ` (${line.dot})` : ''}` : null
	};
}

describe('billing mode, footer and plan line', () => {
	it('draws nothing while unknown or off', () => {
		expect(view({ off: false })).toEqual({ mode: 'unknown', foot: null, line: null });
		expect(view({ off: true })).toEqual({ mode: 'off', foot: null, line: null });
	});

	it('exempt: Running and the memory, no plan', () => {
		expect(view(facts(me('exempt'), billing()))).toEqual({
			mode: 'exempt',
			foot: ['Running', '', '28 GB', '', ''],
			line: null
		});
	});

	it('no plan', () => {
		expect(view(facts(me('none'), billing()))).toEqual({
			mode: 'none',
			foot: ['Plan', 'No plan', 'Choose a plan', 'link', ''],
			line: 'No plan / Choose a plan'
		});
	});

	it('on the waitlist, seats full, a held seat', () => {
		const full = billing({
			plans: billing().plans.map((p) => ({ ...p, available: false }))
		});
		expect(
			view(
				facts(me('none'), billing({ waitlist: { position: 3, joined_at: '2026-10-20T00:00:00Z' } }))
			)
		).toMatchObject({ mode: 'waitlist', line: 'On the waitlist / Billing' });
		expect(view(facts(me('none'), full))).toMatchObject({
			mode: 'full',
			foot: ['Plan', 'No plan', 'repose is full', 'link', '']
		});
		const held = billing({
			waitlist: {
				position: 1,
				joined_at: '2026-10-20T00:00:00Z',
				hold_until: '2026-10-23T12:00:00Z'
			}
		});
		expect(view(facts(me('none'), held))).toMatchObject({
			mode: 'held',
			foot: ['Plan', 'No plan', 'Seat held', 'link', ''],
			line: 'Seat held / Billing'
		});
		const expired = billing({
			waitlist: {
				position: 1,
				joined_at: '2026-10-20T00:00:00Z',
				hold_until: '2026-10-20T12:00:00Z'
			}
		});
		expect(view(facts(me('none'), expired)).mode).toBe('waitlist');
	});

	it('trial, active, over, cancelled, scheduled', () => {
		const trial = billing({
			subscription: sub({ plan: 'solo', status: 'trialing', trial_end: '2026-10-28T12:00:00Z' })
		});
		expect(view(facts(me('trial'), trial))).toEqual({
			mode: 'trial',
			foot: ['Plan', 'Solo', 'Trial until 2026-10-28', '', ''],
			line: null
		});
		const active = billing({ subscription: sub() });
		expect(view(facts(me('active'), active)).foot).toEqual([
			'Plan',
			'Pro',
			'28 GB of 32 GB running',
			'',
			''
		]);
		expect(view(facts(me('active'), active), 36).foot?.[2]).toBe('36 GB of 32 GB running · over');
		const cancelled = billing({ subscription: sub({ cancel_at: '2026-11-21T12:00:00Z' }) });
		expect(view(facts(me('active'), cancelled)).foot?.[2]).toBe('Ends 2026-11-21');
		const scheduled = billing({ subscription: sub({ scheduled_plan: 'plus' }) });
		expect(view(facts(me('active'), scheduled)).foot?.[2]).toBe('Changes to Plus 2026-11-21');
	});

	it('payment failed carries an amber dot, suspended a red one, both a link', () => {
		const pd = billing({ subscription: sub({ status: 'past_due' }) });
		expect(view(facts(me('past_due'), pd))).toEqual({
			mode: 'past_due',
			foot: ['Plan', 'Pro', 'Payment failed', 'link', 'warn'],
			line: 'Pro · Payment failed / Update card (warn)'
		});
		expect(view(facts(me('suspended'), pd))).toEqual({
			mode: 'suspended',
			foot: ['Plan', 'Pro', 'Suspended', 'link', 'err'],
			line: 'Suspended / Update card and pay (err)'
		});
	});
});
