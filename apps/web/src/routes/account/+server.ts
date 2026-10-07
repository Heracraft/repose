import { redirect } from '@sveltejs/kit';

// The Account page is the first section of Settings now (DECISIONS I-578).
// Emails, bookmarks and older docs still link to /account, so it answers a
// permanent redirect. It names no fragment, so the page opens at its top
// with the account section under the title, and an old /account#machine-nix
// keeps its fragment (a browser carries it over a Location without one).
// A server route, not a page: the client router has no page here, so a
// link followed inside the app loads it from the server too. The app has
// no base path, and resolve() on the server gives a relative one.
export function GET() {
	redirect(308, '/settings');
}
