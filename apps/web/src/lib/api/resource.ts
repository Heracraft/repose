import { env } from '$env/dynamic/public';

/**
 * The api's resource identifier: what an access token is asked for and what
 * the api checks the audience against. It lives apart from the Logto client
 * (lib/auth.svelte) because the sign-in server route needs it too and must
 * not pull the browser client in (DECISIONS I-878).
 *
 * `PUBLIC_API_RESOURCE` names it outright, and production sets it: Logto
 * identifies a resource by an opaque string, not by where the api answers,
 * so it stayed `https://api.repose.herakraft.co` when the api moved to
 * `api.repose.run` (DECISIONS I-1156, I-1158). Unset, it falls back to
 * `PUBLIC_API_URL` without its `/v1` suffix, which is what it was before
 * the two could differ, so a fork or a dev deploy needs one value, not two.
 */
export function apiResource(): string {
	const named = (env.PUBLIC_API_RESOURCE ?? '').trim();
	if (named !== '') return named.replace(/\/$/, '');
	return (env.PUBLIC_API_URL ?? '').replace(/\/v1\/?$/, '');
}
