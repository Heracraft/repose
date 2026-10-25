import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

// The theme switch's module (DECISIONS I-837). The root layout renders at
// prerender with no window, so importing it must touch no browser API;
// setTheme stores the choice, sets data-theme and the chrome colour, and
// survives a browser that refuses storage.

function metas() {
	document.head.innerHTML =
		'<meta name="theme-color" content="#fafafa" media="(prefers-color-scheme: light)">' +
		'<meta name="theme-color" content="#09090b" media="(prefers-color-scheme: dark)">';
	return [...document.querySelectorAll<HTMLMetaElement>('meta[name="theme-color"]')];
}

describe('theme', () => {
	beforeEach(() => {
		vi.resetModules();
		localStorage.clear();
		delete document.documentElement.dataset.theme;
	});
	afterEach(() => {
		vi.unstubAllGlobals();
		vi.restoreAllMocks();
	});

	it('imports with no window, document or storage', async () => {
		vi.stubGlobal('window', undefined);
		vi.stubGlobal('document', undefined);
		vi.stubGlobal('localStorage', undefined);
		const mod = await import('./theme.svelte');
		expect(mod.theme.choice).toBe('system');
		expect(mod.theme.resolved).toBe('light');
	});

	it('stores Dark and Light, and System clears both', async () => {
		const [light, dark] = metas();
		const { setTheme, theme } = await import('./theme.svelte');
		setTheme('dark');
		expect(localStorage.getItem('hk-theme')).toBe('dark');
		expect(document.documentElement.dataset.theme).toBe('dark');
		expect(theme.resolved).toBe('dark');
		expect([light.content, dark.content]).toEqual(['#09090b', '#09090b']);
		setTheme('light');
		expect(localStorage.getItem('hk-theme')).toBe('light');
		expect(document.documentElement.dataset.theme).toBe('light');
		expect([light.content, dark.content]).toEqual(['#fafafa', '#fafafa']);
		setTheme('system');
		expect(localStorage.getItem('hk-theme')).toBeNull();
		expect(document.documentElement.dataset.theme).toBeUndefined();
		expect([light.content, dark.content]).toEqual(['#fafafa', '#09090b']);
	});

	it('reads the stored choice and follows the computer under System', async () => {
		localStorage.setItem('hk-theme', 'dark');
		let listener: ((e: MediaQueryListEvent) => void) | undefined;
		vi.stubGlobal('matchMedia', () => ({
			matches: false,
			addEventListener: (_: string, fn: (e: MediaQueryListEvent) => void) => (listener = fn),
			removeEventListener: () => {}
		}));
		const { init, setTheme, theme } = await import('./theme.svelte');
		const stop = init();
		expect(theme.choice).toBe('dark');
		expect(theme.resolved).toBe('dark');
		setTheme('system');
		expect(theme.resolved).toBe('light');
		listener?.({ matches: true } as MediaQueryListEvent);
		expect(theme.resolved).toBe('dark');
		stop();
	});

	it('still applies the choice when storage throws', async () => {
		metas();
		vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
			throw new Error('blocked');
		});
		vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
			throw new Error('blocked');
		});
		const { init, setTheme, theme } = await import('./theme.svelte');
		expect(() => init()).not.toThrow();
		expect(theme.choice).toBe('system');
		expect(() => setTheme('dark')).not.toThrow();
		expect(document.documentElement.dataset.theme).toBe('dark');
		expect(theme.resolved).toBe('dark');
	});
});
