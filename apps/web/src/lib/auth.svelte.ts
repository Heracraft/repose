// Logto SPA login (docs/workstreams/08-dashboard.md 5.1): authorization code
// with PKCE in the browser, access tokens for the api resource, refresh in
// the client. No server route holds a secret; the Logto app is a public
// client. One flow is the exception, and only one: a sign-in a visitor
// starts through `GET /signin` before this code has run, or without it
// (DECISIONS I-864, I-878). There the server holds the PKCE verifier, so
// the server does that one token exchange and hands the result to
// adoptServerSession below.
import { browser } from '$app/environment';
import { env } from '$env/dynamic/public';
import { goto } from '$app/navigation';
import { resolve } from '$app/paths';
import { toast } from 'svelte-sonner';
import LogtoClient, { BrowserStorage, PersistKey } from '@logto/browser';
import { apiResource } from '$lib/api/resource';

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

export function callbackUrl(): string {
	return `${location.origin}/callback`;
}

export async function signIn(): Promise<void> {
	await getClient().signIn(callbackUrl());
}

/** Said when a sign-in did not finish, wherever that is noticed. */
export const SIGN_IN_FAILED = 'Sign-in was cancelled or failed; try again.';

/** Guards a second client flow while the first is still starting. */
let signingIn = false;

/**
 * The click handler every sign-in link carries. The link's href is the
 * floor: it is `GET /signin`, which starts the flow on the server and so
 * works before this code has run and when it never runs at all (DECISIONS
 * I-864, I-878). Once it has run a plain left click takes the browser flow
 * instead, which costs no extra navigation and keeps the tokens the
 * server's business out of it. A middle click or a modified click is left
 * to the browser, which is what makes open-in-new-tab and copy-link work.
 */
export function onSignInLinkClick(event: MouseEvent): void {
	if (event.defaultPrevented) return;
	if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) {
		return;
	}
	event.preventDefault();
	if (signingIn) return;
	signingIn = true;
	void signIn().catch(() => {
		signingIn = false;
		toast.error(SIGN_IN_FAILED);
	});
}

/**
 * Takes over a sign-in that `GET /signin` started and
 * routes/callback/+page.server.ts completed, by writing the two tokens the
 * Logto browser client persists into that client's own storage
 * (`PersistKey`, `BrowserStorage`: both are its public API, so this does
 * not depend on how it lays its storage out). The client is rebuilt
 * afterwards so that no access token it cached for the session before this
 * one survives into it.
 *
 * The access token is deliberately not written: the client fetches one per
 * resource from the refresh token, which is what it does on every reload
 * anyway.
 */
export async function adoptServerSession(idToken: string, refreshToken: string): Promise<void> {
	const storage = new BrowserStorage(env.PUBLIC_LOGTO_APP_ID ?? '');
	await storage.setItem(PersistKey.IdToken, idToken);
	await storage.setItem(PersistKey.RefreshToken, refreshToken);
	client = undefined;
	authState.authenticated = await getClient().isAuthenticated();
	if (!authState.authenticated) throw new Error('the adopted session did not take');
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
