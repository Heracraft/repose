import { env } from '$env/dynamic/public';
import { apiResource } from '$lib/api/resource';
import { CALLBACK_PATH, SIGNIN_COOKIE, decodeFlow, tokenEndpoint } from '$lib/signin';
import type { PageServerLoad } from './$types';

// Completes a flow that `GET /signin` started (DECISIONS I-878). The
// verifier for such a flow is in an httpOnly cookie, so the browser's
// scripts cannot do the exchange: this does it and hands the page the two
// tokens the Logto browser client keeps, which the page writes into that
// client's own storage (lib/auth.svelte, adoptServerSession).
//
// A flow the bundle started carries no cookie and is not touched here:
// `adopt` is null and routes/callback/+page.svelte runs
// handleSignInCallback as before.

interface TokenResponse {
	id_token?: unknown;
	refresh_token?: unknown;
}

export const load: PageServerLoad = async ({ url, cookies, fetch, setHeaders }) => {
	const raw = cookies.get(SIGNIN_COOKIE);
	if (!raw) return { adopt: null };

	// A started flow is spent on its first callback whatever happens next,
	// so neither a code nor a verifier can be put through it twice.
	cookies.delete(SIGNIN_COOKIE, { path: CALLBACK_PATH });
	setHeaders({ 'cache-control': 'no-store' });

	const flow = decodeFlow(raw);
	const code = url.searchParams.get('code');
	const state = url.searchParams.get('state');
	if (!flow || !code || !state || state !== flow.state) return { adopt: null };

	const endpoint = env.PUBLIC_LOGTO_ENDPOINT ?? '';
	if (!endpoint) return { adopt: null };

	let body: TokenResponse;
	try {
		const res = await fetch(tokenEndpoint(endpoint), {
			method: 'POST',
			headers: { 'content-type': 'application/x-www-form-urlencoded' },
			body: new URLSearchParams({
				grant_type: 'authorization_code',
				code,
				code_verifier: flow.verifier,
				client_id: env.PUBLIC_LOGTO_APP_ID ?? '',
				redirect_uri: new URL(CALLBACK_PATH, url.origin).toString(),
				resource: apiResource()
			})
		});
		if (!res.ok) return { adopt: null };
		body = (await res.json()) as TokenResponse;
	} catch {
		// Logto unreachable or answering nonsense: the page says sign-in
		// failed, in the words it already uses.
		return { adopt: null };
	}

	const { id_token: idToken, refresh_token: refreshToken } = body;
	if (typeof idToken !== 'string' || typeof refreshToken !== 'string') return { adopt: null };
	if (!idToken || !refreshToken) return { adopt: null };

	return { adopt: { idToken, refreshToken, returnTo: flow.returnTo } };
};
