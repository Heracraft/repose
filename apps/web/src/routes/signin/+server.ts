import { createHash, randomBytes } from 'node:crypto';
import { error } from '@sveltejs/kit';
import { env } from '$env/dynamic/public';
import { apiResource } from '$lib/api/resource';
import {
	CALLBACK_PATH,
	RETURN_PARAM,
	SIGNIN_COOKIE,
	SIGNIN_COOKIE_MAX_AGE,
	authorizationUrl,
	encodeFlow,
	safeReturnPath
} from '$lib/signin';
import type { RequestHandler } from './$types';

// `GET /signin` starts the OIDC flow on the server and redirects to Logto
// (DECISIONS I-864, I-878). It exists so the landing page's primary call to
// action can be an `<a href>`: the page is prerendered, and a click that
// lands between first paint and hydration used to do nothing at all, with
// no feedback. A link also works when the bundle is blocked, fails or
// times out, which is the visitor this was losing.
//
// The PKCE verifier is generated here and never reaches the browser's
// scripts: it goes into an httpOnly cookie that is only sent to
// /callback, which is where the token exchange happens
// (routes/callback/+page.server.ts). The Logto app is a public client, so
// nothing here is a secret — the three PUBLIC_* values are the ones the
// bundle already carries.

function b64url(bytes: Buffer): string {
	return bytes.toString('base64url');
}

export const GET: RequestHandler = ({ url, cookies }) => {
	const endpoint = env.PUBLIC_LOGTO_ENDPOINT ?? '';
	const appId = env.PUBLIC_LOGTO_APP_ID ?? '';
	if (!endpoint || !appId) {
		error(503, 'Sign-in is not configured.');
	}

	const verifier = b64url(randomBytes(32));
	const codeChallenge = b64url(createHash('sha256').update(verifier).digest());
	const state = b64url(randomBytes(16));
	const returnTo = safeReturnPath(url.searchParams.get(RETURN_PARAM));
	// The origin SvelteKit resolved for this request, which behind Traefik
	// is the site's own. A forged Host header can only make this a
	// redirect_uri Logto has not registered, which Logto refuses: it
	// cannot send the visitor anywhere else.
	const redirectUri = new URL(CALLBACK_PATH, url.origin).toString();

	cookies.set(SIGNIN_COOKIE, encodeFlow({ verifier, state, returnTo }), {
		// Only /callback ever needs it, so no other request carries it.
		path: CALLBACK_PATH,
		httpOnly: true,
		// The site is https everywhere, including the dev box, which is
		// reached through `tailscale serve` because PKCE needs a secure
		// context. Browsers accept a Secure cookie from loopback, which is
		// where the Playwright fixtures run.
		secure: true,
		// Logto comes back as a top-level cross-site navigation, which Lax
		// sends the cookie on and Strict would not.
		sameSite: 'lax',
		maxAge: SIGNIN_COOKIE_MAX_AGE
	});

	// Not a thrown redirect(): this response must never be cached or
	// replayed, since it carries one flow's state and its Set-Cookie.
	return new Response(null, {
		status: 303,
		headers: {
			location: authorizationUrl({
				endpoint,
				appId,
				redirectUri,
				codeChallenge,
				state,
				resource: apiResource()
			}),
			'cache-control': 'no-store'
		}
	});
};
