// Logto SPA login (docs/workstreams/08-dashboard.md 5.1): authorization code
// with PKCE entirely in the browser, access tokens for the api resource,
// refresh in the client. No server route ever sees a token or a secret.
import { browser } from '$app/environment';
import { env } from '$env/dynamic/public';
import { goto } from '$app/navigation';
import { resolve } from '$app/paths';
import { toast } from 'svelte-sonner';
import LogtoClient from '@logto/browser';

/** The api's resource identifier: PUBLIC_API_URL without its /v1 suffix. */
export function apiResource(): string {
	return (env.PUBLIC_API_URL ?? '').replace(/\/v1\/?$/, '');
}

let client: LogtoClient | undefined;

function getClient(): LogtoClient {
	if (!browser) throw new Error('the Logto client only runs in the browser');
	if (!client) {
		client = new LogtoClient({
			endpoint: env.PUBLIC_LOGTO_ENDPOINT ?? '',
			appId: env.PUBLIC_LOGTO_APP_ID ?? '',
			resources: [apiResource()],
			scopes: ['openid', 'profile', 'email', 'offline_access']
		});
	}
	return client;
}

class AuthState {
	// undefined until the first isAuthenticated() check resolves, so the
	// layout can show nothing (rather than flash the landing page) while
	// Logto reads its stored refresh token.
	authenticated = $state<boolean | undefined>(undefined);
}

export const authState = new AuthState();

export async function initAuth(): Promise<void> {
	if (!browser) return;
	authState.authenticated = await getClient().isAuthenticated();
}

/**
 * The signed-in user's email from the ID token Logto already holds, with no
 * request: the Account menu's first line. Undefined when the token has
 * none or cannot be read.
 */
export async function signedInEmail(): Promise<string | undefined> {
	if (!browser) return undefined;
	try {
		const claims = await getClient().getIdTokenClaims();
		return typeof claims.email === 'string' && claims.email ? claims.email : undefined;
	} catch {
		return undefined;
	}
}

export function callbackUrl(): string {
	return `${location.origin}/callback`;
}

/**
 * The pages a sign-in may come back to in place of /projects: same-origin
 * paths named here, never one read from a URL (Guide B 1.11). The landing's
 * "Start a free week" comes back to /billing, where the plans are.
 */
const RETURN_PATHS = ['/billing'] as const;
export type ReturnPath = (typeof RETURN_PATHS)[number];
const RETURN_KEY = 'repose-return-to';

export async function signIn(returnTo?: ReturnPath): Promise<void> {
	try {
		if (returnTo && RETURN_PATHS.includes(returnTo)) sessionStorage.setItem(RETURN_KEY, returnTo);
		else sessionStorage.removeItem(RETURN_KEY);
	} catch {
		// No storage: the sign-in comes back to /projects.
	}
	await getClient().signIn(callbackUrl());
}

/** The page the sign-in asked to come back to, once; undefined for /projects. */
export function takeReturnPath(): ReturnPath | undefined {
	try {
		const v = sessionStorage.getItem(RETURN_KEY);
		sessionStorage.removeItem(RETURN_KEY);
		return RETURN_PATHS.find((p) => p === v);
	} catch {
		return undefined;
	}
}

export async function handleSignInCallback(url: string): Promise<void> {
	await getClient().handleSignInCallback(url);
	authState.authenticated = true;
}

/**
 * Sends the browser to Logto's end-session page, which comes back to /.
 * authState stays as it is until the browser leaves (DECISIONS I-331):
 * flipping it first re-renders the page signed out and sends it to / for a
 * moment before the navigation, a visible flash on every sign-out. Only if
 * the redirect cannot start does the page sign out locally.
 */
export async function signOut(): Promise<void> {
	try {
		await getClient().signOut(`${location.origin}/`);
	} catch {
		authState.authenticated = false;
		await goto(resolve('/'));
	}
}

/**
 * Signs the user out locally and sends them to the landing page without a
 * round trip to Logto's own end-session endpoint, for the "refresh failed"
 * failure mode (08-dashboard.md 6): the refresh token is already dead, so
 * there is nothing for Logto to revoke.
 */
async function forceSignOutExpired(): Promise<void> {
	authState.authenticated = false;
	toast.error('Session expired, sign in again.');
	await goto(resolve('/'));
}

/** Used by the api client. Throws if the user is signed out or refresh failed. */
export async function getAccessToken(): Promise<string> {
	try {
		const token = await getClient().getAccessToken(apiResource());
		if (!token) throw new Error('no access token');
		return token;
	} catch (err) {
		await forceSignOutExpired();
		throw err;
	}
}
