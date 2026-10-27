import { createHash } from 'node:crypto';
import { describe, expect, it, vi } from 'vitest';

// The route reads the three PUBLIC_* values at request time; under Vitest
// there is no SvelteKit server to serve them.
vi.mock('$env/dynamic/public', () => ({
	env: {
		PUBLIC_LOGTO_ENDPOINT: 'https://logto.test',
		PUBLIC_LOGTO_APP_ID: 'app-under-test',
		PUBLIC_API_URL: 'https://api.test/v1'
	}
}));

const { GET } = await import('./+server');
const { SIGNIN_COOKIE, decodeFlow } = await import('$lib/signin');

interface SetCookie {
	name: string;
	value: string;
	options: Record<string, unknown>;
}

function get(target: string): { response: Response; cookies: SetCookie[] } {
	const cookies: SetCookie[] = [];
	const response = GET({
		url: new URL(target),
		cookies: {
			set: (name: string, value: string, options: Record<string, unknown>) =>
				cookies.push({ name, value, options })
		}
	} as never) as Response;
	return { response, cookies };
}

function b64url(bytes: Buffer): string {
	return bytes.toString('base64url');
}

// DECISIONS I-864, I-878: the landing page's call to action is an <a href>
// to this route, so a press before the bundle has hydrated, or with no
// bundle at all, still starts sign-in.
describe('GET /signin', () => {
	it('redirects to the identity provider with a PKCE challenge and a state', () => {
		const { response, cookies } = get('https://repose.test/signin');

		expect(response.status).toBe(303);
		expect(response.headers.get('cache-control')).toBe('no-store');

		const location = new URL(response.headers.get('location') ?? '');
		expect(location.origin + location.pathname).toBe('https://logto.test/oidc/auth');
		expect(location.searchParams.get('response_type')).toBe('code');
		expect(location.searchParams.get('client_id')).toBe('app-under-test');
		expect(location.searchParams.get('redirect_uri')).toBe('https://repose.test/callback');
		expect(location.searchParams.get('resource')).toBe('https://api.test');
		expect(location.searchParams.get('code_challenge_method')).toBe('S256');

		const challenge = location.searchParams.get('code_challenge') ?? '';
		const state = location.searchParams.get('state') ?? '';
		expect(challenge).toMatch(/^[A-Za-z0-9_-]{43}$/);
		expect(state).toMatch(/^[A-Za-z0-9_-]{16,}$/);

		// The cookie holds the verifier for that very challenge, which is
		// what makes this a real PKCE pair rather than two unrelated
		// strings, and the same state, which is what binds the callback to
		// the browser that started the flow.
		expect(cookies).toHaveLength(1);
		expect(cookies[0].name).toBe(SIGNIN_COOKIE);
		const flow = decodeFlow(cookies[0].value);
		expect(flow?.state).toBe(state);
		expect(
			b64url(
				createHash('sha256')
					.update(flow?.verifier ?? '')
					.digest()
			)
		).toBe(challenge);
	});

	it('keeps the verifier out of reach of the page, and only sends it to the callback', () => {
		const { response, cookies } = get('https://repose.test/signin');
		const { options } = cookies[0];
		expect(options.httpOnly).toBe(true);
		expect(options.secure).toBe(true);
		expect(options.sameSite).toBe('lax');
		expect(options.path).toBe('/callback');
		expect(options.maxAge).toBe(600);
		// Belt and braces: the verifier is nowhere in the redirect either.
		const flow = decodeFlow(cookies[0].value);
		expect(response.headers.get('location')).not.toContain(flow?.verifier ?? 'unset');
	});

	it('refuses a return path that is not on the allow-list', () => {
		for (const off of [
			'https%3A%2F%2Fevil.example%2F',
			'%2F%2Fevil.example%2F',
			'%2F%5Cevil.example',
			'%2Fsettings'
		]) {
			const { cookies } = get(`https://repose.test/signin?return=${off}`);
			expect(decodeFlow(cookies[0].value)?.returnTo, off).toBe('/projects');
		}
	});

	it('keeps a return path that is on the allow-list', () => {
		const { cookies } = get('https://repose.test/signin?return=%2Fbilling');
		expect(decodeFlow(cookies[0].value)?.returnTo).toBe('/billing');
	});

	it('starts a flow of its own every time', () => {
		const first = decodeFlow(get('https://repose.test/signin').cookies[0].value);
		const second = decodeFlow(get('https://repose.test/signin').cookies[0].value);
		expect(first?.verifier).not.toBe(second?.verifier);
		expect(first?.state).not.toBe(second?.state);
	});
});
