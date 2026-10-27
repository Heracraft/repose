import { env } from '$env/dynamic/public';

/**
 * The api's resource identifier: PUBLIC_API_URL without its /v1 suffix.
 * What an access token is asked for and what the api checks the audience
 * against. It lives apart from the Logto client (lib/auth.svelte) because
 * the sign-in server route needs it too and must not pull the browser
 * client in (DECISIONS I-878).
 */
export function apiResource(): string {
	return (env.PUBLIC_API_URL ?? '').replace(/\/v1\/?$/, '');
}
