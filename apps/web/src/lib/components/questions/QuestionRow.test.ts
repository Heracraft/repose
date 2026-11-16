import { describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';

import QuestionRow from './QuestionRow.svelte';
import type { Question } from '$lib/api/types';

// jsdom draws no layout: the row's measuring needs these two to exist.
vi.stubGlobal('matchMedia', () => ({ matches: false }));
vi.stubGlobal(
	'ResizeObserver',
	class {
		observe() {}
		disconnect() {}
	}
);

const NOW = new Date(2026, 10, 16, 12, 0, 0).getTime();

function question(over: Partial<Question> = {}): Question {
	return {
		id: 'q1',
		project_id: 'p1',
		project: 'mailflare',
		agent: 'claude',
		window: 'claude',
		text: 'What should the table be called?',
		options: [],
		state: 'pending',
		answer: null,
		answered_via: null,
		created_at: new Date(NOW - 60_000).toISOString(),
		expires_at: new Date(NOW + 30 * 60_000).toISOString(),
		answered_at: null,
		...over
	};
}

function draw(q: Question) {
	const target = document.createElement('div');
	document.body.appendChild(target);
	const onhold = vi.fn();
	const row = mount(QuestionRow, {
		target,
		props: { question: q, now: NOW, onanswer: () => {}, ondismiss: () => {}, onhold }
	});
	flushSync();
	const field = target.querySelector<HTMLTextAreaElement>('textarea');
	return {
		onhold,
		field,
		type(v: string) {
			field!.value = v;
			field!.dispatchEvent(new Event('input', { bubbles: true }));
			flushSync();
		},
		done() {
			unmount(row);
			target.remove();
		}
	};
}

// The row says the owner is at its field, once per change, so the card
// keeps the question open while they type (I-1242).
describe('QuestionRow holding', () => {
	it('holds while the field has focus, and lets go on blur with nothing typed', () => {
		const r = draw(question());
		expect(r.field).not.toBeNull();
		expect(r.onhold).not.toHaveBeenCalled();
		r.field!.focus();
		flushSync();
		expect(r.onhold).toHaveBeenCalledTimes(1);
		expect(r.onhold).toHaveBeenLastCalledWith(true);
		r.field!.blur();
		flushSync();
		expect(r.onhold).toHaveBeenCalledTimes(2);
		expect(r.onhold).toHaveBeenLastCalledWith(false);
		r.done();
	});

	it('keeps holding after blur while a draft is in the field', () => {
		const r = draw(question());
		r.field!.focus();
		flushSync();
		r.type('sessions');
		r.field!.blur();
		flushSync();
		expect(r.onhold).toHaveBeenCalledTimes(1);
		expect(r.onhold).toHaveBeenLastCalledWith(true);
		r.type('   ');
		expect(r.onhold).toHaveBeenCalledTimes(2);
		expect(r.onhold).toHaveBeenLastCalledWith(false);
		r.done();
	});

	it('lets go when the row leaves while held', () => {
		const r = draw(question());
		r.field!.focus();
		flushSync();
		expect(r.onhold).toHaveBeenLastCalledWith(true);
		r.done();
		expect(r.onhold).toHaveBeenCalledTimes(2);
		expect(r.onhold).toHaveBeenLastCalledWith(false);
	});

	it('shows the expiry the question carries, in its last ten minutes', () => {
		const r = draw(question({ expires_at: new Date(NOW + 4 * 60_000).toISOString() }));
		expect(document.body.textContent).toContain('expires in 4m');
		r.done();
	});
});
