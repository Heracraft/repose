import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import type { Project, ViewTicket } from '$lib/api/types';

// The api client reads PUBLIC_API_URL at request time, which no Vitest
// run serves; the test drives the mint itself.
const mintBrowserView = vi.fn<(id: string) => Promise<ViewTicket>>();
vi.mock('$lib/api/client', () => ({
	mintBrowserView: (id: string) => mintBrowserView(id),
	viewStreamUrl: (path: string) => `wss://api.test${path}`
}));

/** The sockets the component opened, newest last. */
const sockets: FakeSocket[] = [];

class FakeSocket extends EventTarget {
	closed = false;
	constructor(readonly url: string) {
		super();
		sockets.push(this);
	}
	close() {
		this.closed = true;
	}
	/** The api's close frame: status 1000 and an outcome word as its reason. */
	end(reason: string) {
		this.dispatchEvent(new CloseEvent('close', { code: 1000, reason }));
	}
}

/** The RFB objects the component made, newest last. */
const views: FakeRFB[] = [];

class FakeRFB extends EventTarget {
	/** Every event type the component listened for. */
	readonly listening: string[] = [];
	disconnected = false;
	constructor(
		readonly target: HTMLElement,
		readonly channel: WebSocket,
		readonly options: Record<string, unknown>
	) {
		super();
		views.push(this);
	}
	addEventListener(type: string, cb: EventListenerOrEventListenerObject | null): void {
		this.listening.push(type);
		super.addEventListener(type, cb);
	}
	disconnect() {
		this.disconnected = true;
	}
	focus() {}
}

vi.mock('@novnc/novnc', () => ({ default: FakeRFB }));

const { default: BrowserView } = await import('./BrowserView.svelte');

const project = { id: 'p1', slug: 'todo-app', state: 'running' } as Project;

const ticket: ViewTicket = {
	expires_at: new Date(Date.now() + 900_000).toISOString(),
	stream_path: '/v1/browser/stream',
	resting: false
};

let target: HTMLDivElement;
let view: Record<string, unknown> | undefined;

/** Mounts the tab's view and lets the mint and the import settle. */
async function open() {
	target = document.createElement('div');
	document.body.appendChild(target);
	view = mount(BrowserView, {
		target,
		props: { project, openSocket: (url: string) => new FakeSocket(url) as unknown as WebSocket }
	});
	flushSync();
	await vi.advanceTimersByTimeAsync(0);
}

const line = () => target.querySelector('.line')?.textContent ?? '';
const resume = () => target.querySelector<HTMLButtonElement>('button');

beforeEach(() => {
	vi.useFakeTimers();
	sockets.length = 0;
	views.length = 0;
	mintBrowserView.mockReset();
	mintBrowserView.mockResolvedValue({ ...ticket });
});

afterEach(() => {
	if (view) unmount(view);
	view = undefined;
	target?.remove();
	vi.useRealTimers();
});

// The Browser tab mints once per connect and draws noVNC itself; a close
// the api explained is not reconnected to, because a reconnect mints and
// a mint is where a frozen machine wakes (DECISIONS I-1216).
describe('BrowserView', () => {
	it('mints once, opens the stream at the api, and draws frames', async () => {
		await open();
		expect(mintBrowserView).toHaveBeenCalledTimes(1);
		expect(sockets[0].url).toBe('wss://api.test/v1/browser/stream');
		// No credential of any kind goes with the connection: the stream
		// arrives past SecurityResult.
		expect(views[0].options).toEqual({ credentials: {}, shared: true });
		expect(views[0].channel).toBe(sockets[0]);
		expect(line()).toBe('Connecting…');

		views[0].dispatchEvent(new CustomEvent('connect', { detail: {} }));
		flushSync();
		expect(target.querySelector('.over')).toBeNull();
	});

	it('reconnects three times after a transport error, minting each time', async () => {
		await open();
		views[0].dispatchEvent(new CustomEvent('connect', { detail: {} }));

		for (const [n, wait] of [1000, 2000, 4000].entries()) {
			const v = views[views.length - 1];
			// A transport error closes with no reason word of the api's.
			sockets[sockets.length - 1].end('');
			v.dispatchEvent(new CustomEvent('disconnect', { detail: { clean: false } }));
			flushSync();
			expect(line(), `after error ${n + 1}`).toBe('Connecting…');
			expect(mintBrowserView, `before wait ${n + 1}`).toHaveBeenCalledTimes(n + 1);
			await vi.advanceTimersByTimeAsync(wait);
			expect(mintBrowserView, `after wait ${n + 1}`).toHaveBeenCalledTimes(n + 2);
		}

		// The fourth failure stops: four mints, and the button is the way back.
		sockets[sockets.length - 1].end('');
		views[views.length - 1].dispatchEvent(new CustomEvent('disconnect', { detail: {} }));
		flushSync();
		await vi.advanceTimersByTimeAsync(10_000);
		expect(mintBrowserView).toHaveBeenCalledTimes(4);
		expect(line()).toBe('The view could not be reached.');
		expect(resume()?.textContent?.trim()).toBe('Resume view');
	});

	it('does not reconnect after an idle close, and resumes on the button', async () => {
		await open();
		views[0].dispatchEvent(new CustomEvent('connect', { detail: {} }));
		sockets[0].end('idle');
		views[0].dispatchEvent(new CustomEvent('disconnect', { detail: { clean: true } }));
		flushSync();
		await vi.advanceTimersByTimeAsync(60_000);
		expect(mintBrowserView).toHaveBeenCalledTimes(1);
		expect(line()).toContain('two minutes');

		resume()!.click();
		await vi.advanceTimersByTimeAsync(0);
		expect(mintBrowserView).toHaveBeenCalledTimes(2);
		expect(line()).toBe('Connecting…');
	});

	it('does not reconnect after the byte budget or the four-hour cap', async () => {
		for (const reason of ['budget', 'cap']) {
			await open();
			views[0].dispatchEvent(new CustomEvent('connect', { detail: {} }));
			sockets[0].end(reason);
			views[0].dispatchEvent(new CustomEvent('disconnect', { detail: {} }));
			flushSync();
			await vi.advanceTimersByTimeAsync(60_000);
			expect(mintBrowserView, reason).toHaveBeenCalledTimes(1);
			expect(resume()?.textContent?.trim(), reason).toBe('Resume view');
			unmount(view!);
			view = undefined;
			target.remove();
			mintBrowserView.mockClear();
			sockets.length = 0;
			views.length = 0;
		}
	});

	it('shows the desktop-off state for a machine the mint did not wake', async () => {
		mintBrowserView.mockResolvedValue({ ...ticket, resting: true });
		await open();
		expect(sockets).toHaveLength(0);
		expect(line()).toBe('todo-app is frozen, so its screen is off.');
		expect(resume()?.textContent?.trim()).toBe('Resume view');
	});

	it("shows the api's own sentence when the mint is refused", async () => {
		const { ApiError } = await import('$lib/api/errors');
		mintBrowserView.mockRejectedValue(
			new ApiError({ code: 'view_busy', message: 'todo-app already has a view open.' }, 409, null)
		);
		await open();
		expect(sockets).toHaveLength(0);
		expect(line()).toBe('todo-app already has a view open.');
	});

	// No clipboard bridge in either direction (I-1216): the guest's own
	// viewer page has one, and a guest the tenant has root on is what this
	// path does not trust.
	it('bridges no clipboard', async () => {
		await open();
		expect(views[0].listening).toEqual(['connect', 'disconnect']);
		// The grep docs/CHECKLIST.md's shape asks for: the word appears
		// nowhere in the view or its tab.
		const read = (p: string) => readFileSync(join(__dirname, p), 'utf8');
		for (const p of ['BrowserView.svelte', '../../../routes/projects/[id]/browser/+page.svelte']) {
			expect(/clipboard/i.test(read(p)), p).toBe(false);
		}
	});
});
