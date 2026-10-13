import { goto } from '$app/navigation';

// Settings holds two guards on a navigation away while something is
// unsaved: one for the ntfy URL and the Google Chat webhook, one for
// machine.nix (DECISIONS I-578, I-692). The guards run on the same navigation, so both ask at once; a Leave pressed in one
// has to pass the other's guard too. With a flag of its own per form, the
// other form cancelled that Leave and asked again, and its Leave was
// cancelled by the first, so the page never let go.
let leaving = false;

/** True while a Leave the user pressed is under way; a guard lets it pass. */
export function leavingAnyway(): boolean {
	return leaving;
}

/** Follow a navigation a guard held, past every guard on the page. */
export async function leaveAnyway(url: URL): Promise<void> {
	leaving = true;
	try {
		// eslint-disable-next-line svelte/no-navigation-without-resolve -- the URL came from SvelteKit's own navigation, already resolved
		await goto(url);
	} finally {
		leaving = false;
	}
}
