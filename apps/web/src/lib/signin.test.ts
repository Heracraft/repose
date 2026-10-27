import { describe, expect, it } from 'vitest';
import {
	DEFAULT_RETURN,
	RETURN_PATHS,
	authorizationUrl,
	decodeFlow,
	encodeFlow,
	safeReturnPath,
	signinHref,
	tokenEndpoint,
	type SignInFlow
} from './signin';

// DECISIONS I-878. safeReturnPath is the one security control on the sign-in
// route: /signin is a GET anyone can put in a link, so a return path taken on
// trust would make the site an open redirect, which is how a sign-in link on
// our own domain ends up phishing credentials. It answers with an allow-list
// of whole paths, the same RETURN_PATHS the browser flow uses (I-837).
describe('safeReturnPath', () => {
	it('keeps a path that is on the allow-list', () => {
		for (const path of RETURN_PATHS) expect(safeReturnPath(path)).toBe(path);
	});

	it('refuses anything else, off-origin or not', () => {
		for (const off of [
			'https://evil.example/',
			'http://evil.example/',
			'//evil.example/',
			'///evil.example/',
			'/\\evil.example/',
			'\\\\evil.example',
			'javascript:alert(1)',
			'data:text/html,<script>',
			'ftp://evil.example/',
			'//evil.example\\@repose.herakraft.co',
			'/billing/../../evil',
			'/billing?x=1',
			'/billing ',
			'/projects/secret',
			'/settings'
		]) {
			expect(safeReturnPath(off), off).toBe(DEFAULT_RETURN);
		}
	});

	it('falls back when nothing was asked for', () => {
		expect(safeReturnPath(null)).toBe(DEFAULT_RETURN);
		expect(safeReturnPath(undefined)).toBe(DEFAULT_RETURN);
		expect(safeReturnPath('')).toBe(DEFAULT_RETURN);
	});

	it('names only paths the app has', () => {
		// A path that no route serves would send a signed-in visitor to a
		// 404 the moment it was added to the list.
		for (const path of RETURN_PATHS) expect(path.startsWith('/')).toBe(true);
	});
});

describe('signinHref', () => {
	it('is the bare route with no return path', () => {
		expect(signinHref()).toBe('/signin');
	});

	it('names the return path in the query, encoded', () => {
		expect(signinHref('/billing')).toBe('/signin?return=%2Fbilling');
	});
});

describe('authorizationUrl', () => {
	it('carries the PKCE challenge, the state and the api resource', () => {
		const url = new URL(
			authorizationUrl({
				endpoint: 'https://logto.example',
				appId: 'app-1',
				redirectUri: 'https://repose.example/callback',
				codeChallenge: 'challenge-value',
				state: 'state-value',
				resource: 'https://api.repose.example'
			})
		);
		expect(url.origin + url.pathname).toBe('https://logto.example/oidc/auth');
		expect(url.searchParams.get('code_challenge')).toBe('challenge-value');
		expect(url.searchParams.get('code_challenge_method')).toBe('S256');
		expect(url.searchParams.get('state')).toBe('state-value');
		expect(url.searchParams.get('response_type')).toBe('code');
		expect(url.searchParams.get('client_id')).toBe('app-1');
		expect(url.searchParams.get('redirect_uri')).toBe('https://repose.example/callback');
		expect(url.searchParams.get('resource')).toBe('https://api.repose.example');
		// offline_access is what gets a refresh token, which is the whole
		// of what the browser client needs to carry the session on.
		expect(url.searchParams.get('scope')?.split(' ')).toContain('offline_access');
	});

	it('does not double the slash when the endpoint has a trailing one', () => {
		expect(tokenEndpoint('https://logto.example/')).toBe('https://logto.example/oidc/token');
	});
});

describe('the flow cookie', () => {
	it('round-trips a flow', () => {
		const flow: SignInFlow = { verifier: 'v'.repeat(43), state: 'st', returnTo: '/billing' };
		expect(decodeFlow(encodeFlow(flow))).toEqual(flow);
	});

	it('is url-safe, so a cookie value never needs quoting', () => {
		const encoded = encodeFlow({ verifier: 'a+b/c', state: 'x+y/z', returnTo: '/projects' });
		expect(encoded).toMatch(/^[A-Za-z0-9_-]+$/);
	});

	it('refuses anything that is not a flow', () => {
		expect(decodeFlow(undefined)).toBeUndefined();
		expect(decodeFlow('')).toBeUndefined();
		expect(decodeFlow('not base64 at all!!')).toBeUndefined();
		expect(
			decodeFlow(encodeFlow({ verifier: '', state: 's', returnTo: '/billing' }))
		).toBeUndefined();
		expect(decodeFlow(btoa('{"verifier":"v"}'))).toBeUndefined();
	});

	it('re-checks the return path on the way out, not only on the way in', () => {
		const tampered = btoa(
			JSON.stringify({ verifier: 'v', state: 's', returnTo: 'https://evil.example/' })
		);
		expect(decodeFlow(tampered)?.returnTo).toBe(DEFAULT_RETURN);
	});
});
