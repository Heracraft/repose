// The rules the Browser tab's connect follows (DECISIONS I-1216), kept
// out of the component so they are tested without a browser.
//
// The api is on the byte path and owns three bounds: 120 s with no frame,
// four hours, and 2 GB on one ticket. Which one fired comes back as the
// close frame's reason word, a closed set the api writes and this page
// reads (docs/interfaces/api.md, "The machine's browser view").

/** The close frame's reason words (api.md). */
export type ViewClose = 'closed' | 'idle' | 'cap' | 'budget' | 'too_large' | 'error';

/**
 * The stream's WebSocket URL: the api's origin and the path the mint
 * answered with. `PUBLIC_API_URL` carries /v1 and `stream_path` is
 * absolute, so the path replaces it rather than being appended.
 */
export function viewStreamUrl(apiUrl: string, streamPath: string): string {
	if (!apiUrl || !streamPath) return '';
	const u = new URL(streamPath, apiUrl);
	u.protocol = u.protocol === 'http:' ? 'ws:' : 'wss:';
	return u.toString();
}

/** The waits before each reconnect, in order. A fourth failure stops. */
export const VIEW_RETRIES = [1000, 2000, 4000] as const;

/** What the page does when a stream ends, and the line it shows. */
export interface ViewEnd {
	/** Connect again (a fresh mint), while a retry is left. */
	retry: boolean;
	/** Why it stopped, when the reason is one the reader can act on. */
	note?: string;
}

/**
 * A stream that ended, by its close reason. The page reconnects after a
 * transport error (no reason reached it) and after `closed`, which is the
 * guest's own desktop going away and comes back when it is asked for
 * again; it never reconnects after `idle`, `cap` or `budget`, because
 * every connect mints, and a mint is where a wake can happen (I-1216).
 */
export function viewEnded(reason: string | undefined): ViewEnd {
	switch (reason) {
		case 'idle':
			return { retry: false, note: 'The view stopped after two minutes with a still screen.' };
		case 'cap':
			return { retry: false, note: 'The view stopped after four hours.' };
		case 'budget':
			return { retry: false, note: 'The view has carried its 2 GB.' };
		case 'closed':
		case '':
		case undefined:
			return { retry: true };
		default:
			return { retry: false, note: 'The view stopped.' };
	}
}
