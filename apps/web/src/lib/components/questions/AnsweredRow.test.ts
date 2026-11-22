import { describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';

import AnsweredRow from './AnsweredRow.svelte';
import type { AnsweredRow as AnsweredRowData } from '$lib/question-view';

vi.stubGlobal(
	'ResizeObserver',
	class {
		observe() {}
		disconnect() {}
	}
);

function makeRow(over: Partial<AnsweredRowData['question']> = {}): AnsweredRowData {
	return {
		question: {
			id: 'q-ans-1',
			project_id: 'p1',
			project: 'recruiting-platform',
			agent: 'claude',
			window: 'claude',
			text: 'Should we run the migration now?',
			options: ['yes', 'no'],
			state: 'answered',
			answer: 'yes',
			answered_via: 'dashboard',
			created_at: '2026-11-19T10:00:00Z',
			expires_at: '2026-11-19T11:00:00Z',
			answered_at: '2026-11-19T10:02:00Z',
			...over
		},
		when: 'yesterday',
		answer: 'yes',
		rest: ''
	};
}

describe('AnsweredRow', () => {
	it('renders the full question text without a hover-only title attribute', () => {
		const target = document.createElement('ol');
		document.body.appendChild(target);
		const comp = mount(AnsweredRow, { target, props: { row: makeRow(), live: true } });
		flushSync();

		const text = target.querySelector<HTMLElement>('[data-testid="answered-text"]');
		expect(text).not.toBeNull();
		expect(text?.textContent?.trim()).toBe('Should we run the migration now?');
		expect(text?.getAttribute('title')).toBeNull();

		const link = target.querySelector<HTMLAnchorElement>('a.arow-project');
		expect(link?.textContent).toBe('recruiting-platform');

		unmount(comp);
		target.remove();
	});

	it('renders the project name unlinked when the project is no longer live', () => {
		const target = document.createElement('ol');
		document.body.appendChild(target);
		const comp = mount(AnsweredRow, { target, props: { row: makeRow(), live: false } });
		flushSync();

		expect(target.querySelector('a.arow-project')).toBeNull();
		expect(target.querySelector('span.arow-project')?.textContent).toBe('recruiting-platform');

		unmount(comp);
		target.remove();
	});
});
