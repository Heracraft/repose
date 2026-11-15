// Google Tag Manager, the only third-party script the site loads
// (DECISIONS I-1214). The container is the owner's; what it fires (GA4
// today) is configured there, not here.
//
// Google's own snippet is two inline scripts in <head>. The app's CSP
// allows one inline script, app.html's first-paint theme script, by hash
// (svelte.config.js), so a second one would need its hash too and would
// change on every edit. This module does what the snippet's loader does
// instead: the data layer first, the `gtm.start` push that marks the
// container's start, then the async <script> for gtm.js. The noscript
// <iframe> of the snippet is left out: it would need frame-src to name
// googletagmanager.com, and a visitor with no scripts runs no tags.
//
// The host gate is the kill switch. The id below ships in the bundle, so
// the gate, not a missing environment variable, is what keeps the tag off
// every other copy of this build: the Playwright fixtures, `vite dev`,
// a preview on a Tailscale address, a local `node build`. Nobody here
// holds a Coolify API token, so a PUBLIC_* variable would have to be set
// by hand after a deploy, and the site would run without analytics until
// someone did.

import { browser, dev } from '$app/environment';

/** The owner's Tag Manager container. */
const CONTAINER = 'GTM-W463L575';

/** The only hostnames that load it. */
const HOSTS = ['repose.run', 'www.repose.run'];

type DataLayer = Record<string, unknown>[];

function dataLayer(): DataLayer {
	const w = window as unknown as { dataLayer?: DataLayer };
	return (w.dataLayer ??= []);
}

/** Whether this page is the live site, where the tag belongs. */
export function onLiveSite(): boolean {
	return browser && !dev && HOSTS.includes(window.location.hostname);
}

let started = false;

/**
 * Loads gtm.js once, on the live site only. Safe to call on every
 * navigation: the second call does nothing.
 */
export function start(): void {
	if (started || !onLiveSite()) return;
	started = true;
	dataLayer().push({ 'gtm.start': Date.now(), event: 'gtm.js' });
	const s = document.createElement('script');
	s.async = true;
	s.src = `https://www.googletagmanager.com/gtm.js?id=${CONTAINER}`;
	document.head.appendChild(s);
}

/**
 * A page view for a client-side navigation. The app is one page that
 * never reloads, so without this the container sees the first URL only.
 */
export function pageView(url: URL): void {
	if (!started) return;
	dataLayer().push({
		event: 'page_view',
		page_location: url.href,
		page_path: url.pathname,
		page_title: document.title
	});
}
