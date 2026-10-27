import { describe, expect, it } from 'vitest';
import {
	DEFAULT_RETURN,
	authorizationUrl,
	decodeFlow,
	encodeFlow,
	safeReturnPath,
	tokenEndpoint
} from './signin';

// DECISIONS I-878. safeReturnPath is the one security control on the
// sign-in route: /signin is a GET anyone can put in a link, so a return
// path it did not check would make the site an open redirect, which is how
// a sign-in link on our own domain ends up phishing credentials.
describe('safeReturnPath', () => {
	it('keeps a same-origin path, with its query and fragment', () => {
		expect(safeReturnPath('/projects')).toBe('/projects');
		expect(safeReturnPath('/projects?filter=running')).toBe('/projects?filter=running');
		expect(safeReturnPath('/settings#machine-nix')).toBe('/settings#machine-nix');
	});

	it('refuses a path that points off-origin', () => {
		for (const off of [
			'https://evil.example/',
			'http://evil.example/',
			'//evil.example/',
			'///evil.example/',
			'/\\evil.example/',
			'/\\/evil.example',
			'\\\\evil.example',
			'javascript:alert(1)',
			'data:text/html,<script>',
			'ftp://evil.example/',
			'//evil.example\\@repose.herakraft.co'
		]) {
			expect(safeReturnPath(off), off).toBe(DEFAULT_RETURN);
		}
	});

	it('refuses whitespace and control characters, which a Location header cannot carry', () => {
		expect(safeReturnPath('/projects\r\nLocation: https://evil.example')).toBe(DEFAULT_RETURN);
		expect(safeReturnPath('/pro jects')).toBe(DEFAULT_RETURN);
		expect(safeReturnPath('/\tprojects')).toBe(DEFAULT_RETURN);
	});

	it('falls back when nothing was asked for', () => {
		expect(safeReturnPath(null)).toBe(DEFAULT_RETURN);
		expect(safeReturnPath(undefined)).toBe(DEFAULT_RETURN);
		expect(safeReturnPath('')).toBe(DEFAULT_RETURN);
		expect(safeReturnPath('projects')).toBe(DEFAULT_RETURN);
	});

	it('leaves an encoded slash encoded, so it stays one path segment', () => {
		expect(safeReturnPath('/%2F%2Fevil.example')).toBe('/%2F%2Fevil.example');
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
		const flow = { verifier: 'v'.repeat(43), state: 'st', returnTo: '/projects' };
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
		expect(decodeFlow(encodeFlow({ verifier: '', state: 's', returnTo: '/' }))).toBeUndefined();
		expect(decodeFlow(btoa('{"verifier":"v"}'))).toBeUndefined();
	});

	it('re-checks the return path on the way out, not only on the way in', () => {
		const tampered = btoa(
			JSON.stringify({ verifier: 'v', state: 's', returnTo: 'https://evil.example/' })
		);
		expect(decodeFlow(tampered)?.returnTo).toBe(DEFAULT_RETURN);
	});
});
