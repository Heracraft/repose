// The theme switch's state (DECISIONS I-837): System follows the computer,
// Light and Dark override it for this browser. app.html's inline script
// applies a stored choice before the first paint; this module keeps the
// page in step after that and tells the toasts which theme is showing.
//
// It touches no browser API at import: the root layout renders at
// prerender for the landing, the docs and the legal pages, where there is
// no window. init() runs from the layout's onMount.

export type ThemeChoice = 'system' | 'light' | 'dark';
export type ResolvedTheme = 'light' | 'dark';

const KEY = 'hk-theme';
/** The page colour per theme, which the browser's chrome takes too. */
const PAGE: Record<ResolvedTheme, string> = { light: '#fafafa', dark: '#09090b' };
const DARK_QUERY = '(prefers-color-scheme: dark)';

class Theme {
	choice = $state<ThemeChoice>('system');
	/** What the OS asks for, read in init(). */
	system = $state<ResolvedTheme>('light');
	resolved = $derived<ResolvedTheme>(this.choice === 'system' ? this.system : this.choice);
}

export const theme = new Theme();

function readStored(): ThemeChoice {
	try {
		const t = localStorage.getItem(KEY);
		return t === 'light' || t === 'dark' ? t : 'system';
	} catch {
		return 'system';
	}
}

/** Paints the browser chrome: one meta per scheme, each its scheme's page
 *  colour under System, both the chosen page colour otherwise. */
function paintChrome(choice: ThemeChoice): void {
	for (const meta of document.querySelectorAll<HTMLMetaElement>('meta[name="theme-color"]')) {
		const scheme: ResolvedTheme = (meta.getAttribute('media') ?? '').includes('dark')
			? 'dark'
			: 'light';
		meta.content = PAGE[choice === 'system' ? scheme : choice];
	}
}

/** Reads the stored choice and follows the OS setting. Returns a cleanup. */
export function init(): () => void {
	theme.choice = readStored();
	let mq: MediaQueryList | undefined;
	const onChange = (e: MediaQueryListEvent) => (theme.system = e.matches ? 'dark' : 'light');
	try {
		mq = window.matchMedia(DARK_QUERY);
		theme.system = mq.matches ? 'dark' : 'light';
		mq.addEventListener('change', onChange);
	} catch {
		theme.system = 'light';
	}
	return () => {
		mq?.removeEventListener('change', onChange);
	};
}

/** Applies and stores a choice: System clears both the key and data-theme. */
export function setTheme(choice: ThemeChoice): void {
	theme.choice = choice;
	try {
		if (choice === 'system') localStorage.removeItem(KEY);
		else localStorage.setItem(KEY, choice);
	} catch {
		// Storage blocked (a private window, a policy): the choice holds for
		// this page view only.
	}
	const root = document.documentElement;
	if (choice === 'system') delete root.dataset.theme;
	else root.dataset.theme = choice;
	paintChrome(choice);
}
