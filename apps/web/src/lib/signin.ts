// The sign-in flow's shared parts: where a sign-in may come back to, the
// href the sign-in links carry, the shape of the cookie that carries one
// flow's PKCE verifier from /signin to /callback, and the URL building.
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
import { resolve } from '$app/paths';

/** The query parameter `/signin` reads the post-sign-in path from. */
export const RETURN_PARAM = 'return';

/** Where sign-in lands when the caller names no return path. */
export const DEFAULT_RETURN = '/projects' as const;

/**
 * The pages a sign-in may come back to in place of {@link DEFAULT_RETURN}:
 * these paths and no others (Guide B 1.11). The landing's "Start a free
 * week" comes back to /billing, where the plans are. The browser flow
 * (lib/auth.svelte) and `GET /signin` share this one list, so neither can
 * send a visitor somewhere the other would not. `/signin` takes its
 * `?return=` from a URL, which is why it matches the whole value against
 * this list rather than checking the shape of whatever arrived.
 */
export const RETURN_PATHS = ['/billing'] as const;
export type ReturnPath = (typeof RETURN_PATHS)[number];

/** Everywhere a completed sign-in may land. */
export type SignInReturn = ReturnPath | typeof DEFAULT_RETURN;

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
	/** Where to send the browser once the flow completes. */
	returnTo: SignInReturn;
}

/**
 * The path a signed-in visitor is sent to, from whatever the request asked
 * for. Anything that is not one of {@link RETURN_PATHS} becomes
 * {@link DEFAULT_RETURN}.
 *
 * This is the one security-relevant decision in the flow: `/signin` is a
 * GET anyone can put in a link, so a return path taken on trust would make
 * the site an open redirect — a sign-in link on our own domain that hands
 * the visitor to an attacker's page the moment they authenticate, which is
 * how credentials get phished. An allow-list of whole paths, rather than a
 * check on the shape of an arbitrary one, leaves nothing to get wrong:
 * there is no parsing to outwit, and the set of destinations is the set a
 * reader can see in this file.
 */
export function safeReturnPath(raw: string | null | undefined): SignInReturn {
	return RETURN_PATHS.find((p) => p === raw) ?? DEFAULT_RETURN;
}

/**
 * Where a sign-in link points. `resolve` is what makes it right from a
 * page at any depth; the query parameter is read back by `GET /signin`
 * through safeReturnPath above.
 */
export function signinHref(returnTo?: ReturnPath): string {
	const base = resolve('/signin');
	return returnTo ? `${base}?${RETURN_PARAM}=${encodeURIComponent(returnTo)}` : base;
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
