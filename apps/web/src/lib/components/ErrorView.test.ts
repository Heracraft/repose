import { describe, expect, it, vi } from 'vitest';
import { createRawSnippet, flushSync, mount, unmount } from 'svelte';
vi.mock('$lib/auth.svelte', () => ({ authState: { authenticated: false } }));
vi.mock('$app/paths', () => ({ resolve: (p: string) => p }));

import AppBoundary from './AppBoundary.svelte';
import ErrorView from './ErrorView.svelte';

// A page that throws while it renders shows the 500 page inside the root
// layout's boundary, with exactly one <main id="main"> (the skip link's
// target), not a skeleton that never resolves (I-837).
describe('the error boundary', () => {
	it('renders ErrorView with one main when a page throws', () => {
		const target = document.createElement('div');
		document.body.appendChild(target);
		const children = createRawSnippet(() => ({
			render: () => {
				throw new Error('render failed');
			}
		}));
		const app = mount(AppBoundary, { target, props: { children } });
		flushSync();
		expect(target.querySelectorAll('main#main')).toHaveLength(1);
		expect(target.querySelector('h1')?.textContent).toBe('Something went wrong');
		expect(target.querySelector('.hk-eyebrow')?.textContent).toBe('500');
		expect(target.querySelector('button')?.textContent).toBe('Reload');
		expect(target.textContent).not.toContain('render failed');
		unmount(app);
		target.remove();
	});

	it('offers the docs and home to a signed-out visitor', () => {
		const target = document.createElement('div');
		const view = mount(ErrorView, { target, props: { status: 404, title: 'Not found' } });
		flushSync();
		expect([...target.querySelectorAll('a')].map((a) => a.textContent)).toEqual(['Docs', 'Home']);
		unmount(view);
	});
});
