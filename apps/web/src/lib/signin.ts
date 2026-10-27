// The sign-in flow's shared, side-effect-free parts: the href the sign-in
// links carry, the shape of the cookie that carries one flow's PKCE
// verifier from /signin to /callback, and the two pieces of URL building.
//
// DECISIONS I-864, I-878. The landing page is prerendered, so its primary
// call to action has to work before the bundle has hydrated and when the
// bundle never arrives. `GET /signin` starts the OIDC flow on the server,
// which is the only way an `<a href>` can start it: the client flow in
// `auth.svelte.ts` builds its authorization URL in the browser, so until
// that code has run there is no URL to put in an href.
//
// Nothing here reads a secret. The Logto app is a public client (PKCE, no
// client secret), and the three values the flow needs are the same
// PUBLIC_* ones the browser already has.

/** The query parameter `/signin` reads the post-sign-in path from. */
export const RETURN_PARAM = 'return';

/** Where sign-in lands when the caller names no return path. */
export const DEFAULT_RETURN = '/projects';

/** The path Logto is told to come back to, and the only path the cookie below is sent to. */
export const CALLBACK_PATH = '/callback';

/** The cookie that carries one flow's verifier, state and return path. */
export const SIGNIN_COOKIE = 'repose_signin';

/** How long a started flow stays completable, in seconds. */
export const SIGNIN_COOKIE_MAX_AGE = 600;

/** What the cookie holds. The verifier never reaches the browser's scripts. */
export interface SignInFlow {
	/** The PKCE code verifier, kept on the server side of the cookie. */
	verifier: string;
	/** The `state` the authorization request carried. */
	state: string;
	/** Where to send the browser once the flow completes; always same-origin. */
	returnTo: string;
}

/**
 * The path a signed-in visitor is sent to, from whatever the request asked
 * for. Anything that is not a plain same-origin path becomes
 * {@link DEFAULT_RETURN}.
 *
 * This is the one security-relevant decision in the flow: `/signin` is a
 * GET anyone can link to, so an unchecked return path would make the site
 * an open redirect — a sign-in link on our own domain that hands the
 * visitor to an attacker's page the moment they authenticate, which is how
 * credentials get phished. The check is an allow-list of shapes, not a
 * deny-list of hosts: one leading slash, no authority, no backslash and no
 * whitespace or control character, and then the parse against a base that
 * can never resolve has to come back on that same base.
 */
export function safeReturnPath(raw: string | null | undefined): string {
	if (!raw) return DEFAULT_RETURN;
	// "//host" and "/\host" are both read as an authority by browsers.
	if (!raw.startsWith('/') || raw.startsWith('//') || raw.startsWith('/\\')) return DEFAULT_RETURN;
	// No whitespace, no backslash and no control character: none of the
	// three belongs in a path, and a control character in a Location
	// header is a header-splitting attempt rather than a return path.
	if (/[\s\\]/u.test(raw) || /\p{Cc}/u.test(raw)) return DEFAULT_RETURN;

	const base = 'https://return.invalid';
	let parsed: URL;
	try {
		parsed = new URL(raw, base);
	} catch {
		return DEFAULT_RETURN;
	}
	if (parsed.origin !== base) return DEFAULT_RETURN;
	return parsed.pathname + parsed.search + parsed.hash;
}

/** Logto's authorization endpoint for an instance at `endpoint`. */
export function authorizationEndpoint(endpoint: string): string {
	return `${endpoint.replace(/\/+$/, '')}/oidc/auth`;
}

/** Logto's token endpoint for an instance at `endpoint`. */
export function tokenEndpoint(endpoint: string): string {
	return `${endpoint.replace(/\/+$/, '')}/oidc/token`;
}

/**
 * The authorization request, in the same parameters `@logto/js`'s
 * `generateSignInUri` sends for the browser flow (scopes, `resource`,
 * `prompt=consent`), so a visitor who arrives through this route and one
 * who arrives through the bundle reach the same Logto page.
 */
export function authorizationUrl(params: {
	endpoint: string;
	appId: string;
	redirectUri: string;
	codeChallenge: string;
	state: string;
	resource: string;
}): string {
	const query = new URLSearchParams({
		client_id: params.appId,
		redirect_uri: params.redirectUri,
		response_type: 'code',
		scope: 'openid offline_access profile email',
		prompt: 'consent',
		state: params.state,
		code_challenge: params.codeChallenge,
		code_challenge_method: 'S256'
	});
	query.append('resource', params.resource);
	return `${authorizationEndpoint(params.endpoint)}?${query.toString()}`;
}

/** The cookie value for a started flow. */
export function encodeFlow(flow: SignInFlow): string {
	return btoa(JSON.stringify(flow)).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

/** The flow a cookie value holds, or undefined if it is not one. */
export function decodeFlow(raw: string | undefined): SignInFlow | undefined {
	if (!raw) return undefined;
	try {
		const json: unknown = JSON.parse(atob(raw.replace(/-/g, '+').replace(/_/g, '/')));
		if (typeof json !== 'object' || json === null) return undefined;
		const { verifier, state, returnTo } = json as Record<string, unknown>;
		if (typeof verifier !== 'string' || typeof state !== 'string') return undefined;
		if (!verifier || !state) return undefined;
		return {
			verifier,
			state,
			returnTo: safeReturnPath(typeof returnTo === 'string' ? returnTo : null)
		};
	} catch {
		return undefined;
	}
}
