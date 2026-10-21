// Where an old dashboard URL lives now (DECISIONS I-841). The dashboard is
// a client-only SPA, so a fragment never reaches the server: the root
// layout asks this table after every navigation, the first one included,
// and replaces the history entry with the answer, so Back never bounces.
// /account keeps its server 308 to /settings (I-578); nothing new answers
// a 308 from /settings, since browsers cache the old one.
//
// Pure, so every row is tested without a browser (legacyUrls.test.ts).

/** The Settings sections that became tabs. */
const SETTINGS_TABS: Record<string, string> = {
	'#machine-nix': '/settings/machine-nix',
	'#secrets': '/settings/secrets',
	'#notifications': '/settings/notifications'
};

/** The project page's old section fragments that became tabs. */
const PROJECT_TABS: Record<string, string> = {
	'#events': 'events',
	'#events-card': 'events',
	'#usage': 'usage',
	'#usage-card': 'usage',
	'#snapshots': 'snapshots',
	'#snapshots-card': 'snapshots'
};

/** /projects/<id> and nothing under it. */
const PROJECT = /^\/projects\/([^/]+)\/?$/;

/**
 * The path, query and fragment an old URL now lives at, or null when the
 * URL is current. Only the path, search and hash are read; the answer is
 * a same-origin path for goto().
 */
export function legacyTarget(url: URL): string | null {
	const path = url.pathname;

	if (path === '/settings' || path === '/settings/') {
		return SETTINGS_TABS[url.hash] ?? null;
	}

	const m = PROJECT.exec(path);
	if (!m) return null;
	const base = `/projects/${m[1]}`;

	// The events pager's keys (I-662) on the bare project path: the pager
	// is on the Events tab now, with the same keys.
	const eventsKeys = [...url.searchParams.keys()].some((k) => k.startsWith('events_'));
	const tab = PROJECT_TABS[url.hash];
	if (eventsKeys && (tab === undefined || tab === 'events')) {
		return `${base}/events${url.search}`;
	}
	if (tab) return `${base}/${tab}${url.search}`;
	if (url.hash === '#secrets') return `${base}/config#secrets`;
	return null;
}
